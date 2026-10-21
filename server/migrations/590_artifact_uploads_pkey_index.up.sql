-- Backing index for artifact_uploads' primary key, attached in 591.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS artifact_uploads_pkey_uidx
    ON artifact_uploads (id);
