package handler

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/testutil"
)

const progressChecklist = "Plan\n\n- [x] read the code\n- [ ] write the fix\n"

func progressCols(at time.Time, extra ...testutil.Cols) testutil.Cols {
	cols := testutil.Cols{
		"type":        "progress_update",
		"author_type": "agent",
		"author_id":   uuid.NewString(),
		"created_at":  at,
	}
	for _, e := range extra {
		for k, v := range e {
			cols[k] = v
		}
	}
	return cols
}

// latestProgressFor returns the single summary row's latest_progress. The
// issue always carries a plain member comment so a row exists even when no
// progress report qualifies.
func latestProgressFor(t *testing.T, issueID string) *LatestProgressResponse {
	t.Helper()
	got := listThreadSummaries(t, issueID)
	if len(got.Summaries) != 1 {
		t.Fatalf("summaries = %+v, want exactly one row", got.Summaries)
	}
	return got.Summaries[0].LatestProgress
}

func TestListIssueThreadSummaries_LatestProgress_NewestWins(t *testing.T) {
	issueID := dbfx.Issue(t, "progress newest")
	at := agentQuestionClock()
	dbfx.Comment(t, issueID, "- [ ] old item", progressCols(at(1)))
	newest := dbfx.Comment(t, issueID, progressChecklist, progressCols(at(3)))
	dbfx.Comment(t, issueID, "- [x] middle item", progressCols(at(2)))

	got := latestProgressFor(t, issueID)
	if got == nil {
		t.Fatal("latest_progress = nil, want the newest report")
	}
	if got.CommentID != newest || got.Content != progressChecklist {
		t.Errorf("latest_progress = %+v, want comment %s with its full body", got, newest)
	}
	created, err := time.Parse(time.RFC3339Nano, got.CreatedAt)
	if err != nil || !created.Equal(at(3)) {
		t.Errorf("created_at = %q (err %v), want %s", got.CreatedAt, err, at(3).Format(time.RFC3339Nano))
	}
}

func TestListIssueThreadSummaries_LatestProgress_NullWhenNone(t *testing.T) {
	issueID := dbfx.Issue(t, "progress none")
	dbfx.Comment(t, issueID, "just a note")
	if got := latestProgressFor(t, issueID); got != nil {
		t.Fatalf("latest_progress = %+v, want null", got)
	}
}

func TestListIssueThreadSummaries_LatestProgress_MemberAuthoredIgnored(t *testing.T) {
	issueID := dbfx.Issue(t, "progress member")
	dbfx.Comment(t, issueID, progressChecklist, testutil.Cols{"type": "progress_update"})
	if got := latestProgressFor(t, issueID); got != nil {
		t.Fatalf("latest_progress = %+v, want null for a member-authored report", got)
	}
}

func TestListIssueThreadSummaries_LatestProgress_DeletedIgnored(t *testing.T) {
	issueID := dbfx.Issue(t, "progress deleted")
	at := agentQuestionClock()
	older := dbfx.Comment(t, issueID, "- [ ] still open", progressCols(at(1)))
	dbfx.Comment(t, issueID, progressChecklist, progressCols(at(2), testutil.Cols{"deleted_at": at(3)}))

	got := latestProgressFor(t, issueID)
	if got == nil || got.CommentID != older {
		t.Fatalf("latest_progress = %+v, want the older report %s once the newest is deleted", got, older)
	}
}

func TestListIssueThreadSummaries_LatestProgress_PlainCommentTypeIgnored(t *testing.T) {
	issueID := dbfx.Issue(t, "progress plain type")
	dbfx.Comment(t, issueID, progressChecklist, testutil.Cols{
		"author_type": "agent", "author_id": uuid.NewString(),
	})
	if got := latestProgressFor(t, issueID); got != nil {
		t.Fatalf("latest_progress = %+v, want null for a type=comment checklist", got)
	}
}

func TestListIssueThreadSummaries_LatestProgress_RequiresTaskList(t *testing.T) {
	issueID := dbfx.Issue(t, "progress task list")
	at := agentQuestionClock()
	checklist := dbfx.Comment(t, issueID, "  * [X] nested and starred", progressCols(at(1)))
	dbfx.Comment(t, issueID, "Working on it, no checklist yet.\n- plain bullet", progressCols(at(2)))

	got := latestProgressFor(t, issueID)
	if got == nil || got.CommentID != checklist {
		t.Fatalf("latest_progress = %+v, want the checklist %s; a newer report without a task list must not shadow it", got, checklist)
	}
}

func TestListIssueThreadSummaries_LatestProgress_PerIssue(t *testing.T) {
	a := dbfx.Issue(t, "progress a")
	b := dbfx.Issue(t, "progress b")
	at := agentQuestionClock()
	progressA := dbfx.Comment(t, a, "- [ ] a", progressCols(at(1)))
	dbfx.Comment(t, b, "plain")

	got := listThreadSummaries(t, a, b)
	byIssue := map[string]*LatestProgressResponse{}
	for _, s := range got.Summaries {
		byIssue[s.IssueID] = s.LatestProgress
	}
	if byIssue[a] == nil || byIssue[a].CommentID != progressA {
		t.Errorf("issue a latest_progress = %+v, want %s", byIssue[a], progressA)
	}
	if byIssue[b] != nil {
		t.Errorf("issue b latest_progress = %+v, want null", byIssue[b])
	}
}
