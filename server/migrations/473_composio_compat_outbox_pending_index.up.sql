CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_composio_compat_outbox_pending ON composio_compat_outbox (available_at) WHERE completed_at IS NULL;
