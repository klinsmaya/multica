-- Backing index for artifact_blobs' primary key, attached in 589.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS artifact_blobs_pkey_uidx
    ON artifact_blobs (id);
