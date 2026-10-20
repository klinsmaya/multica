import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type {
  AdminAuditListParams,
  AdminUserListParams,
  AdminWorkspaceListParams,
} from "./schemas";

// Platform-admin data is account-level, not workspace-scoped, so these keys
// deliberately carry no wsId.
export const adminKeys = {
  all: ["admin"] as const,
  me: () => [...adminKeys.all, "me"] as const,
  overview: () => [...adminKeys.all, "overview"] as const,
  users: (params: AdminUserListParams) =>
    [...adminKeys.all, "users", params] as const,
  user: (id: string) => [...adminKeys.all, "user", id] as const,
  workspaces: (params: AdminWorkspaceListParams) =>
    [...adminKeys.all, "workspaces", params] as const,
  workspace: (id: string) => [...adminKeys.all, "workspace", id] as const,
  audit: (params: AdminAuditListParams) =>
    [...adminKeys.all, "audit", params] as const,
  admins: () => [...adminKeys.all, "admins"] as const,
};

export function adminMeOptions() {
  return queryOptions({
    queryKey: adminKeys.me(),
    queryFn: () => api.adminMe(),
    // A 403 means "not a platform admin"; retrying would not change that.
    retry: false,
    staleTime: 60_000,
  });
}

export function adminOverviewOptions() {
  return queryOptions({
    queryKey: adminKeys.overview(),
    queryFn: () => api.adminOverview(),
    staleTime: 30_000,
  });
}

export function adminUsersOptions(params: AdminUserListParams) {
  return queryOptions({
    queryKey: adminKeys.users(params),
    queryFn: () => api.adminListUsers(params),
  });
}

export function adminUserOptions(id: string) {
  return queryOptions({
    queryKey: adminKeys.user(id),
    queryFn: () => api.adminGetUser(id),
  });
}

export function adminWorkspacesOptions(params: AdminWorkspaceListParams) {
  return queryOptions({
    queryKey: adminKeys.workspaces(params),
    queryFn: () => api.adminListWorkspaces(params),
  });
}

export function adminWorkspaceOptions(id: string) {
  return queryOptions({
    queryKey: adminKeys.workspace(id),
    queryFn: () => api.adminGetWorkspace(id),
  });
}

export function adminAuditOptions(params: AdminAuditListParams) {
  return queryOptions({
    queryKey: adminKeys.audit(params),
    queryFn: () => api.adminListAudit(params),
  });
}

export function adminPlatformAdminsOptions() {
  return queryOptions({
    queryKey: adminKeys.admins(),
    queryFn: () => api.adminListPlatformAdmins(),
  });
}
