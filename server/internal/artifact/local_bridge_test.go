package artifact

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalBridgeRejectsDotDotAndSymlinkEscape(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "auth")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(parent, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("SECRET-BYTES"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "leak.txt")
	if err := os.Symlink(secretPath, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	reads := 0
	bridge := testBridge(root, func(string) ([]byte, error) {
		reads++
		return []byte("SECRET-BYTES"), nil
	})
	cases := []Signal{
		{Source: SignalWriteFile, Path: root + "/../secret.txt"},
		{Source: SignalFileLink, Path: link},
		{Source: SignalMediaHint, Path: "file://" + secretPath},
	}
	for _, signal := range cases {
		got := bridge.Discover("ws", "user", []Signal{signal})
		if len(got) != 1 || got[0].Submittable || got[0].Hint != HintEscape || got[0].Saved {
			t.Fatalf("%s -> %+v", signal.Path, got)
		}
		raw, err := json.Marshal(got[0])
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), secretPath) || strings.Contains(string(raw), "SECRET-BYTES") || strings.Contains(string(raw), parent) {
			t.Fatalf("response leaked a path or secret: %s", raw)
		}
	}
	if reads != 0 {
		t.Fatalf("unauthorized reads = %d", reads)
	}
}

func TestLocalBridgeUntrustedModelPathIsHintWithoutRead(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "auth")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	missing := root + "/no-such-file.md"
	outside := filepath.Join(parent, "model.txt")
	if err := os.WriteFile(outside, []byte("MODEL-SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	reads := 0
	bridge := testBridge(root, func(string) ([]byte, error) {
		reads++
		return []byte("MODEL-SECRET"), nil
	})
	got := bridge.Discover("ws", "user", []Signal{
		{Source: SignalFileLink, Path: "the model said " + missing},
		{Source: SignalFileLink, Path: outside},
		{Source: SignalFileLink, Path: "file://evil.example/etc/passwd"},
		{Source: SignalFileLink, Path: "notes.md"},
	})
	if len(got) != 4 {
		t.Fatalf("candidates = %+v", got)
	}
	for _, cand := range got {
		if cand.Submittable || cand.Kind != CandidateKind || cand.Saved || cand.Name != "" {
			t.Fatalf("candidate = %+v", cand)
		}
		if cand.Hint != HintMissing && cand.Hint != HintEscape {
			t.Fatalf("hint = %s", cand.Hint)
		}
	}
	if reads != 0 {
		t.Fatalf("model path reads = %d", reads)
	}
}

func TestLocalBridgeCandidateStaysUnsavedUntilChecksPass(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.md")
	body := []byte("# note\n")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	reads := 0
	bridge := testBridge(root, func(string) ([]byte, error) {
		reads++
		return body, nil
	})
	got := bridge.Discover("ws", "user", []Signal{
		{Source: SignalWriteFile, Path: path},
		{Source: SignalMediaHint, Path: path},
	})
	if reads != 2 {
		t.Fatalf("reads = %d", reads)
	}
	for _, cand := range got {
		if !cand.Submittable || cand.Saved || cand.Kind != CandidateKind || cand.Name != "note.md" {
			t.Fatalf("candidate = %+v", cand)
		}
		raw, err := json.Marshal(PublishCandidates("user", "user", []Candidate{cand})[0])
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), root) {
			t.Fatalf("absolute path in %s", raw)
		}
	}
	other := PublishCandidates("user", "someone-else", got)
	for _, cand := range other {
		if cand.Name != "" || cand.data != nil {
			t.Fatalf("other user saw %+v", cand)
		}
	}
	bridge.owner = func(string) (bool, error) { return false, nil }
	owned := bridge.Discover("ws", "user", []Signal{{Source: SignalWriteFile, Path: path}})
	if owned[0].Submittable || owned[0].Hint != HintOwnership || reads != 2 {
		t.Fatalf("ownership = %+v reads %d", owned, reads)
	}
}

func TestLocalBridgeHTTPRejectsCrossOriginBadHostAndMissingToken(t *testing.T) {
	bridge := LocalBridge{Enabled: true, AllowedHosts: []string{"127.0.0.1", "localhost"}}
	cases := []struct {
		name   string
		host   string
		origin string
		token  string
		want   int
	}{
		{name: "missing token", host: "127.0.0.1", origin: "http://127.0.0.1", want: http.StatusUnauthorized},
		{name: "bad host", host: "evil.example", origin: "http://127.0.0.1", token: "mdt_test", want: http.StatusForbidden},
		{name: "cross origin", host: "127.0.0.1", origin: "https://evil.example", token: "mdt_test", want: http.StatusForbidden},
		{name: "missing origin", host: "localhost", token: "mdt_test", want: http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/daemon/artifact-bridge/candidates", nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			rec := httptest.NewRecorder()
			if bridge.Reject(rec, req) {
				t.Fatal("request was accepted")
			}
			if rec.Code != tc.want {
				t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestLocalBridgeOffDoesNotRead(t *testing.T) {
	reads := 0
	bridge := LocalBridge{open: func(string) ([]byte, error) {
		reads++
		return []byte("nope"), nil
	}}
	got := bridge.Discover("ws", "user", []Signal{{Source: SignalWriteFile, Path: "/tmp/note.md"}})
	if got[0].Hint != HintBridgeOff || got[0].Submittable || got[0].Saved || reads != 0 {
		t.Fatalf("off = %+v reads %d", got, reads)
	}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/daemon/artifact-bridge/candidates", strings.NewReader(`{}`))
	req.Host = "127.0.0.1"
	req.Header.Set("Origin", "http://127.0.0.1")
	req.Header.Set("Authorization", "Bearer mdt_test")
	rec := httptest.NewRecorder()
	if bridge.Reject(rec, req) {
		t.Fatal("disabled bridge accepted a request")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestLocalBridgeFromEnvDefaultsOff(t *testing.T) {
	t.Setenv("MULTICA_ARTIFACT_BRIDGE", "")
	t.Setenv("MULTICA_ARTIFACT_BRIDGE_ROOTS", "ws:user=/tmp")
	if LocalBridgeFromEnv().Enabled {
		t.Fatal("bridge enabled")
	}
	t.Setenv("MULTICA_ARTIFACT_BRIDGE", "on")
	t.Setenv("MULTICA_ARTIFACT_BRIDGE_ROOTS", "ws:user=/tmp/auth;other:user=relative")
	t.Setenv("MULTICA_ARTIFACT_BRIDGE_HOSTS", "127.0.0.1")
	got := LocalBridgeFromEnv()
	if !got.Enabled || len(got.Bindings) != 1 || got.Bindings[0].Roots[0] != "/tmp/auth" {
		t.Fatalf("env bridge = %+v", got)
	}
	if len(got.AllowedHosts) != 1 || got.AllowedHosts[0] != "127.0.0.1" {
		t.Fatalf("hosts = %+v", got.AllowedHosts)
	}
}

func testBridge(root string, open func(string) ([]byte, error)) LocalBridge {
	return LocalBridge{
		Enabled: true,
		Bindings: []RootBinding{{
			WorkspaceID: "ws",
			UserID:      "user",
			Roots:       []string{root},
		}},
		open: open,
	}
}
