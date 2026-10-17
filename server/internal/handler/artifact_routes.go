package handler

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/pkg/featureflag"
)

// RequireArtifactsV1 answers 404 while artifacts_v1 is off. A missing flag
// service is off, matching every other fail-closed product flag.
func (h *Handler) RequireArtifactsV1(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var flags *featureflag.Service
		if h != nil {
			flags = h.FeatureFlags
		}
		if !featureflags.ArtifactsV1Enabled(r.Context(), flags) {
			writeError(w, http.StatusNotFound, "artifacts are disabled")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// MountArtifactRoutes is the only place artifact HTTP may be registered.
// The flag is read at process start. Off (the default) mounts nothing, so
// /api/artifacts does not exist. On mounts the read routes. Attachment
// routes are not registered here.
func MountArtifactRoutes(r chi.Router, h *Handler) {
	if r == nil || h == nil || !featureflags.ArtifactsV1Enabled(context.Background(), h.FeatureFlags) {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(h.RequireArtifactsV1)
		r.Get("/api/artifacts", h.ListArtifacts)
		r.Get("/api/artifacts/{artifactID}", h.GetArtifact)
		r.Get("/api/artifacts/{artifactID}/revisions", h.ListArtifactRevisions)
		r.Get("/api/artifacts/{artifactID}/revisions/{revisionID}/preview", h.GetArtifactRevisionPreview)
		r.Get("/api/artifacts/{artifactID}/revisions/{revisionID}/content", h.GetArtifactRevisionContent)
	})
}
