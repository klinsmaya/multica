-- Artifact authority (plan A03). These tables are the cloud record of a
-- product file: a stable artifact id, immutable revisions, content-addressed
-- blob metadata, an upload binding, an idempotency operation, and links that
-- do not grant access.
--
-- No foreign keys. Workspace teardown deletes these rows in application code
-- (DeleteWorkspaceArtifacts). Existing attachment rows are untouched.
--
-- Primary keys and the unique constraints from the plan are attached in later
-- single-statement migrations. CREATE INDEX CONCURRENTLY cannot share a file
-- with these statements.

CREATE TABLE IF NOT EXISTS artifacts (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    -- What the artifact is owned by. A link to another object does not change
    -- this and does not widen who can read the bytes.
    owner_scope TEXT NOT NULL CHECK (owner_scope IN ('workspace', 'issue', 'task')),
    owner_id UUID NOT NULL,
    -- Display only. Two artifacts in one workspace may share a title.
    title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 512),
    kind TEXT NOT NULL DEFAULT 'file' CHECK (kind IN ('file')),
    head_revision_id UUID,
    row_version BIGINT NOT NULL DEFAULT 0 CHECK (row_version >= 0),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'deleted')),
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS artifact_blobs (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    -- Object key only. The user-facing filename is not part of this key.
    storage_key TEXT NOT NULL CHECK (
        char_length(storage_key) BETWEEN 1 AND 1024
        AND storage_key !~ '\.\.'
        AND storage_key !~ '\\'
    ),
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    sha256 TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    detected_media_type TEXT NOT NULL CHECK (char_length(detected_media_type) BETWEEN 1 AND 255),
    scan_status TEXT NOT NULL DEFAULT 'unchecked' CHECK (scan_status IN ('unchecked', 'pending', 'clean', 'rejected')),
    encryption_scope TEXT NOT NULL DEFAULT 'workspace' CHECK (char_length(encryption_scope) BETWEEN 1 AND 64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS artifact_uploads (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    actor_id UUID NOT NULL,
    artifact_id UUID NOT NULL,
    -- Bound at insert. The immutability trigger below rejects retargeting.
    base_revision_id UUID,
    expected_size_bytes BIGINT NOT NULL CHECK (expected_size_bytes >= 0),
    expected_sha256 TEXT NOT NULL CHECK (expected_sha256 ~ '^[0-9a-f]{64}$'),
    staging_key TEXT NOT NULL CHECK (
        char_length(staging_key) BETWEEN 1 AND 1024
        AND staging_key !~ '\.\.'
        AND staging_key !~ '\\'
    ),
    state TEXT NOT NULL CHECK (state IN (
        'created', 'uploading', 'uploaded', 'verifying', 'scanning',
        'ready', 'rejected', 'expired', 'cancelled', 'committed'
    )),
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    committed_revision_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS artifact_operations (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    actor_id UUID NOT NULL,
    idempotency_key TEXT NOT NULL CHECK (char_length(idempotency_key) BETWEEN 1 AND 200),
    request_hash TEXT NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    state TEXT NOT NULL CHECK (state IN ('pending', 'succeeded', 'failed')),
    result_ref JSONB,
    error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS artifact_revisions (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    artifact_id UUID NOT NULL,
    revision_no BIGINT NOT NULL CHECK (revision_no > 0),
    parent_revision_id UUID,
    base_revision_id UUID,
    blob_id UUID NOT NULL,
    source_upload_id UUID,
    commit_operation_id UUID NOT NULL,
    media_type TEXT NOT NULL CHECK (char_length(media_type) BETWEEN 1 AND 255),
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    sha256 TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    -- Display name captured with this revision. Not an identity.
    filename TEXT NOT NULL CHECK (char_length(filename) BETWEEN 1 AND 1024),
    source TEXT NOT NULL CHECK (source IN ('agent', 'human_edit', 'restore', 'import')),
    actor_id UUID NOT NULL,
    task_run_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS artifact_links (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    artifact_id UUID NOT NULL,
    revision_id UUID NOT NULL,
    target_type TEXT NOT NULL CHECK (char_length(target_type) BETWEEN 1 AND 64),
    target_id UUID NOT NULL,
    relation_type TEXT NOT NULL CHECK (char_length(relation_type) BETWEEN 1 AND 64),
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Revisions are immutable once inserted. History restore inserts a new row.
CREATE OR REPLACE FUNCTION artifact_revisions_immutable()
RETURNS TRIGGER
LANGUAGE plpgsql AS $function$
BEGIN
    RAISE EXCEPTION 'artifact revisions are immutable';
END
$function$;

DROP TRIGGER IF EXISTS trg_artifact_revisions_immutable ON artifact_revisions;
CREATE TRIGGER trg_artifact_revisions_immutable
BEFORE UPDATE ON artifact_revisions
FOR EACH ROW EXECUTE FUNCTION artifact_revisions_immutable();

-- The upload's target artifact, actor, base revision, and expected bytes are
-- fixed at insert. State and the committed revision pointer may advance.
CREATE OR REPLACE FUNCTION artifact_uploads_immutable_binding()
RETURNS TRIGGER
LANGUAGE plpgsql AS $function$
BEGIN
    IF NEW.workspace_id IS DISTINCT FROM OLD.workspace_id
       OR NEW.actor_id IS DISTINCT FROM OLD.actor_id
       OR NEW.artifact_id IS DISTINCT FROM OLD.artifact_id
       OR NEW.base_revision_id IS DISTINCT FROM OLD.base_revision_id
       OR NEW.expected_size_bytes IS DISTINCT FROM OLD.expected_size_bytes
       OR NEW.expected_sha256 IS DISTINCT FROM OLD.expected_sha256
       OR NEW.staging_key IS DISTINCT FROM OLD.staging_key
    THEN
        RAISE EXCEPTION 'artifact upload binding is immutable';
    END IF;
    RETURN NEW;
END
$function$;

DROP TRIGGER IF EXISTS trg_artifact_uploads_immutable_binding ON artifact_uploads;
CREATE TRIGGER trg_artifact_uploads_immutable_binding
BEFORE UPDATE ON artifact_uploads
FOR EACH ROW EXECUTE FUNCTION artifact_uploads_immutable_binding();

-- Scan status may change later. The key and the bytes it names may not.
CREATE OR REPLACE FUNCTION artifact_blobs_immutable_bytes()
RETURNS TRIGGER
LANGUAGE plpgsql AS $function$
BEGIN
    IF NEW.storage_key IS DISTINCT FROM OLD.storage_key
       OR NEW.size_bytes IS DISTINCT FROM OLD.size_bytes
       OR NEW.sha256 IS DISTINCT FROM OLD.sha256
    THEN
        RAISE EXCEPTION 'artifact blob bytes are immutable';
    END IF;
    RETURN NEW;
END
$function$;

DROP TRIGGER IF EXISTS trg_artifact_blobs_immutable_bytes ON artifact_blobs;
CREATE TRIGGER trg_artifact_blobs_immutable_bytes
BEFORE UPDATE ON artifact_blobs
FOR EACH ROW EXECUTE FUNCTION artifact_blobs_immutable_bytes();

-- The idempotency identity is fixed. State and the stored result may advance.
CREATE OR REPLACE FUNCTION artifact_operations_immutable_identity()
RETURNS TRIGGER
LANGUAGE plpgsql AS $function$
BEGIN
    IF NEW.workspace_id IS DISTINCT FROM OLD.workspace_id
       OR NEW.actor_id IS DISTINCT FROM OLD.actor_id
       OR NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
       OR NEW.request_hash IS DISTINCT FROM OLD.request_hash
    THEN
        RAISE EXCEPTION 'artifact operation identity is immutable';
    END IF;
    RETURN NEW;
END
$function$;

DROP TRIGGER IF EXISTS trg_artifact_operations_immutable_identity ON artifact_operations;
CREATE TRIGGER trg_artifact_operations_immutable_identity
BEFORE UPDATE ON artifact_operations
FOR EACH ROW EXECUTE FUNCTION artifact_operations_immutable_identity();
