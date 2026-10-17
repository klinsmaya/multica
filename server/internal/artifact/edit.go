package artifact

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	// DefaultEditLeaseTTL is how long a heartbeat keeps the editor's lease.
	DefaultEditLeaseTTL = 2 * time.Minute
	// MaxEditDraftBytes is the text draft cap. A draft is not a revision.
	MaxEditDraftBytes = 1 << 20
)

// EditLease is the proof a holder sends with a draft save or a formal commit.
// It is not part of the commit request hash.
type EditLease struct {
	SessionID    uuid.UUID
	ActorID      uuid.UUID
	FencingToken int64
	TokenHash    string
}

// OpenEditSessionInput starts a lease at the artifact's current head.
type OpenEditSessionInput struct {
	WorkspaceID uuid.UUID
	ArtifactID  uuid.UUID
	ActorID     uuid.UUID
	EditorType  string
}

// EditSession is the lease the editor holds. LeaseToken is set only when the
// session is created. Draft is the previous draft for this actor, if one survived.
type EditSession struct {
	ID             uuid.UUID
	ArtifactID     uuid.UUID
	BaseRevisionID uuid.UUID
	ActorID        uuid.UUID
	EditorType     string
	FencingToken   int64
	LeaseExpiresAt time.Time
	LeaseToken     string
	State          string
	Draft          []byte
}

func (s *Service) editLeaseTTL() time.Duration {
	if s != nil && s.EditLeaseTTL > 0 {
		return s.EditLeaseTTL
	}
	return DefaultEditLeaseTTL
}

// OpenEditSession takes the single open lease. An elapsed lease is marked
// expired and its draft is left in place. The new session re-reads head.
func (s *Service) OpenEditSession(ctx context.Context, in OpenEditSessionInput) (EditSession, error) {
	if err := s.configured(); err != nil {
		return EditSession{}, err
	}
	if in.WorkspaceID == uuid.Nil || in.ArtifactID == uuid.Nil || in.ActorID == uuid.Nil ||
		(in.EditorType != "human" && in.EditorType != "agent") {
		return EditSession{}, fmt.Errorf("%w: edit session", ErrInvalidInput)
	}
	token, hash, err := newLeaseToken()
	if err != nil {
		return EditSession{}, err
	}
	sessionID, err := uuid.NewV7()
	if err != nil {
		return EditSession{}, fmt.Errorf("new edit session id: %w", err)
	}

	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return EditSession{}, err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	art, err := q.LockArtifact(ctx, db.LockArtifactParams{
		ID:          pgUUID(in.ArtifactID),
		WorkspaceID: pgUUID(in.WorkspaceID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return EditSession{}, ErrNotFound
	}
	if err != nil {
		return EditSession{}, err
	}
	if art.DeletedAt.Valid || art.Status == "deleted" || !art.HeadRevisionID.Valid {
		return EditSession{}, ErrNotFound
	}
	if _, err := q.ExpireElapsedArtifactEditSessions(ctx, art.ID); err != nil {
		return EditSession{}, err
	}
	if _, err := q.LockOpenArtifactEditSession(ctx, art.ID); err == nil {
		return EditSession{}, ErrLeaseHeld
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return EditSession{}, err
	}
	fencing, err := q.NextArtifactFencingToken(ctx, art.ID)
	if err != nil {
		return EditSession{}, err
	}
	row, err := q.InsertArtifactEditSession(ctx, db.InsertArtifactEditSessionParams{
		ID:             pgUUID(sessionID),
		ArtifactID:     art.ID,
		BaseRevisionID: art.HeadRevisionID,
		ActorID:        pgUUID(in.ActorID),
		EditorType:     in.EditorType,
		LeaseTokenHash: hash,
		FencingToken:   fencing,
		TtlSecs:        int64(s.editLeaseTTL() / time.Second),
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return EditSession{}, ErrLeaseHeld
		}
		return EditSession{}, err
	}
	inherited := ""
	if ref, err := q.LatestActorEditDraft(ctx, db.LatestActorEditDraftParams{
		ArtifactID: art.ID,
		ActorID:    pgUUID(in.ActorID),
		ID:         pgUUID(sessionID),
	}); err == nil && ref.Valid {
		inherited = ref.String
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return EditSession{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EditSession{}, err
	}

	session := editSessionFromRow(row, token)
	if inherited == "" {
		return session, nil
	}
	draft, err := readDraft(ctx, s.Store, inherited)
	if err != nil || len(draft) == 0 || len(draft) > MaxEditDraftBytes || !utf8.Valid(draft) {
		return session, nil
	}
	key := editDraftKey(in.WorkspaceID, sessionID)
	if _, err := s.Store.Upload(ctx, key, draft, "text/plain", "draft.txt"); err != nil {
		return session, nil
	}
	if err := setDraftRef(ctx, s, sessionID, key); err != nil {
		s.Store.Delete(ctx, key)
		return session, nil
	}
	session.Draft = draft
	return session, nil
}

// HeartbeatEditSession extends the lease when the holder, token, and fencing token match.
func (s *Service) HeartbeatEditSession(ctx context.Context, lease EditLease) (EditSession, error) {
	if err := s.configured(); err != nil {
		return EditSession{}, err
	}
	q, err := s.queries()
	if err != nil {
		return EditSession{}, err
	}
	row, err := q.RenewArtifactEditLease(ctx, db.RenewArtifactEditLeaseParams{
		ID:             pgUUID(lease.SessionID),
		ActorID:        pgUUID(lease.ActorID),
		LeaseTokenHash: lease.TokenHash,
		FencingToken:   lease.FencingToken,
		TtlSecs:        int64(s.editLeaseTTL() / time.Second),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return EditSession{}, s.leaseMiss(ctx, lease)
	}
	if err != nil {
		return EditSession{}, err
	}
	return editSessionFromRow(row, ""), nil
}

// SaveEditDraft stores text on the session. It does not move head.
func (s *Service) SaveEditDraft(ctx context.Context, workspaceID uuid.UUID, lease EditLease, content []byte) (EditSession, error) {
	if err := s.configured(); err != nil {
		return EditSession{}, err
	}
	if workspaceID == uuid.Nil || len(content) > MaxEditDraftBytes || !utf8.Valid(content) {
		return EditSession{}, fmt.Errorf("%w: draft", ErrInvalidInput)
	}
	key := editDraftKey(workspaceID, lease.SessionID)
	q, err := s.queries()
	if err != nil {
		return EditSession{}, err
	}
	row, err := q.SaveArtifactEditDraft(ctx, db.SaveArtifactEditDraftParams{
		ID:             pgUUID(lease.SessionID),
		ActorID:        pgUUID(lease.ActorID),
		LeaseTokenHash: lease.TokenHash,
		FencingToken:   lease.FencingToken,
		DraftRef:       pgText(key),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return EditSession{}, s.leaseMiss(ctx, lease)
	}
	if err != nil {
		return EditSession{}, err
	}
	if _, err := s.Store.Upload(ctx, key, content, "text/plain", "draft.txt"); err != nil {
		return EditSession{}, fmt.Errorf("store edit draft: %w", err)
	}
	return editSessionFromRow(row, ""), nil
}

func (s *Service) queries() (*db.Queries, error) {
	q, ok := s.DB.(db.DBTX)
	if !ok || q == nil {
		return nil, fmt.Errorf("%w: service is not configured", ErrInvalidInput)
	}
	return db.New(q), nil
}

func (s *Service) leaseMiss(ctx context.Context, lease EditLease) error {
	q, err := s.queries()
	if err != nil {
		return err
	}
	row, err := q.GetArtifactEditSession(ctx, pgUUID(lease.SessionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseExpired
	}
	if err != nil {
		return err
	}
	if uuid.UUID(row.ActorID.Bytes) != lease.ActorID {
		return ErrNotFound
	}
	if row.State == "open" && row.LeaseExpiresAt.Valid && row.LeaseExpiresAt.Time.After(time.Now()) &&
		!leaseMatches(row.LeaseTokenHash, row.FencingToken, lease) {
		return ErrLeaseHeld
	}
	return ErrLeaseExpired
}

func enforceEditLease(ctx context.Context, q *db.Queries, artifactID, actor uuid.UUID, lease *EditLease) error {
	row, err := q.LockOpenArtifactEditSession(ctx, pgUUID(artifactID))
	if errors.Is(err, pgx.ErrNoRows) {
		if lease != nil {
			return ErrLeaseExpired
		}
		return nil
	}
	if err != nil {
		return err
	}
	if row.LeaseElapsed {
		if lease != nil {
			return ErrLeaseExpired
		}
		return nil
	}
	if lease == nil || lease.SessionID != uuid.UUID(row.ID.Bytes) || lease.ActorID != actor ||
		uuid.UUID(row.ActorID.Bytes) != actor || !leaseMatches(row.LeaseTokenHash, row.FencingToken, *lease) {
		return ErrLeaseHeld
	}
	return nil
}

func (s *Service) rejectLease(ctx context.Context, q *db.Queries, tx pgx.Tx, committed *bool, up db.ArtifactUpload, stagingKey string, cause error) (CommitResult, error) {
	if _, err := q.TransitionArtifactUpload(ctx, db.TransitionArtifactUploadParams{
		ToState:     "cancelled",
		ID:          up.ID,
		WorkspaceID: up.WorkspaceID,
		FromState:   up.State,
	}); err != nil {
		return CommitResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CommitResult{}, err
	}
	*committed = true
	s.Store.Delete(ctx, stagingKey)
	return CommitResult{}, cause
}

func releaseEditLease(ctx context.Context, q *db.Queries, lease *EditLease) error {
	if lease == nil {
		return nil
	}
	return q.ReleaseArtifactEditSession(ctx, pgUUID(lease.SessionID))
}

func finishEditLease(ctx context.Context, q *db.Queries, artifactID, actor uuid.UUID, lease *EditLease) error {
	if lease == nil {
		return nil
	}
	n, err := q.CommitArtifactEditSession(ctx, db.CommitArtifactEditSessionParams{
		ID:             pgUUID(lease.SessionID),
		ArtifactID:     pgUUID(artifactID),
		ActorID:        pgUUID(actor),
		LeaseTokenHash: lease.TokenHash,
		FencingToken:   lease.FencingToken,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrLeaseExpired
	}
	return nil
}

func leaseMatches(hash string, fencing int64, lease EditLease) bool {
	if lease.FencingToken != fencing || len(hash) != len(lease.TokenHash) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(hash), []byte(lease.TokenHash)) == 1
}

func newLeaseToken() (token, hash string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("new lease token: %w", err)
	}
	token = hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(sum[:]), nil
}

func hashLeaseToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func editDraftKey(workspaceID, sessionID uuid.UUID) string {
	return "workspaces/" + workspaceID.String() + "/artifact-drafts/" + sessionID.String()
}

func readDraft(ctx context.Context, store interface {
	GetReader(context.Context, string) (io.ReadCloser, error)
}, key string) ([]byte, error) {
	reader, err := store.GetReader(ctx, key)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(io.LimitReader(reader, MaxEditDraftBytes+1))
}

func setDraftRef(ctx context.Context, s *Service, sessionID uuid.UUID, key string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := db.New(tx).SetArtifactEditDraftRef(ctx, db.SetArtifactEditDraftRefParams{
		ID:       pgUUID(sessionID),
		DraftRef: pgText(key),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func editSessionFromRow(row db.ArtifactEditSession, token string) EditSession {
	return EditSession{
		ID:             uuid.UUID(row.ID.Bytes),
		ArtifactID:     uuid.UUID(row.ArtifactID.Bytes),
		BaseRevisionID: uuid.UUID(row.BaseRevisionID.Bytes),
		ActorID:        uuid.UUID(row.ActorID.Bytes),
		EditorType:     row.EditorType,
		FencingToken:   row.FencingToken,
		LeaseExpiresAt: row.LeaseExpiresAt.Time,
		LeaseToken:     token,
		State:          row.State,
	}
}

// HashLeaseToken is the hash stored for a lease token. The raw token is
// returned once, when the session is created.
func HashLeaseToken(token string) string {
	return hashLeaseToken(token)
}
