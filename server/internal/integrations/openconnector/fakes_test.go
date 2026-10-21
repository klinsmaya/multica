package openconnector

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/openconnector"
)

type fakeSDK struct {
	mu sync.Mutex

	providers    []openconnector.Provider
	providersErr error
	configured   map[string]struct{}
	setup        map[string]*openconnector.ProviderSetup

	beginCalls   []openconnector.BeginOAuthConnectRequest
	beginResp    *openconnector.ConnectionRequest
	beginErr     error
	requestSeq   []openconnector.ConnectionRequestStatus // returned in order; last repeats
	requestCalls int
	apiKeyReq    openconnector.APIKeyConnectRequest
	apiKeyConn   *openconnector.Connection
	apiKeyErr    error
	connByID     map[string]*openconnector.Connection
	remote       []openconnector.Connection
	deleted      [][2]string

	tokenReqs   []openconnector.CreateRuntimeTokenRequest
	tokenResp   *openconnector.CreatedRuntimeToken
	tokenDelErr error
	tokenDels   []string
}

func (f *fakeSDK) ListProviders(context.Context) ([]openconnector.Provider, error) {
	return f.providers, f.providersErr
}
func (f *fakeSDK) ConfiguredOAuthServices(context.Context) (map[string]struct{}, error) {
	return f.configured, nil
}
func (f *fakeSDK) GetProviderSetup(_ context.Context, service string) (*openconnector.ProviderSetup, error) {
	if s, ok := f.setup[service]; ok {
		return s, nil
	}
	return nil, openconnector.ErrNotFound
}
func (f *fakeSDK) BeginOAuthConnect(_ context.Context, _ string, in openconnector.BeginOAuthConnectRequest) (*openconnector.ConnectionRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.beginCalls = append(f.beginCalls, in)
	return f.beginResp, f.beginErr
}
func (f *fakeSDK) GetConnectionRequest(context.Context, string) (*openconnector.ConnectionRequestStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.requestCalls
	if i >= len(f.requestSeq) {
		i = len(f.requestSeq) - 1
	}
	f.requestCalls++
	r := f.requestSeq[i]
	return &r, nil
}
func (f *fakeSDK) ConnectWithAPIKey(_ context.Context, _ string, in openconnector.APIKeyConnectRequest) (*openconnector.Connection, error) {
	f.apiKeyReq = in
	return f.apiKeyConn, f.apiKeyErr
}
func (f *fakeSDK) GetConnectionByID(_ context.Context, id string) (*openconnector.Connection, error) {
	if c, ok := f.connByID[id]; ok {
		return c, nil
	}
	return nil, openconnector.ErrNotFound
}
func (f *fakeSDK) ListConnections(context.Context, string) ([]openconnector.Connection, error) {
	return f.remote, nil
}
func (f *fakeSDK) DeleteConnection(_ context.Context, service, name string, _ bool) error {
	f.deleted = append(f.deleted, [2]string{service, name})
	return nil
}
func (f *fakeSDK) CreateRuntimeToken(_ context.Context, in openconnector.CreateRuntimeTokenRequest) (*openconnector.CreatedRuntimeToken, error) {
	f.tokenReqs = append(f.tokenReqs, in)
	return f.tokenResp, nil
}
func (f *fakeSDK) DeleteRuntimeToken(_ context.Context, id string) error {
	f.tokenDels = append(f.tokenDels, id)
	return f.tokenDelErr
}

type fakeStore struct {
	mu       sync.Mutex
	rows     []db.UserConnectorConnection
	states   map[string]*db.ConnectorOauthState
	policies []db.ConnectorServicePolicy
}

// publish adds an enabled policy row for service, restricted to users when
// any are given. Tests that never call it see an empty, fully delisted catalog.
func (s *fakeStore) publish(service string, users ...pgtype.UUID) *fakeStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policies = append(s.policies, db.ConnectorServicePolicy{
		Provider: ProviderName, Service: service, Enabled: true, AllowedUserIds: users,
	})
	return s
}

// publishedStore is the common fixture: both connectable services of
// defaultSDK are published to every user.
func publishedStore() *fakeStore { return (&fakeStore{}).publish("github").publish("openai") }

func (s *fakeStore) ListConnectorServicePolicies(_ context.Context, provider string) ([]db.ConnectorServicePolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []db.ConnectorServicePolicy
	for _, p := range s.policies {
		if p.Provider == provider {
			out = append(out, p)
		}
	}
	return out, nil
}

func (s *fakeStore) InsertConnectorOAuthState(_ context.Context, a db.InsertConnectorOAuthStateParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.states == nil {
		s.states = map[string]*db.ConnectorOauthState{}
	}
	s.states[string(a.NonceHash)] = &db.ConnectorOauthState{
		NonceHash: a.NonceHash, Provider: a.Provider, UserID: a.UserID, Service: a.Service,
		RequestID: a.RequestID, ExpiresAt: a.ExpiresAt,
	}
	return nil
}
func (s *fakeStore) ConsumeConnectorOAuthState(_ context.Context, a db.ConsumeConnectorOAuthStateParams) (db.ConnectorOauthState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.states[string(a.NonceHash)]
	if !ok || st.ConsumedAt.Valid || !st.ExpiresAt.Time.After(a.Now.Time) {
		return db.ConnectorOauthState{}, pgx.ErrNoRows
	}
	st.ConsumedAt = a.Now
	return *st, nil
}
func (s *fakeStore) ExistsPendingConnectorOAuthForService(_ context.Context, a db.ExistsPendingConnectorOAuthForServiceParams) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.states {
		if st.Provider == a.Provider && st.Service == a.Service && !st.ConsumedAt.Valid &&
			st.ExpiresAt.Time.After(a.Now.Time) && st.UserID != a.UserID {
			return true, nil
		}
	}
	return false, nil
}

func (s *fakeStore) UpsertUserConnectorConnection(_ context.Context, a db.UpsertUserConnectorConnectionParams) (db.UserConnectorConnection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.rows {
		if r.Provider == a.Provider && r.UserID == a.UserID && r.ConnectionID == a.ConnectionID {
			s.rows[i].Service, s.rows[i].ConnectionName, s.rows[i].AuthType = a.Service, a.ConnectionName, a.AuthType
			if a.Label != "" {
				s.rows[i].Label = a.Label
			}
			s.rows[i].Status = "active"
			return s.rows[i], nil
		}
	}
	row := db.UserConnectorConnection{
		ID: dbid.NewV7(), Provider: a.Provider, UserID: a.UserID, Service: a.Service,
		ConnectionID: a.ConnectionID, ConnectionName: a.ConnectionName, AuthType: a.AuthType,
		Status: "active", IsDefault: a.IsDefault, Label: a.Label,
		ConnectedAt: pgtype.Timestamptz{Time: time.Unix(int64(1000+len(s.rows)), 0), Valid: true},
	}
	s.rows = append(s.rows, row)
	return row, nil
}
func (s *fakeStore) ListUserConnectorConnections(_ context.Context, a db.ListUserConnectorConnectionsParams) ([]db.UserConnectorConnection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []db.UserConnectorConnection
	for _, r := range s.rows {
		if r.Provider == a.Provider && r.UserID == a.UserID && r.Status != "revoked" {
			out = append(out, r)
		}
	}
	return out, nil
}
func (s *fakeStore) GetUserConnectorConnection(_ context.Context, a db.GetUserConnectorConnectionParams) (db.UserConnectorConnection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rows {
		if r.ID == a.ID && r.UserID == a.UserID {
			return r, nil
		}
	}
	return db.UserConnectorConnection{}, pgx.ErrNoRows
}
func (s *fakeStore) MarkUserConnectorConnectionRevoked(_ context.Context, a db.MarkUserConnectorConnectionRevokedParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.rows {
		if r.ID == a.ID && r.UserID == a.UserID {
			s.rows[i].Status, s.rows[i].IsDefault = "revoked", false
		}
	}
	return nil
}
func (s *fakeStore) SetUserConnectorConnectionDefault(_ context.Context, a db.SetUserConnectorConnectionDefaultParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.rows {
		if r.Provider == a.Provider && r.UserID == a.UserID && r.Service == a.Service && r.Status == "active" {
			s.rows[i].IsDefault = r.ID == a.ID
		}
	}
	return nil
}
func (s *fakeStore) UpdateUserConnectorConnectionLabel(_ context.Context, a db.UpdateUserConnectorConnectionLabelParams) (db.UserConnectorConnection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.rows {
		if r.ID == a.ID && r.UserID == a.UserID {
			s.rows[i].Label = a.Label
			return s.rows[i], nil
		}
	}
	return db.UserConnectorConnection{}, pgx.ErrNoRows
}
func (s *fakeStore) UpdateUserConnectorConnectionStatus(_ context.Context, a db.UpdateUserConnectorConnectionStatusParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.rows {
		if r.ID == a.ID {
			s.rows[i].Status = a.Status
		}
	}
	return nil
}
func (s *fakeStore) ListUserConnectorConnectionsByProviderAndStatus(_ context.Context, a db.ListUserConnectorConnectionsByProviderAndStatusParams) ([]db.UserConnectorConnection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []db.UserConnectorConnection
	for _, r := range s.rows {
		if r.Provider == a.Provider && r.Status == a.Status {
			out = append(out, r)
		}
	}
	return out, nil
}

// --- helpers ---

var testSecret = []byte("test-secret")

func newTestUser() pgtype.UUID { return dbid.NewV7() }

func githubProvider() openconnector.Provider {
	return openconnector.Provider{Service: "github", DisplayName: "GitHub", AuthTypes: []string{"oauth2"}}
}

func newTestService(t *testing.T, sdk *fakeSDK, store *fakeStore) *Service {
	svc, err := NewService(sdk, store, Config{
		StateSecret:     testSecret,
		CallbackBaseURL: "https://api.example.com/",
		FrontendBaseURL: "https://app.example.com",
		MCPURL:          "https://oc.example.com/mcp",
		PollInterval:    time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func defaultSDK() *fakeSDK {
	return &fakeSDK{
		providers:  []openconnector.Provider{githubProvider(), {Service: "notion", DisplayName: "Notion", AuthTypes: []string{"oauth2"}}, {Service: "openai", DisplayName: "OpenAI", AuthTypes: []string{"api_key"}}},
		configured: map[string]struct{}{"github": {}},
		setup: map[string]*openconnector.ProviderSetup{"github": {Auth: []openconnector.AuthOption{{
			Type: "oauth2",
			AuthorizationOptions: []openconnector.AuthorizationOption{
				{ID: "read", Required: true, Risk: "standard"},
				{ID: "write", DefaultSelected: true, Risk: "sensitive"},
				{ID: "delete", DefaultSelected: true, Risk: "destructive"},
				{ID: "extra", Risk: "standard"},
			},
		}}}},
		beginResp: &openconnector.ConnectionRequest{AuthorizationURL: "https://github.com/login/oauth", ConnectionRequestID: "req-1"},
		connByID:  map[string]*openconnector.Connection{},
	}
}

func uuidStr(u pgtype.UUID) string { return util.UUIDToString(u) }
