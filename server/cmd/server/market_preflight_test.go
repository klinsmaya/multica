package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/marketcatalog"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/featureflag"
)

// marketFixture is a router with market_v1 on, a live catalog served over
// HTTP from typed listings, and a workspace of its own so row counts and
// runtime visibility see only this test's rows.
type marketFixture struct {
	*testutil.Fixture
	h   *handler.Handler
	srv *httptest.Server
}

type marketMember struct {
	userID string
	jwt    string
}

func newMarketTestServer(t *testing.T, listings ...marketcatalog.Listing) *marketFixture {
	t.Helper()
	if testPool == nil {
		t.Skip("no database connection")
	}
	provider := featureflag.NewStaticProvider()
	provider.Set(featureflags.MarketV1, featureflag.Rule{Default: true})
	hub := realtime.NewHub()
	go hub.Run()
	router, h := NewRouterWithOptions(testPool, hub, events.New(), analytics.NoopClient{}, nil, RouterOptions{
		FeatureFlags: featureflag.NewService(provider),
	})
	catalog := httptest.NewServer(marketcatalog.FixtureHandler(listings...))
	t.Cleanup(catalog.Close)
	h.MarketCatalog = marketcatalog.New(marketcatalog.Config{BaseURL: catalog.URL})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	unbound := testutil.New(testPool, "", testUserID)
	slug := "market-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	wsID := unbound.Workspace(t, "Market preflight", slug)
	fx := testutil.New(testPool, wsID, testUserID)
	fx.Cleanup(t, `DELETE FROM skill WHERE workspace_id = $1`, wsID)
	fx.Cleanup(t, `DELETE FROM workspace_mcp_server WHERE workspace_id = $1`, wsID)
	return &marketFixture{Fixture: fx, h: h, srv: srv}
}

func (fx *marketFixture) memberWithJWT(t *testing.T, role string) marketMember {
	t.Helper()
	email := fmt.Sprintf("market-%s-%s@multica.test", role, uuid.NewString()[:8])
	userID := fx.User(t, "Market "+role, email)
	fx.Member(t, fx.WorkspaceID, userID, role)
	jwt, err := generateTestJWT(userID, email, "Market "+role)
	if err != nil {
		t.Fatalf("jwt: %v", err)
	}
	return marketMember{userID: userID, jwt: jwt}
}

func (fx *marketFixture) runtime(t *testing.T, owner marketMember, provider, visibility string) string {
	t.Helper()
	return fx.Runtime(t, provider+" "+visibility+" "+uuid.NewString()[:6], testutil.Cols{
		"provider":   provider,
		"visibility": visibility,
		"owner_id":   owner.userID,
	})
}

func (fx *marketFixture) cacheModels(t *testing.T, runtimeID string, ids ...string) {
	t.Helper()
	models := make([]handler.ModelEntry, 0, len(ids))
	for _, id := range ids {
		models = append(models, handler.ModelEntry{ID: id})
	}
	if err := fx.h.ModelCatalogCache.Put(context.Background(), runtimeID, models, nil, true); err != nil {
		t.Fatalf("cache models: %v", err)
	}
}

func (fx *marketFixture) workspaceMcpServer(t *testing.T, name string) string {
	t.Helper()
	return fx.Insert(t, "workspace_mcp_server", testutil.Cols{
		"workspace_id": fx.WorkspaceID,
		"name":         name,
		"config":       testutil.Raw(`'{"type":"http","url":"https://mcp.example/"}'::jsonb`),
		"created_by":   fx.UserID,
	})
}

var marketCountedTables = map[string]string{
	"agent_task_queue": `SELECT count(*) FROM agent_task_queue
		WHERE runtime_id IN (SELECT id FROM agent_runtime WHERE workspace_id = $1)
		   OR agent_id IN (SELECT id FROM agent WHERE workspace_id = $1)`,
	"chat_session":         `SELECT count(*) FROM chat_session WHERE workspace_id = $1`,
	"agent":                `SELECT count(*) FROM agent WHERE workspace_id = $1`,
	"skill":                `SELECT count(*) FROM skill WHERE workspace_id = $1`,
	"workspace_mcp_server": `SELECT count(*) FROM workspace_mcp_server WHERE workspace_id = $1`,
}

func (fx *marketFixture) rowCounts(t *testing.T) map[string]int {
	t.Helper()
	out := map[string]int{}
	for table, q := range marketCountedTables {
		out[table] = fx.Count(t, q, fx.WorkspaceID)
	}
	return out
}

func (fx *marketFixture) do(t *testing.T, m marketMember, method, path string, body any, edit func(*http.Request)) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, fx.srv.URL+path, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.jwt)
	req.Header.Set("X-Workspace-ID", fx.WorkspaceID)
	if edit != nil {
		edit(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

type preflightWire struct {
	Status       string `json:"status"`
	Source       string `json:"source"`
	EvaluatedFor struct {
		UserID string `json:"user_id"`
		Role   string `json:"role"`
	} `json:"evaluated_for"`
	Model struct {
		Status    string  `json:"status"`
		Reason    string  `json:"reason"`
		RuntimeID *string `json:"runtime_id"`
		Model     *string `json:"model"`
	} `json:"model"`
	Tools     []checkWire `json:"tools"`
	Knowledge []checkWire `json:"knowledge"`
}

type checkWire struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Binding *struct {
		Action string  `json:"action"`
		ID     *string `json:"id"`
	} `json:"binding"`
}

func (fx *marketFixture) preflight(t *testing.T, m marketMember, listingID string, body any, edit func(*http.Request)) preflightWire {
	t.Helper()
	resp := fx.do(t, m, http.MethodPost, "/api/market/listings/"+listingID+"/preflight", body, edit)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&e)
		t.Fatalf("preflight %s: status %d: %v", listingID, resp.StatusCode, e)
	}
	var out preflightWire
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func liveListing(id string, m marketcatalog.Manifest) marketcatalog.Listing {
	m.Agent = marketcatalog.AgentTemplate{Name: "Listing " + id}
	return marketcatalog.Listing{
		Summary:  marketcatalog.ListingSummary{ID: marketcatalog.ListingID(id), Name: "Listing " + id, LatestVersion: "1.0.0"},
		Version:  "1.0.0",
		Manifest: m,
	}
}

var sonnet = marketcatalog.ModelNeed{Provider: "claude", Models: []string{"claude-sonnet-4-5"}}

func TestMarketPreflightIgnoresClientActor(t *testing.T) {
	fx := newMarketTestServer(t, liveListing("acme.notes", marketcatalog.Manifest{Model: sonnet}))
	dana := fx.memberWithJWT(t, "member")
	sam := fx.memberWithJWT(t, "member")
	runtimeID := fx.runtime(t, dana, "claude", "private")
	fx.cacheModels(t, runtimeID, "claude-sonnet-4-5")

	body := map[string]any{
		"version": "1.0.0", "runtime_id": runtimeID,
		"actor": dana.userID, "user_id": dana.userID, "actor_type": "member",
	}
	forge := func(r *http.Request) { r.Header.Set("X-User-ID", dana.userID) }

	asSam := fx.preflight(t, sam, "acme.notes", body, forge)
	if asSam.EvaluatedFor.UserID != sam.userID {
		t.Fatalf("evaluated_for = %s, want the authenticated member %s", asSam.EvaluatedFor.UserID, sam.userID)
	}
	if asSam.Model.Status != "blocked" || asSam.Model.Reason != "runtime_not_found" {
		t.Fatalf("sam model = %s/%s, want blocked/runtime_not_found", asSam.Model.Status, asSam.Model.Reason)
	}

	asDana := fx.preflight(t, dana, "acme.notes", body, forge)
	if asDana.EvaluatedFor.UserID != dana.userID || asDana.Model.Status != "satisfied" {
		t.Fatalf("dana: evaluated_for %s model %s, want dana satisfied", asDana.EvaluatedFor.UserID, asDana.Model.Status)
	}
}

func TestMarketPreflightReadyEnqueuesNothing(t *testing.T) {
	fx := newMarketTestServer(t, liveListing("acme.reviewer", marketcatalog.Manifest{
		Model: sonnet,
		Tools: []marketcatalog.ToolNeed{{ServerName: "github"}},
		Knowledge: []marketcatalog.KnowledgeNeed{{
			Source: marketcatalog.KnowledgeSkill,
			Name:   "code-review",
			Bundle: &marketcatalog.SkillBundle{Content: "# Review the diff"},
		}},
	}))
	owner := fx.memberWithJWT(t, "owner")
	runtimeID := fx.runtime(t, owner, "claude", "public")
	fx.cacheModels(t, runtimeID, "claude-sonnet-4-5")
	fx.workspaceMcpServer(t, "github")

	before := fx.rowCounts(t)
	for range 3 {
		report := fx.preflight(t, owner, "acme.reviewer", map[string]any{"version": "1.0.0"}, nil)
		if report.Status != "ready" || report.Source != "live" {
			t.Fatalf("status = %q source = %q, want ready from live: %+v", report.Status, report.Source, report)
		}
	}
	if after := fx.rowCounts(t); !maps.Equal(before, after) {
		t.Fatalf("preflight wrote rows: before %v after %v", before, after)
	}

	agentID := fx.Agent(t, "counter probe", runtimeID)
	fx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID})
	if got := fx.rowCounts(t)["agent_task_queue"]; got != before["agent_task_queue"]+1 {
		t.Fatalf("agent_task_queue count = %d after one insert, want %d; the counter cannot see task rows", got, before["agent_task_queue"]+1)
	}
}

// TestMarketSkillCreatorsMatchRouter pins checkKnowledge's provision verdict
// to POST /api/skills. If the route grows a role gate, the role it rejects
// must stop getting provision from preflight.
func TestMarketSkillCreatorsMatchRouter(t *testing.T) {
	fx := newMarketTestServer(t, liveListing("acme.skills", marketcatalog.Manifest{
		Model: marketcatalog.ModelNeed{Provider: "claude"},
		Knowledge: []marketcatalog.KnowledgeNeed{{
			Source: marketcatalog.KnowledgeSkill,
			Name:   "never-created",
			Bundle: &marketcatalog.SkillBundle{Content: "# Bundled"},
		}},
	}))
	for _, role := range []string{"owner", "admin", "member"} {
		t.Run(role, func(t *testing.T) {
			m := fx.memberWithJWT(t, role)
			report := fx.preflight(t, m, "acme.skills", map[string]any{"version": "1.0.0"}, nil)
			k := report.Knowledge[0]
			provisions := k.Status == "satisfied" && k.Binding != nil && k.Binding.Action == "provision"

			resp := fx.do(t, m, http.MethodPost, "/api/skills", map[string]any{
				"name":    "pin-" + role + "-" + uuid.NewString()[:8],
				"content": "# Pin",
			}, nil)
			resp.Body.Close()
			routerAllows := resp.StatusCode != http.StatusForbidden

			if routerAllows != provisions {
				t.Fatalf("POST /api/skills as %s: status %d; preflight knowledge %+v. They must agree.", role, resp.StatusCode, k)
			}
			if (role == "owner" || role == "member") && !routerAllows {
				t.Fatalf("POST /api/skills rejected %s for role (status %d)", role, resp.StatusCode)
			}
		})
	}
}

// TestMarketMcpCuratorsMatchRouter pins canCurateMcpLibrary to the role gate
// on POST /api/workspaces/{id}/mcp-servers for every role.
func TestMarketMcpCuratorsMatchRouter(t *testing.T) {
	fx := newMarketTestServer(t, liveListing("acme.tools", marketcatalog.Manifest{
		Model: marketcatalog.ModelNeed{Provider: "claude"},
		Tools: []marketcatalog.ToolNeed{{
			ServerName: "never-created",
			Template:   &marketcatalog.McpTemplate{Transport: "http", URL: "https://mcp.example/"},
		}},
	}))
	want := map[string]bool{"owner": true, "admin": true, "member": false}
	for role, curates := range want {
		t.Run(role, func(t *testing.T) {
			m := fx.memberWithJWT(t, role)
			report := fx.preflight(t, m, "acme.tools", map[string]any{"version": "1.0.0"}, nil)
			tool := report.Tools[0]
			provisions := tool.Status == "satisfied" && tool.Binding != nil && tool.Binding.Action == "provision"

			resp := fx.do(t, m, http.MethodPost, "/api/workspaces/"+fx.WorkspaceID+"/mcp-servers", map[string]any{
				"name":   "pin-" + role,
				"config": map[string]any{"type": "http", "url": "https://mcp.example/"},
			}, nil)
			resp.Body.Close()
			routerAllows := resp.StatusCode != http.StatusForbidden

			if routerAllows != provisions || routerAllows != curates {
				t.Fatalf("role %s: router status %d, preflight tool %+v, want both to say curate=%v", role, resp.StatusCode, tool, curates)
			}
			if !curates && tool.Reason != "mcp_library_admin_only" {
				t.Fatalf("role %s: tool reason = %q, want mcp_library_admin_only", role, tool.Reason)
			}
		})
	}
}

func TestMarketRoutesAre404WhenFlagOff(t *testing.T) {
	if testServer == nil {
		t.Skip("no database connection")
	}
	cases := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/market/listings", nil},
		{http.MethodGet, "/api/market/listings/sample.notes", nil},
		{http.MethodPost, "/api/market/listings/sample.notes/preflight", map[string]any{"version": "0.3.0"}},
	}
	for _, tc := range cases {
		resp := authRequest(t, tc.method, tc.path, tc.body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s %s with market_v1 off: status %d, want 404", tc.method, tc.path, resp.StatusCode)
		}
	}
}

// TestMarketRoutesRejectTaskTokens: the same member passes with a session and is
// refused with a task token bound to them, so the refusal is RequireHumanActor's
// and not a membership miss.
func TestMarketRoutesRejectTaskTokens(t *testing.T) {
	fx := newMarketTestServer(t, liveListing("acme.notes", marketcatalog.Manifest{Model: marketcatalog.ModelNeed{Provider: "claude"}}))
	member := fx.memberWithJWT(t, "member")
	runtimeID := fx.runtime(t, member, "claude", "public")
	agentID := fx.Agent(t, "task token holder", runtimeID)
	taskID := fx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID})
	token := "mat_test_market_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	fx.Insert(t, "task_token", testutil.Cols{
		"token_hash":   auth.HashToken(token),
		"task_id":      taskID,
		"agent_id":     agentID,
		"workspace_id": fx.WorkspaceID,
		"user_id":      member.userID,
		"expires_at":   testutil.Raw(`now() + interval '1 hour'`),
	})
	asTask := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }

	cases := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/market/listings", nil},
		{http.MethodGet, "/api/market/listings/acme.notes", nil},
		{http.MethodPost, "/api/market/listings/acme.notes/preflight", map[string]any{"version": "1.0.0"}},
	}
	for _, tc := range cases {
		session := fx.do(t, member, tc.method, tc.path, tc.body, nil)
		session.Body.Close()
		if session.StatusCode != http.StatusOK {
			t.Fatalf("%s %s with a session: status %d, want 200", tc.method, tc.path, session.StatusCode)
		}

		resp := fx.do(t, member, tc.method, tc.path, tc.body, asTask)
		var body struct {
			Error string `json:"error"`
		}
		readJSON(t, resp, &body)
		if resp.StatusCode != http.StatusForbidden || body.Error != "this endpoint is only available to human actors" {
			t.Fatalf("%s %s with a task token: status %d error %q, want 403 from RequireHumanActor", tc.method, tc.path, resp.StatusCode, body.Error)
		}
	}
}

func TestMarketStubCatalogIsNeverReady(t *testing.T) {
	fx := newMarketTestServer(t)
	fx.h.MarketCatalog = marketcatalog.New(marketcatalog.Config{})
	owner := fx.memberWithJWT(t, "owner")
	runtimeID := fx.runtime(t, owner, "claude", "public")
	fx.cacheModels(t, runtimeID, "claude-sonnet-4-5")

	resp := fx.do(t, owner, http.MethodGet, "/api/market/listings", nil, nil)
	var page struct {
		Source string `json:"source"`
		Items  []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	readJSON(t, resp, &page)
	if page.Source != "stub" || len(page.Items) == 0 {
		t.Fatalf("browse = %+v, want stub samples", page)
	}

	report := fx.preflight(t, owner, "sample.notes", map[string]any{"version": "0.3.0"}, nil)
	if report.Source != "stub" || report.Status != "blocked" || report.Model.Status != "satisfied" {
		t.Fatalf("sample.notes: source %q status %q model %q; want a stub report that is blocked although its model check passes", report.Source, report.Status, report.Model.Status)
	}
}
