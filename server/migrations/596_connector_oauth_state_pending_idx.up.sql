CREATE INDEX CONCURRENTLY IF NOT EXISTS connector_oauth_state_pending_idx
    ON connector_oauth_state (provider, service) WHERE consumed_at IS NULL;
