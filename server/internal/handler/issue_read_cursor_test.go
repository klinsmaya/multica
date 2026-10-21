package handler

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func markIssueRead(t *testing.T, userID, issueRef string) *testutil.Response {
	t.Helper()
	req := withURLParam(newRequestAs(userID, http.MethodPut, "/api/issues/"+issueRef+"/read", nil), "id", issueRef)
	return testutil.Call(t, testHandler.MarkIssueRead, req)
}

func threadSummariesAs(t *testing.T, userID string, issueIDs ...string) map[string]IssueThreadSummaryResponse {
	t.Helper()
	ids := ""
	for i, id := range issueIDs {
		if i > 0 {
			ids += ","
		}
		ids += id
	}
	var out threadSummariesBody
	req := newRequestAs(userID, http.MethodGet, "/api/issues/thread-summaries?ids="+ids, nil)
	testutil.Call(t, testHandler.ListIssueThreadSummaries, req).Want(http.StatusOK).JSON(&out)
	byIssue := make(map[string]IssueThreadSummaryResponse, len(out.Summaries))
	for _, s := range out.Summaries {
		byIssue[s.IssueID] = s
	}
	return byIssue
}

func tsz(ts time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: ts, Valid: true} }

func readCursorAt(t *testing.T, userID, workspaceID, issueID string) (time.Time, bool) {
	t.Helper()
	rows, err := testHandler.Queries.ListIssueReadCursors(context.Background(), db.ListIssueReadCursorsParams{
		UserID:      parseUUID(userID),
		WorkspaceID: parseUUID(workspaceID),
		IssueIds:    []pgtype.UUID{parseUUID(issueID)},
	})
	if err != nil {
		t.Fatalf("list read cursors: %v", err)
	}
	if len(rows) == 0 {
		return time.Time{}, false
	}
	return rows[0].LastReadAt.Time, true
}

func cleanupReadCursors(t *testing.T, issueID string) {
	t.Helper()
	dbfx.Cleanup(t, `DELETE FROM issue_read_cursor WHERE issue_id = $1`, issueID)
}

func TestMarkIssueRead_ReturnsNoContentAndSummaryCarriesLastReadAt(t *testing.T) {
	issueID := dbfx.Issue(t, "read cursor summary")
	cleanupReadCursors(t, issueID)
	dbfx.Comment(t, issueID, "hello")

	before := threadSummariesAs(t, testUserID, issueID)[issueID]
	if before.LastReadAt != nil {
		t.Fatalf("last_read_at before reading = %q, want null", *before.LastReadAt)
	}

	start := time.Now().Add(-time.Minute)
	markIssueRead(t, testUserID, issueID).Want(http.StatusNoContent)

	after := threadSummariesAs(t, testUserID, issueID)[issueID]
	if after.LastReadAt == nil {
		t.Fatal("last_read_at after reading = null, want a timestamp")
	}
	got, err := time.Parse(time.RFC3339Nano, *after.LastReadAt)
	if err != nil {
		t.Fatalf("last_read_at %q is not RFC3339Nano: %v", *after.LastReadAt, err)
	}
	if got.Before(start) || got.After(time.Now().Add(time.Minute)) {
		t.Errorf("last_read_at = %s, want about now", got)
	}
	if after.ReplyCount != before.ReplyCount || after.OpenThreadCount != before.OpenThreadCount {
		t.Errorf("summary counts changed by reading: before %+v, after %+v", before, after)
	}
}

func TestMarkIssueRead_ResolvesIdentifier(t *testing.T) {
	setWorkspaceIssuePrefixForTest(t, "RCT")
	issueID := dbfx.Issue(t, "read cursor identifier")
	cleanupReadCursors(t, issueID)

	var number int32
	dbfx.QueryRow(t, `SELECT number FROM issue WHERE id = $1`, issueID).Scan(&number)

	markIssueRead(t, testUserID, fmt.Sprintf("RCT-%d", number)).Want(http.StatusNoContent)
	if _, ok := readCursorAt(t, testUserID, testWorkspaceID, issueID); !ok {
		t.Fatal("cursor not written under the resolved issue id")
	}
}

func TestMarkIssueRead_OtherWorkspaceIssueIsNotFound(t *testing.T) {
	otherWorkspaceID := dbfx.Workspace(t, "Read cursor other", "read-cursor-"+uuid.NewString())
	foreign := dbfx.Issue(t, "foreign issue", testutil.Cols{"workspace_id": otherWorkspaceID})
	cleanupReadCursors(t, foreign)

	markIssueRead(t, testUserID, foreign).Want(http.StatusNotFound)

	if n := dbfx.Count(t, `SELECT count(*) FROM issue_read_cursor WHERE issue_id = $1`, foreign); n != 0 {
		t.Errorf("cursor rows for foreign issue = %d, want 0", n)
	}
}

func TestMarkIssueRead_UnknownIssueIsNotFound(t *testing.T) {
	markIssueRead(t, testUserID, uuid.NewString()).Want(http.StatusNotFound)
	markIssueRead(t, testUserID, "not-an-issue").Want(http.StatusNotFound)
}

func TestMarkIssueRead_CursorsAreIndependentPerUser(t *testing.T) {
	issueID := dbfx.Issue(t, "read cursor per user")
	cleanupReadCursors(t, issueID)
	dbfx.Comment(t, issueID, "hello")

	otherUserID := dbfx.User(t, "Cursor Reader", "cursor-reader-"+uuid.NewString()+"@example.test")
	dbfx.Member(t, testWorkspaceID, otherUserID, "member")

	markIssueRead(t, otherUserID, issueID).Want(http.StatusNoContent)

	if got := threadSummariesAs(t, testUserID, issueID)[issueID]; got.LastReadAt != nil {
		t.Errorf("caller sees last_read_at %q after only another user read, want null", *got.LastReadAt)
	}
	if got := threadSummariesAs(t, otherUserID, issueID)[issueID]; got.LastReadAt == nil {
		t.Error("reader's own last_read_at = null, want a timestamp")
	}
}

func TestUpsertIssueReadCursor_IsMonotonic(t *testing.T) {
	issueID := dbfx.Issue(t, "read cursor monotonic")
	cleanupReadCursors(t, issueID)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	upsert := func(ts time.Time) {
		t.Helper()
		if err := testHandler.Queries.UpsertIssueReadCursor(ctx, db.UpsertIssueReadCursorParams{
			UserID:      parseUUID(testUserID),
			IssueID:     parseUUID(issueID),
			WorkspaceID: parseUUID(testWorkspaceID),
			LastReadAt:  tsz(ts),
		}); err != nil {
			t.Fatalf("upsert read cursor: %v", err)
		}
	}
	want := func(label string, ts time.Time) {
		t.Helper()
		got, ok := readCursorAt(t, testUserID, testWorkspaceID, issueID)
		if !ok || !got.Equal(ts) {
			t.Errorf("%s: cursor = %s (present %v), want %s", label, got, ok, ts)
		}
	}

	upsert(base.Add(10 * time.Minute))
	want("first write", base.Add(10*time.Minute))

	upsert(base.Add(5 * time.Minute))
	want("older timestamp must not move the cursor back", base.Add(10*time.Minute))

	upsert(base.Add(20 * time.Minute))
	want("newer timestamp advances the cursor", base.Add(20*time.Minute))

	if n := dbfx.Count(t, `SELECT count(*) FROM issue_read_cursor WHERE issue_id = $1 AND user_id = $2`, issueID, testUserID); n != 1 {
		t.Errorf("cursor rows = %d, want 1 (upsert, not insert)", n)
	}
}

func TestDeleteIssue_RemovesReadCursors(t *testing.T) {
	issueID := dbfx.Issue(t, "read cursor deleted with issue")
	cleanupReadCursors(t, issueID)
	otherUserID := dbfx.User(t, "Cursor Other", "cursor-other-"+uuid.NewString()+"@example.test")
	dbfx.Member(t, testWorkspaceID, otherUserID, "member")

	markIssueRead(t, testUserID, issueID).Want(http.StatusNoContent)
	markIssueRead(t, otherUserID, issueID).Want(http.StatusNoContent)
	if n := dbfx.Count(t, `SELECT count(*) FROM issue_read_cursor WHERE issue_id = $1`, issueID); n != 2 {
		t.Fatalf("cursor rows before delete = %d, want 2", n)
	}

	req := withURLParam(newRequest(http.MethodDelete, "/api/issues/"+issueID, nil), "id", issueID)
	testutil.Call(t, testHandler.DeleteIssue, req).Want(http.StatusNoContent)

	if n := dbfx.Count(t, `SELECT count(*) FROM issue_read_cursor WHERE issue_id = $1`, issueID); n != 0 {
		t.Errorf("cursor rows after issue delete = %d, want 0", n)
	}
}

func TestIssueReadCursorBulkDeletes(t *testing.T) {
	ctx := context.Background()
	otherWorkspaceID := dbfx.Workspace(t, "Read cursor bulk", "read-cursor-bulk-"+uuid.NewString())
	otherUserID := dbfx.User(t, "Cursor Bulk", "cursor-bulk-"+uuid.NewString()+"@example.test")
	inHere := dbfx.Issue(t, "bulk here")
	inThere := dbfx.Issue(t, "bulk there", testutil.Cols{"workspace_id": otherWorkspaceID})
	cleanupReadCursors(t, inHere)
	cleanupReadCursors(t, inThere)

	now := time.Now().UTC()
	put := func(userID, workspaceID, issueID string) {
		t.Helper()
		if err := testHandler.Queries.UpsertIssueReadCursor(ctx, db.UpsertIssueReadCursorParams{
			UserID: parseUUID(userID), IssueID: parseUUID(issueID), WorkspaceID: parseUUID(workspaceID), LastReadAt: tsz(now),
		}); err != nil {
			t.Fatalf("upsert read cursor: %v", err)
		}
	}
	put(testUserID, testWorkspaceID, inHere)
	put(otherUserID, testWorkspaceID, inHere)
	put(testUserID, otherWorkspaceID, inThere)

	// Member removal: only that user's cursors in that workspace go.
	if err := testHandler.Queries.DeleteIssueReadCursorsByUserWorkspace(ctx, db.DeleteIssueReadCursorsByUserWorkspaceParams{
		UserID: parseUUID(otherUserID), WorkspaceID: parseUUID(testWorkspaceID),
	}); err != nil {
		t.Fatalf("delete by user workspace: %v", err)
	}
	if _, ok := readCursorAt(t, otherUserID, testWorkspaceID, inHere); ok {
		t.Error("removed member's cursor survived")
	}
	if _, ok := readCursorAt(t, testUserID, testWorkspaceID, inHere); !ok {
		t.Error("another member's cursor was deleted with the removed member's")
	}
	if _, ok := readCursorAt(t, testUserID, otherWorkspaceID, inThere); !ok {
		t.Error("cursor in another workspace was deleted by member removal here")
	}

	// Workspace teardown: every cursor in that workspace, none elsewhere.
	if err := testHandler.Queries.DeleteIssueReadCursorsForWorkspace(ctx, parseUUID(otherWorkspaceID)); err != nil {
		t.Fatalf("delete for workspace: %v", err)
	}
	if _, ok := readCursorAt(t, testUserID, otherWorkspaceID, inThere); ok {
		t.Error("workspace delete left a cursor behind")
	}
	if _, ok := readCursorAt(t, testUserID, testWorkspaceID, inHere); !ok {
		t.Error("workspace delete removed a cursor from another workspace")
	}
}
