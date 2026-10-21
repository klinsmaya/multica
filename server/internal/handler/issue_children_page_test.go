package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

type childrenPageResponse struct {
	Issues []IssueResponse `json:"issues"`
	Total  int64           `json:"total"`
}

func callListChildren(t *testing.T, parentID, query string) *testutil.Response {
	t.Helper()
	path := "/api/issues/" + parentID + "/children"
	if query != "" {
		path += "?" + query
	}
	return testutil.Call(t, testHandler.ListChildIssues,
		withURLParam(newRequest("GET", path, nil), "id", parentID))
}

func TestGetIssue_ReturnsChildProgress(t *testing.T) {
	// newScrambledChildren creates four children, exactly one of them done.
	parent, children := newScrambledChildren(t)

	var detail IssueResponse
	testutil.Call(t, testHandler.GetIssue,
		withURLParam(newRequest("GET", "/api/issues/"+parent.ID, nil), "id", parent.ID)).
		Want(http.StatusOK).JSON(&detail)
	if detail.ChildCount == nil || *detail.ChildCount != int64(len(children)) {
		t.Fatalf("child_count = %v, want %d", detail.ChildCount, len(children))
	}
	if detail.ChildDoneCount == nil || *detail.ChildDoneCount != 1 {
		t.Fatalf("child_done_count = %v, want 1", detail.ChildDoneCount)
	}

	// A leaf issue reports an explicit zero rather than omitting the fields.
	var leaf IssueResponse
	testutil.Call(t, testHandler.GetIssue,
		withURLParam(newRequest("GET", "/api/issues/"+children[0].ID, nil), "id", children[0].ID)).
		Want(http.StatusOK).JSON(&leaf)
	if leaf.ChildCount == nil || *leaf.ChildCount != 0 || leaf.ChildDoneCount == nil || *leaf.ChildDoneCount != 0 {
		t.Fatalf("leaf child_count/child_done_count = %v/%v, want 0/0", leaf.ChildCount, leaf.ChildDoneCount)
	}
}

func TestListChildIssues_Paginates(t *testing.T) {
	parent, children := newScrambledChildren(t)

	var page childrenPageResponse
	callListChildren(t, parent.ID, "limit=2&offset=2").Want(http.StatusOK).JSON(&page)
	if page.Total != int64(len(children)) {
		t.Fatalf("total = %d, want %d", page.Total, len(children))
	}
	assertNumberAscending(t, page.Issues, children[2:4])

	// A page past the end is empty but still reports the full total.
	var empty childrenPageResponse
	callListChildren(t, parent.ID, "limit=2&offset=10").Want(http.StatusOK).JSON(&empty)
	if len(empty.Issues) != 0 || empty.Total != int64(len(children)) {
		t.Fatalf("past-end page = %d issues, total %d; want 0 issues, total %d",
			len(empty.Issues), empty.Total, len(children))
	}
}

func TestListChildIssues_WithoutParamsReturnsAll(t *testing.T) {
	parent, children := newScrambledChildren(t)

	var all childrenPageResponse
	callListChildren(t, parent.ID, "").Want(http.StatusOK).JSON(&all)
	assertNumberAscending(t, all.Issues, children)
	if all.Total != int64(len(children)) {
		t.Fatalf("total = %d, want %d", all.Total, len(children))
	}
}

func TestListChildIssues_RejectsInvalidPaging(t *testing.T) {
	parent, _ := newScrambledChildren(t)

	for _, query := range []string{
		"limit=0",
		"limit=201",
		"limit=-1",
		"limit=abc",
		"limit=2&offset=-1",
		"limit=2&offset=abc",
		"offset=2",
	} {
		callListChildren(t, parent.ID, query).Want(http.StatusBadRequest)
	}
}
