import { z } from "zod";

// Response contracts for /api/admin/*. Every shape carries defaults so an
// older or partially failing backend degrades to an empty page instead of a
// crash — the same contract packages/core/api/schemas.ts applies elsewhere.

export const PLATFORM_ROLES = ["super_admin", "operator", "viewer"] as const;
export type PlatformRole = (typeof PLATFORM_ROLES)[number];

const PlatformRoleSchema = z
  .enum(PLATFORM_ROLES)
  .catch("viewer" as PlatformRole);

export const AdminMeSchema = z
  .object({
    user_id: z.string().default(""),
    email: z.string().default(""),
    name: z.string().default(""),
    role: PlatformRoleSchema.default("viewer"),
  })
  .loose();
export type AdminMe = z.infer<typeof AdminMeSchema>;
export const EMPTY_ADMIN_ME: AdminMe = {
  user_id: "",
  email: "",
  name: "",
  role: "viewer",
};

export const AdminOverviewSchema = z
  .object({
    users_total: z.number().default(0),
    users_new_7d: z.number().default(0),
    users_disabled: z.number().default(0),
    workspaces_total: z.number().default(0),
    runtimes_total: z.number().default(0),
    runtimes_online: z.number().default(0),
    tasks_24h: z.record(z.string(), z.number()).default({}),
  })
  .loose();
export type AdminOverview = z.infer<typeof AdminOverviewSchema>;
export const EMPTY_ADMIN_OVERVIEW: AdminOverview = {
  users_total: 0,
  users_new_7d: 0,
  users_disabled: 0,
  workspaces_total: 0,
  runtimes_total: 0,
  runtimes_online: 0,
  tasks_24h: {},
};

export const AdminUserSchema = z
  .object({
    id: z.string(),
    name: z.string().default(""),
    email: z.string().default(""),
    avatar_url: z.string().nullable().default(null),
    created_at: z.string().default(""),
    disabled_at: z.string().nullable().default(null),
    disabled_reason: z.string().default(""),
    workspace_count: z.number().default(0),
  })
  .loose();
export type AdminUser = z.infer<typeof AdminUserSchema>;

export const AdminUserWorkspaceSchema = z
  .object({
    id: z.string(),
    name: z.string().default(""),
    slug: z.string().default(""),
    role: z.string().default(""),
    joined_at: z.string().default(""),
  })
  .loose();
export type AdminUserWorkspace = z.infer<typeof AdminUserWorkspaceSchema>;

export const AdminUserTokenSchema = z
  .object({
    id: z.string(),
    name: z.string().default(""),
    token_prefix: z.string().default(""),
    created_at: z.string().default(""),
    last_used_at: z.string().nullable().default(null),
    expires_at: z.string().nullable().default(null),
  })
  .loose();
export type AdminUserToken = z.infer<typeof AdminUserTokenSchema>;

export const AdminUserDetailSchema = AdminUserSchema.extend({
  workspaces: z.array(AdminUserWorkspaceSchema).default([]),
  tokens: z.array(AdminUserTokenSchema).default([]),
});
export type AdminUserDetail = z.infer<typeof AdminUserDetailSchema>;
export const EMPTY_ADMIN_USER_DETAIL: AdminUserDetail = {
  id: "",
  name: "",
  email: "",
  avatar_url: null,
  created_at: "",
  disabled_at: null,
  disabled_reason: "",
  workspace_count: 0,
  workspaces: [],
  tokens: [],
};

export const AdminWorkspaceSchema = z
  .object({
    id: z.string(),
    name: z.string().default(""),
    slug: z.string().default(""),
    created_at: z.string().default(""),
    member_count: z.number().default(0),
    agent_count: z.number().default(0),
  })
  .loose();
export type AdminWorkspace = z.infer<typeof AdminWorkspaceSchema>;

export const AdminWorkspaceMemberSchema = z
  .object({
    user_id: z.string(),
    name: z.string().default(""),
    email: z.string().default(""),
    role: z.string().default(""),
    joined_at: z.string().default(""),
    disabled_at: z.string().nullable().default(null),
  })
  .loose();
export type AdminWorkspaceMember = z.infer<typeof AdminWorkspaceMemberSchema>;

export const AdminWorkspaceDetailSchema = AdminWorkspaceSchema.extend({
  members: z.array(AdminWorkspaceMemberSchema).default([]),
});
export type AdminWorkspaceDetail = z.infer<typeof AdminWorkspaceDetailSchema>;
export const EMPTY_ADMIN_WORKSPACE_DETAIL: AdminWorkspaceDetail = {
  id: "",
  name: "",
  slug: "",
  created_at: "",
  member_count: 0,
  agent_count: 0,
  members: [],
};

export const AdminAuditEntrySchema = z
  .object({
    id: z.string(),
    actor_user_id: z.string().default(""),
    actor_email: z.string().default(""),
    action: z.string().default(""),
    target_type: z.string().default(""),
    target_id: z.string().default(""),
    reason: z.string().default(""),
    payload: z.unknown().default(null),
    ip: z.string().default(""),
    created_at: z.string().default(""),
  })
  .loose();
export type AdminAuditEntry = z.infer<typeof AdminAuditEntrySchema>;

export const PlatformAdminSchema = z
  .object({
    user_id: z.string(),
    name: z.string().default(""),
    email: z.string().default(""),
    role: PlatformRoleSchema.default("viewer"),
    granted_by: z.string().nullable().default(null),
    created_at: z.string().default(""),
  })
  .loose();
export type PlatformAdmin = z.infer<typeof PlatformAdminSchema>;
export const EMPTY_PLATFORM_ADMIN: PlatformAdmin = {
  user_id: "",
  name: "",
  email: "",
  role: "viewer",
  granted_by: null,
  created_at: "",
};

export function adminPageSchema<T extends z.ZodType>(item: T) {
  return z
    .object({
      items: z.array(item).default([]),
      total: z.number().default(0),
      page: z.number().default(1),
      page_size: z.number().default(25),
    })
    .loose();
}

export interface AdminPage<T> {
  items: T[];
  total: number;
  page: number;
  page_size: number;
}

export function emptyAdminPage<T>(): AdminPage<T> {
  return { items: [], total: 0, page: 1, page_size: 25 };
}

export const AdminUserPageSchema = adminPageSchema(AdminUserSchema);
export const AdminWorkspacePageSchema = adminPageSchema(AdminWorkspaceSchema);
export const AdminAuditPageSchema = adminPageSchema(AdminAuditEntrySchema);
export const PlatformAdminListSchema = z.array(PlatformAdminSchema);

export interface AdminPaging {
  page?: number;
  page_size?: number;
}

export interface AdminUserListParams extends AdminPaging {
  q?: string;
  disabled_only?: boolean;
}

export interface AdminWorkspaceListParams extends AdminPaging {
  q?: string;
}

export interface AdminAuditListParams extends AdminPaging {
  actor_user_id?: string;
  action?: string;
  target_id?: string;
}

export interface AdminActionRequest {
  reason: string;
}

export interface SetPlatformAdminRequest {
  email: string;
  role: PlatformRole;
  reason: string;
}
