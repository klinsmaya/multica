package openconnector

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/pkg/openconnector"
)

func stateFromReturnURI(t *testing.T, sdk *fakeSDK) string {
	t.Helper()
	u, err := url.Parse(sdk.beginCalls[len(sdk.beginCalls)-1].ReturnURI)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/api/integrations/open-connector/callback" || u.Host != "api.example.com" {
		t.Fatalf("returnUri = %s", u)
	}
	return u.Query().Get("state")
}

func TestCatalogFiltersAndCaches(t *testing.T) {
	sdk := defaultSDK()
	svc := newTestService(t, sdk, publishedStore())
	apps, err := svc.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// notion has no configured client; openai accepts an api key.
	var got []string
	for _, a := range apps {
		got = append(got, a.Service+":"+strings.Join(a.AuthTypes, ","))
	}
	want := []string{"github:oauth", "openai:api_key"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog = %v, want %v", got, want)
	}
	sdk.providers = nil
	if again, _ := svc.Catalog(context.Background()); len(again) != 2 {
		t.Fatalf("expected cached result, got %d apps", len(again))
	}
	svc.InvalidateCatalog()
	if after, _ := svc.Catalog(context.Background()); len(after) != 0 {
		t.Fatalf("expected refetch after invalidate, got %d apps", len(after))
	}
}

func TestListAppsFollowsPolicy(t *testing.T) {
	ctx := context.Background()
	sdk := defaultSDK()
	store := &fakeStore{}
	svc := newTestService(t, sdk, store)
	alice, bob := newTestUser(), newTestUser()

	// No policy rows: nothing is published, even though the instance offers two services.
	if apps, err := svc.ListApps(ctx, alice); err != nil || len(apps) != 0 {
		t.Fatalf("unpublished apps = %v, err = %v", apps, err)
	}
	if _, err := svc.BeginOAuthConnect(ctx, alice, "github"); !errors.Is(err, ErrServiceNotSupported) {
		t.Fatalf("connect to unpublished service err = %v", err)
	}

	store.publish("github").publish("openai", alice)
	services := func(u pgtype.UUID) []string {
		apps, err := svc.ListApps(ctx, u)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, a := range apps {
			out = append(out, a.Service)
		}
		return out
	}
	if got := services(alice); !reflect.DeepEqual(got, []string{"github", "openai"}) {
		t.Fatalf("alice apps = %v", got)
	}
	if got := services(bob); !reflect.DeepEqual(got, []string{"github"}) {
		t.Fatalf("bob apps = %v", got)
	}
	sdk.apiKeyConn = &openconnector.Connection{ID: "app-k", Service: "openai", ConnectionName: "alias", AuthType: "api_key"}
	if _, err := svc.ConnectWithAPIKey(ctx, bob, "openai", "sk-x", nil, ""); !errors.Is(err, ErrUserNotAllowed) {
		t.Fatalf("bob api key connect err = %v", err)
	}
	if _, err := svc.ConnectWithAPIKey(ctx, alice, "openai", "sk-x", nil, ""); err != nil {
		t.Fatalf("alice api key connect err = %v", err)
	}

	// A disabled row delists the service for everyone.
	store.policies[0].Enabled = false
	if got := services(alice); !reflect.DeepEqual(got, []string{"openai"}) {
		t.Fatalf("alice apps after delist = %v", got)
	}
}

func TestHealthReportsReachability(t *testing.T) {
	sdk := defaultSDK()
	svc := newTestService(t, sdk, &fakeStore{})
	svc.baseURL = "https://oc.example.com"
	h := svc.Health(context.Background())
	if !h.Reachable || h.ProvidersTotal != 3 || h.OAuthConfigured != 1 || h.BaseURL != "https://oc.example.com" || h.Error != "" {
		t.Fatalf("health = %+v", h)
	}
	sdk.providersErr = errors.New("connection refused")
	h = svc.Health(context.Background())
	if h.Reachable || !strings.Contains(h.Error, "connection refused") {
		t.Fatalf("health after failure = %+v", h)
	}
}

func TestNormalizeAllowedTools(t *testing.T) {
	got, err := NormalizeAllowedTools("GitHub", []string{" github.create_issue ", "list_repos", "create_issue", "", "search.*"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"create_issue", "list_repos", "search.*"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
	if got, err := NormalizeAllowedTools("github", []string{"create_issue", "*"}); err != nil || len(got) != 0 {
		t.Fatalf("wildcard = %v, %v", got, err)
	}
	for _, bad := range [][]string{{"bad tool"}, {"a;b"}, {strings.Repeat("x", 121)}} {
		if _, err := NormalizeAllowedTools("github", bad); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%v: err = %v", bad, err)
		}
	}
}

func TestBeginOAuthConnectScopes(t *testing.T) {
	ctx := context.Background()
	sdk := defaultSDK()
	store := publishedStore()
	svc := newTestService(t, sdk, store)
	if _, err := svc.BeginOAuthConnect(ctx, newTestUser(), "GitHub"); err != nil {
		t.Fatal(err)
	}
	// Required and default-selected, destructive excluded.
	if got := sdk.beginCalls[0].AuthorizationOptionIDs; !reflect.DeepEqual(got, []string{"read", "write"}) {
		t.Fatalf("option ids = %v", got)
	}

	svc.scopes = map[string][]string{"github": {"read"}}
	store.states = nil
	if _, err := svc.BeginOAuthConnect(ctx, newTestUser(), "github"); err != nil {
		t.Fatal(err)
	}
	if got := sdk.beginCalls[1].AuthorizationOptionIDs; !reflect.DeepEqual(got, []string{"read"}) {
		t.Fatalf("override ids = %v", got)
	}
}

func TestBeginOAuthConnectUnsupportedAndBusy(t *testing.T) {
	ctx := context.Background()
	sdk := defaultSDK()
	svc := newTestService(t, sdk, publishedStore())
	if _, err := svc.BeginOAuthConnect(ctx, newTestUser(), "notion"); !errors.Is(err, ErrServiceNotSupported) {
		t.Fatalf("unconfigured service err = %v", err)
	}
	a, b := newTestUser(), newTestUser()
	if _, err := svc.BeginOAuthConnect(ctx, a, "github"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.BeginOAuthConnect(ctx, b, "github"); !errors.Is(err, ErrConnectBusy) {
		t.Fatalf("other user err = %v, want ErrConnectBusy", err)
	}
	if _, err := svc.BeginOAuthConnect(ctx, a, "github"); err != nil {
		t.Fatalf("same user restart: %v", err)
	}
}

func TestBusyAcrossServiceInstancesAndExpiry(t *testing.T) {
	ctx := context.Background()
	sdk := defaultSDK()
	store := publishedStore()
	// Two instances sharing one store model two replicas.
	a, b := newTestService(t, sdk, store), newTestService(t, sdk, store)
	now := time.Unix(1_700_000_000, 0)
	a.now, b.now = func() time.Time { return now }, func() time.Time { return now }
	if _, err := a.BeginOAuthConnect(ctx, newTestUser(), "github"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.BeginOAuthConnect(ctx, newTestUser(), "github"); !errors.Is(err, ErrConnectBusy) {
		t.Fatalf("err = %v, want ErrConnectBusy", err)
	}
	b.now = func() time.Time { return now.Add(stateTTL + time.Second) }
	if _, err := b.BeginOAuthConnect(ctx, newTestUser(), "github"); err != nil {
		t.Fatalf("after expiry: %v", err)
	}
}

func TestCallbackSurvivesRestartAndIsSingleUse(t *testing.T) {
	ctx := context.Background()
	sdk := defaultSDK()
	store := publishedStore()
	sdk.requestSeq = []openconnector.ConnectionRequestStatus{connectedStatus("app-1")}
	sdk.connByID["app-1"] = &openconnector.Connection{ID: "app-1", Service: "github", ConnectionName: "alias-1"}
	user := newTestUser()
	if _, err := newTestService(t, sdk, store).BeginOAuthConnect(ctx, user, "github"); err != nil {
		t.Fatal(err)
	}
	state := stateFromReturnURI(t, sdk)
	// A fresh instance (restart / other replica) completes the flow.
	svc := newTestService(t, sdk, store)
	if _, err := svc.CompleteCallback(ctx, state); err != nil {
		t.Fatalf("callback on new instance: %v", err)
	}
	if _, err := svc.CompleteCallback(ctx, state); !errors.Is(err, ErrConnectNotSuccessful) {
		t.Fatalf("replay err = %v", err)
	}
	// Consumed state no longer blocks other users.
	if _, err := svc.BeginOAuthConnect(ctx, newTestUser(), "github"); err != nil {
		t.Fatalf("after consume: %v", err)
	}
}

func connectedStatus(appID string) openconnector.ConnectionRequestStatus {
	return openconnector.ConnectionRequestStatus{Status: "connected", AppID: &appID}
}

func TestCompleteCallbackRetriesInitiated(t *testing.T) {
	ctx := context.Background()
	sdk := defaultSDK()
	store := publishedStore()
	svc := newTestService(t, sdk, store)
	user := newTestUser()
	sdk.connByID["app-1"] = &openconnector.Connection{ID: "app-1", Service: "github", ConnectionName: "alias-1", AuthType: "oauth2", AccountLabel: "octocat"}
	sdk.requestSeq = []openconnector.ConnectionRequestStatus{{Status: "initiated"}, {Status: "initiated"}, connectedStatus("app-1")}

	if _, err := svc.BeginOAuthConnect(ctx, user, "github"); err != nil {
		t.Fatal(err)
	}
	service, err := svc.CompleteCallback(ctx, stateFromReturnURI(t, sdk))
	if err != nil || service != "github" {
		t.Fatalf("CompleteCallback = %q, %v", service, err)
	}
	if sdk.requestCalls != 3 {
		t.Fatalf("polled %d times, want 3", sdk.requestCalls)
	}
	if len(store.rows) != 1 || !store.rows[0].IsDefault || store.rows[0].ConnectionName != "alias-1" ||
		store.rows[0].AuthType != "oauth" || store.rows[0].Label != "octocat" {
		t.Fatalf("rows = %+v", store.rows)
	}

	// A second connection of the same service is not the default.
	sdk.connByID["app-2"] = &openconnector.Connection{ID: "app-2", Service: "github", ConnectionName: "alias-2", AuthType: "oauth2"}
	sdk.requestSeq, sdk.requestCalls = []openconnector.ConnectionRequestStatus{connectedStatus("app-2")}, 0
	if _, err := svc.BeginOAuthConnect(ctx, user, "github"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteCallback(ctx, stateFromReturnURI(t, sdk)); err != nil {
		t.Fatal(err)
	}
	if len(store.rows) != 2 || store.rows[1].IsDefault {
		t.Fatalf("second connection must not be default: %+v", store.rows)
	}
}

func TestCompleteCallbackFailures(t *testing.T) {
	ctx := context.Background()
	user := newTestUser()

	t.Run("still initiated after retries", func(t *testing.T) {
		sdk := defaultSDK()
		sdk.requestSeq = []openconnector.ConnectionRequestStatus{{Status: "initiated"}}
		svc := newTestService(t, sdk, publishedStore())
		_, _ = svc.BeginOAuthConnect(ctx, user, "github")
		if _, err := svc.CompleteCallback(ctx, stateFromReturnURI(t, sdk)); !errors.Is(err, ErrConnectNotSuccessful) {
			t.Fatalf("err = %v", err)
		}
		if sdk.requestCalls != 1+maxCallbackRetries {
			t.Fatalf("polled %d times", sdk.requestCalls)
		}
	})
	t.Run("failed request", func(t *testing.T) {
		sdk := defaultSDK()
		sdk.requestSeq = []openconnector.ConnectionRequestStatus{{Status: "failed"}}
		store := publishedStore()
		svc := newTestService(t, sdk, store)
		_, _ = svc.BeginOAuthConnect(ctx, user, "github")
		if _, err := svc.CompleteCallback(ctx, stateFromReturnURI(t, sdk)); !errors.Is(err, ErrConnectNotSuccessful) || len(store.rows) != 0 {
			t.Fatalf("err = %v rows = %d", err, len(store.rows))
		}
	})
	t.Run("connection of another service", func(t *testing.T) {
		sdk := defaultSDK()
		sdk.requestSeq = []openconnector.ConnectionRequestStatus{connectedStatus("app-x")}
		sdk.connByID["app-x"] = &openconnector.Connection{ID: "app-x", Service: "notion"}
		store := publishedStore()
		svc := newTestService(t, sdk, store)
		_, _ = svc.BeginOAuthConnect(ctx, user, "github")
		if _, err := svc.CompleteCallback(ctx, stateFromReturnURI(t, sdk)); !errors.Is(err, ErrConnectNotSuccessful) || len(store.rows) != 0 {
			t.Fatalf("err = %v rows = %d", err, len(store.rows))
		}
	})
	t.Run("replay without pending flow", func(t *testing.T) {
		sdk := defaultSDK()
		sdk.requestSeq = []openconnector.ConnectionRequestStatus{connectedStatus("app-1")}
		sdk.connByID["app-1"] = &openconnector.Connection{ID: "app-1", Service: "github"}
		svc := newTestService(t, sdk, publishedStore())
		_, _ = svc.BeginOAuthConnect(ctx, user, "github")
		state := stateFromReturnURI(t, sdk)
		if _, err := svc.CompleteCallback(ctx, state); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.CompleteCallback(ctx, state); !errors.Is(err, ErrConnectNotSuccessful) {
			t.Fatalf("replay err = %v", err)
		}
	})
	t.Run("expired and tampered state", func(t *testing.T) {
		sdk := defaultSDK()
		now := time.Unix(1_700_000_000, 0)
		svc := newTestService(t, sdk, publishedStore())
		svc.now = func() time.Time { return now }
		_, _ = svc.BeginOAuthConnect(ctx, user, "github")
		state := stateFromReturnURI(t, sdk)
		svc.now = func() time.Time { return now.Add(stateTTL + time.Second) }
		if _, err := svc.CompleteCallback(ctx, state); !errors.Is(err, ErrStateExpired) {
			t.Fatalf("expired err = %v", err)
		}
		if _, err := svc.CompleteCallback(ctx, state+"x"); !errors.Is(err, ErrStateSignature) {
			t.Fatalf("tampered err = %v", err)
		}
	})
}

func TestConnectWithAPIKey(t *testing.T) {
	ctx := context.Background()
	const key = "sk-super-secret-key"
	user := newTestUser()

	sdk := defaultSDK()
	sdk.apiKeyConn = &openconnector.Connection{ID: "app-k", Service: "openai", ConnectionName: "uuid-alias", AuthType: "api_key"}
	store := publishedStore()
	svc := newTestService(t, sdk, store)
	view, err := svc.ConnectWithAPIKey(ctx, user, "openai", key, nil, " work ")
	if err != nil {
		t.Fatal(err)
	}
	if view.Label != "work" || view.AuthType != "api_key" || !view.IsDefault {
		t.Fatalf("view = %+v", view)
	}
	if sdk.apiKeyReq.APIKey != key || sdk.apiKeyReq.Comment != "work" {
		t.Fatalf("request = %+v", sdk.apiKeyReq)
	}
	if strings.Contains(view.ID+view.Label, key) {
		t.Fatal("view leaks key")
	}

	// Unsupported service and empty key.
	if _, err := svc.ConnectWithAPIKey(ctx, user, "github", key, nil, ""); !errors.Is(err, ErrServiceNotSupported) {
		t.Fatalf("err = %v", err)
	}
	if _, err := svc.ConnectWithAPIKey(ctx, user, "openai", "  ", nil, ""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v", err)
	}
}

func TestConnectWithAPIKeyNeverLeaksKeyInError(t *testing.T) {
	const key = "sk-super-secret-key"
	sdk := defaultSDK()
	// A hostile or buggy upstream that echoes the submitted key in its message.
	sdk.apiKeyErr = &openconnector.APIError{Status: 400, Code: "credential_verification_failed", Message: "bad key " + key}
	svc := newTestService(t, sdk, publishedStore())
	_, err := svc.ConnectWithAPIKey(context.Background(), newTestUser(), "openai", key, map[string]string{"region": "eu-secret"}, "")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), key) {
		t.Fatalf("error leaks key: %v", err)
	}

	// Without an echo the typed error stays inspectable for the handler.
	sdk.apiKeyErr = &openconnector.APIError{Status: 400, Code: "credential_verification_failed", Message: "rejected"}
	_, err = svc.ConnectWithAPIKey(context.Background(), newTestUser(), "openai", key, nil, "")
	var apiErr *openconnector.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "credential_verification_failed" {
		t.Fatalf("err = %v", err)
	}
}

func TestDisconnectIdempotentAndPromotesDefault(t *testing.T) {
	ctx := context.Background()
	sdk := defaultSDK()
	store := publishedStore()
	svc := newTestService(t, sdk, store)
	user := newTestUser()
	for _, id := range []string{"a", "b"} {
		sdk.apiKeyConn = &openconnector.Connection{ID: id, Service: "openai", ConnectionName: "alias-" + id, AuthType: "api_key"}
		if _, err := svc.ConnectWithAPIKey(ctx, user, "openai", "k", nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	first := store.rows[0].ID
	if err := svc.Disconnect(ctx, user, first); err != nil {
		t.Fatal(err)
	}
	if err := svc.Disconnect(ctx, user, first); err != nil {
		t.Fatalf("second disconnect: %v", err)
	}
	if len(sdk.deleted) != 1 || sdk.deleted[0] != [2]string{"openai", "alias-a"} {
		t.Fatalf("deleted = %v", sdk.deleted)
	}
	if store.rows[0].Status != "revoked" || !store.rows[1].IsDefault {
		t.Fatalf("rows = %+v", store.rows)
	}
	// Another user cannot see or remove it.
	if err := svc.Disconnect(ctx, newTestUser(), store.rows[1].ID); !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("foreign disconnect err = %v", err)
	}
}

func TestSetDefaultAndLabel(t *testing.T) {
	ctx := context.Background()
	sdk := defaultSDK()
	store := publishedStore()
	svc := newTestService(t, sdk, store)
	user := newTestUser()
	for _, id := range []string{"a", "b"} {
		sdk.apiKeyConn = &openconnector.Connection{ID: id, Service: "openai", ConnectionName: "alias-" + id, AuthType: "api_key"}
		_, _ = svc.ConnectWithAPIKey(ctx, user, "openai", "k", nil, "")
	}
	if _, err := svc.SetDefault(ctx, user, store.rows[1].ID); err != nil {
		t.Fatal(err)
	}
	if store.rows[0].IsDefault || !store.rows[1].IsDefault {
		t.Fatalf("rows = %+v", store.rows)
	}
	v, err := svc.UpdateLabel(ctx, user, store.rows[0].ID, "  Personal ")
	if err != nil || v.Label != "Personal" {
		t.Fatalf("label = %+v, %v", v, err)
	}
	if _, err := svc.UpdateLabel(ctx, user, store.rows[0].ID, strings.Repeat("x", 101)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v", err)
	}
}

func TestSyncStatuses(t *testing.T) {
	ctx := context.Background()
	sdk := defaultSDK()
	store := publishedStore()
	svc := newTestService(t, sdk, store)
	user := newTestUser()
	for _, id := range []string{"a", "b", "c"} {
		sdk.apiKeyConn = &openconnector.Connection{ID: id, Service: "openai", ConnectionName: id, AuthType: "api_key"}
		_, _ = svc.ConnectWithAPIKey(ctx, user, "openai", "k", nil, "")
	}
	store.rows[2].Status = "reauth_required"
	sdk.remote = []openconnector.Connection{
		{ID: "a", Status: "reauth_required"},
		{ID: "b", Status: "disconnected"},
		{ID: "c", Status: "active"},
	}
	if err := svc.SyncStatuses(ctx); err != nil {
		t.Fatal(err)
	}
	got := []string{store.rows[0].Status, store.rows[1].Status, store.rows[2].Status}
	if !reflect.DeepEqual(got, []string{"reauth_required", "revoked", "active"}) {
		t.Fatalf("statuses = %v", got)
	}
}

func TestCallbackRedirect(t *testing.T) {
	svc := newTestService(t, defaultSDK(), publishedStore())
	if got := svc.CallbackRedirect("github", true); got != "https://app.example.com/settings?tab=integrations&connected=github" {
		t.Fatal(got)
	}
	if got := svc.CallbackRedirect("github", false); got != "https://app.example.com/settings?tab=integrations&error=open_connector_connect_failed" {
		t.Fatal(got)
	}
}
