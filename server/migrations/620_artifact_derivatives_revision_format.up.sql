CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS artifact_derivatives_revision_format_uidx
    ON artifact_derivatives (revision_id, format);
