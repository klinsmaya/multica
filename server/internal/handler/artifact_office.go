package handler

import (
	"io"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/artifact"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func artifactOfficePreviewPath(artifactID, revisionID string) string {
	return "/api/artifacts/" + artifactID + "/revisions/" + revisionID + "/office-preview"
}

func (h *Handler) attachOfficePreview(r *http.Request, art db.Artifact, rev db.GetArtifactRevisionRow, resp *artifactPreviewResponse) {
	format := resp.PreviewKind
	var data []byte
	oversize := rev.SizeBytes > artifact.MaxOfficeInputBytes
	if !oversize && h.Storage != nil {
		blob, err := h.Queries.GetArtifactBlob(r.Context(), rev.BlobID)
		if err != nil {
			resp.OfficeStatus = artifact.OfficeFailed
			resp.OfficeError = artifact.CodeConversionFailed
			return
		}
		reader, err := h.Storage.GetReader(r.Context(), blob.StorageKey)
		if err != nil {
			resp.OfficeStatus = artifact.OfficeFailed
			resp.OfficeError = artifact.CodeConversionFailed
			return
		}
		data, err = io.ReadAll(io.LimitReader(reader, artifact.MaxOfficeInputBytes+1))
		reader.Close()
		if err != nil {
			resp.OfficeStatus = artifact.OfficeFailed
			resp.OfficeError = artifact.CodeConversionFailed
			return
		}
	}
	view, err := h.artifactService().EnsureOfficeDerivative(r.Context(), artifact.OfficeDerivativeInput{
		WorkspaceID: uuid.UUID(art.WorkspaceID.Bytes),
		ArtifactID:  uuid.UUID(art.ID.Bytes),
		RevisionID:  uuid.UUID(rev.ID.Bytes),
		Format:      format,
		Data:        data,
		Oversize:    oversize,
	})
	if err != nil {
		slog.Error("office preview failed", "error", err)
		resp.OfficeStatus = artifact.OfficeFailed
		resp.OfficeError = artifact.CodeConversionFailed
		return
	}
	resp.OfficeStatus = view.Status
	resp.OfficeError = view.ErrorCode
	resp.PageCount = view.PageCount
	resp.SheetNames = view.SheetNames
	resp.SlideCount = view.SlideCount
	if view.Status == artifact.OfficeReady {
		resp.OfficePreviewPath = artifactOfficePreviewPath(uuidToString(rev.ArtifactID), uuidToString(rev.ID))
	}
}

// GetArtifactOfficePreview — GET /api/artifacts/{artifactID}/revisions/{revisionID}/office-preview
//
// Serves the sanitized derivative for one revision. Membership is checked
// again here. A missing or failed derivative has no body. The original
// bytes stay on the content route.
func (h *Handler) GetArtifactOfficePreview(w http.ResponseWriter, r *http.Request) {
	art, rev, ok := h.loadArtifactRevisionForMember(w, r)
	if !ok {
		return
	}
	format, office := artifact.OfficeFormat(rev.MediaType, rev.Filename)
	if !office {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	row, err := h.Queries.GetArtifactDerivative(r.Context(), db.GetArtifactDerivativeParams{
		RevisionID: rev.ID,
		Format:     format,
	})
	if err != nil || row.Status != artifact.OfficeReady || row.StorageKey == "" {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	if row.WorkspaceID != art.WorkspaceID || row.ArtifactID != art.ID || row.RevisionID != rev.ID {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	if h.Storage == nil {
		writeFeatureDisabled(w, "storage_not_configured", "storage not configured")
		return
	}
	reader, err := h.Storage.GetReader(r.Context(), row.StorageKey)
	if err != nil {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	defer reader.Close()
	body, err := io.ReadAll(io.LimitReader(reader, artifact.MaxStaticPreviewBytes+1))
	if err != nil || len(body) == 0 || len(body) > artifact.MaxStaticPreviewBytes {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	writeStaticPreview(w, "text/html; charset=utf-8", body)
}
