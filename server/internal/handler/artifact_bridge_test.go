package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/artifact"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestBridgeOffLeavesCloudArtifactReadable(t *testing.T) {
	if testPool == nil {
		t.Skip("test database not available")
	}
	origBridge := testHandler.ArtifactBridge
	store := &mockStorage{}
	origStore := testHandler.Storage
	testHandler.ArtifactBridge = artifact.LocalBridge{}
	testHandler.Storage = store
	t.Cleanup(func() {
		testHandler.ArtifactBridge = origBridge
		testHandler.Storage = origStore
	})

	ws := mustUUID(t, testWorkspaceID)
	actor := mustUUID(t, testUserID)
	t.Cleanup(func() {
		_ = db.New(testPool).DeleteWorkspaceArtifacts(context.Background(), artifactPG(ws))
	})
	svc := &artifact.Service{DB: testPool, Store: store, Scanner: artifact.NopScanner{}}
	owner := uuid.New()
	body := []byte("# still here\n")
	created := createHTTPArtifact(t, svc, ws, actor, owner, "issue", "Cloud report")
	committed := commitHTTPArtifact(t, svc, ws, actor, created.ID, nil, "bridge-off", "report.md", "text/markdown", body)

	router := chi.NewRouter()
	router.Get("/api/artifacts/{artifactID}/revisions/{revisionID}/content", testHandler.GetArtifactRevisionContent)
	router.Post("/api/daemon/artifact-bridge/candidates", testHandler.DiscoverLocalCandidates)
	router.Post("/api/daemon/artifact-bridge/local-delete", testHandler.KeepCloudOnLocalDelete)

	content := artifactGET(t, router, "/api/artifacts/"+created.ID.String()+"/revisions/"+committed.RevisionID.String()+"/content", testUserID, "")
	if content.Code != http.StatusOK || content.Body.String() != string(body) {
		t.Fatalf("content status = %d body %s", content.Code, content.Body.String())
	}

	for _, path := range []string{"/api/daemon/artifact-bridge/candidates", "/api/daemon/artifact-bridge/local-delete"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"path":"/etc/passwd"}`))
		req.Host = "127.0.0.1"
		req.Header.Set("Origin", "http://127.0.0.1")
		req.Header.Set("Authorization", "Bearer mdt_test")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d body %s", path, rec.Code, rec.Body.String())
		}
	}

	again := artifactGET(t, router, "/api/artifacts/"+created.ID.String()+"/revisions/"+committed.RevisionID.String()+"/content", testUserID, "")
	if again.Code != http.StatusOK || again.Body.String() != string(body) {
		t.Fatalf("content after bridge refusal = %d %s", again.Code, again.Body.String())
	}
}

func TestBridgeHTTPRejectsMissingTokenBadHostAndCrossOrigin(t *testing.T) {
	orig := testHandler.ArtifactBridge
	testHandler.ArtifactBridge = artifact.LocalBridge{Enabled: true}
	t.Cleanup(func() { testHandler.ArtifactBridge = orig })

	router := chi.NewRouter()
	router.Post("/api/daemon/artifact-bridge/candidates", testHandler.DiscoverLocalCandidates)
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/daemon/artifact-bridge/candidates", strings.NewReader(`{}`))
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestBridgeOnLocalDeleteDoesNotChangeCloudRead(t *testing.T) {
	if testPool == nil {
		t.Skip("test database not available")
	}
	origBridge := testHandler.ArtifactBridge
	store := &mockStorage{}
	origStore := testHandler.Storage
	testHandler.ArtifactBridge = artifact.LocalBridge{Enabled: true}
	testHandler.Storage = store
	t.Cleanup(func() {
		testHandler.ArtifactBridge = origBridge
		testHandler.Storage = origStore
	})

	ws := mustUUID(t, testWorkspaceID)
	actor := mustUUID(t, testUserID)
	t.Cleanup(func() {
		_ = db.New(testPool).DeleteWorkspaceArtifacts(context.Background(), artifactPG(ws))
	})
	svc := &artifact.Service{DB: testPool, Store: store, Scanner: artifact.NopScanner{}}
	body := []byte("# kept\n")
	created := createHTTPArtifact(t, svc, ws, actor, uuid.New(), "issue", "Kept report")
	committed := commitHTTPArtifact(t, svc, ws, actor, created.ID, nil, "keep-cloud", "kept.md", "text/markdown", body)

	router := chi.NewRouter()
	router.Get("/api/artifacts/{artifactID}/revisions/{revisionID}/content", testHandler.GetArtifactRevisionContent)
	router.Post("/api/daemon/artifact-bridge/local-delete", testHandler.KeepCloudOnLocalDelete)

	req := httptest.NewRequest(http.MethodPost, "/api/daemon/artifact-bridge/local-delete", strings.NewReader(`{}`))
	req.Host = "127.0.0.1"
	req.Header.Set("Origin", "http://127.0.0.1")
	req.Header.Set("Authorization", "Bearer mdt_test")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "keep_cloud") {
		t.Fatalf("delete status = %d body %s", rec.Code, rec.Body.String())
	}
	content := artifactGET(t, router, "/api/artifacts/"+created.ID.String()+"/revisions/"+committed.RevisionID.String()+"/content", testUserID, "")
	if content.Code != http.StatusOK || content.Body.String() != string(body) {
		t.Fatalf("content = %d %s", content.Code, content.Body.String())
	}
}
