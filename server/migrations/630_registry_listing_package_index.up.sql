CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_registry_listing_package
    ON registry_listing (package_id);
