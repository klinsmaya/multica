package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/artifact"
	"github.com/multica-ai/multica/server/internal/featureflags"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestArtifactShareIgnoresSessionCookie(t *testing.T) {
	if testPool == nil {
		t.Skip("test database not available")
	}
	store := &mockStorage{}
	orig := testHandler.Storage
	testHandler.Storage = store
	t.Cleanup(func() { testHandler.Storage = orig })
	withFeatureFlag(t, testHandler, featureflags.ArtifactsV1, true)

	ws := mustUUID(t, testWorkspaceID)
	actor := mustUUID(t, testUserID)
	t.Cleanup(func() {
		_ = db.New(testPool).DeleteWorkspaceArtifacts(context.Background(), artifactPG(ws))
	})
	svc := &artifact.Service{DB: testPool, Store: store, Scanner: artifact.NopScanner{}}
	created := createHTTPArtifact(t, svc, ws, actor, uuid.New(), "issue", "Shared page")
	body := []byte(`<html><p>shared-page</p><img src="secret.png"></html>`)
	committed := commitHTTPArtifact(t, svc, ws, actor, created.ID, nil, "share-html", "page.html", "text/html", body)
	published, err := svc.Publish(context.Background(), artifact.PublishInput{
		WorkspaceID: ws,
		ActorID:     actor,
		ArtifactID:  created.ID,
		RevisionID:  committed.RevisionID,
	})
	if err != nil {
		t.Fatal(err)
	}

	router := chi.NewRouter()
	MountArtifactShareRoutes(router, testHandler)

	cookieOnly := httptest.NewRequest(http.MethodGet, "/api/artifact-shares/missing-token/content", nil)
	cookieOnly.Header.Set("Cookie", "session=main-app-secret")
	cookieOnly.Header.Set("X-User-ID", testUserID)
	denied := httptest.NewRecorder()
	router.ServeHTTP(denied, cookieOnly)
	if denied.Code != http.StatusNotFound {
		t.Fatalf("cookie status = %d, body %s", denied.Code, denied.Body.String())
	}
	if strings.Contains(denied.Body.String(), "shared-page") || strings.Contains(denied.Body.String(), "main-app-secret") {
		t.Fatalf("cookie response leaked %s", denied.Body.String())
	}

	content := httptest.NewRequest(http.MethodGet, "/api/artifact-shares/"+published.Token+"/content", nil)
	content.Header.Set("Cookie", "session=main-app-secret")
	content.Header.Set("X-User-ID", testUserID)
	ok := httptest.NewRecorder()
	router.ServeHTTP(ok, content)
	if ok.Code != http.StatusOK {
		t.Fatalf("content status = %d, body %s", ok.Code, ok.Body.String())
	}
	if ok.Body.String() != string(body) {
		t.Fatalf("content = %q", ok.Body.String())
	}
	if strings.Contains(ok.Body.String(), "main-app-secret") {
		t.Fatal("share body echoed the session cookie")
	}
	if ok.Header().Get("Set-Cookie") != "" {
		t.Fatalf("set-cookie = %q", ok.Header().Get("Set-Cookie"))
	}
	if ok.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("cache = %q", ok.Header().Get("Cache-Control"))
	}
	if ok.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("content type = %q", ok.Header().Get("Content-Type"))
	}
	if !strings.Contains(ok.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("disposition = %q", ok.Header().Get("Content-Disposition"))
	}

	resource := httptest.NewRequest(http.MethodGet, "/api/artifact-shares/"+published.Token+"/resources/secret.png", nil)
	resource.Header.Set("Cookie", "session=main-app-secret")
	missing := httptest.NewRecorder()
	router.ServeHTTP(missing, resource)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("resource status = %d, body %s", missing.Code, missing.Body.String())
	}
	if strings.Contains(missing.Body.String(), "shared-page") {
		t.Fatal("unlisted resource returned the html")
	}
}

func TestArtifactSharePublishRefusesExternalAudience(t *testing.T) {
	if testPool == nil {
		t.Skip("test database not available")
	}
	store := &mockStorage{}
	orig := testHandler.Storage
	testHandler.Storage = store
	t.Cleanup(func() { testHandler.Storage = orig })
	withFeatureFlag(t, testHandler, featureflags.ArtifactsV1, true)

	ws := mustUUID(t, testWorkspaceID)
	actor := mustUUID(t, testUserID)
	t.Cleanup(func() {
		_ = db.New(testPool).DeleteWorkspaceArtifacts(context.Background(), artifactPG(ws))
	})
	svc := &artifact.Service{DB: testPool, Store: store, Scanner: artifact.NopScanner{}}
	created := createHTTPArtifact(t, svc, ws, actor, uuid.New(), "issue", "Closed share")
	committed := commitHTTPArtifact(t, svc, ws, actor, created.ID, nil, "ext-http", "note.md", "text/markdown", []byte("nope"))

	router := chi.NewRouter()
	MountArtifactRoutes(router, testHandler)
	payload, _ := json.Marshal(map[string]string{
		"revision_id": committed.RevisionID.String(),
		"audience":    artifact.AudiencePublic,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/artifacts/"+created.ID.String()+"/shares", bytes.NewReader(payload))
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), artifact.CodeExternalShareDisabled) {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var n int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM artifact_publish_snapshots WHERE artifact_id = $1
	`, created.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("snapshots = %d", n)
	}
}

func TestArtifactShareRoutesMountWhenEnabled(t *testing.T) {
	h := &Handler{}
	withFeatureFlag(t, h, featureflags.ArtifactsV1, true)
	r := chi.NewRouter()
	MountArtifactShareRoutes(r, h)
	var patterns []string
	walkRoutes(r, "", &patterns)
	joined := strings.Join(patterns, "\n")
	for _, want := range []string{
		"/api/artifact-shares/{token}",
		"/api/artifact-shares/{token}/content",
		"/api/artifact-shares/{token}/resources/*",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in %s", want, joined)
		}
	}
}
