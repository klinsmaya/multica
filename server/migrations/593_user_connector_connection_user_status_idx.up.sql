CREATE INDEX CONCURRENTLY IF NOT EXISTS user_connector_connection_user_status_idx
    ON user_connector_connection (user_id, status);
