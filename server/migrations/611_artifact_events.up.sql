-- A08 transactional outbox. Rows carry ids only. No file body and no storage key.

CREATE TABLE IF NOT EXISTS artifact_events (
    id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    artifact_id UUID NOT NULL,
    revision_id UUID,
    operation_id UUID,
    event_type TEXT NOT NULL CHECK (event_type IN (
        'artifact.created',
        'artifact.revision.committed',
        'artifact.preview.ready'
    )),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
