-- A new lease takes the next fencing token. An older token cannot be reused.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS artifact_edit_sessions_fencing_uidx
    ON artifact_edit_sessions (artifact_id, fencing_token);
