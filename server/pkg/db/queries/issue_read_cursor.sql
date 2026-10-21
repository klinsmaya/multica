-- name: UpsertIssueReadCursor :exec
-- Monotonic: GREATEST keeps the newest value, so a late or replayed request
-- never moves a cursor backwards and re-marking an issue as read is a no-op.
-- A NULL last_read_at means "now" on the database clock, the same clock that
-- stamps comment.created_at, so API/DB clock skew cannot leave a just-read
-- issue looking unread.
INSERT INTO issue_read_cursor (user_id, issue_id, workspace_id, last_read_at)
VALUES (
    sqlc.arg('user_id')::uuid,
    sqlc.arg('issue_id')::uuid,
    sqlc.arg('workspace_id')::uuid,
    COALESCE(sqlc.narg('last_read_at')::timestamptz, now())
)
ON CONFLICT (user_id, issue_id) DO UPDATE
SET last_read_at = GREATEST(issue_read_cursor.last_read_at, EXCLUDED.last_read_at);

-- name: ListIssueReadCursors :many
-- The caller's cursors for a batch of issues, scoped to the workspace. Issues
-- the user never opened have no row.
SELECT issue_id, last_read_at
FROM issue_read_cursor
WHERE user_id = sqlc.arg('user_id')::uuid
  AND workspace_id = sqlc.arg('workspace_id')::uuid
  AND issue_id = ANY(sqlc.arg('issue_ids')::uuid[]);

-- name: DeleteIssueReadCursorsByIssue :exec
-- No foreign keys by project rule, so issue deletion cleans up here.
DELETE FROM issue_read_cursor
WHERE issue_id = sqlc.arg('issue_id')::uuid
  AND workspace_id = sqlc.arg('workspace_id')::uuid;

-- name: DeleteIssueReadCursorsByUserWorkspace :exec
-- A departing member's cursors, in the same tx as the member-row delete.
DELETE FROM issue_read_cursor
WHERE user_id = sqlc.arg('user_id')::uuid
  AND workspace_id = sqlc.arg('workspace_id')::uuid;

-- name: DeleteIssueReadCursorsForWorkspace :exec
-- No foreign keys by project rule, so workspace teardown cleans up here.
DELETE FROM issue_read_cursor WHERE workspace_id = sqlc.arg('workspace_id')::uuid;
