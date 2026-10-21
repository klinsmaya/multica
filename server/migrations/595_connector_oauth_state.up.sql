-- In-flight OAuth connects of an external connector provider. The signed state
-- carries a random nonce; only sha256(nonce) is stored here, together with the
-- provider-side connection request id that the callback cannot supply.
-- No foreign keys: user_id references "user".id at the application layer.
CREATE TABLE connector_oauth_state (
    nonce_hash BYTEA PRIMARY KEY CHECK (octet_length(nonce_hash) = 32),
    provider TEXT NOT NULL,
    user_id UUID NOT NULL,
    service TEXT NOT NULL,
    request_id TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
