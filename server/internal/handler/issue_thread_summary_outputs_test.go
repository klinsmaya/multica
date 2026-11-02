package handler

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func outputsClock() func(minutes int) time.Time {
	base := time.Now().UTC().Truncate(time.Microsecond).Add(-24 * time.Hour)
	return func(minutes int) time.Time { return base.Add(time.Duration(minutes) * time.Minute) }
}

// seedGitHubPR links a GitHub PR to the issue and returns its id.
func seedGitHubPR(t *testing.T, issueID string, number int, createdAt time.Time, over ...testutil.Cols) string {
	t.Helper()
	cols := testutil.Cols{
		"workspace_id":    testWorkspaceID,
		"installation_id": 1,
		"repo_owner":      "multica-ai",
		"repo_name":       "outputs-" + uuid.NewString()[:8],
		"pr_number":       number,
		"title":           fmt.Sprintf("PR %d", number),
		"state":           "open",
		"html_url":        "https://example.test/pr/" + uuid.NewString(),
		"pr_created_at":   createdAt,
		"pr_updated_at":   createdAt,
		"additions":       10 + number,
		"deletions":       number,
	}
	for _, o := range over {
		for k, v := range o {
			cols[k] = v
		}
	}
	id := dbfx.Insert(t, "github_pull_request", cols)
	dbfx.InsertNoID(t, "issue_pull_request",
		testutil.Cols{"issue_id": issueID, "pull_request_id": id},
		"pull_request_id = $1", id)
	return id
}

// seedVCSPR links a self-hosted provider PR to the issue and returns its id.
// Its statuses are keyed by the head sha the caller names.
func seedVCSPR(t *testing.T, issueID string, number int, createdAt time.Time, headSHA string, statuses ...string) string {
	t.Helper()
	connectionID := uuid.NewString()
	id := dbfx.Insert(t, "vcs_pull_request", testutil.Cols{
		"workspace_id":  testWorkspaceID,
		"connection_id": connectionID,
		"provider":      "gitea",
		"repo_owner":    "team",
		"repo_name":     "outputs",
		"pr_number":     number,
		"title":         "VCS PR",
		"state":         "open",
		"html_url":      "https://git.example.test/team/outputs/pulls/" + uuid.NewString(),
		"head_sha":      headSHA,
		"pr_created_at": createdAt,
		"pr_updated_at": createdAt,
	})
	dbfx.InsertNoID(t, "issue_vcs_pull_request",
		testutil.Cols{"issue_id": issueID, "pull_request_id": id},
		"pull_request_id = $1", id)
	for i, state := range statuses {
		dbfx.InsertNoID(t, "vcs_commit_status", testutil.Cols{
			"connection_id": connectionID,
			"sha":           headSHA,
			"context":       "ci/" + string(rune('a'+i)),
			"state":         state,
		}, "connection_id = $1", connectionID)
	}
	return id
}

// seedAttachment uploads a file. commentID empty means a description file.
func seedAttachment(t *testing.T, issueID, commentID, filename, contentType string, createdAt time.Time) string {
	t.Helper()
	cols := testutil.Cols{
		"workspace_id":  testWorkspaceID,
		"issue_id":      issueID,
		"uploader_type": "member",
		"uploader_id":   testUserID,
		"filename":      filename,
		"url":           "https://cdn.example.test/" + uuid.NewString(),
		"content_type":  contentType,
		"size_bytes":    10,
		"created_at":    createdAt,
	}
	if commentID != "" {
		cols["comment_id"] = commentID
	}
	return dbfx.Insert(t, "attachment", cols)
}

func TestListIssueThreadSummaries_PullRequestsNewestThreeAcrossProviders(t *testing.T) {
	issueID := dbfx.Issue(t, "outputs pull requests")
	dbfx.Comment(t, issueID, "hello")
	at := outputsClock()

	oldest := seedGitHubPR(t, issueID, 101, at(1))
	merged := seedGitHubPR(t, issueID, 102, at(3), testutil.Cols{
		"state": "merged", "merged_at": at(4),
	})
	// Newer than both GitHub PRs but older than the second VCS PR.
	failing := seedVCSPR(t, issueID, 103, at(5), "aaa111", "passed", "failed")
	pending := seedVCSPR(t, issueID, 104, at(7), "bbb222", "passed", "pending")
	// A different issue's PR must not leak in.
	otherIssue := dbfx.Issue(t, "outputs pull requests other")
	seedGitHubPR(t, otherIssue, 105, at(9))

	got := listThreadSummaries(t, issueID)
	if len(got.Summaries) != 1 {
		t.Fatalf("summaries = %+v, want one row", got.Summaries)
	}
	prs := got.Summaries[0].PullRequests
	if len(prs) != 3 {
		t.Fatalf("pull_requests = %+v, want the 3 newest of 4", prs)
	}
	wantIDs := []string{pending, failing, merged}
	for i, want := range wantIDs {
		if prs[i].ID != want {
			t.Errorf("pull_requests[%d].id = %s, want %s (newest first, %s dropped)", i, prs[i].ID, want, oldest)
		}
	}
	if prs[0].Number != 104 || prs[0].State != "open" || prs[0].ChecksState != "pending" {
		t.Errorf("pull_requests[0] = %+v, want #104 open with pending checks", prs[0])
	}
	if prs[1].Number != 103 || prs[1].ChecksState != "failed" {
		t.Errorf("pull_requests[1] = %+v, want #103 with failed checks", prs[1])
	}
	if prs[2].Number != 102 || prs[2].State != "merged" || prs[2].Additions != 112 || prs[2].Deletions != 102 {
		t.Errorf("pull_requests[2] = %+v, want merged #102 with +112 -102", prs[2])
	}
	// The test handler runs without a GitHub App key, so no GitHub snapshot is
	// current and its CI is not claimed either way.
	if prs[2].ChecksState != "none" {
		t.Errorf("github checks_state = %q, want none without a current snapshot", prs[2].ChecksState)
	}
	if prs[2].URL == "" || prs[2].Title == "" {
		t.Errorf("pull_requests[2] = %+v, want url and title", prs[2])
	}
}

func TestListIssueThreadSummaries_VCSChecksStatePassedAndNone(t *testing.T) {
	issueID := dbfx.Issue(t, "outputs vcs checks")
	dbfx.Comment(t, issueID, "hello")
	at := outputsClock()

	passed := seedVCSPR(t, issueID, 201, at(2), "ccc333", "passed", "passed")
	// No status at all is "none", not passed.
	bare := seedVCSPR(t, issueID, 202, at(1), "ddd444")

	got := listThreadSummaries(t, issueID)
	prs := got.Summaries[0].PullRequests
	if len(prs) != 2 || prs[0].ID != passed || prs[1].ID != bare {
		t.Fatalf("pull_requests = %+v, want [%s %s]", prs, passed, bare)
	}
	if prs[0].ChecksState != "passed" || prs[1].ChecksState != "none" {
		t.Errorf("checks_state = [%q %q], want [passed none]", prs[0].ChecksState, prs[1].ChecksState)
	}
}

func TestListIssueThreadSummaries_OutputsWithoutCommentsStillGetARow(t *testing.T) {
	withPR := dbfx.Issue(t, "outputs pr only")
	seedGitHubPR(t, withPR, 301, outputsClock()(1))
	nothing := dbfx.Issue(t, "outputs nothing")

	got := listThreadSummaries(t, nothing, withPR)
	if len(got.Summaries) != 1 || got.Summaries[0].IssueID != withPR {
		t.Fatalf("summaries = %+v, want only the issue with a PR", got.Summaries)
	}
	s := got.Summaries[0]
	if s.ReplyCount != 0 || s.OpenThreadCount != 0 || s.LastReplyAt != nil {
		t.Errorf("summary = %+v, want zero comment counts", s)
	}
	if s.Participants == nil || s.AwaitingMe == nil || len(s.PullRequests) != 1 {
		t.Errorf("summary = %+v, want empty (non-nil) lists and one PR", s)
	}
}

func TestListIssueThreadSummaries_DeliverablesCountDistinctFiles(t *testing.T) {
	issueID := dbfx.Issue(t, "outputs deliverables")
	at := outputsClock()
	first := dbfx.Comment(t, issueID, "v1", testutil.Cols{"created_at": at(1)})
	second := dbfx.Comment(t, issueID, "v2", testutil.Cols{"created_at": at(5)})
	deleted := dbfx.Comment(t, issueID, "gone", testutil.Cols{"created_at": at(2), "deleted_at": at(3)})

	// Three uploads of one report: the media-type parameter and its case do
	// not make a different file.
	seedAttachment(t, issueID, first, "report.html", "text/html", at(1))
	seedAttachment(t, issueID, second, "report.html", "text/html; charset=utf-8", at(5))
	seedAttachment(t, issueID, second, "report.html", "TEXT/HTML", at(6))
	// Another name, and the same name with another type, are other files.
	seedAttachment(t, issueID, first, "shot.png", "image/png", at(2))
	seedAttachment(t, issueID, first, "report.html", "text/plain", at(3))
	// Inputs and removed comments are not output.
	seedAttachment(t, issueID, "", "brief.pdf", "application/pdf", at(8))
	seedAttachment(t, issueID, deleted, "ghost.png", "image/png", at(9))

	got := listThreadSummaries(t, issueID)
	if len(got.Summaries) != 1 {
		t.Fatalf("summaries = %+v, want one row", got.Summaries)
	}
	s := got.Summaries[0]
	if s.DeliverableCount != 3 {
		t.Errorf("deliverable_count = %d, want 3 (report.html text/html, shot.png, report.html text/plain)", s.DeliverableCount)
	}
	if s.LastDeliverableAt == nil {
		t.Fatalf("last_deliverable_at = nil, want %s", at(6).Format(time.RFC3339Nano))
	}
	last, err := time.Parse(time.RFC3339Nano, *s.LastDeliverableAt)
	if err != nil || !last.Equal(at(6)) {
		t.Errorf("last_deliverable_at = %q (err %v), want %s", *s.LastDeliverableAt, err, at(6).Format(time.RFC3339Nano))
	}
}

func TestListIssueThreadSummaries_NoOutputsIsEmptyNotNull(t *testing.T) {
	issueID := dbfx.Issue(t, "outputs none")
	dbfx.Comment(t, issueID, "hello")

	var body struct {
		Summaries []map[string]any `json:"summaries"`
	}
	req := newRequest(http.MethodGet, "/api/issues/thread-summaries?ids="+issueID, nil)
	testutil.Call(t, testHandler.ListIssueThreadSummaries, req).Want(http.StatusOK).JSON(&body)
	if len(body.Summaries) != 1 {
		t.Fatalf("summaries = %+v, want one row", body.Summaries)
	}
	row := body.Summaries[0]
	prs, ok := row["pull_requests"].([]any)
	if !ok || len(prs) != 0 {
		t.Errorf("pull_requests = %#v, want an empty array", row["pull_requests"])
	}
	if n, ok := row["deliverable_count"].(float64); !ok || n != 0 {
		t.Errorf("deliverable_count = %#v, want 0", row["deliverable_count"])
	}
	if v, present := row["last_deliverable_at"]; !present || v != nil {
		t.Errorf("last_deliverable_at = %#v, want present and null", v)
	}
}

func TestListIssueThreadSummaries_OutputsAreWorkspaceScoped(t *testing.T) {
	otherWorkspaceID := dbfx.Workspace(t, "Outputs other", "outputs-"+uuid.NewString())
	foreign := dbfx.Issue(t, "foreign outputs", testutil.Cols{"workspace_id": otherWorkspaceID})
	at := outputsClock()
	seedGitHubPR(t, foreign, 401, at(1), testutil.Cols{"workspace_id": otherWorkspaceID})
	foreignComment := dbfx.Comment(t, foreign, "file", testutil.Cols{"workspace_id": otherWorkspaceID})
	seedAttachment(t, foreign, foreignComment, "secret.pdf", "application/pdf", at(2))

	got := listThreadSummaries(t, foreign)
	if len(got.Summaries) != 0 {
		t.Fatalf("summaries = %+v, want none for another workspace's issue", got.Summaries)
	}
}

func TestGithubChecksState(t *testing.T) {
	text := func(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }
	cases := []struct {
		name      string
		available bool
		rollup    string
		failed    int64
		running   int64
		passed    int64
		want      string
	}{
		{"no current snapshot hides everything", false, "FAILURE", 2, 0, 0, "none"},
		{"failing rollup", true, "FAILURE", 0, 0, 3, "failed"},
		{"error rollup", true, "ERROR", 0, 0, 0, "failed"},
		{"failed run beats a green rollup", true, "SUCCESS", 1, 0, 2, "failed"},
		{"pending rollup", true, "PENDING", 0, 2, 1, "pending"},
		{"expected rollup", true, "EXPECTED", 0, 0, 0, "pending"},
		{"success rollup", true, "SUCCESS", 0, 0, 4, "passed"},
		{"unknown rollup falls back to running runs", true, "SOMETHING_NEW", 0, 1, 2, "pending"},
		{"unknown rollup falls back to passed runs", true, "SOMETHING_NEW", 0, 0, 2, "passed"},
		{"unknown rollup without runs", true, "SOMETHING_NEW", 0, 0, 0, "none"},
		{"null rollup is no checks, never passed", true, "", 0, 0, 0, "none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := githubChecksState(tc.available, text(tc.rollup), tc.failed, tc.running, tc.passed); got != tc.want {
				t.Errorf("githubChecksState = %q, want %q", got, tc.want)
			}
		})
	}
}
