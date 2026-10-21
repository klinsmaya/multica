CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_registry_consumer_api_key_hash
    ON registry_consumer (api_key_hash);
