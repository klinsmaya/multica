//go:build agentintegration

package handler

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestCompatibilityOfflineCodexActualCLI(t *testing.T) {
	if os.Getenv("MULTICA_RUN_REAL_AGENT_SMOKE") != "1" {
		t.Skip("explicit offline CLI authorization required")
	}
	binary, probe := os.Getenv("MULTICA_CODEX_BINARY"), os.Getenv("MULTICA_CODEX_PROBE_BINARY")
	image := os.Getenv("MULTICA_CLI_IMAGE")
	if !strings.HasPrefix(image, "sha256:") {
		t.Fatal("explicit immutable local image ID required")
	}
	if binary == "" || probe == "" {
		t.Fatal("explicit verified CLI and static probe binary required")
	}
	exerciseCompatibilityClaim(t, "terminal", func(t *testing.T, ctx context.Context, fixture compatibilityClaimFixture) {
		endpoint, bearer := fixture.Endpoint, fixture.Bearer
		folder := t.TempDir()
		target, err := url.Parse(endpoint)
		if err != nil {
			t.Fatal(err)
		}
		listener, err := net.Listen("unix", filepath.Join(folder, "mcp.sock"))
		if err != nil {
			t.Fatal(err)
		}
		proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: target.Scheme, Host: target.Host})
		server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != target.Path {
				w.WriteHeader(403)
				return
			}
			proxy.ServeHTTP(w, r)
		})}
		go func() { _ = server.Serve(listener) }()
		defer server.Close()
		overlay, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"composio": map[string]any{"type": "http", "url": endpoint, "headers": map[string]string{"Authorization": "Bearer " + bearer}}}})
		if err = os.WriteFile(filepath.Join(folder, "mcp.json"), overlay, 0600); err != nil {
			t.Fatal(err)
		}
		passwd := fmt.Sprintf("codex:x:%d:%d:Offline fixture:/tmp/codex-home:/bin/false\n", os.Getuid(), os.Getgid())
		if err = os.WriteFile(filepath.Join(folder, "passwd"), []byte(passwd), 0644); err != nil {
			t.Fatal(err)
		}
		command := exec.CommandContext(ctx, "docker", "run", "--rm", "--pull=never", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "--tmpfs", fmt.Sprintf("/tmp:rw,uid=%d,gid=%d,mode=1777", os.Getuid(), os.Getgid()), "--mount", "type=bind,src="+binary+",dst=/codex,readonly", "--mount", "type=bind,src="+probe+",dst=/probe,readonly", "--mount", "type=bind,src="+folder+",dst=/fixture,readonly", "--mount", "type=bind,src="+filepath.Join(folder, "passwd")+",dst=/etc/passwd,readonly", "-e", "MULTICA_RUN_REAL_AGENT_SMOKE=1", "-e", "MULTICA_CODEX_CONTAINER=1", "--entrypoint", "/probe", image, "-test.run=^TestOfflineCodexContainerProbe$", "-test.v")
		output, err := command.CombinedOutput()
		safe := strings.ReplaceAll(string(output), bearer, "[session-redacted]")
		if err != nil {
			t.Fatalf("isolated actual CLI: %v\n%s", err, safe)
		}
		t.Log(safe)
	})
}
