package handler

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

type projectAttachmentsPayload struct {
	Attachments []struct {
		ID           string `json:"id"`
		Filename     string `json:"filename"`
		UploaderType string `json:"uploader_type"`
		IssueID      string `json:"issue_id"`
		Issue        struct {
			ID         string `json:"id"`
			Identifier string `json:"identifier"`
			Title      string `json:"title"`
		} `json:"issue"`
	} `json:"attachments"`
	Total            int `json:"total"`
	DeliverableTotal int `json:"deliverable_total"`
	UploadTotal      int `json:"upload_total"`
}

func listProjectAttachmentsForTest(t *testing.T, projectID, rawQuery string) (projectAttachmentsPayload, *testutil.Response) {
	t.Helper()
	path := "/api/projects/" + projectID + "/attachments"
	if rawQuery != "" {
		path += "?" + rawQuery
	}
	req := withURLParam(newRequest(http.MethodGet, path, nil), "id", projectID)
	resp := testutil.Call(t, testHandler.ListProjectAttachments, req)
	var out projectAttachmentsPayload
	if resp.Code == http.StatusOK {
		resp.JSON(&out)
	}
	return out, resp
}

func insertProjectAttachment(t *testing.T, issueID, filename, uploaderType string, over ...testutil.Cols) string {
	t.Helper()
	// uploader_id carries no foreign key, so the member id stands in for an agent's.
	uploaderID := testUserID
	cols := testutil.Cols{
		"workspace_id":  testWorkspaceID,
		"issue_id":      issueID,
		"uploader_type": uploaderType,
		"uploader_id":   uploaderID,
		"filename":      filename,
		"url":           "https://example.test/" + filename,
		"content_type":  "application/octet-stream",
		"size_bytes":    1,
	}
	for _, o := range over {
		for k, v := range o {
			cols[k] = v
		}
	}
	return dbfx.Insert(t, "attachment", cols)
}

func TestListProjectAttachmentsScopesToProject(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	projectID := dbfx.Project(t, "Library project")
	otherProjectID := dbfx.Project(t, "Other library project")
	issueID := dbfx.Issue(t, "In project", testutil.Cols{"project_id": projectID})
	otherIssueID := dbfx.Issue(t, "In other project", testutil.Cols{"project_id": otherProjectID})
	noProjectIssueID := dbfx.Issue(t, "In no project")

	insertProjectAttachment(t, issueID, "mine.pdf", "member")
	insertProjectAttachment(t, otherIssueID, "other.pdf", "member")
	insertProjectAttachment(t, noProjectIssueID, "none.pdf", "member")

	out, _ := listProjectAttachmentsForTest(t, projectID, "")
	if out.Total != 1 || len(out.Attachments) != 1 || out.Attachments[0].Filename != "mine.pdf" {
		t.Fatalf("attachments = %+v (total %d), want only mine.pdf", out.Attachments, out.Total)
	}
	row := out.Attachments[0]
	if row.Issue.ID != issueID || row.Issue.Title != "In project" || row.Issue.Identifier == "" || row.IssueID != issueID {
		t.Fatalf("row issue = %+v, want the project issue with an identifier", row.Issue)
	}
}

func TestListProjectAttachmentsSourceFilter(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	projectID := dbfx.Project(t, "Source filter project")
	issueID := dbfx.Issue(t, "Source filter issue", testutil.Cols{"project_id": projectID})
	insertProjectAttachment(t, issueID, "report.pdf", "agent")
	insertProjectAttachment(t, issueID, "photo.png", "agent")
	insertProjectAttachment(t, issueID, "notes.txt", "member")

	both, _ := listProjectAttachmentsForTest(t, projectID, "")
	if both.Total != 3 || both.DeliverableTotal != 2 || both.UploadTotal != 1 {
		t.Fatalf("unfiltered totals = %d/%d/%d, want 3/2/1", both.Total, both.DeliverableTotal, both.UploadTotal)
	}

	deliverables, _ := listProjectAttachmentsForTest(t, projectID, "source=deliverable")
	if deliverables.Total != 2 || len(deliverables.Attachments) != 2 {
		t.Fatalf("deliverables = %d rows (total %d), want 2", len(deliverables.Attachments), deliverables.Total)
	}
	for _, a := range deliverables.Attachments {
		if a.UploaderType != "agent" {
			t.Fatalf("deliverable row uploaded by %q, want agent", a.UploaderType)
		}
	}
	// Both group counts stay available whichever group was asked for.
	if deliverables.UploadTotal != 1 {
		t.Fatalf("upload_total = %d next to a deliverable query, want 1", deliverables.UploadTotal)
	}

	uploads, _ := listProjectAttachmentsForTest(t, projectID, "source=upload")
	if uploads.Total != 1 || len(uploads.Attachments) != 1 || uploads.Attachments[0].Filename != "notes.txt" {
		t.Fatalf("uploads = %+v (total %d), want notes.txt only", uploads.Attachments, uploads.Total)
	}

	_, bad := listProjectAttachmentsForTest(t, projectID, "source=everything")
	bad.Want(http.StatusBadRequest)
}

func TestListProjectAttachmentsPagesNewestFirst(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	projectID := dbfx.Project(t, "Paging project")
	issueID := dbfx.Issue(t, "Paging issue", testutil.Cols{"project_id": projectID})
	for i := 0; i < 5; i++ {
		insertProjectAttachment(t, issueID, fmt.Sprintf("file-%d.txt", i), "member", testutil.Cols{
			"created_at": testutil.Raw(fmt.Sprintf("now() - interval '%d minutes'", 10-i)),
		})
	}

	first, _ := listProjectAttachmentsForTest(t, projectID, "limit=2")
	if first.Total != 5 || len(first.Attachments) != 2 {
		t.Fatalf("first page = %d rows (total %d), want 2 of 5", len(first.Attachments), first.Total)
	}
	if first.Attachments[0].Filename != "file-4.txt" || first.Attachments[1].Filename != "file-3.txt" {
		t.Fatalf("first page = %q, %q, want newest first", first.Attachments[0].Filename, first.Attachments[1].Filename)
	}
	last, _ := listProjectAttachmentsForTest(t, projectID, "limit=2&offset=4")
	if last.Total != 5 || len(last.Attachments) != 1 || last.Attachments[0].Filename != "file-0.txt" {
		t.Fatalf("last page = %+v (total %d), want file-0.txt alone", last.Attachments, last.Total)
	}
}

func TestListProjectAttachmentsCountsEachRowOnce(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	projectID := dbfx.Project(t, "Comment attachments project")
	issueID := dbfx.Issue(t, "Comment attachments issue", testutil.Cols{"project_id": projectID})
	liveComment := dbfx.Comment(t, issueID, "has a file")
	deletedComment := dbfx.Comment(t, issueID, "")
	dbfx.Exec(t, `UPDATE comment SET deleted_at = now() WHERE id = $1`, deletedComment)

	// A comment attachment keeps the issue id too; it must still appear once.
	insertProjectAttachment(t, issueID, "on-comment.pdf", "member", testutil.Cols{"comment_id": liveComment})
	insertProjectAttachment(t, issueID, "on-issue.pdf", "member")
	insertProjectAttachment(t, issueID, "on-tombstone.pdf", "member", testutil.Cols{"comment_id": deletedComment})

	out, _ := listProjectAttachmentsForTest(t, projectID, "")
	names := map[string]int{}
	for _, a := range out.Attachments {
		names[a.Filename]++
	}
	if out.Total != 2 || names["on-comment.pdf"] != 1 || names["on-issue.pdf"] != 1 || names["on-tombstone.pdf"] != 0 {
		t.Fatalf("attachments = %v (total %d), want the live comment's and the issue's file once each, no tombstone file", names, out.Total)
	}
}

func TestListProjectAttachmentsRejectsForeignProject(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	foreignWorkspaceID := dbfx.Workspace(t, "Library foreign", "library-foreign-ws")
	foreignProjectID := dbfx.Project(t, "Foreign project", testutil.Cols{"workspace_id": foreignWorkspaceID})
	_, resp := listProjectAttachmentsForTest(t, foreignProjectID, "")
	resp.Want(http.StatusNotFound)
}

func TestEnsureProjectLibraryIssueCreatesOnceThenReuses(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	projectID := dbfx.Project(t, "Holder project")
	call := func() (*testutil.Response, ProjectLibraryIssueResponse) {
		req := withURLParam(newRequest(http.MethodPost, "/api/projects/"+projectID+"/library-issue", nil), "id", projectID)
		resp := testutil.Call(t, testHandler.EnsureProjectLibraryIssue, req)
		var out ProjectLibraryIssueResponse
		resp.JSON(&out)
		return resp, out
	}

	first, created := call()
	first.Want(http.StatusCreated)
	dbfx.Cleanup(t, `DELETE FROM issue WHERE id = $1`, created.ID)
	if created.ID == "" || created.Identifier == "" {
		t.Fatalf("created = %+v, want an id and identifier", created)
	}

	second, reused := call()
	second.Want(http.StatusOK)
	if reused.ID != created.ID {
		t.Fatalf("second call returned issue %s, want the first (%s)", reused.ID, created.ID)
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM issue WHERE project_id = $1`, projectID); n != 1 {
		t.Fatalf("project has %d issues, want exactly the holder", n)
	}

	var title, status string
	var parentSet, assigneeSet bool
	dbfx.QueryRow(t, `SELECT title, status, parent_issue_id IS NOT NULL, assignee_id IS NOT NULL FROM issue WHERE id = $1`, created.ID).
		Scan(&title, &status, &parentSet, &assigneeSet)
	if title != projectLibraryIssueTitle || status != "backlog" || parentSet || assigneeSet {
		t.Fatalf("holder = title %q status %q parent %v assignee %v, want a top-level unassigned backlog issue", title, status, parentSet, assigneeSet)
	}
}
