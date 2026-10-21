-- A07: one editor holds a lease on a text artifact. The draft is not a revision.
-- No foreign keys. Workspace teardown deletes these rows with the artifact.

CREATE TABLE IF NOT EXISTS artifact_edit_sessions (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    artifact_id UUID NOT NULL,
    base_revision_id UUID NOT NULL,
    actor_id UUID NOT NULL,
    editor_type TEXT NOT NULL CHECK (editor_type IN ('human', 'agent')),
    lease_token_hash TEXT NOT NULL CHECK (lease_token_hash ~ '^[0-9a-f]{64}$'),
    fencing_token BIGINT NOT NULL CHECK (fencing_token > 0),
    lease_expires_at TIMESTAMPTZ NOT NULL,
    draft_ref TEXT CHECK (
        draft_ref IS NULL
        OR (
            char_length(draft_ref) BETWEEN 1 AND 1024
            AND draft_ref !~ '\.\.'
            AND draft_ref !~ '\\'
        )
    ),
    state TEXT NOT NULL CHECK (state IN ('open', 'expired', 'committed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
