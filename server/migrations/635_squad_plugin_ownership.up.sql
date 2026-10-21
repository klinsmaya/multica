-- A plugin's `squad` resource becomes an ordinary workspace squad whose
-- leader and members are the installation's own agents. Same ownership rule
-- as agent (633): NULL means a person made it. No FK per repository policy.
ALTER TABLE squad ADD COLUMN IF NOT EXISTS plugin_installation_id UUID;
