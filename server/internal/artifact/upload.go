package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	// DefaultWorkspaceArtifactQuotaBytes is the ceiling used when the service
	// has no workspace override. A14 replaces this constant with real quota
	// accounts.
	DefaultWorkspaceArtifactQuotaBytes int64 = 1 << 30
	// DefaultActorArtifactQuotaBytes is the per-actor ceiling inside one
	// workspace when the service has no override.
	DefaultActorArtifactQuotaBytes int64 = 256 << 20

	uploadLifetime = 24 * time.Hour
)

// CreateUploadInput binds an actor to one artifact, one base revision, and
// the bytes they intend to stage. Those fields cannot be changed later.
type CreateUploadInput struct {
	WorkspaceID    uuid.UUID
	ActorID        uuid.UUID
	ArtifactID     uuid.UUID
	BaseRevisionID *uuid.UUID
	ExpectedSize   int64
	ExpectedSHA256 string
	Filename       string
	MediaType      string
	EditLease      *EditLease
}

// Upload is the staging binding returned to callers.
type Upload struct {
	ID                uuid.UUID
	WorkspaceID       uuid.UUID
	ActorID           uuid.UUID
	ArtifactID        uuid.UUID
	BaseRevisionID    *uuid.UUID
	ExpectedSize      int64
	ExpectedSHA256    string
	StagingKey        string
	DeclaredMediaType string
	Filename          string
	State             string
	ExpiresAt         time.Time
}

// FinalizeInput seals a ready upload. IdempotencyKey follows the same
// uniqueness rule as Commit. A direct Finalize hashes the upload id; Commit
// passes the A03 request hash so a retry stays the same request.
type FinalizeInput struct {
	WorkspaceID    uuid.UUID
	ActorID        uuid.UUID
	UploadID       uuid.UUID
	IdempotencyKey string
	Source         string
	TaskRunID      *uuid.UUID
	EditLease      *EditLease
}

// CreateUpload reserves expected bytes and inserts a created upload.
// The reservation is the expected size of non-terminal uploads plus
// committed revision bytes, taken under a workspace advisory lock.
func (s *Service) CreateUpload(ctx context.Context, in CreateUploadInput) (Upload, error) {
	if err := s.configured(); err != nil {
		return Upload{}, err
	}
	in.Filename = trimSpace(in.Filename)
	in.MediaType = trimSpace(in.MediaType)
	in.ExpectedSHA256 = lowerHex(in.ExpectedSHA256)
	if in.WorkspaceID == uuid.Nil || in.ActorID == uuid.Nil || in.ArtifactID == uuid.Nil ||
		in.ExpectedSize < 0 || !validFilename(in.Filename) || !validMediaType(in.MediaType) ||
		!validSHA256(in.ExpectedSHA256) {
		return Upload{}, fmt.Errorf("%w: create upload", ErrInvalidInput)
	}
	if in.BaseRevisionID != nil && *in.BaseRevisionID == uuid.Nil {
		return Upload{}, fmt.Errorf("%w: base revision", ErrInvalidInput)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Upload{}, fmt.Errorf("new upload id: %w", err)
	}
	key := stagingStorageKey(in.WorkspaceID, id)
	if keyContainsFilename(key, in.Filename) {
		return Upload{}, fmt.Errorf("%w: staging key contains filename", ErrInvalidInput)
	}

	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Upload{}, err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	if err := q.LockWorkspaceArtifactQuota(ctx, pgUUID(in.WorkspaceID)); err != nil {
		return Upload{}, err
	}
	art, err := q.LockArtifact(ctx, db.LockArtifactParams{
		ID:          pgUUID(in.ArtifactID),
		WorkspaceID: pgUUID(in.WorkspaceID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Upload{}, ErrNotFound
	}
	if err != nil {
		return Upload{}, err
	}
	if art.DeletedAt.Valid || art.Status == "deleted" {
		return Upload{}, ErrNotFound
	}
	if err := enforceEditLease(ctx, q, in.ArtifactID, in.ActorID, in.EditLease); err != nil {
		return Upload{}, err
	}
	wsReserved, err := q.SumWorkspaceArtifactBytes(ctx, pgUUID(in.WorkspaceID))
	if err != nil {
		return Upload{}, err
	}
	actorReserved, err := q.SumActorArtifactBytes(ctx, db.SumActorArtifactBytesParams{
		WorkspaceID: pgUUID(in.WorkspaceID),
		ActorID:     pgUUID(in.ActorID),
	})
	if err != nil {
		return Upload{}, err
	}
	if exceeds(wsReserved, in.ExpectedSize, s.workspaceQuota()) ||
		exceeds(actorReserved, in.ExpectedSize, s.actorQuota()) {
		return Upload{}, ErrQuotaExceeded
	}
	row, err := q.InsertArtifactUpload(ctx, db.InsertArtifactUploadParams{
		ID:                pgUUID(id),
		WorkspaceID:       pgUUID(in.WorkspaceID),
		ActorID:           pgUUID(in.ActorID),
		ArtifactID:        pgUUID(in.ArtifactID),
		BaseRevisionID:    pgUUIDPtr(in.BaseRevisionID),
		ExpectedSizeBytes: in.ExpectedSize,
		ExpectedSha256:    in.ExpectedSHA256,
		StagingKey:        key,
		DeclaredMediaType: in.MediaType,
		Filename:          in.Filename,
		ExpiresAt:         pgTime(time.Now().Add(uploadLifetime)),
	})
	if err != nil {
		return Upload{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Upload{}, err
	}
	return uploadFromRow(row), nil
}

// WriteStaging stores the full object under the upload's private key and
// advances created → uploading → uploaded. A second call after uploaded
// returns nil and does not replace the bytes.
func (s *Service) WriteStaging(ctx context.Context, workspaceID, uploadID uuid.UUID, data []byte) error {
	if err := s.configured(); err != nil {
		return err
	}
	if data == nil {
		data = []byte{}
	}
	row, err := s.getUpload(ctx, workspaceID, uploadID)
	if err != nil {
		return err
	}
	if err := s.expireIfDue(ctx, row); err != nil {
		return err
	}
	switch row.State {
	case "uploaded", "verifying", "scanning", "ready":
		return nil
	case "created", "uploading":
	default:
		return uploadStateError(row.State)
	}
	if int64(len(data)) != row.ExpectedSizeBytes {
		return s.rejectUpload(ctx, row, row.State, ErrDigestMismatch)
	}
	if row.State == "created" {
		if err := s.forceState(ctx, workspaceID, uploadID, "created", "uploading"); err != nil {
			return err
		}
	}
	if _, err := s.Store.Upload(ctx, row.StagingKey, data, row.DeclaredMediaType, row.Filename); err != nil {
		return fmt.Errorf("stage artifact upload: %w", err)
	}
	return s.forceState(ctx, workspaceID, uploadID, "uploading", "uploaded")
}

// VerifyUpload checks the staged size, server sha256, and sniffed media
// type, then asks the scanner. Ready is idempotent. Pending leaves the
// upload in scanning.
func (s *Service) VerifyUpload(ctx context.Context, workspaceID, uploadID uuid.UUID) error {
	if err := s.configured(); err != nil {
		return err
	}
	row, err := s.getUpload(ctx, workspaceID, uploadID)
	if err != nil {
		return err
	}
	if err := s.expireIfDue(ctx, row); err != nil {
		return err
	}
	switch row.State {
	case "ready":
		return nil
	case "uploaded", "verifying", "scanning":
	default:
		return uploadStateError(row.State)
	}
	if row.State == "uploaded" {
		if err := s.forceState(ctx, workspaceID, uploadID, "uploaded", "verifying"); err != nil {
			return err
		}
		row.State = "verifying"
	}
	if row.State != "scanning" {
		data, err := readObjectLimited(ctx, s.Store, row.StagingKey, row.ExpectedSizeBytes)
		if err != nil {
			return fmt.Errorf("read staged artifact: %w", err)
		}
		if problem := digestProblem(data, row.ExpectedSizeBytes, row.ExpectedSha256); problem != nil {
			return s.rejectUpload(ctx, row, row.State, problem)
		}
		if _, ok := resolveMediaType(data, row.DeclaredMediaType); !ok {
			return s.rejectUpload(ctx, row, row.State, ErrMediaTypeMismatch)
		}
		if err := s.forceState(ctx, workspaceID, uploadID, row.State, "scanning"); err != nil {
			return err
		}
		row.State = "scanning"
	}
	verdict, err := s.scanner().Scan(ctx, ScanObject{
		Key:       row.StagingKey,
		Size:      row.ExpectedSizeBytes,
		SHA256:    row.ExpectedSha256,
		MediaType: row.DeclaredMediaType,
	})
	if err != nil || (verdict != ScanClean && verdict != ScanRejected) {
		return ErrScanPending
	}
	if verdict == ScanRejected {
		return s.rejectUpload(ctx, row, "scanning", ErrUploadRejected)
	}
	return s.forceState(ctx, workspaceID, uploadID, "scanning", "ready")
}

// Finalize seals a ready upload into an immutable revision.
func (s *Service) Finalize(ctx context.Context, in FinalizeInput) (CommitResult, error) {
	hash, err := directFinalizeHash(in)
	if err != nil {
		return CommitResult{}, err
	}
	return s.finalize(ctx, in, hash)
}

// CancelUpload moves a non-committed upload to cancelled and deletes staging.
func (s *Service) CancelUpload(ctx context.Context, workspaceID, uploadID uuid.UUID) error {
	if err := s.configured(); err != nil {
		return err
	}
	row, err := s.getUpload(ctx, workspaceID, uploadID)
	if err != nil {
		return err
	}
	switch row.State {
	case "cancelled":
		s.Store.Delete(ctx, row.StagingKey)
		return nil
	case "created", "uploading", "uploaded", "verifying", "scanning", "ready", "conflict_draft":
		if err := s.forceState(ctx, workspaceID, uploadID, row.State, "cancelled"); err != nil {
			return err
		}
		s.Store.Delete(ctx, row.StagingKey)
		return nil
	default:
		return uploadStateError(row.State)
	}
}

// ExpireUploads marks this workspace's overdue open uploads expired and
// deletes their staging objects. It is not scheduled; A14 owns the sweep.
func (s *Service) ExpireUploads(ctx context.Context, workspaceID uuid.UUID, limit int) (int, error) {
	if err := s.configured(); err != nil {
		return 0, err
	}
	if workspaceID == uuid.Nil {
		return 0, fmt.Errorf("%w: workspace", ErrInvalidInput)
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return 0, err
	}
	rows, err := db.New(tx).ListExpiredArtifactUploads(ctx, db.ListExpiredArtifactUploadsParams{
		WorkspaceID: pgUUID(workspaceID),
		Lim:         int32(limit),
	})
	_ = tx.Rollback(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, row := range rows {
		err := s.expireIfDue(ctx, row)
		if errors.Is(err, ErrUploadExpired) {
			n++
			continue
		}
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func (s *Service) finalize(ctx context.Context, in FinalizeInput, requestHash string) (CommitResult, error) {
	if err := s.configured(); err != nil {
		return CommitResult{}, err
	}
	in, err := normalizeFinalize(in)
	if err != nil {
		return CommitResult{}, err
	}
	if !validSHA256(requestHash) {
		return CommitResult{}, fmt.Errorf("%w: request hash", ErrInvalidInput)
	}

	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return CommitResult{}, err
	}
	committed := false
	sealedKey := ""
	rejectAfter := false
	stagingKey := ""
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
			if sealedKey != "" {
				s.Store.Delete(ctx, sealedKey)
			}
		}
		if rejectAfter {
			row, err := s.getUpload(ctx, in.WorkspaceID, in.UploadID)
			if err == nil && row.State == "ready" {
				_ = s.forceState(ctx, in.WorkspaceID, in.UploadID, "ready", "rejected")
				s.Store.Delete(ctx, row.StagingKey)
			}
		}
	}()

	q := db.New(tx)
	up, err := q.LockArtifactUpload(ctx, db.LockArtifactUploadParams{
		ID:          pgUUID(in.UploadID),
		WorkspaceID: pgUUID(in.WorkspaceID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return CommitResult{}, ErrNotFound
	}
	if err != nil {
		return CommitResult{}, err
	}
	if uuid.UUID(up.ActorID.Bytes) != in.ActorID {
		return CommitResult{}, ErrNotFound
	}
	stagingKey = up.StagingKey

	if openUpload(up.State) && uploadExpired(up, time.Now()) {
		if _, err := q.TransitionArtifactUpload(ctx, db.TransitionArtifactUploadParams{
			ToState:     "expired",
			ID:          up.ID,
			WorkspaceID: up.WorkspaceID,
			FromState:   up.State,
		}); err != nil {
			return CommitResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return CommitResult{}, err
		}
		committed = true
		s.Store.Delete(ctx, stagingKey)
		return CommitResult{}, ErrUploadExpired
	}

	switch up.State {
	case "committed":
		return s.replayConsumed(ctx, q, tx, &committed, in, requestHash, stagingKey)
	case "conflict_draft":
		return s.recordConflict(ctx, q, tx, &committed, in, requestHash)
	case "ready":
	default:
		return CommitResult{}, uploadStateError(up.State)
	}

	art, err := q.LockArtifact(ctx, db.LockArtifactParams{
		ID:          up.ArtifactID,
		WorkspaceID: up.WorkspaceID,
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
	if err := enforceEditLease(ctx, q, uuid.UUID(art.ID.Bytes), in.ActorID, in.EditLease); err != nil {
		return s.rejectLease(ctx, q, tx, &committed, up, stagingKey, err)
	}

	opID, err := uuid.NewV7()
	if err != nil {
		return CommitResult{}, fmt.Errorf("new operation id: %w", err)
	}
	op, err := q.InsertArtifactOperation(ctx, db.InsertArtifactOperationParams{
		ID:             pgUUID(opID),
		WorkspaceID:    up.WorkspaceID,
		ActorID:        up.ActorID,
		IdempotencyKey: in.IdempotencyKey,
		RequestHash:    requestHash,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return s.replayOperation(ctx, q, tx, &committed, commitInputFromFinalize(in, up), requestHash)
	}
	if err != nil {
		return CommitResult{}, err
	}

	base := uuidPtrFromPG(up.BaseRevisionID)
	data, err := readObjectLimited(ctx, s.Store, up.StagingKey, up.ExpectedSizeBytes)
	if err != nil {
		return CommitResult{}, fmt.Errorf("read staged artifact: %w", err)
	}
	if problem := digestProblem(data, up.ExpectedSizeBytes, up.ExpectedSha256); problem != nil {
		rejectAfter = true
		return CommitResult{}, problem
	}
	detected, ok := resolveMediaType(data, up.DeclaredMediaType)
	if !ok {
		rejectAfter = true
		return CommitResult{}, ErrMediaTypeMismatch
	}
	sealBytes := data
	sealSHA := up.ExpectedSha256
	sealSize := up.ExpectedSizeBytes
	parent := base
	casBase := base
	media := detected
	if !headMatches(art.HeadRevisionID, base) {
		merged, mergedOK := s.mergeStaleText(ctx, q, art, up, data)
		if !mergedOK {
			if _, err := q.RetainArtifactUploadConflict(ctx, db.RetainArtifactUploadConflictParams{
				ID:          up.ID,
				WorkspaceID: up.WorkspaceID,
			}); err != nil {
				return CommitResult{}, err
			}
			if _, err := q.FinishArtifactOperation(ctx, db.FinishArtifactOperationParams{
				State:     "failed",
				ErrorCode: pgText(CodeRevisionConflict),
				ID:        op.ID,
			}); err != nil {
				return CommitResult{}, err
			}
			if err := releaseEditLease(ctx, q, in.EditLease); err != nil {
				return CommitResult{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return CommitResult{}, err
			}
			committed = true
			return CommitResult{}, ErrRevisionConflict
		}
		sum := sha256.Sum256(merged)
		sealBytes = merged
		sealSHA = hex.EncodeToString(sum[:])
		sealSize = int64(len(merged))
		headID := uuid.UUID(art.HeadRevisionID.Bytes)
		parent = &headID
		casBase = &headID
		if next, ok := resolveMediaType(merged, up.DeclaredMediaType); ok {
			media = next
		}
	}

	blobID, err := uuid.NewV7()
	if err != nil {
		return CommitResult{}, fmt.Errorf("new blob id: %w", err)
	}
	revID, err := uuid.NewV7()
	if err != nil {
		return CommitResult{}, fmt.Errorf("new revision id: %w", err)
	}
	key := storageKey(uuid.UUID(up.WorkspaceID.Bytes), blobID)
	if keyContainsFilename(key, up.Filename) {
		return CommitResult{}, fmt.Errorf("%w: storage key contains filename", ErrInvalidInput)
	}
	if _, err := s.Store.Upload(ctx, key, sealBytes, media, up.Filename); err != nil {
		return CommitResult{}, fmt.Errorf("seal artifact blob: %w", err)
	}
	sealedKey = key
	sealed, err := readObjectLimited(ctx, s.Store, key, sealSize)
	if err != nil {
		return CommitResult{}, fmt.Errorf("read sealed artifact: %w", err)
	}
	if problem := digestProblem(sealed, sealSize, sealSHA); problem != nil {
		return CommitResult{}, problem
	}

	if _, err := q.InsertArtifactBlob(ctx, db.InsertArtifactBlobParams{
		ID:                pgUUID(blobID),
		StorageKey:        key,
		SizeBytes:         sealSize,
		Sha256:            sealSHA,
		DetectedMediaType: media,
		ScanStatus:        "clean",
	}); err != nil {
		return CommitResult{}, err
	}
	revNo, err := q.NextArtifactRevisionNo(ctx, up.ArtifactID)
	if err != nil {
		return CommitResult{}, err
	}
	if _, err := q.InsertArtifactRevision(ctx, db.InsertArtifactRevisionParams{
		ID:                pgUUID(revID),
		ArtifactID:        up.ArtifactID,
		RevisionNo:        revNo,
		ParentRevisionID:  pgUUIDPtr(parent),
		BaseRevisionID:    pgUUIDPtr(base),
		BlobID:            pgUUID(blobID),
		SourceUploadID:    up.ID,
		CommitOperationID: op.ID,
		MediaType:         media,
		SizeBytes:         sealSize,
		Sha256:            sealSHA,
		Filename:          up.Filename,
		Source:            in.Source,
		ActorID:           up.ActorID,
		TaskRunID:         pgUUIDPtr(in.TaskRunID),
	}); err != nil {
		return CommitResult{}, err
	}
	rowVersion, err := q.CasArtifactHead(ctx, db.CasArtifactHeadParams{
		HeadRevisionID: pgUUID(revID),
		ID:             up.ArtifactID,
		WorkspaceID:    up.WorkspaceID,
		BaseRevisionID: pgUUIDPtr(casBase),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		s.Store.Delete(ctx, sealedKey)
		sealedKey = ""
		if _, err := q.RetainArtifactUploadConflict(ctx, db.RetainArtifactUploadConflictParams{
			ID:          up.ID,
			WorkspaceID: up.WorkspaceID,
		}); err != nil {
			return CommitResult{}, err
		}
		if _, err := q.FinishArtifactOperation(ctx, db.FinishArtifactOperationParams{
			State:     "failed",
			ErrorCode: pgText(CodeRevisionConflict),
			ID:        op.ID,
		}); err != nil {
			return CommitResult{}, err
		}
		if err := releaseEditLease(ctx, q, in.EditLease); err != nil {
			return CommitResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return CommitResult{}, err
		}
		committed = true
		return CommitResult{}, ErrRevisionConflict
	}
	if err != nil {
		return CommitResult{}, err
	}
	if err := finishEditLease(ctx, q, uuid.UUID(art.ID.Bytes), in.ActorID, in.EditLease); err != nil {
		return CommitResult{}, err
	}
	if _, err := q.ConsumeArtifactUpload(ctx, db.ConsumeArtifactUploadParams{
		CommittedRevisionID: pgUUID(revID),
		ID:                  up.ID,
		WorkspaceID:         up.WorkspaceID,
	}); err != nil {
		return CommitResult{}, err
	}

	result := CommitResult{
		ArtifactID:     uuid.UUID(up.ArtifactID.Bytes),
		RevisionID:     revID,
		RevisionNo:     revNo,
		BlobID:         blobID,
		OperationID:    uuid.UUID(op.ID.Bytes),
		StorageKey:     key,
		RowVersion:     rowVersion,
		HeadRevisionID: revID,
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
	artUUID := uuid.UUID(up.ArtifactID.Bytes)
	if err := insertEvents(ctx, q, uuid.UUID(up.WorkspaceID.Bytes), artUUID, &revID, &opUUID, EventRevisionCommitted, EventPreviewReady); err != nil {
		return CommitResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CommitResult{}, err
	}
	committed = true
	sealedKey = ""
	// The staging key must not remain a writable object after the seal.
	s.Store.Delete(ctx, stagingKey)
	return result, nil
}

func (s *Service) replayConsumed(ctx context.Context, q *db.Queries, tx pgx.Tx, committed *bool, in FinalizeInput, hash, stagingKey string) (CommitResult, error) {
	op, err := q.LockArtifactOperation(ctx, db.LockArtifactOperationParams{
		WorkspaceID:    pgUUID(in.WorkspaceID),
		ActorID:        pgUUID(in.ActorID),
		IdempotencyKey: in.IdempotencyKey,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return CommitResult{}, ErrUploadConsumed
	}
	if err != nil {
		return CommitResult{}, err
	}
	if op.RequestHash != hash {
		return CommitResult{}, ErrIdempotencyReused
	}
	if op.State != "succeeded" {
		if op.State == "pending" {
			return CommitResult{}, ErrOperationPending
		}
		return CommitResult{}, ErrUploadConsumed
	}
	result, err := decodeCommitResult(op.ResultRef)
	if err != nil {
		return CommitResult{}, err
	}
	result.Replayed = true
	if err := tx.Commit(ctx); err != nil {
		return CommitResult{}, err
	}
	*committed = true
	s.Store.Delete(ctx, stagingKey)
	return result, nil
}

func (s *Service) recordConflict(ctx context.Context, q *db.Queries, tx pgx.Tx, committed *bool, in FinalizeInput, hash string) (CommitResult, error) {
	opID, err := uuid.NewV7()
	if err != nil {
		return CommitResult{}, fmt.Errorf("new operation id: %w", err)
	}
	op, err := q.InsertArtifactOperation(ctx, db.InsertArtifactOperationParams{
		ID:             pgUUID(opID),
		WorkspaceID:    pgUUID(in.WorkspaceID),
		ActorID:        pgUUID(in.ActorID),
		IdempotencyKey: in.IdempotencyKey,
		RequestHash:    hash,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return s.replayOperation(ctx, q, tx, committed, CommitInput{
			WorkspaceID:    in.WorkspaceID,
			ActorID:        in.ActorID,
			IdempotencyKey: in.IdempotencyKey,
		}, hash)
	}
	if err != nil {
		return CommitResult{}, err
	}
	if _, err := q.FinishArtifactOperation(ctx, db.FinishArtifactOperationParams{
		State:     "failed",
		ErrorCode: pgText(CodeRevisionConflict),
		ID:        op.ID,
	}); err != nil {
		return CommitResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CommitResult{}, err
	}
	*committed = true
	return CommitResult{}, ErrRevisionConflict
}

func (s *Service) getUpload(ctx context.Context, workspaceID, uploadID uuid.UUID) (db.ArtifactUpload, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return db.ArtifactUpload{}, err
	}
	defer tx.Rollback(ctx)
	row, err := db.New(tx).LockArtifactUpload(ctx, db.LockArtifactUploadParams{
		ID:          pgUUID(uploadID),
		WorkspaceID: pgUUID(workspaceID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.ArtifactUpload{}, ErrNotFound
	}
	if err != nil {
		return db.ArtifactUpload{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.ArtifactUpload{}, err
	}
	return row, nil
}

func (s *Service) forceState(ctx context.Context, workspaceID, uploadID uuid.UUID, from, to string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	_, err = q.TransitionArtifactUpload(ctx, db.TransitionArtifactUploadParams{
		ToState:     to,
		ID:          pgUUID(uploadID),
		WorkspaceID: pgUUID(workspaceID),
		FromState:   from,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		current, err2 := q.LockArtifactUpload(ctx, db.LockArtifactUploadParams{
			ID:          pgUUID(uploadID),
			WorkspaceID: pgUUID(workspaceID),
		})
		if errors.Is(err2, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err2 != nil {
			return err2
		}
		if current.State == to {
			return tx.Commit(ctx)
		}
		return uploadStateError(current.State)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) rejectUpload(ctx context.Context, row db.ArtifactUpload, from string, cause error) error {
	ws := uuid.UUID(row.WorkspaceID.Bytes)
	id := uuid.UUID(row.ID.Bytes)
	if err := s.forceState(ctx, ws, id, from, "rejected"); err != nil && !errors.Is(err, ErrUploadRejected) {
		return err
	}
	s.Store.Delete(ctx, row.StagingKey)
	return cause
}

func (s *Service) expireIfDue(ctx context.Context, row db.ArtifactUpload) error {
	if !openUpload(row.State) || !uploadExpired(row, time.Now()) {
		return nil
	}
	ws := uuid.UUID(row.WorkspaceID.Bytes)
	id := uuid.UUID(row.ID.Bytes)
	if err := s.forceState(ctx, ws, id, row.State, "expired"); err != nil && !errors.Is(err, ErrUploadExpired) {
		return err
	}
	s.Store.Delete(ctx, row.StagingKey)
	return ErrUploadExpired
}

func (s *Service) configured() error {
	if s == nil || s.DB == nil || s.Store == nil {
		return fmt.Errorf("%w: service is not configured", ErrInvalidInput)
	}
	return nil
}

func (s *Service) scanner() Scanner {
	if s == nil || s.Scanner == nil {
		return NopScanner{}
	}
	return s.Scanner
}

func (s *Service) workspaceQuota() int64 {
	if s != nil && s.WorkspaceQuotaBytes > 0 {
		return s.WorkspaceQuotaBytes
	}
	return DefaultWorkspaceArtifactQuotaBytes
}

func (s *Service) actorQuota() int64 {
	if s != nil && s.ActorQuotaBytes > 0 {
		return s.ActorQuotaBytes
	}
	return DefaultActorArtifactQuotaBytes
}

func uploadFromRow(row db.ArtifactUpload) Upload {
	out := Upload{
		ID:                uuid.UUID(row.ID.Bytes),
		WorkspaceID:       uuid.UUID(row.WorkspaceID.Bytes),
		ActorID:           uuid.UUID(row.ActorID.Bytes),
		ArtifactID:        uuid.UUID(row.ArtifactID.Bytes),
		ExpectedSize:      row.ExpectedSizeBytes,
		ExpectedSHA256:    row.ExpectedSha256,
		StagingKey:        row.StagingKey,
		DeclaredMediaType: row.DeclaredMediaType,
		Filename:          row.Filename,
		State:             row.State,
	}
	if row.BaseRevisionID.Valid {
		id := uuid.UUID(row.BaseRevisionID.Bytes)
		out.BaseRevisionID = &id
	}
	if row.ExpiresAt.Valid {
		out.ExpiresAt = row.ExpiresAt.Time
	}
	return out
}

func normalizeFinalize(in FinalizeInput) (FinalizeInput, error) {
	in.IdempotencyKey = trimSpace(in.IdempotencyKey)
	in.Source = trimSpace(in.Source)
	if in.WorkspaceID == uuid.Nil || in.ActorID == uuid.Nil || in.UploadID == uuid.Nil ||
		!validIdempotencyKey(in.IdempotencyKey) || !validSource(in.Source) {
		return FinalizeInput{}, fmt.Errorf("%w: finalize", ErrInvalidInput)
	}
	if in.TaskRunID != nil && *in.TaskRunID == uuid.Nil {
		return FinalizeInput{}, fmt.Errorf("%w: task run", ErrInvalidInput)
	}
	return in, nil
}

func directFinalizeHash(in FinalizeInput) (string, error) {
	norm, err := normalizeFinalize(in)
	if err != nil {
		return "", err
	}
	identity := struct {
		UploadID  string `json:"upload_id"`
		Source    string `json:"source"`
		TaskRunID string `json:"task_run_id"`
	}{
		UploadID: norm.UploadID.String(),
		Source:   norm.Source,
	}
	if norm.TaskRunID != nil {
		identity.TaskRunID = norm.TaskRunID.String()
	}
	raw, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func commitInputFromFinalize(in FinalizeInput, up db.ArtifactUpload) CommitInput {
	return CommitInput{
		WorkspaceID:    in.WorkspaceID,
		ActorID:        in.ActorID,
		ArtifactID:     uuid.UUID(up.ArtifactID.Bytes),
		IdempotencyKey: in.IdempotencyKey,
	}
}

func stagingStorageKey(workspaceID, uploadID uuid.UUID) string {
	return "workspaces/" + workspaceID.String() + "/artifact-staging/" + uploadID.String()
}

func digestProblem(data []byte, size int64, sum string) error {
	if int64(len(data)) != size {
		return ErrDigestMismatch
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != sum {
		return ErrDigestMismatch
	}
	return nil
}

func readObjectLimited(ctx context.Context, store interface {
	GetReader(context.Context, string) (io.ReadCloser, error)
}, key string, limit int64) ([]byte, error) {
	rc, err := store.GetReader(ctx, key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	if limit < 0 {
		limit = 0
	}
	return io.ReadAll(io.LimitReader(rc, limit+1))
}

func uploadExpired(row db.ArtifactUpload, now time.Time) bool {
	return row.ExpiresAt.Valid && !row.ExpiresAt.Time.After(now)
}

func openUpload(state string) bool {
	switch state {
	case "created", "uploading", "uploaded", "verifying", "scanning", "ready", "conflict_draft":
		return true
	default:
		return false
	}
}

func uploadStateError(state string) error {
	switch state {
	case "rejected":
		return ErrUploadRejected
	case "expired":
		return ErrUploadExpired
	case "cancelled":
		return ErrUploadCancelled
	case "committed":
		return ErrUploadConsumed
	case "scanning":
		return ErrScanPending
	case "conflict_draft":
		return ErrRevisionConflict
	default:
		return ErrUploadNotReady
	}
}

func exceeds(reserved, add, quota int64) bool {
	if add < 0 || quota < 0 || reserved < 0 || reserved > quota {
		return true
	}
	return add > quota-reserved
}

func uuidPtrFromPG(id pgtype.UUID) *uuid.UUID {
	if !id.Valid {
		return nil
	}
	out := uuid.UUID(id.Bytes)
	return &out
}

func trimSpace(s string) string {
	return strings.TrimSpace(s)
}

func lowerHex(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
