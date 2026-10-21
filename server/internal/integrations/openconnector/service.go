// Package openconnector is the business-integration glue between Multica and
// a self-hosted open-connector (server/pkg/openconnector). It owns Multica
// semantics: the signed-state OAuth handshake, API key connections, the local
// user_connector_connection mirror, and the per-task runtime token that is
// mounted into agent runs as an MCP server.
//
// Security invariants: the admin token lives only inside the SDK client;
// API keys are forwarded to open-connector and never stored or logged; the
// callback trusts only the signed state and server-side lookups.
package openconnector

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/openconnector"
)

// ProviderName is the ConnectorProvider name and the user_connector_connection
// provider value.
const ProviderName = "open-connector"

// Service-level errors surfaced to the handler layer.
var (
	// ErrServiceNotSupported: the service is not connectable with the requested
	// auth type (no configured OAuth client, or not an allowed API key service).
	ErrServiceNotSupported = errors.New("open-connector: service not supported")
	// ErrConnectBusy: another user's OAuth flow for the same service is still
	// pending. open-connector has a single owner, so a second connect would
	// supersede the first; the caller should ask the user to retry shortly.
	ErrConnectBusy = errors.New("open-connector: another connection for this service is in progress")
	// ErrConnectNotSuccessful: the connection request did not end in connected.
	ErrConnectNotSuccessful = errors.New("open-connector: connection was not successful")
	// ErrConnectionNotFound: the connection id does not belong to the user.
	ErrConnectionNotFound = errors.New("open-connector: connection not found")
	// ErrInvalidInput: a request field is missing or out of range.
	ErrInvalidInput = errors.New("open-connector: invalid input")
)

const (
	appsCacheTTL       = 5 * time.Minute
	defaultPollEvery   = 2 * time.Second
	maxCallbackRetries = 3
	maxLabelLen        = 100

	callbackPath = "/api/integrations/open-connector/callback"

	// Connection statuses stored in user_connector_connection.
	statusActive         = "active"
	statusReauthRequired = "reauth_required"

	authTypeOAuth  = "oauth"
	authTypeAPIKey = "api_key"
)

// defaultAPIKeyServices is the fixed candidate list of services offered for
// API key connections. Checking every provider's setup would cost ~1650
// calls, so only these (that also list api_key in the catalog) are offered.
var defaultAPIKeyServices = []string{
	"airtable", "anthropic", "openai", "sendgrid", "stripe", "17track",
}

// SDK is the subset of *openconnector.Client the service depends on, declared
// as an interface so tests can inject a fake.
type SDK interface {
	ListProviders(ctx context.Context) ([]openconnector.Provider, error)
	ConfiguredOAuthServices(ctx context.Context) (map[string]struct{}, error)
	GetProviderSetup(ctx context.Context, service string) (*openconnector.ProviderSetup, error)
	BeginOAuthConnect(ctx context.Context, service string, in openconnector.BeginOAuthConnectRequest) (*openconnector.ConnectionRequest, error)
	GetConnectionRequest(ctx context.Context, id string) (*openconnector.ConnectionRequestStatus, error)
	ConnectWithAPIKey(ctx context.Context, service string, in openconnector.APIKeyConnectRequest) (*openconnector.Connection, error)
	GetConnectionByID(ctx context.Context, appID string) (*openconnector.Connection, error)
	ListConnections(ctx context.Context, status string) ([]openconnector.Connection, error)
	DeleteConnection(ctx context.Context, service, connectionName string, revoke bool) error
	CreateRuntimeToken(ctx context.Context, in openconnector.CreateRuntimeTokenRequest) (*openconnector.CreatedRuntimeToken, error)
	DeleteRuntimeToken(ctx context.Context, id string) error
}

var _ SDK = (*openconnector.Client)(nil)

// Store is the persistence seam for the local connection mirror. *db.Queries
// satisfies it; tests use an in-memory fake.
type Store interface {
	UpsertUserConnectorConnection(ctx context.Context, arg db.UpsertUserConnectorConnectionParams) (db.UserConnectorConnection, error)
	ListUserConnectorConnections(ctx context.Context, arg db.ListUserConnectorConnectionsParams) ([]db.UserConnectorConnection, error)
	GetUserConnectorConnection(ctx context.Context, arg db.GetUserConnectorConnectionParams) (db.UserConnectorConnection, error)
	MarkUserConnectorConnectionRevoked(ctx context.Context, arg db.MarkUserConnectorConnectionRevokedParams) error
	SetUserConnectorConnectionDefault(ctx context.Context, arg db.SetUserConnectorConnectionDefaultParams) error
	UpdateUserConnectorConnectionLabel(ctx context.Context, arg db.UpdateUserConnectorConnectionLabelParams) (db.UserConnectorConnection, error)
	UpdateUserConnectorConnectionStatus(ctx context.Context, arg db.UpdateUserConnectorConnectionStatusParams) error
	ListUserConnectorConnectionsByProviderAndStatus(ctx context.Context, arg db.ListUserConnectorConnectionsByProviderAndStatusParams) ([]db.UserConnectorConnection, error)
	InsertConnectorOAuthState(ctx context.Context, arg db.InsertConnectorOAuthStateParams) error
	ConsumeConnectorOAuthState(ctx context.Context, arg db.ConsumeConnectorOAuthStateParams) (db.ConnectorOauthState, error)
	ExistsPendingConnectorOAuthForService(ctx context.Context, arg db.ExistsPendingConnectorOAuthForServiceParams) (bool, error)
}

var _ Store = (*db.Queries)(nil)

// Config configures a Service.
type Config struct {
	// StateSecret signs the connect-state HMAC. Required.
	StateSecret []byte
	// CallbackBaseURL is the public base URL of this API without trailing
	// slash. Required.
	CallbackBaseURL string
	// FrontendBaseURL builds the post-callback browser redirect; empty yields
	// a site-relative path.
	FrontendBaseURL string
	// MCPURL is the open-connector MCP endpoint mounted into agent runs
	// (BASE_URL + "/mcp"). Required.
	MCPURL string
	// ScopeOverrides maps a service to the authorizationOptionIds to request,
	// replacing the computed minimal default.
	ScopeOverrides map[string][]string
	// APIKeyServices overrides the candidate list of API key services. Nil
	// uses the built-in list.
	APIKeyServices []string
	// PollInterval is the wait between callback status polls. Zero uses 2s.
	PollInterval time.Duration
	// Now is overridable for deterministic tests. Nil uses time.Now.
	Now func() time.Time
}

// Service is the open-connector business-integration service. It also
// implements runtimeapps.ConnectorProvider (see dispatch.go).
type Service struct {
	sdk         SDK
	store       Store
	secret      []byte
	callbackURL string
	frontendURL string
	mcpURL      string
	scopes      map[string][]string
	apiKeySet   map[string]struct{}
	pollEvery   time.Duration
	now         func() time.Time

	appsMu  sync.Mutex
	apps    []AppView
	appsExp time.Time

	// serviceLocks is a per-process fast path that serializes BeginOAuthConnect
	// per service. Cross-replica busy detection and callback correlation live
	// in the connector_oauth_state table.
	serviceLocks sync.Map // service -> *sync.Mutex
}

// NewService validates its inputs so a misconfigured boot fails loudly.
func NewService(client SDK, store Store, cfg Config) (*Service, error) {
	if client == nil {
		return nil, errors.New("open-connector: SDK client is required")
	}
	if store == nil {
		return nil, errors.New("open-connector: store is required")
	}
	if len(cfg.StateSecret) == 0 {
		return nil, errors.New("open-connector: StateSecret is required")
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.CallbackBaseURL), "/")
	if base == "" {
		return nil, errors.New("open-connector: CallbackBaseURL is required")
	}
	mcpURL := strings.TrimSpace(cfg.MCPURL)
	if mcpURL == "" {
		return nil, errors.New("open-connector: MCPURL is required")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	poll := cfg.PollInterval
	if poll <= 0 {
		poll = defaultPollEvery
	}
	keys := cfg.APIKeyServices
	if keys == nil {
		keys = defaultAPIKeyServices
	}
	apiKeySet := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		apiKeySet[normalizeService(k)] = struct{}{}
	}
	return &Service{
		sdk:         client,
		store:       store,
		secret:      cfg.StateSecret,
		callbackURL: base + callbackPath,
		frontendURL: strings.TrimRight(strings.TrimSpace(cfg.FrontendBaseURL), "/"),
		mcpURL:      mcpURL,
		scopes:      cfg.ScopeOverrides,
		apiKeySet:   apiKeySet,
		pollEvery:   poll,
		now:         now,
	}, nil
}

func normalizeService(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// AppView is the API-facing descriptor of one connectable service.
type AppView struct {
	Service string `json:"service"`
	Name    string `json:"name"`
	Logo    string `json:"logo,omitempty"`
	// AuthTypes holds "oauth" and/or "api_key".
	AuthTypes []string `json:"auth_types"`
}

// ConnectionView is the API-facing view of a local connection row. The
// open-connector connection id and alias are server-internal.
type ConnectionView struct {
	ID          string  `json:"id"`
	Service     string  `json:"service"`
	Label       string  `json:"label"`
	AuthType    string  `json:"auth_type"`
	Status      string  `json:"status"`
	IsDefault   bool    `json:"is_default"`
	ConnectedAt string  `json:"connected_at"`
	LastUsedAt  *string `json:"last_used_at"`
}

func rowToView(row db.UserConnectorConnection) ConnectionView {
	v := ConnectionView{
		ID:        util.UUIDToString(row.ID),
		Service:   row.Service,
		Label:     row.Label,
		AuthType:  row.AuthType,
		Status:    row.Status,
		IsDefault: row.IsDefault,
	}
	if row.ConnectedAt.Valid {
		v.ConnectedAt = row.ConnectedAt.Time.UTC().Format(time.RFC3339)
	}
	if row.LastUsedAt.Valid {
		t := row.LastUsedAt.Time.UTC().Format(time.RFC3339)
		v.LastUsedAt = &t
	}
	return v
}

// ListApps returns the services a user can connect: catalog providers that
// support OAuth with a configured client, or API key from the fixed candidate
// list. The result is cached for five minutes.
func (s *Service) ListApps(ctx context.Context) ([]AppView, error) {
	s.appsMu.Lock()
	defer s.appsMu.Unlock()
	if s.apps != nil && s.now().Before(s.appsExp) {
		return append([]AppView(nil), s.apps...), nil
	}
	providers, err := s.sdk.ListProviders(ctx)
	if err != nil {
		return nil, fmt.Errorf("open-connector: list providers: %w", err)
	}
	configured, err := s.sdk.ConfiguredOAuthServices(ctx)
	if err != nil {
		return nil, fmt.Errorf("open-connector: list oauth configs: %w", err)
	}
	apps := make([]AppView, 0, len(configured)+len(s.apiKeySet))
	for _, p := range providers {
		service := normalizeService(p.Service)
		types := []string{}
		if _, ok := configured[p.Service]; ok && p.SupportsAuthType(openconnector.AuthTypeOAuth2) {
			types = append(types, authTypeOAuth)
		}
		if _, ok := s.apiKeySet[service]; ok && p.SupportsAuthType(openconnector.AuthTypeAPIKey) {
			types = append(types, authTypeAPIKey)
		}
		if len(types) == 0 {
			continue
		}
		app := AppView{Service: p.Service, Name: p.DisplayName, AuthTypes: types}
		if app.Name == "" {
			app.Name = p.Service
		}
		if p.IconURL != nil {
			app.Logo = *p.IconURL
		}
		apps = append(apps, app)
	}
	sort.Slice(apps, func(i, j int) bool {
		return strings.ToLower(apps[i].Name) < strings.ToLower(apps[j].Name)
	})
	s.apps, s.appsExp = apps, s.now().Add(appsCacheTTL)
	return append([]AppView(nil), apps...), nil
}

// supports reports whether service is connectable with authType.
func (s *Service) supports(ctx context.Context, service, authType string) (bool, error) {
	apps, err := s.ListApps(ctx)
	if err != nil {
		return false, err
	}
	for _, a := range apps {
		if normalizeService(a.Service) != service {
			continue
		}
		for _, t := range a.AuthTypes {
			if t == authType {
				return true, nil
			}
		}
	}
	return false, nil
}

func (s *Service) serviceLock(service string) *sync.Mutex {
	m, _ := s.serviceLocks.LoadOrStore(service, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// authorizationOptionIDs returns the scopes to request: the operator override,
// else the minimal default (required or default-selected, never destructive).
// The result is never nil because omitting the list would request every scope.
func (s *Service) authorizationOptionIDs(ctx context.Context, service string) ([]string, error) {
	if ids, ok := s.scopes[service]; ok {
		return append([]string{}, ids...), nil
	}
	setup, err := s.sdk.GetProviderSetup(ctx, service)
	if err != nil {
		return nil, fmt.Errorf("open-connector: get provider setup: %w", err)
	}
	auth, ok := setup.FindAuth(openconnector.AuthTypeOAuth2)
	if !ok {
		return nil, ErrServiceNotSupported
	}
	return auth.DefaultAuthorizationOptionIDs(), nil
}

// BeginOAuthConnect starts an OAuth flow and returns the authorization URL to
// send the user's browser to. Flows are serialized per service: open-connector
// has a single owner, so a second connect for the same service would supersede
// the first user's pending request. A pending flow of a different user yields
// ErrConnectBusy until it completes or its state expires. The serialization is
// process-local (single API replica).
func (s *Service) BeginOAuthConnect(ctx context.Context, userID pgtype.UUID, service string) (string, error) {
	service = normalizeService(service)
	if !userID.Valid {
		return "", errors.New("open-connector: invalid user id")
	}
	if service == "" {
		return "", ErrInvalidInput
	}
	ok, err := s.supports(ctx, service, authTypeOAuth)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrServiceNotSupported
	}
	optionIDs, err := s.authorizationOptionIDs(ctx, service)
	if err != nil {
		return "", err
	}

	lock := s.serviceLock(service)
	lock.Lock()
	defer lock.Unlock()

	uid := util.UUIDToString(userID)
	now := s.now()
	busy, err := s.store.ExistsPendingConnectorOAuthForService(ctx, db.ExistsPendingConnectorOAuthForServiceParams{
		Provider: ProviderName, Service: service, Now: pgtype.Timestamptz{Time: now, Valid: true}, UserID: userID,
	})
	if err != nil {
		return "", fmt.Errorf("open-connector: check pending connect: %w", err)
	}
	if busy {
		return "", ErrConnectBusy
	}

	nonce := util.UUIDToString(dbid.NewV7()) + util.UUIDToString(dbid.NewV7())
	exp := now.Add(stateTTL)
	state, err := signState(s.secret, stateClaims{UserID: uid, Service: service, Nonce: nonce, Exp: exp.Unix()})
	if err != nil {
		return "", fmt.Errorf("open-connector: sign state: %w", err)
	}
	resp, err := s.sdk.BeginOAuthConnect(ctx, service, openconnector.BeginOAuthConnectRequest{
		ReturnURI:              s.callbackURL + "?state=" + url.QueryEscape(state),
		AuthorizationOptionIDs: optionIDs,
	})
	if err != nil {
		return "", fmt.Errorf("open-connector: begin oauth connect: %w", err)
	}
	if resp == nil || resp.AuthorizationURL == "" || resp.ConnectionRequestID == "" {
		return "", errors.New("open-connector: begin oauth connect: empty response")
	}
	if !resp.ExpiresAt.IsZero() && resp.ExpiresAt.Before(exp) {
		exp = resp.ExpiresAt
	}
	// The connect call and this insert are not atomic. If the process dies in
	// between, open-connector holds a request we have no row for: the callback
	// then fails (ErrConnectNotSuccessful) and the user retries. A failed insert
	// likewise only costs this attempt; the worst case for other users is a
	// transient false "busy" until the row expires.
	if err := s.store.InsertConnectorOAuthState(ctx, db.InsertConnectorOAuthStateParams{
		NonceHash: hashNonce(nonce), Provider: ProviderName, UserID: userID, Service: service,
		RequestID: resp.ConnectionRequestID, ExpiresAt: pgtype.Timestamptz{Time: exp, Valid: true},
	}); err != nil {
		return "", fmt.Errorf("open-connector: save connect state: %w", err)
	}
	return resp.AuthorizationURL, nil
}

func hashNonce(nonce string) []byte {
	h := sha256.Sum256([]byte(nonce))
	return h[:]
}

func (s *Service) wait(ctx context.Context) error {
	t := time.NewTimer(s.pollEvery)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// CompleteCallback finishes an OAuth flow. It trusts only the signed state and
// the connection request fetched from open-connector, never the callback's own
// query. It returns the service named by the state so the handler can build the
// redirect even on failure. A request still initiated is polled up to three
// more times.
func (s *Service) CompleteCallback(ctx context.Context, state string) (string, error) {
	claims, err := verifyState(s.secret, state, s.now())
	if err != nil {
		return "", err
	}
	userID, err := util.ParseUUID(claims.UserID)
	if err != nil {
		return claims.Service, fmt.Errorf("open-connector: state has invalid user id: %w", err)
	}
	// Single-use: a replayed, unknown or expired nonce yields no row.
	pending, err := s.store.ConsumeConnectorOAuthState(ctx, db.ConsumeConnectorOAuthStateParams{
		NonceHash: hashNonce(claims.Nonce), Now: pgtype.Timestamptz{Time: s.now(), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return claims.Service, ErrConnectNotSuccessful
	}
	if err != nil {
		return claims.Service, fmt.Errorf("open-connector: consume connect state: %w", err)
	}
	if pending.Provider != ProviderName || pending.UserID != userID || pending.Service != claims.Service {
		return claims.Service, ErrConnectNotSuccessful
	}

	var req *openconnector.ConnectionRequestStatus
	for attempt := 0; ; attempt++ {
		req, err = s.sdk.GetConnectionRequest(ctx, pending.RequestID)
		if err != nil {
			return claims.Service, fmt.Errorf("open-connector: get connection request: %w", err)
		}
		if req.Status != openconnector.RequestStatusInitiated {
			break
		}
		if attempt >= maxCallbackRetries {
			return claims.Service, ErrConnectNotSuccessful
		}
		if err := s.wait(ctx); err != nil {
			return claims.Service, err
		}
	}
	if req.Status != openconnector.RequestStatusConnected || req.AppID == nil || *req.AppID == "" {
		return claims.Service, ErrConnectNotSuccessful
	}
	conn, err := s.sdk.GetConnectionByID(ctx, *req.AppID)
	if err != nil {
		return claims.Service, fmt.Errorf("open-connector: get connection: %w", err)
	}
	if normalizeService(conn.Service) != claims.Service {
		return claims.Service, ErrConnectNotSuccessful
	}
	if _, err := s.saveConnection(ctx, userID, claims.Service, conn, ""); err != nil {
		return claims.Service, err
	}
	return claims.Service, nil
}

// saveConnection upserts the mirror row. The user's first live connection of a
// service becomes its default.
func (s *Service) saveConnection(ctx context.Context, userID pgtype.UUID, service string, conn *openconnector.Connection, label string) (db.UserConnectorConnection, error) {
	rows, err := s.store.ListUserConnectorConnections(ctx, db.ListUserConnectorConnectionsParams{Provider: ProviderName, UserID: userID})
	if err != nil {
		return db.UserConnectorConnection{}, fmt.Errorf("open-connector: list connections: %w", err)
	}
	first := true
	for _, r := range rows {
		if r.Service == service && r.ConnectionID != conn.ID {
			first = false
			break
		}
	}
	authType := authTypeOAuth
	if conn.AuthType == openconnector.AuthTypeAPIKey {
		authType = authTypeAPIKey
	}
	if label == "" {
		label = conn.AccountLabel
	}
	row, err := s.store.UpsertUserConnectorConnection(ctx, db.UpsertUserConnectorConnectionParams{
		Provider:       ProviderName,
		UserID:         userID,
		Service:        service,
		ConnectionID:   conn.ID,
		ConnectionName: conn.ConnectionName,
		AuthType:       authType,
		Label:          truncateRunes(strings.TrimSpace(label), maxLabelLen),
		IsDefault:      first,
	})
	if err != nil {
		return db.UserConnectorConnection{}, fmt.Errorf("open-connector: save connection: %w", err)
	}
	return row, nil
}

// ConnectWithAPIKey creates an API key connection. apiKey and extra are
// forwarded to open-connector only: they are never stored, logged, or included
// in a returned error.
func (s *Service) ConnectWithAPIKey(ctx context.Context, userID pgtype.UUID, service, apiKey string, extra map[string]string, label string) (ConnectionView, error) {
	service = normalizeService(service)
	if !userID.Valid {
		return ConnectionView{}, errors.New("open-connector: invalid user id")
	}
	if service == "" || strings.TrimSpace(apiKey) == "" {
		return ConnectionView{}, ErrInvalidInput
	}
	label = strings.TrimSpace(label)
	if len([]rune(label)) > maxLabelLen {
		return ConnectionView{}, ErrInvalidInput
	}
	ok, err := s.supports(ctx, service, authTypeAPIKey)
	if err != nil {
		return ConnectionView{}, err
	}
	if !ok {
		return ConnectionView{}, ErrServiceNotSupported
	}
	conn, err := s.sdk.ConnectWithAPIKey(ctx, service, openconnector.APIKeyConnectRequest{
		APIKey: apiKey, Extra: extra, Comment: label,
	})
	if err != nil {
		return ConnectionView{}, redact(fmt.Errorf("open-connector: connect with api key: %w", err), apiKey, extra)
	}
	if conn == nil || conn.ID == "" {
		return ConnectionView{}, errors.New("open-connector: connect with api key: empty response")
	}
	row, err := s.saveConnection(ctx, userID, service, conn, label)
	if err != nil {
		return ConnectionView{}, err
	}
	return rowToView(row), nil
}

// redact drops the wrapped chain if the message contains a secret value, so a
// server that echoes input can never leak a key through an error string.
func redact(err error, apiKey string, extra map[string]string) error {
	msg := err.Error()
	leaks := strings.Contains(msg, apiKey)
	for _, v := range extra {
		if v != "" && strings.Contains(msg, v) {
			leaks = true
		}
	}
	if !leaks {
		return err
	}
	return errors.New("open-connector: connect with api key failed (details withheld)")
}

// ListConnections returns the user's non-revoked connections.
func (s *Service) ListConnections(ctx context.Context, userID pgtype.UUID) ([]ConnectionView, error) {
	rows, err := s.store.ListUserConnectorConnections(ctx, db.ListUserConnectorConnectionsParams{Provider: ProviderName, UserID: userID})
	if err != nil {
		return nil, fmt.Errorf("open-connector: list connections: %w", err)
	}
	out := make([]ConnectionView, 0, len(rows))
	for _, r := range rows {
		out = append(out, rowToView(r))
	}
	return out, nil
}

func (s *Service) getOwned(ctx context.Context, userID, id pgtype.UUID) (db.UserConnectorConnection, error) {
	row, err := s.store.GetUserConnectorConnection(ctx, db.GetUserConnectorConnectionParams{ID: id, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.UserConnectorConnection{}, ErrConnectionNotFound
	}
	if err != nil {
		return db.UserConnectorConnection{}, fmt.Errorf("open-connector: get connection: %w", err)
	}
	if row.Provider != ProviderName {
		return db.UserConnectorConnection{}, ErrConnectionNotFound
	}
	return row, nil
}

// SetDefault makes one active connection the default of its service for the user.
func (s *Service) SetDefault(ctx context.Context, userID, id pgtype.UUID) (ConnectionView, error) {
	row, err := s.getOwned(ctx, userID, id)
	if err != nil {
		return ConnectionView{}, err
	}
	if row.Status != statusActive {
		return ConnectionView{}, ErrConnectionNotFound
	}
	if err := s.store.SetUserConnectorConnectionDefault(ctx, db.SetUserConnectorConnectionDefaultParams{
		ID: row.ID, Provider: ProviderName, UserID: userID, Service: row.Service,
	}); err != nil {
		return ConnectionView{}, fmt.Errorf("open-connector: set default: %w", err)
	}
	row.IsDefault = true
	return rowToView(row), nil
}

// UpdateLabel renames a connection.
func (s *Service) UpdateLabel(ctx context.Context, userID, id pgtype.UUID, label string) (ConnectionView, error) {
	label = strings.TrimSpace(label)
	if len([]rune(label)) > maxLabelLen {
		return ConnectionView{}, ErrInvalidInput
	}
	if _, err := s.getOwned(ctx, userID, id); err != nil {
		return ConnectionView{}, err
	}
	row, err := s.store.UpdateUserConnectorConnectionLabel(ctx, db.UpdateUserConnectorConnectionLabelParams{
		Label: label, ID: id, UserID: userID,
	})
	if err != nil {
		return ConnectionView{}, fmt.Errorf("open-connector: update label: %w", err)
	}
	return rowToView(row), nil
}

// Disconnect deletes the connection in open-connector (revoking upstream
// credentials) and marks the local row revoked. It is idempotent: an already
// revoked row returns nil. When the default connection is removed, the oldest
// remaining active connection of the service becomes the default.
func (s *Service) Disconnect(ctx context.Context, userID, id pgtype.UUID) error {
	row, err := s.getOwned(ctx, userID, id)
	if err != nil {
		return err
	}
	if row.Status == "revoked" {
		return nil
	}
	if err := s.sdk.DeleteConnection(ctx, row.Service, row.ConnectionName, true); err != nil {
		return fmt.Errorf("open-connector: delete connection: %w", err)
	}
	if err := s.store.MarkUserConnectorConnectionRevoked(ctx, db.MarkUserConnectorConnectionRevokedParams{ID: row.ID, UserID: userID}); err != nil {
		return fmt.Errorf("open-connector: mark revoked: %w", err)
	}
	if !row.IsDefault {
		return nil
	}
	rows, err := s.store.ListUserConnectorConnections(ctx, db.ListUserConnectorConnectionsParams{Provider: ProviderName, UserID: userID})
	if err != nil {
		slog.Warn("open-connector: promote default after disconnect: list failed", "error", err)
		return nil
	}
	for _, r := range rows {
		if r.Service == row.Service && r.Status == statusActive && r.ID != row.ID {
			if err := s.store.SetUserConnectorConnectionDefault(ctx, db.SetUserConnectorConnectionDefaultParams{
				ID: r.ID, Provider: ProviderName, UserID: userID, Service: row.Service,
			}); err != nil {
				slog.Warn("open-connector: promote default after disconnect failed", "error", err)
			}
			break
		}
	}
	return nil
}

// SyncStatuses mirrors open-connector's connection status into local rows:
// reauth_required and active follow upstream, disconnected becomes revoked.
// Connections missing upstream are left alone. Intended for a periodic job.
func (s *Service) SyncStatuses(ctx context.Context) error {
	remote, err := s.sdk.ListConnections(ctx, "")
	if err != nil {
		return fmt.Errorf("open-connector: sync statuses: %w", err)
	}
	upstream := make(map[string]string, len(remote))
	for _, c := range remote {
		upstream[c.ID] = c.Status
	}
	var firstErr error
	for _, localStatus := range []string{statusActive, statusReauthRequired} {
		rows, err := s.store.ListUserConnectorConnectionsByProviderAndStatus(ctx, db.ListUserConnectorConnectionsByProviderAndStatusParams{
			Provider: ProviderName, Status: localStatus,
		})
		if err != nil {
			return fmt.Errorf("open-connector: sync statuses: %w", err)
		}
		for _, r := range rows {
			target := ""
			switch upstream[r.ConnectionID] {
			case openconnector.ConnectionStatusReauthRequired:
				target = statusReauthRequired
			case openconnector.ConnectionStatusActive:
				target = statusActive
			case openconnector.ConnectionStatusDisconnected:
				target = "revoked"
			}
			if target == "" || target == r.Status {
				continue
			}
			if err := s.store.UpdateUserConnectorConnectionStatus(ctx, db.UpdateUserConnectorConnectionStatusParams{ID: r.ID, Status: target}); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("open-connector: sync status of %s: %w", util.UUIDToString(r.ID), err)
			}
		}
	}
	return firstErr
}

// CallbackRedirect builds the browser redirect for the callback handler. It is
// the slug-less settings path; the web proxy prepends the workspace slug.
func (s *Service) CallbackRedirect(service string, success bool) string {
	var path string
	if success {
		path = "/settings?tab=integrations&connected=" + url.QueryEscape(service)
	} else {
		path = "/settings?tab=integrations&error=open_connector_connect_failed"
	}
	return s.frontendURL + path
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
