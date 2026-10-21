CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_registry_targeting_scope
    ON registry_targeting (listing_id, consumer_id, COALESCE(tenant_id, ''), COALESCE(tenant_group, ''));
