package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/artifact"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/middleware"
)

// DiscoverLocalCandidates classifies file links, write-file events, and media
// hints. The response is a list of artifact_candidate values. None of them is
// a saved artifact, and none of them includes an absolute path.
func (h *Handler) DiscoverLocalCandidates(w http.ResponseWriter, r *http.Request) {
	if h == nil || !h.ArtifactBridge.Reject(w, r) {
		return
	}
	body, ok := decodeLocalBridgeBody(w, r)
	if !ok {
		return
	}
	if !h.localBridgeIdentity(w, r, body.WorkspaceID, body.UserID) {
		return
	}
	found := h.ArtifactBridge.Discover(body.WorkspaceID, body.UserID, body.Signals)
	writeJSON(w, http.StatusOK, map[string]any{
		"candidates": artifact.PublishCandidates(body.UserID, body.UserID, found),
	})
}

// SubmitLocalCandidate checks the file, then seals it with Service.Create and
// Service.Commit. artifacts_v1 still has to be on. A hint path does not seal.
func (h *Handler) SubmitLocalCandidate(w http.ResponseWriter, r *http.Request) {
	if h == nil || !h.ArtifactBridge.Reject(w, r) {
		return
	}
	if !featureflags.ArtifactsV1Enabled(r.Context(), h.FeatureFlags) {
		writeError(w, http.StatusNotFound, "artifacts are disabled")
		return
	}
	body, ok := decodeLocalBridgeBody(w, r)
	if !ok {
		return
	}
	if !h.localBridgeIdentity(w, r, body.WorkspaceID, body.UserID) {
		return
	}
	workspaceID, err := uuid.Parse(body.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "workspace_id is invalid")
		return
	}
	actorID, err := uuid.Parse(body.UserID)
	if err != nil || (body.ActorID != "" && body.ActorID != body.UserID) {
		writeError(w, http.StatusForbidden, "actor does not match the local bridge user")
		return
	}
	in := artifact.LocalSubmit{
		WorkspaceID: workspaceID,
		UserID:      body.UserID,
		ActorID:     actorID,
		OwnerScope:  body.OwnerScope,
		Title:       body.Title,
		Path:        body.Path,
	}
	if body.OwnerID != "" {
		ownerID, err := uuid.Parse(body.OwnerID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "owner_id is invalid")
			return
		}
		in.OwnerID = ownerID
	}
	if body.ArtifactID != "" {
		artifactID, err := uuid.Parse(body.ArtifactID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "artifact_id is invalid")
			return
		}
		in.ArtifactID = &artifactID
	}
	if body.BaseRevisionID != "" {
		baseID, err := uuid.Parse(body.BaseRevisionID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "base_revision_id is invalid")
			return
		}
		in.BaseRevisionID = &baseID
	}
	result, err := h.ArtifactBridge.Submit(r.Context(), h.artifactService(), in)
	if err != nil {
		if errors.Is(err, artifact.ErrHintOnly) || errors.Is(err, artifact.ErrBridgeDisabled) || errors.Is(err, artifact.ErrLocalDeleteKept) {
			artifact.WriteLocalBridgeError(w, err)
			return
		}
		writeArtifactServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, artifactCommandFromCommit(result, "committed", body.Title, filepath.Base(body.Path)))
}

// KeepCloudOnLocalDelete acknowledges a local removal. It does not load,
// soft-delete, or otherwise change the cloud artifact.
func (h *Handler) KeepCloudOnLocalDelete(w http.ResponseWriter, r *http.Request) {
	if h == nil || !h.ArtifactBridge.Reject(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"action": "keep_cloud"})
}

type localBridgeBody struct {
	WorkspaceID    string            `json:"workspace_id"`
	UserID         string            `json:"user_id"`
	ActorID        string            `json:"actor_id"`
	OwnerScope     string            `json:"owner_scope"`
	OwnerID        string            `json:"owner_id"`
	Title          string            `json:"title"`
	Path           string            `json:"path"`
	ArtifactID     string            `json:"artifact_id"`
	BaseRevisionID string            `json:"base_revision_id"`
	Signals        []artifact.Signal `json:"signals"`
}

func decodeLocalBridgeBody(w http.ResponseWriter, r *http.Request) (localBridgeBody, bool) {
	var body localBridgeBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid local bridge request")
		return body, false
	}
	body.WorkspaceID = strings.TrimSpace(body.WorkspaceID)
	body.UserID = strings.TrimSpace(body.UserID)
	body.ActorID = strings.TrimSpace(body.ActorID)
	return body, true
}

func (h *Handler) localBridgeIdentity(w http.ResponseWriter, r *http.Request, workspaceID, userID string) bool {
	headerUser := strings.TrimSpace(r.Header.Get("X-User-ID"))
	daemonWorkspace := middleware.DaemonWorkspaceIDFromContext(r.Context())
	if headerUser == "" && daemonWorkspace == "" {
		writeError(w, http.StatusUnauthorized, "local bridge identity is missing")
		return false
	}
	if headerUser != "" && headerUser != userID {
		writeError(w, http.StatusForbidden, "user does not match the local bridge")
		return false
	}
	if daemonWorkspace != "" && daemonWorkspace != workspaceID {
		writeError(w, http.StatusForbidden, "workspace does not match the local bridge")
		return false
	}
	if workspaceID == "" || userID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id and user_id are required")
		return false
	}
	return true
}
