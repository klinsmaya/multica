package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/multica-ai/multica/server/internal/auth"
	composio "github.com/multica-ai/multica/server/internal/integrations/composio"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	sdk "github.com/multica-ai/multica/server/pkg/composio"
	"github.com/redis/go-redis/v9"
)

func TestComposioVerifyUsesRealLoginAndRejectsSpoofedIdentity(t *testing.T) {
	seen := ""
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]string
		_ = json.NewDecoder(r.Body).Decode(&input)
		seen = input["subject"]
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":{"message":"identity mismatch","status":403,"code":403}}`))
	}))
	defer upstream.Close()
	client, err := sdk.NewClient(sdk.Options{Backend: "compat", APIKey: "control", BaseURL: upstream.URL + "/api/v3.1", AllowInsecureLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := composio.NewService(client, &composioFakeStore{}, composio.Config{Backend: "compat", MCPOrigin: "https://mcp.example", CallbackBaseURL: "https://multica.ai", StateSecret: []byte("fixture-state-signing-key")})
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Composio: svc}
	withComposioMCPAppsFlag(t, h, true)
	guarded := middleware.Auth(nil, nil, nil)(RequireHumanActor(http.HandlerFunc(h.ComposioVerifyIdentity)))
	forged := testutil.JSONRequest("GET", "/api/integrations/composio/verify?verify_ticket=ticket", nil)
	forged.Header.Set("X-User-ID", composioTestUserID)
	testutil.Call(t, guarded.ServeHTTP, forged).Want(401)
	if seen != "" {
		t.Fatal("spoofed header reached gateway")
	}
	loginUser := "33333333-3333-3333-3333-333333333333"
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": loginUser, "exp": time.Now().Add(time.Minute).Unix()}).SignedString(auth.JWTSecret())
	if err != nil {
		t.Fatal(err)
	}
	browser := testutil.JSONRequest("GET", "/api/integrations/composio/verify?verify_ticket=ticket", nil)
	browser.AddCookie(&http.Cookie{Name: auth.AuthCookieName, Value: token})
	browser.Header.Set("X-User-ID", composioTestUserID)
	testutil.Call(t, guarded.ServeHTTP, browser).Want(403)
	if seen != loginUser {
		t.Fatalf("expected authenticated subject, got %q", seen)
	}
	agent := testutil.JSONRequest("GET", "/api/integrations/composio/verify?verify_ticket=ticket", nil)
	agent.Header.Set("X-Actor-Source", "task_token")
	agent.Header.Set("X-User-ID", composioTestUserID)
	testutil.Call(t, h.ComposioVerifyIdentity, agent).Want(401)
	bearer := testutil.JSONRequest("GET", "/api/integrations/composio/verify?verify_ticket=ticket", nil)
	bearer.Header.Set("Authorization", "Bearer "+token)
	testutil.Call(t, guarded.ServeHTTP, bearer).Want(401)
	cachedPAT := auth.NewPATCache(&compatPATCache{user: loginUser})
	patGuard := middleware.Auth(nil, cachedPAT, nil)(RequireHumanActor(http.HandlerFunc(h.ComposioVerifyIdentity)))
	pat := testutil.JSONRequest("GET", "/api/integrations/composio/verify?verify_ticket=ticket", nil)
	pat.Header.Set("Authorization", "Bearer mul_valid-cached-fixture")
	testutil.Call(t, patGuard.ServeHTTP, pat).Want(401)

}

// This fixture supplies a valid PAT cache hit; Auth and the handler remain real.
type compatPATCache struct {
	redis.UniversalClient
	user string
}

func (c *compatPATCache) Get(context.Context, string) *redis.StringCmd {
	return redis.NewStringResult(c.user, nil)
}
