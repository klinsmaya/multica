package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// adminRoute wraps a handler in the platform-admin gate the router mounts,
// so the tests exercise role resolution and the handler together.
func adminRoute(minRole string, h http.HandlerFunc) http.HandlerFunc {
	return testHandler.RequirePlatformAdmin(minRole)(h).ServeHTTP
}

func grantPlatformAdmin(t *testing.T, userID, role string) {
	t.Helper()
	dbfx.Exec(t, `INSERT INTO platform_admin (user_id, role) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET role = EXCLUDED.role`, userID, role)
	dbfx.Cleanup(t, `DELETE FROM platform_admin WHERE user_id = $1`, userID)
	dbfx.Cleanup(t, `DELETE FROM admin_audit_log WHERE actor_user_id = $1`, userID)
}

func TestAdminRequiresPlatformRole(t *testing.T) {
	req := newRequest("GET", "/api/admin/me", nil)
	testutil.Call(t, adminRoute(PlatformRoleViewer, testHandler.AdminMe), req).Want(http.StatusForbidden)

	grantPlatformAdmin(t, testUserID, PlatformRoleViewer)
	var me AdminMeResponse
	testutil.Call(t, adminRoute(PlatformRoleViewer, testHandler.AdminMe), newRequest("GET", "/api/admin/me", nil)).
		Want(http.StatusOK).JSON(&me)
	if me.Role != PlatformRoleViewer || me.UserID != testUserID {
		t.Fatalf("me = %+v, want viewer %s", me, testUserID)
	}

	// A viewer cannot reach operator routes.
	target := dbfx.User(t, "Target", "admin-target-"+testUserID[:8]+"@example.com")
	req = withURLParam(newRequest("POST", "/api/admin/users/"+target+"/disable", adminReasonRequest{Reason: "spam"}), "id", target)
	testutil.Call(t, adminRoute(PlatformRoleOperator, testHandler.AdminDisableUser), req).Want(http.StatusForbidden)
}

func TestAdminBootstrapEmailGrantsSuperAdmin(t *testing.T) {
	email := "bootstrap-" + testUserID[:8] + "@example.com"
	userID := dbfx.User(t, "Bootstrap", email)
	dbfx.Cleanup(t, `DELETE FROM platform_admin WHERE user_id = $1`, userID)

	prev := testHandler.cfg.PlatformAdminEmails
	testHandler.cfg.PlatformAdminEmails = []string{" " + email + " "}
	t.Cleanup(func() { testHandler.cfg.PlatformAdminEmails = prev })

	req := newRequest("GET", "/api/admin/me", nil)
	req.Header.Set("X-User-ID", userID)
	var me AdminMeResponse
	testutil.Call(t, adminRoute(PlatformRoleSuperAdmin, testHandler.AdminMe), req).Want(http.StatusOK).JSON(&me)
	if me.Role != PlatformRoleSuperAdmin {
		t.Fatalf("role = %q, want super_admin", me.Role)
	}
}

func TestAdminDisableUserIsAuditedAndEnforced(t *testing.T) {
	grantPlatformAdmin(t, testUserID, PlatformRoleOperator)
	email := "disable-me-" + testUserID[:8] + "@example.com"
	target := dbfx.User(t, "Disable Me", email)
	t.Cleanup(func() { auth.MarkUserEnabled(target, email) })

	// Reason is mandatory.
	req := withURLParam(newRequest("POST", "/api/admin/users/"+target+"/disable", adminReasonRequest{}), "id", target)
	testutil.Call(t, adminRoute(PlatformRoleOperator, testHandler.AdminDisableUser), req).Want(http.StatusBadRequest)

	req = withURLParam(newRequest("POST", "/api/admin/users/"+target+"/disable", adminReasonRequest{Reason: "abuse report #12"}), "id", target)
	var user AdminUserResponse
	testutil.Call(t, adminRoute(PlatformRoleOperator, testHandler.AdminDisableUser), req).Want(http.StatusOK).JSON(&user)
	if user.DisabledAt == nil || user.DisabledReason != "abuse report #12" {
		t.Fatalf("user after disable = %+v", user)
	}
	if !auth.IsDisabledUser(target, email) {
		t.Fatal("disabled user must be rejected by the in-process set immediately")
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM admin_audit_log WHERE actor_user_id = $1 AND action = 'user.disable' AND target_id = $2`, testUserID, target); n != 1 {
		t.Fatalf("audit rows = %d, want 1", n)
	}

	// Disabling twice is a conflict and writes no second audit row.
	req = withURLParam(newRequest("POST", "/api/admin/users/"+target+"/disable", adminReasonRequest{Reason: "again"}), "id", target)
	testutil.Call(t, adminRoute(PlatformRoleOperator, testHandler.AdminDisableUser), req).Want(http.StatusConflict)
	if n := dbfx.Count(t, `SELECT count(*) FROM admin_audit_log WHERE actor_user_id = $1 AND action = 'user.disable'`, testUserID); n != 1 {
		t.Fatalf("audit rows after conflict = %d, want 1", n)
	}

	req = withURLParam(newRequest("POST", "/api/admin/users/"+target+"/enable", adminReasonRequest{Reason: "resolved"}), "id", target)
	testutil.Call(t, adminRoute(PlatformRoleOperator, testHandler.AdminEnableUser), req).Want(http.StatusOK).JSON(&user)
	if user.DisabledAt != nil || auth.IsDisabledUser(target, email) {
		t.Fatalf("user still disabled after enable: %+v", user)
	}

	// The audit log lists both actions, newest first.
	var page AdminPage[AdminAuditEntryResponse]
	testutil.Call(t, adminRoute(PlatformRoleViewer, testHandler.AdminListAudit),
		newRequest("GET", "/api/admin/audit?target_id="+target, nil)).Want(http.StatusOK).JSON(&page)
	if page.Total != 2 || len(page.Items) != 2 || page.Items[0].Action != "user.enable" {
		t.Fatalf("audit page = %+v", page)
	}
}

func TestAdminCannotDisableSelfOrPlatformAdmin(t *testing.T) {
	grantPlatformAdmin(t, testUserID, PlatformRoleSuperAdmin)
	req := withURLParam(newRequest("POST", "/api/admin/users/"+testUserID+"/disable", adminReasonRequest{Reason: "x"}), "id", testUserID)
	testutil.Call(t, adminRoute(PlatformRoleOperator, testHandler.AdminDisableUser), req).Want(http.StatusBadRequest)

	other := dbfx.User(t, "Other Admin", "other-admin-"+testUserID[:8]+"@example.com")
	grantPlatformAdmin(t, other, PlatformRoleViewer)
	req = withURLParam(newRequest("POST", "/api/admin/users/"+other+"/disable", adminReasonRequest{Reason: "x"}), "id", other)
	testutil.Call(t, adminRoute(PlatformRoleOperator, testHandler.AdminDisableUser), req).Want(http.StatusConflict)
}

func TestAdminPlatformRoleGrantAndSelfProtection(t *testing.T) {
	grantPlatformAdmin(t, testUserID, PlatformRoleSuperAdmin)
	otherEmail := "colleague-" + testUserID[:8] + "@example.com"
	other := dbfx.User(t, "Colleague", otherEmail)
	dbfx.Cleanup(t, `DELETE FROM platform_admin WHERE user_id = $1`, other)

	// Granting a role by email works for a super_admin and is audited.
	var pa PlatformAdminResponse
	testutil.Call(t, adminRoute(PlatformRoleSuperAdmin, testHandler.AdminSetPlatformAdmin),
		newRequest("PUT", "/api/admin/admins", setPlatformAdminRequest{Email: otherEmail, Role: PlatformRoleOperator, Reason: "onboarding"})).
		Want(http.StatusOK).JSON(&pa)
	if pa.Role != PlatformRoleOperator || pa.UserID != other {
		t.Fatalf("granted = %+v", pa)
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM admin_audit_log WHERE actor_user_id = $1 AND action = 'platform_admin.set' AND target_id = $2`, testUserID, other); n != 1 {
		t.Fatalf("audit rows = %d, want 1", n)
	}

	// Unknown email and invalid role are rejected.
	testutil.Call(t, adminRoute(PlatformRoleSuperAdmin, testHandler.AdminSetPlatformAdmin),
		newRequest("PUT", "/api/admin/admins", setPlatformAdminRequest{Email: "nobody-" + testUserID[:8] + "@example.com", Role: PlatformRoleViewer, Reason: "x"})).
		Want(http.StatusNotFound)
	testutil.Call(t, adminRoute(PlatformRoleSuperAdmin, testHandler.AdminSetPlatformAdmin),
		newRequest("PUT", "/api/admin/admins", setPlatformAdminRequest{Email: otherEmail, Role: "root", Reason: "x"})).
		Want(http.StatusBadRequest)

	// An admin can neither lower nor remove their own role.
	me, _ := testHandler.Queries.GetUser(t.Context(), parseUUID(testUserID))
	testutil.Call(t, adminRoute(PlatformRoleSuperAdmin, testHandler.AdminSetPlatformAdmin),
		newRequest("PUT", "/api/admin/admins", setPlatformAdminRequest{Email: me.Email, Role: PlatformRoleViewer, Reason: "x"})).
		Want(http.StatusBadRequest)
	req := withURLParam(newRequest("DELETE", "/api/admin/admins/"+testUserID, adminReasonRequest{Reason: "x"}), "userId", testUserID)
	testutil.Call(t, adminRoute(PlatformRoleSuperAdmin, testHandler.AdminRemovePlatformAdmin), req).Want(http.StatusBadRequest)

	// Removing a colleague works, and an operator cannot reach the route.
	req = withURLParam(newRequest("DELETE", "/api/admin/admins/"+other, adminReasonRequest{Reason: "left the team"}), "userId", other)
	testutil.Call(t, adminRoute(PlatformRoleSuperAdmin, testHandler.AdminRemovePlatformAdmin), req).Want(http.StatusOK)
	dbfx.Exec(t, `UPDATE platform_admin SET role = 'operator' WHERE user_id = $1`, testUserID)
	testutil.Call(t, adminRoute(PlatformRoleSuperAdmin, testHandler.AdminListPlatformAdmins),
		newRequest("GET", "/api/admin/admins", nil)).Want(http.StatusForbidden)
}

func TestAdminListsAndOverview(t *testing.T) {
	grantPlatformAdmin(t, testUserID, PlatformRoleViewer)

	var users AdminPage[AdminUserResponse]
	testutil.Call(t, adminRoute(PlatformRoleViewer, testHandler.AdminListUsers),
		newRequest("GET", "/api/admin/users?q="+testUserID[:8]+"&page_size=5", nil)).Want(http.StatusOK).JSON(&users)
	if users.PageSize != 5 {
		t.Fatalf("page_size = %d", users.PageSize)
	}

	var detail AdminUserDetailResponse
	testutil.Call(t, adminRoute(PlatformRoleViewer, testHandler.AdminGetUser),
		withURLParam(newRequest("GET", "/api/admin/users/"+testUserID, nil), "id", testUserID)).Want(http.StatusOK).JSON(&detail)
	if detail.ID != testUserID || len(detail.Workspaces) == 0 {
		t.Fatalf("detail = %+v", detail)
	}

	var ws AdminWorkspaceDetailResponse
	testutil.Call(t, adminRoute(PlatformRoleViewer, testHandler.AdminGetWorkspace),
		withURLParam(newRequest("GET", "/api/admin/workspaces/"+testWorkspaceID, nil), "id", testWorkspaceID)).Want(http.StatusOK).JSON(&ws)
	if ws.ID != testWorkspaceID || len(ws.Members) == 0 {
		t.Fatalf("workspace = %+v", ws)
	}

	var overview AdminOverviewResponse
	testutil.Call(t, adminRoute(PlatformRoleViewer, testHandler.AdminOverview),
		newRequest("GET", "/api/admin/overview", nil)).Want(http.StatusOK).JSON(&overview)
	if overview.UsersTotal < 1 || overview.WorkspacesTotal < 1 {
		t.Fatalf("overview = %+v", overview)
	}
}
