package handler

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/multica-ai/multica/server/internal/artifact"
)

// TrashArtifact — POST /api/artifacts/{artifactID}/trash
//
// Hides the artifact, expires open leases, and revokes shares. Bytes stay.
func (h *Handler) TrashArtifact(w http.ResponseWriter, r *http.Request) {
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
	err := h.artifactService().Trash(r.Context(), uuid.UUID(art.WorkspaceID.Bytes), actor, uuid.UUID(art.ID.Bytes))
	if err != nil {
		writeLifecycleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RestoreTrashedArtifact — POST /api/artifacts/{artifactID}/trash/restore
//
// Membership is checked again. Shares and edit sessions stay closed.
func (h *Handler) RestoreTrashedArtifact(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "artifactID"), "artifact id")
	if !ok {
		return
	}
	art, err := h.Queries.GetArtifactAnyStatus(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load artifact")
		return
	}
	if !h.authorizeArtifactWorkspace(w, r, uuidToString(art.WorkspaceID)) {
		return
	}
	if taskID, _, scoped := taskTokenBinding(r); scoped && !artifactOwnedByTask(art, taskID) {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	err = h.artifactService().RestoreTrash(r.Context(), uuid.UUID(art.WorkspaceID.Bytes), uuid.UUID(art.ID.Bytes))
	if err != nil {
		writeLifecycleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetArtifactOps — GET /api/artifact-ops
//
// Reports GC, quota drift, and the conversion queue for the caller's workspace.
// Retention fields are ops defaults.
func (h *Handler) GetArtifactOps(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	if !h.authorizeArtifactWorkspace(w, r, workspaceID) {
		return
	}
	ws, err := uuid.Parse(workspaceID)
	if err != nil {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	health, err := h.artifactService().OpsHealth(r.Context(), ws)
	if err != nil {
		writeLifecycleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, health)
}

func writeLifecycleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, artifact.ErrNotFound):
		writeError(w, http.StatusNotFound, "artifact not found")
	case errors.Is(err, artifact.ErrPurgeRefused):
		writeError(w, http.StatusConflict, "artifact purge refused")
	case errors.Is(err, artifact.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid artifact request")
	default:
		writeError(w, http.StatusInternalServerError, "failed to update artifact")
	}
}
