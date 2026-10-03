DROP TRIGGER IF EXISTS trg_composio_compat_member_revoke ON member;
DROP FUNCTION IF EXISTS composio_compat_member_revoke();
-- Rollback must first stop compatibility issuance and revoke all live sessions.
DROP TRIGGER IF EXISTS trg_composio_compat_connection_revoke ON user_composio_connection;
DROP FUNCTION IF EXISTS composio_compat_connection_revoke();
DROP TRIGGER IF EXISTS trg_composio_compat_target_revoke ON agent_invocation_target;
DROP FUNCTION IF EXISTS composio_compat_target_revoke();
DROP TRIGGER IF EXISTS trg_composio_compat_agent_revoke ON agent;
DROP FUNCTION IF EXISTS composio_compat_agent_revoke();
DROP TRIGGER IF EXISTS trg_composio_compat_task_revoke ON agent_task_queue;
DROP FUNCTION IF EXISTS composio_compat_task_revoke();
DROP TABLE composio_compat_outbox;
DROP TABLE composio_compat_task;
DROP SEQUENCE composio_compat_policy_revision;
