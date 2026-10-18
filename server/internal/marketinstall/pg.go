package marketinstall

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Beginner is the process pool. Each store call commits its own transaction,
// so an armed id and the native insert are not one commit.
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

var (
	_ installStore = pgStore{}
	_ nativePort   = pgNatives{}
)

func newPGStore(db Beginner) installStore {
	return pgStore{db: db}
}

func newPGNatives(db Beginner) nativePort {
	return pgNatives{db: db}
}

type pgStore struct{ db Beginner }

func (s pgStore) Claim(ctx context.Context, row Operation) (Operation, bool, bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Operation{}, false, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1)::bigint)`, string(row.Actor.WorkspaceID)+"/"+string(row.IdempotencyKey)); err != nil {
		return Operation{}, false, false, err
	}
	existing, err := loadByKey(ctx, tx, row.Actor.WorkspaceID, row.IdempotencyKey)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return Operation{}, false, false, err
		}
		return existing, false, existing.PlanHash != row.PlanHash, nil
	}
	if !errors.Is(err, errMissing) {
		return Operation{}, false, false, err
	}
	steps, err := json.Marshal(row.Steps)
	if err != nil {
		return Operation{}, false, false, err
	}
	payload, err := json.Marshal(row.payload)
	if err != nil {
		return Operation{}, false, false, err
	}
	if row.InstanceID != "" {
		if _, err := tx.Exec(ctx, `
INSERT INTO market_install_instance (
    id, workspace_id, operation_id, listing_id, listing_version, plan_hash,
    revision, fence_token, owner_member_id
) VALUES ($1,$2,$3,$4,$5,$6,1,$7,$8)`,
			row.InstanceID, row.Actor.WorkspaceID, row.ID, row.ListingID, row.Version, row.PlanHash, row.Fence, row.Actor.MemberID); err != nil {
			return Operation{}, false, false, err
		}
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO market_install_operation (
    id, workspace_id, instance_id, idempotency_key, plan_hash, actor_member_id,
    actor_user_id, listing_id, listing_version, approval_ref, policy_version,
    fence_token, steps, payload, revision, outbox_open, lease_token, leased_until, created_at
) VALUES (
    $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19
)`,
		row.ID, row.Actor.WorkspaceID, nullUUID(string(row.InstanceID)), row.IdempotencyKey, row.PlanHash,
		row.Actor.MemberID, row.Actor.UserID, row.ListingID, row.Version, row.ApprovalRef, row.PolicyVersion,
		row.Fence, steps, payload, row.revision, row.outboxOpen, nullUUID(row.leaseToken), row.leasedUntil, row.CreatedAt); err != nil {
		return Operation{}, false, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Operation{}, false, false, err
	}
	return row.clone(), true, false, nil
}

func (s pgStore) Load(ctx context.Context, workspace WorkspaceID, id OperationID) (Operation, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Operation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := loadByID(ctx, tx, workspace, id)
	if err != nil {
		return Operation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Operation{}, err
	}
	return row, nil
}

func (s pgStore) Save(ctx context.Context, row Operation) (Operation, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Operation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	steps, err := json.Marshal(row.Steps)
	if err != nil {
		return Operation{}, err
	}
	var updated Operation
	err = scanOperation(tx.QueryRow(ctx, `
UPDATE market_install_operation
SET steps = $1, revision = revision + 1, outbox_open = $2, updated_at = now()
WHERE id = $3 AND workspace_id = $4 AND revision = $5 AND lease_token = $6
RETURNING id, workspace_id, instance_id, idempotency_key, plan_hash, actor_member_id,
    actor_user_id, listing_id, listing_version, approval_ref, policy_version, fence_token,
    steps, payload, revision, outbox_open, lease_token, leased_until, created_at`,
		steps, row.outboxOpen, row.ID, row.Actor.WorkspaceID, row.revision, nullUUID(row.leaseToken)), &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return Operation{}, errRevision
	}
	if err != nil {
		return Operation{}, err
	}
	updated.bindings = append([]Binding(nil), row.bindings...)
	if err := replaceBindings(ctx, tx, updated); err != nil {
		return Operation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Operation{}, err
	}
	return updated, nil
}

func (s pgStore) RotateLease(ctx context.Context, workspace WorkspaceID, id OperationID, token string, until, now time.Time) (Operation, bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Operation{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var updated Operation
	err = scanOperation(tx.QueryRow(ctx, `
UPDATE market_install_operation
SET lease_token = $1, leased_until = $2, revision = revision + 1, updated_at = now()
WHERE id = $3 AND workspace_id = $4 AND (leased_until IS NULL OR leased_until <= $5)
RETURNING id, workspace_id, instance_id, idempotency_key, plan_hash, actor_member_id,
    actor_user_id, listing_id, listing_version, approval_ref, policy_version, fence_token,
    steps, payload, revision, outbox_open, lease_token, leased_until, created_at`,
		token, until, id, workspace, now), &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		current, loadErr := loadByID(ctx, tx, workspace, id)
		if loadErr != nil {
			return Operation{}, false, loadErr
		}
		if err := tx.Commit(ctx); err != nil {
			return Operation{}, false, err
		}
		return current, false, nil
	}
	if err != nil {
		return Operation{}, false, err
	}
	if err := attachBindings(ctx, tx, &updated); err != nil {
		return Operation{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Operation{}, false, err
	}
	return updated, true, nil
}

func (s pgStore) ClaimDue(ctx context.Context, now time.Time, limit int, until func(time.Time) time.Time, token func() string) ([]Operation, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
SELECT id
FROM market_install_operation
WHERE outbox_open AND (leased_until IS NULL OR leased_until <= $1)
ORDER BY created_at
LIMIT $2
FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, err
	}
	var ids []OperationID
	for rows.Next() {
		var id OperationID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Operation, 0, len(ids))
	for _, id := range ids {
		var updated Operation
		err := scanOperation(tx.QueryRow(ctx, `
UPDATE market_install_operation
SET lease_token = $1, leased_until = $2, revision = revision + 1, updated_at = now()
WHERE id = $3
RETURNING id, workspace_id, instance_id, idempotency_key, plan_hash, actor_member_id,
    actor_user_id, listing_id, listing_version, approval_ref, policy_version, fence_token,
    steps, payload, revision, outbox_open, lease_token, leased_until, created_at`,
			token(), until(now), id), &updated)
		if err != nil {
			return nil, err
		}
		if err := attachBindings(ctx, tx, &updated); err != nil {
			return nil, err
		}
		out = append(out, updated)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (s pgStore) ListInstances(ctx context.Context, workspace WorkspaceID) ([]Instance, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
SELECT id FROM market_install_operation
WHERE workspace_id = $1 AND instance_id IS NOT NULL
ORDER BY created_at`, workspace)
	if err != nil {
		return nil, err
	}
	var ids []OperationID
	for rows.Next() {
		var id OperationID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Instance, 0, len(ids))
	for _, id := range ids {
		row, err := loadByID(ctx, tx, workspace, id)
		if err != nil {
			return nil, err
		}
		out = append(out, instanceFrom(row))
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (s pgStore) GetInstance(ctx context.Context, workspace WorkspaceID, id InstanceID) (Instance, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Instance{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var op OperationID
	err = tx.QueryRow(ctx, `
SELECT id FROM market_install_operation
WHERE workspace_id = $1 AND instance_id = $2`, workspace, id).Scan(&op)
	if errors.Is(err, pgx.ErrNoRows) {
		return Instance{}, errMissing
	}
	if err != nil {
		return Instance{}, err
	}
	row, err := loadByID(ctx, tx, workspace, op)
	if err != nil {
		return Instance{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Instance{}, err
	}
	return instanceFrom(row), nil
}

func loadByKey(ctx context.Context, tx pgx.Tx, workspace WorkspaceID, key IdempotencyKey) (Operation, error) {
	var id OperationID
	err := tx.QueryRow(ctx, `
SELECT id FROM market_install_operation
WHERE workspace_id = $1 AND idempotency_key = $2`, workspace, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Operation{}, errMissing
	}
	if err != nil {
		return Operation{}, err
	}
	return loadByID(ctx, tx, workspace, id)
}

func loadByID(ctx context.Context, tx pgx.Tx, workspace WorkspaceID, id OperationID) (Operation, error) {
	var row Operation
	err := scanOperation(tx.QueryRow(ctx, `
SELECT id, workspace_id, instance_id, idempotency_key, plan_hash, actor_member_id,
    actor_user_id, listing_id, listing_version, approval_ref, policy_version, fence_token,
    steps, payload, revision, outbox_open, lease_token, leased_until, created_at
FROM market_install_operation
WHERE id = $1 AND workspace_id = $2`, id, workspace), &row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Operation{}, errMissing
	}
	if err != nil {
		return Operation{}, err
	}
	if err := attachBindings(ctx, tx, &row); err != nil {
		return Operation{}, err
	}
	return row, nil
}

func attachBindings(ctx context.Context, tx pgx.Tx, row *Operation) error {
	if row.InstanceID == "" {
		return nil
	}
	binds, err := tx.Query(ctx, `
SELECT kind, resource_id, ref FROM market_install_binding
WHERE workspace_id = $1 AND instance_id = $2`, row.Actor.WorkspaceID, row.InstanceID)
	if err != nil {
		return err
	}
	defer binds.Close()
	for binds.Next() {
		var binding Binding
		if err := binds.Scan(&binding.Kind, &binding.ResourceID, &binding.Ref); err != nil {
			return err
		}
		row.bindings = append(row.bindings, binding)
	}
	return binds.Err()
}

func scanOperation(row pgx.Row, op *Operation) error {
	var instanceID, lease *string
	var steps, payload []byte
	var leasedUntil *time.Time
	err := row.Scan(
		&op.ID, &op.Actor.WorkspaceID, &instanceID, &op.IdempotencyKey, &op.PlanHash,
		&op.Actor.MemberID, &op.Actor.UserID, &op.ListingID, &op.Version, &op.ApprovalRef,
		&op.PolicyVersion, &op.Fence, &steps, &payload, &op.revision, &op.outboxOpen,
		&lease, &leasedUntil, &op.CreatedAt,
	)
	if err != nil {
		return err
	}
	if instanceID != nil {
		op.InstanceID = InstanceID(*instanceID)
	}
	if lease != nil {
		op.leaseToken = *lease
	}
	if leasedUntil != nil {
		op.leasedUntil = *leasedUntil
	}
	if err := json.Unmarshal(steps, &op.Steps); err != nil {
		return err
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &op.payload); err != nil {
			return err
		}
	}
	return nil
}

func replaceBindings(ctx context.Context, tx pgx.Tx, row Operation) error {
	if row.InstanceID == "" {
		return nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM market_install_binding WHERE workspace_id = $1 AND instance_id = $2`, row.Actor.WorkspaceID, row.InstanceID); err != nil {
		return err
	}
	for _, binding := range row.bindings {
		if _, err := tx.Exec(ctx, `
INSERT INTO market_install_binding (instance_id, workspace_id, kind, resource_id, ref)
VALUES ($1,$2,$3,$4,$5)`, row.InstanceID, row.Actor.WorkspaceID, binding.Kind, binding.ResourceID, binding.Ref); err != nil {
			return err
		}
	}
	return nil
}

func nullUUID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

type pgNatives struct{ db Beginner }

func (n pgNatives) observeAgent(ctx context.Context, workspace WorkspaceID, id CorrelationID) (Presence, error) {
	return n.observeOne(ctx, `SELECT 1 FROM agent WHERE id = $1 AND workspace_id = $2`, id, workspace)
}

func (n pgNatives) observeSkill(ctx context.Context, workspace WorkspaceID, id CorrelationID) (Presence, error) {
	var content string
	err := n.queryRow(ctx, `SELECT content FROM skill WHERE id = $1 AND workspace_id = $2`, id, workspace).Scan(&content)
	return presenceFrom(err, SkillDigest(content, nil))
}

func (n pgNatives) observeMCP(ctx context.Context, workspace WorkspaceID, id CorrelationID) (Presence, error) {
	var config []byte
	err := n.queryRow(ctx, `SELECT config FROM workspace_mcp_server WHERE id = $1 AND workspace_id = $2`, id, workspace).Scan(&config)
	transport, rawURL := mcpConfigParts(config)
	return presenceFrom(err, MCPDigest(transport, rawURL))
}

func (n pgNatives) observeSkillBind(ctx context.Context, agentID, skillID CorrelationID) (Presence, error) {
	return n.observeOne(ctx, `SELECT 1 FROM agent_skill WHERE agent_id = $1 AND skill_id = $2`, agentID, skillID)
}

func (n pgNatives) observeMCPBind(ctx context.Context, agentID, serverID CorrelationID) (Presence, error) {
	return n.observeOne(ctx, `SELECT 1 FROM agent_mcp_server WHERE agent_id = $1 AND server_id = $2`, agentID, serverID)
}

func (n pgNatives) insertAgent(ctx context.Context, draft agentDraft) error {
	var runtime any
	if draft.RuntimeID != "" {
		runtime = draft.RuntimeID
	}
	var model any
	if draft.Model != "" {
		model = draft.Model
	}
	return n.exec(ctx, `
INSERT INTO agent (
    id, workspace_id, name, description, runtime_mode, runtime_config, runtime_id,
    visibility, max_concurrent_tasks, owner_id, instructions, custom_env, custom_args,
    mcp_config, model, permission_mode, kind, composio_toolkit_allowlist
) VALUES (
    $1,$2,$3,$4,'local','{}'::jsonb,$5,'private',1,$6,$7,'{}'::jsonb,'[]'::jsonb,'{}'::jsonb,$8,'private','user',$9
)`, draft.ID, draft.Workspace, draft.Name, draft.Description, runtime, draft.Owner, draft.Instructions, model, draft.Toolkits)
}

func (n pgNatives) insertSkill(ctx context.Context, draft skillDraft) error {
	return n.exec(ctx, `
INSERT INTO skill (id, workspace_id, name, description, content, config, created_by)
VALUES ($1,$2,$3,$4,$5,'{}'::jsonb,$6)`, draft.ID, draft.Workspace, draft.Name, draft.Description, draft.Content, draft.CreatedBy)
}

func (n pgNatives) insertMCP(ctx context.Context, draft mcpDraft) error {
	if URLHasUserinfo(draft.URL) {
		return errDefinite
	}
	config, err := json.Marshal(map[string]string{"transport": draft.Transport, "url": draft.URL})
	if err != nil {
		return err
	}
	return n.exec(ctx, `
INSERT INTO workspace_mcp_server (id, workspace_id, name, config, created_by)
VALUES ($1,$2,$3,$4,$5)`, draft.ID, draft.Workspace, draft.Name, config, draft.CreatedBy)
}

func (n pgNatives) bindSkill(ctx context.Context, agentID, skillID CorrelationID) error {
	return n.exec(ctx, `INSERT INTO agent_skill (agent_id, skill_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, agentID, skillID)
}

func (n pgNatives) bindMCP(ctx context.Context, agentID, serverID CorrelationID) error {
	return n.exec(ctx, `INSERT INTO agent_mcp_server (agent_id, server_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, agentID, serverID)
}

func (n pgNatives) observeOne(ctx context.Context, sql string, args ...any) (Presence, error) {
	var one int
	err := n.queryRow(ctx, sql, args...).Scan(&one)
	return presenceFrom(err, "")
}

func (n pgNatives) queryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	tx, err := n.db.Begin(ctx)
	if err != nil {
		return errRow{err: err}
	}
	return txRow{tx: tx, row: tx.QueryRow(ctx, sql, args...)}
}

func (n pgNatives) exec(ctx context.Context, sql string, args ...any) error {
	tx, err := n.db.Begin(ctx)
	if err != nil {
		return classifyNative(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		return classifyNative(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return classifyNative(err)
	}
	return nil
}

type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }

type txRow struct {
	tx  pgx.Tx
	row pgx.Row
}

func (r txRow) Scan(dest ...any) error {
	err := r.row.Scan(dest...)
	_ = r.tx.Rollback(context.Background())
	return err
}

func presenceFrom(err error, digest string) (Presence, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return Presence{Definitive: true}, nil
	}
	if err != nil {
		return Presence{}, nil
	}
	return Presence{Found: true, Definitive: true, Digest: digest}, nil
}

func classifyNative(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return errAmbiguous
	}
	if pgErr.Code != "23505" {
		return errDefinite
	}
	if strings.Contains(pgErr.ConstraintName, "pkey") {
		return errPKExists
	}
	return errNameTaken
}

func mcpConfigParts(config []byte) (string, string) {
	var parsed struct {
		Transport string `json:"transport"`
		URL       string `json:"url"`
	}
	if err := json.Unmarshal(config, &parsed); err != nil {
		return "", ""
	}
	return parsed.Transport, parsed.URL
}
