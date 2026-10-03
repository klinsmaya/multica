package handler

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	integ "github.com/multica-ai/multica/server/internal/integrations/composio"
	"github.com/multica-ai/multica/server/internal/testutil"
	sdk "github.com/multica-ai/multica/server/pkg/composio"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// This is a daemon CLAIM integration test, not a launch of a third-party agent
// CLI. Its explicit fixtures run the actual gateway and native OC processes.
func TestCompatibilityDaemonClaimLifecycleRealGateway(t *testing.T) {
	for _, kind := range []string{"terminal", "cancelled", "permission", "connection", "disconnect", "agent-delete", "member"} {
		t.Run(kind, func(t *testing.T) { exerciseCompatibilityClaim(t, kind) })
	}
}

type compatibilityClaimFixture struct {
	Endpoint, Bearer, NativeFile, Owner, Agent, Task string
	Gateway                                          *pgxpool.Pool
}

func exerciseCompatibilityClaim(t *testing.T, kind string, probes ...func(*testing.T, context.Context, compatibilityClaimFixture)) {

	binary, fixture := os.Getenv("GATEWAY_TEST_BINARY"), os.Getenv("GATEWAY_TEST_NATIVE_FIXTURE")
	if testHandler == nil || binary == "" || fixture == "" {
		t.Skip("NOT_RUN: isolated database, gateway binary and native fixture required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	gatewayPool, control, mcpOrigin, nativeFile := startClaimGateway(t, ctx, binary, fixture)
	withComposioMCPAppsFlag(t, testHandler, true)
	client, err := sdk.NewClient(sdk.Options{Backend: "compat", APIKey: "control-fixture", BaseURL: control + "/api/v3.1", AllowInsecureLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := integ.NewService(client, testHandler.Queries, integ.Config{Backend: "compat", StateSecret: []byte("state-fixture"), MCPOrigin: mcpOrigin, CallbackBaseURL: "https://multica.example", AllowInsecureLoopback: true, LifecycleDB: testPool, AuthorizeClaim: testHandler.AuthorizeComposioClaim, AuthorityToken: strings.Repeat("a", 32)})
	if err != nil {
		t.Fatal(err)
	}
	previous := testHandler.Composio
	testHandler.Composio = svc
	t.Cleanup(func() { testHandler.Composio = previous })
	agentID, runtimeID, daemonID := createRuntimeGuardAgent(t, ctx)
	dbfx.Exec(t, `UPDATE agent SET composio_toolkit_allowlist=ARRAY['example'],mcp_config='{"mcpServers":{"composio":{"type":"http","url":"https://stale.invalid","headers":{"x-api-key":"stale-project-secret"}}}}' WHERE id=$1`, agentID)
	connectionID := dbfx.Insert(t, "user_composio_connection", testutil.Cols{"user_id": testUserID, "composio_user_id": testUserID, "toolkit_slug": "example", "auth_config_id": "ac", "connected_account_id": "ca_fixture"})
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM composio_compat_connection_outbox WHERE connection_id=$1`, connectionID)
	})
	chat := dbfx.ChatSession(t, agentID, testutil.Cols{"title": "Compatibility lifecycle fixture"})
	taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "chat_session_id": chat, "status": "queued", "originator_user_id": testUserID, "accountable_user_id": testUserID, "originator_source": "direct_human"})
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM composio_compat_outbox WHERE task_id=$1;`, taskID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM composio_compat_task WHERE task_id=$1`, taskID)
	})
	agent, err := testHandler.Queries.GetAgent(ctx, parseUUID(agentID))
	if err != nil {
		t.Fatal(err)
	}
	queued, err := svc.BuildTaskOverlay(ctx, parseUUID(testUserID), agent)
	if err != nil || len(queued.MCPOverlay) != 0 {
		t.Fatal("queue issued credential")
	}
	var before int
	if err = gatewayPool.QueryRow(ctx, `SELECT count(*) FROM session`).Scan(&before); err != nil || before != 0 {
		t.Fatal("queued session exists")
	}
	req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+runtimeID+"/claim", nil, testWorkspaceID, daemonID)
	route := chi.NewRouteContext()
	route.URLParams.Add("runtimeId", runtimeID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, route))
	response := httptest.NewRecorder()
	testHandler.ClaimTaskByRuntime(response, req)
	if response.Code != 200 {
		t.Fatalf("claim %d: %s", response.Code, response.Body.String())
	}
	var wire struct {
		Task map[string]json.RawMessage `json:"task"`
	}
	if json.Unmarshal(response.Body.Bytes(), &wire) != nil || wire.Task == nil {
		t.Fatal("missing claim")
	}
	var agentWire map[string]json.RawMessage
	if json.Unmarshal(wire.Task["agent"], &agentWire) != nil {
		t.Fatal("missing claim agent")
	}
	var config struct {
		Servers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if json.Unmarshal(agentWire["mcp_config"], &config) != nil {
		t.Fatal("missing MCP config")
	}
	entry := config.Servers["composio"]
	bearer := strings.TrimPrefix(entry.Headers["Authorization"], "Bearer ")
	if len(bearer) < 32 || !strings.HasPrefix(entry.URL, mcpOrigin+"/sessions/") {
		t.Fatal("unsafe MCP descriptor")
	}
	// Remove the dedicated credential carrier, then scan every other claim field,
	// including all task prompts and agent instructions.
	delete(agentWire, "mcp_config")
	wire.Task["agent"], _ = json.Marshal(agentWire)
	prompt, _ := json.Marshal(wire)
	for _, secret := range []string{bearer, "control-fixture", "native-admin-fixture", "fixture-provider-secret", "stale-project-secret", strings.Repeat("a", 32)} {
		if bytes.Contains(prompt, []byte(secret)) {
			t.Fatal("secret leaked into task/prompt payload")
		}
	}
	if bytes.Contains(response.Body.Bytes(), []byte("stale-project-secret")) {
		t.Fatal("saved project-key fallback leaked")
	}
	ping := func() int {
		request, _ := http.NewRequestWithContext(ctx, "POST", entry.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"daemon-claim-test","version":"1"}}}`))
		request.Header.Set("Authorization", "Bearer "+bearer)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		res, e := http.DefaultClient.Do(request)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		_, _ = io.Copy(io.Discard, res.Body)
		return res.StatusCode
	}
	otherWorkspace := dbfx.Workspace(t, "Unrelated workspace", fmt.Sprintf("compat-other-%d", time.Now().UnixNano()))
	otherMember := dbfx.Insert(t, "member", testutil.Cols{"workspace_id": otherWorkspace, "user_id": testUserID, "role": "member"})
	dbfx.Exec(t, `DELETE FROM member WHERE id=$1`, otherMember)
	if count := dbfx.Count(t, `SELECT count(*) FROM composio_compat_outbox WHERE task_id=$1`, taskID); count != 0 {
		t.Fatal("unrelated workspace membership revoked task")
	}
	// Idempotent source writes must not create a permanent task tombstone.
	dbfx.Exec(t, `UPDATE agent SET permission_mode=permission_mode WHERE id=$1`, agentID)
	dbfx.Exec(t, `UPDATE user_composio_connection SET status=status,updated_at=now() WHERE user_id=$1 AND connected_account_id='ca_fixture'`, testUserID)
	if count := dbfx.Count(t, `SELECT count(*) FROM composio_compat_outbox WHERE task_id=$1`, taskID); count != 0 {
		t.Fatal("no-op update revoked task")
	}
	if code := ping(); code != 200 {
		t.Fatalf("live admission %d", code)
	}
	for _, probe := range probes {
		probe(t, ctx, compatibilityClaimFixture{entry.URL, bearer, nativeFile, testUserID, agentID, taskID, gatewayPool})
	}
	switch kind {
	case "terminal":
		dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, taskID)
	case "cancelled":
		dbfx.Exec(t, `UPDATE agent_task_queue SET status='cancelled' WHERE id=$1`, taskID)
	case "agent-delete":
		dbfx.Exec(t, `DELETE FROM agent WHERE id=$1`, agentID)
	case "member":
		dbfx.Exec(t, `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, testWorkspaceID, testUserID)
		defer dbfx.Exec(t, `INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'owner') ON CONFLICT DO NOTHING`, testWorkspaceID, testUserID)
	case "permission":
		dbfx.Exec(t, `UPDATE agent SET composio_toolkit_allowlist='{}' WHERE id=$1`, agentID)
	case "disconnect":
		if err = svc.Disconnect(ctx, parseUUID(testUserID), parseUUID(connectionID)); err == nil {
			t.Fatal("remote revocation falsely reported complete")
		}
		if code := ping(); code != 401 {
			t.Fatal("local disable waited for remote revoke")
		}
		if err = svc.DrainConnectionRevocation(ctx); err != nil {
			t.Fatal(err)
		}
		var remote string
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			if err = gatewayPool.QueryRow(ctx, `SELECT remote_revocation FROM connection WHERE id='ca_fixture'`).Scan(&remote); err != nil {
				t.Fatal(err)
			}
			if remote == "UNSUPPORTED" {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		if remote != "UNSUPPORTED" {
			t.Fatalf("unverified provider outcome %s", remote)
		}
		dbfx.Exec(t, `UPDATE composio_compat_connection_outbox SET available_at=now() WHERE connection_id=$1`, connectionID)
		if err = svc.DrainConnectionRevocation(ctx); err != nil {
			t.Fatal(err)
		}
		var localRemote string
		if err = testPool.QueryRow(ctx, `SELECT state FROM composio_compat_connection_outbox WHERE connection_id=$1`, connectionID).Scan(&localRemote); err != nil || localRemote != "UNSUPPORTED" {
			t.Fatalf("lost remote state %s %v", localRemote, err)
		}
		if _, err = testPool.Exec(ctx, `UPDATE user_composio_connection SET status='active' WHERE id=$1`, connectionID); err == nil {
			t.Fatal("callback could revive disconnected account")
		}
	case "connection":
		dbfx.Exec(t, `UPDATE user_composio_connection SET status='revoked' WHERE connected_account_id='ca_fixture' AND user_id=$1`, testUserID)
	}
	// Live source authority denies immediately, before outbox delivery.
	if code := ping(); code != 401 {
		t.Fatalf("terminal task still admitted %d", code)
	}
	if err = svc.DrainRevocation(ctx); err != nil {
		t.Fatal(err)
	}
	var state string
	var unexpired bool
	var ciphertext []byte
	if err = gatewayPool.QueryRow(ctx, `SELECT state,expires_at>now(),runtime_ciphertext FROM session WHERE task_id=$1`, taskID).Scan(&state, &unexpired, &ciphertext); err != nil {
		t.Fatal(err)
	}
	if state != "REVOKED" || !unexpired || len(ciphertext) == 0 {
		t.Fatalf("terminal revoke lost unexpired session recovery: %s %v %d", state, unexpired, len(ciphertext))
	}
	// The completed source tombstone remains after cleanup and cannot remint.
	task, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
	if kind == "agent-delete" && err != nil {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if again, err := svc.BuildClaimOverlay(ctx, task, agent); err == nil && len(again.MCPOverlay) > 0 {
		t.Fatal("revoked claim revived")
	}
}

func startClaimGateway(t *testing.T, ctx context.Context, binary, fixture string) (*pgxpool.Pool, string, string, string) {
	t.Helper()
	native := exec.CommandContext(ctx, "node", fixture)
	native.Dir = filepath.Dir(fixture)
	nativeFile := filepath.Join(t.TempDir(), "native.sqlite")
	native.Env = append(os.Environ(), "OC_NATIVE_FIXTURE_DB="+nativeFile)
	stdout, err := native.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	native.Stderr = io.Discard
	if err = native.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = native.Process.Kill(); _ = native.Wait() })
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		t.Fatal("native fixture startup")
	}
	origin := scanner.Text()
	httpClient := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	call := func(method, target string, body string) map[string]any {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, method, target, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer native-admin-fixture")
		req.Header.Set("X-Connector-Subject", "claim-fixture")
		req.Header.Set("Content-Type", "application/json")
		res, e := httpClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		var out map[string]any
		if res.StatusCode >= 300 && res.StatusCode < 400 {
			return nil
		}
		if res.StatusCode != 200 && res.StatusCode != 201 {
			t.Fatalf("native fixture status %d", res.StatusCode)
		}
		if json.NewDecoder(res.Body).Decode(&out) != nil {
			t.Fatal("native response")
		}
		return out
	}
	started := call("POST", origin+"/v1/connections/example/connect", `{"returnUri":"http://127.0.0.1:1/complete"}`)["data"].(map[string]any)
	authURL, _ := url.Parse(started["authorizationUrl"].(string))
	call("GET", origin+"/oauth/callback?code=offline-fixture&state="+url.QueryEscape(authURL.Query().Get("state")), "")
	connected := call("GET", origin+"/v1/connection-requests/"+started["connectionRequestId"].(string), "")["data"].(map[string]any)
	nativeID := connected["appId"].(string)
	schema := fmt.Sprintf("gateway_claim_%d", time.Now().UnixNano())
	if _, err = testPool.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`) })
	dsn, err := url.Parse(os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	q := dsn.Query()
	q.Set("search_path", schema)
	dsn.RawQuery = q.Encode()
	folder := t.TempDir()
	write := func(name string, data []byte) string {
		path := filepath.Join(folder, name)
		if os.WriteFile(path, data, 0600) != nil {
			t.Fatal("fixture secret file")
		}
		return path
	}
	dbFile := write("database", []byte(dsn.String()))
	migrate := exec.CommandContext(ctx, binary, "-mode", "migrate", "-database-url-file", dbFile)
	if out, e := migrate.CombinedOutput(); e != nil {
		t.Fatalf("gateway migration: %v %s", e, out)
	}
	pool, err := pgxpool.New(ctx, dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	run := func(sql string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	digest := sha256.Sum256([]byte("control-fixture"))
	run(`INSERT INTO project(id,control_digest,enabled) VALUES('p',$1,true)`, digest[:])
	run(`INSERT INTO subject(project_id,id) VALUES('p',$1)`, testUserID)
	run(`INSERT INTO auth_config(project_id,id,toolkit,display_name,runtime_id,auth_type,enabled,approved_actions,capabilities) VALUES('p','ac','example','Example','runtime','OAUTH2',true,ARRAY['example.read'],'{"auth_configured":true,"runtime_verified":true}')`)
	run(`INSERT INTO connection(project_id,id,subject_id,auth_config_id,toolkit,runtime_id,native_id,state) VALUES('p','ca_fixture',$1,'ac','example','runtime',$2,'ACTIVE')`, testUserID, nativeID)
	run(`UPDATE recovery_state SET quarantined=false`)
	free := func() string {
		listener, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		address := listener.Addr().String()
		_ = listener.Close()
		return address
	}
	control, data := free(), free()
	source := httptest.NewServer(http.HandlerFunc(testHandler.ComposioAuthorizeExecution))
	t.Cleanup(source.Close)
	command := exec.CommandContext(ctx, binary, "-database-url-file", dbFile, "-control-listen", control, "-mcp-listen", data, "-public-origin", "http://"+control, "-mcp-origin", "http://"+data, "-native-url", origin, "-native-admin-file", write("native", []byte("native-admin-fixture")), "-vault-key-file", write("vault", bytes.Repeat([]byte{7}, 32)), "-source-origin", source.URL, "-source-token-file", write("source", []byte(strings.Repeat("a", 32))))
	command.Stderr = io.Discard
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		res, e := httpClient.Get("http://" + control + "/health")
		if e == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				return pool, "http://" + control, "http://" + data, nativeFile
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("gateway listener did not start")
	return nil, "", "", ""
}

func TestCompatibilityRegistrationSerializesPermissionWithdrawal(t *testing.T) {
	if testHandler == nil {
		t.Skip("NOT_RUN: isolated PostgreSQL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	agentID, runtimeID, _ := createRuntimeGuardAgent(t, ctx)
	dbfx.Exec(t, `UPDATE agent SET composio_toolkit_allowlist=ARRAY['example'] WHERE id=$1`, agentID)
	taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "status": "running", "originator_user_id": testUserID, "accountable_user_id": testUserID, "originator_source": "direct_human"})
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM composio_compat_outbox WHERE task_id=$1`, taskID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM composio_compat_task WHERE task_id=$1`, taskID)
	})
	agent, err := testHandler.Queries.GetAgent(ctx, parseUUID(agentID))
	if err != nil {
		t.Fatal(err)
	}
	task, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
	if err != nil {
		t.Fatal(err)
	}
	unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer unavailable.Close()
	client, _ := sdk.NewClient(sdk.Options{Backend: "compat", APIKey: "fixture", BaseURL: unavailable.URL + "/api/v3.1", AllowInsecureLoopback: true})
	locked, release := make(chan struct{}), make(chan struct{})
	svc, err := integ.NewService(client, testHandler.Queries, integ.Config{Backend: "compat", StateSecret: []byte("fixture"), MCPOrigin: unavailable.URL, CallbackBaseURL: "https://multica.example", LifecycleDB: testPool, AuthorityToken: strings.Repeat("a", 32), AuthorizeClaim: func(context.Context, db.Agent, string) bool {
		close(locked)
		select {
		case <-release:
			return true
		case <-ctx.Done():
			return false
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	registration := make(chan struct{})
	go func() { _, _ = svc.BuildClaimOverlay(ctx, task, agent); close(registration) }()
	select {
	case <-locked:
	case <-ctx.Done():
		t.Fatal("registration did not reach lock boundary")
	}
	writer, err := testPool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Release()
	pid := writer.Conn().PgConn().PID()
	withdrawn := make(chan error, 1)
	go func() {
		_, err := writer.Exec(ctx, `UPDATE agent SET composio_toolkit_allowlist='{}' WHERE id=$1`, agentID)
		withdrawn <- err
	}()
	// Verify the withdrawing transaction actually waits on the source-row lock,
	// rather than relying on sleep ordering between goroutines.
	waiting := false
	for !waiting && ctx.Err() == nil {
		var event *string
		if err = testPool.QueryRow(ctx, `SELECT wait_event_type FROM pg_stat_activity WHERE pid=$1`, pid).Scan(&event); err != nil {
			break
		}
		waiting = event != nil && *event == "Lock"
		if !waiting {
			time.Sleep(time.Millisecond)
		}
	}
	close(release)
	if !waiting {
		t.Fatal("withdrawal did not wait for registry commit")
	}
	select {
	case <-registration:
	case <-ctx.Done():
		t.Fatal("registration did not finish")
	}
	if err = <-withdrawn; err != nil {
		t.Fatal(err)
	}
	if count := dbfx.Count(t, `SELECT count(*) FROM composio_compat_outbox WHERE task_id=$1`, taskID); count != 1 {
		t.Fatal("withdrawal missed committed registry")
	}
}
