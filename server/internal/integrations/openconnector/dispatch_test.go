package openconnector

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/openconnector"
)

const testToken = "oct_plaintext_token"

func seedConnection(t *testing.T, store *fakeStore, user pgtype.UUID, service, id, name, label string, isDefault bool) {
	t.Helper()
	if _, err := store.UpsertUserConnectorConnection(context.Background(), db.UpsertUserConnectorConnectionParams{
		Provider: ProviderName, UserID: user, Service: service, ConnectionID: id, ConnectionName: name,
		AuthType: "oauth", Label: label, IsDefault: isDefault,
	}); err != nil {
		t.Fatal(err)
	}
}

func dispatchFixture(t *testing.T) (*Service, *fakeSDK, *fakeStore, db.Agent) {
	t.Helper()
	sdk := defaultSDK()
	sdk.tokenResp = &openconnector.CreatedRuntimeToken{Token: testToken, Record: openconnector.RuntimeToken{ID: "tok-1"}}
	store := (&fakeStore{}).publish("github").publish("openai").publish("notion")
	owner := newTestUser()
	seedConnection(t, store, owner, "github", "app-gh1", "alias-gh1", "work", true)
	seedConnection(t, store, owner, "github", "app-gh2", "alias-gh2", "", false)
	seedConnection(t, store, owner, "openai", "app-oa", "alias-oa", "", true)
	seedConnection(t, store, owner, "notion", "app-no", "alias-no", "", true)
	agent := db.Agent{ID: dbid.NewV7(), OwnerID: owner, ConnectorServiceAllowlist: []string{" GitHub ", "openai"}}
	return newTestService(t, sdk, store), sdk, store, agent
}

func TestBuildTaskOverlayScopesTokenToAllConnections(t *testing.T) {
	svc, sdk, _, agent := dispatchFixture(t)
	res, err := svc.BuildTaskOverlay(context.Background(), pgtype.UUID{}, pgtype.UUID{}, agent)
	if err != nil {
		t.Fatal(err)
	}
	req := sdk.tokenReqs[0]
	if !strings.HasPrefix(req.Name, "multica-task-") {
		t.Fatalf("name = %q", req.Name)
	}
	// Every allowed connection (both github ones), none of notion (not allowlisted).
	if got := strings.Join(req.AllowedConnections, ","); got != "app-gh1,app-gh2,app-oa" {
		t.Fatalf("allowedConnections = %s", got)
	}
	if got := strings.Join(req.AllowedActions, ","); got != "github.*,openai.*" {
		t.Fatalf("allowedActions = %s", got)
	}
	if res.ResourceHandle != "tok-1" {
		t.Fatalf("handle = %q", res.ResourceHandle)
	}

	// Empty lists must serialize as [] (omitting a field resets or rejects it).
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{`"blockedActions":[]`, `"allowedProxies":[]`, `"allowedTriggers":[]`} {
		if !strings.Contains(string(body), f) {
			t.Errorf("request body missing %s: %s", f, body)
		}
	}

	var overlay struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(res.MCPOverlay, &overlay); err != nil {
		t.Fatal(err)
	}
	srv := overlay.MCPServers["open-connector"]
	if srv.Type != "http" || srv.URL != "https://oc.example.com/mcp" || srv.Headers["Authorization"] != "Bearer "+testToken {
		t.Fatalf("overlay = %+v", srv)
	}

	// One ConnectedApp per connection, carrying the connection name; the token
	// must not leak into the non-secret metadata.
	if len(res.ConnectedApps) != 3 {
		t.Fatalf("apps = %+v", res.ConnectedApps)
	}
	first := res.ConnectedApps[0]
	if first.Provider != "open-connector" || first.ToolkitSlug != "github" || first.ToolkitName != "work" || first.ConnectionName != "alias-gh1" || !first.IsDefault || res.ConnectedApps[1].IsDefault {
		t.Fatalf("first app = %+v", first)
	}
	if res.ConnectedApps[1].ToolkitName != "GitHub" || res.ConnectedApps[1].ConnectionName != "alias-gh2" {
		t.Fatalf("second app = %+v", res.ConnectedApps[1])
	}
	meta, _ := json.Marshal(res.ConnectedApps)
	if strings.Contains(string(meta), testToken) {
		t.Fatal("token leaked into connected apps")
	}
}

func TestBuildTaskOverlaySkipsReauthRequired(t *testing.T) {
	svc, sdk, store, agent := dispatchFixture(t)
	for i := range store.rows {
		if store.rows[i].ConnectionID == "app-gh2" {
			store.rows[i].Status = "reauth_required"
		}
	}
	if _, err := svc.BuildTaskOverlay(context.Background(), pgtype.UUID{}, pgtype.UUID{}, agent); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(sdk.tokenReqs[0].AllowedConnections, ","); got != "app-gh1,app-oa" {
		t.Fatalf("allowedConnections = %s", got)
	}
}

func TestBuildTaskOverlayFollowsServicePolicy(t *testing.T) {
	svc, sdk, store, agent := dispatchFixture(t)
	ctx := context.Background()

	// A tool allowlist narrows allowedActions to the listed actions.
	store.policies[0].AllowedTools = []string{"create_issue", "list_repos"}
	if _, err := svc.BuildTaskOverlay(ctx, pgtype.UUID{}, pgtype.UUID{}, agent); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(sdk.tokenReqs[0].AllowedActions, ","); got != "github.create_issue,github.list_repos,openai.*" {
		t.Fatalf("allowedActions = %s", got)
	}

	// A delisted service, or one restricted to other users, is not mounted.
	store.policies[0].Enabled = false
	store.policies[1].AllowedUserIds = []pgtype.UUID{newTestUser()}
	res, err := svc.BuildTaskOverlay(ctx, pgtype.UUID{}, pgtype.UUID{}, agent)
	if err != nil || len(res.MCPOverlay) != 0 || len(sdk.tokenReqs) != 1 {
		t.Fatalf("delisted: res=%+v err=%v tokens=%d", res, err, len(sdk.tokenReqs))
	}
	store.policies[1].AllowedUserIds = []pgtype.UUID{agent.OwnerID}
	if _, err := svc.BuildTaskOverlay(ctx, pgtype.UUID{}, pgtype.UUID{}, agent); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(sdk.tokenReqs[1].AllowedConnections, ","); got != "app-oa" {
		t.Fatalf("allowedConnections = %s", got)
	}
}

func TestBuildTaskOverlayZeroCases(t *testing.T) {
	svc, sdk, _, agent := dispatchFixture(t)
	ctx := context.Background()

	noOwner := agent
	noOwner.OwnerID = pgtype.UUID{}
	emptyAllow := agent
	emptyAllow.ConnectorServiceAllowlist = nil
	noMatch := agent
	noMatch.ConnectorServiceAllowlist = []string{"slack"}

	for name, a := range map[string]db.Agent{"no owner": noOwner, "empty allowlist": emptyAllow, "no active match": noMatch} {
		res, err := svc.BuildTaskOverlay(ctx, pgtype.UUID{}, pgtype.UUID{}, a)
		if err != nil || len(res.MCPOverlay) != 0 || res.ResourceHandle != "" || len(res.ConnectedApps) != 0 {
			t.Errorf("%s: res=%+v err=%v", name, res, err)
		}
	}
	if len(sdk.tokenReqs) != 0 {
		t.Fatalf("a token was minted for a zero overlay: %d", len(sdk.tokenReqs))
	}
}

func TestReleaseTaskResourcesIdempotent(t *testing.T) {
	svc, sdk, _, _ := dispatchFixture(t)
	ctx := context.Background()

	if err := svc.ReleaseTaskResources(ctx, pgtype.UUID{}, ""); err != nil || len(sdk.tokenDels) != 0 {
		t.Fatalf("empty handle: err=%v dels=%v", err, sdk.tokenDels)
	}
	if err := svc.ReleaseTaskResources(ctx, pgtype.UUID{}, "tok-1"); err != nil {
		t.Fatal(err)
	}
	// Second delete: the server answers 404, which counts as released.
	sdk.tokenDelErr = &openconnector.APIError{Status: 404, Code: "runtime_token_not_found"}
	if err := svc.ReleaseTaskResources(ctx, pgtype.UUID{}, "tok-1"); err != nil {
		t.Fatalf("404 must be success: %v", err)
	}
	sdk.tokenDelErr = &openconnector.APIError{Status: 500}
	if err := svc.ReleaseTaskResources(ctx, pgtype.UUID{}, "tok-1"); err == nil || errors.Is(err, openconnector.ErrNotFound) {
		t.Fatalf("500 must surface, got %v", err)
	}
}
