-- Artifact authority queries. The service owns the transaction: lock the
-- artifact, record the idempotency operation, then CAS the head. These
-- statements do not grant access; callers pass a workspace they already
-- decided the actor may write.

-- name: CreateArtifact :one
INSERT INTO artifacts (
    id, workspace_id, owner_scope, owner_id, title, kind, created_by
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;

-- name: LockArtifact :one
SELECT * FROM artifacts
WHERE id = $1 AND workspace_id = $2
FOR UPDATE;

-- name: InsertArtifactOperation :one
INSERT INTO artifact_operations (
    id, workspace_id, actor_id, idempotency_key, request_hash, state
) VALUES (
    $1, $2, $3, $4, $5, 'pending'
)
ON CONFLICT (workspace_id, actor_id, idempotency_key) DO NOTHING
RETURNING *;

-- name: LockArtifactOperation :one
SELECT * FROM artifact_operations
WHERE workspace_id = $1 AND actor_id = $2 AND idempotency_key = $3
FOR UPDATE;

-- name: FinishArtifactOperation :one
UPDATE artifact_operations
SET state = sqlc.arg(state),
    result_ref = sqlc.narg(result_ref),
    error_code = sqlc.narg(error_code),
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: NextArtifactRevisionNo :one
SELECT (COALESCE(MAX(revision_no), 0) + 1)::bigint
FROM artifact_revisions
WHERE artifact_id = $1;

-- name: InsertArtifactBlob :one
INSERT INTO artifact_blobs (
    id, storage_key, size_bytes, sha256, detected_media_type, scan_status, encryption_scope
) VALUES (
    $1, $2, $3, $4, $5, $6, 'workspace'
)
RETURNING *;

-- name: InsertArtifactUpload :one
-- Opens a staging binding. State starts at created; commit is a later transition.
INSERT INTO artifact_uploads (
    id, workspace_id, actor_id, artifact_id, base_revision_id,
    expected_size_bytes, expected_sha256, staging_key,
    declared_media_type, filename, state, expires_at
) VALUES (
    sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(actor_id), sqlc.arg(artifact_id),
    sqlc.narg(base_revision_id),
    sqlc.arg(expected_size_bytes), sqlc.arg(expected_sha256), sqlc.arg(staging_key),
    sqlc.arg(declared_media_type), sqlc.arg(filename), 'created', sqlc.arg(expires_at)
)
RETURNING *;

-- name: LockArtifactUpload :one
SELECT * FROM artifact_uploads
WHERE id = sqlc.arg(id) AND workspace_id = sqlc.arg(workspace_id)
FOR UPDATE;

-- name: TransitionArtifactUpload :one
UPDATE artifact_uploads
SET state = sqlc.arg(to_state)
WHERE id = sqlc.arg(id)
  AND workspace_id = sqlc.arg(workspace_id)
  AND state = sqlc.arg(from_state)
RETURNING *;

-- name: ConsumeArtifactUpload :one
UPDATE artifact_uploads
SET state = 'committed',
    consumed_at = now(),
    committed_revision_id = sqlc.arg(committed_revision_id)
WHERE id = sqlc.arg(id)
  AND workspace_id = sqlc.arg(workspace_id)
  AND state = 'ready'
RETURNING *;

-- name: RetainArtifactUploadConflict :one
-- The verified bytes stay in staging. A sweeper (A14) expires the row;
-- this statement only records the retention deadline.
UPDATE artifact_uploads
SET state = 'conflict_draft',
    expires_at = now() + interval '7 days'
WHERE id = sqlc.arg(id)
  AND workspace_id = sqlc.arg(workspace_id)
  AND state = 'ready'
RETURNING *;

-- name: GetArtifactOperation :one
SELECT * FROM artifact_operations
WHERE workspace_id = $1 AND actor_id = $2 AND idempotency_key = $3;

-- name: LockWorkspaceArtifactQuota :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(workspace_id)::uuid::text || ':artifact-quota', 604));

-- name: SumWorkspaceArtifactBytes :one
WITH reserved_uploads AS (
    SELECT COALESCE(SUM(artifact_uploads.expected_size_bytes), 0)::bigint AS bytes
    FROM artifact_uploads
    WHERE artifact_uploads.workspace_id = sqlc.arg(workspace_id)
      AND artifact_uploads.state IN (
          'created', 'uploading', 'uploaded', 'verifying', 'scanning', 'ready', 'conflict_draft'
      )
      AND artifact_uploads.expires_at > now()
),
reserved_revisions AS (
    SELECT COALESCE(SUM(artifact_revisions.size_bytes), 0)::bigint AS bytes
    FROM artifact_revisions
    WHERE artifact_revisions.artifact_id IN (
        SELECT artifacts.id FROM artifacts WHERE artifacts.workspace_id = sqlc.arg(workspace_id)
    )
)
SELECT (reserved_uploads.bytes + reserved_revisions.bytes)::bigint
FROM reserved_uploads, reserved_revisions;

-- name: SumActorArtifactBytes :one
WITH reserved_uploads AS (
    SELECT COALESCE(SUM(artifact_uploads.expected_size_bytes), 0)::bigint AS bytes
    FROM artifact_uploads
    WHERE artifact_uploads.workspace_id = sqlc.arg(workspace_id)
      AND artifact_uploads.actor_id = sqlc.arg(actor_id)
      AND artifact_uploads.state IN (
          'created', 'uploading', 'uploaded', 'verifying', 'scanning', 'ready', 'conflict_draft'
      )
      AND artifact_uploads.expires_at > now()
),
reserved_revisions AS (
    SELECT COALESCE(SUM(artifact_revisions.size_bytes), 0)::bigint AS bytes
    FROM artifact_revisions
    WHERE artifact_revisions.actor_id = sqlc.arg(actor_id)
      AND artifact_revisions.artifact_id IN (
          SELECT artifacts.id FROM artifacts WHERE artifacts.workspace_id = sqlc.arg(workspace_id)
      )
)
SELECT (reserved_uploads.bytes + reserved_revisions.bytes)::bigint
FROM reserved_uploads, reserved_revisions;

-- name: ListExpiredArtifactUploads :many
SELECT * FROM artifact_uploads
WHERE artifact_uploads.workspace_id = sqlc.arg(workspace_id)
  AND artifact_uploads.expires_at <= now()
  AND artifact_uploads.state IN (
      'created', 'uploading', 'uploaded', 'verifying', 'scanning', 'ready', 'conflict_draft'
  )
ORDER BY artifact_uploads.expires_at
LIMIT sqlc.arg(lim);

-- name: InsertArtifactRevision :one
INSERT INTO artifact_revisions (
    id, artifact_id, revision_no, parent_revision_id, base_revision_id,
    blob_id, source_upload_id, commit_operation_id, media_type, size_bytes,
    sha256, filename, source, actor_id, task_run_id
) VALUES (
    $1, $2, $3, sqlc.narg(parent_revision_id), sqlc.narg(base_revision_id),
    $4, $5, $6, $7, $8,
    $9, $10, $11, $12, sqlc.narg(task_run_id)
)
RETURNING *;

-- name: CasArtifactHead :one
-- Compare-and-swap. head_revision_id changes only when it still equals the
-- caller's base (NULL matches NULL for the first revision).
UPDATE artifacts
SET head_revision_id = sqlc.arg(head_revision_id),
    row_version = row_version + 1,
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND workspace_id = sqlc.arg(workspace_id)
  AND deleted_at IS NULL
  AND head_revision_id IS NOT DISTINCT FROM sqlc.narg(base_revision_id)
RETURNING row_version;

-- name: ListArtifactStorageKeysByWorkspace :many
SELECT artifact_blobs.storage_key
FROM artifact_blobs
WHERE artifact_blobs.id IN (
    SELECT artifact_revisions.blob_id
    FROM artifact_revisions
    WHERE artifact_revisions.artifact_id IN (
        SELECT artifacts.id FROM artifacts WHERE artifacts.workspace_id = sqlc.arg(workspace_id)
    )
)
UNION
SELECT artifact_uploads.staging_key
FROM artifact_uploads
WHERE artifact_uploads.workspace_id = sqlc.arg(workspace_id)
  AND artifact_uploads.staging_key <> '';

-- name: DeleteWorkspaceArtifacts :exec
-- Bottom-up delete for one workspace. Blob rows have no workspace_id; they
-- are removed by the revision ids captured before those revisions go away.
-- Data-modifying CTEs do not see each other's table changes, so blob ids are
-- read in doomed_blobs before any delete.
WITH doomed AS (
    SELECT artifacts.id FROM artifacts WHERE artifacts.workspace_id = sqlc.arg(workspace_id)
),
doomed_blobs AS (
    SELECT DISTINCT artifact_revisions.blob_id AS id
    FROM artifact_revisions
    WHERE artifact_revisions.artifact_id IN (SELECT doomed.id FROM doomed)
),
deleted_links AS (
    DELETE FROM artifact_links
    WHERE artifact_links.artifact_id IN (SELECT doomed.id FROM doomed)
),
deleted_revisions AS (
    DELETE FROM artifact_revisions
    WHERE artifact_revisions.artifact_id IN (SELECT doomed.id FROM doomed)
),
deleted_uploads AS (
    DELETE FROM artifact_uploads
    WHERE artifact_uploads.workspace_id = sqlc.arg(workspace_id)
),
deleted_operations AS (
    DELETE FROM artifact_operations
    WHERE artifact_operations.workspace_id = sqlc.arg(workspace_id)
),
deleted_artifacts AS (
    DELETE FROM artifacts
    WHERE artifacts.workspace_id = sqlc.arg(workspace_id)
)
DELETE FROM artifact_blobs
WHERE artifact_blobs.id IN (SELECT doomed_blobs.id FROM doomed_blobs);

-- name: ListArtifactsByOwner :many
-- Read model for one owner. Storage keys are not selected.
SELECT
    artifacts.id,
    artifacts.workspace_id,
    artifacts.owner_scope,
    artifacts.owner_id,
    artifacts.title,
    artifacts.kind,
    artifacts.status,
    artifacts.head_revision_id,
    artifacts.row_version,
    artifacts.created_by,
    artifacts.created_at,
    artifacts.updated_at,
    artifact_revisions.revision_no,
    artifact_revisions.media_type,
    artifact_revisions.filename,
    artifact_revisions.size_bytes,
    artifact_revisions.actor_id AS updated_by
FROM artifacts
LEFT JOIN artifact_revisions ON artifact_revisions.id = artifacts.head_revision_id
WHERE artifacts.workspace_id = sqlc.arg(workspace_id)
  AND artifacts.owner_scope = sqlc.arg(owner_scope)
  AND artifacts.owner_id = sqlc.arg(owner_id)
  AND artifacts.deleted_at IS NULL
  AND artifacts.status = 'active'
ORDER BY artifacts.updated_at DESC, artifacts.id DESC
LIMIT sqlc.arg(lim);

-- name: GetArtifactByID :one
SELECT * FROM artifacts
WHERE id = $1 AND deleted_at IS NULL AND status = 'active';

-- name: ListArtifactRevisions :many
SELECT
    id, artifact_id, revision_no, media_type, size_bytes, filename, source, actor_id, commit_operation_id, created_at
FROM artifact_revisions
WHERE artifact_id = $1
ORDER BY revision_no DESC;

-- name: GetArtifactRevision :one
SELECT
    id, artifact_id, revision_no, blob_id, media_type, size_bytes, filename, source, actor_id, commit_operation_id, created_at
FROM artifact_revisions
WHERE id = $1 AND artifact_id = $2;

-- name: GetArtifactOperationByID :one
SELECT * FROM artifact_operations
WHERE id = $1 AND workspace_id = $2;

-- name: GetArtifactBlob :one
SELECT id, storage_key, size_bytes, sha256, detected_media_type, scan_status
FROM artifact_blobs
WHERE id = $1;
