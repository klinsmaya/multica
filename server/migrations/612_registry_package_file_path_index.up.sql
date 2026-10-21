CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_registry_package_file_version_path
    ON registry_package_file (version_id, path);
