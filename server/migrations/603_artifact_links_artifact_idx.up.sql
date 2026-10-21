-- Workspace teardown deletes links by the artifact ids it is removing.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_artifact_links_artifact
    ON artifact_links (artifact_id);
