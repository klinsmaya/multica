package openconnector_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/openconnector"
)

const adminToken = "admin-test-token"

type recorded struct {
	Method, Path, Query, Auth string
	Body                      []byte
}

// fixture replays testdata/<file> with the given status and records the request.
func newClient(t *testing.T, status int, file string) (*openconnector.Client, *recorded) {
	t.Helper()
	var data []byte
	if file != "" {
		var err error
		data, err = os.ReadFile(filepath.Join("testdata", file))
		if err != nil {
			t.Fatal(err)
		}
	}
	rec := &recorded{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*rec = recorded{r.Method, r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Get("Authorization"), body}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	c, err := openconnector.NewClient(openconnector.Options{BaseURL: srv.URL, AdminToken: adminToken})
	if err != nil {
		t.Fatal(err)
	}
	return c, rec
}

func wantReq(t *testing.T, rec *recorded, method, path string) {
	t.Helper()
	if rec.Method != method || rec.Path != path {
		t.Fatalf("request = %s %s, want %s %s", rec.Method, rec.Path, method, path)
	}
	if rec.Auth != "Bearer "+adminToken {
		t.Fatalf("Authorization = %q", rec.Auth)
	}
}

func bodyMap(t *testing.T, rec *recorded) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body, &m); err != nil {
		t.Fatalf("body %q: %v", rec.Body, err)
	}
	return m
}

func TestNewClient(t *testing.T) {
	for _, bad := range []string{"", "  ", "ftp://x", "localhost:3000", "http://"} {
		if _, err := openconnector.NewClient(openconnector.Options{BaseURL: bad}); err == nil {
			t.Errorf("BaseURL %q: want error", bad)
		}
	}
	c, err := openconnector.NewClient(openconnector.Options{BaseURL: "https://oc.example.com/"})
	if err != nil || c.BaseURL() != "https://oc.example.com" {
		t.Fatalf("got %v, %v", c, err)
	}
	if _, err := openconnector.NewClient(openconnector.Options{BaseURL: "http://x", HTTPClient: &http.Client{}}); err != nil {
		t.Fatal(err)
	}
}

func TestListProviders(t *testing.T) {
	c, rec := newClient(t, 200, "providers.json")
	got, err := c.ListProviders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantReq(t, rec, "GET", "/v1/providers")
	if len(got) != 5 || got[0].Service != "17track" || got[0].DisplayName != "17TRACK" || got[0].IconURL != nil {
		t.Fatalf("got %+v", got[0])
	}
	if !got[0].SupportsAuthType(openconnector.AuthTypeAPIKey) || len(got[0].Categories) != 2 {
		t.Fatalf("got %+v", got[0])
	}
}

func TestGetProviderSetup(t *testing.T) {
	c, rec := newClient(t, 200, "setup_github.json")
	got, err := c.GetProviderSetup(context.Background(), "github")
	if err != nil {
		t.Fatal(err)
	}
	wantReq(t, rec, "GET", "/v1/providers/github/setup")
	if len(got.Auth) != 3 {
		t.Fatalf("auth = %d", len(got.Auth))
	}
	oa, ok := got.FindAuth(openconnector.AuthTypeOAuth2)
	if !ok || len(oa.AuthorizationOptions) != 5 || len(oa.ClientFields) != 2 {
		t.Fatalf("oauth2 = %+v", oa)
	}
	ids := oa.DefaultAuthorizationOptionIDs()
	if strings.Join(ids, ",") != "read:user,repo" {
		t.Fatalf("default ids = %v", ids)
	}
	if oa.AuthorizationOptions[3].Requires[0] != "repo" {
		t.Fatalf("requires lost: %+v", oa.AuthorizationOptions[3])
	}
	ak, _ := got.FindAuth(openconnector.AuthTypeAPIKey)
	if len(ak.Fields) != 1 || ak.Fields[0].Key != "apiKey" || !ak.Fields[0].Secret {
		t.Fatalf("api_key = %+v", ak)
	}
	cc, _ := got.FindAuth(openconnector.AuthTypeCustomCredential)
	if cc.Label == "" || len(cc.Fields) != 3 {
		t.Fatalf("custom = %+v", cc)
	}
	if got.OAuthClient == nil || got.OAuthClient.Configured || got.OAuthClient.ExpectedRedirectURI != "http://127.0.0.1:3000/oauth/callback" {
		t.Fatalf("oauthClient = %+v", got.OAuthClient)
	}
}

func TestGetProviderSetup_NoAuthAndAPIKeyOnly(t *testing.T) {
	c, _ := newClient(t, 200, "setup_hackernews.json")
	got, err := c.GetProviderSetup(context.Background(), "hackernews")
	if err != nil || got.OAuthClient != nil || got.Auth[0].Type != openconnector.AuthTypeNoAuth {
		t.Fatalf("got %+v, %v", got, err)
	}
	c, _ = newClient(t, 200, "setup_17track.json")
	got, err = c.GetProviderSetup(context.Background(), "17track")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.FindAuth(openconnector.AuthTypeOAuth2); ok {
		t.Fatal("unexpected oauth2")
	}
}

func TestListOAuthConfigs(t *testing.T) {
	c, rec := newClient(t, 200, "oauth_configs_after_put.json")
	got, err := c.ListOAuthConfigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantReq(t, rec, "GET", "/api/oauth/configs")
	if len(got) != 3 || !got[0].Configured || got[0].ClientID == nil || *got[0].ClientID != "fake-client-id" {
		t.Fatalf("got %+v", got[0])
	}
	if got[1].Configured || got[1].ClientID != nil || len(got[1].MissingFields) != 2 || got[1].Auth.Type != "oauth2" {
		t.Fatalf("got %+v", got[1])
	}
	if len(got[0].Auth.AuthorizationOptions) != 5 || len(got[0].EffectiveScopes) != 5 {
		t.Fatalf("got %+v", got[0])
	}

	c, _ = newClient(t, 200, "oauth_configs_after_put.json")
	set, err := c.ConfiguredOAuthServices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := set["github"]; !ok || len(set) != 1 {
		t.Fatalf("set = %v", set)
	}
	c, _ = newClient(t, 200, "oauth_configs_empty.json")
	if set, _ = c.ConfiguredOAuthServices(context.Background()); len(set) != 0 {
		t.Fatalf("set = %v", set)
	}
}

func TestPutOAuthConfig(t *testing.T) {
	c, rec := newClient(t, 200, "oauth_configs_put_github.json")
	got, err := c.PutOAuthConfig(context.Background(), "github", openconnector.OAuthConfigInput{ClientID: "id", ClientSecret: "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	wantReq(t, rec, "PUT", "/api/oauth/configs/github")
	if m := bodyMap(t, rec); m["clientId"] != "id" || m["clientSecret"] != "s3cret" {
		t.Fatalf("body = %v", m)
	}
	if !got.Configured || got.Service != "github" {
		t.Fatalf("got %+v", got)
	}
}

func TestOAuthConfigInput_Redacts(t *testing.T) {
	in := openconnector.OAuthConfigInput{ClientID: "id", ClientSecret: "s3cret"}
	for _, s := range []string{fmt.Sprint(in), fmt.Sprintf("%+v", in), fmt.Sprintf("%#v", in)} {
		if strings.Contains(s, "s3cret") {
			t.Fatalf("leaked: %s", s)
		}
	}
	var sb strings.Builder
	slog.New(slog.NewTextHandler(&sb, nil)).Info("x", "in", in)
	if strings.Contains(sb.String(), "s3cret") {
		t.Fatalf("slog leaked: %s", sb.String())
	}
}

func TestBeginOAuthConnect(t *testing.T) {
	c, rec := newClient(t, 200, "connect_github_with_options.json")
	got, err := c.BeginOAuthConnect(context.Background(), "github", openconnector.BeginOAuthConnectRequest{
		ReturnURI: "http://127.0.0.1:9999/cb", AuthorizationOptionIDs: []string{"read:user", "repo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantReq(t, rec, "POST", "/v1/connections/github/connect")
	m := bodyMap(t, rec)
	ids, _ := m["authorizationOptionIds"].([]any)
	if m["returnUri"] != "http://127.0.0.1:9999/cb" || len(ids) != 2 {
		t.Fatalf("body = %v", m)
	}
	if got.StateHandle == "" || got.ConnectionRequestID != "859c5953-e0e6-4780-8c88-faeddcd5a981" || got.Status != "initiated" ||
		!strings.HasPrefix(got.AuthorizationURL, "https://github.com/login/oauth/authorize") ||
		!got.ExpiresAt.Equal(time.Date(2026, 10, 20, 17, 14, 42, 360_000_000, time.UTC)) {
		t.Fatalf("got %+v", got)
	}
}

func TestBeginOAuthConnect_OptionIDsSerialization(t *testing.T) {
	c, rec := newClient(t, 200, "connect_github.json")
	if _, err := c.BeginOAuthConnect(context.Background(), "github", openconnector.BeginOAuthConnectRequest{ReturnURI: "http://x/cb"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := bodyMap(t, rec)["authorizationOptionIds"]; ok {
		t.Fatalf("nil ids must be omitted: %s", rec.Body)
	}
	if _, err := c.BeginOAuthConnect(context.Background(), "github", openconnector.BeginOAuthConnectRequest{AuthorizationOptionIDs: []string{}}); err != nil {
		t.Fatal(err)
	}
	if string(rec.Body) != `{"authorizationOptionIds":[]}` {
		t.Fatalf("body = %s", rec.Body)
	}
}

func TestBeginOAuthConnect_Unconfigured(t *testing.T) {
	c, _ := newClient(t, 400, "err_connect_unconfigured_oauth.json")
	_, err := c.BeginOAuthConnect(context.Background(), "slack", openconnector.BeginOAuthConnectRequest{})
	var ae *openconnector.APIError
	if !errors.As(err, &ae) || ae.Status != 400 || ae.Code != "oauth_client_config_required" {
		t.Fatalf("err = %v", err)
	}
}

func TestGetConnectionRequest(t *testing.T) {
	c, rec := newClient(t, 200, "connection_request_initiated.json")
	got, err := c.GetConnectionRequest(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	wantReq(t, rec, "GET", "/v1/connection-requests/abc")
	if got.Status != openconnector.RequestStatusInitiated || got.AppID != nil || got.ErrorCode != nil ||
		!got.CreatedAt.Equal(time.UnixMilli(1792515848224)) ||
		!got.ExpiresAt.Equal(time.Date(2026, 10, 20, 17, 14, 8, 224_000_000, time.UTC)) {
		t.Fatalf("got %+v", got)
	}

	c, _ = newClient(t, 200, "connection_request_failed.json")
	got, err = c.GetConnectionRequest(context.Background(), "abc")
	if err != nil || got.Status != "failed" || got.ErrorCode == nil || *got.ErrorCode != "request_superseded" ||
		!got.UpdatedAt.Equal(time.UnixMilli(1792515882360)) {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestGetConnectionRequest_Connected(t *testing.T) {
	// Inferred shape (not observed): connected with a non-null appId.
	body := `{"success":true,"message":"OK","data":{"connectionRequestId":"r","service":"github","status":"connected","appId":"app-1","errorCode":null,"errorMessage":null,"expiresAt":"2026-10-20T17:14:08.224Z","createdAt":"2026-10-20T17:04:08.224Z","updatedAt":1792515848224},"meta":{}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
	defer srv.Close()
	c, _ := openconnector.NewClient(openconnector.Options{BaseURL: srv.URL})
	got, err := c.GetConnectionRequest(context.Background(), "r")
	if err != nil || got.AppID == nil || *got.AppID != "app-1" || got.CreatedAt.IsZero() {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestGetConnectionRequest_NotFound(t *testing.T) {
	c, _ := newClient(t, 404, "connection_request_404.json")
	_, err := c.GetConnectionRequest(context.Background(), "nope")
	var ae *openconnector.APIError
	if !errors.Is(err, openconnector.ErrNotFound) || !errors.As(err, &ae) || ae.Code != "connection_request_not_found" {
		t.Fatalf("err = %v", err)
	}
}

func TestConnectWithAPIKey(t *testing.T) {
	c, rec := newClient(t, 200, "apikey_connect_github.json")
	got, err := c.ConnectWithAPIKey(context.Background(), "github", openconnector.APIKeyConnectRequest{APIKey: "k-1"})
	if err != nil {
		t.Fatal(err)
	}
	wantReq(t, rec, "POST", "/v1/connections/github/connect/api-key")
	if string(rec.Body) != `{"apiKey":"k-1"}` {
		t.Fatalf("body = %s (server is strict about extra fields)", rec.Body)
	}
	if got.ID != "3602ae65-8fd2-4372-afb8-7edf5785f7a6" || got.ConnectionName != "e4500f74-2f16-4c77-9eb1-4a660c4b5a77" ||
		got.Service != "github" || got.Status != "active" || got.AuthType != "api_key" || got.IsDefault || got.Comment != nil {
		t.Fatalf("got %+v", got)
	}

	_, err = c.ConnectWithAPIKey(context.Background(), "x", openconnector.APIKeyConnectRequest{APIKey: "k", Extra: map[string]string{"a": "b"}, Comment: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if m := bodyMap(t, rec); m["comment"] != "hi" || m["extra"].(map[string]any)["a"] != "b" {
		t.Fatalf("body = %v", m)
	}
}

func TestConnectWithAPIKey_VerificationFailed(t *testing.T) {
	c, _ := newClient(t, 400, "apikey_connect_17track_failed.json")
	_, err := c.ConnectWithAPIKey(context.Background(), "17track", openconnector.APIKeyConnectRequest{APIKey: "super-secret-key"})
	var ae *openconnector.APIError
	if !errors.As(err, &ae) || ae.Status != 400 || ae.Code != "credential_verification_failed" {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "super-secret-key") {
		t.Fatalf("error leaks api key: %v", err)
	}
	if s := fmt.Sprintf("%v %+v %#v", openconnector.APIKeyConnectRequest{APIKey: "super-secret-key"}, openconnector.APIKeyConnectRequest{APIKey: "super-secret-key"}, openconnector.APIKeyConnectRequest{APIKey: "super-secret-key"}); strings.Contains(s, "super-secret-key") {
		t.Fatal("request formatting leaks api key")
	}
}

func TestGetConnectionByID(t *testing.T) {
	c, rec := newClient(t, 200, "connection_by_id.json")
	got, err := c.GetConnectionByID(context.Background(), "3602ae65")
	if err != nil || got.ConnectionName == "" || got.AccountLabel != "octocat" || len(got.Scopes) != 0 {
		t.Fatalf("got %+v, %v", got, err)
	}
	wantReq(t, rec, "GET", "/v1/connections/by-id/3602ae65")

	c, _ = newClient(t, 404, "err404_connection_by_id.json")
	_, err = c.GetConnectionByID(context.Background(), "gone")
	var ae *openconnector.APIError
	if !errors.Is(err, openconnector.ErrNotFound) || !errors.As(err, &ae) || ae.Code != "app_not_found" {
		t.Fatalf("err = %v", err)
	}
}

func TestListConnections(t *testing.T) {
	c, rec := newClient(t, 200, "connections_list.json")
	got, err := c.ListConnections(context.Background(), "")
	if err != nil || len(got) != 1 || got[0].Service != "github" {
		t.Fatalf("got %+v, %v", got, err)
	}
	wantReq(t, rec, "GET", "/v1/connections")
	if rec.Query != "" {
		t.Fatalf("query = %q", rec.Query)
	}

	c, rec = newClient(t, 200, "connections_list_reauth.json")
	got, err = c.ListConnections(context.Background(), openconnector.ConnectionStatusReauthRequired)
	if err != nil || len(got) != 0 || rec.Query != "status=reauth_required" {
		t.Fatalf("got %+v, %v, query %q", got, err, rec.Query)
	}
}

func TestDeleteConnection(t *testing.T) {
	c, rec := newClient(t, 200, "connection_delete.json")
	for i := 0; i < 2; i++ { // idempotent: repeated delete is still nil
		if err := c.DeleteConnection(context.Background(), "github", "alias-1", true); err != nil {
			t.Fatal(err)
		}
	}
	wantReq(t, rec, "DELETE", "/api/connections/github")
	if m := bodyMap(t, rec); m["connectionName"] != "alias-1" || m["revoke"] != true {
		t.Fatalf("body = %v", m)
	}
	c, _ = newClient(t, 200, "connection_delete_unknown_service.json")
	if err := c.DeleteConnection(context.Background(), "nope", "x", false); err != nil {
		t.Fatal(err)
	}
	c, _ = newClient(t, 404, "err404_unknown_service_setup.json")
	if err := c.DeleteConnection(context.Background(), "nope", "x", false); err != nil {
		t.Fatalf("404 should count as deleted: %v", err)
	}
	c, _ = newClient(t, 401, "err401_runtime_tokens.json")
	if err := c.DeleteConnection(context.Background(), "github", "x", false); !errors.Is(err, openconnector.ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateRuntimeToken(t *testing.T) {
	c, rec := newClient(t, 200, "runtime_token_create_A.json")
	got, err := c.CreateRuntimeToken(context.Background(), openconnector.CreateRuntimeTokenRequest{
		Name: "task-1", AllowedActions: []string{"github.*"}, AllowedConnections: []string{"c1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantReq(t, rec, "POST", "/api/runtime-tokens")
	m := bodyMap(t, rec)
	for _, k := range []string{"allowedActions", "blockedActions", "allowedProxies", "allowedTriggers", "allowedConnections"} {
		if _, ok := m[k].([]any); !ok {
			t.Errorf("%s must serialize as an array, body = %s", k, rec.Body)
		}
	}
	if m["name"] != "task-1" || len(m["blockedActions"].([]any)) != 0 || len(m["allowedConnections"].([]any)) != 1 {
		t.Fatalf("body = %s", rec.Body)
	}
	if got.Token != "oct_REDACTED" || got.Record.ID != "c785d826-5267-488b-b05a-6fc0d8c2bf4b" ||
		got.Record.AllowedActions[0] != "github.*" || got.Record.CreatedAt.IsZero() {
		t.Fatalf("got %+v", got)
	}
}

func TestCreatedRuntimeToken_Redacts(t *testing.T) {
	tok := openconnector.CreatedRuntimeToken{Token: "oct_plaintext", Record: openconnector.RuntimeToken{ID: "id1", Name: "n"}}
	for _, s := range []string{fmt.Sprint(tok), fmt.Sprintf("%+v", tok), fmt.Sprintf("%#v", tok), fmt.Sprint(&tok)} {
		if strings.Contains(s, "oct_plaintext") {
			t.Fatalf("leaked: %s", s)
		}
	}
	var sb strings.Builder
	slog.New(slog.NewJSONHandler(&sb, nil)).Info("x", "tok", tok)
	if strings.Contains(sb.String(), "oct_plaintext") {
		t.Fatalf("slog leaked: %s", sb.String())
	}
}

func TestListRuntimeTokens(t *testing.T) {
	c, rec := newClient(t, 200, "runtime_tokens_list.json")
	got, err := c.ListRuntimeTokens(context.Background())
	if err != nil || len(got) != 2 || got[0].Name != "phase0-B" || got[1].AllowedConnections[0] == "" {
		t.Fatalf("got %+v, %v", got, err)
	}
	wantReq(t, rec, "GET", "/api/runtime-tokens")
}

func TestDeleteRuntimeToken(t *testing.T) {
	c, rec := newClient(t, 200, "runtime_token_delete_A.json")
	if err := c.DeleteRuntimeToken(context.Background(), "tok-1"); err != nil {
		t.Fatal(err)
	}
	wantReq(t, rec, "DELETE", "/api/runtime-tokens/tok-1")

	c, _ = newClient(t, 404, "runtime_token_delete_A_again.json")
	err := c.DeleteRuntimeToken(context.Background(), "tok-1")
	var ae *openconnector.APIError
	if !errors.Is(err, openconnector.ErrNotFound) || !errors.As(err, &ae) || ae.Code != "runtime_token_not_found" {
		t.Fatalf("err = %v", err)
	}
}

func TestErrorShapes(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		status   int
		file     string
		call     func(c *openconnector.Client) error
		sentinel error
		code     string
	}{
		{"v1 401", 401, "err401_v1_apps.json", func(c *openconnector.Client) error { _, err := c.ListProviders(ctx); return err }, openconnector.ErrUnauthorized, "unauthorized"},
		{"v1 admin 401", 401, "err401_connections.json", func(c *openconnector.Client) error { _, err := c.ListConnections(ctx, ""); return err }, openconnector.ErrUnauthorized, "unauthorized"},
		{"api 401", 401, "err401_runtime_tokens.json", func(c *openconnector.Client) error { _, err := c.ListRuntimeTokens(ctx); return err }, openconnector.ErrUnauthorized, "unauthorized"},
		{"api oauth 401", 401, "err401_runtime_tokens.json", func(c *openconnector.Client) error { _, err := c.ListOAuthConfigs(ctx); return err }, openconnector.ErrUnauthorized, "unauthorized"},
		{"v1 403", 403, "err401_v1_apps.json", func(c *openconnector.Client) error { _, err := c.GetProviderSetup(ctx, "x"); return err }, openconnector.ErrForbidden, "unauthorized"},
		{"api 403", 403, "err401_runtime_tokens.json", func(c *openconnector.Client) error {
			_, err := c.CreateRuntimeToken(ctx, openconnector.CreateRuntimeTokenRequest{Name: "n"})
			return err
		}, openconnector.ErrForbidden, "unauthorized"},
		{"v1 404 unknown service", 404, "err404_unknown_service_setup.json", func(c *openconnector.Client) error { _, err := c.GetProviderSetup(ctx, "nope"); return err }, openconnector.ErrNotFound, "unknown_service"},
		{"v1 400", 400, "err400_connect_bad_json.json", func(c *openconnector.Client) error {
			_, err := c.BeginOAuthConnect(ctx, "github", openconnector.BeginOAuthConnectRequest{})
			return err
		}, nil, "invalid_input"},
		{"v1 200 success=false", 200, "err404_connection_by_id.json", func(c *openconnector.Client) error { _, err := c.GetConnectionByID(ctx, "x"); return err }, nil, "app_not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newClient(t, tt.status, tt.file)
			err := tt.call(c)
			var ae *openconnector.APIError
			if !errors.As(err, &ae) {
				t.Fatalf("err = %v, want *APIError", err)
			}
			if ae.Status != tt.status || ae.Code != tt.code || ae.Message == "" {
				t.Fatalf("got %+v", ae)
			}
			if tt.sentinel != nil && !errors.Is(err, tt.sentinel) {
				t.Fatalf("errors.Is(%v, %v) = false", err, tt.sentinel)
			}
			for _, other := range []error{openconnector.ErrNotFound, openconnector.ErrUnauthorized, openconnector.ErrForbidden} {
				if other != tt.sentinel && errors.Is(err, other) {
					t.Fatalf("err wrongly matches %v", other)
				}
			}
			if strings.Contains(err.Error(), adminToken) {
				t.Fatalf("error leaks admin token: %v", err)
			}
		})
	}
}

func TestErrorNonJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(502)
		_, _ = w.Write([]byte("<html>bad gateway with secret</html>"))
	}))
	defer srv.Close()
	c, _ := openconnector.NewClient(openconnector.Options{BaseURL: srv.URL, AdminToken: adminToken})
	_, err := c.ListProviders(context.Background())
	var ae *openconnector.APIError
	if !errors.As(err, &ae) || ae.Status != 502 || ae.Code != "" || strings.Contains(err.Error(), "secret") {
		t.Fatalf("err = %v", err)
	}
}

func TestTransportErrorHasNoCredentials(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c, _ := openconnector.NewClient(openconnector.Options{BaseURL: url, AdminToken: adminToken})
	err := c.DeleteConnection(context.Background(), "github", "x", false)
	if err == nil || strings.Contains(err.Error(), adminToken) {
		t.Fatalf("err = %v", err)
	}
}
