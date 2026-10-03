//go:build agentintegration

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Runs only against the explicitly selected disposable recovery database. The
// container has no network; its sole external socket reaches that a separate disposable PG instance.
func TestCompatibilityIsolatedMultiServiceCanary(t *testing.T) {
	if os.Getenv("MULTICA_RUN_LOCAL_CANARY") != "1" {
		t.Skip("explicit isolated canary required")
	}
	image := os.Getenv("MULTICA_CLI_IMAGE")
	if !strings.HasPrefix(image, "sha256:") {
		t.Fatal("immutable local image ID required")
	}
	dsn, e := url.Parse(os.Getenv("DATABASE_URL"))
	if e != nil || dsn.Host != "127.0.0.1:55432" || dsn.Path != "/multica_recovery_test" || os.Getenv("GATEWAY_TEST_PG_CONTAINER") != "oc-recovery-pg" {
		t.Fatal("only disposable recovery database supported")
	}
	oc := os.Getenv("GATEWAY_TEST_OC_ROOT")
	node := os.Getenv("GATEWAY_TEST_NODE_BINARY")
	mc := os.Getenv("GATEWAY_TEST_MULTICA_BINARY")
	if oc != "/workspace/open-connector" || node == "" || mc == "" {
		t.Fatal("explicit source and binaries required")
	}
	exerciseCompatibilityClaim(t, "terminal", func(t *testing.T, ctx context.Context, f compatibilityClaimFixture) {
		folder := t.TempDir()
		// The fixture has no writer between claim and snapshot. Both databases are
		// copied before any canary process starts; original fixtures remain untouched.
		backup := exec.CommandContext(ctx, "python3", "-c", `import sqlite3,sys; s=sqlite3.connect(sys.argv[1]); d=sqlite3.connect(sys.argv[2]); s.backup(d); d.close(); s.close()`, f.NativeFile, filepath.Join(folder, "native.sqlite"))
		if out, e := backup.CombinedOutput(); e != nil {
			t.Fatalf("SQLite backup: %v %s", e, out)
		}
		dump, e := exec.CommandContext(ctx, "docker", "exec", "oc-recovery-pg", "pg_dump", "-U", "oc_recovery", "-d", "multica_recovery_test", "-Fc", "--no-owner", "--no-privileges").Output()
		if e != nil {
			t.Fatal(e)
		}
		name := "multica_canary_" + strings.ReplaceAll(f.Task, "-", "")
		pgImage, e := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.Image}}", "oc-recovery-pg").Output()
		if e != nil || !strings.HasPrefix(string(pgImage), "sha256:") {
			t.Fatal("local immutable PG image required")
		}
		pgFolder := filepath.Join(folder, "pgsocket")
		if e = os.Mkdir(pgFolder, 0777); e != nil {
			t.Fatal(e)
		}
		if e = os.Chmod(pgFolder, 0777); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			_ = exec.Command("docker", "stop", "-t", "3", name).Run()
			_ = exec.Command("docker", "rm", "-f", name).Run()
		})
		boot := exec.CommandContext(ctx, "docker", "run", "-d", "--pull=never", "--network=none", "--name", name, "--tmpfs", "/var/lib/postgresql/data", "--mount", "type=bind,src="+pgFolder+",dst=/var/run/postgresql", "-e", "POSTGRES_USER=oc_recovery", "-e", "POSTGRES_PASSWORD=isolated-canary-only", "-e", "POSTGRES_DB="+name, strings.TrimSpace(string(pgImage)))
		if out, e := boot.CombinedOutput(); e != nil {
			t.Fatalf("isolated PG startup: %v %s", e, out)
		}
		ready := false
		for i := 0; i < 100; i++ {
			if exec.CommandContext(ctx, "docker", "exec", name, "pg_isready", "-h", "127.0.0.1", "-U", "oc_recovery", "-d", name).Run() == nil {
				ready = true
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !ready {
			t.Fatal("isolated PG unavailable")
		}
		restore := exec.CommandContext(ctx, "docker", "exec", "-i", name, "pg_restore", "-h", "127.0.0.1", "-U", "oc_recovery", "-d", name, "--no-owner", "--no-privileges", "--exit-on-error")
		restore.Stdin = strings.NewReader(string(dump))
		if out, e := restore.CombinedOutput(); e != nil {
			t.Fatalf("PG restore: %v %s", e, out)
		}
		socket, e := net.Listen("unix", filepath.Join(folder, "postgres.sock"))
		if e != nil {
			t.Fatal(e)
		}
		defer socket.Close()
		go func() {
			for {
				c, e := socket.Accept()
				if e != nil {
					return
				}
				go func() {
					defer c.Close()
					p, e := net.Dial("unix", filepath.Join(pgFolder, ".s.PGSQL.5432"))
					if e != nil {
						return
					}
					defer p.Close()
					go func() {
						_, _ = io.Copy(p, c)
						if tcp, ok := p.(*net.UnixConn); ok {
							_ = tcp.CloseWrite()
						}
					}()
					_, _ = io.Copy(c, p)
				}()
			}
		}()
		var schema string
		if e = f.Gateway.QueryRow(ctx, "select current_schema()").Scan(&schema); e != nil {
			t.Fatal(e)
		}
		dsn.User = url.UserPassword("oc_recovery", "isolated-canary-only")
		dsn.Path = "/" + name
		dsn.Host = "127.0.0.1:55432"
		config := map[string]string{"database": dsn.String(), "schema": schema, "endpoint": f.Endpoint, "bearer": f.Bearer, "owner": f.Owner, "agent": f.Agent, "task": f.Task}
		raw, _ := json.Marshal(config)
		if e = os.WriteFile(filepath.Join(folder, "canary.json"), raw, 0600); e != nil {
			t.Fatal(e)
		}
		args := []string{"run", "--rm", "--pull=never", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "--tmpfs", fmt.Sprintf("/tmp:rw,uid=%d,gid=%d,mode=1777", os.Getuid(), os.Getgid())}
		mount := func(src, dst string) { args = append(args, "--mount", "type=bind,src="+src+",dst="+dst+",readonly") }
		mount(folder, "/fixture")
		mount(node, "/node")
		mount(mc, "/multica")
		mount(os.Getenv("GATEWAY_TEST_BINARY"), "/gateway")
		for _, part := range []string{"src", "node_modules", "migrations", "package.json", "gateway/tests/integration"} {
			mount(filepath.Join(oc, part), "/oc/"+part)
		}
		for _, lib := range []string{"libdl.so.2", "libstdc++.so.6", "libm.so.6", "libgcc_s.so.1", "libpthread.so.0", "libc.so.6"} {
			mount("/lib/x86_64-linux-gnu/"+lib, "/lib/x86_64-linux-gnu/"+lib)
		}
		mount("/lib64/ld-linux-x86-64.so.2", "/lib64/ld-linux-x86-64.so.2")
		args = append(args, "--entrypoint", "/node", image, "/oc/gateway/tests/integration/multiservice_canary.mjs")
		output, e := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		safe := strings.ReplaceAll(string(output), f.Bearer, "[session-redacted]")
		if e != nil {
			t.Fatalf("isolated multiservice canary: %v\n%s", e, safe)
		}
		t.Log(safe)
	})
}
