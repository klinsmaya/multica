CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_agent_plugin_installation
    ON agent (plugin_installation_id)
    WHERE plugin_installation_id IS NOT NULL;
