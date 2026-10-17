-- Backing index for artifact_operations' primary key, attached in 593.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS artifact_operations_pkey_uidx
    ON artifact_operations (id);
