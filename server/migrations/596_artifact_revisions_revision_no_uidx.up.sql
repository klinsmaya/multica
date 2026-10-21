-- revision_no is the display version (v1, v2) and is unique per artifact.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS artifact_revisions_revision_no_uidx
    ON artifact_revisions (artifact_id, revision_no);
