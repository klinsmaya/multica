package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestArtifactSubmitCreateUpdateGetStatus(t *testing.T) {
	if testPool == nil || dbfx == nil {
		t.Skip("test database not available")
	}
	store := &mockStorage{}
	orig := testHandler.Storage
	testHandler.Storage = store
	t.Cleanup(func() { testHandler.Storage = orig })
	ws := mustUUID(t, testWorkspaceID)
	t.Cleanup(func() {
		_ = db.New(testPool).DeleteWorkspaceArtifacts(context.Background(), artifactPG(ws))
	})

	agentID := dbfx.Agent(t, "artifact-cli-agent", testRuntimeID)
	taskA := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": testRuntimeID})
	taskB := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": testRuntimeID})
	router := newArtifactSubmitRouter()

	created := postArtifact(t, router, "/api/artifacts", taskHeaders(taskA, agentID), map[string]string{
		"task_id": taskA,
		"title":   "Weekly report",
	}, "report.md", []byte("# report v1\n"))
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body %s", created.Code, created.Body.String())
	}
	var first artifactCommandResponse
	if err := json.Unmarshal(created.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.State != "committed" || first.ArtifactID == "" || first.RevisionID == "" || first.OperationID == "" {
		t.Fatalf("create = %+v", first)
	}
	if strings.Contains(created.Body.String(), "storage_key") || strings.Contains(created.Body.String(), "workspaces/") {
		t.Fatalf("create leaked storage: %s", created.Body.String())
	}

	got := artifactGET(t, router, "/api/artifacts/"+first.ArtifactID, testUserID, testWorkspaceID)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), first.RevisionID) || !strings.Contains(got.Body.String(), first.OperationID) {
		t.Fatalf("get status = %d, body %s", got.Code, got.Body.String())
	}
	body := artifactGET(t, router, "/api/artifacts/"+first.ArtifactID+"/revisions/"+first.RevisionID+"/content", testUserID, "")
	if body.Body.String() != "# report v1\n" {
		t.Fatalf("content = %q", body.Body.String())
	}

	status := artifactRequest(t, router, http.MethodGet, "/api/artifact-operations/"+first.OperationID, taskHeaders(taskA, agentID), nil, "")
	if status.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", status.Code, status.Body.String())
	}
	if strings.Contains(status.Body.String(), "storage_key") {
		t.Fatalf("status leaked storage: %s", status.Body.String())
	}
	var op artifactCommandResponse
	if err := json.Unmarshal(status.Body.Bytes(), &op); err != nil {
		t.Fatal(err)
	}
	if op.State != "committed" || op.ArtifactID != first.ArtifactID || op.RevisionID != first.RevisionID || op.OperationID != first.OperationID {
		t.Fatalf("operation = %+v", op)
	}

	updated := postArtifact(t, router, "/api/artifacts/"+first.ArtifactID+"/revisions", taskHeaders(taskA, agentID), map[string]string{
		"base_revision_id": first.RevisionID,
	}, "report.md", []byte("# report v2\n"))
	if updated.Code != http.StatusOK {
		t.Fatalf("update status = %d, body %s", updated.Code, updated.Body.String())
	}
	var second artifactCommandResponse
	if err := json.Unmarshal(updated.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if second.ArtifactID != first.ArtifactID || second.RevisionID == first.RevisionID || second.RevisionNo != 2 || second.State != "committed" {
		t.Fatalf("update = %+v", second)
	}
	v2 := artifactGET(t, router, "/api/artifacts/"+first.ArtifactID+"/revisions/"+second.RevisionID+"/content", testUserID, "")
	if v2.Body.String() != "# report v2\n" {
		t.Fatalf("v2 content = %q", v2.Body.String())
	}
	selected := artifactGET(t, router, "/api/artifacts/"+first.ArtifactID+"?revision_id="+first.RevisionID, testUserID, testWorkspaceID)
	var selectedBody artifactResponse
	if err := json.Unmarshal(selected.Body.Bytes(), &selectedBody); err != nil {
		t.Fatal(err)
	}
	if selected.Code != http.StatusOK || selectedBody.RevisionID != first.RevisionID || selectedBody.RevisionNo == nil || *selectedBody.RevisionNo != 1 {
		t.Fatalf("selected revision = %s", selected.Body.String())
	}

	wrongTask := postArtifact(t, router, "/api/artifacts", taskHeaders(taskA, agentID), map[string]string{
		"task_id": taskB,
		"title":   "Other",
	}, "other.md", []byte("# no\n"))
	if wrongTask.Code != http.StatusForbidden {
		t.Fatalf("wrong task create = %d, body %s", wrongTask.Code, wrongTask.Body.String())
	}
	cross := postArtifact(t, router, "/api/artifacts/"+first.ArtifactID+"/revisions", taskHeaders(taskB, agentID), map[string]string{
		"base_revision_id": second.RevisionID,
	}, "report.md", []byte("# stolen\n"))
	if cross.Code != http.StatusNotFound {
		t.Fatalf("cross-task update = %d, body %s", cross.Code, cross.Body.String())
	}
	if strings.Contains(cross.Body.String(), "# stolen") {
		t.Fatal("cross-task update returned file bytes")
	}
	hidden := artifactRequest(t, router, http.MethodGet, "/api/artifacts/"+first.ArtifactID, taskHeaders(taskB, agentID), nil, "")
	if hidden.Code != http.StatusNotFound {
		t.Fatalf("cross-task get = %d", hidden.Code)
	}
	hiddenOp := artifactRequest(t, router, http.MethodGet, "/api/artifact-operations/"+first.OperationID, taskHeaders(taskB, agentID), nil, "")
	if hiddenOp.Code != http.StatusNotFound {
		t.Fatalf("cross-task status = %d, body %s", hiddenOp.Code, hiddenOp.Body.String())
	}
	list := artifactRequest(t, router, http.MethodGet, "/api/artifacts?owner_scope=workspace&owner_id="+testWorkspaceID, taskHeaders(taskA, agentID), nil, "")
	if list.Code != http.StatusNotFound {
		t.Fatalf("task token workspace list = %d, body %s", list.Code, list.Body.String())
	}
	member := artifactGET(t, router, "/api/artifacts/"+first.ArtifactID, testUserID, testWorkspaceID)
	if member.Code != http.StatusOK {
		t.Fatalf("member get = %d", member.Code)
	}
	human := postArtifact(t, router, "/api/artifacts", map[string]string{
		"X-User-ID":      testUserID,
		"X-Workspace-ID": testWorkspaceID,
	}, map[string]string{"task_id": taskA, "title": "Nope"}, "nope.md", []byte("nope"))
	if human.Code != http.StatusForbidden {
		t.Fatalf("human create = %d, body %s", human.Code, human.Body.String())
	}
	anon := postArtifact(t, router, "/api/artifacts", nil, map[string]string{"task_id": taskA, "title": "Nope"}, "nope.md", []byte("nope"))
	if anon.Code != http.StatusUnauthorized {
		t.Fatalf("anon create = %d", anon.Code)
	}
}

func newArtifactSubmitRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/api/artifacts", testHandler.ListArtifacts)
	r.Post("/api/artifacts", testHandler.CreateArtifact)
	r.Get("/api/artifacts/{artifactID}", testHandler.GetArtifact)
	r.Post("/api/artifacts/{artifactID}/revisions", testHandler.UpdateArtifact)
	r.Get("/api/artifacts/{artifactID}/revisions/{revisionID}/content", testHandler.GetArtifactRevisionContent)
	r.Get("/api/artifact-operations/{operationID}", testHandler.GetArtifactOperationStatus)
	return r
}

func taskHeaders(taskID, agentID string) map[string]string {
	return map[string]string{
		"X-User-ID":      testUserID,
		"X-Workspace-ID": testWorkspaceID,
		"X-Actor-Source": "task_token",
		"X-Task-ID":      taskID,
		"X-Agent-ID":     agentID,
	}
}

func postArtifact(t *testing.T, router http.Handler, path string, headers map[string]string, fields map[string]string, filename string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return artifactRequest(t, router, http.MethodPost, path, headers, &buf, writer.FormDataContentType())
}

func artifactRequest(t *testing.T, router http.Handler, method, path string, headers map[string]string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}
