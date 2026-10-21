-- open-connector integration: which connector services (e.g. "github") an
-- agent may use. Mirrors agent.composio_toolkit_allowlist.
ALTER TABLE agent
    ADD COLUMN connector_service_allowlist TEXT[] NULL;

COMMENT ON COLUMN agent.connector_service_allowlist IS
    'open-connector service names this agent may mount as MCP. NULL or empty array = no overlay. Intersected with the agent owner''s active connections when the task overlay is built.';
