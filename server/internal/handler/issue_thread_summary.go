package handler

import (
	"net/http"
	"regexp"
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
}

type LatestProgressResponse struct {
	CommentID string `json:"comment_id"`
	// Content is the full comment body; the client decides how many lines to
	// show.
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
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
// Only issues with at least one counted comment (not deleted, not
// platform-authored) get a row; callers treat a missing issue as zero.
// Workspace scope is enforced in the query, so an id from another workspace
// yields no row rather than an error.
//
// awaiting_me carries the caller's open questions from agents: unresolved
// threads whose latest comment is agent-authored and mentions the caller.
//
// latest_progress carries the newest agent progress report per issue, read
// from progress_update comments whose body is a Markdown task list.
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

	summaries := make([]IssueThreadSummaryResponse, len(rows))
	for i, row := range rows {
		issueID := uuidToString(row.IssueID)
		list := participants[issueID]
		if list == nil {
			list = []ThreadParticipantResponse{}
		}
		awaitingList := awaiting[issueID]
		if awaitingList == nil {
			awaitingList = []AwaitingMeResponse{}
		}
		summaries[i] = IssueThreadSummaryResponse{
			IssueID:         issueID,
			ReplyCount:      row.ReplyCount,
			LastReplyAt:     timestampToNanoPtr(row.LastReplyAt),
			OpenThreadCount: row.OpenThreadCount,
			Participants:    list,
			LastReadAt:      lastRead[issueID],
			AwaitingMe:      awaitingList,
			LatestProgress:  progress[issueID],
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"summaries": summaries})
}
