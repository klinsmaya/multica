package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/featureflags"
)

func TestArtifactsV1RoutesStayUnmountedWhenOff(t *testing.T) {
	h := &Handler{}
	r := chi.NewRouter()
	MountArtifactRoutes(r, h)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/artifacts", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var patterns []string
	walkRoutes(r, "", &patterns)
	for _, pattern := range patterns {
		if strings.Contains(pattern, "artifact") {
			t.Fatalf("flag off mounted %s", pattern)
		}
	}
}

func TestArtifactsV1ReadRoutesMountWhenEnabled(t *testing.T) {
	h := &Handler{}
	withFeatureFlag(t, h, featureflags.ArtifactsV1, true)
	r := chi.NewRouter()
	MountArtifactRoutes(r, h)
	var patterns []string
	walkRoutes(r, "", &patterns)
	joined := strings.Join(patterns, "\n")
	for _, want := range []string{
		"/api/artifacts",
		"/api/artifacts/{artifactID}",
		"/api/artifacts/{artifactID}/revisions",
		"/api/artifacts/{artifactID}/revisions/{revisionID}/preview",
		"/api/artifacts/{artifactID}/revisions/{revisionID}/content",
		"/api/artifacts/{artifactID}/edit-sessions",
		"/api/artifacts/{artifactID}/restore",
		"/api/artifact-edit-sessions/{sessionID}/heartbeat",
		"/api/artifact-edit-sessions/{sessionID}/draft",
		"/api/artifact-operations/{operationID}",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in %s", want, joined)
		}
	}
}

func TestRequireArtifactsV1IsDarkByDefault(t *testing.T) {
	h := &Handler{}
	called := false
	next := h.RequireArtifactsV1(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusCreated)
	}))
	rec := httptest.NewRecorder()
	next.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/artifacts", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if called {
		t.Fatal("artifact handler ran while the flag was off")
	}
}

func TestRequireArtifactsV1PassesWhenEnabled(t *testing.T) {
	h := &Handler{}
	withFeatureFlag(t, h, featureflags.ArtifactsV1, true)
	called := false
	next := h.RequireArtifactsV1(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	next.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/artifacts", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if !called {
		t.Fatal("artifact handler did not run while the flag was on")
	}
}

func walkRoutes(r chi.Routes, prefix string, out *[]string) {
	for _, route := range r.Routes() {
		pattern := prefix + route.Pattern
		*out = append(*out, pattern)
		if route.SubRoutes != nil {
			walkRoutes(route.SubRoutes, pattern, out)
		}
	}
}
