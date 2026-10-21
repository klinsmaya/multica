CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_registry_install_report_tenant
    ON registry_install_report (consumer_id, tenant_id, listing_id);
