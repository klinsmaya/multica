package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func readConnectorAllowlistColumn(t *testing.T, agentID string) ([]string, bool) {
	t.Helper()
	var stored []string
	var isNull bool
	if err := testPool.QueryRow(context.Background(), `
		SELECT COALESCE(connector_service_allowlist, '{}'::text[]), connector_service_allowlist IS NULL
		FROM agent WHERE id = $1
	`, agentID).Scan(&stored, &isNull); err != nil {
		t.Fatalf("read connector allowlist: %v", err)
	}
	return stored, isNull
}

func TestUpdateAgent_ConnectorAllowlistRoundtripAndNonOwnerDrop(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withOpenConnector(t, testHandler, newOpenConnectorService(t, &ocFakeSDK{}))
	agentID, ownerID := allowlistFixture(t)

	put := func(userID string, body map[string]any) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		testHandler.UpdateAgent(w, withURLParam(newRequestAs(userID, http.MethodPut, "/api/agents/"+agentID, body), "id", agentID))
		return w
	}

	if w := put(ownerID, map[string]any{"connector_service_allowlist": []string{" GitHub ", "github", "Notion", ""}}); w.Code != http.StatusOK {
		t.Fatalf("owner write: %d %s", w.Code, w.Body.String())
	}
	stored, isNull := readConnectorAllowlistColumn(t, agentID)
	if isNull || len(stored) != 2 || stored[0] != "github" || stored[1] != "notion" {
		t.Fatalf("stored = %v null=%v", stored, isNull)
	}

	// Workspace owner (not agent owner) is silently dropped.
	if w := put(testUserID, map[string]any{"connector_service_allowlist": []string{"slack"}}); w.Code != http.StatusOK {
		t.Fatalf("admin write: %d %s", w.Code, w.Body.String())
	}
	if stored, _ = readConnectorAllowlistColumn(t, agentID); len(stored) != 2 {
		t.Fatalf("non-owner write changed allowlist: %v", stored)
	}

	if w := put(ownerID, map[string]any{"connector_service_allowlist": []string{}}); w.Code != http.StatusOK {
		t.Fatalf("owner empty write: %d", w.Code)
	}
	if stored, isNull = readConnectorAllowlistColumn(t, agentID); isNull || len(stored) != 0 {
		t.Fatalf("empty write: %v null=%v", stored, isNull)
	}
	if w := put(ownerID, map[string]any{"connector_service_allowlist": nil}); w.Code != http.StatusOK {
		t.Fatalf("owner null write: %d", w.Code)
	}
	if _, isNull = readConnectorAllowlistColumn(t, agentID); !isNull {
		t.Fatal("explicit null must clear to NULL")
	}
}

func TestGetAgent_ConnectorAllowlistVisibility(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, ownerID := allowlistFixture(t)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent SET connector_service_allowlist = ARRAY['github'] WHERE id = $1`, agentID); err != nil {
		t.Fatal(err)
	}
	get := func(userID string) AgentResponse {
		w := httptest.NewRecorder()
		testHandler.GetAgent(w, withURLParam(newRequestAs(userID, http.MethodGet, "/api/agents/"+agentID, nil), "id", agentID))
		if w.Code != http.StatusOK {
			t.Fatalf("GetAgent: %d %s", w.Code, w.Body.String())
		}
		var resp AgentResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return resp
	}

	withOpenConnector(t, testHandler, newOpenConnectorService(t, &ocFakeSDK{}))
	if r := get(ownerID); len(r.ConnectorServiceAllowlist) != 1 || r.ConnectorServiceAllowlistRedacted {
		t.Errorf("owner view = %v redacted=%v", r.ConnectorServiceAllowlist, r.ConnectorServiceAllowlistRedacted)
	}
	// Workspace owner is not the agent owner: redacted, no bypass.
	if r := get(testUserID); len(r.ConnectorServiceAllowlist) != 0 || !r.ConnectorServiceAllowlistRedacted {
		t.Errorf("non-owner view = %v redacted=%v", r.ConnectorServiceAllowlist, r.ConnectorServiceAllowlistRedacted)
	}

	withOpenConnector(t, testHandler, nil)
	if r := get(ownerID); len(r.ConnectorServiceAllowlist) != 0 || r.ConnectorServiceAllowlistRedacted {
		t.Errorf("unwired owner view = %v redacted=%v", r.ConnectorServiceAllowlist, r.ConnectorServiceAllowlistRedacted)
	}
}
