package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/featureflags"
)

func TestArtifactsV1RoutesStayUnmounted(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		h := &Handler{}
		if enabled {
			withFeatureFlag(t, h, featureflags.ArtifactsV1, true)
		}
		r := chi.NewRouter()
		MountArtifactRoutes(r, h)
		var patterns []string
		walkRoutes(r, "", &patterns)
		for _, pattern := range patterns {
			if strings.Contains(pattern, "artifact") {
				t.Fatalf("enabled=%v mounted %s", enabled, pattern)
			}
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
