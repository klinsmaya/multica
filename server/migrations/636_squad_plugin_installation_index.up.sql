CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_squad_plugin_installation
    ON squad (plugin_installation_id)
    WHERE plugin_installation_id IS NOT NULL;
