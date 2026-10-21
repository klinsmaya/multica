ALTER TABLE "user"
    DROP COLUMN IF EXISTS disabled_reason,
    DROP COLUMN IF EXISTS disabled_at;
DROP TABLE IF EXISTS admin_audit_log;
DROP TABLE IF EXISTS platform_admin;
