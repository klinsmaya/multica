-- User-owned connections held by an external connector provider
-- (provider = 'open-connector'). The row is a local mirror of a connection in
-- the provider so the product can list, label, default and disconnect without
-- a provider round-trip.
--
-- No foreign keys or cascades: user_id references "user".id at the application
-- layer only.
--
-- connection_id is the provider's connection id (open-connector appId);
-- connection_name is the name the agent must pass when calling the provider
-- (open-connector alias).
CREATE TABLE user_connector_connection (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider        TEXT NOT NULL,
    user_id         UUID NOT NULL,
    service         TEXT NOT NULL,
    connection_id   TEXT NOT NULL,
    connection_name TEXT NOT NULL,
    auth_type       TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'active',
    is_default      BOOLEAN NOT NULL DEFAULT FALSE,
    label           TEXT NOT NULL DEFAULT '',
    connected_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, user_id, connection_id)
);

COMMENT ON COLUMN user_connector_connection.status IS
    'active | reauth_required | revoked';
COMMENT ON COLUMN user_connector_connection.auth_type IS
    'oauth | api_key';
