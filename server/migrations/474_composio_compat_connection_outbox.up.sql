CREATE TABLE composio_compat_connection_outbox (
 connection_id uuid PRIMARY KEY,
 owner_id uuid NOT NULL,
 connected_account_id text NOT NULL,
 state text NOT NULL DEFAULT 'PENDING' CHECK(state IN ('PENDING','REVOKED','CLEANED','UNSUPPORTED','BLOCKED')),
 attempts integer NOT NULL DEFAULT 0,
 available_at timestamptz NOT NULL DEFAULT now(),
 lease_until timestamptz,
 last_error_code text
);
CREATE FUNCTION composio_compat_no_reactivate() RETURNS trigger AS $$
BEGIN
 IF NEW.status='active' AND EXISTS(SELECT 1 FROM composio_compat_connection_outbox WHERE owner_id=NEW.user_id AND connected_account_id=NEW.connected_account_id) THEN
  RAISE EXCEPTION 'compatibility connection has been disconnected';
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER composio_compat_no_reactivate BEFORE INSERT OR UPDATE ON user_composio_connection FOR EACH ROW EXECUTE FUNCTION composio_compat_no_reactivate();
