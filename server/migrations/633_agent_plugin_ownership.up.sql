-- A plugin's `agent` resources become ordinary workspace agents, created
-- without a runtime so the workspace binds one. As with skill (368), the only
-- thing to remember is which installation contributed the row: uninstall and
-- upgrade pruning archive exactly those agents and never one a person made.
-- NULL means a person made it. No FK per repository policy.
ALTER TABLE agent ADD COLUMN IF NOT EXISTS plugin_installation_id UUID;
