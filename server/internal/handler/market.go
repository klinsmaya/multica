package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/market"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// marketMember is the catalog and preflight gate shared by the three routes.
// The flag is checked here, not in the router, because one preflight mixes
// read and write demands.
func (h *Handler) marketMember(w http.ResponseWriter, r *http.Request) (string, db.Member, bool) {
	if !featureflags.MarketCatalogV1Enabled(r.Context(), h.FeatureFlags) {
		writeFeatureDisabled(w, "market_catalog_disabled", "market catalog is not enabled")
		return "", db.Member{}, false
	}
	// resolveActor treats cloud_pat as a member. Market install is a human
	// decision, so the same sources RequireHumanActor rejects stop here.
	if isMachineCredentialActor(r) {
		writeErrorCode(w, http.StatusForbidden, "identity.machine_credential", "market catalog requires a human actor")
		return "", db.Member{}, false
	}
	workspaceID := workspaceIDFromURL(r, "id")
	member, ok := h.requireWorkspaceMember(w, r, workspaceID, "workspace not found")
	if !ok {
		return "", db.Member{}, false
	}
	return workspaceID, member, true
}

func (h *Handler) marketSource() market.Source {
	if h.MarketCatalog != nil {
		return h.MarketCatalog
	}
	return market.StubSource{}
}

func (h *Handler) ListMarketCatalog(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := h.marketMember(w, r); !ok {
		return
	}
	resources, err := h.marketSource().List(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "market catalog unavailable")
		return
	}
	if resources == nil {
		resources = []market.Resource{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"resources": resources})
}

func (h *Handler) GetMarketCatalogResource(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := h.marketMember(w, r); !ok {
		return
	}
	resourceID, err := url.PathUnescape(chi.URLParam(r, "*"))
	if err != nil || resourceID == "" {
		writeError(w, http.StatusBadRequest, "resource id is required")
		return
	}
	resource, err := h.marketSource().Get(r.Context(), resourceID)
	if errors.Is(err, market.ErrNotFound) {
		writeError(w, http.StatusNotFound, "market resource not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "market catalog unavailable")
		return
	}
	writeJSON(w, http.StatusOK, resource)
}

type preflightRequest struct {
	MappingID    string             `json:"mappingId"`
	Revision     int64              `json:"revision"`
	ConsumerRef  market.ConsumerRef `json:"consumerRef"`
	TargetScope  market.TargetScope `json:"targetScope"`
	Verification string             `json:"verification"`
	Expiry       string             `json:"expiry"`
	Demands      []market.Demand    `json:"demands"`
	// Actor is decoded so a client-supplied principal is consumed and dropped.
	// The member comes from the authenticated session.
	Actor json.RawMessage `json:"actor"`
}

func (h *Handler) PreflightMarket(w http.ResponseWriter, r *http.Request) {
	workspaceID, member, ok := h.marketMember(w, r)
	if !ok {
		return
	}
	var req preflightRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	_ = req.Actor
	identity := market.Identity{
		MappingID:    req.MappingID,
		Revision:     req.Revision,
		ConsumerRef:  req.ConsumerRef,
		TargetScope:  req.TargetScope,
		Verification: req.Verification,
		Expiry:       req.Expiry,
	}
	if err := market.Gate(identity, workspaceID, time.Now()); err != nil {
		writeMarketError(w, err)
		return
	}
	facts, err := h.marketFacts(r, workspaceID, member)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workspace permissions")
		return
	}
	result, err := market.Evaluate(req.Demands, facts, req.TargetScope)
	if err != nil {
		writeMarketError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeMarketError(w http.ResponseWriter, err error) {
	var me *market.Error
	if !errors.As(err, &me) {
		writeError(w, http.StatusInternalServerError, "market request failed")
		return
	}
	status := http.StatusBadRequest
	switch me.Code {
	case market.CodeScopeMismatch, market.CodeIdentityUnverified, market.CodeIdentityExpired:
		status = http.StatusForbidden
	}
	writeErrorCode(w, status, me.Code, me.Message)
}

func (h *Handler) marketFacts(r *http.Request, workspaceID string, member db.Member) (market.Facts, error) {
	ctx := r.Context()
	wsUUID := parseUUID(workspaceID)
	actorType, _ := h.resolveActor(r, uuidToString(member.UserID), workspaceID)
	facts := market.Facts{
		ModelIDs:      map[string]struct{}{},
		Skills:        map[string]market.SkillFact{},
		MCPNames:      map[string]struct{}{},
		ConnectionIDs: map[string]struct{}{},
		VCSAvailable:  h.isVCSAvailable(),
		ActorUserID:   uuidToString(member.UserID),
		Role:          member.Role,
		ActorIsAgent:  actorType == "agent",
	}

	modelIDs, err := h.visibleModelIDs(ctx, member)
	if err != nil {
		return market.Facts{}, err
	}
	facts.ModelIDs = modelIDs

	skills, err := h.Queries.ListSkillSummariesByWorkspace(ctx, wsUUID)
	if err != nil {
		return market.Facts{}, err
	}
	for _, skill := range skills {
		createdBy := ""
		if skill.CreatedBy.Valid {
			createdBy = uuidToString(skill.CreatedBy)
		}
		facts.Skills[skill.Name] = market.SkillFact{CreatedBy: createdBy}
	}

	servers, err := h.Queries.ListWorkspaceMcpServers(ctx, wsUUID)
	if err != nil {
		return market.Facts{}, err
	}
	for _, server := range servers {
		facts.MCPNames[server.Name] = struct{}{}
	}

	if facts.VCSAvailable {
		connections, err := h.Queries.ListVCSConnectionsByWorkspace(ctx, wsUUID)
		if err != nil {
			return market.Facts{}, err
		}
		for _, connection := range connections {
			facts.ConnectionIDs[uuidToString(connection.ID)] = struct{}{}
		}
	}
	return facts, nil
}

func (h *Handler) visibleModelIDs(ctx context.Context, member db.Member) (map[string]struct{}, error) {
	runtimes, err := h.Queries.ListVisibleAgentRuntimes(ctx, db.ListVisibleAgentRuntimesParams{
		WorkspaceID: member.WorkspaceID,
		OwnerID:     member.UserID,
	})
	if err != nil {
		return nil, err
	}
	ids := map[string]struct{}{}
	for _, runtime := range runtimes {
		if !canUseRuntimeForAgent(member, runtime) {
			continue
		}
		// A cache miss is a denial. Revalidation would enqueue daemon work,
		// and preflight must not start a run.
		snapshot := h.cachedModelCatalog(ctx, uuidToString(runtime.ID))
		if snapshot == nil || !snapshot.Supported {
			continue
		}
		for _, model := range snapshot.Models {
			if model.ID != "" {
				ids[model.ID] = struct{}{}
			}
		}
	}
	return ids, nil
}
