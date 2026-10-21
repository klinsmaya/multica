-- Rebuildable office previews. Not revisions and not attachments.
-- Failed rows set expires_at (seven days is an ops default). Nothing deletes them.

CREATE TABLE IF NOT EXISTS artifact_derivatives (
    id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    artifact_id UUID NOT NULL,
    revision_id UUID NOT NULL,
    format TEXT NOT NULL CHECK (format IN ('docx', 'xlsx', 'pptx')),
    status TEXT NOT NULL CHECK (status IN ('pending', 'ready', 'failed')),
    media_type TEXT NOT NULL DEFAULT '',
    storage_key TEXT NOT NULL DEFAULT '',
    error_code TEXT NOT NULL DEFAULT '',
    page_count INTEGER NOT NULL DEFAULT 0,
    sheet_names TEXT NOT NULL DEFAULT '',
    slide_count INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ
);
