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

// MountArtifactRoutes registers authenticated artifact HTTP. The flag is
// read at process start. Off (the default) mounts nothing, so /api/artifacts
// does not exist. On mounts the read routes, the task-scoped submit routes,
// text edit sessions, history restore, static HTML/SVG preview, read-only
// Office preview, and share publish/revoke. Attachment routes are not
// registered here. Token reads are MountArtifactShareRoutes, outside the
// session cookie group.
func MountArtifactRoutes(r chi.Router, h *Handler) {
	if r == nil || h == nil || !featureflags.ArtifactsV1Enabled(context.Background(), h.FeatureFlags) {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(h.RequireArtifactsV1)
		r.Get("/api/artifacts", h.ListArtifacts)
		r.Post("/api/artifacts", h.CreateArtifact)
		r.Get("/api/artifacts/{artifactID}", h.GetArtifact)
		r.Get("/api/artifacts/{artifactID}/revisions", h.ListArtifactRevisions)
		r.Post("/api/artifacts/{artifactID}/revisions", h.UpdateArtifact)
		r.Post("/api/artifacts/{artifactID}/restore", h.RestoreArtifact)
		r.Post("/api/artifacts/{artifactID}/trash", h.TrashArtifact)
		r.Post("/api/artifacts/{artifactID}/trash/restore", h.RestoreTrashedArtifact)
		r.Post("/api/artifacts/{artifactID}/shares", h.PublishArtifactShare)
		r.Get("/api/artifact-ops", h.GetArtifactOps)
		r.Post("/api/artifacts/{artifactID}/shares/{snapshotID}/revoke", h.RevokeArtifactShare)
		r.Post("/api/artifacts/{artifactID}/edit-sessions", h.CreateArtifactEditSession)
		r.Get("/api/artifacts/{artifactID}/revisions/{revisionID}/preview", h.GetArtifactRevisionPreview)
		r.Get("/api/artifacts/{artifactID}/revisions/{revisionID}/static-preview", h.GetArtifactStaticPreview)
		r.Get("/api/artifacts/{artifactID}/revisions/{revisionID}/office-preview", h.GetArtifactOfficePreview)
		r.Get("/api/artifacts/{artifactID}/revisions/{revisionID}/resources/*", h.GetArtifactPreviewResource)
		r.Get("/api/artifacts/{artifactID}/revisions/{revisionID}/content", h.GetArtifactRevisionContent)
		r.Post("/api/artifact-edit-sessions/{sessionID}/heartbeat", h.HeartbeatArtifactEditSession)
		r.Put("/api/artifact-edit-sessions/{sessionID}/draft", h.SaveArtifactEditDraft)
		r.Get("/api/artifact-operations/{operationID}", h.GetArtifactOperationStatus)
	})
}

// MountArtifactShareRoutes registers token reads. It must be called outside
// the session Auth group. Off mounts nothing. A cookie is not a credential
// on these routes.
func MountArtifactShareRoutes(r chi.Router, h *Handler) {
	if r == nil || h == nil || !featureflags.ArtifactsV1Enabled(context.Background(), h.FeatureFlags) {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(h.RequireArtifactsV1)
		r.Get("/api/artifact-shares/{token}", h.GetArtifactShare)
		r.Get("/api/artifact-shares/{token}/content", h.GetArtifactShareContent)
		r.Get("/api/artifact-shares/{token}/resources/*", h.GetArtifactShareResource)
	})
}
