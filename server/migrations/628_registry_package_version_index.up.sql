CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_registry_package_version_package_version
    ON registry_package_version (package_id, version);
