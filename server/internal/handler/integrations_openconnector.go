package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/featureflags"
	openconnector "github.com/multica-ai/multica/server/internal/integrations/openconnector"
)

// open-connector integration handlers. Connections belong to a user, not a
// workspace, so like Composio these live outside the workspace-membership
// group. All endpoints but the callback are user-scoped (requireUserID); the
// callback is a public route (outside the Auth group) whose identity comes
// from the signed state. A missing deployment capability (service not wired or
// open_connector_apps flag off) is a non-retryable 403 `open_connector_not_configured`,
// matching Composio and writeFeatureDisabled, not a transient 503.

const maxOpenConnectorBodyBytes = 64 << 10

// OpenConnectorConnectInitRequest is the POST /connect/init body.
type OpenConnectorConnectInitRequest struct {
	Service string `json:"service"`
}

// OpenConnectorConnectInitResponse carries the authorization URL the frontend
// redirects the user to.
type OpenConnectorConnectInitResponse struct {
	RedirectURL string `json:"redirect_url"`
}

// OpenConnectorAPIKeyRequest is the POST /connect/api-key body. api_key and
// extra values are forwarded upstream only; they are never echoed back.
type OpenConnectorAPIKeyRequest struct {
	Service string            `json:"service"`
	APIKey  string            `json:"api_key"`
	Extra   map[string]string `json:"extra,omitempty"`
	Label   string            `json:"label,omitempty"`
}

// OpenConnectorUpdateConnectionRequest is the PATCH /connections/{id} body.
type OpenConnectorUpdateConnectionRequest struct {
	IsDefault *bool   `json:"is_default"`
	Label     *string `json:"label"`
}

func (h *Handler) openConnectorAppsEnabled(ctx context.Context) bool {
	return featureflags.OpenConnectorAppsEnabled(ctx, h.FeatureFlags)
}

// openConnectorReady reports whether the integration is wired and enabled and
// writes the 403 otherwise.
func (h *Handler) openConnectorReady(w http.ResponseWriter, r *http.Request) bool {
	if h.OpenConnector == nil || !h.openConnectorAppsEnabled(r.Context()) {
		writeFeatureDisabled(w, "open_connector_not_configured", "open-connector integration not configured")
		return false
	}
	return true
}

// openConnectorUser runs the readiness gate and resolves the caller's UUID.
func (h *Handler) openConnectorUser(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	if !h.openConnectorReady(w, r) {
		return pgtype.UUID{}, false
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return pgtype.UUID{}, false
	}
	return parseUUIDOrBadRequest(w, userID, "user id")
}

func decodeOpenConnectorBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxOpenConnectorBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

// writeOpenConnectorError maps service errors to stable responses. Messages
// are fixed strings: upstream error text is logged server-side only (the
// service already withholds secrets from it) and never returned.
func writeOpenConnectorError(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	switch {
	case errors.Is(err, openconnector.ErrServiceNotSupported):
		writeErrorCode(w, http.StatusBadRequest, "open_connector_service_not_supported", "service not supported")
	case errors.Is(err, openconnector.ErrConnectBusy):
		writeErrorCode(w, http.StatusConflict, "open_connector_connect_busy", "another connection for this service is in progress, retry shortly")
	case errors.Is(err, openconnector.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid request")
	case errors.Is(err, openconnector.ErrConnectionNotFound):
		writeError(w, http.StatusNotFound, "open-connector connection not found")
	default:
		slog.Warn("open-connector request failed", "path", r.URL.Path, "error", err)
		writeError(w, http.StatusBadGateway, fallback)
	}
}

// OpenConnectorConnectInit (POST /api/integrations/open-connector/connect/init)
// starts an OAuth flow and returns the authorization URL.
func (h *Handler) OpenConnectorConnectInit(w http.ResponseWriter, r *http.Request) {
	userUUID, ok := h.openConnectorUser(w, r)
	if !ok {
		return
	}
	var req OpenConnectorConnectInitRequest
	if !decodeOpenConnectorBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Service) == "" {
		writeError(w, http.StatusBadRequest, "service is required")
		return
	}
	redirectURL, err := h.OpenConnector.BeginOAuthConnect(r.Context(), userUUID, req.Service)
	if err != nil {
		writeOpenConnectorError(w, r, err, "failed to start open-connector connect")
		return
	}
	writeJSON(w, http.StatusOK, OpenConnectorConnectInitResponse{RedirectURL: redirectURL})
}

// OpenConnectorConnectAPIKey (POST /api/integrations/open-connector/connect/api-key)
// creates an API key connection.
func (h *Handler) OpenConnectorConnectAPIKey(w http.ResponseWriter, r *http.Request) {
	userUUID, ok := h.openConnectorUser(w, r)
	if !ok {
		return
	}
	var req OpenConnectorAPIKeyRequest
	if !decodeOpenConnectorBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Service) == "" || strings.TrimSpace(req.APIKey) == "" {
		writeError(w, http.StatusBadRequest, "service and api_key are required")
		return
	}
	view, err := h.OpenConnector.ConnectWithAPIKey(r.Context(), userUUID, req.Service, req.APIKey, req.Extra, req.Label)
	if err != nil {
		// err is logged by writeOpenConnectorError; the service strips api_key
		// and extra values from it before it gets here.
		writeOpenConnectorError(w, r, err, "failed to connect with api key")
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

// ListOpenConnectorApps (GET /api/integrations/open-connector/apps) returns the
// connectable services. An upstream failure is a 502 so the UI shows its
// load-failed state rather than an empty catalog.
func (h *Handler) ListOpenConnectorApps(w http.ResponseWriter, r *http.Request) {
	if !h.openConnectorReady(w, r) {
		return
	}
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	apps, err := h.OpenConnector.ListApps(r.Context())
	if err != nil {
		writeOpenConnectorError(w, r, err, "failed to list open-connector apps")
		return
	}
	if apps == nil {
		apps = []openconnector.AppView{}
	}
	writeJSON(w, http.StatusOK, apps)
}

// ListOpenConnectorConnections (GET /api/integrations/open-connector/connections)
// returns the caller's non-revoked connections.
func (h *Handler) ListOpenConnectorConnections(w http.ResponseWriter, r *http.Request) {
	userUUID, ok := h.openConnectorUser(w, r)
	if !ok {
		return
	}
	conns, err := h.OpenConnector.ListConnections(r.Context(), userUUID)
	if err != nil {
		writeOpenConnectorError(w, r, err, "failed to list open-connector connections")
		return
	}
	if conns == nil {
		conns = []openconnector.ConnectionView{}
	}
	writeJSON(w, http.StatusOK, conns)
}

// UpdateOpenConnectorConnection (PATCH /api/integrations/open-connector/connections/{id})
// renames a connection and/or makes it the default of its service.
func (h *Handler) UpdateOpenConnectorConnection(w http.ResponseWriter, r *http.Request) {
	userUUID, ok := h.openConnectorUser(w, r)
	if !ok {
		return
	}
	connUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "connection id")
	if !ok {
		return
	}
	var req OpenConnectorUpdateConnectionRequest
	if !decodeOpenConnectorBody(w, r, &req) {
		return
	}
	if req.IsDefault == nil && req.Label == nil {
		writeError(w, http.StatusBadRequest, "is_default or label is required")
		return
	}
	if req.IsDefault != nil && !*req.IsDefault {
		// A service always keeps a default; pick another connection instead.
		writeError(w, http.StatusBadRequest, "is_default can only be set to true")
		return
	}
	var (
		view openconnector.ConnectionView
		err  error
	)
	// Label first: it validates input before the default flips.
	if req.Label != nil {
		if view, err = h.OpenConnector.UpdateLabel(r.Context(), userUUID, connUUID, *req.Label); err != nil {
			writeOpenConnectorError(w, r, err, "failed to update open-connector connection")
			return
		}
	}
	if req.IsDefault != nil {
		if view, err = h.OpenConnector.SetDefault(r.Context(), userUUID, connUUID); err != nil {
			writeOpenConnectorError(w, r, err, "failed to update open-connector connection")
			return
		}
	}
	writeJSON(w, http.StatusOK, view)
}

// DeleteOpenConnectorConnection (DELETE /api/integrations/open-connector/connections/{id})
// disconnects a connection the caller owns. Idempotent; a connection that does
// not belong to the caller is a 404.
func (h *Handler) DeleteOpenConnectorConnection(w http.ResponseWriter, r *http.Request) {
	userUUID, ok := h.openConnectorUser(w, r)
	if !ok {
		return
	}
	connUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "connection id")
	if !ok {
		return
	}
	if err := h.OpenConnector.Disconnect(r.Context(), userUUID, connUUID); err != nil {
		writeOpenConnectorError(w, r, err, "failed to disconnect open-connector connection")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// OpenConnectorCallback (GET /api/integrations/open-connector/callback) is the
// browser redirect target after the provider's consent screen. It is a PUBLIC
// route: identity comes solely from the HMAC-signed `state`, verified by
// CompleteCallback. Every failure collapses to the generic failure redirect so
// the browser never learns which check failed.
func (h *Handler) OpenConnectorCallback(w http.ResponseWriter, r *http.Request) {
	if !h.openConnectorReady(w, r) {
		return
	}
	service, err := h.OpenConnector.CompleteCallback(r.Context(), r.URL.Query().Get("state"))
	if err != nil {
		slog.Warn("open-connector callback failed", "error", err)
		http.Redirect(w, r, h.OpenConnector.CallbackRedirect(service, false), http.StatusFound)
		return
	}
	http.Redirect(w, r, h.OpenConnector.CallbackRedirect(service, true), http.StatusFound)
}
