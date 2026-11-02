package handler

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
)

type childProgressBody struct {
	Progress []struct {
		ParentIssueID string `json:"parent_issue_id"`
		Total         int64  `json:"total"`
		Done          int64  `json:"done"`
	} `json:"progress"`
}

// byParent maps parent id to [done, total].
func (b childProgressBody) byParent() map[string][2]int64 {
	out := make(map[string][2]int64, len(b.Progress))
	for _, p := range b.Progress {
		out[p.ParentIssueID] = [2]int64{p.Done, p.Total}
	}
	return out
}

func getChildProgress(t *testing.T, query string) childProgressBody {
	t.Helper()
	var out childProgressBody
	testutil.Call(t, testHandler.ChildIssueProgress,
		newRequest("GET", "/api/issues/child-progress"+query, nil)).
		Want(http.StatusOK).JSON(&out)
	return out
}

func TestChildIssueProgress_ParentIDsNarrowsResponse(t *testing.T) {
	parentA := dbfx.Issue(t, "progress parent A")
	parentB := dbfx.Issue(t, "progress parent B")
	parentC := dbfx.Issue(t, "progress parent C")
	dbfx.Issue(t, "a done", testutil.Cols{"parent_issue_id": parentA, "status": "done"})
	dbfx.Issue(t, "a open", testutil.Cols{"parent_issue_id": parentA, "status": "todo"})
	dbfx.Issue(t, "b open", testutil.Cols{"parent_issue_id": parentB, "status": "todo"})
	dbfx.Issue(t, "c done", testutil.Cols{"parent_issue_id": parentC, "status": "done"})

	got := getChildProgress(t, "?parent_ids="+parentA+","+parentB).byParent()
	if len(got) != 2 {
		t.Fatalf("expected exactly the 2 named parents, got %v", got)
	}
	if got[parentA] != [2]int64{1, 2} {
		t.Errorf("parent A done/total = %v, want [1 2]", got[parentA])
	}
	if got[parentB] != [2]int64{0, 1} {
		t.Errorf("parent B done/total = %v, want [0 1]", got[parentB])
	}
	if _, ok := got[parentC]; ok {
		t.Errorf("parent C was not requested but is in the response")
	}
}

func TestChildIssueProgress_ParentWithoutChildrenYieldsNoRow(t *testing.T) {
	childless := dbfx.Issue(t, "progress childless parent")
	if got := getChildProgress(t, "?parent_ids="+childless).Progress; len(got) != 0 {
		t.Fatalf("expected no rows for a parent without children, got %v", got)
	}
}

func TestChildIssueProgress_ForeignWorkspaceParentIsAbsent(t *testing.T) {
	foreignWorkspaceID := dbfx.Workspace(t, "Child progress foreign", "child-progress-"+uuid.NewString())
	foreignParent := dbfx.Issue(t, "foreign progress parent", testutil.Cols{"workspace_id": foreignWorkspaceID})
	dbfx.Issue(t, "foreign progress child", testutil.Cols{
		"workspace_id":    foreignWorkspaceID,
		"parent_issue_id": foreignParent,
		"status":          "done",
	})
	localParent := dbfx.Issue(t, "local progress parent")
	dbfx.Issue(t, "local progress child", testutil.Cols{"parent_issue_id": localParent, "status": "done"})

	got := getChildProgress(t, "?parent_ids="+foreignParent+","+localParent).byParent()
	if _, ok := got[foreignParent]; ok {
		t.Errorf("foreign workspace parent leaked into the response: %v", got)
	}
	if got[localParent] != [2]int64{1, 1} {
		t.Errorf("local parent done/total = %v, want [1 1]", got[localParent])
	}
}

func TestChildIssueProgress_RejectsTooManyParentIDs(t *testing.T) {
	ids := make([]string, childIssueProgressParentsLimit+1)
	for i := range ids {
		ids[i] = uuid.NewString()
	}
	testutil.Call(t, testHandler.ChildIssueProgress,
		newRequest("GET", "/api/issues/child-progress?parent_ids="+strings.Join(ids, ","), nil)).
		Want(http.StatusBadRequest)

	// Exactly at the cap is accepted.
	getChildProgress(t, "?parent_ids="+strings.Join(ids[:childIssueProgressParentsLimit], ","))
}

func TestChildIssueProgress_RejectsMalformedParentID(t *testing.T) {
	parent := dbfx.Issue(t, "progress malformed parent")
	testutil.Call(t, testHandler.ChildIssueProgress,
		newRequest("GET", "/api/issues/child-progress?parent_ids="+parent+",not-a-uuid", nil)).
		Want(http.StatusBadRequest)
}

func TestChildIssueProgress_WithoutParentIDsReturnsWorkspaceMap(t *testing.T) {
	parentA := dbfx.Issue(t, "progress map parent A")
	parentB := dbfx.Issue(t, "progress map parent B")
	dbfx.Issue(t, "map a done", testutil.Cols{"parent_issue_id": parentA, "status": "done"})
	dbfx.Issue(t, "map b open", testutil.Cols{"parent_issue_id": parentB, "status": "todo"})

	got := getChildProgress(t, "").byParent()
	if got[parentA] != [2]int64{1, 1} || got[parentB] != [2]int64{0, 1} {
		t.Fatalf("workspace map missing the seeded parents: A=%v B=%v", got[parentA], got[parentB])
	}
}
