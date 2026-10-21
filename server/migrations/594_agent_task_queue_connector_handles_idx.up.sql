CREATE INDEX CONCURRENTLY IF NOT EXISTS agent_task_queue_connector_handles_idx
    ON agent_task_queue (id) WHERE runtime_connector_handles IS NOT NULL;
