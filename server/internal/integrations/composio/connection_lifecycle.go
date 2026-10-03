package composio

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	composiosdk "github.com/multica-ai/multica/server/pkg/composio"
)

// PendingRemoteRevocation means local execution is disabled, not that the
// provider has acknowledged revocation. State contains no provider secrets.
type PendingRemoteRevocation struct{ State string }

func (e *PendingRemoteRevocation) Error() string {
	return "connection disabled locally; remote revocation: " + e.State
}

func (s *Service) disconnectCompatibility(ctx context.Context, owner, id pgtype.UUID) error {
	if s.lifecycleDB == nil {
		return errors.New("compatibility lifecycle unavailable")
	}
	tx, err := s.lifecycleDB.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var account string
	if err = tx.QueryRow(ctx, `SELECT connected_account_id FROM user_composio_connection WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, owner).Scan(&account); err != nil {
		return ErrConnectionNotFound
	}
	if _, err = tx.Exec(ctx, `INSERT INTO composio_compat_connection_outbox(connection_id,owner_id,connected_account_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, id, owner, account); err != nil {
		return errors.New("connection cleanup unavailable")
	}
	if _, err = tx.Exec(ctx, `UPDATE user_composio_connection SET status='revoked',updated_at=now() WHERE id=$1 AND user_id=$2`, id, owner); err != nil {
		return errors.New("local disconnect unavailable")
	}
	var state string
	if err = tx.QueryRow(ctx, `SELECT state FROM composio_compat_connection_outbox WHERE connection_id=$1`, id).Scan(&state); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return errors.New("local disconnect unavailable")
	}
	if state == "CLEANED" {
		return nil
	}
	return &PendingRemoteRevocation{State: state}
}

func (s *Service) DrainConnectionRevocation(ctx context.Context) error {
	if !s.compat || s.lifecycleDB == nil {
		return nil
	}
	var id, account, state string
	var attempt int
	err := s.lifecycleDB.QueryRow(ctx, `WITH candidate AS(SELECT connection_id FROM composio_compat_connection_outbox WHERE state IN ('PENDING','REVOKED') AND available_at<=now() AND (lease_until IS NULL OR lease_until<now()) ORDER BY available_at FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE composio_compat_connection_outbox o SET attempts=attempts+1,lease_until=now()+interval '30 seconds' FROM candidate c WHERE o.connection_id=c.connection_id RETURNING o.connection_id::text,o.connected_account_id,o.state,o.attempts`).Scan(&id, &account, &state, &attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.New("connection cleanup unavailable")
	}
	code := ""
	if state == "PENDING" {
		err = s.sdk.RevokeConnection(ctx, account)
		if err == nil {
			state = "REVOKED"
		} else {
			code = "GATEWAY_REVOCATION_PENDING"
			var api *composiosdk.APIError
			if errors.As(err, &api) {
				if api.HTTPStatus == 501 {
					state = "UNSUPPORTED"
					code = "REMOTE_REVOCATION_UNSUPPORTED"
				}
				if api.HTTPStatus == 409 && api.Slug == "REMOTE_REVOCATION_BLOCKED" {
					state = "BLOCKED"
					code = "REMOTE_REVOCATION_BLOCKED"
				}
			}
		}
	}
	if state == "REVOKED" {
		if s.sdk.DeleteConnectedAccount(ctx, account) == nil {
			state = "CLEANED"
		} else {
			code = "NATIVE_CLEANUP_PENDING"
		}
	}
	_, err = s.lifecycleDB.Exec(ctx, `UPDATE composio_compat_connection_outbox SET state=$3,lease_until=NULL,available_at=now()+interval '10 seconds',last_error_code=$4 WHERE connection_id=$1::uuid AND attempts=$2`, id, attempt, state, code)
	return err
}

// CompatibilityStatus exposes only the authenticated owner's local cleanup state.
func (s *Service) CompatibilityStatus(ctx context.Context, owner pgtype.UUID) (map[string]any, error) {
	backend := "official"
	items := make([]map[string]string, 0)
	if s.compat {
		backend = "compat"
		if s.lifecycleDB == nil {
			return nil, errors.New("compatibility lifecycle unavailable")
		}
		rows, err := s.lifecycleDB.Query(ctx, `SELECT o.connection_id::text,coalesce(c.toolkit_slug,''),o.state FROM composio_compat_connection_outbox o LEFT JOIN user_composio_connection c ON c.id=o.connection_id AND c.user_id=o.owner_id WHERE o.owner_id=$1 AND o.state<>'CLEANED' ORDER BY o.connection_id LIMIT 1000`, owner)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id, toolkit, state string
			if err = rows.Scan(&id, &toolkit, &state); err != nil {
				return nil, err
			}
			items = append(items, map[string]string{"connection_id": id, "toolkit_slug": toolkit, "remote_revocation": state})
		}
		if rows.Err() != nil {
			return nil, rows.Err()
		}
	}
	return map[string]any{"backend": backend, "cleanup": items}, nil
}
