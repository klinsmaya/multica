DROP TRIGGER IF EXISTS composio_compat_no_reactivate ON user_composio_connection;
DROP FUNCTION IF EXISTS composio_compat_no_reactivate();
DROP TABLE IF EXISTS composio_compat_connection_outbox;
