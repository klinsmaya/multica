-- Provider-private resource handles minted while building a task's MCP overlay
-- (before the queue row exists), keyed by provider name, e.g.
-- {"open-connector":"<runtime token id>"}. Used to release per-task resources
-- when the task reaches a terminal state. Cleared once released.
ALTER TABLE agent_task_queue
    ADD COLUMN runtime_connector_handles JSONB NULL;

COMMENT ON COLUMN agent_task_queue.runtime_connector_handles IS
    'Opaque per-provider resource handles ({"<provider>":"<handle>"}) created by ConnectorProvider.BuildTaskOverlay. Not secret. NULL when nothing needs releasing or after release.';
