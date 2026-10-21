package handler

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func mentionMember(label, userID string) string {
	return "[@" + label + "](mention://member/" + userID + ")"
}

func awaitingMeFor(t *testing.T, issueID string) []AwaitingMeResponse {
	t.Helper()
	got := listThreadSummaries(t, issueID)
	if len(got.Summaries) != 1 {
		t.Fatalf("summaries = %+v, want exactly one row", got.Summaries)
	}
	if got.Summaries[0].AwaitingMe == nil {
		t.Fatal("awaiting_me = nil, want an empty slice")
	}
	return got.Summaries[0].AwaitingMe
}

func agentQuestionClock() func(minutes int) time.Time {
	base := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	return func(minutes int) time.Time { return base.Add(time.Duration(minutes) * time.Minute) }
}

func TestListIssueThreadSummaries_AwaitingMe_AgentReplyMentioningCaller(t *testing.T) {
	issueID := dbfx.Issue(t, "awaiting me reply")
	at := agentQuestionClock()
	agent := uuid.NewString()

	root := dbfx.Comment(t, issueID, "Should we ship?", testutil.Cols{"created_at": at(1)})
	reply := dbfx.Comment(t, issueID, "Ship "+mentionMember("Me", testUserID)+"?\n\n  Yes or no", testutil.Cols{
		"author_type": "agent", "author_id": agent, "parent_id": root, "created_at": at(2),
	})

	got := awaitingMeFor(t, issueID)
	if len(got) != 1 {
		t.Fatalf("awaiting_me = %+v, want one entry", got)
	}
	if got[0].CommentID != reply || got[0].ActorID != agent {
		t.Errorf("entry = %+v, want comment %s by agent %s", got[0], reply, agent)
	}
	if got[0].Excerpt != "Ship @Me? Yes or no" {
		t.Errorf("excerpt = %q, want mention reduced to @Label and whitespace collapsed", got[0].Excerpt)
	}
	created, err := time.Parse(time.RFC3339Nano, got[0].CreatedAt)
	if err != nil || !created.Equal(at(2)) {
		t.Errorf("created_at = %q (err %v), want %s", got[0].CreatedAt, err, at(2).Format(time.RFC3339Nano))
	}
}

func TestListIssueThreadSummaries_AwaitingMe_RootCommentCounts(t *testing.T) {
	issueID := dbfx.Issue(t, "awaiting me root")
	root := dbfx.Comment(t, issueID, mentionMember("Me", testUserID)+" pick one", testutil.Cols{
		"author_type": "agent", "author_id": uuid.NewString(),
	})
	got := awaitingMeFor(t, issueID)
	if len(got) != 1 || got[0].CommentID != root {
		t.Fatalf("awaiting_me = %+v, want the agent root %s", got, root)
	}
}

func TestListIssueThreadSummaries_AwaitingMe_EmptyWhenNothingMatches(t *testing.T) {
	issueID := dbfx.Issue(t, "awaiting me none")
	dbfx.Comment(t, issueID, "plain member comment")
	if got := awaitingMeFor(t, issueID); len(got) != 0 {
		t.Fatalf("awaiting_me = %+v, want empty", got)
	}
}

func TestListIssueThreadSummaries_AwaitingMe_UserAnsweredLast(t *testing.T) {
	issueID := dbfx.Issue(t, "awaiting me answered")
	at := agentQuestionClock()
	root := dbfx.Comment(t, issueID, "question", testutil.Cols{"created_at": at(1)})
	dbfx.Comment(t, issueID, mentionMember("Me", testUserID)+" which one?", testutil.Cols{
		"author_type": "agent", "author_id": uuid.NewString(), "parent_id": root, "created_at": at(2),
	})
	dbfx.Comment(t, issueID, "the first one", testutil.Cols{"parent_id": root, "created_at": at(3)})

	if got := awaitingMeFor(t, issueID); len(got) != 0 {
		t.Fatalf("awaiting_me = %+v, want empty once the user replied", got)
	}
}

func TestListIssueThreadSummaries_AwaitingMe_ResolvedThreadIsExcluded(t *testing.T) {
	issueID := dbfx.Issue(t, "awaiting me resolved")
	at := agentQuestionClock()
	root := dbfx.Comment(t, issueID, "question", testutil.Cols{
		"created_at": at(1), "resolved_at": at(4),
		"resolved_by_type": "member", "resolved_by_id": testUserID,
	})
	dbfx.Comment(t, issueID, mentionMember("Me", testUserID)+" which one?", testutil.Cols{
		"author_type": "agent", "author_id": uuid.NewString(), "parent_id": root, "created_at": at(2),
	})

	if got := awaitingMeFor(t, issueID); len(got) != 0 {
		t.Fatalf("awaiting_me = %+v, want empty for a resolved thread", got)
	}
}

func TestListIssueThreadSummaries_AwaitingMe_MentionOfSomeoneElseIsExcluded(t *testing.T) {
	issueID := dbfx.Issue(t, "awaiting me other")
	other := uuid.NewString()
	dbfx.Comment(t, issueID, mentionMember("Someone", other)+" which one?", testutil.Cols{
		"author_type": "agent", "author_id": uuid.NewString(),
	})
	// An agent mention carrying the caller's id is not a member mention.
	dbfx.Comment(t, issueID, "[@Bot](mention://agent/"+testUserID+") ping", testutil.Cols{
		"author_type": "agent", "author_id": uuid.NewString(),
	})

	if got := awaitingMeFor(t, issueID); len(got) != 0 {
		t.Fatalf("awaiting_me = %+v, want empty when the caller is not mentioned", got)
	}
}

func TestListIssueThreadSummaries_AwaitingMe_MemberAuthoredMentionIsExcluded(t *testing.T) {
	issueID := dbfx.Issue(t, "awaiting me member author")
	dbfx.Comment(t, issueID, mentionMember("Me", testUserID)+" note to self")
	if got := awaitingMeFor(t, issueID); len(got) != 0 {
		t.Fatalf("awaiting_me = %+v, want empty for a member-authored comment", got)
	}
}

func TestListIssueThreadSummaries_AwaitingMe_DeletedLatestFallsBackToPrevious(t *testing.T) {
	issueID := dbfx.Issue(t, "awaiting me deleted tail")
	at := agentQuestionClock()
	agent := uuid.NewString()

	// Deleted member reply on top of an agent question: the question stands.
	root := dbfx.Comment(t, issueID, "thread one", testutil.Cols{"created_at": at(1)})
	question := dbfx.Comment(t, issueID, mentionMember("Me", testUserID)+" decide", testutil.Cols{
		"author_type": "agent", "author_id": agent, "parent_id": root, "created_at": at(2),
	})
	dbfx.Comment(t, issueID, "deleted answer", testutil.Cols{
		"parent_id": root, "created_at": at(3), "deleted_at": at(4),
	})

	// Deleted agent question on top of a member comment: nothing is pending.
	root2 := dbfx.Comment(t, issueID, "thread two", testutil.Cols{"created_at": at(5)})
	dbfx.Comment(t, issueID, mentionMember("Me", testUserID)+" withdrawn", testutil.Cols{
		"author_type": "agent", "author_id": agent, "parent_id": root2, "created_at": at(6), "deleted_at": at(7),
	})

	got := awaitingMeFor(t, issueID)
	if len(got) != 1 || got[0].CommentID != question {
		t.Fatalf("awaiting_me = %+v, want only the surviving question %s", got, question)
	}
}

func TestListIssueThreadSummaries_AwaitingMe_NestedReplyBelongsToRootThread(t *testing.T) {
	issueID := dbfx.Issue(t, "awaiting me nested")
	at := agentQuestionClock()
	root := dbfx.Comment(t, issueID, "root", testutil.Cols{"created_at": at(1)})
	child := dbfx.Comment(t, issueID, "child", testutil.Cols{"parent_id": root, "created_at": at(2)})
	grandchild := dbfx.Comment(t, issueID, mentionMember("Me", testUserID)+" deep question", testutil.Cols{
		"author_type": "agent", "author_id": uuid.NewString(), "parent_id": child, "created_at": at(3),
	})

	got := awaitingMeFor(t, issueID)
	if len(got) != 1 || got[0].CommentID != grandchild {
		t.Fatalf("awaiting_me = %+v, want the deep agent reply %s", got, grandchild)
	}
}

func TestListIssueThreadSummaries_AwaitingMe_CapsAtThreeNewestFirst(t *testing.T) {
	issueID := dbfx.Issue(t, "awaiting me cap")
	at := agentQuestionClock()
	agent := uuid.NewString()

	ids := make([]string, 4)
	for i := range ids {
		ids[i] = dbfx.Comment(t, issueID, mentionMember("Me", testUserID)+" question", testutil.Cols{
			"author_type": "agent", "author_id": agent, "created_at": at(i + 1),
		})
	}

	got := awaitingMeFor(t, issueID)
	if len(got) != 3 {
		t.Fatalf("awaiting_me = %+v, want 3 entries", got)
	}
	for i, wantID := range []string{ids[3], ids[2], ids[1]} {
		if got[i].CommentID != wantID {
			t.Errorf("awaiting_me[%d] = %s, want %s (newest first)", i, got[i].CommentID, wantID)
		}
	}
}

func TestListIssueThreadSummaries_AwaitingMe_ExcerptCutAtRunes(t *testing.T) {
	issueID := dbfx.Issue(t, "awaiting me excerpt")
	// Multi-byte runes: a byte cut would split a character.
	dbfx.Comment(t, issueID, mentionMember("Me", testUserID)+strings.Repeat("决", 200), testutil.Cols{
		"author_type": "agent", "author_id": uuid.NewString(),
	})

	got := awaitingMeFor(t, issueID)
	if len(got) != 1 {
		t.Fatalf("awaiting_me = %+v, want one entry", got)
	}
	want := "@Me" + strings.Repeat("决", issueAwaitingMeExcerptRunes-len("@Me")) + "…"
	if got[0].Excerpt != want {
		t.Errorf("excerpt = %q (%d runes), want %d runes plus an ellipsis",
			got[0].Excerpt, utf8.RuneCountInString(got[0].Excerpt), issueAwaitingMeExcerptRunes)
	}
}

func TestCommentExcerpt(t *testing.T) {
	tests := []struct {
		name    string
		content string
		max     int
		want    string
	}{
		{"member mention", "Hi [@Ann](mention://member/0f0f0f0f-0000-0000-0000-000000000001), ok?", 140, "Hi @Ann, ok?"},
		{"issue mention drops the link only", "see [MUL-12](mention://issue/0f0f0f0f-0000-0000-0000-000000000002)", 140, "see MUL-12"},
		{"label with brackets", "[@David[TF]](mention://agent/0f0f0f0f-0000-0000-0000-000000000003) go", 140, "@David[TF] go"},
		{"whitespace collapsed", "  a\n\n b\t\tc  ", 140, "a b c"},
		{"exact length is kept", strings.Repeat("a", 10), 10, strings.Repeat("a", 10)},
		{"cut with ellipsis", strings.Repeat("a", 11), 10, strings.Repeat("a", 10) + "…"},
		{"cut counts runes", strings.Repeat("决", 11), 10, strings.Repeat("决", 10) + "…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := commentExcerpt(tt.content, tt.max); got != tt.want {
				t.Errorf("commentExcerpt() = %q, want %q", got, tt.want)
			}
		})
	}
}
