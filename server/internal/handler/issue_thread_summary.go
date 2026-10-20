package handler

import (
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Cap on the number of issues summarised in one request. The project feed
// renders one page of issues at a time, so this is a guard against ANY()
// across a whole workspace rather than a page-size contract.
const issueThreadSummariesLimit = 100

// Participants shown per issue; the rest are implied by reply_count.
const issueThreadSummaryParticipantLimit = 3

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
}

// ListIssueThreadSummaries returns comment rollups for a batch of issues so a
// feed can render reply counts and avatars without one comment list per card.
//
// Only issues with at least one counted comment (not deleted, not
// platform-authored) get a row; callers treat a missing issue as zero.
// Workspace scope is enforced in the query, so an id from another workspace
// yields no row rather than an error.
func (h *Handler) ListIssueThreadSummaries(w http.ResponseWriter, r *http.Request) {
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
		summaries[i] = IssueThreadSummaryResponse{
			IssueID:         issueID,
			ReplyCount:      row.ReplyCount,
			LastReplyAt:     timestampToPtr(row.LastReplyAt),
			OpenThreadCount: row.OpenThreadCount,
			Participants:    list,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"summaries": summaries})
}
