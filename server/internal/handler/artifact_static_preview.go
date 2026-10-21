package handler

import (
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/artifact"
)

// GetArtifactStaticPreview — GET /api/artifacts/{artifactID}/revisions/{revisionID}/static-preview
//
// Serves a sanitized HTML or SVG fragment. The original bytes stay on the
// content route. This response is on the API host, so a direct browser GET
// still sends the session cookie. The body does not contain that cookie, and
// the CSP does not grant the document script or network access.
func (h *Handler) GetArtifactStaticPreview(w http.ResponseWriter, r *http.Request) {
	_, rev, ok := h.loadArtifactRevisionForMember(w, r)
	if !ok {
		return
	}
	if _, active := artifact.StaticPreviewKind(rev.MediaType, rev.Filename); !active {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	if rev.SizeBytes > artifact.MaxStaticPreviewBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "artifact is too large to preview")
		return
	}
	if h.Storage == nil {
		writeFeatureDisabled(w, "storage_not_configured", "storage not configured")
		return
	}
	blob, err := h.Queries.GetArtifactBlob(r.Context(), rev.BlobID)
	if err != nil {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	reader, err := h.Storage.GetReader(r.Context(), blob.StorageKey)
	if err != nil {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	defer reader.Close()
	raw, err := io.ReadAll(io.LimitReader(reader, artifact.MaxStaticPreviewBytes+1))
	if err != nil {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	doc, err := artifact.SanitizeStaticDocument(rev.MediaType, rev.Filename, raw)
	if errors.Is(err, artifact.ErrStaticPreviewTooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "artifact is too large to preview")
		return
	}
	if err != nil {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	writeStaticPreview(w, doc.ContentType, doc.Body)
}

// GetArtifactPreviewResource — GET /api/artifacts/{artifactID}/revisions/{revisionID}/resources/*
//
// Accepts one package-internal path. The path is not a storage key and not a
// URL. A single-file HTML or SVG revision has an empty manifest, so a clean
// path is still 404.
func (h *Handler) GetArtifactPreviewResource(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := h.loadArtifactRevisionForMember(w, r); !ok {
		return
	}
	raw, err := url.PathUnescape(chi.URLParam(r, "*"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid resource path")
		return
	}
	if _, err := artifact.NormalizePackagePath(raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid resource path")
		return
	}
	if !artifact.SingleFileManifest().Allows(raw) {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	writeError(w, http.StatusNotFound, "artifact not found")
}

func writeStaticPreview(w http.ResponseWriter, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Security-Policy", artifact.StaticPreviewCSP)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", artifact.StaticPreviewReferrerPolicy)
	w.Header().Set("Permissions-Policy", artifact.StaticPreviewPermissionsPolicy)
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
