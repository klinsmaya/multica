package handler

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func summaryFor(t *testing.T, body threadSummariesBody, issueID string) IssueThreadSummaryResponse {
	t.Helper()
	for _, s := range body.Summaries {
		if s.IssueID == issueID {
			return s
		}
	}
	t.Fatalf("no summary row for issue %s in %+v", issueID, body.Summaries)
	return IssueThreadSummaryResponse{}
}

func TestListIssueThreadSummaries_ChildrenNewestFirstCappedWithTotal(t *testing.T) {
	setWorkspaceIssuePrefixForTest(t, "THC")
	parent := dbfx.Issue(t, "thread children parent")
	base := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)

	// Seven children, created one minute apart; only the newest five are
	// listed, the total still counts all seven.
	childIDs := make([]string, 7)
	for i := range childIDs {
		childIDs[i] = dbfx.Issue(t, fmt.Sprintf("child %d", i), testutil.Cols{
			"parent_issue_id": parent,
			"created_at":      base.Add(time.Duration(i) * time.Minute),
		})
	}
	// A grandchild is not a direct child and must not count.
	dbfx.Issue(t, "grandchild", testutil.Cols{"parent_issue_id": childIDs[0]})

	got := summaryFor(t, listThreadSummaries(t, parent), parent)
	if got.ChildTotal != 7 {
		t.Errorf("child_total = %d, want 7", got.ChildTotal)
	}
	if len(got.Children) != 5 {
		t.Fatalf("children = %d, want 5", len(got.Children))
	}
	for i, c := range got.Children {
		want := childIDs[6-i]
		if c.ID != want {
			t.Errorf("children[%d] = %s, want %s (newest first)", i, c.ID, want)
		}
	}
	var number int
	dbfx.QueryRow(t, `SELECT number FROM issue WHERE id = $1`, childIDs[6]).Scan(&number)
	if want := fmt.Sprintf("THC-%d", number); got.Children[0].Identifier != want {
		t.Errorf("identifier = %q, want %q", got.Children[0].Identifier, want)
	}
	if got.Children[0].Title != "child 6" || got.Children[0].Status != "todo" {
		t.Errorf("children[0] = %+v, want title %q and status todo", got.Children[0], "child 6")
	}
}

func TestListIssueThreadSummaries_ChildrenWithoutCommentsStillGetARow(t *testing.T) {
	parent := dbfx.Issue(t, "children only parent")
	child := dbfx.Issue(t, "only child", testutil.Cols{"parent_issue_id": parent})
	bare := dbfx.Issue(t, "no comments no children")

	body := listThreadSummaries(t, bare, parent)
	if len(body.Summaries) != 1 {
		t.Fatalf("summaries = %+v, want only the parent's row", body.Summaries)
	}
	s := body.Summaries[0]
	if s.IssueID != parent {
		t.Fatalf("issue_id = %s, want %s", s.IssueID, parent)
	}
	if s.ReplyCount != 0 || s.LastReplyAt != nil {
		t.Errorf("reply_count/last_reply_at = %d/%v, want 0/nil", s.ReplyCount, s.LastReplyAt)
	}
	if s.ChildTotal != 1 || len(s.Children) != 1 || s.Children[0].ID != child {
		t.Errorf("children = %+v total %d, want just %s", s.Children, s.ChildTotal, child)
	}
}

func TestListIssueThreadSummaries_ChildrenCarryCountedReplyRollup(t *testing.T) {
	parent := dbfx.Issue(t, "rollup parent")
	agentID := uuid.NewString()
	quiet := dbfx.Issue(t, "quiet child", testutil.Cols{"parent_issue_id": parent})
	busy := dbfx.Issue(t, "busy child", testutil.Cols{
		"parent_issue_id": parent,
		"assignee_type":   "agent",
		"assignee_id":     agentID,
		"status":          "in_progress",
	})
	base := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	at := func(minutes int) time.Time { return base.Add(time.Duration(minutes) * time.Minute) }

	dbfx.Comment(t, busy, "first", testutil.Cols{"created_at": at(1)})
	dbfx.Comment(t, busy, "second", testutil.Cols{"created_at": at(2)})
	// Neither a platform notice nor a deleted comment is conversation.
	dbfx.Comment(t, busy, "notice", testutil.Cols{"author_type": "system", "created_at": at(8)})
	dbfx.Comment(t, busy, "deleted", testutil.Cols{"created_at": at(9), "deleted_at": at(10)})
	// A comment on the parent must not leak into the child's rollup.
	dbfx.Comment(t, parent, "parent comment", testutil.Cols{"created_at": at(20)})

	got := summaryFor(t, listThreadSummaries(t, parent), parent)
	byID := map[string]ThreadChildResponse{}
	for _, c := range got.Children {
		byID[c.ID] = c
	}
	if len(byID) != 2 {
		t.Fatalf("children = %+v, want the two children", got.Children)
	}

	b := byID[busy]
	if b.ReplyCount != 2 {
		t.Errorf("busy reply_count = %d, want 2 (system and deleted excluded)", b.ReplyCount)
	}
	if b.LastReplyAt == nil {
		t.Fatalf("busy last_reply_at = nil, want %s", at(2).Format(time.RFC3339Nano))
	}
	if last, err := time.Parse(time.RFC3339Nano, *b.LastReplyAt); err != nil || !last.Equal(at(2)) {
		t.Errorf("busy last_reply_at = %q (err %v), want %s", *b.LastReplyAt, err, at(2).Format(time.RFC3339Nano))
	}
	if b.Status != "in_progress" {
		t.Errorf("busy status = %q, want in_progress", b.Status)
	}
	if b.AssigneeType == nil || *b.AssigneeType != "agent" || b.AssigneeID == nil || *b.AssigneeID != agentID {
		t.Errorf("busy assignee = %v/%v, want agent/%s", b.AssigneeType, b.AssigneeID, agentID)
	}

	q := byID[quiet]
	if q.ReplyCount != 0 || q.LastReplyAt != nil {
		t.Errorf("quiet reply_count/last_reply_at = %d/%v, want 0/nil", q.ReplyCount, q.LastReplyAt)
	}
	if q.AssigneeType != nil || q.AssigneeID != nil {
		t.Errorf("quiet assignee = %v/%v, want nil/nil", q.AssigneeType, q.AssigneeID)
	}
}

func TestListIssueThreadSummaries_ChildrenAreScopedPerParentAndWorkspace(t *testing.T) {
	first := dbfx.Issue(t, "first parent")
	second := dbfx.Issue(t, "second parent")
	firstChild := dbfx.Issue(t, "first child", testutil.Cols{"parent_issue_id": first})
	secondChild := dbfx.Issue(t, "second child", testutil.Cols{"parent_issue_id": second})

	// A child that points at the parent but lives in another workspace is
	// not this workspace's to list.
	otherWS := dbfx.Workspace(t, "Other children ws", "other-children-"+uuid.NewString()[:8])
	dbfx.Issue(t, "foreign child", testutil.Cols{"workspace_id": otherWS, "parent_issue_id": first})

	body := listThreadSummaries(t, first, second)
	a := summaryFor(t, body, first)
	b := summaryFor(t, body, second)
	if a.ChildTotal != 1 || len(a.Children) != 1 || a.Children[0].ID != firstChild {
		t.Errorf("first = %+v total %d, want only %s", a.Children, a.ChildTotal, firstChild)
	}
	if b.ChildTotal != 1 || len(b.Children) != 1 || b.Children[0].ID != secondChild {
		t.Errorf("second = %+v total %d, want only %s", b.Children, b.ChildTotal, secondChild)
	}
}
