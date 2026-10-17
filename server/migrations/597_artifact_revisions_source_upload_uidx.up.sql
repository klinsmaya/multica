-- One upload becomes at most one revision. Null is allowed only until a
-- revision is bound; committed rows set source_upload_id.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS artifact_revisions_source_upload_uidx
    ON artifact_revisions (source_upload_id)
    WHERE source_upload_id IS NOT NULL;
