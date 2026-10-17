package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestArtifactCommandsPrintStableJSON(t *testing.T) {
	var gotPath string
	var gotTask string
	var gotTitle string
	var gotBase string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/artifacts":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			gotTask = r.FormValue("task_id")
			gotTitle = r.FormValue("title")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"artifact_id":  "art-1",
				"revision_id":  "rev-1",
				"operation_id": "op-1",
				"state":        "committed",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/artifacts/art-1/revisions":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			gotBase = r.FormValue("base_revision_id")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"artifact_id":  "art-1",
				"revision_id":  "rev-2",
				"operation_id": "op-2",
				"state":        "committed",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/artifacts/art-1":
			if r.URL.Query().Get("revision_id") != "rev-1" {
				t.Fatalf("revision query = %q", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":           "art-1",
				"revision_id":  "rev-1",
				"operation_id": "op-1",
				"status":       "active",
				"title":        "Weekly report",
				"filename":     "report.md",
				"revision_no":  1,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/artifact-operations/op-1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"artifact_id":  "art-1",
				"revision_id":  "rev-1",
				"operation_id": "op-1",
				"state":        "committed",
			})
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.RequestURI())
		}
	}))
	defer srv.Close()
	setCLITestServerEnv(t, srv.URL)
	t.Setenv("MULTICA_TOKEN", "mat_test-token")
	t.Setenv("MULTICA_TASK_ID", "task-1")

	file := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(file, []byte("# report\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	createOut := runArtifactCommand(t, artifactCreateCmd, nil, map[string]string{
		"task":  "task-1",
		"file":  file,
		"title": "Weekly report",
	})
	assertArtifactJSON(t, createOut, "art-1", "rev-1", "op-1", "committed")
	if gotTask != "task-1" || gotTitle != "Weekly report" || gotPath != "POST /api/artifacts" {
		t.Fatalf("create request task=%q title=%q path=%q", gotTask, gotTitle, gotPath)
	}

	updateOut := runArtifactCommand(t, artifactUpdateCmd, []string{"art-1"}, map[string]string{
		"base": "rev-1",
		"file": file,
	})
	assertArtifactJSON(t, updateOut, "art-1", "rev-2", "op-2", "committed")
	if gotBase != "rev-1" {
		t.Fatalf("base = %q", gotBase)
	}

	getOut := runArtifactCommand(t, artifactGetCmd, []string{"art-1"}, map[string]string{
		"revision": "rev-1",
	})
	assertArtifactJSON(t, getOut, "art-1", "rev-1", "op-1", "committed")

	statusOut := runArtifactCommand(t, artifactStatusCmd, nil, map[string]string{
		"operation": "op-1",
	})
	assertArtifactJSON(t, statusOut, "art-1", "rev-1", "op-1", "committed")
}

func TestArtifactCreateRejectsWrongTaskLocally(t *testing.T) {
	setCLITestServerEnv(t, "http://127.0.0.1:1")
	t.Setenv("MULTICA_TOKEN", "mat_test-token")
	t.Setenv("MULTICA_TASK_ID", "task-a")
	cmd := artifactCreateCmd
	_ = cmd.Flags().Set("task", "task-b")
	_ = cmd.Flags().Set("file", "report.md")
	_ = cmd.Flags().Set("title", "Report")
	err := runArtifactCreate(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "task token") {
		t.Fatalf("err = %v", err)
	}
}

func TestArtifactStatusSurfacesServerNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"artifacts are disabled"}`))
	}))
	defer srv.Close()
	setCLITestServerEnv(t, srv.URL)
	t.Setenv("MULTICA_TOKEN", "mat_test-token")
	cmd := artifactStatusCmd
	_ = cmd.Flags().Set("operation", "op-missing")
	err := runArtifactStatus(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v", err)
	}
}

func TestArtifactCommandIsRegisteredBesideAttachment(t *testing.T) {
	var hasArtifact, hasAttachment bool
	for _, cmd := range rootCmd.Commands() {
		switch cmd.Name() {
		case "artifact":
			hasArtifact = true
		case "attachment":
			hasAttachment = true
		}
	}
	if !hasArtifact || !hasAttachment {
		t.Fatalf("artifact=%v attachment=%v", hasArtifact, hasAttachment)
	}
}

func runArtifactCommand(t *testing.T, cmd *cobra.Command, args []string, flags map[string]string) string {
	t.Helper()
	for key, value := range flags {
		if err := cmd.Flags().Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	out, err := captureStdout(t, func() error {
		if cmd.RunE != nil {
			return cmd.RunE(cmd, args)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func assertArtifactJSON(t *testing.T, out, artifactID, revisionID, operationID, state string) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	for key, want := range map[string]string{
		"artifact_id":  artifactID,
		"revision_id":  revisionID,
		"operation_id": operationID,
		"state":        state,
	} {
		if got[key] != want {
			t.Fatalf("%s = %#v, want %s in %s", key, got[key], want, out)
		}
	}
}
