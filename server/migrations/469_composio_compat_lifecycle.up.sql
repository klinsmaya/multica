-- Compatibility-only records have no foreign keys or cascading deletion.
-- Keep task tombstones after source rows disappear so cleanup remains durable.
CREATE SEQUENCE composio_compat_policy_revision;
CREATE TABLE composio_compat_task (
 task_id uuid PRIMARY KEY,
 agent_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 owner_id uuid NOT NULL,
 actor_id uuid NOT NULL,
 toolkits text[] NOT NULL DEFAULT '{}',
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE composio_compat_outbox (
 task_id uuid PRIMARY KEY,
 attempts integer NOT NULL DEFAULT 0,
 available_at timestamptz NOT NULL DEFAULT now(),
 lease_until timestamptz,
 completed_at timestamptz,
 last_error_code text
);
CREATE FUNCTION composio_compat_task_revoke() RETURNS trigger AS $$
BEGIN
 IF TG_OP='DELETE' OR (NEW.status IS DISTINCT FROM OLD.status AND NEW.status NOT IN ('dispatched','running')) THEN
  INSERT INTO composio_compat_outbox(task_id)
  SELECT task_id FROM composio_compat_task WHERE task_id=OLD.id ON CONFLICT DO NOTHING;
 END IF;
 RETURN OLD;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER trg_composio_compat_task_revoke AFTER UPDATE OF status OR DELETE ON agent_task_queue FOR EACH ROW EXECUTE FUNCTION composio_compat_task_revoke();
CREATE FUNCTION composio_compat_agent_revoke() RETURNS trigger AS $$
BEGIN
 IF TG_OP='UPDATE' AND ROW(NEW.workspace_id,NEW.owner_id,NEW.composio_toolkit_allowlist,NEW.permission_mode,NEW.archived_at) IS NOT DISTINCT FROM ROW(OLD.workspace_id,OLD.owner_id,OLD.composio_toolkit_allowlist,OLD.permission_mode,OLD.archived_at) THEN RETURN NEW; END IF;
 INSERT INTO composio_compat_outbox(task_id)
 SELECT task_id FROM composio_compat_task WHERE agent_id=OLD.id ON CONFLICT DO NOTHING;
 RETURN OLD;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER trg_composio_compat_agent_revoke AFTER UPDATE OF workspace_id,owner_id,composio_toolkit_allowlist,permission_mode,archived_at OR DELETE ON agent FOR EACH ROW EXECUTE FUNCTION composio_compat_agent_revoke();
CREATE FUNCTION composio_compat_target_revoke() RETURNS trigger AS $$
BEGIN
 IF TG_OP='UPDATE' AND ROW(NEW.agent_id,NEW.target_type,NEW.target_id) IS NOT DISTINCT FROM ROW(OLD.agent_id,OLD.target_type,OLD.target_id) THEN RETURN NEW; END IF;
 INSERT INTO composio_compat_outbox(task_id)
 SELECT task_id FROM composio_compat_task WHERE agent_id=OLD.agent_id ON CONFLICT DO NOTHING;
 RETURN OLD;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER trg_composio_compat_target_revoke AFTER UPDATE OR DELETE ON agent_invocation_target FOR EACH ROW EXECUTE FUNCTION composio_compat_target_revoke();
CREATE FUNCTION composio_compat_connection_revoke() RETURNS trigger AS $$
BEGIN
 IF TG_OP='UPDATE' AND ROW(NEW.user_id,NEW.toolkit_slug,NEW.auth_config_id,NEW.connected_account_id,NEW.status) IS NOT DISTINCT FROM ROW(OLD.user_id,OLD.toolkit_slug,OLD.auth_config_id,OLD.connected_account_id,OLD.status) THEN RETURN NEW; END IF;
 INSERT INTO composio_compat_outbox(task_id)
 SELECT task_id FROM composio_compat_task WHERE owner_id=OLD.user_id AND OLD.toolkit_slug=ANY(toolkits) ON CONFLICT DO NOTHING;
 RETURN OLD;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER trg_composio_compat_connection_revoke AFTER UPDATE OR DELETE ON user_composio_connection FOR EACH ROW EXECUTE FUNCTION composio_compat_connection_revoke();
CREATE FUNCTION composio_compat_member_revoke() RETURNS trigger AS $$
BEGIN
 IF TG_OP='UPDATE' AND ROW(NEW.user_id,NEW.workspace_id) IS NOT DISTINCT FROM ROW(OLD.user_id,OLD.workspace_id) THEN RETURN NEW; END IF;
 INSERT INTO composio_compat_outbox(task_id)
 SELECT task_id FROM composio_compat_task WHERE workspace_id=OLD.workspace_id AND (actor_id=OLD.user_id OR owner_id=OLD.user_id) ON CONFLICT DO NOTHING;
 RETURN OLD;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER trg_composio_compat_member_revoke AFTER UPDATE OF user_id,workspace_id OR DELETE ON member FOR EACH ROW EXECUTE FUNCTION composio_compat_member_revoke();
