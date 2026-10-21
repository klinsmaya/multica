CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_registry_package_source_name
    ON registry_package (source_id, name);
