package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

type projectPullRequestsBody struct {
	PullRequests []ProjectPullRequestResponse     `json:"pull_requests"`
	Total        int64                            `json:"total"`
	Counts       ProjectPullRequestCountsResponse `json:"counts"`
}

func listProjectPullRequests(t *testing.T, projectID, query string) projectPullRequestsBody {
	t.Helper()
	var out projectPullRequestsBody
	req := newRequest(http.MethodGet, "/api/projects/"+projectID+"/pull-requests"+query, nil)
	req = withURLParam(req, "id", projectID)
	testutil.Call(t, testHandler.ListProjectPullRequests, req).Want(http.StatusOK).JSON(&out)
	return out
}

// linkGitHubPR links an existing GitHub PR to one more issue.
func linkGitHubPR(t *testing.T, issueID, prID string) {
	t.Helper()
	dbfx.InsertNoID(t, "issue_pull_request",
		testutil.Cols{"issue_id": issueID, "pull_request_id": prID},
		"issue_id = $1 AND pull_request_id = $2", issueID, prID)
}

func TestListProjectPullRequests_MergesSharedPRIntoOneRow(t *testing.T) {
	at := outputsClock()
	projectID := dbfx.Project(t, "pr tab shared")
	first := dbfx.Issue(t, "first", testutil.Cols{"project_id": projectID})
	second := dbfx.Issue(t, "second", testutil.Cols{"project_id": projectID})

	shared := seedGitHubPR(t, first, 1, at(1))
	linkGitHubPR(t, second, shared)
	solo := seedGitHubPR(t, second, 2, at(2))

	out := listProjectPullRequests(t, projectID, "")
	if out.Total != 2 || len(out.PullRequests) != 2 {
		t.Fatalf("total=%d rows=%d, want 2 distinct PRs", out.Total, len(out.PullRequests))
	}
	byID := map[string]ProjectPullRequestResponse{}
	for _, pr := range out.PullRequests {
		byID[pr.ID] = pr
	}
	if got := len(byID[shared].Issues); got != 2 {
		t.Fatalf("shared PR issues = %d, want 2", got)
	}
	if got := byID[solo].Issues; len(got) != 1 || got[0].ID != second {
		t.Fatalf("solo PR issues = %+v, want only the second issue", got)
	}
	for _, issue := range byID[shared].Issues {
		if issue.Identifier == "" || issue.Title == "" {
			t.Fatalf("issue chip missing identifier or title: %+v", issue)
		}
	}
	if byID[solo].Additions != 12 || byID[solo].Deletions != 2 {
		t.Fatalf("solo PR diff = +%d -%d, want +12 -2", byID[solo].Additions, byID[solo].Deletions)
	}
}

func TestListProjectPullRequests_ExcludesOtherProjectsAndOrdersByUpdate(t *testing.T) {
	at := outputsClock()
	projectID := dbfx.Project(t, "pr tab scope")
	otherProjectID := dbfx.Project(t, "pr tab other")
	inProject := dbfx.Issue(t, "in project", testutil.Cols{"project_id": projectID})
	inOther := dbfx.Issue(t, "in other project", testutil.Cols{"project_id": otherProjectID})
	noProject := dbfx.Issue(t, "no project")

	older := seedGitHubPR(t, inProject, 1, at(1))
	newer := seedGitHubPR(t, inProject, 2, at(5))
	seedGitHubPR(t, inOther, 3, at(9))
	seedGitHubPR(t, noProject, 4, at(10))
	vcs := seedVCSPR(t, inProject, 5, at(3), "sha-scope", "passed", "failed")

	out := listProjectPullRequests(t, projectID, "")
	if out.Total != 3 {
		t.Fatalf("total = %d, want 3", out.Total)
	}
	var ids []string
	for _, pr := range out.PullRequests {
		ids = append(ids, pr.ID)
	}
	want := []string{newer, vcs, older}
	for i := range want {
		if i >= len(ids) || ids[i] != want[i] {
			t.Fatalf("order = %v, want %v (newest update first, GitHub and VCS merged)", ids, want)
		}
	}
	if out.PullRequests[1].Provider != "gitea" {
		t.Fatalf("provider = %q, want gitea", out.PullRequests[1].Provider)
	}
	if got := out.PullRequests[1].ChecksConclusion; got == nil || *got != "failed" {
		t.Fatalf("vcs checks_conclusion = %v, want failed", got)
	}
}

func TestListProjectPullRequests_StateFilterAndCounts(t *testing.T) {
	at := outputsClock()
	projectID := dbfx.Project(t, "pr tab states")
	issue := dbfx.Issue(t, "states", testutil.Cols{"project_id": projectID})

	open := seedGitHubPR(t, issue, 1, at(1))
	draft := seedGitHubPR(t, issue, 2, at(2), testutil.Cols{"state": "draft"})
	merged := seedGitHubPR(t, issue, 3, at(3), testutil.Cols{"state": "merged", "merged_at": at(3)})
	closed := seedGitHubPR(t, issue, 4, at(4), testutil.Cols{"state": "closed", "closed_at": at(4)})

	cases := []struct {
		state string
		want  []string
	}{
		{"open", []string{draft, open}},
		{"merged", []string{merged}},
		{"closed", []string{closed}},
		{"", []string{closed, merged, draft, open}},
	}
	for _, tc := range cases {
		query := ""
		if tc.state != "" {
			query = "?state=" + tc.state
		}
		out := listProjectPullRequests(t, projectID, query)
		if out.Total != int64(len(tc.want)) {
			t.Fatalf("state %q: total = %d, want %d", tc.state, out.Total, len(tc.want))
		}
		for i, id := range tc.want {
			if i >= len(out.PullRequests) || out.PullRequests[i].ID != id {
				t.Fatalf("state %q: rows out of order or missing", tc.state)
			}
		}
		// Counts cover every group whatever the filter.
		if out.Counts.Open != 2 || out.Counts.Merged != 1 || out.Counts.Closed != 1 {
			t.Fatalf("state %q: counts = %+v, want open 2 merged 1 closed 1", tc.state, out.Counts)
		}
	}

	req := newRequest(http.MethodGet, "/api/projects/"+projectID+"/pull-requests?state=bogus", nil)
	req = withURLParam(req, "id", projectID)
	testutil.Call(t, testHandler.ListProjectPullRequests, req).Want(http.StatusBadRequest)
}

func TestListProjectPullRequests_PagesPerPullRequest(t *testing.T) {
	at := outputsClock()
	projectID := dbfx.Project(t, "pr tab paging")
	first := dbfx.Issue(t, "first", testutil.Cols{"project_id": projectID})
	second := dbfx.Issue(t, "second", testutil.Cols{"project_id": projectID})

	// Three PRs, the newest linked to both issues: paging counts it once.
	oldest := seedGitHubPR(t, first, 1, at(1))
	middle := seedGitHubPR(t, first, 2, at(2))
	newest := seedGitHubPR(t, first, 3, at(3))
	linkGitHubPR(t, second, newest)

	page1 := listProjectPullRequests(t, projectID, "?limit=2")
	if page1.Total != 3 || len(page1.PullRequests) != 2 {
		t.Fatalf("page 1: total=%d rows=%d, want 3 and 2", page1.Total, len(page1.PullRequests))
	}
	if page1.PullRequests[0].ID != newest || page1.PullRequests[1].ID != middle {
		t.Fatalf("page 1 order wrong")
	}
	if len(page1.PullRequests[0].Issues) != 2 {
		t.Fatalf("shared PR on page 1 has %d issues, want 2", len(page1.PullRequests[0].Issues))
	}

	page2 := listProjectPullRequests(t, projectID, "?limit=2&offset=2")
	if page2.Total != 3 || len(page2.PullRequests) != 1 || page2.PullRequests[0].ID != oldest {
		t.Fatalf("page 2: total=%d rows=%d, want the oldest PR alone", page2.Total, len(page2.PullRequests))
	}

	beyond := listProjectPullRequests(t, projectID, "?limit=2&offset=10")
	if beyond.Total != 3 || len(beyond.PullRequests) != 0 {
		t.Fatalf("past the end: total=%d rows=%d, want 3 and 0", beyond.Total, len(beyond.PullRequests))
	}
}

func TestListProjectPullRequests_EmptyAndMissingProject(t *testing.T) {
	projectID := dbfx.Project(t, "pr tab empty")
	out := listProjectPullRequests(t, projectID, "")
	if out.Total != 0 || out.PullRequests == nil || len(out.PullRequests) != 0 {
		t.Fatalf("empty project: %+v", out)
	}

	missing := "00000000-0000-4000-8000-000000000000"
	req := newRequest(http.MethodGet, "/api/projects/"+missing+"/pull-requests", nil)
	req = withURLParam(req, "id", missing)
	testutil.Call(t, testHandler.ListProjectPullRequests, req).Want(http.StatusNotFound)
}
