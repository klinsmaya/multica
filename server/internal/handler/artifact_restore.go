package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/artifact"
)

// RestoreArtifact — POST /api/artifacts/{artifactID}/restore
//
// Copies one historical revision into a new revision. The old rows stay.
// base_revision_id is the head the caller still expects.
func (h *Handler) RestoreArtifact(w http.ResponseWriter, r *http.Request) {
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
		writeError(w, http.StatusBadRequest, "invalid restore request")
		return
	}
	var req struct {
		RevisionID     string `json:"revision_id"`
		BaseRevisionID string `json:"base_revision_id"`
		EditSessionID  string `json:"edit_session_id"`
		FencingToken   int64  `json:"fencing_token"`
		LeaseToken     string `json:"lease_token"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid restore request")
		return
	}
	baseRaw := strings.TrimSpace(req.BaseRevisionID)
	if !ifMatchAgrees(r, baseRaw) {
		writeErrorCode(w, http.StatusConflict, artifact.CodeRevisionConflict, "revision conflict")
		return
	}
	revisionID, err := uuid.Parse(strings.TrimSpace(req.RevisionID))
	if err != nil {
		writeError(w, http.StatusBadRequest, "revision_id is invalid")
		return
	}
	baseID, err := uuid.Parse(baseRaw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "base_revision_id is invalid")
		return
	}
	var lease *artifact.EditLease
	if req.EditSessionID != "" || req.FencingToken != 0 || strings.TrimSpace(req.LeaseToken) != "" {
		sessionID, err := uuid.Parse(strings.TrimSpace(req.EditSessionID))
		if err != nil || req.FencingToken <= 0 || strings.TrimSpace(req.LeaseToken) == "" {
			writeError(w, http.StatusBadRequest, "edit session, fencing token, and lease token are required")
			return
		}
		lease = &artifact.EditLease{
			SessionID:    sessionID,
			ActorID:      actor,
			FencingToken: req.FencingToken,
			TokenHash:    artifact.HashLeaseToken(strings.TrimSpace(req.LeaseToken)),
		}
	}
	result, err := h.artifactService().Restore(r.Context(), artifact.RestoreInput{
		WorkspaceID:    uuid.UUID(art.WorkspaceID.Bytes),
		ActorID:        actor,
		ArtifactID:     uuid.UUID(art.ID.Bytes),
		RevisionID:     revisionID,
		BaseRevisionID: baseID,
		EditLease:      lease,
	})
	if err != nil {
		writeArtifactServiceError(w, err)
		return
	}
	resp := artifactCommandFromCommit(result, "committed", art.Title, "")
	resp.RestoredFromRevisionID = result.RestoredFromRevisionID.String()
	writeJSON(w, http.StatusOK, resp)
}
