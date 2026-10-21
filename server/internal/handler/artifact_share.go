package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/artifact"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/storage"
)

// PublishArtifactShare — POST /api/artifacts/{artifactID}/shares
//
// Pins one revision. The response token is returned once. External and public
// audiences stay refused while artifact_external_share is off.
func (h *Handler) PublishArtifactShare(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	art, ok := h.loadArtifactForMember(w, r)
	if !ok {
		return
	}
	actor, _, ok := editorIdentity(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid share request")
		return
	}
	var req struct {
		RevisionID string   `json:"revision_id"`
		Audience   string   `json:"audience"`
		GranteeIDs []string `json:"grantee_ids"`
		ExpiresAt  *string  `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid share request")
		return
	}
	revisionID, err := uuid.Parse(strings.TrimSpace(req.RevisionID))
	if err != nil {
		writeError(w, http.StatusBadRequest, "revision_id is invalid")
		return
	}
	grantees := make([]uuid.UUID, 0, len(req.GranteeIDs))
	for _, raw := range req.GranteeIDs {
		id, err := uuid.Parse(strings.TrimSpace(raw))
		if err != nil {
			writeError(w, http.StatusBadRequest, "grantee_ids is invalid")
			return
		}
		grantees = append(grantees, id)
	}
	var expires *time.Time
	if req.ExpiresAt != nil && strings.TrimSpace(*req.ExpiresAt) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*req.ExpiresAt))
		if err != nil {
			writeError(w, http.StatusBadRequest, "expires_at is invalid")
			return
		}
		expires = &parsed
	}
	svc := h.artifactService()
	svc.ExternalShareEnabled = featureflags.ArtifactExternalShareEnabled(r.Context(), h.FeatureFlags)
	result, err := svc.Publish(r.Context(), artifact.PublishInput{
		WorkspaceID: uuid.UUID(art.WorkspaceID.Bytes),
		ActorID:     actor,
		ArtifactID:  uuid.UUID(art.ID.Bytes),
		RevisionID:  revisionID,
		Audience:    req.Audience,
		GranteeIDs:  grantees,
		ExpiresAt:   expires,
	})
	if err != nil {
		writeShareError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

// RevokeArtifactShare — POST /api/artifacts/{artifactID}/shares/{snapshotID}/revoke
//
// Clears grants for one snapshot. A later share read fails. The outbox row
// stores ids only.
func (h *Handler) RevokeArtifactShare(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	art, ok := h.loadArtifactForMember(w, r)
	if !ok {
		return
	}
	actor, _, ok := editorIdentity(w, r)
	if !ok {
		return
	}
	snapshotID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "snapshotID"), "snapshot id")
	if !ok {
		return
	}
	err := h.artifactService().RevokeShare(r.Context(), uuid.UUID(art.WorkspaceID.Bytes), actor, uuid.UUID(snapshotID.Bytes))
	if err != nil {
		writeShareError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetArtifactShare — GET /api/artifact-shares/{token}
//
// The token is the credential. Cookie and X-User-ID are ignored.
func (h *Handler) GetArtifactShare(w http.ResponseWriter, r *http.Request) {
	file, ok := h.openArtifactShare(w, r, false)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, map[string]string{
		"snapshot_id": file.SnapshotID.String(),
		"revision_id": file.RevisionID.String(),
		"filename":    file.Filename,
		"media_type":  file.MediaType,
	})
}

// GetArtifactShareContent — GET /api/artifact-shares/{token}/content
func (h *Handler) GetArtifactShareContent(w http.ResponseWriter, r *http.Request) {
	file, ok := h.openArtifactShare(w, r, true)
	if !ok {
		return
	}
	writeShareBytes(w, file)
}

// GetArtifactShareResource — GET /api/artifact-shares/{token}/resources/*
func (h *Handler) GetArtifactShareResource(w http.ResponseWriter, r *http.Request) {
	raw, err := url.PathUnescape(chi.URLParam(r, "*"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid resource path")
		return
	}
	file, ok := h.openArtifactSharePath(w, r, raw)
	if !ok {
		return
	}
	writeShareBytes(w, file)
}

func (h *Handler) openArtifactShare(w http.ResponseWriter, r *http.Request, download bool) (artifact.ShareFile, bool) {
	r.Header.Del("Cookie")
	r.Header.Del("X-User-ID")
	token := strings.TrimSpace(chi.URLParam(r, "token"))
	var (
		file artifact.ShareFile
		err  error
	)
	if download {
		file, err = h.artifactService().OpenShareContent(r.Context(), token)
	} else {
		file, err = h.artifactService().OpenShare(r.Context(), token)
	}
	if err != nil {
		writeShareError(w, err)
		return artifact.ShareFile{}, false
	}
	return file, true
}

func (h *Handler) openArtifactSharePath(w http.ResponseWriter, r *http.Request, path string) (artifact.ShareFile, bool) {
	r.Header.Del("Cookie")
	r.Header.Del("X-User-ID")
	token := strings.TrimSpace(chi.URLParam(r, "token"))
	file, err := h.artifactService().OpenShareResource(r.Context(), token, path)
	if err != nil {
		writeShareError(w, err)
		return artifact.ShareFile{}, false
	}
	return file, true
}

func writeShareBytes(w http.ResponseWriter, file artifact.ShareFile) {
	contentType := file.MediaType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	disposition := storage.ContentDisposition(contentType, file.Filename)
	if artifactMediaIsActive(file.MediaType, file.Filename) {
		contentType = "application/octet-stream"
		disposition = storage.AttachmentContentDisposition(file.Filename)
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file.Data)
}

func writeShareError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, artifact.ErrNotFound):
		writeError(w, http.StatusNotFound, "artifact not found")
	case errors.Is(err, artifact.ErrExternalShareDisabled):
		writeErrorCode(w, http.StatusForbidden, artifact.CodeExternalShareDisabled, "external share is disabled")
	case errors.Is(err, artifact.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid share request")
	default:
		writeError(w, http.StatusInternalServerError, "failed to share artifact")
	}
}
