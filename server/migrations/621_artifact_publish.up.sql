-- A13 fixed-version publish. A snapshot pins one revision and a manifest blob.
-- No foreign keys. External and public audience values exist so the check can
-- name them. The product flag that would allow those audiences stays off.

CREATE TABLE IF NOT EXISTS artifact_publish_snapshots (
    id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    artifact_id UUID NOT NULL,
    revision_id UUID NOT NULL,
    manifest_blob_id UUID NOT NULL,
    audience TEXT NOT NULL CHECK (audience IN ('workspace_members', 'named_members', 'external', 'public')),
    token_hash TEXT NOT NULL CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS artifact_share_grants (
    id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    snapshot_id UUID NOT NULL,
    grantee_user_id UUID NOT NULL,
    token_hash TEXT NOT NULL CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS artifact_audit_events (
    id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    actor_id UUID,
    action TEXT NOT NULL CHECK (action IN ('share.publish', 'share.view', 'share.download', 'share.revoke')),
    target_id UUID NOT NULL,
    result TEXT NOT NULL CHECK (result IN ('allowed', 'denied')),
    request_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE artifact_events ADD COLUMN IF NOT EXISTS snapshot_id UUID;

ALTER TABLE artifact_events DROP CONSTRAINT IF EXISTS artifact_events_event_type_check;
ALTER TABLE artifact_events ADD CONSTRAINT artifact_events_event_type_check CHECK (event_type IN (
    'artifact.created',
    'artifact.revision.committed',
    'artifact.preview.ready',
    'artifact.share.revoked'
));
