package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	projectPullRequestsDefaultLimit = 20
	projectPullRequestsMaxLimit     = 100
)

// projectPullRequestStates maps the tab's state filter onto the stored
// pull-request states (open, draft, merged, closed). "closed" is closed
// without merging: a merged PR is stored as "merged", never "closed".
var projectPullRequestStates = map[string][]string{
	"open":   {"open", "draft"},
	"merged": {"merged"},
	"closed": {"closed"},
}

// ProjectPullRequestIssueResponse is one project issue a pull request is
// linked to.
type ProjectPullRequestIssueResponse struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
}

// ProjectPullRequestResponse is the issue PR list row plus the project issues
// the PR is linked to.
type ProjectPullRequestResponse struct {
	GitHubPullRequestResponse
	Issues []ProjectPullRequestIssueResponse `json:"issues"`
}

// ProjectPullRequestCountsResponse is the distinct PR count per tab group,
// independent of the request's state filter.
type ProjectPullRequestCountsResponse struct {
	Open   int64 `json:"open"`
	Merged int64 `json:"merged"`
	Closed int64 `json:"closed"`
}

// ListProjectPullRequests (GET /api/projects/{id}/pull-requests) returns the
// pull requests linked to the project's issues, newest update first. A PR
// linked to several issues of the project is one row carrying all of them, and
// limit/offset page per PR. state narrows to open (open + draft), merged, or
// closed (closed without merging); total describes the filtered set and counts
// every group.
func (h *Handler) ListProjectPullRequests(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	query := r.URL.Query()
	stateFilter := []string{}
	if state := query.Get("state"); state != "" {
		states, known := projectPullRequestStates[state]
		if !known {
			writeError(w, http.StatusBadRequest, "invalid state; valid values: open, merged, closed")
			return
		}
		stateFilter = states
	}
	limit := projectPullRequestsDefaultLimit
	if v, err := strconv.Atoi(query.Get("limit")); err == nil && v > 0 {
		limit = min(v, projectPullRequestsMaxLimit)
	}
	offset := 0
	if v, err := strconv.Atoi(query.Get("offset")); err == nil && v > 0 {
		offset = v
	}

	ctx := r.Context()
	ws, err := h.Queries.GetWorkspace(ctx, project.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list pull requests")
		return
	}
	prefix := issuePrefixForWorkspace(ws)

	countRows, err := h.Queries.CountProjectPullRequestsByState(ctx, db.CountProjectPullRequestsByStateParams{
		ProjectID:   project.ID,
		WorkspaceID: project.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list pull requests")
		return
	}
	byState := make(map[string]int64, len(countRows))
	for _, row := range countRows {
		byState[row.State] = row.Total
	}
	counts := ProjectPullRequestCountsResponse{
		Open:   byState["open"] + byState["draft"],
		Merged: byState["merged"],
		Closed: byState["closed"],
	}
	var total int64
	if len(stateFilter) == 0 {
		total = counts.Open + counts.Merged + counts.Closed
	} else {
		for _, state := range stateFilter {
			total += byState[state]
		}
	}

	page, err := h.Queries.ListProjectPullRequestPage(ctx, db.ListProjectPullRequestPageParams{
		ProjectID:   project.ID,
		WorkspaceID: project.WorkspaceID,
		StateFilter: stateFilter,
		PageLimit:   int32(limit),
		PageOffset:  int32(offset),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list pull requests")
		return
	}

	githubIDs := []pgtype.UUID{}
	vcsIDs := []pgtype.UUID{}
	for _, row := range page {
		if row.Kind == "github" {
			githubIDs = append(githubIDs, row.ID)
		} else {
			vcsIDs = append(vcsIDs, row.ID)
		}
	}

	byID := make(map[string]GitHubPullRequestResponse, len(page))
	if len(githubIDs) > 0 {
		rows, err := h.Queries.ListProjectPullRequests(ctx, db.ListProjectPullRequestsParams{
			PrIds:       githubIDs,
			WorkspaceID: project.WorkspaceID,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list pull requests")
			return
		}
		for _, row := range rows {
			// Same column set as the issue PR list, so its serializer applies.
			byID[uuidToString(row.ID)] = issuePullRequestRowToResponse(db.ListPullRequestsByIssueRow(row), h.PRRefresh.Enabled())
			// Page-visit refresh, like the issue PR list: a missing or stale
			// snapshot is re-fetched asynchronously and arrives over realtime.
			h.PRRefresh.MaybeEnqueueOnView(
				row.InstallationID, row.RepoOwner, row.RepoName, row.PrNumber,
				row.SnapshotFetchedAt.Time,
				row.SnapshotFetchedAt.Valid &&
					row.SnapshotHeadSha != "" &&
					row.SnapshotHeadSha == row.HeadSha,
			)
		}
	}
	if len(vcsIDs) > 0 {
		rows, err := h.Queries.ListProjectVCSPullRequests(ctx, db.ListProjectVCSPullRequestsParams{
			PrIds:       vcsIDs,
			WorkspaceID: project.WorkspaceID,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list pull requests")
			return
		}
		for _, row := range rows {
			byID[uuidToString(row.ID)] = vcsPullRequestRowToResponse(db.ListVCSPullRequestsByIssueRow(row))
		}
	}

	issuesByPR := map[string][]ProjectPullRequestIssueResponse{}
	if len(page) > 0 {
		issueRows, err := h.Queries.ListProjectPullRequestIssues(ctx, db.ListProjectPullRequestIssuesParams{
			GithubPrIds: githubIDs,
			VcsPrIds:    vcsIDs,
			ProjectID:   project.ID,
			WorkspaceID: project.WorkspaceID,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list pull requests")
			return
		}
		for _, row := range issueRows {
			key := uuidToString(row.PrID)
			issuesByPR[key] = append(issuesByPR[key], ProjectPullRequestIssueResponse{
				ID:         uuidToString(row.IssueID),
				Identifier: fmt.Sprintf("%s-%d", prefix, row.Number),
				Title:      row.Title,
			})
		}
	}

	out := make([]ProjectPullRequestResponse, 0, len(page))
	for _, row := range page {
		id := uuidToString(row.ID)
		pr, found := byID[id]
		if !found {
			continue
		}
		issues := issuesByPR[id]
		if issues == nil {
			issues = []ProjectPullRequestIssueResponse{}
		}
		out = append(out, ProjectPullRequestResponse{GitHubPullRequestResponse: pr, Issues: issues})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"pull_requests": out,
		"total":         total,
		"counts":        counts,
	})
}
