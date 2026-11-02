package handler

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

type claimProjectFilesPayload struct {
	Task *struct {
		ProjectID    string `json:"project_id"`
		ProjectFiles []struct {
			AttachmentID string `json:"attachment_id"`
			Filename     string `json:"filename"`
			SizeBytes    int64  `json:"size_bytes"`
			ContentType  string `json:"content_type"`
			UploadedAt   string `json:"uploaded_at"`
			UploaderType string `json:"uploader_type"`
			UploaderID   string `json:"uploader_id"`
		} `json:"project_files"`
	} `json:"task"`
}

// claimIssueTaskInProject queues a task on a fresh issue of the project and
// claims it through the daemon endpoint, the path every agent run takes.
func claimIssueTaskInProject(t *testing.T, projectID string, number int, tokenName string) claimProjectFilesPayload {
	t.Helper()
	var agentID, runtimeID string
	dbfx.QueryRow(t,
		`SELECT id, runtime_id FROM agent WHERE workspace_id = $1 LIMIT 1`,
		testWorkspaceID,
	).Scan(&agentID, &runtimeID)

	issueID := dbfx.Issue(t, "works in the project", testutil.Cols{
		"project_id": projectID,
		"priority":   "medium",
		"number":     number,
	})
	dbfx.Task(t, agentID, testutil.Cols{
		"runtime_id": runtimeID,
		"issue_id":   issueID,
	})

	req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+runtimeID+"/claim", nil, testWorkspaceID, tokenName)
	req = withURLParam(req, "runtimeId", runtimeID)
	w := testutil.Call(t, testHandler.ClaimTaskByRuntime, req).Want(http.StatusOK)

	var resp claimProjectFilesPayload
	w.JSON(&resp)
	if resp.Task == nil {
		t.Fatal("expected task in response")
	}
	if resp.Task.ProjectID != projectID {
		t.Fatalf("project_id = %q, want %q", resp.Task.ProjectID, projectID)
	}
	return resp
}

// The files members upload in the project Library hang off the "Project files"
// holder issue. A claim for ANY issue of the project must list the newest
// version of each of them, so every thread of the project can read them.
func TestClaimTask_ProjectFilesListLibraryNewestVersionPerFile(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	projectID := dbfx.Project(t, "Claim project files")
	otherProjectID := dbfx.Project(t, "Claim project files, other project")
	holderID := dbfx.Issue(t, projectLibraryIssueTitle, testutil.Cols{"project_id": projectID, "number": 88301})
	otherHolderID := dbfx.Issue(t, projectLibraryIssueTitle, testutil.Cols{"project_id": otherProjectID, "number": 88302})
	nonHolderID := dbfx.Issue(t, "A regular issue", testutil.Cols{"project_id": projectID, "number": 88303})

	// Two uploads of one file (media type compared without parameters): only
	// the newer one is listed.
	insertProjectAttachment(t, holderID, "guide.pdf", "member", testutil.Cols{
		"content_type": "application/pdf",
		"size_bytes":   100,
		"created_at":   testutil.Raw("now() - interval '3 hours'"),
	})
	newestGuide := insertProjectAttachment(t, holderID, "guide.pdf", "member", testutil.Cols{
		"content_type": "Application/PDF; charset=binary",
		"size_bytes":   200,
		"created_at":   testutil.Raw("now() - interval '2 hours'"),
	})
	newestNotes := insertProjectAttachment(t, holderID, "notes.txt", "member", testutil.Cols{
		"content_type": "text/plain",
		"size_bytes":   30,
		"created_at":   testutil.Raw("now() - interval '1 hour'"),
	})
	// Not library files: another project's holder, a regular issue of this
	// project, a snapshot copy, and a file on a deleted comment.
	insertProjectAttachment(t, otherHolderID, "foreign.pdf", "member")
	insertProjectAttachment(t, nonHolderID, "not-in-library.pdf", "member")
	// source_context_id carries no foreign key, so any UUID marks a snapshot copy.
	insertProjectAttachment(t, holderID, "snapshot.pdf", "member", testutil.Cols{
		"source_context_id": "11111111-1111-4111-8111-111111111111",
	})
	deletedComment := dbfx.Comment(t, holderID, "")
	dbfx.Exec(t, `UPDATE comment SET deleted_at = now() WHERE id = $1`, deletedComment)
	insertProjectAttachment(t, holderID, "tombstone.pdf", "member", testutil.Cols{"comment_id": deletedComment})

	resp := claimIssueTaskInProject(t, projectID, 88304, "test-claim-project-files")

	files := resp.Task.ProjectFiles
	if len(files) != 2 {
		t.Fatalf("project_files = %+v, want exactly notes.txt and guide.pdf", files)
	}
	if files[0].AttachmentID != newestNotes || files[0].Filename != "notes.txt" {
		t.Errorf("project_files[0] = %+v, want newest file notes.txt (%s) first", files[0], newestNotes)
	}
	if files[1].AttachmentID != newestGuide || files[1].SizeBytes != 200 {
		t.Errorf("project_files[1] = %+v, want the newest guide.pdf version (%s, 200 bytes)", files[1], newestGuide)
	}
	if files[0].ContentType != "text/plain" || files[0].SizeBytes != 30 ||
		files[0].UploaderType != "member" || files[0].UploaderID != testUserID || files[0].UploadedAt == "" {
		t.Errorf("project_files[0] fields = %+v, want content type, size, uploader and upload time filled", files[0])
	}
}

// A project nobody uploaded a library file to has no holder issue: the claim
// succeeds and carries no project_files.
func TestClaimTask_ProjectWithoutLibraryHasNoProjectFiles(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	projectID := dbfx.Project(t, "Claim project without library")
	resp := claimIssueTaskInProject(t, projectID, 88305, "test-claim-project-no-library")
	if len(resp.Task.ProjectFiles) != 0 {
		t.Fatalf("project_files = %+v, want none without a library issue", resp.Task.ProjectFiles)
	}
}

// The brief lists every file, so a large library is cut to the newest
// projectFilesClaimLimit.
func TestClaimTask_ProjectFilesCappedAtNewest(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	projectID := dbfx.Project(t, "Claim project files cap")
	holderID := dbfx.Issue(t, projectLibraryIssueTitle, testutil.Cols{"project_id": projectID, "number": 88306})
	total := projectFilesClaimLimit + 5
	for i := 0; i < total; i++ {
		insertProjectAttachment(t, holderID, fmt.Sprintf("file-%03d.txt", i), "member", testutil.Cols{
			"content_type": "text/plain",
			"created_at":   testutil.Raw(fmt.Sprintf("now() - interval '%d minutes'", total-i)),
		})
	}

	resp := claimIssueTaskInProject(t, projectID, 88307, "test-claim-project-files-cap")
	files := resp.Task.ProjectFiles
	if len(files) != projectFilesClaimLimit {
		t.Fatalf("project_files = %d entries, want the cap of %d", len(files), projectFilesClaimLimit)
	}
	if want := fmt.Sprintf("file-%03d.txt", total-1); files[0].Filename != want {
		t.Errorf("first file = %q, want the newest upload %q", files[0].Filename, want)
	}
	if want := fmt.Sprintf("file-%03d.txt", total-projectFilesClaimLimit); files[len(files)-1].Filename != want {
		t.Errorf("last file = %q, want %q (older uploads are dropped)", files[len(files)-1].Filename, want)
	}
}
