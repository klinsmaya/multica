-- Workspace teardown and later listing filter by workspace_id.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_artifacts_workspace
    ON artifacts (workspace_id);
