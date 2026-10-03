//go:build agentintegration

package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Run only inside the credential-free, network-none container created by the
// handler integration. Never discovers a host CLI or launches a model turn.
func TestOfflineCodexContainerProbe(t *testing.T) {
	if os.Getenv("MULTICA_RUN_REAL_AGENT_SMOKE") != "1" || os.Getenv("MULTICA_CODEX_CONTAINER") != "1" {
		t.Skip("explicit offline container probe required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	raw, err := os.ReadFile("/fixture/mcp.json")
	if err != nil {
		t.Fatal(err)
	}
	var overlay struct {
		Servers map[string]map[string]any `json:"mcpServers"`
	}
	if json.Unmarshal(raw, &overlay) != nil {
		t.Fatal("invalid fixture overlay")
	}
	original, err := url.Parse(overlay.Servers["composio"]["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", "/fixture/mcp.sock")
	}}
	proxy := &httputil.ReverseProxy{Director: func(r *http.Request) { r.URL.Scheme = "http"; r.URL.Host = "fixture" }, Transport: transport}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != original.Path {
			w.WriteHeader(403)
			return
		}
		proxy.ServeHTTP(w, r)
	}), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	overlay.Servers["composio"]["url"] = "http://" + listener.Addr().String() + original.Path
	configured, _ := json.Marshal(overlay)
	block, _, err := renderCodexMcpServersBlock(configured)
	if err != nil {
		t.Fatal(err)
	}
	// passwd supplies this private home; no HOME/CODEX_HOME override or auth file.
	home := "/tmp/codex-home/.codex"
	if err = os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	config := "model=\"offline-fixture\"\nmodel_provider=\"offline\"\n" + block + "\n[model_providers.offline]\nname=\"Offline fixture\"\nbase_url=\"http://127.0.0.1:1/never-model\"\nwire_api=\"responses\"\nrequires_openai_auth=false\nrequest_max_retries=0\nstream_max_retries=0\n"
	if err = os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, "/codex", "app-server", "--listen", "stdio://")
	command.Env = []string{"PATH=/nonexistent"}
	command.Dir = "/tmp"
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var diagnostics bytes.Buffer
	command.Stderr = &diagnostics
	scanControlSecrets := func(data string) {
		t.Helper()
		for _, secret := range []string{"control-fixture", "native-admin-fixture", "fixture-provider-secret", "stale-project-secret", strings.Repeat("a", 32)} {
			if strings.Contains(data, secret) {
				t.Error("control/provider secret entered actual CLI config, environment or protocol/log output")
			}
		}
	}
	scanControlSecrets(config)
	scanControlSecrets(strings.Join(command.Env, "\n"))
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait(); scanControlSecrets(diagnostics.String()) }()
	lines := make(chan map[string]json.RawMessage, 100)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 8<<20)
		for scanner.Scan() {
			scanControlSecrets(scanner.Text())
			var m map[string]json.RawMessage
			if json.Unmarshal(scanner.Bytes(), &m) == nil {
				select {
				case lines <- m:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	next := 0
	rpc := func(method string, params any) json.RawMessage {
		t.Helper()
		next++
		id := next
		message, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
		if _, e := stdin.Write(append(message, '\n')); e != nil {
			t.Fatal(e)
		}
		for {
			select {
			case <-ctx.Done():
				t.Fatalf("offline CLI timeout at %s", method)
			case response, ok := <-lines:
				if !ok {
					t.Fatalf("CLI closed at %s", method)
				}
				var responseID int
				_ = json.Unmarshal(response["id"], &responseID)
				if responseID != id {
					continue
				}
				if len(response["error"]) > 0 {
					t.Fatalf("CLI rejected %s: %s", method, response["error"])
				}
				return response["result"]
			}
		}
	}
	rpc("initialize", map[string]any{"clientInfo": map[string]string{"name": "multica-offline-probe", "version": "1"}, "capabilities": map[string]bool{"experimentalApi": true}})
	_, _ = stdin.Write([]byte("{\"method\":\"initialized\"}\n"))
	started := rpc("thread/start", map[string]any{"cwd": "/tmp", "ephemeral": true, "approvalPolicy": "never", "sandbox": "read-only"})
	var thread struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(started, &thread) != nil || thread.Thread.ID == "" {
		t.Fatal("missing offline thread")
	}
	inventory := rpc("mcpServerStatus/list", map[string]any{"threadId": thread.Thread.ID, "detail": "toolsAndAuthOnly"})
	if !strings.Contains(string(inventory), "execute_action") {
		t.Fatalf("CLI did not discover gateway tools: %s", inventory)
	}
	called := rpc("mcpServer/tool/call", map[string]any{"threadId": thread.Thread.ID, "server": "composio", "tool": "list_connections", "arguments": map[string]any{}})
	if !strings.Contains(string(called), "ca_fixture") {
		t.Fatalf("CLI lost exact account: %s", called)
	}
	action := rpc("mcpServer/tool/call", map[string]any{"threadId": thread.Thread.ID, "server": "composio", "tool": "execute_action", "arguments": map[string]any{"actionId": "example.read", "connectionName": "ca_fixture", "operationId": "real_cli_offline_1", "input": map[string]string{"value": "offline-cli"}}})
	if !strings.Contains(string(action), "offline-cli") {
		t.Fatalf("CLI action not executed: %s", action)
	}
	t.Log("actual CLI initialize, MCP discovery and tool execution passed; no model turn or provider network")
}
