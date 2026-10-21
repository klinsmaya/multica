-- Platform-level publication policy for connector services (open-connector).
-- One row per (provider, service): whether the service is published to users,
-- which actions a runtime token may call, and which users may connect it.
-- Empty arrays mean "no restriction". A service without a row is not published.
--
-- No foreign keys: allowed_user_ids and updated_by reference "user".id at the
-- application layer. The primary key is attached in 599 from the index built
-- CONCURRENTLY in 598 (repo convention, see 584-585).
CREATE TABLE IF NOT EXISTS connector_service_policy (
    provider         TEXT NOT NULL,
    service          TEXT NOT NULL,
    enabled          BOOLEAN NOT NULL DEFAULT FALSE,
    allowed_tools    TEXT[] NOT NULL DEFAULT '{}',
    allowed_user_ids UUID[] NOT NULL DEFAULT '{}',
    updated_by       UUID,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON COLUMN connector_service_policy.allowed_tools IS
    'Action names (without the service prefix) a task runtime token may call. Empty = every action of the service.';
COMMENT ON COLUMN connector_service_policy.allowed_user_ids IS
    'Users who may connect and mount this service. Empty = every user.';
