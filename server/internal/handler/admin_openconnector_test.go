package handler

import (
	"net/http"
	"strings"
	"testing"

	openconnector "github.com/multica-ai/multica/server/internal/integrations/openconnector"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// adminOCFixture wires a fake open-connector (github: oauth, openai: api key)
// into testHandler and grants the test user role. Policy rows a test creates
// through the handlers are cleaned up here.
func adminOCFixture(t *testing.T, role string) *db.Queries {
	t.Helper()
	if testHandler == nil {
		t.Skip("database not available")
	}
	grantPlatformAdmin(t, testUserID, role)
	withOpenConnector(t, testHandler, newOpenConnectorService(t, &ocFakeSDK{}))
	for _, svc := range []string{"github", "openai", "slack"} {
		dbfx.Cleanup(t, `DELETE FROM connector_service_policy WHERE provider = $1 AND service = $2`, openconnector.ProviderName, svc)
	}
	return db.New(testPool)
}

func adminOCServiceReq(method, service, suffix string, body any) *http.Request {
	return withURLParam(newRequest(method, "/api/admin/open-connector/services/"+service+suffix, body), "service", service)
}

func TestAdminOpenConnectorHealth(t *testing.T) {
	adminOCFixture(t, PlatformRoleViewer)
	var resp AdminOpenConnectorHealthResponse
	testutil.Call(t, adminRoute(PlatformRoleViewer, testHandler.AdminOpenConnectorHealth),
		newRequest("GET", "/api/admin/open-connector/health", nil)).Want(http.StatusOK).JSON(&resp)
	if !resp.Configured || resp.Health == nil || !resp.Health.Reachable || resp.Health.ProvidersTotal != 2 || resp.Health.OAuthConfigured != 1 || resp.Health.BaseURL != "https://oc.example.com" {
		t.Fatalf("health = %+v %+v", resp, resp.Health)
	}

	withOpenConnector(t, testHandler, nil)
	resp = AdminOpenConnectorHealthResponse{}
	testutil.Call(t, adminRoute(PlatformRoleViewer, testHandler.AdminOpenConnectorHealth),
		newRequest("GET", "/api/admin/open-connector/health", nil)).Want(http.StatusOK).JSON(&resp)
	if resp.Configured || resp.Health != nil {
		t.Fatalf("unwired health = %+v", resp)
	}
}

func TestAdminOpenConnectorEnableDisableIsAudited(t *testing.T) {
	adminOCFixture(t, PlatformRoleOperator)
	enable := adminRoute(PlatformRoleOperator, testHandler.AdminOpenConnectorEnableService)
	disable := adminRoute(PlatformRoleOperator, testHandler.AdminOpenConnectorDisableService)

	// Reason is mandatory; the slug must be one the instance offers.
	testutil.Call(t, enable, adminOCServiceReq("POST", "github", "/enable", adminReasonRequest{})).Want(http.StatusBadRequest)
	testutil.Call(t, enable, adminOCServiceReq("POST", "slack", "/enable", adminReasonRequest{Reason: "x"})).Want(http.StatusNotFound)
	testutil.Call(t, enable, adminOCServiceReq("POST", "bad.slug", "/enable", adminReasonRequest{Reason: "x"})).Want(http.StatusBadRequest)

	var policy AdminConnectorPolicyResponse
	testutil.Call(t, enable, adminOCServiceReq("POST", "github", "/enable", adminReasonRequest{Reason: "launch"})).Want(http.StatusOK).JSON(&policy)
	if policy.Service != "github" || !policy.Enabled || len(policy.AllowedTools) != 0 || len(policy.AllowedUsers) != 0 {
		t.Fatalf("policy after enable = %+v", policy)
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM admin_audit_log WHERE actor_user_id = $1 AND action = 'connector_service.enable' AND target_id = 'github'`, testUserID); n != 1 {
		t.Fatalf("enable audit rows = %d, want 1", n)
	}

	// Published services reach users through the regular catalog.
	user, _ := newOCUser(t)
	var apps []openconnector.AppView
	testutil.Call(t, testHandler.ListOpenConnectorApps, ocReq(user, http.MethodGet, "/", nil)).Want(http.StatusOK).JSON(&apps)
	if len(apps) != 1 || apps[0].Service != "github" {
		t.Fatalf("user apps after enable = %+v", apps)
	}

	// Disabling works even for a service the instance no longer offers.
	testutil.Call(t, disable, adminOCServiceReq("POST", "slack", "/disable", adminReasonRequest{Reason: "never offered"})).Want(http.StatusOK)
	testutil.Call(t, disable, adminOCServiceReq("POST", "github", "/disable", adminReasonRequest{Reason: "incident"})).Want(http.StatusOK).JSON(&policy)
	if policy.Enabled {
		t.Fatalf("policy after disable = %+v", policy)
	}
	apps = nil
	testutil.Call(t, testHandler.ListOpenConnectorApps, ocReq(user, http.MethodGet, "/", nil)).Want(http.StatusOK).JSON(&apps)
	if len(apps) != 0 {
		t.Fatalf("user apps after disable = %+v", apps)
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM admin_audit_log WHERE actor_user_id = $1 AND action = 'connector_service.disable'`, testUserID); n != 2 {
		t.Fatalf("disable audit rows = %d, want 2", n)
	}

	// Enabling needs a wired instance; a viewer cannot write at all.
	withOpenConnector(t, testHandler, nil)
	testutil.Call(t, enable, adminOCServiceReq("POST", "github", "/enable", adminReasonRequest{Reason: "x"})).Want(http.StatusForbidden)
	dbfx.Exec(t, `UPDATE platform_admin SET role = 'viewer' WHERE user_id = $1`, testUserID)
	testutil.Call(t, disable, adminOCServiceReq("POST", "github", "/disable", adminReasonRequest{Reason: "x"})).Want(http.StatusForbidden)
}

func TestAdminOpenConnectorPolicyResolvesUsersAndTools(t *testing.T) {
	adminOCFixture(t, PlatformRoleOperator)
	setPolicy := adminRoute(PlatformRoleOperator, testHandler.AdminOpenConnectorSetPolicy)
	aliceEmail := "oc-alice-" + testUserID[:8] + "@example.com"
	alice := dbfx.User(t, "Alice", aliceEmail)

	// Unknown email and malformed tool names are rejected before any write.
	testutil.Call(t, setPolicy, adminOCServiceReq("PUT", "github", "/policy", setConnectorPolicyRequest{
		AllowedUserEmails: []string{"nobody-" + testUserID[:8] + "@example.com"}, Reason: "x",
	})).Want(http.StatusNotFound)
	testutil.Call(t, setPolicy, adminOCServiceReq("PUT", "github", "/policy", setConnectorPolicyRequest{
		AllowedTools: []string{"rm -rf"}, Reason: "x",
	})).Want(http.StatusBadRequest)
	if n := dbfx.Count(t, `SELECT count(*) FROM connector_service_policy WHERE provider = $1 AND service = 'github'`, openconnector.ProviderName); n != 0 {
		t.Fatalf("policy rows after rejected writes = %d", n)
	}

	var policy AdminConnectorPolicyResponse
	testutil.Call(t, setPolicy, adminOCServiceReq("PUT", "github", "/policy", setConnectorPolicyRequest{
		AllowedTools:      []string{"github.create_issue", "list_repos", "create_issue"},
		AllowedUserEmails: []string{" " + strings.ToUpper(aliceEmail) + " ", aliceEmail},
		Reason:            "pilot group",
	})).Want(http.StatusOK).JSON(&policy)
	if policy.Enabled || strings.Join(policy.AllowedTools, ",") != "create_issue,list_repos" {
		t.Fatalf("policy = %+v", policy)
	}
	if len(policy.AllowedUsers) != 1 || policy.AllowedUsers[0].UserID != alice || policy.AllowedUsers[0].Email != aliceEmail {
		t.Fatalf("allowed users = %+v", policy.AllowedUsers)
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM admin_audit_log WHERE actor_user_id = $1 AND action = 'connector_service.policy' AND target_id = 'github' AND payload->'allowed_user_emails' ? $2`, testUserID, aliceEmail); n != 1 {
		t.Fatalf("policy audit rows = %d, want 1", n)
	}

	// Enabling afterwards keeps the allowlists; clearing them is an empty PUT.
	testutil.Call(t, adminRoute(PlatformRoleOperator, testHandler.AdminOpenConnectorEnableService),
		adminOCServiceReq("POST", "github", "/enable", adminReasonRequest{Reason: "go"})).Want(http.StatusOK).JSON(&policy)
	if !policy.Enabled || len(policy.AllowedTools) != 2 || len(policy.AllowedUsers) != 1 {
		t.Fatalf("policy after enable = %+v", policy)
	}
	other, _ := newOCUser(t)
	var apps []openconnector.AppView
	testutil.Call(t, testHandler.ListOpenConnectorApps, ocReq(other, http.MethodGet, "/", nil)).Want(http.StatusOK).JSON(&apps)
	if len(apps) != 0 {
		t.Fatalf("non-allowlisted user sees %+v", apps)
	}
	apps = nil
	testutil.Call(t, testHandler.ListOpenConnectorApps, ocReq(alice, http.MethodGet, "/", nil)).Want(http.StatusOK).JSON(&apps)
	if len(apps) != 1 {
		t.Fatalf("alice sees %+v", apps)
	}
	testutil.Call(t, setPolicy, adminOCServiceReq("PUT", "github", "/policy", setConnectorPolicyRequest{Reason: "open to all"})).Want(http.StatusOK).JSON(&policy)
	if !policy.Enabled || len(policy.AllowedTools) != 0 || len(policy.AllowedUsers) != 0 {
		t.Fatalf("policy after clear = %+v", policy)
	}
}

func TestAdminOpenConnectorServicesAndUsers(t *testing.T) {
	q := adminOCFixture(t, PlatformRoleViewer)
	ownerEmail := "oc-owner-" + testUserID[:8] + "@example.com"
	ownerStr := dbfx.User(t, "Owner", ownerEmail)
	owner := parseUUID(ownerStr)
	t.Cleanup(func() { dbfx.Exec(t, `DELETE FROM user_connector_connection WHERE user_id = $1`, ownerStr) })
	publishOCService(t, "github", owner)
	seedOCConnection(t, q, owner, "svc-1", true)
	seedOCConnection(t, q, owner, "svc-2", false)
	dbfx.Exec(t, `UPDATE user_connector_connection SET status = 'revoked' WHERE connection_id = 'svc-2' AND user_id = $1`, ownerStr)
	// A legacy row for a service the instance no longer offers still shows up.
	dbfx.Exec(t, `INSERT INTO connector_service_policy (provider, service, enabled) VALUES ($1, 'slack', FALSE)`, openconnector.ProviderName)

	var services AdminOpenConnectorServicesResponse
	testutil.Call(t, adminRoute(PlatformRoleViewer, testHandler.AdminOpenConnectorListServices),
		newRequest("GET", "/api/admin/open-connector/services", nil)).Want(http.StatusOK).JSON(&services)
	if !services.Configured || services.CatalogError != "" {
		t.Fatalf("services = %+v", services)
	}
	byService := map[string]AdminConnectorServiceResponse{}
	for _, item := range services.Items {
		byService[item.Service] = item
	}
	gh := byService["github"]
	if !gh.Available || !gh.Enabled || gh.Name != "GitHub" || strings.Join(gh.AuthTypes, ",") != "oauth" {
		t.Fatalf("github = %+v", gh)
	}
	if gh.Connections != (AdminConnectorCounts{Active: 1, Revoked: 1, Users: 1}) {
		t.Fatalf("github counts = %+v", gh.Connections)
	}
	if len(gh.AllowedUsers) != 1 || gh.AllowedUsers[0].Email != ownerEmail {
		t.Fatalf("github allowed users = %+v", gh.AllowedUsers)
	}
	if oa := byService["openai"]; !oa.Available || oa.Enabled || strings.Join(oa.AuthTypes, ",") != "api_key" {
		t.Fatalf("openai = %+v", oa)
	}
	if sl := byService["slack"]; sl.Available || sl.Enabled || sl.UpdatedAt == nil {
		t.Fatalf("slack = %+v", sl)
	}

	var users AdminPage[AdminConnectorUserResponse]
	testutil.Call(t, adminRoute(PlatformRoleViewer, testHandler.AdminOpenConnectorListUsers),
		newRequest("GET", "/api/admin/open-connector/users?service=github&q="+ownerEmail, nil)).Want(http.StatusOK).JSON(&users)
	if users.Total != 1 || len(users.Items) != 1 {
		t.Fatalf("users = %+v", users)
	}
	u := users.Items[0]
	if u.UserID != util.UUIDToString(owner) || u.Email != ownerEmail || strings.Join(u.Services, ",") != "github" || u.Connections != (AdminConnectorCounts{Active: 1, Revoked: 1}) || u.LastConnectedAt == nil {
		t.Fatalf("user row = %+v", u)
	}
	users = AdminPage[AdminConnectorUserResponse]{}
	testutil.Call(t, adminRoute(PlatformRoleViewer, testHandler.AdminOpenConnectorListUsers),
		newRequest("GET", "/api/admin/open-connector/users?service=openai", nil)).Want(http.StatusOK).JSON(&users)
	if users.Total != 0 {
		t.Fatalf("openai users = %+v", users)
	}

	// Without a wired instance the catalog is policy-only.
	withOpenConnector(t, testHandler, nil)
	services = AdminOpenConnectorServicesResponse{}
	testutil.Call(t, adminRoute(PlatformRoleViewer, testHandler.AdminOpenConnectorListServices),
		newRequest("GET", "/api/admin/open-connector/services", nil)).Want(http.StatusOK).JSON(&services)
	if services.Configured || len(services.Items) != 2 {
		t.Fatalf("unwired services = %+v", services)
	}
	for _, item := range services.Items {
		if item.Available {
			t.Fatalf("unwired item available: %+v", item)
		}
	}
}
