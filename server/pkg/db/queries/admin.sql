-- Platform admin console. Everything here is platform-level (not workspace
-- scoped) and is only reachable through /api/admin/*.

-- name: GetPlatformAdmin :one
SELECT user_id, role, granted_by, created_at FROM platform_admin
WHERE user_id = $1;

-- name: UpsertPlatformAdmin :one
INSERT INTO platform_admin (user_id, role, granted_by)
VALUES ($1, $2, sqlc.narg('granted_by'))
ON CONFLICT (user_id) DO UPDATE SET role = EXCLUDED.role
RETURNING user_id, role, granted_by, created_at;

-- name: DeletePlatformAdmin :execrows
DELETE FROM platform_admin WHERE user_id = $1;

-- name: CountPlatformSuperAdmins :one
SELECT count(*) FROM platform_admin WHERE role = 'super_admin';

-- name: ListPlatformAdmins :many
SELECT pa.user_id, pa.role, pa.granted_by, pa.created_at, u.name, u.email
FROM platform_admin pa
JOIN "user" u ON u.id = pa.user_id
ORDER BY pa.created_at ASC, pa.user_id ASC;

-- name: InsertAdminAuditLog :one
INSERT INTO admin_audit_log (actor_user_id, action, target_type, target_id, reason, payload, ip, user_agent)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, created_at;

-- name: ListAdminAuditLog :many
SELECT l.id, l.actor_user_id, l.action, l.target_type, l.target_id, l.reason,
       l.payload, l.ip, l.created_at,
       COALESCE(u.email, '')::text AS actor_email
FROM admin_audit_log l
LEFT JOIN "user" u ON u.id = l.actor_user_id
WHERE (sqlc.narg('actor_user_id')::uuid IS NULL OR l.actor_user_id = sqlc.narg('actor_user_id'))
  AND (sqlc.narg('action')::text IS NULL OR l.action = sqlc.narg('action'))
  AND (sqlc.narg('target_id')::text IS NULL OR l.target_id = sqlc.narg('target_id'))
ORDER BY l.created_at DESC, l.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountAdminAuditLog :one
SELECT count(*) FROM admin_audit_log l
WHERE (sqlc.narg('actor_user_id')::uuid IS NULL OR l.actor_user_id = sqlc.narg('actor_user_id'))
  AND (sqlc.narg('action')::text IS NULL OR l.action = sqlc.narg('action'))
  AND (sqlc.narg('target_id')::text IS NULL OR l.target_id = sqlc.narg('target_id'));

-- name: AdminListUsers :many
SELECT u.id, u.name, u.email, u.avatar_url, u.created_at, u.disabled_at, u.disabled_reason,
       (SELECT count(*) FROM member m WHERE m.user_id = u.id) AS workspace_count
FROM "user" u
WHERE (sqlc.narg('q')::text IS NULL
       OR u.email ILIKE '%' || sqlc.narg('q')::text || '%'
       OR u.name ILIKE '%' || sqlc.narg('q')::text || '%')
  AND (NOT @disabled_only::boolean OR u.disabled_at IS NOT NULL)
ORDER BY u.created_at DESC, u.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: AdminCountUsers :one
SELECT count(*) FROM "user" u
WHERE (sqlc.narg('q')::text IS NULL
       OR u.email ILIKE '%' || sqlc.narg('q')::text || '%'
       OR u.name ILIKE '%' || sqlc.narg('q')::text || '%')
  AND (NOT @disabled_only::boolean OR u.disabled_at IS NOT NULL);

-- name: AdminGetUser :one
SELECT u.id, u.name, u.email, u.avatar_url, u.created_at, u.disabled_at, u.disabled_reason,
       (SELECT count(*) FROM member m WHERE m.user_id = u.id) AS workspace_count
FROM "user" u
WHERE u.id = $1;

-- name: AdminListUserWorkspaces :many
SELECT w.id, w.name, w.slug, m.role, m.created_at AS joined_at
FROM member m
JOIN workspace w ON w.id = m.workspace_id
WHERE m.user_id = $1
ORDER BY m.created_at ASC;

-- name: GetUserDisabledAt :one
SELECT disabled_at FROM "user" WHERE id = $1;

-- name: AdminDisableUser :execrows
UPDATE "user" SET disabled_at = now(), disabled_reason = $2, updated_at = now()
WHERE id = $1 AND disabled_at IS NULL;

-- name: AdminEnableUser :execrows
UPDATE "user" SET disabled_at = NULL, disabled_reason = '', updated_at = now()
WHERE id = $1 AND disabled_at IS NOT NULL;

-- name: AdminListWorkspaces :many
SELECT w.id, w.name, w.slug, w.created_at,
       (SELECT count(*) FROM member m WHERE m.workspace_id = w.id) AS member_count,
       (SELECT count(*) FROM agent a WHERE a.workspace_id = w.id) AS agent_count
FROM workspace w
WHERE (sqlc.narg('q')::text IS NULL
       OR w.name ILIKE '%' || sqlc.narg('q')::text || '%'
       OR w.slug ILIKE '%' || sqlc.narg('q')::text || '%')
ORDER BY w.created_at DESC, w.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: AdminCountWorkspaces :one
SELECT count(*) FROM workspace w
WHERE (sqlc.narg('q')::text IS NULL
       OR w.name ILIKE '%' || sqlc.narg('q')::text || '%'
       OR w.slug ILIKE '%' || sqlc.narg('q')::text || '%');

-- name: AdminGetWorkspace :one
SELECT w.id, w.name, w.slug, w.created_at,
       (SELECT count(*) FROM member m WHERE m.workspace_id = w.id) AS member_count,
       (SELECT count(*) FROM agent a WHERE a.workspace_id = w.id) AS agent_count
FROM workspace w
WHERE w.id = $1;

-- name: AdminListWorkspaceMembers :many
SELECT u.id AS user_id, u.name, u.email, m.role, m.created_at AS joined_at, u.disabled_at
FROM member m
JOIN "user" u ON u.id = m.user_id
WHERE m.workspace_id = $1
ORDER BY m.created_at ASC;

-- name: AdminOverviewCounts :one
SELECT
    (SELECT count(*) FROM "user")::bigint AS users_total,
    (SELECT count(*) FROM "user" WHERE created_at > now() - interval '7 days')::bigint AS users_new_7d,
    (SELECT count(*) FROM "user" WHERE disabled_at IS NOT NULL)::bigint AS users_disabled,
    (SELECT count(*) FROM workspace)::bigint AS workspaces_total,
    (SELECT count(*) FROM agent_runtime)::bigint AS runtimes_total,
    (SELECT count(*) FROM agent_runtime WHERE status = 'online')::bigint AS runtimes_online;

-- name: AdminTaskCounts24h :many
SELECT status, count(*)::bigint AS total
FROM agent_task_queue
WHERE created_at > now() - interval '24 hours'
GROUP BY status;

-- name: ListDisabledUsers :many
SELECT id, email FROM "user" WHERE disabled_at IS NOT NULL;
