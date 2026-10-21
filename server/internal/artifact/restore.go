package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// RestoreInput copies a historical revision onto a new head. The source row
// stays where it is. BaseRevisionID is the head the caller still expects.
type RestoreInput struct {
	WorkspaceID    uuid.UUID
	ActorID        uuid.UUID
	ArtifactID     uuid.UUID
	RevisionID     uuid.UUID
	BaseRevisionID uuid.UUID
	EditLease      *EditLease
}

// Restore inserts one revision with source restore. CAS refuses a stale head.
func (s *Service) Restore(ctx context.Context, in RestoreInput) (CommitResult, error) {
	if err := s.configured(); err != nil {
		return CommitResult{}, err
	}
	if in.WorkspaceID == uuid.Nil || in.ActorID == uuid.Nil || in.ArtifactID == uuid.Nil ||
		in.RevisionID == uuid.Nil || in.BaseRevisionID == uuid.Nil {
		return CommitResult{}, fmt.Errorf("%w: restore", ErrInvalidInput)
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return CommitResult{}, err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)

	art, err := q.LockArtifact(ctx, db.LockArtifactParams{
		ID:          pgUUID(in.ArtifactID),
		WorkspaceID: pgUUID(in.WorkspaceID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return CommitResult{}, ErrNotFound
	}
	if err != nil {
		return CommitResult{}, err
	}
	if art.DeletedAt.Valid || art.Status == "deleted" {
		return CommitResult{}, ErrNotFound
	}
	if err := enforceEditLease(ctx, q, in.ArtifactID, in.ActorID, in.EditLease); err != nil {
		return CommitResult{}, err
	}
	source, err := q.GetArtifactRevisionRecord(ctx, db.GetArtifactRevisionRecordParams{
		ID:         pgUUID(in.RevisionID),
		ArtifactID: art.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return CommitResult{}, ErrNotFound
	}
	if err != nil {
		return CommitResult{}, err
	}
	base := in.BaseRevisionID
	if !headMatches(art.HeadRevisionID, &base) {
		return CommitResult{}, ErrRevisionConflict
	}

	opID, err := uuid.NewV7()
	if err != nil {
		return CommitResult{}, fmt.Errorf("new restore operation id: %w", err)
	}
	revID, err := uuid.NewV7()
	if err != nil {
		return CommitResult{}, fmt.Errorf("new restore revision id: %w", err)
	}
	sum := sha256.Sum256([]byte("restore\n" + in.ArtifactID.String() + "\n" + in.RevisionID.String() + "\n" + in.BaseRevisionID.String() + "\n" + revID.String()))
	key := "restore:" + revID.String()
	op, err := q.InsertArtifactOperation(ctx, db.InsertArtifactOperationParams{
		ID:             pgUUID(opID),
		WorkspaceID:    art.WorkspaceID,
		ActorID:        pgUUID(in.ActorID),
		IdempotencyKey: key,
		RequestHash:    hex.EncodeToString(sum[:]),
	})
	if err != nil {
		return CommitResult{}, err
	}
	revNo, err := q.NextArtifactRevisionNo(ctx, art.ID)
	if err != nil {
		return CommitResult{}, err
	}
	head := in.BaseRevisionID
	if _, err := q.InsertArtifactRevision(ctx, db.InsertArtifactRevisionParams{
		ID:                     pgUUID(revID),
		ArtifactID:             art.ID,
		RevisionNo:             revNo,
		ParentRevisionID:       pgUUIDPtr(&head),
		BaseRevisionID:         pgUUIDPtr(&base),
		BlobID:                 source.BlobID,
		CommitOperationID:      op.ID,
		MediaType:              source.MediaType,
		SizeBytes:              source.SizeBytes,
		Sha256:                 source.Sha256,
		Filename:               source.Filename,
		Source:                 "restore",
		ActorID:                pgUUID(in.ActorID),
		RestoredFromRevisionID: pgUUID(in.RevisionID),
	}); err != nil {
		return CommitResult{}, err
	}
	rowVersion, err := q.CasArtifactHead(ctx, db.CasArtifactHeadParams{
		HeadRevisionID: pgUUID(revID),
		ID:             art.ID,
		WorkspaceID:    art.WorkspaceID,
		BaseRevisionID: pgUUIDPtr(&base),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return CommitResult{}, ErrRevisionConflict
	}
	if err != nil {
		return CommitResult{}, err
	}
	if err := finishEditLease(ctx, q, in.ArtifactID, in.ActorID, in.EditLease); err != nil {
		return CommitResult{}, err
	}
	result := CommitResult{
		ArtifactID:             in.ArtifactID,
		RevisionID:             revID,
		RevisionNo:             revNo,
		BlobID:                 uuid.UUID(source.BlobID.Bytes),
		OperationID:            uuid.UUID(op.ID.Bytes),
		RowVersion:             rowVersion,
		HeadRevisionID:         revID,
		RestoredFromRevisionID: in.RevisionID,
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return CommitResult{}, err
	}
	if _, err := q.FinishArtifactOperation(ctx, db.FinishArtifactOperationParams{
		State:     "succeeded",
		ResultRef: encoded,
		ID:        op.ID,
	}); err != nil {
		return CommitResult{}, err
	}
	opUUID := uuid.UUID(op.ID.Bytes)
	if err := insertEvents(ctx, q, in.WorkspaceID, in.ArtifactID, &revID, &opUUID, EventRevisionCommitted, EventPreviewReady); err != nil {
		return CommitResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CommitResult{}, err
	}
	return result, nil
}
