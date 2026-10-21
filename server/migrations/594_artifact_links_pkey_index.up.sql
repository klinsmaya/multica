-- Backing index for artifact_links' primary key, attached in 595.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS artifact_links_pkey_uidx
    ON artifact_links (id);
