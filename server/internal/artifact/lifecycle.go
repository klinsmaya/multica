package artifact

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	// defaultTrashRetention is an ops default. It is not a promise that a
	// deleted artifact disappears, and it does not authorize a purge.
	defaultTrashRetention = 30 * 24 * time.Hour
	// defaultConflictDraftRetention is independent of the 24h staging window.
	defaultConflictDraftRetention = 7 * 24 * time.Hour
)

// OpsRetention is the set of ops defaults. A zero field uses the built-in
// duration. These values are not a product promise.
type OpsRetention struct {
	Trash            time.Duration
	Staging          time.Duration
	ConflictDraft    time.Duration
	FailedDerivative time.Duration
}

// GCResult is one mark-and-sweep pass. Paused means no blob was deleted.
type GCResult struct {
	Paused  bool
	Deleted int
	Kept    int
}

// SweepResult is one retention pass. Conflict drafts are expired only when
// their own deadline has passed.
type SweepResult struct {
	Paused             bool
	StagingExpired     int
	DerivativesCleared int
}

// OpsHealth is the small report for GC, quota, and the conversion queue.
type OpsHealth struct {
	GCPaused                  bool   `json:"gc_paused"`
	LastGCDeleted             int    `json:"last_gc_deleted"`
	ReservedBytes             int64  `json:"reserved_bytes"`
	CommittedBytes            int64  `json:"committed_bytes"`
	DriftBytes                int64  `json:"drift_bytes"`
	ConversionPending         int64  `json:"conversion_pending"`
	ConversionFailed          int64  `json:"conversion_failed"`
	TrashPastRetention        int64  `json:"trash_past_retention"`
	RetentionClass            string `json:"retention_class"`
	TrashRetention            string `json:"trash_retention"`
	StagingRetention          string `json:"staging_retention"`
	ConflictDraftRetention    string `json:"conflict_draft_retention"`
	FailedDerivativeRetention string `json:"failed_derivative_retention"`
}

func (s *Service) retention() OpsRetention {
	out := OpsRetention{
		Trash:            defaultTrashRetention,
		Staging:          uploadLifetime,
		ConflictDraft:    defaultConflictDraftRetention,
		FailedDerivative: FailedDerivativeRetention,
	}
	if s == nil {
		return out
	}
	if s.Retention.Trash > 0 {
		out.Trash = s.Retention.Trash
	}
	if s.Retention.Staging > 0 {
		out.Staging = s.Retention.Staging
	}
	if s.Retention.ConflictDraft > 0 {
		out.ConflictDraft = s.Retention.ConflictDraft
	}
	if s.Retention.FailedDerivative > 0 {
		out.FailedDerivative = s.Retention.FailedDerivative
	}
	return out
}

// Trash hides an artifact, expires open leases, and revokes every live share.
// Blobs stay. A second call on an already deleted row is a no-op.
func (s *Service) Trash(ctx context.Context, workspaceID, actorID, artifactID uuid.UUID) error {
	if err := s.configured(); err != nil {
		return err
	}
	if workspaceID == uuid.Nil || actorID == uuid.Nil || artifactID == uuid.Nil {
		return fmt.Errorf("%w: trash", ErrInvalidInput)
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	art, err := q.LockArtifact(ctx, db.LockArtifactParams{
		ID:          pgUUID(artifactID),
		WorkspaceID: pgUUID(workspaceID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if art.Status == "deleted" || art.DeletedAt.Valid {
		return nil
	}
	n, err := q.MarkArtifactDeleted(ctx, db.MarkArtifactDeletedParams{
		ID:          art.ID,
		WorkspaceID: art.WorkspaceID,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	if err := q.ExpireOpenArtifactEditSessions(ctx, art.ID); err != nil {
		return err
	}
	ids, err := q.ListLiveArtifactPublishSnapshotIDs(ctx, db.ListLiveArtifactPublishSnapshotIDsParams{
		WorkspaceID: art.WorkspaceID,
		ArtifactID:  art.ID,
	})
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := revokeSnapshotInTx(ctx, q, workspaceID, actorID, uuid.UUID(id.Bytes)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// RestoreTrash makes a deleted artifact active again. Share rows stay
// revoked. Edit sessions stay expired.
func (s *Service) RestoreTrash(ctx context.Context, workspaceID, artifactID uuid.UUID) error {
	if err := s.configured(); err != nil {
		return err
	}
	if workspaceID == uuid.Nil || artifactID == uuid.Nil {
		return fmt.Errorf("%w: restore trash", ErrInvalidInput)
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	art, err := q.LockArtifact(ctx, db.LockArtifactParams{
		ID:          pgUUID(artifactID),
		WorkspaceID: pgUUID(workspaceID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if art.Status != "deleted" || !art.DeletedAt.Valid {
		return ErrNotFound
	}
	n, err := q.MarkArtifactRestored(ctx, db.MarkArtifactRestoredParams{
		ID:          art.ID,
		WorkspaceID: art.WorkspaceID,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// PurgeExpiredTrash does not delete rows. Permanent delete stays refused
// until a legal-hold check exists.
func (s *Service) PurgeExpiredTrash(ctx context.Context, workspaceID uuid.UUID) error {
	if err := s.configured(); err != nil {
		return err
	}
	if workspaceID == uuid.Nil {
		return fmt.Errorf("%w: purge", ErrInvalidInput)
	}
	return ErrPurgeRefused
}

func revokeSnapshotInTx(ctx context.Context, q *db.Queries, workspaceID, actorID, snapshotID uuid.UUID) error {
	snap, err := q.GetArtifactPublishSnapshotByID(ctx, db.GetArtifactPublishSnapshotByIDParams{
		ID:          pgUUID(snapshotID),
		WorkspaceID: pgUUID(workspaceID),
	})
	if errors.Is(err, pgx.ErrNoRows) || snap.RevokedAt.Valid {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := q.RevokeArtifactPublishSnapshot(ctx, db.RevokeArtifactPublishSnapshotParams{
		ID:          pgUUID(snapshotID),
		WorkspaceID: pgUUID(workspaceID),
	}); err != nil {
		return err
	}
	if err := q.DeleteArtifactShareGrants(ctx, db.DeleteArtifactShareGrantsParams{
		SnapshotID:  pgUUID(snapshotID),
		WorkspaceID: pgUUID(workspaceID),
	}); err != nil {
		return err
	}
	if err := insertAudit(ctx, q, workspaceID, &actorID, "share.revoke", snapshotID, "allowed"); err != nil {
		return err
	}
	revID := uuid.UUID(snap.RevisionID.Bytes)
	artID := uuid.UUID(snap.ArtifactID.Bytes)
	return insertShareEvent(ctx, q, workspaceID, artID, revID, snapshotID, EventShareRevoked)
}

// CollectGarbage deletes blob rows in this workspace that no revision,
// snapshot, draft, open upload, or in-retention derivative still references.
// A paused sweep deletes nothing.
func (s *Service) CollectGarbage(ctx context.Context, workspaceID uuid.UUID, limit int) (GCResult, error) {
	if err := s.configured(); err != nil {
		return GCResult{}, err
	}
	if workspaceID == uuid.Nil {
		return GCResult{}, fmt.Errorf("%w: gc", ErrInvalidInput)
	}
	if s.GCPaused {
		return GCResult{Paused: true}, nil
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	q, err := s.queries()
	if err != nil {
		return GCResult{}, err
	}
	rows, err := q.ListUnreferencedArtifactBlobs(ctx, db.ListUnreferencedArtifactBlobsParams{
		KeyPrefix: pgText("workspaces/" + workspaceID.String() + "/"),
		Lim:       int32(limit),
	})
	if err != nil {
		return GCResult{}, err
	}
	result := GCResult{}
	for _, row := range rows {
		held, err := q.ArtifactStorageKeyHeld(ctx, row.StorageKey)
		if err != nil {
			return result, err
		}
		if held.Valid && held.Bool {
			result.Kept++
			continue
		}
		n, err := q.DeleteUnreferencedArtifactBlob(ctx, row.ID)
		if err != nil {
			return result, err
		}
		if n == 0 {
			result.Kept++
			continue
		}
		s.Store.Delete(ctx, row.StorageKey)
		result.Deleted++
	}
	s.lastGCDeleted = result.Deleted
	return result, nil
}

// SweepRetention expires overdue staging and clears failed derivative objects
// whose own deadline has passed. A conflict_draft is overdue only after its
// longer retention. A paused sweep deletes nothing.
func (s *Service) SweepRetention(ctx context.Context, workspaceID uuid.UUID, limit int) (SweepResult, error) {
	if s != nil && s.GCPaused {
		return SweepResult{Paused: true}, nil
	}
	n, err := s.ExpireUploads(ctx, workspaceID, limit)
	if err != nil {
		return SweepResult{}, err
	}
	cleared, err := s.sweepFailedDerivatives(ctx, workspaceID, limit)
	if err != nil {
		return SweepResult{StagingExpired: n}, err
	}
	return SweepResult{StagingExpired: n, DerivativesCleared: cleared}, nil
}

func (s *Service) sweepFailedDerivatives(ctx context.Context, workspaceID uuid.UUID, limit int) (int, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	q, err := s.queries()
	if err != nil {
		return 0, err
	}
	rows, err := q.ListExpiredFailedArtifactDerivatives(ctx, db.ListExpiredFailedArtifactDerivativesParams{
		WorkspaceID: pgUUID(workspaceID),
		Lim:         int32(limit),
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, row := range rows {
		cleared, err := q.ClearExpiredArtifactDerivativeKey(ctx, db.ClearExpiredArtifactDerivativeKeyParams{
			ID:          row.ID,
			WorkspaceID: pgUUID(workspaceID),
		})
		if err != nil {
			return n, err
		}
		if cleared == 0 {
			continue
		}
		s.Store.Delete(ctx, row.StorageKey)
		n++
	}
	return n, nil
}

// OpsHealth reconciles reserved upload bytes with committed revision bytes.
// Drift is the quota sum minus those two parts. Retention strings are ops defaults.
func (s *Service) OpsHealth(ctx context.Context, workspaceID uuid.UUID) (OpsHealth, error) {
	if err := s.configured(); err != nil {
		return OpsHealth{}, err
	}
	if workspaceID == uuid.Nil {
		return OpsHealth{}, fmt.Errorf("%w: ops", ErrInvalidInput)
	}
	q, err := s.queries()
	if err != nil {
		return OpsHealth{}, err
	}
	reserved, err := q.SumReservedArtifactUploadBytes(ctx, pgUUID(workspaceID))
	if err != nil {
		return OpsHealth{}, err
	}
	committed, err := q.SumCommittedArtifactRevisionBytes(ctx, pgUUID(workspaceID))
	if err != nil {
		return OpsHealth{}, err
	}
	accounted, err := q.SumWorkspaceArtifactBytes(ctx, pgUUID(workspaceID))
	if err != nil {
		return OpsHealth{}, err
	}
	queue, err := q.CountArtifactDerivativeQueue(ctx, pgUUID(workspaceID))
	if err != nil {
		return OpsHealth{}, err
	}
	past, err := q.CountArtifactsPastTrash(ctx, db.CountArtifactsPastTrashParams{
		WorkspaceID: pgUUID(workspaceID),
		Cutoff:      pgTime(time.Now().Add(-s.retention().Trash)),
	})
	if err != nil {
		return OpsHealth{}, err
	}
	ret := s.retention()
	return OpsHealth{
		GCPaused:                  s.GCPaused,
		LastGCDeleted:             s.lastGCDeleted,
		ReservedBytes:             reserved,
		CommittedBytes:            committed,
		DriftBytes:                accounted - (reserved + committed),
		ConversionPending:         queue.Pending,
		ConversionFailed:          queue.Failed,
		TrashPastRetention:        past,
		RetentionClass:            "ops_default",
		TrashRetention:            ret.Trash.String(),
		StagingRetention:          ret.Staging.String(),
		ConflictDraftRetention:    ret.ConflictDraft.String(),
		FailedDerivativeRetention: ret.FailedDerivative.String(),
	}, nil
}
