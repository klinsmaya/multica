-- Same actor, workspace, and key is one operation. A different request
-- hash is rejected in application code; this index stops a second row.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS artifact_operations_idempotency_uidx
    ON artifact_operations (workspace_id, actor_id, idempotency_key);
