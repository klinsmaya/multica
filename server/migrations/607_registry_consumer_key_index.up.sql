CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_registry_consumer_key
    ON registry_consumer (key);
