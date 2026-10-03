CREATE INDEX CONCURRENTLY IF NOT EXISTS composio_compat_connection_outbox_pending_idx ON composio_compat_connection_outbox(available_at) WHERE state IN ('PENDING','REVOKED');
