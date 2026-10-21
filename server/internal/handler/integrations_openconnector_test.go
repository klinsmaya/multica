package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	openconnector "github.com/multica-ai/multica/server/internal/integrations/openconnector"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	sdk "github.com/multica-ai/multica/server/pkg/openconnector"
)

const ocSecretAPIKey = "sk-super-secret-key-123"

// ocFakeSDK implements openconnector.SDK for handler tests. Only the calls
// the handler paths reach are meaningful.
type ocFakeSDK struct {
	apiKeyErr error
	deleted   int
}

func (f *ocFakeSDK) ListProviders(context.Context) ([]sdk.Provider, error) {
	return []sdk.Provider{
		{Service: "github", DisplayName: "GitHub", AuthTypes: []string{"oauth2"}},
		{Service: "openai", DisplayName: "OpenAI", AuthTypes: []string{"api_key"}},
	}, nil
}
func (f *ocFakeSDK) ConfiguredOAuthServices(context.Context) (map[string]struct{}, error) {
	return map[string]struct{}{"github": {}}, nil
}
func (f *ocFakeSDK) GetProviderSetup(context.Context, string) (*sdk.ProviderSetup, error) {
	return &sdk.ProviderSetup{Auth: []sdk.AuthOption{{Type: "oauth2"}}}, nil
}
func (f *ocFakeSDK) BeginOAuthConnect(context.Context, string, sdk.BeginOAuthConnectRequest) (*sdk.ConnectionRequest, error) {
	return &sdk.ConnectionRequest{AuthorizationURL: "https://github.example/authorize", ConnectionRequestID: "req-" + util.UUIDToString(dbid.NewV7())}, nil
}
func (f *ocFakeSDK) GetConnectionRequest(context.Context, string) (*sdk.ConnectionRequestStatus, error) {
	return &sdk.ConnectionRequestStatus{Status: "failed"}, nil
}
func (f *ocFakeSDK) ConnectWithAPIKey(_ context.Context, _ string, in sdk.APIKeyConnectRequest) (*sdk.Connection, error) {
	if f.apiKeyErr != nil {
		return nil, f.apiKeyErr
	}
	return &sdk.Connection{ID: "app-" + util.UUIDToString(dbid.NewV7()), Service: "openai", ConnectionName: "alias", AuthType: "api_key"}, nil
}
func (f *ocFakeSDK) GetConnectionByID(context.Context, string) (*sdk.Connection, error) {
	return nil, sdk.ErrNotFound
}
func (f *ocFakeSDK) ListConnections(context.Context, string) ([]sdk.Connection, error) {
	return nil, nil
}
func (f *ocFakeSDK) DeleteConnection(context.Context, string, string, bool) error {
	f.deleted++
	return nil
}
func (f *ocFakeSDK) CreateRuntimeToken(context.Context, sdk.CreateRuntimeTokenRequest) (*sdk.CreatedRuntimeToken, error) {
	return nil, errors.New("not used")
}
func (f *ocFakeSDK) DeleteRuntimeToken(context.Context, string) error { return nil }

func newOpenConnectorService(t *testing.T, fake *ocFakeSDK) *openconnector.Service {
	t.Helper()
	if testPool == nil {
		t.Skip("database not available")
	}
	svc, err := openconnector.NewService(fake, db.New(testPool), openconnector.Config{
		StateSecret:     []byte("handler-test-secret"),
		CallbackBaseURL: "https://api.example.com",
		FrontendBaseURL: "https://app.example.com",
		MCPURL:          "https://oc.example.com/mcp",
		BaseURL:         "https://oc.example.com",
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

// withOpenConnector wires svc into h for the test, the way router.go does
// when OPEN_CONNECTOR_BASE_URL / ADMIN_TOKEN are set. nil unwires it.
func withOpenConnector(t *testing.T, h *Handler, svc *openconnector.Service) {
	t.Helper()
	prev := h.OpenConnector
	h.OpenConnector = svc
	t.Cleanup(func() { h.OpenConnector = prev })
}

// publishOCService inserts an enabled policy row, which is what makes a
// catalog service visible to users. Tests that test the delisted state call
// nothing and rely on the table being empty for the service.
func publishOCService(t *testing.T, service string, allowedUserIDs ...pgtype.UUID) {
	t.Helper()
	ids := make([]string, 0, len(allowedUserIDs))
	for _, id := range allowedUserIDs {
		ids = append(ids, util.UUIDToString(id))
	}
	dbfx.Exec(t, `INSERT INTO connector_service_policy (provider, service, enabled, allowed_user_ids)
		VALUES ($1, $2, TRUE, $3::uuid[])
		ON CONFLICT (provider, service) DO UPDATE SET enabled = TRUE, allowed_user_ids = EXCLUDED.allowed_user_ids`,
		openconnector.ProviderName, service, ids)
	dbfx.Cleanup(t, `DELETE FROM connector_service_policy WHERE provider = $1 AND service = $2`, openconnector.ProviderName, service)
}

func newOpenConnectorTestHandler(t *testing.T, fake *ocFakeSDK) (*Handler, *db.Queries) {
	t.Helper()
	svc := newOpenConnectorService(t, fake)
	publishOCService(t, "github")
	publishOCService(t, "openai")
	return &Handler{OpenConnector: svc}, db.New(testPool)
}

func newOCUser(t *testing.T) (string, pgtype.UUID) {
	t.Helper()
	id := dbid.NewV7()
	t.Cleanup(func() {
		ctx := context.Background()
		testPool.Exec(ctx, `DELETE FROM user_connector_connection WHERE user_id = $1`, id)
		testPool.Exec(ctx, `DELETE FROM connector_oauth_state WHERE user_id = $1`, id)
	})
	return util.UUIDToString(id), id
}

func ocReq(userID, method, path string, body any) *http.Request {
	r := newRequestAs(userID, method, path, body)
	r.Header.Del("X-Workspace-ID")
	return r
}

func seedOCConnection(t *testing.T, q *db.Queries, userID pgtype.UUID, connID string, isDefault bool) db.UserConnectorConnection {
	t.Helper()
	row, err := q.UpsertUserConnectorConnection(context.Background(), db.UpsertUserConnectorConnectionParams{
		Provider: openconnector.ProviderName, UserID: userID, Service: "github",
		ConnectionID: connID, ConnectionName: "alias-" + connID, AuthType: "oauth", Label: "work", IsDefault: isDefault,
	})
	if err != nil {
		t.Fatalf("seed connection: %v", err)
	}
	return row
}

func TestOpenConnector_NotConfigured(t *testing.T) {
	h := &Handler{}
	for name, hf := range map[string]http.HandlerFunc{
		"init": h.OpenConnectorConnectInit, "api-key": h.OpenConnectorConnectAPIKey,
		"apps": h.ListOpenConnectorApps, "list": h.ListOpenConnectorConnections,
		"patch": h.UpdateOpenConnectorConnection, "delete": h.DeleteOpenConnectorConnection,
		"callback": h.OpenConnectorCallback,
	} {
		var body struct {
			Code string `json:"code"`
		}
		testutil.Call(t, hf, ocReq(testUserID, http.MethodGet, "/", nil)).Want(http.StatusForbidden).JSON(&body)
		if body.Code != "open_connector_not_configured" {
			t.Errorf("%s: code = %q", name, body.Code)
		}
	}
}

func TestOpenConnector_PolicyDecidesWhatUsersSee(t *testing.T) {
	h := &Handler{OpenConnector: newOpenConnectorService(t, &ocFakeSDK{})}
	alice, aliceID := newOCUser(t)
	bob, _ := newOCUser(t)

	// Nothing published: the instance offers two services, users see none.
	var apps []openconnector.AppView
	testutil.Call(t, h.ListOpenConnectorApps, ocReq(alice, http.MethodGet, "/", nil)).Want(http.StatusOK).JSON(&apps)
	if len(apps) != 0 {
		t.Fatalf("unpublished apps = %+v", apps)
	}
	testutil.Call(t, h.OpenConnectorConnectInit,
		ocReq(alice, http.MethodPost, "/", map[string]any{"service": "github"})).Want(http.StatusBadRequest)

	// github for everyone, openai only for alice.
	publishOCService(t, "github")
	publishOCService(t, "openai", aliceID)
	apps = nil
	testutil.Call(t, h.ListOpenConnectorApps, ocReq(alice, http.MethodGet, "/", nil)).Want(http.StatusOK).JSON(&apps)
	if len(apps) != 2 {
		t.Fatalf("alice apps = %+v", apps)
	}
	apps = nil
	testutil.Call(t, h.ListOpenConnectorApps, ocReq(bob, http.MethodGet, "/", nil)).Want(http.StatusOK).JSON(&apps)
	if len(apps) != 1 || apps[0].Service != "github" {
		t.Fatalf("bob apps = %+v", apps)
	}
	var denied struct {
		Code string `json:"code"`
	}
	testutil.Call(t, h.OpenConnectorConnectAPIKey, ocReq(bob, http.MethodPost, "/", map[string]any{
		"service": "openai", "api_key": ocSecretAPIKey,
	})).Want(http.StatusForbidden).JSON(&denied)
	if denied.Code != "open_connector_user_not_allowed" {
		t.Fatalf("denied code = %q", denied.Code)
	}
}

func TestOpenConnector_ListApps(t *testing.T) {
	h, _ := newOpenConnectorTestHandler(t, &ocFakeSDK{})
	var apps []openconnector.AppView
	testutil.Call(t, h.ListOpenConnectorApps, ocReq(testUserID, http.MethodGet, "/", nil)).Want(http.StatusOK).JSON(&apps)
	if len(apps) != 2 || apps[0].Service != "github" || apps[1].Service != "openai" {
		t.Fatalf("apps = %+v", apps)
	}
}

func TestOpenConnector_ConnectInitSuccessAndBusy(t *testing.T) {
	h, _ := newOpenConnectorTestHandler(t, &ocFakeSDK{})
	userA, _ := newOCUser(t)
	userB, _ := newOCUser(t)

	var resp OpenConnectorConnectInitResponse
	testutil.Call(t, h.OpenConnectorConnectInit,
		ocReq(userA, http.MethodPost, "/", map[string]any{"service": "github"})).Want(http.StatusOK).JSON(&resp)
	if resp.RedirectURL != "https://github.example/authorize" {
		t.Fatalf("redirect_url = %q", resp.RedirectURL)
	}

	var busy struct {
		Code string `json:"code"`
	}
	testutil.Call(t, h.OpenConnectorConnectInit,
		ocReq(userB, http.MethodPost, "/", map[string]any{"service": "github"})).Want(http.StatusConflict).JSON(&busy)
	if busy.Code != "open_connector_connect_busy" {
		t.Errorf("busy code = %q", busy.Code)
	}

	var unsupported struct {
		Code string `json:"code"`
	}
	testutil.Call(t, h.OpenConnectorConnectInit,
		ocReq(userA, http.MethodPost, "/", map[string]any{"service": "notion"})).Want(http.StatusBadRequest).JSON(&unsupported)
	if unsupported.Code != "open_connector_service_not_supported" {
		t.Errorf("unsupported code = %q", unsupported.Code)
	}

	testutil.Call(t, h.OpenConnectorConnectInit,
		ocReq(userA, http.MethodPost, "/", map[string]any{})).Want(http.StatusBadRequest)
}

func TestOpenConnector_APIKeyConnectAndNoKeyEcho(t *testing.T) {
	fake := &ocFakeSDK{}
	h, _ := newOpenConnectorTestHandler(t, fake)
	user, _ := newOCUser(t)

	var view openconnector.ConnectionView
	testutil.Call(t, h.OpenConnectorConnectAPIKey, ocReq(user, http.MethodPost, "/", map[string]any{
		"service": "openai", "api_key": ocSecretAPIKey, "label": "personal",
	})).Want(http.StatusCreated).JSON(&view)
	if view.Service != "openai" || view.AuthType != "api_key" || view.Label != "personal" {
		t.Fatalf("view = %+v", view)
	}

	// Upstream echoes the key in its error: it must not reach the response.
	fake.apiKeyErr = errors.New("upstream rejected key " + ocSecretAPIKey)
	resp := testutil.Call(t, h.OpenConnectorConnectAPIKey, ocReq(user, http.MethodPost, "/", map[string]any{
		"service": "openai", "api_key": ocSecretAPIKey,
	})).Want(http.StatusBadGateway)
	if strings.Contains(resp.Body.String(), ocSecretAPIKey) {
		t.Fatalf("response leaked api key: %s", resp.Body.String())
	}

	// Unsupported service and bad input also never echo the key.
	resp = testutil.Call(t, h.OpenConnectorConnectAPIKey, ocReq(user, http.MethodPost, "/", map[string]any{
		"service": "github", "api_key": ocSecretAPIKey,
	})).Want(http.StatusBadRequest)
	if strings.Contains(resp.Body.String(), ocSecretAPIKey) {
		t.Fatalf("response leaked api key: %s", resp.Body.String())
	}
	testutil.Call(t, h.OpenConnectorConnectAPIKey, ocReq(user, http.MethodPost, "/", map[string]any{
		"service": "openai",
	})).Want(http.StatusBadRequest)
}

func TestOpenConnector_CallbackFailureRedirects(t *testing.T) {
	h, _ := newOpenConnectorTestHandler(t, &ocFakeSDK{})
	for _, target := range []string{"/callback", "/callback?state=garbage", "/callback?state=a.b"} {
		resp := testutil.Call(t, h.OpenConnectorCallback, ocReq(testUserID, http.MethodGet, target, nil)).Want(http.StatusFound)
		loc := resp.Header().Get("Location")
		if loc != "https://app.example.com/settings?tab=integrations&error=open_connector_connect_failed" {
			t.Errorf("%s: location = %q", target, loc)
		}
	}
}

func TestOpenConnector_ListPatchDeleteOwnership(t *testing.T) {
	fake := &ocFakeSDK{}
	h, q := newOpenConnectorTestHandler(t, fake)
	owner, ownerID := newOCUser(t)
	other, _ := newOCUser(t)
	first := seedOCConnection(t, q, ownerID, "app-1", true)
	second := seedOCConnection(t, q, ownerID, "app-2", false)
	secondID, firstID := util.UUIDToString(second.ID), util.UUIDToString(first.ID)

	var list []openconnector.ConnectionView
	testutil.Call(t, h.ListOpenConnectorConnections, ocReq(owner, http.MethodGet, "/", nil)).Want(http.StatusOK).JSON(&list)
	if len(list) != 2 {
		t.Fatalf("owner list = %+v", list)
	}
	list = nil
	testutil.Call(t, h.ListOpenConnectorConnections, ocReq(other, http.MethodGet, "/", nil)).Want(http.StatusOK).JSON(&list)
	if len(list) != 0 {
		t.Fatalf("other list = %+v, want empty array", list)
	}

	patch := func(user, id string, body any) *testutil.Response {
		return testutil.Call(t, h.UpdateOpenConnectorConnection,
			withURLParam(ocReq(user, http.MethodPatch, "/", body), "id", id))
	}
	// Other user's connection is a 404 for both PATCH and DELETE.
	patch(other, secondID, map[string]any{"is_default": true}).Want(http.StatusNotFound)
	testutil.Call(t, h.DeleteOpenConnectorConnection,
		withURLParam(ocReq(other, http.MethodDelete, "/", nil), "id", secondID)).Want(http.StatusNotFound)
	if fake.deleted != 0 {
		t.Fatal("other user's delete reached upstream")
	}

	// Owner: label + default.
	var view openconnector.ConnectionView
	patch(owner, secondID, map[string]any{"label": "side", "is_default": true}).Want(http.StatusOK).JSON(&view)
	if view.Label != "side" || !view.IsDefault {
		t.Fatalf("patched view = %+v", view)
	}
	patch(owner, secondID, map[string]any{}).Want(http.StatusBadRequest)
	patch(owner, secondID, map[string]any{"is_default": false}).Want(http.StatusBadRequest)
	patch(owner, "not-a-uuid", map[string]any{"label": "x"}).Want(http.StatusBadRequest)

	// Delete is idempotent.
	for i := 0; i < 2; i++ {
		testutil.Call(t, h.DeleteOpenConnectorConnection,
			withURLParam(ocReq(owner, http.MethodDelete, "/", nil), "id", firstID)).Want(http.StatusNoContent)
	}
	if fake.deleted != 1 {
		t.Fatalf("upstream deletes = %d, want 1", fake.deleted)
	}
	list = nil
	testutil.Call(t, h.ListOpenConnectorConnections, ocReq(owner, http.MethodGet, "/", nil)).Want(http.StatusOK).JSON(&list)
	if len(list) != 1 || list[0].ID != secondID {
		t.Fatalf("after delete list = %+v", list)
	}
}
