-- Backing index for artifact_revisions' primary key, attached in 587.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS artifact_revisions_pkey_uidx
    ON artifact_revisions (id);
