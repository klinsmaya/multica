// Package artifact is the authority for product files: stable artifact ids,
// immutable revisions, and compare-and-swap updates of artifacts.head_revision_id.
//
// HTTP is not served from this package. Handlers behind artifacts_v1 call
// Create and Commit. Attachment upload is a separate path and is not called.
package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/storage"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TxBeginner is the subset of *pgxpool.Pool the service uses.
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Service commits artifact revisions. Store is the same storage.Storage
// implementation attachments use; this package only supplies the object key.
//
// Bytes land in a private staging key first. Finalize verifies the size and
// server-side sha256, sniffs the media type, and only then copies the object
// to the immutable blob key.
type Service struct {
	DB    TxBeginner
	Store storage.Storage
	// Scanner classifies staged bytes. Nil uses NopScanner, which returns
	// clean as an explicit no-op. A timeout must surface as ScanPending and
	// must not become ready.
	Scanner Scanner
	// WorkspaceQuotaBytes and ActorQuotaBytes cap open staging reservations
	// plus committed revision bytes. Zero selects the built-in defaults.
	// Settlement, refunds, and the sweeper schedule belong to A14.
	WorkspaceQuotaBytes int64
	ActorQuotaBytes     int64
	// EditLeaseTTL is how long a heartbeat extends a text-edit lease.
	// Zero selects DefaultEditLeaseTTL.
	EditLeaseTTL time.Duration
}

// CreateInput opens an artifact with no revision. Title is a display name.
type CreateInput struct {
	WorkspaceID uuid.UUID
	OwnerScope  string
	OwnerID     uuid.UUID
	Title       string
	Kind        string
	CreatedBy   uuid.UUID
}

// Artifact is the row returned to callers, with ids as UUIDs.
type Artifact struct {
	ID             uuid.UUID
	WorkspaceID    uuid.UUID
	OwnerScope     string
	OwnerID        uuid.UUID
	Title          string
	Kind           string
	HeadRevisionID *uuid.UUID
	RowVersion     int64
	Status         string
	CreatedBy      uuid.UUID
}

// CommitInput seals bytes as the next revision of an existing artifact.
// Filename is stored for display and Content-Disposition. It is not looked
// up and it is not unique.
type CommitInput struct {
	WorkspaceID    uuid.UUID
	ActorID        uuid.UUID
	ArtifactID     uuid.UUID
	BaseRevisionID *uuid.UUID
	IdempotencyKey string
	Filename       string
	MediaType      string
	SHA256         string
	Source         string
	TaskRunID      *uuid.UUID
	Data           []byte
	// EditLease is optional. It is not part of the request hash. When an
	// unexpired lease is open, a commit without that lease is rejected.
	EditLease *EditLease
}

// CommitResult is the sealed revision. Replayed is true when an earlier
// commit with the same key and request already won.
type CommitResult struct {
	ArtifactID             uuid.UUID `json:"artifact_id"`
	RevisionID             uuid.UUID `json:"revision_id"`
	RevisionNo             int64     `json:"revision_no"`
	BlobID                 uuid.UUID `json:"blob_id"`
	OperationID            uuid.UUID `json:"operation_id"`
	StorageKey             string    `json:"storage_key"`
	RowVersion             int64     `json:"row_version"`
	HeadRevisionID         uuid.UUID `json:"head_revision_id"`
	RestoredFromRevisionID uuid.UUID `json:"restored_from_revision_id,omitempty"`
	Replayed               bool      `json:"-"`
}

// Create inserts an artifact. It does not upload bytes or create a revision.
func (s *Service) Create(ctx context.Context, in CreateInput) (Artifact, error) {
	if s == nil || s.DB == nil {
		return Artifact{}, fmt.Errorf("%w: service is not configured", ErrInvalidInput)
	}
	in.Title = strings.TrimSpace(in.Title)
	if in.Kind == "" {
		in.Kind = "file"
	}
	if in.WorkspaceID == uuid.Nil || in.OwnerID == uuid.Nil || in.CreatedBy == uuid.Nil ||
		!validOwnerScope(in.OwnerScope) || in.Kind != "file" || !validTitle(in.Title) {
		return Artifact{}, fmt.Errorf("%w: create", ErrInvalidInput)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Artifact{}, fmt.Errorf("new artifact id: %w", err)
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Artifact{}, err
	}
	defer tx.Rollback(ctx)

	row, err := db.New(tx).CreateArtifact(ctx, db.CreateArtifactParams{
		ID:          pgUUID(id),
		WorkspaceID: pgUUID(in.WorkspaceID),
		OwnerScope:  in.OwnerScope,
		OwnerID:     pgUUID(in.OwnerID),
		Title:       in.Title,
		Kind:        in.Kind,
		CreatedBy:   pgUUID(in.CreatedBy),
	})
	if err != nil {
		return Artifact{}, err
	}
	if err := insertEvents(ctx, db.New(tx), in.WorkspaceID, id, nil, nil, EventCreated); err != nil {
		return Artifact{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Artifact{}, err
	}
	return artifactFromRow(row), nil
}

// Commit stages in.Data, verifies it, and seals a new immutable revision when
// head still equals BaseRevisionID. The same idempotency key and request hash
// returns the stored result and does not open a second upload.
func (s *Service) Commit(ctx context.Context, in CommitInput) (CommitResult, error) {
	if err := s.configured(); err != nil {
		return CommitResult{}, err
	}
	norm, hash, err := normalizeCommit(in)
	if err != nil {
		return CommitResult{}, err
	}
	sum := sha256.Sum256(norm.Data)
	if hex.EncodeToString(sum[:]) != norm.SHA256 {
		return CommitResult{}, ErrDigestMismatch
	}
	if result, done, err := s.peekCommit(ctx, norm, hash); done || err != nil {
		return result, err
	}

	up, err := s.CreateUpload(ctx, CreateUploadInput{
		WorkspaceID:    norm.WorkspaceID,
		ActorID:        norm.ActorID,
		ArtifactID:     norm.ArtifactID,
		BaseRevisionID: norm.BaseRevisionID,
		ExpectedSize:   int64(len(norm.Data)),
		ExpectedSHA256: norm.SHA256,
		Filename:       norm.Filename,
		MediaType:      norm.MediaType,
		EditLease:      norm.EditLease,
	})
	if err != nil {
		return CommitResult{}, err
	}
	if err := s.WriteStaging(ctx, norm.WorkspaceID, up.ID, norm.Data); err != nil {
		return CommitResult{}, err
	}
	if err := s.VerifyUpload(ctx, norm.WorkspaceID, up.ID); err != nil {
		return CommitResult{}, err
	}
	return s.finalize(ctx, FinalizeInput{
		WorkspaceID:    norm.WorkspaceID,
		ActorID:        norm.ActorID,
		UploadID:       up.ID,
		IdempotencyKey: norm.IdempotencyKey,
		Source:         norm.Source,
		TaskRunID:      norm.TaskRunID,
		EditLease:      norm.EditLease,
	}, hash)
}

// peekCommit reports an operation that already owns this key. done is false
// only when the key is unused. The hash is the A03 commit identity, which
// does not include the upload id, so a retry does not look like a new request.
func (s *Service) peekCommit(ctx context.Context, in CommitInput, hash string) (CommitResult, bool, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return CommitResult{}, false, err
	}
	defer tx.Rollback(ctx)

	op, err := db.New(tx).GetArtifactOperation(ctx, db.GetArtifactOperationParams{
		WorkspaceID:    pgUUID(in.WorkspaceID),
		ActorID:        pgUUID(in.ActorID),
		IdempotencyKey: in.IdempotencyKey,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return CommitResult{}, false, nil
	}
	if err != nil {
		return CommitResult{}, false, err
	}
	if op.RequestHash != hash {
		return CommitResult{}, true, ErrIdempotencyReused
	}
	switch op.State {
	case "succeeded":
		result, err := decodeCommitResult(op.ResultRef)
		if err != nil {
			return CommitResult{}, true, err
		}
		result.Replayed = true
		return result, true, nil
	case "failed":
		if op.ErrorCode.Valid && op.ErrorCode.String == CodeRevisionConflict {
			return CommitResult{}, true, ErrRevisionConflict
		}
		return CommitResult{}, true, fmt.Errorf("%w: operation failed", ErrInvalidInput)
	default:
		return CommitResult{}, true, ErrOperationPending
	}
}

func (s *Service) replayOperation(ctx context.Context, q *db.Queries, tx pgx.Tx, committed *bool, in CommitInput, hash string) (CommitResult, error) {
	op, err := q.LockArtifactOperation(ctx, db.LockArtifactOperationParams{
		WorkspaceID:    pgUUID(in.WorkspaceID),
		ActorID:        pgUUID(in.ActorID),
		IdempotencyKey: in.IdempotencyKey,
	})
	if err != nil {
		return CommitResult{}, err
	}
	if op.RequestHash != hash {
		return CommitResult{}, ErrIdempotencyReused
	}
	switch op.State {
	case "succeeded":
		result, err := decodeCommitResult(op.ResultRef)
		if err != nil {
			return CommitResult{}, err
		}
		result.Replayed = true
		if err := tx.Commit(ctx); err != nil {
			return CommitResult{}, err
		}
		*committed = true
		return result, nil
	case "failed":
		if err := tx.Commit(ctx); err != nil {
			return CommitResult{}, err
		}
		*committed = true
		if op.ErrorCode.Valid && op.ErrorCode.String == CodeRevisionConflict {
			return CommitResult{}, ErrRevisionConflict
		}
		return CommitResult{}, fmt.Errorf("%w: operation failed", ErrInvalidInput)
	default:
		return CommitResult{}, ErrOperationPending
	}
}

type commitIdentity struct {
	ArtifactID     string `json:"artifact_id"`
	BaseRevisionID string `json:"base_revision_id"`
	SHA256         string `json:"sha256"`
	Size           int64  `json:"size"`
	MediaType      string `json:"media_type"`
	Filename       string `json:"filename"`
	Source         string `json:"source"`
	TaskRunID      string `json:"task_run_id"`
}

func normalizeCommit(in CommitInput) (CommitInput, string, error) {
	in.Filename = strings.TrimSpace(in.Filename)
	in.MediaType = strings.TrimSpace(in.MediaType)
	in.SHA256 = strings.ToLower(strings.TrimSpace(in.SHA256))
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	in.Source = strings.TrimSpace(in.Source)
	if in.Data == nil {
		in.Data = []byte{}
	}
	if in.WorkspaceID == uuid.Nil || in.ActorID == uuid.Nil || in.ArtifactID == uuid.Nil ||
		!validFilename(in.Filename) || !validMediaType(in.MediaType) || !validSHA256(in.SHA256) ||
		!validIdempotencyKey(in.IdempotencyKey) || !validSource(in.Source) {
		return CommitInput{}, "", fmt.Errorf("%w: commit", ErrInvalidInput)
	}
	if in.BaseRevisionID != nil && *in.BaseRevisionID == uuid.Nil {
		return CommitInput{}, "", fmt.Errorf("%w: base revision", ErrInvalidInput)
	}
	if in.TaskRunID != nil && *in.TaskRunID == uuid.Nil {
		return CommitInput{}, "", fmt.Errorf("%w: task run", ErrInvalidInput)
	}
	identity := commitIdentity{
		ArtifactID: in.ArtifactID.String(),
		SHA256:     in.SHA256,
		Size:       int64(len(in.Data)),
		MediaType:  in.MediaType,
		Filename:   in.Filename,
		Source:     in.Source,
	}
	if in.BaseRevisionID != nil {
		identity.BaseRevisionID = in.BaseRevisionID.String()
	}
	if in.TaskRunID != nil {
		identity.TaskRunID = in.TaskRunID.String()
	}
	raw, err := json.Marshal(identity)
	if err != nil {
		return CommitInput{}, "", err
	}
	sum := sha256.Sum256(raw)
	return in, hex.EncodeToString(sum[:]), nil
}

func decodeCommitResult(raw []byte) (CommitResult, error) {
	var result CommitResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return CommitResult{}, fmt.Errorf("decode artifact operation result: %w", err)
	}
	if result.RevisionID == uuid.Nil || result.ArtifactID == uuid.Nil {
		return CommitResult{}, fmt.Errorf("%w: stored operation result", ErrInvalidInput)
	}
	return result, nil
}

func headMatches(head pgtype.UUID, base *uuid.UUID) bool {
	if !head.Valid {
		return base == nil
	}
	if base == nil {
		return false
	}
	return uuid.UUID(head.Bytes) == *base
}

func storageKey(workspaceID, blobID uuid.UUID) string {
	return "workspaces/" + workspaceID.String() + "/artifacts/" + blobID.String()
}

func keyContainsFilename(key, filename string) bool {
	for _, seg := range strings.Split(key, "/") {
		if seg == filename {
			return true
		}
	}
	return false
}

func artifactFromRow(row db.Artifact) Artifact {
	out := Artifact{
		ID:          uuid.UUID(row.ID.Bytes),
		WorkspaceID: uuid.UUID(row.WorkspaceID.Bytes),
		OwnerScope:  row.OwnerScope,
		OwnerID:     uuid.UUID(row.OwnerID.Bytes),
		Title:       row.Title,
		Kind:        row.Kind,
		RowVersion:  row.RowVersion,
		Status:      row.Status,
		CreatedBy:   uuid.UUID(row.CreatedBy.Bytes),
	}
	if row.HeadRevisionID.Valid {
		id := uuid.UUID(row.HeadRevisionID.Bytes)
		out.HeadRevisionID = &id
	}
	return out
}

func pgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte(id), Valid: id != uuid.Nil}
}

func pgUUIDPtr(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgUUID(*id)
}

func pgText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

func pgTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func validOwnerScope(scope string) bool {
	switch scope {
	case "workspace", "issue", "task":
		return true
	default:
		return false
	}
}

func validSource(source string) bool {
	switch source {
	case "agent", "human_edit", "restore", "import":
		return true
	default:
		return false
	}
}

func validTitle(title string) bool {
	return len(title) >= 1 && len(title) <= 512
}

func validFilename(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 1024 {
		return false
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return false
	}
	return true
}

func validMediaType(mt string) bool {
	if mt == "" || len(mt) > 255 {
		return false
	}
	for _, r := range mt {
		if unicode.IsControl(r) || r == ' ' {
			return false
		}
	}
	return true
}

func validSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

func validIdempotencyKey(key string) bool {
	return len(key) >= 1 && len(key) <= 200 && !strings.ContainsAny(key, "\r\n\x00")
}
