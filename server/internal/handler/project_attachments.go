package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Project library sources, as the `source` query parameter spells them. The
// two map onto the attachment's uploader: agents produce deliverables, members
// upload files.
const (
	projectAttachmentSourceDeliverable = "deliverable"
	projectAttachmentSourceUpload      = "upload"
)

const (
	projectAttachmentsDefaultLimit = 30
	projectAttachmentsMaxLimit     = 100
)

// projectLibraryIssueTitle is the fixed title of the issue that holds files
// uploaded straight to a project. It is stored as the issue's title.
const projectLibraryIssueTitle = "Project files"

// ProjectAttachmentIssue is the issue a library attachment belongs to.
type ProjectAttachmentIssue struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
}

// ProjectAttachmentResponse is the regular attachment payload plus the issue
// it hangs off. The uploader is already part of the attachment
// (uploader_type / uploader_id).
type ProjectAttachmentResponse struct {
	AttachmentResponse
	Issue ProjectAttachmentIssue `json:"issue"`
}

// ListProjectAttachments returns the attachments of every issue in a project,
// newest first.
//
//	GET /api/projects/{id}/attachments?source=deliverable|upload&limit=&offset=
//
// `total` counts the rows of the requested source; `deliverable_total` and
// `upload_total` always count both groups so one request can label both.
//
// There is deliberately no `category` filter. The frontend buckets files by a
// content-type sniff plus filename-extension tables (getPreviewKind /
// isDocumentFile) that cannot be mirrored here without a second copy to keep
// in sync, so the client filters the loaded rows instead.
func (h *Handler) ListProjectAttachments(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	query := r.URL.Query()
	var uploaderType pgtype.Text
	switch source := query.Get("source"); source {
	case "":
	case projectAttachmentSourceDeliverable:
		uploaderType = pgtype.Text{String: "agent", Valid: true}
	case projectAttachmentSourceUpload:
		uploaderType = pgtype.Text{String: "member", Valid: true}
	default:
		writeError(w, http.StatusBadRequest, "invalid source; valid values: deliverable, upload")
		return
	}
	limit, ok := parseProjectAttachmentsInt(w, query.Get("limit"), "limit", projectAttachmentsDefaultLimit)
	if !ok {
		return
	}
	if limit < 1 {
		limit = projectAttachmentsDefaultLimit
	}
	if limit > projectAttachmentsMaxLimit {
		limit = projectAttachmentsMaxLimit
	}
	offset, ok := parseProjectAttachmentsInt(w, query.Get("offset"), "offset", 0)
	if !ok {
		return
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := h.Queries.ListProjectAttachments(r.Context(), db.ListProjectAttachmentsParams{
		ProjectID:    project.ID,
		WorkspaceID:  project.WorkspaceID,
		UploaderType: uploaderType,
		PageLimit:    int32(limit),
		PageOffset:   int32(offset),
	})
	if err != nil {
		slog.Error("list project attachments", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to list project attachments")
		return
	}
	counts, err := h.Queries.CountProjectAttachments(r.Context(), db.CountProjectAttachmentsParams{
		ProjectID:   project.ID,
		WorkspaceID: project.WorkspaceID,
	})
	if err != nil {
		slog.Error("count project attachments", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to list project attachments")
		return
	}

	total := counts.AgentCount + counts.MemberCount
	switch uploaderType.String {
	case "agent":
		total = counts.AgentCount
	case "member":
		total = counts.MemberCount
	}

	prefix := h.getIssuePrefix(r.Context(), project.WorkspaceID)
	mode := attachmentURLModeFromRequest(r)
	resp := make([]ProjectAttachmentResponse, len(rows))
	for i, row := range rows {
		resp[i] = ProjectAttachmentResponse{
			AttachmentResponse: h.attachmentToResponse(row.Attachment, mode),
			Issue: ProjectAttachmentIssue{
				ID:         uuidToString(row.Attachment.IssueID),
				Identifier: prefix + "-" + strconv.Itoa(int(row.IssueNumber)),
				Title:      row.IssueTitle,
			},
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"attachments":       resp,
		"total":             total,
		"deliverable_total": counts.AgentCount,
		"upload_total":      counts.MemberCount,
	})
}

func parseProjectAttachmentsInt(w http.ResponseWriter, raw, field string, fallback int) (int, bool) {
	if raw == "" {
		return fallback, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid "+field)
		return 0, false
	}
	return n, true
}

// ProjectLibraryIssueResponse names the issue a project-level upload attaches
// to.
type ProjectLibraryIssueResponse struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier"`
}

// EnsureProjectLibraryIssue returns the project's "Project files" issue,
// creating it on first use.
//
//	POST /api/projects/{id}/library-issue
//
// Files uploaded from the library have to hang off an issue (attachments are
// issue- or comment-owned), so the project gets one top-level, unassigned,
// backlog issue for them. The create goes through IssueService, whose
// duplicate guard serializes concurrent first uploads per (project, title):
// the loser gets ErrActiveDuplicate and returns the winner's issue.
func (h *Handler) EnsureProjectLibraryIssue(w http.ResponseWriter, r *http.Request) {
	r = h.withWakeupActor(r)
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	creatorID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := uuidToString(project.WorkspaceID)
	prefix := h.getIssuePrefix(r.Context(), project.WorkspaceID)

	respond := func(status int, issue db.Issue) {
		writeJSON(w, status, ProjectLibraryIssueResponse{
			ID:         uuidToString(issue.ID),
			Identifier: prefix + "-" + strconv.Itoa(int(issue.Number)),
		})
	}

	existing, err := h.Queries.GetProjectLibraryIssue(r.Context(), db.GetProjectLibraryIssueParams{
		ProjectID:   project.ID,
		WorkspaceID: project.WorkspaceID,
		Title:       projectLibraryIssueTitle,
	})
	if err == nil {
		respond(http.StatusOK, existing)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		slog.Error("find project library issue", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to find project files issue")
		return
	}

	creatorType, actualCreatorID := h.resolveActor(r, creatorID, workspaceID)
	platform, _, _ := middleware.ClientMetadataFromContext(r.Context())
	fill := h.newStatusCategoryFiller(r.Context(), project.WorkspaceID)
	res, err := h.IssueService.Create(r.Context(), service.IssueCreateParams{
		WorkspaceID: project.WorkspaceID,
		Title:       projectLibraryIssueTitle,
		Status:      issuestatus.Backlog,
		Priority:    "none",
		CreatorType: creatorType,
		CreatorID:   parseUUID(actualCreatorID),
		ProjectID:   project.ID,
	}, service.IssueCreateOpts{
		ActorID:  actualCreatorID,
		Platform: platform,
		BroadcastPayload: func(issue db.Issue, _ []db.Attachment, labels []db.IssueLabel) map[string]any {
			payload := issueToResponse(issue, prefix)
			fill(&payload)
			labelResponses := labelsToResponse(labels)
			payload.Labels = &labelResponses
			return map[string]any{"issue": payload}
		},
	})
	switch {
	case errors.Is(err, service.ErrActiveDuplicate) && res.DuplicateIssue != nil:
		respond(http.StatusOK, *res.DuplicateIssue)
		return
	case errors.Is(err, service.ErrProjectNotFound):
		writeError(w, http.StatusNotFound, "project not found")
		return
	case errors.Is(err, service.ErrIssueStatusUnavailable):
		writeError(w, http.StatusConflict, "the target status was archived while this request was in flight; retry")
		return
	case writeIssueLimitReached(w, err):
		return
	case err != nil:
		slog.Warn("create project library issue failed", append(logger.RequestAttrs(r), "error", err, "project_id", uuidToString(project.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to create project files issue")
		return
	}
	respond(http.StatusCreated, res.Issue)
}
