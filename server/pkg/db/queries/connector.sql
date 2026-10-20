-- =====================
-- User Connector Connection (open-connector)
-- =====================

-- name: UpsertUserConnectorConnection :one
-- Idempotent on (provider, user_id, connection_id): a duplicate callback
-- re-activates the same row. is_default and connected_at are preserved on
-- conflict; a non-empty label replaces the stored one.
INSERT INTO user_connector_connection (
    provider, user_id, service, connection_id, connection_name, auth_type, label, is_default, status
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, 'active'
)
ON CONFLICT (provider, user_id, connection_id) DO UPDATE SET
    service         = EXCLUDED.service,
    connection_name = EXCLUDED.connection_name,
    auth_type       = EXCLUDED.auth_type,
    label           = CASE WHEN EXCLUDED.label <> '' THEN EXCLUDED.label ELSE user_connector_connection.label END,
    status          = 'active',
    updated_at      = now()
RETURNING *;

-- name: ListUserConnectorConnections :many
-- Non-revoked connections (active and reauth_required) so the owner can see and
-- repair the latter; callers that mount connections filter on status = 'active'.
-- Default connection first within each service.
SELECT * FROM user_connector_connection
WHERE provider = $1 AND user_id = $2 AND status <> 'revoked'
ORDER BY service ASC, is_default DESC, connected_at ASC;

-- name: GetUserConnectorConnection :one
-- Owner-scoped lookup so one user cannot act on another's connection by
-- guessing the UUID.
SELECT * FROM user_connector_connection
WHERE id = $1 AND user_id = $2;

-- name: MarkUserConnectorConnectionRevoked :exec
-- Idempotent; a revoked row is never the default.
UPDATE user_connector_connection
SET status = 'revoked', is_default = FALSE, updated_at = now()
WHERE id = $1 AND user_id = $2;

-- name: SetUserConnectorConnectionDefault :exec
-- Makes one active connection the default of its (provider, user, service)
-- group and clears the flag on its siblings in a single statement.
UPDATE user_connector_connection
SET is_default = (id = @id), updated_at = now()
WHERE provider = @provider
  AND user_id = @user_id
  AND service = @service
  AND status = 'active'
  AND (id = @id OR is_default);

-- name: UpdateUserConnectorConnectionLabel :one
UPDATE user_connector_connection
SET label = @label, updated_at = now()
WHERE id = @id AND user_id = @user_id
RETURNING *;

-- name: UpdateUserConnectorConnectionStatus :exec
UPDATE user_connector_connection
SET status = @status, updated_at = now()
WHERE id = @id AND status <> @status;

-- name: ListUserConnectorConnectionsByProviderAndStatus :many
SELECT * FROM user_connector_connection
WHERE provider = $1 AND status = $2
ORDER BY created_at ASC;

-- =====================
-- Connector OAuth state (in-flight connects)
-- =====================

-- name: InsertConnectorOAuthState :exec
INSERT INTO connector_oauth_state (nonce_hash, provider, user_id, service, request_id, expires_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetConnectorOAuthStateByNonceHash :one
SELECT * FROM connector_oauth_state
WHERE nonce_hash = @nonce_hash AND consumed_at IS NULL AND expires_at > @now;

-- name: ConsumeConnectorOAuthState :one
-- Atomic single-use: the first caller gets the row, replays get no rows.
UPDATE connector_oauth_state
SET consumed_at = @now
WHERE nonce_hash = @nonce_hash AND consumed_at IS NULL AND expires_at > @now
RETURNING *;

-- name: ExistsPendingConnectorOAuthForService :one
-- True when a different user has an unconsumed, unexpired connect for the
-- service (open-connector has one owner, so a second connect would supersede it).
SELECT EXISTS (
    SELECT 1 FROM connector_oauth_state
    WHERE provider = @provider AND service = @service
      AND consumed_at IS NULL AND expires_at > @now
      AND user_id <> @user_id
);

-- name: DeleteExpiredConnectorOAuthStates :execrows
DELETE FROM connector_oauth_state WHERE expires_at < @now;
