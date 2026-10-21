package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/artifact"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type editSessionResponse struct {
	EditSessionID  string `json:"edit_session_id"`
	ArtifactID     string `json:"artifact_id"`
	BaseRevisionID string `json:"base_revision_id"`
	FencingToken   int64  `json:"fencing_token"`
	LeaseToken     string `json:"lease_token,omitempty"`
	LeaseExpiresAt string `json:"lease_expires_at"`
	EditorType     string `json:"editor_type"`
	State          string `json:"state"`
	Editable       bool   `json:"editable"`
	DraftContent   string `json:"draft_content,omitempty"`
}

// CreateArtifactEditSession — POST /api/artifacts/{artifactID}/edit-sessions
//
// Opens a lease for a markdown, text, JSON, CSV, or DOCX head. The caller must
// already be allowed to read the artifact. XLSX and PPTX stay read only.
func (h *Handler) CreateArtifactEditSession(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	art, ok := h.loadArtifactForMember(w, r)
	if !ok {
		return
	}
	actor, editorType, ok := editorIdentity(w, r)
	if !ok {
		return
	}
	if !art.HeadRevisionID.Valid {
		writeErrorCode(w, http.StatusUnsupportedMediaType, artifact.CodeNotEditable, "artifact is not editable")
		return
	}
	rev, err := h.Queries.GetArtifactRevision(r.Context(), db.GetArtifactRevisionParams{
		ID:         art.HeadRevisionID,
		ArtifactID: art.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load revision")
		return
	}
	textOK := artifactTextEditable(rev.MediaType, rev.Filename)
	docxOK := artifactDocxEditable(rev.MediaType, rev.Filename)
	if !textOK && !docxOK {
		writeErrorCode(w, http.StatusUnsupportedMediaType, artifact.CodeNotEditable, "artifact is not editable")
		return
	}
	if docxOK {
		if rev.SizeBytes > artifact.MaxOfficeInputBytes {
			writeErrorCode(w, http.StatusUnsupportedMediaType, artifact.CodeOfficeFeatureBlocked, "docx feature is not edited")
			return
		}
		body, err := h.artifactBlobBytes(r.Context(), rev.BlobID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load revision")
			return
		}
		reason, err := artifact.DocxEditBlock(body)
		if err != nil || reason != "" {
			writeErrorCode(w, http.StatusUnsupportedMediaType, artifact.CodeOfficeFeatureBlocked, "docx feature is not edited")
			return
		}
	}
	session, err := h.artifactService().OpenEditSession(r.Context(), artifact.OpenEditSessionInput{
		WorkspaceID: uuid.UUID(art.WorkspaceID.Bytes),
		ArtifactID:  uuid.UUID(art.ID.Bytes),
		ActorID:     actor,
		EditorType:  editorType,
	})
	if err != nil {
		writeArtifactServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, editSessionJSON(session, true))
}

// HeartbeatArtifactEditSession — POST /api/artifact-edit-sessions/{sessionID}/heartbeat
func (h *Handler) HeartbeatArtifactEditSession(w http.ResponseWriter, r *http.Request) {
	lease, _, ok := h.readEditProof(w, r, false)
	if !ok {
		return
	}
	session, err := h.artifactService().HeartbeatEditSession(r.Context(), lease)
	if err != nil {
		writeArtifactServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, editSessionJSON(session, false))
}

// SaveArtifactEditDraft — PUT /api/artifact-edit-sessions/{sessionID}/draft
func (h *Handler) SaveArtifactEditDraft(w http.ResponseWriter, r *http.Request) {
	lease, art, ok := h.readEditProof(w, r, true)
	if !ok {
		return
	}
	if docx, err := h.artifactHeadIsDocx(r, art); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load revision")
		return
	} else if docx {
		writeError(w, http.StatusBadRequest, "a docx draft is not stored")
		return
	}
	content, ok := editDraftContent(w, r)
	if !ok {
		return
	}
	session, err := h.artifactService().SaveEditDraft(r.Context(), uuid.UUID(art.WorkspaceID.Bytes), lease, content)
	if err != nil {
		writeArtifactServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, editSessionJSON(session, false))
}

func (h *Handler) readEditProof(w http.ResponseWriter, r *http.Request, keepBody bool) (artifact.EditLease, db.Artifact, bool) {
	if _, ok := requireUserID(w, r); !ok {
		return artifact.EditLease{}, db.Artifact{}, false
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "sessionID"), "edit session id")
	if !ok {
		return artifact.EditLease{}, db.Artifact{}, false
	}
	row, err := h.Queries.GetArtifactEditSession(r.Context(), sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "artifact not found")
		return artifact.EditLease{}, db.Artifact{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load edit session")
		return artifact.EditLease{}, db.Artifact{}, false
	}
	art, ok := h.artifactVisible(w, r, row.ArtifactID)
	if !ok {
		return artifact.EditLease{}, db.Artifact{}, false
	}
	actor, _, ok := editorIdentity(w, r)
	if !ok {
		return artifact.EditLease{}, db.Artifact{}, false
	}
	var body struct {
		LeaseToken   string `json:"lease_token"`
		FencingToken int64  `json:"fencing_token"`
		Content      string `json:"content"`
	}
	limit := int64(4096)
	if keepBody {
		limit = artifact.MaxEditDraftBytes + 4096
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, limit))
	if err != nil || int64(len(raw)) >= limit && keepBody {
		writeError(w, http.StatusRequestEntityTooLarge, "draft is too large")
		return artifact.EditLease{}, db.Artifact{}, false
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid edit session request")
		return artifact.EditLease{}, db.Artifact{}, false
	}
	if strings.TrimSpace(body.LeaseToken) == "" || body.FencingToken <= 0 {
		writeError(w, http.StatusBadRequest, "lease token and fencing token are required")
		return artifact.EditLease{}, db.Artifact{}, false
	}
	if keepBody {
		r.Body = io.NopCloser(strings.NewReader(body.Content))
	}
	return artifact.EditLease{
		SessionID:    uuid.UUID(sessionID.Bytes),
		ActorID:      actor,
		FencingToken: body.FencingToken,
		TokenHash:    artifact.HashLeaseToken(strings.TrimSpace(body.LeaseToken)),
	}, art, true
}

func editDraftContent(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	content, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid edit session request")
		return nil, false
	}
	if len(content) > artifact.MaxEditDraftBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "draft is too large")
		return nil, false
	}
	if !utf8.Valid(content) {
		writeError(w, http.StatusBadRequest, "draft must be text")
		return nil, false
	}
	return content, true
}

func (h *Handler) artifactVisible(w http.ResponseWriter, r *http.Request, artifactID pgtype.UUID) (db.Artifact, bool) {
	art, err := h.Queries.GetArtifactByID(r.Context(), artifactID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "artifact not found")
		return db.Artifact{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load artifact")
		return db.Artifact{}, false
	}
	if !h.authorizeArtifactWorkspace(w, r, uuidToString(art.WorkspaceID)) {
		return db.Artifact{}, false
	}
	if taskID, _, scoped := taskTokenBinding(r); scoped && !artifactOwnedByTask(art, taskID) {
		writeError(w, http.StatusNotFound, "artifact not found")
		return db.Artifact{}, false
	}
	return art, true
}

func editorIdentity(w http.ResponseWriter, r *http.Request) (uuid.UUID, string, bool) {
	if _, agentID, scoped := taskTokenBinding(r); scoped {
		id, err := uuid.Parse(agentID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "agent id is invalid")
			return uuid.Nil, "", false
		}
		return id, "agent", true
	}
	id, err := uuid.Parse(requestUserID(r))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "user not authenticated")
		return uuid.Nil, "", false
	}
	return id, "human", true
}

func submittedEditLease(w http.ResponseWriter, r *http.Request) (artifact.EditLease, bool, bool) {
	sessionRaw := strings.TrimSpace(r.FormValue("edit_session_id"))
	fencingRaw := strings.TrimSpace(r.FormValue("fencing_token"))
	token := strings.TrimSpace(r.FormValue("lease_token"))
	if sessionRaw == "" && fencingRaw == "" && token == "" {
		return artifact.EditLease{}, false, true
	}
	if sessionRaw == "" || fencingRaw == "" || token == "" {
		writeError(w, http.StatusBadRequest, "edit session, fencing token, and lease token are required")
		return artifact.EditLease{}, false, false
	}
	sessionID, err := uuid.Parse(sessionRaw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "edit_session_id is invalid")
		return artifact.EditLease{}, false, false
	}
	fencing, err := strconv.ParseInt(fencingRaw, 10, 64)
	if err != nil || fencing <= 0 {
		writeError(w, http.StatusBadRequest, "fencing_token is invalid")
		return artifact.EditLease{}, false, false
	}
	return artifact.EditLease{
		SessionID:    sessionID,
		FencingToken: fencing,
		TokenHash:    artifact.HashLeaseToken(token),
	}, true, true
}

func ifMatchAgrees(r *http.Request, base string) bool {
	match := strings.TrimSpace(r.Header.Get("If-Match"))
	if match == "" {
		return true
	}
	match = strings.TrimPrefix(match, "W/")
	match = strings.Trim(strings.TrimSpace(match), `"`)
	return strings.EqualFold(match, strings.TrimSpace(base))
}

func artifactDocxEditable(mediaType, filename string) bool {
	format, ok := artifact.OfficeFormat(mediaType, filename)
	return ok && format == artifact.FormatDocx
}

func (h *Handler) artifactHeadIsDocx(r *http.Request, art db.Artifact) (bool, error) {
	if !art.HeadRevisionID.Valid {
		return false, nil
	}
	rev, err := h.Queries.GetArtifactRevision(r.Context(), db.GetArtifactRevisionParams{
		ID:         art.HeadRevisionID,
		ArtifactID: art.ID,
	})
	if err != nil {
		return false, err
	}
	return artifactDocxEditable(rev.MediaType, rev.Filename), nil
}

func (h *Handler) artifactBlobBytes(ctx context.Context, blobID pgtype.UUID) ([]byte, error) {
	if h.Storage == nil {
		return nil, errors.New("storage not configured")
	}
	blob, err := h.Queries.GetArtifactBlob(ctx, blobID)
	if err != nil {
		return nil, err
	}
	reader, err := h.Storage.GetReader(ctx, blob.StorageKey)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(io.LimitReader(reader, artifact.MaxOfficeInputBytes+1))
}

func artifactTextEditable(mediaType, filename string) bool {
	if artifactMediaIsActive(mediaType, filename) {
		return false
	}
	switch fileExt(filename) {
	case "md", "markdown", "txt", "json", "csv":
		return true
	}
	switch canonicalMediaType(mediaType) {
	case "text/markdown", "text/x-markdown", "text/plain", "application/json", "text/csv":
		return true
	}
	return false
}

func editSessionJSON(session artifact.EditSession, includeToken bool) editSessionResponse {
	resp := editSessionResponse{
		EditSessionID:  session.ID.String(),
		ArtifactID:     session.ArtifactID.String(),
		BaseRevisionID: session.BaseRevisionID.String(),
		FencingToken:   session.FencingToken,
		LeaseExpiresAt: session.LeaseExpiresAt.UTC().Format(time.RFC3339Nano),
		EditorType:     session.EditorType,
		State:          session.State,
		Editable:       true,
		DraftContent:   string(session.Draft),
	}
	if includeToken {
		resp.LeaseToken = session.LeaseToken
	}
	return resp
}
