-- Backing index for artifacts' primary key, attached in 585.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS artifacts_pkey_uidx
    ON artifacts (id);
