package handler

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/artifact"
	"github.com/multica-ai/multica/server/internal/storage"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const artifactListLimit int32 = 50

// artifactResponse is the public metadata shape. It has no storage key.
type artifactResponse struct {
	ID             string `json:"id"`
	WorkspaceID    string `json:"workspace_id"`
	OwnerScope     string `json:"owner_scope"`
	OwnerID        string `json:"owner_id"`
	Title          string `json:"title"`
	Kind           string `json:"kind"`
	Status         string `json:"status"`
	HeadRevisionID string `json:"head_revision_id,omitempty"`
	RevisionNo     *int64 `json:"revision_no,omitempty"`
	MediaType      string `json:"media_type,omitempty"`
	Filename       string `json:"filename,omitempty"`
	SizeBytes      *int64 `json:"size_bytes,omitempty"`
	UpdatedBy      string `json:"updated_by,omitempty"`
	RevisionID     string `json:"revision_id,omitempty"`
	OperationID    string `json:"operation_id,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	RowVersion     int64  `json:"row_version"`
}

type artifactRevisionResponse struct {
	ID          string `json:"id"`
	ArtifactID  string `json:"artifact_id"`
	RevisionNo  int64  `json:"revision_no"`
	MediaType   string `json:"media_type"`
	Filename    string `json:"filename"`
	SizeBytes   int64  `json:"size_bytes"`
	Source      string `json:"source"`
	ActorID     string `json:"actor_id"`
	OperationID string `json:"operation_id,omitempty"`
	CreatedAt   string `json:"created_at"`
}

type artifactPreviewResponse struct {
	ArtifactID         string `json:"artifact_id"`
	RevisionID         string `json:"revision_id"`
	Filename           string `json:"filename"`
	MediaType          string `json:"media_type"`
	SizeBytes          int64  `json:"size_bytes"`
	RevisionNo         int64  `json:"revision_no"`
	PreviewKind        string `json:"preview_kind"`
	ContentPath        string `json:"content_path"`
	ContentDisposition string `json:"content_disposition"`
	StaticPreviewPath  string `json:"static_preview_path,omitempty"`
	// ActiveContent stays false. Static preview is a separate sanitized
	// document. This flag does not enable scripts.
	ActiveContent bool `json:"active_content"`
}

// ListArtifacts — GET /api/artifacts?owner_scope=&owner_id=
func (h *Handler) ListArtifacts(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return
	}
	if !h.authorizeArtifactWorkspace(w, r, workspaceID) {
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("owner_scope"))
	if taskID, _, scoped := taskTokenBinding(r); scoped {
		// A task token may list only the artifacts owned by its own task.
		ownerRaw := strings.TrimSpace(r.URL.Query().Get("owner_id"))
		if scope != "task" || taskID == "" || !strings.EqualFold(ownerRaw, taskID) {
			writeError(w, http.StatusNotFound, "artifact not found")
			return
		}
	}
	switch scope {
	case "workspace", "issue", "task":
	default:
		writeError(w, http.StatusBadRequest, "invalid owner_scope")
		return
	}
	ownerID, ok := parseUUIDOrBadRequest(w, r.URL.Query().Get("owner_id"), "owner_id")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	rows, err := h.Queries.ListArtifactsByOwner(r.Context(), db.ListArtifactsByOwnerParams{
		WorkspaceID: wsUUID,
		OwnerScope:  scope,
		OwnerID:     ownerID,
		Lim:         artifactListLimit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list artifacts")
		return
	}
	items := make([]artifactResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, artifactFromListRow(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"artifacts": items})
}

// GetArtifact — GET /api/artifacts/{artifactID}
func (h *Handler) GetArtifact(w http.ResponseWriter, r *http.Request) {
	art, ok := h.loadArtifactForMember(w, r)
	if !ok {
		return
	}
	resp := artifactFromModel(art)
	revID := art.HeadRevisionID
	if raw := strings.TrimSpace(r.URL.Query().Get("revision_id")); raw != "" {
		parsed, ok := parseUUIDOrBadRequest(w, raw, "revision_id")
		if !ok {
			return
		}
		revID = parsed
	}
	if revID.Valid {
		rev, err := h.Queries.GetArtifactRevision(r.Context(), db.GetArtifactRevisionParams{
			ID:         revID,
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
		fillArtifactRevision(&resp, rev)
	}
	writeJSON(w, http.StatusOK, resp)
}

// ListArtifactRevisions — GET /api/artifacts/{artifactID}/revisions
func (h *Handler) ListArtifactRevisions(w http.ResponseWriter, r *http.Request) {
	art, ok := h.loadArtifactForMember(w, r)
	if !ok {
		return
	}
	rows, err := h.Queries.ListArtifactRevisions(r.Context(), art.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list revisions")
		return
	}
	items := make([]artifactRevisionResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, artifactRevisionFromList(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"revisions": items})
}

// GetArtifactRevisionPreview — GET /api/artifacts/{artifactID}/revisions/{revisionID}/preview
//
// Returns a preview entry. The body is JSON, never the file. HTML and SVG
// are classified as download so the client does not render them as a document.
func (h *Handler) GetArtifactRevisionPreview(w http.ResponseWriter, r *http.Request) {
	_, rev, ok := h.loadArtifactRevisionForMember(w, r)
	if !ok {
		return
	}
	kind, disposition := classifyArtifactPreview(rev.MediaType, rev.Filename)
	resp := artifactPreviewResponse{
		ArtifactID:         uuidToString(rev.ArtifactID),
		RevisionID:         uuidToString(rev.ID),
		Filename:           rev.Filename,
		MediaType:          rev.MediaType,
		SizeBytes:          rev.SizeBytes,
		RevisionNo:         rev.RevisionNo,
		PreviewKind:        kind,
		ContentPath:        artifactContentPath(uuidToString(rev.ArtifactID), uuidToString(rev.ID)),
		ContentDisposition: disposition,
		ActiveContent:      false,
	}
	if (kind == "html" || kind == "svg") && rev.SizeBytes <= artifact.MaxStaticPreviewBytes {
		resp.StaticPreviewPath = artifactStaticPreviewPath(uuidToString(rev.ArtifactID), uuidToString(rev.ID))
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetArtifactRevisionContent — GET /api/artifacts/{artifactID}/revisions/{revisionID}/content
//
// Authenticated private download. Range follows the attachment proxy. HTML
// and SVG are forced to an attachment octet-stream so a browser navigation
// does not execute them.
func (h *Handler) GetArtifactRevisionContent(w http.ResponseWriter, r *http.Request) {
	_, rev, ok := h.loadArtifactRevisionForMember(w, r)
	if !ok {
		return
	}
	if h.Storage == nil {
		writeFeatureDisabled(w, "storage_not_configured", "storage not configured")
		return
	}
	blob, err := h.Queries.GetArtifactBlob(r.Context(), rev.BlobID)
	if err != nil {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	reader, err := h.Storage.GetReader(r.Context(), blob.StorageKey)
	if err != nil {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	defer reader.Close()

	contentType := rev.MediaType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	disposition := storage.ContentDisposition(contentType, rev.Filename)
	if artifactMediaIsActive(rev.MediaType, rev.Filename) {
		contentType = "application/octet-stream"
		disposition = storage.AttachmentContentDisposition(rev.Filename)
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	} else {
		h.setAttachmentPreviewSecurityHeaders(w)
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if seeker, ok := reader.(io.ReadSeeker); ok {
		http.ServeContent(w, r, rev.Filename, time.Time{}, seeker)
		return
	}
	h.serveProxyRange(w, r, db.Attachment{
		ID:          rev.ID,
		ContentType: contentType,
		Filename:    rev.Filename,
		SizeBytes:   rev.SizeBytes,
	}, reader)
}

func (h *Handler) loadArtifactForMember(w http.ResponseWriter, r *http.Request) (db.Artifact, bool) {
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "artifactID"), "artifact id")
	if !ok {
		return db.Artifact{}, false
	}
	art, err := h.Queries.GetArtifactByID(r.Context(), id)
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

func (h *Handler) loadArtifactRevisionForMember(w http.ResponseWriter, r *http.Request) (db.Artifact, db.GetArtifactRevisionRow, bool) {
	art, ok := h.loadArtifactForMember(w, r)
	if !ok {
		return db.Artifact{}, db.GetArtifactRevisionRow{}, false
	}
	revID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "revisionID"), "revision id")
	if !ok {
		return db.Artifact{}, db.GetArtifactRevisionRow{}, false
	}
	rev, err := h.Queries.GetArtifactRevision(r.Context(), db.GetArtifactRevisionParams{
		ID:         revID,
		ArtifactID: art.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "artifact not found")
		return db.Artifact{}, db.GetArtifactRevisionRow{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load revision")
		return db.Artifact{}, db.GetArtifactRevisionRow{}, false
	}
	return art, rev, true
}

func (h *Handler) authorizeArtifactWorkspace(w http.ResponseWriter, r *http.Request, workspaceID string) bool {
	userID, ok := requireUserID(w, r)
	if !ok {
		return false
	}
	if workspaceID == "" {
		writeError(w, http.StatusNotFound, "artifact not found")
		return false
	}
	if h.MembershipCache != nil && h.MembershipCache.Get(r.Context(), userID, workspaceID) {
		return true
	}
	if _, err := h.getWorkspaceMember(r.Context(), userID, workspaceID); err != nil {
		writeError(w, http.StatusNotFound, "artifact not found")
		return false
	}
	if h.MembershipCache != nil {
		h.MembershipCache.Set(r.Context(), userID, workspaceID)
	}
	return true
}

func artifactFromListRow(row db.ListArtifactsByOwnerRow) artifactResponse {
	resp := artifactResponse{
		ID:          uuidToString(row.ID),
		WorkspaceID: uuidToString(row.WorkspaceID),
		OwnerScope:  row.OwnerScope,
		OwnerID:     uuidToString(row.OwnerID),
		Title:       row.Title,
		Kind:        row.Kind,
		Status:      row.Status,
		CreatedAt:   formatPGTime(row.CreatedAt),
		UpdatedAt:   formatPGTime(row.UpdatedAt),
		RowVersion:  row.RowVersion,
	}
	if row.HeadRevisionID.Valid {
		resp.HeadRevisionID = uuidToString(row.HeadRevisionID)
	}
	if row.RevisionNo.Valid {
		n := row.RevisionNo.Int64
		resp.RevisionNo = &n
	}
	if row.MediaType.Valid {
		resp.MediaType = row.MediaType.String
	}
	if row.Filename.Valid {
		resp.Filename = row.Filename.String
	}
	if row.SizeBytes.Valid {
		n := row.SizeBytes.Int64
		resp.SizeBytes = &n
	}
	if row.UpdatedBy.Valid {
		resp.UpdatedBy = uuidToString(row.UpdatedBy)
	}
	return resp
}

func artifactFromModel(art db.Artifact) artifactResponse {
	resp := artifactResponse{
		ID:          uuidToString(art.ID),
		WorkspaceID: uuidToString(art.WorkspaceID),
		OwnerScope:  art.OwnerScope,
		OwnerID:     uuidToString(art.OwnerID),
		Title:       art.Title,
		Kind:        art.Kind,
		Status:      art.Status,
		CreatedAt:   formatPGTime(art.CreatedAt),
		UpdatedAt:   formatPGTime(art.UpdatedAt),
		RowVersion:  art.RowVersion,
	}
	if art.HeadRevisionID.Valid {
		resp.HeadRevisionID = uuidToString(art.HeadRevisionID)
	}
	return resp
}

func fillArtifactRevision(resp *artifactResponse, rev db.GetArtifactRevisionRow) {
	n := rev.RevisionNo
	size := rev.SizeBytes
	resp.RevisionNo = &n
	resp.MediaType = rev.MediaType
	resp.Filename = rev.Filename
	resp.SizeBytes = &size
	resp.UpdatedBy = uuidToString(rev.ActorID)
	resp.RevisionID = uuidToString(rev.ID)
	if rev.CommitOperationID.Valid {
		resp.OperationID = uuidToString(rev.CommitOperationID)
	}
}

func artifactRevisionFromList(row db.ListArtifactRevisionsRow) artifactRevisionResponse {
	return artifactRevisionResponse{
		ID:          uuidToString(row.ID),
		ArtifactID:  uuidToString(row.ArtifactID),
		RevisionNo:  row.RevisionNo,
		MediaType:   row.MediaType,
		Filename:    row.Filename,
		SizeBytes:   row.SizeBytes,
		Source:      row.Source,
		ActorID:     uuidToString(row.ActorID),
		OperationID: uuidToString(row.CommitOperationID),
		CreatedAt:   formatPGTime(row.CreatedAt),
	}
}

func taskTokenBinding(r *http.Request) (taskID, agentID string, scoped bool) {
	if r.Header.Get("X-Actor-Source") != "task_token" {
		return "", "", false
	}
	return strings.TrimSpace(r.Header.Get("X-Task-ID")), strings.TrimSpace(r.Header.Get("X-Agent-ID")), true
}

func artifactOwnedByTask(art db.Artifact, taskID string) bool {
	return taskID != "" && art.OwnerScope == "task" && strings.EqualFold(uuidToString(art.OwnerID), taskID)
}

func formatPGTime(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.UTC().Format(time.RFC3339Nano)
}

func artifactContentPath(artifactID, revisionID string) string {
	return "/api/artifacts/" + artifactID + "/revisions/" + revisionID + "/content"
}

func artifactStaticPreviewPath(artifactID, revisionID string) string {
	return "/api/artifacts/" + artifactID + "/revisions/" + revisionID + "/static-preview"
}

func classifyArtifactPreview(mediaType, filename string) (kind, disposition string) {
	if kind, ok := artifact.StaticPreviewKind(mediaType, filename); ok {
		return kind, "attachment"
	}
	mt := canonicalMediaType(mediaType)
	ext := fileExt(filename)
	switch {
	case mt == "application/pdf" || ext == "pdf":
		return "pdf", "inline"
	case strings.HasPrefix(mt, "video/") || ext == "mp4" || ext == "webm" || ext == "mov":
		return "video", "inline"
	case strings.HasPrefix(mt, "audio/") || ext == "mp3" || ext == "wav" || ext == "m4a" || ext == "ogg":
		return "audio", "inline"
	case strings.HasPrefix(mt, "image/"):
		return "image", "inline"
	case mt == "text/markdown" || ext == "md" || ext == "markdown":
		return "markdown", "inline"
	case mt == "text/csv" || mt == "text/tab-separated-values" || ext == "csv" || ext == "tsv":
		return "table", "inline"
	case strings.HasPrefix(mt, "text/") || mt == "application/json" || ext == "txt" || ext == "json" || ext == "log":
		return "text", "inline"
	default:
		return "download", "attachment"
	}
}

// artifactMediaIsActive reports formats that must not be rendered as a live
// document. SVG is XML and can carry script; HTML executes when served as a page.
func artifactMediaIsActive(mediaType, filename string) bool {
	switch canonicalMediaType(mediaType) {
	case "text/html", "application/xhtml+xml", "image/svg+xml":
		return true
	}
	switch fileExt(filename) {
	case "html", "htm", "xhtml", "svg":
		return true
	default:
		return false
	}
}

func canonicalMediaType(mt string) string {
	mt = strings.ToLower(strings.TrimSpace(mt))
	if i := strings.IndexByte(mt, ';'); i >= 0 {
		mt = strings.TrimSpace(mt[:i])
	}
	return mt
}

func fileExt(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if i := strings.LastIndex(name, "."); i >= 0 && i < len(name)-1 {
		return name[i+1:]
	}
	return ""
}
