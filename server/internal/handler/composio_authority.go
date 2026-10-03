package handler

import (
	"context"
	"encoding/json"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"io"
	"net/http"
	"strings"
)

// ComposioAuthorizeExecution exposes only a boolean verdict to the configured
// gateway service. Request fields are claims to compare with durable records,
// never an authority to choose a task, owner or invoking user.
func (h *Handler) ComposioAuthorizeExecution(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.Composio == nil || !h.composioMCPAppsEnabled(r.Context()) || len(r.Header.Values("Authorization")) != 1 || !h.Composio.VerifyAuthorityToken(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")) || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var input struct {
		Subject     string              `json:"subject"`
		Actor       string              `json:"actor"`
		Agent       string              `json:"agent"`
		Task        string              `json:"task"`
		Connections map[string][]string `json:"connections"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF || len(input.Connections) > 100 {
		http.Error(w, "invalid request", 400)
		return
	}
	taskID, agentID := parseUUID(input.Task), parseUUID(input.Agent)
	if !taskID.Valid || !agentID.Valid {
		http.Error(w, "denied", 403)
		return
	}
	task, err := h.Queries.GetAgentTask(r.Context(), taskID)
	if err != nil {
		http.Error(w, "denied", 403)
		return
	}
	agent, err := h.Queries.GetAgent(r.Context(), agentID)
	if err != nil {
		http.Error(w, "denied", 403)
		return
	}
	if _, err = h.getWorkspaceMember(r.Context(), input.Actor, uuidToString(agent.WorkspaceID)); err != nil {
		http.Error(w, "denied", 403)
		return
	}
	if _, err = h.getWorkspaceMember(r.Context(), input.Subject, uuidToString(agent.WorkspaceID)); err != nil {
		http.Error(w, "denied", 403)
		return
	}
	if !h.canInvokeAgent(r.Context(), agent, "member", input.Actor, input.Actor, uuidToString(agent.WorkspaceID)) || !h.Composio.ValidateClaimPins(r.Context(), task, agent, input.Subject, input.Actor, input.Connections) {
		http.Error(w, "denied", 403)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"allowed":true}`))
}

// AuthorizeComposioClaim uses the same current invocation model as task enqueue,
// with explicit membership checks for both the app owner and invoking human.
func (h *Handler) AuthorizeComposioClaim(ctx context.Context, agent db.Agent, actor string) bool {
	workspace := uuidToString(agent.WorkspaceID)
	if _, err := h.getWorkspaceMember(ctx, actor, workspace); err != nil {
		return false
	}
	if _, err := h.getWorkspaceMember(ctx, uuidToString(agent.OwnerID), workspace); err != nil {
		return false
	}
	return h.canInvokeAgent(ctx, agent, "member", actor, actor, workspace)
}
