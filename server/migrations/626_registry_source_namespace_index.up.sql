CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_registry_source_namespace
    ON registry_source (namespace);
