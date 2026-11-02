package handler

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// MarkIssueRead sets the caller's read cursor on an issue to the current
// database time. The cursor only ever moves forward (see UpsertIssueReadCursor),
// so a retried or out-of-order request is harmless. It carries no body and no
// payload: the cursor is private to the caller, so nothing is broadcast.
func (h *Handler) MarkIssueRead(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	userUUID, ok := parseUUIDOrBadRequest(w, userID, "user_id")
	if !ok {
		return
	}

	if err := h.Queries.UpsertIssueReadCursor(r.Context(), db.UpsertIssueReadCursorParams{
		UserID:      userUUID,
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		slog.Warn("mark issue read failed", "error", err, "issue_id", uuidToString(issue.ID))
		writeError(w, http.StatusInternalServerError, "failed to mark issue read")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
