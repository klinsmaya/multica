-- One idempotent operation commits at most one revision.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS artifact_revisions_commit_operation_uidx
    ON artifact_revisions (commit_operation_id);
