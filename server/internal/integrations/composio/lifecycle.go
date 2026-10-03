package composio

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"github.com/jackc/pgx/v5"
	"log/slog"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeapps"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// BuildClaimOverlay requires the handler's fresh invocation decision. Register
// the trusted task before contacting the gateway so terminal transitions cannot
// lose cleanup, including when the task disappears during the network call.
func (s *Service) BuildClaimOverlay(ctx context.Context, task db.AgentTaskQueue, agent db.Agent) (runtimeapps.MCPOverlayResult, error) {
	empty := runtimeapps.MCPOverlayResult{}
	if !s.compat {
		return empty, errors.New("compatibility backend required")
	}
	if len(agent.ComposioToolkitAllowlist) == 0 {
		return empty, nil
	}
	if s.lifecycleDB == nil || s.authorizeClaim == nil || !task.OriginatorUserID.Valid || !agent.OwnerID.Valid || task.AgentID != agent.ID {
		return empty, errors.New("trusted task context unavailable")
	}
	tx, err := s.lifecycleDB.Begin(ctx)
	if err != nil {
		return empty, errors.New("lifecycle unavailable")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM agent_task_queue WHERE id=$1 AND agent_id=$2 AND originator_user_id=$3 FOR UPDATE`, task.ID, agent.ID, task.OriginatorUserID).Scan(&status); err != nil || (status != "dispatched" && status != "running") {
		return empty, errors.New("task is not claimable")
	}
	// Hold the source rows through registry commit. Withdrawal triggers cannot
	// miss an uncommitted registry insert; their writes wait for these locks.
	if _, err = tx.Exec(ctx, `SELECT id FROM agent WHERE id=$1 FOR SHARE`, agent.ID); err != nil {
		return empty, errors.New("agent authority unavailable")
	}
	currentAgent, err := db.New(tx).GetAgent(ctx, agent.ID)
	if err != nil || !currentAgent.OwnerID.Valid || currentAgent.ArchivedAt.Valid {
		return empty, errors.New("agent unavailable")
	}
	agent = currentAgent
	for _, lock := range []struct {
		sql  string
		args []any
	}{
		{`SELECT id FROM agent_invocation_target WHERE agent_id=$1 FOR SHARE`, []any{agent.ID}},
		{`SELECT id FROM member WHERE workspace_id=$1 AND user_id IN ($2,$3) FOR SHARE`, []any{agent.WorkspaceID, agent.OwnerID, task.OriginatorUserID}},
		{`SELECT id FROM user_composio_connection WHERE user_id=$1 FOR SHARE`, []any{agent.OwnerID}},
	} {
		if _, err = tx.Exec(ctx, lock.sql, lock.args...); err != nil {
			return empty, errors.New("source authority unavailable")
		}
	}
	if !s.authorizeClaim(ctx, agent, util.UUIDToString(task.OriginatorUserID)) {
		return empty, errors.New("invocation permission revoked")
	}
	_, err = tx.Exec(ctx, `INSERT INTO composio_compat_task(task_id,agent_id,workspace_id,owner_id,actor_id,toolkits) SELECT $1,id,workspace_id,owner_id,$3,composio_toolkit_allowlist FROM agent WHERE id=$2 AND owner_id=$4 AND archived_at IS NULL ON CONFLICT DO NOTHING`, task.ID, agent.ID, task.OriginatorUserID, agent.OwnerID)
	if err != nil {
		return empty, errors.New("lifecycle unavailable")
	}
	var current bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM composio_compat_task t WHERE task_id=$1 AND agent_id=$2 AND owner_id=$3 AND actor_id=$4 AND workspace_id=$5 AND NOT EXISTS(SELECT 1 FROM composio_compat_outbox o WHERE o.task_id=t.task_id))`, task.ID, agent.ID, agent.OwnerID, task.OriginatorUserID, agent.WorkspaceID).Scan(&current); err != nil || !current {
		return empty, errors.New("task authorization revoked")
	}
	var revision int64
	if err = tx.QueryRow(ctx, `SELECT nextval('composio_compat_policy_revision')`).Scan(&revision); err != nil {
		return empty, errors.New("policy revision unavailable")
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, errors.New("lifecycle unavailable")
	}
	return s.buildTaskOverlay(ctx, task.OriginatorUserID, agent, &task, revision)
}

// DrainRevocation leases one durable task tombstone. The gateway operation is
// idempotent, so a lost response or expired lease safely retries after restart.
func (s *Service) DrainRevocation(ctx context.Context) error {
	if !s.compat || s.lifecycleDB == nil {
		return nil
	}
	var task string
	var attempt int
	err := s.lifecycleDB.QueryRow(ctx, `WITH candidate AS(SELECT task_id FROM composio_compat_outbox WHERE completed_at IS NULL AND available_at<=now() AND (lease_until IS NULL OR lease_until<now()) ORDER BY available_at FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE composio_compat_outbox o SET attempts=attempts+1,lease_until=now()+interval '30 seconds' FROM candidate c WHERE o.task_id=c.task_id RETURNING o.task_id::text,o.attempts`).Scan(&task, &attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.New("lifecycle queue unavailable")
	}
	code := ""
	done := true
	if s.compatClient.RevokeCompatibilityTask(ctx, task) != nil {
		done = false
		code = "GATEWAY_REVOKE_FAILED"
	}
	_, err = s.lifecycleDB.Exec(ctx, `UPDATE composio_compat_outbox SET completed_at=CASE WHEN $3 THEN now() ELSE NULL END,lease_until=NULL,available_at=now()+interval '10 seconds',last_error_code=$4 WHERE task_id=$1::uuid AND attempts=$2`, task, attempt, done, code)
	if err != nil {
		return errors.New("lifecycle result unavailable")
	}
	return nil
}
func (s *Service) RunLifecycle(ctx context.Context) {
	if !s.compat {
		return
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			attempt, cancel := context.WithTimeout(ctx, 15*time.Second)
			if s.DrainRevocation(attempt) != nil {
				slog.Warn("composio compatibility cleanup deferred")
			}
			cancel()
		}
	}
}

// ValidateClaimPins reads current source state on every gateway admission.
func (s *Service) ValidateClaimPins(ctx context.Context, task db.AgentTaskQueue, agent db.Agent, owner, actor string, pins map[string][]string) bool {
	if !s.compat || s.lifecycleDB == nil || !task.OriginatorUserID.Valid || util.UUIDToString(task.OriginatorUserID) != actor || util.UUIDToString(agent.OwnerID) != owner || task.AgentID != agent.ID || agent.ArchivedAt.Valid || (task.Status != "dispatched" && task.Status != "running") {
		return false
	}
	var registered bool
	if s.lifecycleDB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM composio_compat_task t WHERE task_id=$1 AND agent_id=$2 AND owner_id=$3 AND actor_id=$4 AND workspace_id=$5 AND NOT EXISTS(SELECT 1 FROM composio_compat_outbox o WHERE o.task_id=t.task_id))`, task.ID, agent.ID, agent.OwnerID, task.OriginatorUserID, agent.WorkspaceID).Scan(&registered) != nil || !registered {
		return false
	}
	rows, err := s.store.ListActiveUserComposioConnections(ctx, agent.OwnerID)
	if err != nil {
		return false
	}
	current := pinConnectedAccounts(rows, normaliseAllowlistToSet(agent.ComposioToolkitAllowlist))
	if len(pins) == 0 {
		return false
	}
	for toolkit, ids := range pins {
		allowed, ok := current[toolkit].([]string)
		if !ok || len(ids) != 1 || len(allowed) != 1 || ids[0] != allowed[0] {
			return false
		}
	}
	return true
}

// Authority authentication is independent of browser, PAT and task credentials.
func (s *Service) VerifyAuthorityToken(value string) bool {
	if !s.compat || len(s.authorityToken) < 32 {
		return false
	}
	got, want := sha256.Sum256([]byte(value)), sha256.Sum256([]byte(s.authorityToken))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}
