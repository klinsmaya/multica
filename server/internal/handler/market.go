package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/marketcatalog"
)

const marketTargetScope = "workspace"

func (h *Handler) RequireMarketV1(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !featureflags.MarketV1Enabled(r.Context(), h.FeatureFlags) {
			writeError(w, http.StatusNotFound, "market is not enabled")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) BrowseMarketListings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, ok := optionalIntQuery(w, q, "page")
	if !ok {
		return
	}
	pageSize, ok := optionalIntQuery(w, q, "page_size")
	if !ok {
		return
	}
	result, err := h.MarketCatalog.Browse(r.Context(), marketcatalog.BrowseQuery{Text: q.Get("q"), Page: page, PageSize: pageSize})
	if err != nil {
		writeCatalogError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, listingPageResponseFrom(result))
}

func optionalIntQuery(w http.ResponseWriter, q map[string][]string, name string) (int, bool) {
	raw := ""
	if v := q[name]; len(v) > 0 {
		raw = strings.TrimSpace(v[0])
	}
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, name+" must be an integer")
		return 0, false
	}
	return n, true
}

func (h *Handler) GetMarketListing(w http.ResponseWriter, r *http.Request) {
	id, ok := listingIDOrBadRequest(w, r)
	if !ok {
		return
	}
	var version marketcatalog.Version
	if raw := r.URL.Query().Get("version"); raw != "" {
		v, err := marketcatalog.ParseVersion(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid version")
			return
		}
		version = v
	}
	listing, err := h.MarketCatalog.Listing(r.Context(), id, version)
	if err != nil {
		writeCatalogError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, listingResponseFrom(listing))
}

func (h *Handler) PreflightMarketInstall(w http.ResponseWriter, r *http.Request) {
	member, ok := h.workspaceMember(w, r, ctxWorkspaceID(r.Context()))
	if !ok {
		return
	}
	in, ok := decodePreflightInput(w, r)
	if !ok {
		return
	}
	listing, err := h.MarketCatalog.Listing(r.Context(), in.Listing, in.Version)
	if err != nil {
		writeCatalogError(w, r, err)
		return
	}
	var report preflightReport
	err = h.runMarketRead(r.Context(), func(q marketReader) error {
		report, err = h.preflightInstall(r.Context(), q, member, listing, in.Choice)
		return err
	})
	if err != nil {
		slog.Warn("market preflight failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to check listing")
		return
	}
	writeJSON(w, http.StatusOK, preflightResponseFrom(report))
}

type preflightRequest struct {
	Version   string `json:"version"`
	RuntimeID string `json:"runtime_id"`
	Model     string `json:"model"`
}

func decodePreflightInput(w http.ResponseWriter, r *http.Request) (preflightInput, bool) {
	id, ok := listingIDOrBadRequest(w, r)
	if !ok {
		return preflightInput{}, false
	}
	var req preflightRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return preflightInput{}, false
	}
	version, err := marketcatalog.ParseVersion(req.Version)
	if err != nil {
		writeError(w, http.StatusBadRequest, "version is required")
		return preflightInput{}, false
	}
	in := preflightInput{Listing: id, Version: version, Choice: modelChoice{ModelID: strings.TrimSpace(req.Model)}}
	if req.RuntimeID != "" {
		runtimeID, ok := parseUUIDOrBadRequest(w, req.RuntimeID, "runtime_id")
		if !ok {
			return preflightInput{}, false
		}
		in.Choice.RuntimeID = runtimeID
	}
	return in, true
}

func listingIDOrBadRequest(w http.ResponseWriter, r *http.Request) (marketcatalog.ListingID, bool) {
	id, err := marketcatalog.ParseListingID(chi.URLParam(r, "listingId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid listing id")
		return "", false
	}
	return id, true
}

func writeCatalogError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, marketcatalog.ErrListingNotFound):
		writeErrorCode(w, http.StatusNotFound, "listing_not_found", "listing not found")
	case errors.Is(err, marketcatalog.ErrInvalidListing):
		slog.Warn("market catalog sent an invalid listing", append(logger.RequestAttrs(r), "error", err)...)
		writeErrorCode(w, http.StatusBadGateway, "catalog_invalid_listing", "the catalog sent a listing this server cannot read")
	default:
		slog.Warn("market catalog unavailable", append(logger.RequestAttrs(r), "error", err)...)
		writeErrorCode(w, http.StatusBadGateway, "catalog_unavailable", "the catalog is unavailable")
	}
}

func publicSource(s marketcatalog.Source) string {
	switch s {
	case marketcatalog.SourceNocoBase:
		return "live"
	case marketcatalog.SourceStub:
		return "stub"
	default:
		return "unknown"
	}
}


type listingSummaryResponse struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Summary       string  `json:"summary"`
	Publisher     string  `json:"publisher"`
	IconURL       *string `json:"icon_url"`
	LatestVersion string  `json:"latest_version"`
}

type listingPageResponse struct {
	Source   string                   `json:"source"`
	Items    []listingSummaryResponse `json:"items"`
	Page     int                      `json:"page"`
	PageSize int                      `json:"page_size"`
	Total    int                      `json:"total"`
}

type listingResponse struct {
	Source       string                 `json:"source"`
	Listing      listingSummaryResponse `json:"listing"`
	Version      string                 `json:"version"`
	Description  string                 `json:"description"`
	Agent        agentTemplateResponse  `json:"agent"`
	Requirements requirementsResponse   `json:"requirements"`
}

type agentTemplateResponse struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type requirementsResponse struct {
	Model       modelNeedResponse        `json:"model"`
	Tools       []toolNeedResponse       `json:"tools"`
	Connections []connectionNeedResponse `json:"connections"`
	Knowledge   []knowledgeNeedResponse  `json:"knowledge"`
	Unsupported []unsupportedResponse    `json:"unsupported"`
}

type modelNeedResponse struct {
	Provider string   `json:"provider"`
	Models   []string `json:"models"`
}

type toolNeedResponse struct {
	Name          string `json:"name"`
	Provisionable bool   `json:"provisionable"`
}

type connectionNeedResponse struct {
	Provider string `json:"provider"`
	Toolkit  string `json:"toolkit"`
}

type knowledgeNeedResponse struct {
	Source        string `json:"source"`
	Name          string `json:"name"`
	Provisionable bool   `json:"provisionable"`
}

type unsupportedResponse struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type preflightResponse struct {
	ListingID    string               `json:"listing_id"`
	Version      string               `json:"version"`
	Source       string               `json:"source"`
	TargetScope  string               `json:"target_scope"`
	EvaluatedFor evaluatedForResponse `json:"evaluated_for"`
	Status       string               `json:"status"`
	Model        modelCheckResponse   `json:"model"`
	Tools        []checkResponse      `json:"tools"`
	Connections  []checkResponse      `json:"connections"`
	Knowledge    []checkResponse      `json:"knowledge"`
	Unsupported  []checkResponse      `json:"unsupported"`
}

type evaluatedForResponse struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

type checkResponse struct {
	Name    string           `json:"name"`
	Status  string           `json:"status"`
	Reason  string           `json:"reason,omitempty"`
	Binding *bindingResponse `json:"binding,omitempty"`
}

type bindingResponse struct {
	Action string  `json:"action"`
	ID     *string `json:"id"`
}

type modelCheckResponse struct {
	Provider  string                  `json:"provider"`
	Status    string                  `json:"status"`
	Reason    string                  `json:"reason,omitempty"`
	RuntimeID *string                 `json:"runtime_id"`
	Model     *string                 `json:"model"`
	Options   []runtimeOptionResponse `json:"options"`
}

type runtimeOptionResponse struct {
	RuntimeID string   `json:"runtime_id"`
	Label     string   `json:"label"`
	Usable    bool     `json:"usable"`
	Reason    *string  `json:"reason"`
	Models    []string `json:"models"`
}

func listingSummaryResponseFrom(s marketcatalog.ListingSummary) listingSummaryResponse {
	resp := listingSummaryResponse{
		ID:            string(s.ID),
		Name:          s.Name,
		Summary:       s.Summary,
		Publisher:     s.Publisher,
		LatestVersion: string(s.LatestVersion),
	}
	if s.IconURL != "" {
		resp.IconURL = &s.IconURL
	}
	return resp
}

func listingPageResponseFrom(p marketcatalog.ListingPage) listingPageResponse {
	items := make([]listingSummaryResponse, 0, len(p.Items))
	for _, s := range p.Items {
		items = append(items, listingSummaryResponseFrom(s))
	}
	return listingPageResponse{Source: publicSource(p.Source), Items: items, Page: p.Page, PageSize: p.PageSize, Total: p.Total}
}

func listingResponseFrom(l marketcatalog.Listing) listingResponse {
	m := l.Manifest
	req := requirementsResponse{
		Model:       modelNeedResponse{Provider: m.Model.Provider, Models: append([]string{}, m.Model.Models...)},
		Tools:       make([]toolNeedResponse, 0, len(m.Tools)),
		Connections: make([]connectionNeedResponse, 0, len(m.Connections)),
		Knowledge:   make([]knowledgeNeedResponse, 0, len(m.Knowledge)),
		Unsupported: make([]unsupportedResponse, 0, len(m.Unsupported)),
	}
	for _, t := range m.Tools {
		req.Tools = append(req.Tools, toolNeedResponse{Name: t.ServerName, Provisionable: t.Template != nil})
	}
	for _, c := range m.Connections {
		req.Connections = append(req.Connections, connectionNeedResponse{Provider: string(c.Provider), Toolkit: c.Toolkit})
	}
	for _, k := range m.Knowledge {
		req.Knowledge = append(req.Knowledge, knowledgeNeedResponse{
			Source:        string(k.Source),
			Name:          k.Name,
			Provisionable: k.Source == marketcatalog.KnowledgeSkill && k.Bundle != nil,
		})
	}
	for _, u := range m.Unsupported {
		req.Unsupported = append(req.Unsupported, unsupportedResponse{Kind: u.Kind, Name: u.Name})
	}
	return listingResponse{
		Source:       publicSource(l.Source),
		Listing:      listingSummaryResponseFrom(l.Summary),
		Version:      string(l.Version),
		Description:  l.Description,
		Agent:        agentTemplateResponse{Name: m.Agent.Name, Description: m.Agent.Description},
		Requirements: req,
	}
}

func preflightResponseFrom(r preflightReport) preflightResponse {
	return preflightResponse{
		ListingID:    string(r.Listing.ID),
		Version:      string(r.Version),
		Source:       publicSource(r.Source),
		TargetScope:  marketTargetScope,
		EvaluatedFor: evaluatedForResponse{UserID: uuidToString(r.EvaluatedFor.UserID), Role: r.EvaluatedFor.Role},
		Status:       string(r.Status()),
		Model:        modelCheckResponseFrom(r.Model),
		Tools:        checkResponsesFrom(r.Tools),
		Connections:  checkResponsesFrom(r.Connections),
		Knowledge:    checkResponsesFrom(r.Knowledge),
		Unsupported:  checkResponsesFrom(r.Unsupported),
	}
}

func modelCheckResponseFrom(mc modelCheck) modelCheckResponse {
	resp := modelCheckResponse{
		Provider: mc.Provider,
		Status:   string(mc.Status),
		Reason:   string(mc.Reason),
		Options:  make([]runtimeOptionResponse, 0, len(mc.Options)),
	}
	if mc.Status == checkSatisfied {
		resp.RuntimeID = uuidToPtr(mc.RuntimeID)
		if mc.ModelID != "" {
			model := mc.ModelID
			resp.Model = &model
		}
	}
	for _, o := range mc.Options {
		opt := runtimeOptionResponse{
			RuntimeID: uuidToString(o.RuntimeID),
			Label:     o.Label,
			Usable:    o.Usable,
			Models:    append([]string{}, o.Models...),
		}
		if !o.Usable {
			reason := string(o.Reason)
			opt.Reason = &reason
		}
		resp.Options = append(resp.Options, opt)
	}
	return resp
}

func checkResponsesFrom(checks []check) []checkResponse {
	out := make([]checkResponse, 0, len(checks))
	for _, c := range checks {
		resp := checkResponse{Name: c.Name, Status: string(c.Status), Reason: string(c.Reason)}
		if c.Status == checkSatisfied {
			resp.Binding = &bindingResponse{Action: string(c.Binding.Action), ID: uuidToPtr(c.Binding.ID)}
		}
		out = append(out, resp)
	}
	return out
}
