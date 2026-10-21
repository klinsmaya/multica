package handler

import (
	"context"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Cap on the number of issues summarised in one request. The project feed
// renders one page of issues at a time, so this is a guard against ANY()
// across a whole workspace rather than a page-size contract.
const issueThreadSummariesLimit = 100

// Participants shown per issue; the rest are implied by reply_count.
const issueThreadSummaryParticipantLimit = 3

// Agent questions listed per issue (newest first) and the excerpt length, in
// runes, of each.
const (
	issueAwaitingMeLimit        = 3
	issueAwaitingMeExcerptRunes = 140
)

// Linked PRs listed per issue on the output strip, newest first.
const issueThreadSummaryPullRequestLimit = 3

// Values of ThreadPullRequestResponse.ChecksState.
const (
	threadChecksPassed  = "passed"
	threadChecksFailed  = "failed"
	threadChecksPending = "pending"
	threadChecksNone    = "none"
)

type ThreadParticipantResponse struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type IssueThreadSummaryResponse struct {
	IssueID         string                      `json:"issue_id"`
	ReplyCount      int64                       `json:"reply_count"`
	LastReplyAt     *string                     `json:"last_reply_at"`
	OpenThreadCount int64                       `json:"open_thread_count"`
	Participants    []ThreadParticipantResponse `json:"participants"`
	// LastReadAt is the caller's own read cursor on the issue; null when the
	// caller never opened it.
	LastReadAt *string `json:"last_read_at"`
	// AwaitingMe lists unresolved threads whose latest comment is an agent
	// mentioning the caller, newest first, at most issueAwaitingMeLimit. Never
	// null.
	AwaitingMe []AwaitingMeResponse `json:"awaiting_me"`
	// LatestProgress is the newest agent progress report (a progress_update
	// comment holding a Markdown task list); null when the issue has none.
	LatestProgress *LatestProgressResponse `json:"latest_progress"`
	// PullRequests are the newest linked PRs (GitHub and self-hosted VCS),
	// newest first, at most issueThreadSummaryPullRequestLimit. Never null.
	PullRequests []ThreadPullRequestResponse `json:"pull_requests"`
	// DeliverableCount is the number of distinct files uploaded in live
	// comments; uploads of the same name and type are versions of one file.
	DeliverableCount int64 `json:"deliverable_count"`
	// LastDeliverableAt is the newest upload among those files, or null.
	LastDeliverableAt *string `json:"last_deliverable_at"`
}

type LatestProgressResponse struct {
	CommentID string `json:"comment_id"`
	// Content is the full comment body; the client decides how many lines to
	// show.
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

// ThreadPullRequestResponse is the slice of a linked PR the feed's output
// strip renders. ChecksState is one of passed, failed, pending or none; none
// covers both "no checks" and "no current CI snapshot", which the strip shows
// the same way.
type ThreadPullRequestResponse struct {
	ID          string `json:"id"`
	Number      int32  `json:"number"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	State       string `json:"state"`
	Additions   int32  `json:"additions"`
	Deletions   int32  `json:"deletions"`
	ChecksState string `json:"checks_state"`

	// createdAt orders the merged GitHub and VCS lists; never serialized.
	createdAt time.Time
}

type AwaitingMeResponse struct {
	CommentID string `json:"comment_id"`
	ActorID   string `json:"actor_id"`
	Excerpt   string `json:"excerpt"`
	CreatedAt string `json:"created_at"`
}

var whitespaceRunRe = regexp.MustCompile(`\s+`)

// commentExcerpt flattens comment Markdown into a one-line preview: mention
// links collapse to their label ("@Name"; issue links keep the bare identifier),
// whitespace runs to a single space, and the result is cut to maxRunes runes
// with an ellipsis.
func commentExcerpt(content string, maxRunes int) string {
	flat := util.MentionRe.ReplaceAllStringFunc(content, func(m string) string {
		sub := util.MentionRe.FindStringSubmatch(m)
		if sub[2] == "issue" {
			return sub[1]
		}
		return "@" + sub[1]
	})
	flat = strings.TrimSpace(whitespaceRunRe.ReplaceAllString(flat, " "))
	runes := []rune(flat)
	if len(runes) <= maxRunes {
		return flat
	}
	return strings.TrimSpace(string(runes[:maxRunes])) + "…"
}

// ListIssueThreadSummaries returns comment rollups for a batch of issues so a
// feed can render reply counts and avatars without one comment list per card.
//
// An issue gets a row when it has at least one counted comment (not deleted,
// not platform-authored), a linked PR, or a deliverable; callers treat a
// missing issue as zero.
// Workspace scope is enforced in the query, so an id from another workspace
// yields no row rather than an error.
//
// awaiting_me carries the caller's open questions from agents: unresolved
// threads whose latest comment is agent-authored and mentions the caller.
//
// latest_progress carries the newest agent progress report per issue, read
// from progress_update comments whose body is a Markdown task list.
//
// pull_requests and deliverable_count/last_deliverable_at feed the output
// strip: what the issue has produced, without opening it.
//
// Each row also carries the caller's read cursor (last_read_at, null when never
// opened) so the feed can tell unread cards from read ones; other users'
// cursors are never exposed.
func (h *Handler) ListIssueThreadSummaries(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	userUUID, ok := parseUUIDOrBadRequest(w, userID, "user_id")
	if !ok {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}

	empty := map[string]any{"summaries": []IssueThreadSummaryResponse{}}
	raw := r.URL.Query().Get("ids")
	if raw == "" {
		writeJSON(w, http.StatusOK, empty)
		return
	}

	parts := strings.Split(raw, ",")
	if len(parts) > issueThreadSummariesLimit {
		writeError(w, http.StatusBadRequest, "too many ids")
		return
	}
	issueIDs := make([]pgtype.UUID, 0, len(parts))
	for _, s := range parts {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		id, ok := parseUUIDOrBadRequest(w, s, "ids")
		if !ok {
			return
		}
		issueIDs = append(issueIDs, id)
	}
	if len(issueIDs) == 0 {
		writeJSON(w, http.StatusOK, empty)
		return
	}

	rows, err := h.Queries.ListIssueThreadSummaries(r.Context(), db.ListIssueThreadSummariesParams{
		WorkspaceID: wsUUID,
		IssueIds:    issueIDs,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list thread summaries")
		return
	}
	participantRows, err := h.Queries.ListIssueThreadParticipants(r.Context(), db.ListIssueThreadParticipantsParams{
		WorkspaceID: wsUUID,
		IssueIds:    issueIDs,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list thread participants")
		return
	}

	cursorRows, err := h.Queries.ListIssueReadCursors(r.Context(), db.ListIssueReadCursorsParams{
		UserID:      userUUID,
		WorkspaceID: wsUUID,
		IssueIds:    issueIDs,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list read cursors")
		return
	}
	lastRead := make(map[string]*string, len(cursorRows))
	for _, c := range cursorRows {
		lastRead[uuidToString(c.IssueID)] = timestampToNanoPtr(c.LastReadAt)
	}

	questionRows, err := h.Queries.ListAgentQuestionsForIssues(r.Context(), db.ListAgentQuestionsForIssuesParams{
		WorkspaceID: wsUUID,
		IssueIds:    issueIDs,
		CallerID:    userUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list agent questions")
		return
	}
	// Rows arrive newest first per issue; keep the first few.
	awaiting := make(map[string][]AwaitingMeResponse)
	for _, q := range questionRows {
		issueID := uuidToString(q.IssueID)
		if len(awaiting[issueID]) >= issueAwaitingMeLimit {
			continue
		}
		awaiting[issueID] = append(awaiting[issueID], AwaitingMeResponse{
			CommentID: uuidToString(q.CommentID),
			ActorID:   uuidToString(q.AuthorID),
			Excerpt:   commentExcerpt(q.Content, issueAwaitingMeExcerptRunes),
			CreatedAt: q.CreatedAt.Time.Format(time.RFC3339Nano),
		})
	}

	pullRequests, err := h.listThreadPullRequests(r.Context(), wsUUID, issueIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list pull requests")
		return
	}
	deliverableRows, err := h.Queries.ListIssueDeliverableSummaries(r.Context(), db.ListIssueDeliverableSummariesParams{
		WorkspaceID: wsUUID,
		IssueIds:    issueIDs,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list deliverables")
		return
	}
	deliverables := make(map[string]db.ListIssueDeliverableSummariesRow, len(deliverableRows))
	for _, d := range deliverableRows {
		deliverables[uuidToString(d.IssueID)] = d
	}

	progressRows, err := h.Queries.ListLatestProgressCommentsForIssues(r.Context(), db.ListLatestProgressCommentsForIssuesParams{
		WorkspaceID: wsUUID,
		IssueIds:    issueIDs,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list progress reports")
		return
	}
	progress := make(map[string]*LatestProgressResponse, len(progressRows))
	for _, p := range progressRows {
		progress[uuidToString(p.IssueID)] = &LatestProgressResponse{
			CommentID: uuidToString(p.CommentID),
			Content:   p.Content,
			CreatedAt: p.CreatedAt.Time.Format(time.RFC3339Nano),
		}
	}

	// Rows arrive ordered by issue then most recent activity, so the first
	// few per issue are the participants to show.
	participants := make(map[string][]ThreadParticipantResponse, len(rows))
	for _, p := range participantRows {
		issueID := uuidToString(p.IssueID)
		if len(participants[issueID]) >= issueThreadSummaryParticipantLimit {
			continue
		}
		participants[issueID] = append(participants[issueID], ThreadParticipantResponse{
			Type: p.AuthorType,
			ID:   uuidToString(p.AuthorID),
		})
	}

	build := func(issueID string, row *db.ListIssueThreadSummariesRow) IssueThreadSummaryResponse {
		list := participants[issueID]
		if list == nil {
			list = []ThreadParticipantResponse{}
		}
		awaitingList := awaiting[issueID]
		if awaitingList == nil {
			awaitingList = []AwaitingMeResponse{}
		}
		prs := pullRequests[issueID]
		if prs == nil {
			prs = []ThreadPullRequestResponse{}
		}
		summary := IssueThreadSummaryResponse{
			IssueID:        issueID,
			Participants:   list,
			LastReadAt:     lastRead[issueID],
			AwaitingMe:     awaitingList,
			PullRequests:   prs,
			LatestProgress: progress[issueID],
		}
		if row != nil {
			summary.ReplyCount = row.ReplyCount
			summary.LastReplyAt = timestampToNanoPtr(row.LastReplyAt)
			summary.OpenThreadCount = row.OpenThreadCount
		}
		if d, ok := deliverables[issueID]; ok {
			summary.DeliverableCount = d.DeliverableCount
			summary.LastDeliverableAt = timestampToNanoPtr(d.LastDeliverableAt)
		}
		return summary
	}

	summaries := make([]IssueThreadSummaryResponse, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for i := range rows {
		issueID := uuidToString(rows[i].IssueID)
		seen[issueID] = true
		summaries = append(summaries, build(issueID, &rows[i]))
	}
	// Issues with output but no counted comment (a PR linked before anyone
	// replied) still need a row; they follow the commented ones in request
	// order.
	for _, id := range issueIDs {
		issueID := uuidToString(id)
		if seen[issueID] {
			continue
		}
		if _, hasFiles := deliverables[issueID]; !hasFiles && len(pullRequests[issueID]) == 0 {
			continue
		}
		seen[issueID] = true
		summaries = append(summaries, build(issueID, nil))
	}
	writeJSON(w, http.StatusOK, map[string]any{"summaries": summaries})
}

// listThreadPullRequests returns each issue's newest linked PRs across GitHub
// and the self-hosted providers, newest first, capped per issue. Each source
// is queried once for the whole batch and already capped per issue, so the
// merge sees at most the cap per issue per source.
func (h *Handler) listThreadPullRequests(ctx context.Context, wsUUID pgtype.UUID, issueIDs []pgtype.UUID) (map[string][]ThreadPullRequestResponse, error) {
	ghRows, err := h.Queries.ListIssueThreadPullRequests(ctx, db.ListIssueThreadPullRequestsParams{
		WorkspaceID: wsUUID,
		IssueIds:    issueIDs,
		PrLimit:     issueThreadSummaryPullRequestLimit,
	})
	if err != nil {
		return nil, err
	}
	vcsRows, err := h.Queries.ListIssueThreadVCSPullRequests(ctx, db.ListIssueThreadVCSPullRequestsParams{
		WorkspaceID: wsUUID,
		IssueIds:    issueIDs,
		PrLimit:     issueThreadSummaryPullRequestLimit,
	})
	if err != nil {
		return nil, err
	}

	snapshotsEnabled := h.PRRefresh.Enabled()
	out := make(map[string][]ThreadPullRequestResponse)
	for _, p := range ghRows {
		available := currentGitHubSnapshotAvailable(snapshotsEnabled, p.HeadSha, p.SnapshotHeadSha, p.SnapshotFetchedAt)
		issueID := uuidToString(p.IssueID)
		out[issueID] = append(out[issueID], ThreadPullRequestResponse{
			ID:          uuidToString(p.ID),
			Number:      p.PrNumber,
			Title:       p.Title,
			URL:         p.HtmlUrl,
			State:       p.State,
			Additions:   p.Additions,
			Deletions:   p.Deletions,
			ChecksState: githubChecksState(available, p.ChecksRollupState, p.ChecksFailed, p.ChecksRunning, p.ChecksPassed),
			createdAt:   p.PrCreatedAt.Time,
		})
	}
	for _, p := range vcsRows {
		issueID := uuidToString(p.IssueID)
		out[issueID] = append(out[issueID], ThreadPullRequestResponse{
			ID:          uuidToString(p.ID),
			Number:      p.PrNumber,
			Title:       p.Title,
			URL:         p.HtmlUrl,
			State:       p.State,
			Additions:   p.Additions,
			Deletions:   p.Deletions,
			ChecksState: checksStateFromConclusion(aggregateChecksConclusion(p.ChecksFailed, p.ChecksPassed, p.ChecksPending, p.ChecksTotal)),
			createdAt:   p.PrCreatedAt.Time,
		})
	}
	for issueID, list := range out {
		sort.SliceStable(list, func(i, j int) bool {
			if !list[i].createdAt.Equal(list[j].createdAt) {
				return list[i].createdAt.After(list[j].createdAt)
			}
			return list[i].ID > list[j].ID
		})
		if len(list) > issueThreadSummaryPullRequestLimit {
			list = list[:issueThreadSummaryPullRequestLimit]
		}
		out[issueID] = list
	}
	return out, nil
}

// githubChecksState folds a GitHub PR's CI snapshot into the strip's enum the
// way the PR list does (deriveChecksStatus in
// packages/core/github/pull-request-status.ts): nothing is claimed without a
// current snapshot, a failed run or a failing rollup wins, then pending, then
// passed.
func githubChecksState(snapshotAvailable bool, rollup pgtype.Text, failed, running, passed int64) string {
	if !snapshotAvailable {
		return threadChecksNone
	}
	if failed > 0 {
		return threadChecksFailed
	}
	return checksStateFromConclusion(rollupToConclusion(rollup, failed, running, passed))
}

// checksStateFromConclusion maps the coarse failed/pending/passed conclusion
// shared by the PR list responses onto the strip's enum; nil is "none".
func checksStateFromConclusion(conclusion *string) string {
	if conclusion == nil {
		return threadChecksNone
	}
	switch *conclusion {
	case "failed":
		return threadChecksFailed
	case "pending":
		return threadChecksPending
	case "passed":
		return threadChecksPassed
	}
	return threadChecksNone
}
