package handler

import (
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openconnector "github.com/multica-ai/multica/server/internal/integrations/openconnector"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// open-connector operations in the platform admin console
// (/api/admin/open-connector/*). Reads combine the instance catalog with the
// connector_service_policy table and connection counts; writes change only
// the policy table and go through runAdminAction, so every publication or
// allowlist change lands in admin_audit_log. Policy rows are read on every
// user-facing catalog call, so a change takes effect immediately.

const (
	adminConnectorTargetType = "connector_service"
	maxAllowedUsers          = 500
)

// AdminOpenConnectorHealthResponse reports whether the integration is wired
// and, if so, the outcome of one live probe.
type AdminOpenConnectorHealthResponse struct {
	Configured bool                      `json:"configured"`
	Health     *openconnector.HealthView `json:"health,omitempty"`
}

func (h *Handler) AdminOpenConnectorHealth(w http.ResponseWriter, r *http.Request) {
	resp := AdminOpenConnectorHealthResponse{Configured: h.openConnectorWired()}
	if resp.Configured {
		view := h.OpenConnector.Health(r.Context())
		resp.Health = &view
	}
	writeJSON(w, http.StatusOK, resp)
}

// AdminConnectorUserRef identifies a user on a service allowlist.
type AdminConnectorUserRef struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
}

// AdminConnectorServiceResponse is one row of the service catalog: what the
// instance offers (Available, AuthTypes), what the operator decided (Enabled,
// AllowedTools, AllowedUsers) and how it is used (connection counts).
type AdminConnectorServiceResponse struct {
	Service      string                  `json:"service"`
	Name         string                  `json:"name"`
	Logo         string                  `json:"logo,omitempty"`
	AuthTypes    []string                `json:"auth_types"`
	Available    bool                    `json:"available"`
	Enabled      bool                    `json:"enabled"`
	AllowedTools []string                `json:"allowed_tools"`
	AllowedUsers []AdminConnectorUserRef `json:"allowed_users"`
	Connections  AdminConnectorCounts    `json:"connections"`
	UpdatedAt    *string                 `json:"updated_at"`
}

// AdminConnectorCounts are per-status connection counts; Users counts
// distinct owners of a non-revoked connection.
type AdminConnectorCounts struct {
	Active  int64 `json:"active"`
	Reauth  int64 `json:"reauth_required"`
	Revoked int64 `json:"revoked"`
	Users   int64 `json:"users"`
}

// AdminOpenConnectorServicesResponse carries the catalog plus whether the
// instance could be reached: when it could not, Available is false for every
// row and the UI explains why instead of offering a toggle on a dead catalog.
type AdminOpenConnectorServicesResponse struct {
	Configured   bool                            `json:"configured"`
	CatalogError string                          `json:"catalog_error,omitempty"`
	Items        []AdminConnectorServiceResponse `json:"items"`
}

func (h *Handler) AdminOpenConnectorListServices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	resp := AdminOpenConnectorServicesResponse{Configured: h.openConnectorWired(), Items: []AdminConnectorServiceResponse{}}
	byService := map[string]*AdminConnectorServiceResponse{}
	get := func(service string) *AdminConnectorServiceResponse {
		key := strings.ToLower(strings.TrimSpace(service))
		if row, ok := byService[key]; ok {
			return row
		}
		row := &AdminConnectorServiceResponse{
			Service: key, Name: key, AuthTypes: []string{}, AllowedTools: []string{}, AllowedUsers: []AdminConnectorUserRef{},
		}
		byService[key] = row
		return row
	}

	if resp.Configured {
		apps, err := h.OpenConnector.Catalog(ctx)
		if err != nil {
			resp.CatalogError = err.Error()
		}
		for _, a := range apps {
			row := get(a.Service)
			row.Name, row.Logo, row.AuthTypes, row.Available = a.Name, a.Logo, a.AuthTypes, true
		}
	}

	policies, err := h.Queries.ListConnectorServicePolicies(ctx, openconnector.ProviderName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load service policies")
		return
	}
	var userIDs []pgtype.UUID
	for _, p := range policies {
		row := get(p.Service)
		row.Enabled = p.Enabled
		row.AllowedTools = append([]string{}, p.AllowedTools...)
		row.UpdatedAt = timestampToPtr(p.UpdatedAt)
		userIDs = append(userIDs, p.AllowedUserIds...)
	}
	users := map[pgtype.UUID]AdminConnectorUserRef{}
	if len(userIDs) > 0 {
		rows, err := h.Queries.GetUsersByIDs(ctx, userIDs)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load allowed users")
			return
		}
		for _, u := range rows {
			users[u.ID] = AdminConnectorUserRef{UserID: uuidToString(u.ID), Name: u.Name, Email: u.Email}
		}
	}
	for _, p := range policies {
		row := get(p.Service)
		for _, id := range p.AllowedUserIds {
			ref, ok := users[id]
			if !ok {
				// The account was deleted; keep the id so the operator sees
				// the stale entry and can remove it.
				ref = AdminConnectorUserRef{UserID: uuidToString(id)}
			}
			row.AllowedUsers = append(row.AllowedUsers, ref)
		}
	}

	counts, err := h.Queries.AdminCountConnectorConnectionsByService(ctx, openconnector.ProviderName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count connections")
		return
	}
	for _, c := range counts {
		row := get(c.Service)
		row.Connections = AdminConnectorCounts{Active: c.ActiveCount, Reauth: c.ReauthCount, Revoked: c.RevokedCount, Users: c.UserCount}
	}

	for _, row := range byService {
		resp.Items = append(resp.Items, *row)
	}
	sort.Slice(resp.Items, func(i, j int) bool {
		return strings.ToLower(resp.Items[i].Name) < strings.ToLower(resp.Items[j].Name)
	})
	writeJSON(w, http.StatusOK, resp)
}

// AdminConnectorUserResponse is one user's connection footprint.
type AdminConnectorUserResponse struct {
	UserID          string               `json:"user_id"`
	Name            string               `json:"name"`
	Email           string               `json:"email"`
	DisabledAt      *string              `json:"disabled_at"`
	Services        []string             `json:"services"`
	Connections     AdminConnectorCounts `json:"connections"`
	LastConnectedAt *string              `json:"last_connected_at"`
}

// AdminOpenConnectorListUsers (GET /api/admin/open-connector/users) pages
// through users who hold at least one connection, optionally narrowed to one
// service (?service=) or a name / email search (?q=).
func (h *Handler) AdminOpenConnectorListUsers(w http.ResponseWriter, r *http.Request) {
	limit, offset, page := adminPaging(r)
	service := optionalText(strings.ToLower(r.URL.Query().Get("service")))
	q := optionalText(r.URL.Query().Get("q"))
	rows, err := h.Queries.AdminListConnectorUsers(r.Context(), db.AdminListConnectorUsersParams{
		Provider: openconnector.ProviderName, Service: service, Q: q, PageLimit: limit, PageOffset: offset,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list connector users")
		return
	}
	total, err := h.Queries.AdminCountConnectorUsers(r.Context(), db.AdminCountConnectorUsersParams{
		Provider: openconnector.ProviderName, Service: service, Q: q,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count connector users")
		return
	}
	items := make([]AdminConnectorUserResponse, 0, len(rows))
	for _, u := range rows {
		services := u.Services
		if services == nil {
			services = []string{}
		}
		items = append(items, AdminConnectorUserResponse{
			UserID: uuidToString(u.UserID), Name: u.Name, Email: u.Email, DisabledAt: timestampToPtr(u.DisabledAt),
			Services:        services,
			Connections:     AdminConnectorCounts{Active: u.ActiveCount, Reauth: u.ReauthCount, Revoked: u.RevokedCount},
			LastConnectedAt: timestampToPtr(u.LastConnectedAt),
		})
	}
	writeJSON(w, http.StatusOK, AdminPage[AdminConnectorUserResponse]{Items: items, Total: total, Page: page, PageSize: int(limit)})
}

// adminConnectorService validates the {service} route param. Service names
// are open-connector slugs: lowercase letters, digits, `_` and `-`.
func adminConnectorService(w http.ResponseWriter, r *http.Request) (string, bool) {
	service := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "service")))
	if service == "" || len(service) > 100 {
		writeError(w, http.StatusBadRequest, "service is required")
		return "", false
	}
	for _, c := range service {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			writeError(w, http.StatusBadRequest, "invalid service name")
			return "", false
		}
	}
	return service, true
}

// AdminConnectorPolicyResponse is the stored policy of one service, returned
// by every write so the UI can replace the row without a refetch.
type AdminConnectorPolicyResponse struct {
	Service      string                  `json:"service"`
	Enabled      bool                    `json:"enabled"`
	AllowedTools []string                `json:"allowed_tools"`
	AllowedUsers []AdminConnectorUserRef `json:"allowed_users"`
	UpdatedAt    *string                 `json:"updated_at"`
}

func (h *Handler) adminConnectorPolicyResponse(q *db.Queries, r *http.Request, p db.ConnectorServicePolicy) (AdminConnectorPolicyResponse, error) {
	resp := AdminConnectorPolicyResponse{
		Service: p.Service, Enabled: p.Enabled,
		AllowedTools: append([]string{}, p.AllowedTools...),
		AllowedUsers: []AdminConnectorUserRef{},
		UpdatedAt:    timestampToPtr(p.UpdatedAt),
	}
	if len(p.AllowedUserIds) == 0 {
		return resp, nil
	}
	rows, err := q.GetUsersByIDs(r.Context(), p.AllowedUserIds)
	if err != nil {
		return resp, err
	}
	byID := make(map[pgtype.UUID]db.GetUsersByIDsRow, len(rows))
	for _, u := range rows {
		byID[u.ID] = u
	}
	for _, id := range p.AllowedUserIds {
		u := byID[id]
		resp.AllowedUsers = append(resp.AllowedUsers, AdminConnectorUserRef{UserID: uuidToString(id), Name: u.Name, Email: u.Email})
	}
	return resp, nil
}

func (h *Handler) AdminOpenConnectorEnableService(w http.ResponseWriter, r *http.Request) {
	h.adminSetConnectorServiceEnabled(w, r, true)
}

func (h *Handler) AdminOpenConnectorDisableService(w http.ResponseWriter, r *http.Request) {
	h.adminSetConnectorServiceEnabled(w, r, false)
}

// adminSetConnectorServiceEnabled publishes or delists a service. Enabling
// requires the instance to offer the service right now, so an operator cannot
// publish a slug users could never connect; disabling always works, including
// for a service the instance no longer lists.
func (h *Handler) adminSetConnectorServiceEnabled(w http.ResponseWriter, r *http.Request, enable bool) {
	service, ok := adminConnectorService(w, r)
	if !ok {
		return
	}
	var req adminReasonRequest
	reason, ok := readAdminReason(w, r, &req)
	if !ok {
		return
	}
	name := service
	if enable {
		if !h.openConnectorWired() {
			writeFeatureDisabled(w, "open_connector_not_configured", "open-connector integration not configured")
			return
		}
		apps, err := h.OpenConnector.Catalog(r.Context())
		if err != nil {
			writeError(w, http.StatusBadGateway, "failed to load the open-connector catalog")
			return
		}
		found := false
		for _, a := range apps {
			if strings.ToLower(a.Service) == service {
				found, name = true, a.Name
				break
			}
		}
		if !found {
			writeError(w, http.StatusNotFound, "service is not offered by the open-connector instance")
			return
		}
	}
	action := "connector_service.disable"
	if enable {
		action = "connector_service.enable"
	}
	actor, _ := PlatformAdminFromContext(r.Context())
	h.runAdminAction(w, r, adminAction{
		Action: action, TargetType: adminConnectorTargetType, TargetID: service, Reason: reason,
		Payload: map[string]any{"provider": openconnector.ProviderName, "name": name},
	}, func(q *db.Queries) (int, any, error) {
		p, err := q.SetConnectorServiceEnabled(r.Context(), db.SetConnectorServiceEnabledParams{
			Provider: openconnector.ProviderName, Service: service, Enabled: enable, UpdatedBy: actor.UserID,
		})
		if err != nil {
			return 0, nil, err
		}
		resp, err := h.adminConnectorPolicyResponse(q, r, p)
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, resp, nil
	})
}

type setConnectorPolicyRequest struct {
	AllowedTools      []string `json:"allowed_tools"`
	AllowedUserEmails []string `json:"allowed_user_emails"`
	Reason            string   `json:"reason"`
}

func (r *setConnectorPolicyRequest) reasonValue() string { return r.Reason }

// AdminOpenConnectorSetPolicy (PUT /api/admin/open-connector/services/{service}/policy)
// replaces a service's tool and user allowlists. Users are addressed by
// email, like platform admins, and every email must belong to an existing
// account: a typo must fail loudly rather than silently allow nobody.
func (h *Handler) AdminOpenConnectorSetPolicy(w http.ResponseWriter, r *http.Request) {
	service, ok := adminConnectorService(w, r)
	if !ok {
		return
	}
	var req setConnectorPolicyRequest
	reason, ok := readAdminReason(w, r, &req)
	if !ok {
		return
	}
	tools, err := openconnector.NormalizeAllowedTools(service, req.AllowedTools)
	if err != nil {
		if errors.Is(err, openconnector.ErrInvalidInput) {
			writeError(w, http.StatusBadRequest, "allowed_tools contains an invalid action name or too many entries")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to validate allowed_tools")
		return
	}
	if len(req.AllowedUserEmails) > maxAllowedUsers {
		writeError(w, http.StatusBadRequest, "too many allowed users")
		return
	}
	userIDs := make([]pgtype.UUID, 0, len(req.AllowedUserEmails))
	emails := make([]string, 0, len(req.AllowedUserEmails))
	seen := map[string]struct{}{}
	for _, raw := range req.AllowedUserEmails {
		email := strings.ToLower(strings.TrimSpace(raw))
		if email == "" {
			continue
		}
		if _, dup := seen[email]; dup {
			continue
		}
		seen[email] = struct{}{}
		user, err := h.Queries.GetUserByEmail(r.Context(), email)
		if err != nil {
			if isNotFound(err) {
				writeError(w, http.StatusNotFound, "no user with email "+email+"; they must sign in once first")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to load user")
			return
		}
		userIDs = append(userIDs, user.ID)
		emails = append(emails, user.Email)
	}
	actor, _ := PlatformAdminFromContext(r.Context())
	h.runAdminAction(w, r, adminAction{
		Action: "connector_service.policy", TargetType: adminConnectorTargetType, TargetID: service, Reason: reason,
		Payload: map[string]any{"provider": openconnector.ProviderName, "allowed_tools": tools, "allowed_user_emails": emails},
	}, func(q *db.Queries) (int, any, error) {
		p, err := q.SetConnectorServicePolicy(r.Context(), db.SetConnectorServicePolicyParams{
			Provider: openconnector.ProviderName, Service: service,
			AllowedTools: tools, AllowedUserIds: userIDs, UpdatedBy: actor.UserID,
		})
		if err != nil {
			return 0, nil, err
		}
		resp, err := h.adminConnectorPolicyResponse(q, r, p)
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, resp, nil
	})
}
