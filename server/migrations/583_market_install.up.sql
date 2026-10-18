-- Install ledger for an approved market plan.
-- Steps are JSON on the operation because the watcher always loads them
-- with that row. payload keeps the catalog bytes needed to finish an insert
-- after a crash. The API does not return payload. No foreign keys.
-- Indexes are attached in the following migrations.
CREATE TABLE IF NOT EXISTS market_install_operation (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    instance_id UUID,
    idempotency_key TEXT NOT NULL CHECK (char_length(idempotency_key) BETWEEN 16 AND 64),
    plan_hash TEXT NOT NULL,
    actor_member_id UUID NOT NULL,
    actor_user_id UUID NOT NULL,
    listing_id TEXT NOT NULL,
    listing_version TEXT NOT NULL,
    approval_ref TEXT NOT NULL,
    policy_version TEXT NOT NULL DEFAULT '',
    fence_token UUID NOT NULL,
    steps JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(steps) = 'array'),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(payload) = 'object'),
    revision BIGINT NOT NULL DEFAULT 1,
    outbox_open BOOLEAN NOT NULL DEFAULT true,
    lease_token UUID,
    leased_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS market_install_instance (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    operation_id UUID NOT NULL,
    listing_id TEXT NOT NULL,
    listing_version TEXT NOT NULL,
    plan_hash TEXT NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1,
    fence_token UUID NOT NULL,
    owner_member_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS market_install_binding (
    instance_id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    kind TEXT NOT NULL,
    resource_id UUID NOT NULL,
    ref TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
