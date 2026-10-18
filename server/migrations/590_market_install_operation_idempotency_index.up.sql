CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS market_install_operation_idempotency_uidx
    ON market_install_operation (workspace_id, idempotency_key);
