CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_admin_audit_log_actor
    ON admin_audit_log (actor_user_id, created_at DESC);
