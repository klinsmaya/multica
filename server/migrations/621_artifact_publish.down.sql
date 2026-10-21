ALTER TABLE artifact_events DROP CONSTRAINT IF EXISTS artifact_events_event_type_check;
ALTER TABLE artifact_events ADD CONSTRAINT artifact_events_event_type_check CHECK (event_type IN (
    'artifact.created',
    'artifact.revision.committed',
    'artifact.preview.ready'
));
ALTER TABLE artifact_events DROP COLUMN IF EXISTS snapshot_id;
DROP TABLE IF EXISTS artifact_audit_events;
DROP TABLE IF EXISTS artifact_share_grants;
DROP TABLE IF EXISTS artifact_publish_snapshots;
