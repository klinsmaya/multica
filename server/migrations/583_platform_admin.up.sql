-- Platform admin console: platform-level roles, an append-only audit log, and
-- persisted account suspension. Relationships (user ids) are validated in
-- application code; no foreign keys by project rule.
--
-- Primary keys are attached in 585/587 from the indexes built CONCURRENTLY in
-- 584/586 (repo convention, see 574-575).
CREATE TABLE IF NOT EXISTS platform_admin (
    user_id UUID NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('super_admin', 'operator', 'viewer')),
    granted_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS admin_audit_log (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    actor_user_id UUID NOT NULL,
    action TEXT NOT NULL,
    target_type TEXT NOT NULL DEFAULT '',
    target_id TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    payload JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(payload) = 'object'),
    ip TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Persisted suspension replaces the hardcoded denylist in
-- internal/auth/temporary_disabled_users.go.
ALTER TABLE "user"
    ADD COLUMN IF NOT EXISTS disabled_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS disabled_reason TEXT NOT NULL DEFAULT '';

-- Carry the former hardcoded denylist over so those accounts stay suspended.
UPDATE "user" SET disabled_at = now(), disabled_reason = 'migrated from emergency denylist'
WHERE disabled_at IS NULL
  AND (id IN ('514492f7-b30f-4147-bd33-c0e8ce5d6d4f', '1d542296-17c6-484a-9914-dcee589be116')
       OR lower(email) IN ('pdzzer68@embassybase.com', 'gtwtrox@mowan666.com'));
