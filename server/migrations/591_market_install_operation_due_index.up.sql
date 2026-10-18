-- Due outbox scan. The predicate is outbox_open. The watcher also
-- requires the lease to be empty or expired, which is not immutable, so it
-- stays in the query.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_market_install_operation_due
    ON market_install_operation (leased_until, created_at)
    WHERE outbox_open;
