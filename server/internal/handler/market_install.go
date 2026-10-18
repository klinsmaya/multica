package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/marketcatalog"
	"github.com/multica-ai/multica/server/internal/marketinstall"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (h *Handler) WireMarketInstall() {
	if h.TxStarter == nil || h.MarketCatalog == nil {
		return
	}
	h.MarketInstall = marketinstall.Open(h.TxStarter, installGate{h: h}, listingCatalog{h: h})
}

type listingCatalog struct{ h *Handler }

func (c listingCatalog) Listing(ctx context.Context, listingID, version string) (marketcatalog.Listing, error) {
	if c.h.MarketCatalog == nil {
		return marketcatalog.Listing{}, marketcatalog.ErrCatalogUnavailable
	}
	id, err := marketcatalog.ParseListingID(listingID)
	if err != nil {
		return marketcatalog.Listing{}, err
	}
	var parsed marketcatalog.Version
	if version != "" {
		parsed, err = marketcatalog.ParseVersion(version)
		if err != nil {
			return marketcatalog.Listing{}, err
		}
	}
	return c.h.MarketCatalog.Listing(ctx, id, parsed)
}

type installGate struct{ h *Handler }

func (g installGate) Admit(ctx context.Context, caller marketinstall.Actor, plan marketinstall.Plan, listing marketcatalog.Listing) (marketinstall.Admission, error) {
	choice := modelChoice{ModelID: plan.ModelID}
	if plan.RuntimeID != "" {
		id, err := util.ParseUUID(plan.RuntimeID)
		if err != nil {
			return marketinstall.Admission{Code: marketinstall.CodeStaleMapping}, nil
		}
		choice.RuntimeID = id
	}
	var admission marketinstall.Admission
	err := g.h.runMarketRead(ctx, func(q marketReader) error {
		queries, ok := q.(*db.Queries)
		if !ok {
			return errors.New("market install reader")
		}
		memberID, err := util.ParseUUID(string(caller.MemberID))
		if err != nil {
			admission.Code = marketinstall.CodeStalePermission
			return nil
		}
		member, err := queries.GetMember(ctx, memberID)
		if errors.Is(err, pgx.ErrNoRows) {
			admission.Code = marketinstall.CodeStalePermission
			return nil
		}
		if err != nil {
			return err
		}
		if uuidToString(member.WorkspaceID) != string(caller.WorkspaceID) {
			admission.Code = marketinstall.CodeStalePermission
			return nil
		}
		report, err := g.h.preflightInstall(ctx, q, member, listing, choice)
		if err != nil {
			return err
		}
		if report.Status() != reportReady {
			admission.Code = staleForReport(report)
			return nil
		}
		admission.Ready = true
		admission.RuntimeID = uuidToString(report.Model.RuntimeID)
		admission.ModelID = report.Model.ModelID
		admission.ExistingSkills, err = existingSkills(ctx, queries, member.WorkspaceID, report.Knowledge)
		if err != nil {
			return err
		}
		admission.ExistingMCPs, err = existingMCPs(ctx, queries, member.WorkspaceID, report.Tools)
		return err
	})
	if err != nil {
		return marketinstall.Admission{}, err
	}
	return admission, nil
}

func staleForReport(report preflightReport) marketinstall.StaleCode {
	if report.Status() == reportChoose {
		return marketinstall.CodeStaleMapping
	}
	if reason := firstBlockReason(report); reason != "" {
		code, ok := staleCodeFor(reason)
		if ok {
			return code
		}
	}
	return marketinstall.CodeStaleRelease
}

func firstBlockReason(report preflightReport) blockReason {
	if report.Model.Status == checkBlocked {
		return report.Model.Reason
	}
	for _, list := range [][]check{report.Tools, report.Connections, report.Knowledge, report.Unsupported} {
		for _, item := range list {
			if item.Status == checkBlocked {
				return item.Reason
			}
		}
	}
	return ""
}

func staleCodeFor(reason blockReason) (marketinstall.StaleCode, bool) {
	switch reason {
	case reasonNoRuntimeForProvider, reasonNoUsableRuntime, reasonRuntimeNotFound,
		reasonRuntimeProviderMismatch, reasonMcpServerMissing, reasonSkillMissing,
		reasonModelNotOffered, reasonModelUnavailable, reasonConnectionNotConnected:
		return marketinstall.CodeStaleMapping, true
	case reasonRuntimePrivate, reasonRuntimeOwnerless, reasonMcpAttachForbidden,
		reasonMcpLibraryAdminOnly, reasonSkillBindForbidden:
		return marketinstall.CodeStalePermission, true
	case reasonModelCatalogUnknown, reasonModelSelectionUnsupported,
		reasonConnectionProviderUnsupported, reasonConnectionsUnavailable,
		reasonKnowledgeSourceUnsupported, reasonRequirementUnsupported:
		return marketinstall.CodeStaleCapabilities, true
	default:
		return "", false
	}
}

func existingSkills(ctx context.Context, q *db.Queries, workspace pgtype.UUID, checks []check) ([]marketinstall.ExistingResource, error) {
	var out []marketinstall.ExistingResource
	for _, item := range checks {
		if item.Binding.Action != bindUseExisting || !item.Binding.ID.Valid {
			continue
		}
		resource := marketinstall.ExistingResource{Name: item.Name, ID: marketinstall.CorrelationID(uuidToString(item.Binding.ID))}
		skill, err := q.GetSkillInWorkspace(ctx, db.GetSkillInWorkspaceParams{ID: item.Binding.ID, WorkspaceID: workspace})
		if errors.Is(err, pgx.ErrNoRows) {
			out = append(out, resource)
			continue
		}
		if err != nil {
			return nil, err
		}
		files, err := q.ListSkillFiles(ctx, skill.ID)
		if err != nil {
			return nil, err
		}
		resource.Digest = marketinstall.ContentDigest(skill.Content)
		for _, file := range files {
			resource.Files = append(resource.Files, marketinstall.NamedDigest{
				Name:   file.Path,
				Digest: marketinstall.ContentDigest(file.Content),
			})
		}
		out = append(out, resource)
	}
	return out, nil
}

func existingMCPs(ctx context.Context, q *db.Queries, workspace pgtype.UUID, checks []check) ([]marketinstall.ExistingResource, error) {
	var out []marketinstall.ExistingResource
	for _, item := range checks {
		if item.Binding.Action != bindUseExisting || !item.Binding.ID.Valid {
			continue
		}
		resource := marketinstall.ExistingResource{Name: item.Name, ID: marketinstall.CorrelationID(uuidToString(item.Binding.ID))}
		server, err := q.GetWorkspaceMcpServer(ctx, db.GetWorkspaceMcpServerParams{ID: item.Binding.ID, WorkspaceID: workspace})
		if errors.Is(err, pgx.ErrNoRows) {
			out = append(out, resource)
			continue
		}
		if err != nil {
			return nil, err
		}
		resource.Digest = mcpDigestFromConfig(server.Config)
		out = append(out, resource)
	}
	return out, nil
}

func mcpDigestFromConfig(config []byte) string {
	var parsed struct {
		Transport string `json:"transport"`
		URL       string `json:"url"`
	}
	if err := json.Unmarshal(config, &parsed); err != nil || parsed.URL == "" {
		return ""
	}
	transport := parsed.Transport
	if transport == "" {
		transport = mcpTransportOf(config)
	}
	if transport == "" || transport == "unknown" || marketinstall.URLHasUserinfo(parsed.URL) {
		return ""
	}
	return marketinstall.MCPDigest(transport, parsed.URL)
}

func (h *Handler) CommitMarketOperation(w http.ResponseWriter, r *http.Request) {
	if h.MarketInstall == nil {
		writeError(w, http.StatusNotFound, "market install is not enabled")
		return
	}
	member, ok := h.workspaceMember(w, r, ctxWorkspaceID(r.Context()))
	if !ok {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid commit document")
		return
	}
	doc, err := marketinstall.ParseCommitDocument(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid commit document")
		return
	}
	op, err := h.MarketInstall.Commit(r.Context(), actorFromMember(member), doc)
	writeInstallResult(w, r, op, err)
}

func (h *Handler) GetMarketOperation(w http.ResponseWriter, r *http.Request) {
	if h.MarketInstall == nil {
		writeError(w, http.StatusNotFound, "market install is not enabled")
		return
	}
	member, ok := h.workspaceMember(w, r, ctxWorkspaceID(r.Context()))
	if !ok {
		return
	}
	id, ok := operationIDOrBadRequest(w, r)
	if !ok {
		return
	}
	op, err := h.MarketInstall.Get(r.Context(), actorFromMember(member), id)
	writeInstallResult(w, r, op, err)
}

func (h *Handler) ReconcileMarketOperation(w http.ResponseWriter, r *http.Request) {
	if h.MarketInstall == nil {
		writeError(w, http.StatusNotFound, "market install is not enabled")
		return
	}
	member, ok := h.workspaceMember(w, r, ctxWorkspaceID(r.Context()))
	if !ok {
		return
	}
	id, ok := operationIDOrBadRequest(w, r)
	if !ok {
		return
	}
	op, err := h.MarketInstall.Reconcile(r.Context(), actorFromMember(member), id)
	writeInstallResult(w, r, op, err)
}

func (h *Handler) ListMarketInstances(w http.ResponseWriter, r *http.Request) {
	if h.MarketInstall == nil {
		writeError(w, http.StatusNotFound, "market install is not enabled")
		return
	}
	member, ok := h.workspaceMember(w, r, ctxWorkspaceID(r.Context()))
	if !ok {
		return
	}
	items, err := h.MarketInstall.ListInstances(r.Context(), actorFromMember(member))
	if err != nil {
		slog.Warn("market install list failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "market install failed")
		return
	}
	out := make([]instanceResponse, 0, len(items))
	for _, item := range items {
		out = append(out, instanceView(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"instances": out})
}

func (h *Handler) GetMarketInstance(w http.ResponseWriter, r *http.Request) {
	if h.MarketInstall == nil {
		writeError(w, http.StatusNotFound, "market install is not enabled")
		return
	}
	member, ok := h.workspaceMember(w, r, ctxWorkspaceID(r.Context()))
	if !ok {
		return
	}
	raw := chi.URLParam(r, "instanceID")
	if _, err := util.ParseUUID(raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance id")
		return
	}
	item, err := h.MarketInstall.GetInstance(r.Context(), actorFromMember(member), marketinstall.InstanceID(raw))
	if errors.Is(err, marketinstall.ErrNotFound) {
		writeError(w, http.StatusNotFound, "instance not found")
		return
	}
	if err != nil {
		slog.Warn("market install get failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "market install failed")
		return
	}
	writeJSON(w, http.StatusOK, instanceView(item))
}

func actorFromMember(member db.Member) marketinstall.Actor {
	return marketinstall.Actor{
		WorkspaceID: marketinstall.WorkspaceID(uuidToString(member.WorkspaceID)),
		MemberID:    marketinstall.MemberID(uuidToString(member.ID)),
		UserID:      marketinstall.UserID(uuidToString(member.UserID)),
	}
}

func operationIDOrBadRequest(w http.ResponseWriter, r *http.Request) (marketinstall.OperationID, bool) {
	raw := chi.URLParam(r, "operationID")
	if _, err := util.ParseUUID(raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid operation id")
		return "", false
	}
	return marketinstall.OperationID(raw), true
}

func writeInstallResult(w http.ResponseWriter, r *http.Request, op marketinstall.Operation, err error) {
	var stale marketinstall.Stale
	switch {
	case errors.As(err, &stale):
		writeErrorCode(w, http.StatusForbidden, string(stale.Code), string(stale.Code))
	case errors.Is(err, marketinstall.ErrIdempotencyConflict), errors.Is(err, marketinstall.ErrFence):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":     err.Error(),
			"operation": operationView(op),
		})
	case errors.Is(err, marketinstall.ErrNotFound):
		writeError(w, http.StatusNotFound, "operation not found")
	case errors.Is(err, marketinstall.ErrInvalidDocument):
		writeError(w, http.StatusBadRequest, "invalid commit document")
	case err != nil:
		slog.Warn("market install failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "market install failed")
	default:
		writeJSON(w, http.StatusOK, operationView(op))
	}
}

type operationResponse struct {
	ID             string                `json:"id"`
	InstanceID     string                `json:"instance_id,omitempty"`
	Status         string                `json:"status"`
	Readiness      string                `json:"readiness"`
	IdempotencyKey string                `json:"idempotency_key"`
	PlanHash       string                `json:"plan_hash"`
	Steps          []installStepResponse `json:"steps"`
}

type installStepResponse struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Phase    string `json:"phase"`
	OutputID string `json:"output_id,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type instanceResponse struct {
	ID        string                `json:"id"`
	ListingID string                `json:"listing_id"`
	Version   string                `json:"version"`
	PlanHash  string                `json:"plan_hash"`
	Readiness string                `json:"readiness"`
	Steps     []installStepResponse `json:"steps"`
}

func operationView(op marketinstall.Operation) operationResponse {
	return operationResponse{
		ID:             string(op.ID),
		InstanceID:     string(op.InstanceID),
		Status:         string(op.Status()),
		Readiness:      string(op.Readiness()),
		IdempotencyKey: string(op.IdempotencyKey),
		PlanHash:       string(op.PlanHash),
		Steps:          stepViews(op.Steps),
	}
}

func instanceView(item marketinstall.Instance) instanceResponse {
	return instanceResponse{
		ID:        string(item.ID),
		ListingID: item.ListingID,
		Version:   item.Version,
		PlanHash:  string(item.PlanHash),
		Readiness: string(item.Readiness()),
		Steps:     stepViews(item.Steps),
	}
}

func stepViews(steps []marketinstall.Step) []installStepResponse {
	out := make([]installStepResponse, 0, len(steps))
	for _, step := range steps {
		out = append(out, installStepResponse{
			Key:      string(step.Key),
			Kind:     string(step.Kind),
			Phase:    string(step.Phase),
			OutputID: string(step.OutputID),
			Reason:   string(step.Reason),
		})
	}
	return out
}
