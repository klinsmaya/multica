CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_artifact_uploads_workspace
    ON artifact_uploads (workspace_id);
