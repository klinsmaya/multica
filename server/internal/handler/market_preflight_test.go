package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/featureflags"
)

func TestMarketPreflight_FlagOff(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withFeatureFlag(t, testHandler, featureflags.MarketCatalogV1, false)

	w := postMarketPreflight(t, testUserID, map[string]any{})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if got := marketErrorCode(t, w); got != "market_catalog_disabled" {
		t.Fatalf("code = %s", got)
	}
}

func TestMarketPreflight_MemberBlocked(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withFeatureFlag(t, testHandler, featureflags.MarketCatalogV1, true)
	withVCSAvailable(t, true)
	withMarketModelCache(t)

	memberID := insertMarketUser(t, "member")
	skillName := insertMarketSkill(t, testUserID)
	mcpName := insertMarketMCP(t, "member-mcp")
	connID := insertMarketVCS(t, "member")

	body := marketPreflightBody(testWorkspaceID, time.Now().Add(time.Hour), []map[string]string{
		{"kind": "model", "ref": "missing-model", "scope": "read-only"},
		{"kind": "tool", "ref": "skill:" + skillName, "scope": "read-write"},
		{"kind": "tool", "ref": "mcp:" + mcpName, "scope": "read-write"},
		{"kind": "connection", "ref": connID, "scope": "read-write"},
		{"kind": "knowledge", "ref": "notes", "scope": "read-only"},
	}, nil)
	w := postMarketPreflight(t, memberID, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	got := decodePreflight(t, w)
	if got.Status != "blocked" || got.TaskStarted {
		t.Fatalf("result = %+v", got)
	}
	want := []marketDenial{
		{Kind: "model", Ref: "missing-model", Code: "model.denied"},
		{Kind: "tool", Ref: "skill:" + skillName, Code: "tool.denied"},
		{Kind: "tool", Ref: "mcp:" + mcpName, Code: "tool.denied"},
		{Kind: "connection", Ref: connID, Code: "connection.denied"},
		{Kind: "knowledge", Ref: "notes", Code: "knowledge.denied"},
	}
	if len(got.Denials) != len(want) {
		t.Fatalf("denials = %+v", got.Denials)
	}
	for i := range want {
		if got.Denials[i] != want[i] {
			t.Fatalf("denial %d = %+v, want %+v", i, got.Denials[i], want[i])
		}
	}
}

func TestMarketPreflight_KnowledgeBlocksOwnerAndReadyDoesNotEnqueue(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withFeatureFlag(t, testHandler, featureflags.MarketCatalogV1, true)
	withVCSAvailable(t, true)
	cache := withMarketModelCache(t)

	runtimeID := insertMarketRuntime(t, testUserID, "public")
	if err := cache.Put(context.Background(), runtimeID, []ModelEntry{{ID: "market-model", Label: "Market"}}, nil, true); err != nil {
		t.Fatalf("put catalog: %v", err)
	}
	skillName := insertMarketSkill(t, testUserID)
	mcpName := insertMarketMCP(t, "owner-mcp")
	connID := insertMarketVCS(t, "owner")

	passing := []map[string]string{
		{"kind": "model", "ref": "market-model", "scope": "read-only"},
		{"kind": "tool", "ref": "skill:" + skillName, "scope": "read-write"},
		{"kind": "tool", "ref": "mcp:" + mcpName, "scope": "read-write"},
		{"kind": "connection", "ref": connID, "scope": "read-write"},
	}
	blockedDemands := append(append([]map[string]string{}, passing...), map[string]string{
		"kind": "knowledge", "ref": "notes", "scope": "read-only",
	})
	actor := map[string]any{"userId": "00000000-0000-4000-8000-000000000099", "source": "client"}

	before := agentTaskCount(t)
	w := postMarketPreflight(t, testUserID, marketPreflightBody(testWorkspaceID, time.Now().Add(time.Hour), blockedDemands, actor))
	if w.Code != http.StatusOK {
		t.Fatalf("blocked status = %d: %s", w.Code, w.Body.String())
	}
	blocked := decodePreflight(t, w)
	if blocked.Status != "blocked" || blocked.TaskStarted || blocked.ActorUserID != testUserID {
		t.Fatalf("blocked = %+v, actor want %s", blocked, testUserID)
	}
	if len(blocked.Denials) != 1 || blocked.Denials[0].Code != "knowledge.denied" {
		t.Fatalf("denials = %+v", blocked.Denials)
	}
	if got := agentTaskCount(t); got != before {
		t.Fatalf("agent_task_queue count %d, want %d after blocked preflight", got, before)
	}

	readyW := postMarketPreflight(t, testUserID, marketPreflightBody(testWorkspaceID, time.Now().Add(time.Hour), passing, actor))
	if readyW.Code != http.StatusOK {
		t.Fatalf("ready status = %d: %s", readyW.Code, readyW.Body.String())
	}
	ready := decodePreflight(t, readyW)
	if ready.Status != "ready" || ready.TaskStarted || len(ready.Denials) != 0 || ready.ActorUserID != testUserID {
		t.Fatalf("ready = %+v, actor want %s", ready, testUserID)
	}
	if got := agentTaskCount(t); got != before {
		t.Fatalf("agent_task_queue count %d, want %d after ready preflight", got, before)
	}
}

func TestMarketPreflight_MachineCredentialRejected(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withFeatureFlag(t, testHandler, featureflags.MarketCatalogV1, true)
	before := agentTaskCount(t)
	for _, source := range []string{"cloud_pat", "task_token"} {
		req := newRequestAsUser(testUserID, http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/market/preflight", marketPreflightBody(testWorkspaceID, time.Now().Add(time.Hour), []map[string]string{
			{"kind": "knowledge", "ref": "notes", "scope": "read-only"},
		}, map[string]any{"source": "client", "userId": "forged"}))
		req = withURLParam(req, "id", testWorkspaceID)
		req.Header.Set("X-Actor-Source", source)
		w := httptest.NewRecorder()
		testHandler.PreflightMarket(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d: %s", source, w.Code, w.Body.String())
		}
		if got := marketErrorCode(t, w); got != "identity.machine_credential" {
			t.Fatalf("%s code = %s", source, got)
		}
	}
	if got := agentTaskCount(t); got != before {
		t.Fatalf("agent_task_queue count %d, want %d", got, before)
	}
}

func TestMarketPreflight_PrivateRuntimeModelDenied(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withFeatureFlag(t, testHandler, featureflags.MarketCatalogV1, true)
	cache := withMarketModelCache(t)
	runtimeID := insertMarketRuntime(t, testUserID, "private")
	if err := cache.Put(context.Background(), runtimeID, []ModelEntry{{ID: "private-model", Label: "Private"}}, nil, true); err != nil {
		t.Fatalf("put catalog: %v", err)
	}
	memberID := insertMarketUser(t, "member")
	w := postMarketPreflight(t, memberID, marketPreflightBody(testWorkspaceID, time.Now().Add(time.Hour), []map[string]string{
		{"kind": "model", "ref": "private-model", "scope": "read-only"},
	}, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	got := decodePreflight(t, w)
	if got.Status != "blocked" || got.TaskStarted || len(got.Denials) != 1 || got.Denials[0].Code != "model.denied" {
		t.Fatalf("result = %+v", got)
	}
}

func TestMarketPreflight_ScopeMismatch(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withFeatureFlag(t, testHandler, featureflags.MarketCatalogV1, true)

	body := marketPreflightBody("00000000-0000-4000-8000-0000000000aa", time.Now().Add(time.Hour), nil, nil)
	w := postMarketPreflight(t, testUserID, body)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if got := marketErrorCode(t, w); got != "scope.mapping_mismatch" {
		t.Fatalf("code = %s", got)
	}
}

func TestMarketPreflight_IdentityGates(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withFeatureFlag(t, testHandler, featureflags.MarketCatalogV1, true)

	tests := []struct {
		name   string
		edit   func(map[string]any)
		status int
		code   string
	}{
		{
			name:   "consumer",
			edit:   func(body map[string]any) { body["consumerRef"].(map[string]any)["consumerType"] = "tencent" },
			status: http.StatusBadRequest,
			code:   "consumer.type",
		},
		{
			name:   "scope kind",
			edit:   func(body map[string]any) { body["targetScope"].(map[string]any)["kind"] = "squad" },
			status: http.StatusBadRequest,
			code:   "scope.kind",
		},
		{
			name:   "unverified",
			edit:   func(body map[string]any) { body["verification"] = "unverified" },
			status: http.StatusForbidden,
			code:   "identity.unverified",
		},
		{
			name:   "expired",
			edit:   func(body map[string]any) { body["expiry"] = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339) },
			status: http.StatusForbidden,
			code:   "identity.expired",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := marketPreflightBody(testWorkspaceID, time.Now().Add(time.Hour), nil, nil)
			tt.edit(body)
			w := postMarketPreflight(t, testUserID, body)
			if w.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", w.Code, tt.status, w.Body.String())
			}
			if got := marketErrorCode(t, w); got != tt.code {
				t.Fatalf("code = %s, want %s", got, tt.code)
			}
		})
	}
}

func TestMarketCatalog_StubListAndGet(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withFeatureFlag(t, testHandler, featureflags.MarketCatalogV1, true)

	req := newRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/market/catalog", nil)
	req = withURLParam(req, "id", testWorkspaceID)
	w := httptest.NewRecorder()
	testHandler.ListMarketCatalog(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", w.Code, w.Body.String())
	}
	var listed struct {
		Resources []struct {
			ResourceID string `json:"resourceId"`
			Kind       string `json:"kind"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Resources) != 2 {
		t.Fatalf("resources = %+v", listed.Resources)
	}
	resourceID := listed.Resources[0].ResourceID
	getReq := newRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/market/catalog/"+url.PathEscape(resourceID), nil)
	getReq = withURLParams(getReq, "id", testWorkspaceID, "*", url.PathEscape(resourceID))
	getW := httptest.NewRecorder()
	testHandler.GetMarketCatalogResource(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("get status = %d: %s", getW.Code, getW.Body.String())
	}
	var got struct {
		ResourceID string `json:"resourceId"`
	}
	if err := json.Unmarshal(getW.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got.ResourceID != resourceID {
		t.Fatalf("resourceId = %s, want %s", got.ResourceID, resourceID)
	}

	router := chi.NewRouter()
	router.Get("/api/workspaces/{id}/market/catalog/*", testHandler.GetMarketCatalogResource)
	routed := httptest.NewRecorder()
	router.ServeHTTP(routed, newRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/market/catalog/"+resourceID, nil))
	if routed.Code != http.StatusOK {
		t.Fatalf("wildcard status = %d: %s", routed.Code, routed.Body.String())
	}
	var viaRouter struct {
		ResourceID string `json:"resourceId"`
	}
	if err := json.Unmarshal(routed.Body.Bytes(), &viaRouter); err != nil {
		t.Fatalf("decode wildcard: %v", err)
	}
	if viaRouter.ResourceID != resourceID {
		t.Fatalf("wildcard resourceId = %s, want %s", viaRouter.ResourceID, resourceID)
	}
}

type marketDenial struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
	Code string `json:"code"`
}

type marketPreflightResponse struct {
	Status      string         `json:"status"`
	TaskStarted bool           `json:"taskStarted"`
	ActorUserID string         `json:"actorUserId"`
	Denials     []marketDenial `json:"denials"`
}

func postMarketPreflight(t *testing.T, userID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequestAsUser(userID, http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/market/preflight", body)
	req = withURLParam(req, "id", testWorkspaceID)
	w := httptest.NewRecorder()
	testHandler.PreflightMarket(w, req)
	return w
}

func marketPreflightBody(wsID string, expiry time.Time, demands []map[string]string, actor map[string]any) map[string]any {
	if demands == nil {
		demands = []map[string]string{}
	}
	body := map[string]any{
		"mappingId": "map-1",
		"revision":  1,
		"consumerRef": map[string]any{
			"consumerType": "multica",
			"consumerId":   "multica",
			"tenantId":     wsID,
			"subjectId":    "subject",
		},
		"targetScope":  map[string]any{"kind": "workspace", "id": wsID},
		"verification": "verified",
		"expiry":       expiry.UTC().Format(time.RFC3339),
		"demands":      demands,
	}
	if actor != nil {
		body["actor"] = actor
	}
	return body
}

func decodePreflight(t *testing.T, w *httptest.ResponseRecorder) marketPreflightResponse {
	t.Helper()
	var got marketPreflightResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v body %s", err, w.Body.String())
	}
	return got
}

func marketErrorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error: %v body %s", err, w.Body.String())
	}
	return body.Code
}

func withVCSAvailable(t *testing.T, on bool) {
	t.Helper()
	prev := testHandler.cfg.VCSIntegrationEnabled
	testHandler.cfg.VCSIntegrationEnabled = on
	t.Cleanup(func() { testHandler.cfg.VCSIntegrationEnabled = prev })
}

func withMarketModelCache(t *testing.T) *InMemoryModelCatalogCache {
	t.Helper()
	cache := NewInMemoryModelCatalogCache()
	prev := testHandler.ModelCatalogCache
	testHandler.ModelCatalogCache = cache
	t.Cleanup(func() { testHandler.ModelCatalogCache = prev })
	return cache
}

func agentTaskCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM agent_task_queue`).Scan(&n); err != nil {
		t.Fatalf("count agent_task_queue: %v", err)
	}
	return n
}

func insertMarketUser(t *testing.T, role string) string {
	t.Helper()
	email := fmt.Sprintf("market-%s-%d@multica.ai", role, time.Now().UnixNano())
	var userID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id
	`, "Market "+role, email).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, $3)
	`, testWorkspaceID, userID, role); err != nil {
		t.Fatalf("insert member: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		testPool.Exec(ctx, `DELETE FROM member WHERE user_id = $1`, userID)
		testPool.Exec(ctx, `DELETE FROM "user" WHERE id = $1`, userID)
	})
	return userID
}

func insertMarketSkill(t *testing.T, createdBy string) string {
	t.Helper()
	name := fmt.Sprintf("market-skill-%d", time.Now().UnixNano())
	var id string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO skill (workspace_id, name, description, content, created_by)
		VALUES ($1, $2, '', '# market', $3)
		RETURNING id
	`, testWorkspaceID, name, createdBy).Scan(&id); err != nil {
		t.Fatalf("insert skill: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM skill WHERE id = $1`, id)
	})
	return name
}

func insertMarketMCP(t *testing.T, label string) string {
	t.Helper()
	name := fmt.Sprintf("market-%s-%d", label, time.Now().UnixNano())
	var id string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO workspace_mcp_server (workspace_id, name, config)
		VALUES ($1, $2, '{}'::jsonb)
		RETURNING id
	`, testWorkspaceID, name).Scan(&id); err != nil {
		t.Fatalf("insert mcp: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace_mcp_server WHERE id = $1`, id)
	})
	return name
}

func insertMarketVCS(t *testing.T, label string) string {
	t.Helper()
	instance := fmt.Sprintf("https://git-%s-%d.example", label, time.Now().UnixNano())
	var id string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO vcs_connection (
			workspace_id, provider, instance_url, account_login,
			access_token_encrypted, webhook_secret_encrypted
		) VALUES ($1, 'forgejo', $2, 'bot', 'sealed', 'sealed')
		RETURNING id::text
	`, testWorkspaceID, instance).Scan(&id); err != nil {
		t.Fatalf("insert vcs: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM vcs_connection WHERE id = $1`, id)
	})
	return id
}

func insertMarketRuntime(t *testing.T, ownerID, visibility string) string {
	t.Helper()
	stamp := time.Now().UnixNano()
	var id string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, owner_id, visibility, last_seen_at
		) VALUES (
			$1, $2, $3, 'local', 'claude', 'offline', '', '{}'::jsonb, $4, $5, now()
		) RETURNING id::text
	`, testWorkspaceID, fmt.Sprintf("market-daemon-%d", stamp), fmt.Sprintf("market-runtime-%d", stamp), ownerID, visibility).Scan(&id); err != nil {
		t.Fatalf("insert runtime: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, id)
	})
	return id
}
