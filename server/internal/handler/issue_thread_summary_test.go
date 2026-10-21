package handler

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/testutil"
)

type threadSummariesBody struct {
	Summaries []IssueThreadSummaryResponse `json:"summaries"`
}

func listThreadSummaries(t *testing.T, ids ...string) threadSummariesBody {
	t.Helper()
	var out threadSummariesBody
	req := newRequest(http.MethodGet, "/api/issues/thread-summaries?ids="+strings.Join(ids, ","), nil)
	testutil.Call(t, testHandler.ListIssueThreadSummaries, req).Want(http.StatusOK).JSON(&out)
	return out
}

func TestListIssueThreadSummaries_CountsOpenThreadsAndOrdersParticipants(t *testing.T) {
	issueID := dbfx.Issue(t, "thread summary counts")
	base := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	at := func(minutes int) time.Time { return base.Add(time.Duration(minutes) * time.Minute) }

	member1 := testUserID
	member2 := uuid.NewString()
	agent1 := uuid.NewString()
	agent2 := uuid.NewString()

	resolvedRoot := dbfx.Comment(t, issueID, "resolved root", testutil.Cols{
		"created_at":       at(1),
		"resolved_at":      at(2),
		"resolved_by_type": "member",
		"resolved_by_id":   member1,
	})
	openRoot := dbfx.Comment(t, issueID, "open root", testutil.Cols{
		"author_type": "agent", "author_id": agent1, "created_at": at(2),
	})
	dbfx.Comment(t, issueID, "reply 1", testutil.Cols{
		"author_type": "agent", "author_id": agent2, "parent_id": resolvedRoot, "created_at": at(3),
	})
	dbfx.Comment(t, issueID, "reply 2", testutil.Cols{
		"author_id": member2, "parent_id": openRoot, "created_at": at(4),
	})
	dbfx.Comment(t, issueID, "reply 3", testutil.Cols{
		"parent_id": openRoot, "created_at": at(5),
	})
	// Newer than everything above, but deleted: must change neither the count,
	// last_reply_at, nor agent1's place in the participant order.
	dbfx.Comment(t, issueID, "deleted reply", testutil.Cols{
		"author_type": "agent", "author_id": agent1, "parent_id": openRoot,
		"created_at": at(10), "deleted_at": at(11),
	})

	got := listThreadSummaries(t, issueID)
	if len(got.Summaries) != 1 {
		t.Fatalf("summaries = %+v, want exactly one row", got.Summaries)
	}
	s := got.Summaries[0]
	if s.IssueID != issueID {
		t.Errorf("issue_id = %s, want %s", s.IssueID, issueID)
	}
	if s.ReplyCount != 5 {
		t.Errorf("reply_count = %d, want 5 (2 roots + 3 replies, deleted excluded)", s.ReplyCount)
	}
	if s.OpenThreadCount != 1 {
		t.Errorf("open_thread_count = %d, want 1 (one root resolved)", s.OpenThreadCount)
	}
	if s.LastReplyAt == nil {
		t.Fatalf("last_reply_at = nil, want %s", at(5).Format(time.RFC3339Nano))
	}
	last, err := time.Parse(time.RFC3339Nano, *s.LastReplyAt)
	if err != nil || !last.Equal(at(5)) {
		t.Errorf("last_reply_at = %q (err %v), want %s", *s.LastReplyAt, err, at(5).Format(time.RFC3339Nano))
	}

	want := []ThreadParticipantResponse{
		{Type: "member", ID: member1},
		{Type: "member", ID: member2},
		{Type: "agent", ID: agent2},
	}
	if len(s.Participants) != len(want) {
		t.Fatalf("participants = %+v, want %+v", s.Participants, want)
	}
	for i := range want {
		if s.Participants[i] != want[i] {
			t.Errorf("participants[%d] = %+v, want %+v (all: %+v)", i, s.Participants[i], want[i], s.Participants)
		}
	}
}

func TestListIssueThreadSummaries_SystemCommentsAreNotCounted(t *testing.T) {
	onlySystem := dbfx.Issue(t, "thread summary system only")
	dbfx.Comment(t, onlySystem, "child done", testutil.Cols{
		"author_type": "system", "author_id": uuid.Nil.String(), "type": "system",
	})

	mixed := dbfx.Issue(t, "thread summary system mixed")
	dbfx.Comment(t, mixed, "human")
	dbfx.Comment(t, mixed, "child done", testutil.Cols{
		"author_type": "system", "author_id": uuid.Nil.String(), "type": "system",
		"created_at": time.Now().UTC().Add(time.Minute),
	})

	got := listThreadSummaries(t, onlySystem, mixed)
	if len(got.Summaries) != 1 || got.Summaries[0].IssueID != mixed {
		t.Fatalf("summaries = %+v, want only the mixed issue", got.Summaries)
	}
	s := got.Summaries[0]
	if s.ReplyCount != 1 || len(s.Participants) != 1 || s.Participants[0].Type != "member" {
		t.Errorf("mixed summary = %+v, want 1 counted comment and the member as sole participant", s)
	}
}

func TestListIssueThreadSummaries_LastReplyExcerptIsNewestCountedComment(t *testing.T) {
	issueID := dbfx.Issue(t, "thread summary excerpt")
	base := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	at := func(minutes int) time.Time { return base.Add(time.Duration(minutes) * time.Minute) }

	dbfx.Comment(t, issueID, "older comment", testutil.Cols{"created_at": at(1)})
	dbfx.Comment(t, issueID, "Ping [@Alice](mention://member/"+uuid.NewString()+")\n\nsee   [MUL-7](mention://issue/"+uuid.NewString()+") please", testutil.Cols{
		"created_at": at(2),
	})
	// Newer, but a deleted comment and a platform notice are not conversation.
	dbfx.Comment(t, issueID, "deleted reply", testutil.Cols{"created_at": at(3), "deleted_at": at(4)})
	dbfx.Comment(t, issueID, "child done", testutil.Cols{
		"author_type": "system", "author_id": uuid.Nil.String(), "type": "system", "created_at": at(5),
	})

	got := listThreadSummaries(t, issueID)
	if len(got.Summaries) != 1 {
		t.Fatalf("summaries = %+v, want exactly one row", got.Summaries)
	}
	excerpt := got.Summaries[0].LastReplyExcerpt
	if excerpt == nil || *excerpt != "Ping @Alice see MUL-7 please" {
		t.Errorf("last_reply_excerpt = %v, want %q", excerpt, "Ping @Alice see MUL-7 please")
	}

	long := dbfx.Issue(t, "thread summary long excerpt")
	dbfx.Comment(t, long, strings.Repeat("word ", 60))
	gotLong := listThreadSummaries(t, long)
	if len(gotLong.Summaries) != 1 || gotLong.Summaries[0].LastReplyExcerpt == nil {
		t.Fatalf("summaries = %+v, want one row with an excerpt", gotLong.Summaries)
	}
	if n := len([]rune(*gotLong.Summaries[0].LastReplyExcerpt)); n > issueLastReplyExcerptRunes+1 {
		t.Errorf("excerpt has %d runes, want at most %d plus the ellipsis", n, issueLastReplyExcerptRunes)
	}
}

func TestListIssueThreadSummaries_OtherWorkspaceAndEmptyIssuesYieldNoRow(t *testing.T) {
	otherWorkspaceID := dbfx.Workspace(t, "Thread summaries other", "thread-summaries-"+uuid.NewString())
	foreign := dbfx.Issue(t, "foreign issue", testutil.Cols{"workspace_id": otherWorkspaceID})
	dbfx.Comment(t, foreign, "foreign comment", testutil.Cols{"workspace_id": otherWorkspaceID})
	empty := dbfx.Issue(t, "no comments")
	commented := dbfx.Issue(t, "commented")
	dbfx.Comment(t, commented, "hello")

	got := listThreadSummaries(t, foreign, empty, commented)
	if len(got.Summaries) != 1 || got.Summaries[0].IssueID != commented {
		t.Fatalf("summaries = %+v, want only the commented issue in this workspace", got.Summaries)
	}
}

func TestListIssueThreadSummaries_MissingIdsIsEmpty(t *testing.T) {
	var out threadSummariesBody
	req := newRequest(http.MethodGet, "/api/issues/thread-summaries", nil)
	testutil.Call(t, testHandler.ListIssueThreadSummaries, req).Want(http.StatusOK).JSON(&out)
	if out.Summaries == nil || len(out.Summaries) != 0 {
		t.Fatalf("summaries = %#v, want empty non-nil slice", out.Summaries)
	}
}

func TestListIssueThreadSummaries_RejectsTooManyAndMalformedIds(t *testing.T) {
	ids := make([]string, issueThreadSummariesLimit+1)
	for i := range ids {
		ids[i] = uuid.NewString()
	}

	cases := map[string]string{
		"too many":  strings.Join(ids, ","),
		"malformed": uuid.NewString() + ",not-a-uuid",
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			req := newRequest(http.MethodGet, "/api/issues/thread-summaries?ids="+query, nil)
			testutil.Call(t, testHandler.ListIssueThreadSummaries, req).Want(http.StatusBadRequest)
		})
	}

	// Exactly at the cap is accepted.
	listThreadSummaries(t, ids[:issueThreadSummariesLimit]...)
}
