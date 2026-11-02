package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func storedProjectLeadCoordinates(t *testing.T, projectID string) bool {
	t.Helper()
	stored, err := testHandler.Queries.GetProjectInWorkspace(context.Background(), db.GetProjectInWorkspaceParams{
		ID: parseUUID(projectID), WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatal(err)
	}
	return stored.LeadCoordinates
}

func TestCreateProjectLeadCoordinates(t *testing.T) {
	agentID := dbfx.Agent(t, "coordinating lead", testRuntimeID)

	t.Run("defaults to off", func(t *testing.T) {
		var created ProjectResponse
		testutil.Call(t, testHandler.CreateProject, newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
			"title": "lead coordinates default",
		})).Want(http.StatusCreated).JSON(&created)
		if created.LeadCoordinates || storedProjectLeadCoordinates(t, created.ID) {
			t.Fatal("lead_coordinates should default to false")
		}
	})

	t.Run("round-trips with an agent lead", func(t *testing.T) {
		var created ProjectResponse
		testutil.Call(t, testHandler.CreateProject, newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
			"title":            "lead coordinates on",
			"lead_type":        "agent",
			"lead_id":          agentID,
			"lead_coordinates": true,
		})).Want(http.StatusCreated).JSON(&created)
		if !created.LeadCoordinates || !storedProjectLeadCoordinates(t, created.ID) {
			t.Fatal("lead_coordinates should be stored and returned as true")
		}
	})

	t.Run("rejected without an agent lead", func(t *testing.T) {
		for name, body := range map[string]map[string]any{
			"no lead":     {"title": "no lead", "lead_coordinates": true},
			"member lead": {"title": "member lead", "lead_type": "member", "lead_id": testUserID, "lead_coordinates": true},
		} {
			var errBody struct {
				Code string `json:"code"`
			}
			testutil.Call(t, testHandler.CreateProject, newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, body)).
				Want(http.StatusBadRequest).JSON(&errBody)
			if errBody.Code != errProjectLeadNotAgent {
				t.Errorf("%s: code = %q, want %q", name, errBody.Code, errProjectLeadNotAgent)
			}
		}
	})
}

func TestUpdateProjectLeadCoordinates(t *testing.T) {
	agentID := dbfx.Agent(t, "update coordinating lead", testRuntimeID)

	update := func(projectID string, body map[string]any) *testutil.Response {
		return testutil.Call(t, testHandler.UpdateProject, withURLParam(newRequest("PUT", "/api/projects/"+projectID, body), "id", projectID))
	}

	t.Run("toggles with an agent lead and leaves other updates alone", func(t *testing.T) {
		projectID := dbfx.Project(t, "toggle", testutil.Cols{"lead_type": "agent", "lead_id": agentID})

		var on ProjectResponse
		update(projectID, map[string]any{"lead_coordinates": true}).Want(http.StatusOK).JSON(&on)
		if !on.LeadCoordinates || !storedProjectLeadCoordinates(t, projectID) {
			t.Fatal("lead_coordinates should be true after enabling")
		}

		var renamed ProjectResponse
		update(projectID, map[string]any{"title": "renamed"}).Want(http.StatusOK).JSON(&renamed)
		if !renamed.LeadCoordinates || !storedProjectLeadCoordinates(t, projectID) {
			t.Fatal("an update that omits lead_coordinates must keep it")
		}

		var off ProjectResponse
		update(projectID, map[string]any{"lead_coordinates": false}).Want(http.StatusOK).JSON(&off)
		if off.LeadCoordinates || storedProjectLeadCoordinates(t, projectID) {
			t.Fatal("lead_coordinates should be false after disabling")
		}
	})

	t.Run("can be enabled together with the agent lead", func(t *testing.T) {
		projectID := dbfx.Project(t, "enable with lead")
		var got ProjectResponse
		update(projectID, map[string]any{"lead_type": "agent", "lead_id": agentID, "lead_coordinates": true}).
			Want(http.StatusOK).JSON(&got)
		if !got.LeadCoordinates {
			t.Fatal("lead_coordinates should be true")
		}
	})

	t.Run("rejected when the lead is not an agent", func(t *testing.T) {
		for name, tc := range map[string]struct {
			cols testutil.Cols
			body map[string]any
		}{
			"no lead":            {testutil.Cols{}, map[string]any{"lead_coordinates": true}},
			"member lead":        {testutil.Cols{"lead_type": "member", "lead_id": testUserID}, map[string]any{"lead_coordinates": true}},
			"lead moves to user": {testutil.Cols{"lead_type": "agent", "lead_id": agentID}, map[string]any{"lead_type": "member", "lead_id": testUserID, "lead_coordinates": true}},
			"lead cleared":       {testutil.Cols{"lead_type": "agent", "lead_id": agentID}, map[string]any{"lead_type": nil, "lead_id": nil, "lead_coordinates": true}},
		} {
			projectID := dbfx.Project(t, "reject "+name, tc.cols)
			var errBody struct {
				Code string `json:"code"`
			}
			update(projectID, tc.body).Want(http.StatusBadRequest).JSON(&errBody)
			if errBody.Code != errProjectLeadNotAgent {
				t.Errorf("%s: code = %q, want %q", name, errBody.Code, errProjectLeadNotAgent)
			}
			if storedProjectLeadCoordinates(t, projectID) {
				t.Errorf("%s: a rejected update must not store the flag", name)
			}
		}
	})

	t.Run("flag survives the lead moving away from an agent", func(t *testing.T) {
		projectID := dbfx.Project(t, "lead leaves", testutil.Cols{"lead_type": "agent", "lead_id": agentID, "lead_coordinates": true})
		var got ProjectResponse
		update(projectID, map[string]any{"lead_type": "member", "lead_id": testUserID}).Want(http.StatusOK).JSON(&got)
		if !got.LeadCoordinates || !storedProjectLeadCoordinates(t, projectID) {
			t.Fatal("the stored flag should be kept; it only has no effect without an agent lead")
		}
	})
}

// The claim carries the coordinator flags the brief's Project coordinator
// section needs: the project opt-in, whether the claimed agent is the project
// lead on its own assigned issue, and whether the issue is a sub-issue.
func TestClaimTask_ProjectCoordinatorFlags(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	var leadID, runtimeID string
	dbfx.QueryRow(t,
		`SELECT id, runtime_id FROM agent WHERE workspace_id = $1 LIMIT 1`,
		testWorkspaceID,
	).Scan(&leadID, &runtimeID)
	otherID := dbfx.Agent(t, "not the lead", runtimeID)

	projectID := dbfx.Project(t, "claim coordinator", testutil.Cols{
		"lead_type":        "agent",
		"lead_id":          leadID,
		"lead_coordinates": true,
	})
	parentID := dbfx.Issue(t, "feed message parent", testutil.Cols{"project_id": projectID})

	for _, tc := range []struct {
		name                                         string
		agentID                                      string
		issueCols                                    testutil.Cols
		wantCoordinates, wantAssigneeLead, wantChild bool
	}{
		{
			name: "top-level issue assigned to the lead", agentID: leadID,
			issueCols:       testutil.Cols{"assignee_type": "agent", "assignee_id": leadID},
			wantCoordinates: true, wantAssigneeLead: true,
		},
		{
			name: "sub-issue assigned to the lead", agentID: leadID,
			issueCols:       testutil.Cols{"assignee_type": "agent", "assignee_id": leadID, "parent_issue_id": parentID},
			wantCoordinates: true, wantAssigneeLead: true, wantChild: true,
		},
		{
			name: "issue assigned to another agent", agentID: otherID,
			issueCols:       testutil.Cols{"assignee_type": "agent", "assignee_id": otherID},
			wantCoordinates: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cols := testutil.Cols{"project_id": projectID}
			for k, v := range tc.issueCols {
				cols[k] = v
			}
			issueID := dbfx.Issue(t, "feed message", cols)
			dbfx.Task(t, tc.agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID})

			req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+runtimeID+"/claim", nil, testWorkspaceID, "test-claim-project-coordinator")
			req = withURLParam(req, "runtimeId", runtimeID)
			var resp struct {
				Task *struct {
					ProjectLeadCoordinates bool `json:"project_lead_coordinates"`
					AssigneeIsProjectLead  bool `json:"assignee_is_project_lead"`
					IssueHasParent         bool `json:"issue_has_parent"`
				} `json:"task"`
			}
			testutil.Call(t, testHandler.ClaimTaskByRuntime, req).Want(http.StatusOK).JSON(&resp)
			if resp.Task == nil {
				t.Fatal("expected task in response")
			}
			if resp.Task.ProjectLeadCoordinates != tc.wantCoordinates {
				t.Errorf("project_lead_coordinates = %v, want %v", resp.Task.ProjectLeadCoordinates, tc.wantCoordinates)
			}
			if resp.Task.AssigneeIsProjectLead != tc.wantAssigneeLead {
				t.Errorf("assignee_is_project_lead = %v, want %v", resp.Task.AssigneeIsProjectLead, tc.wantAssigneeLead)
			}
			if resp.Task.IssueHasParent != tc.wantChild {
				t.Errorf("issue_has_parent = %v, want %v", resp.Task.IssueHasParent, tc.wantChild)
			}
		})
	}
}
