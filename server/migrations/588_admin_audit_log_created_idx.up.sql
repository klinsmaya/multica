CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_admin_audit_log_created
    ON admin_audit_log (created_at DESC, id DESC);
