package artifact

import (
	"context"
	"crypto/rand"
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
	AudienceWorkspaceMembers = "workspace_members"
	AudienceNamedMembers     = "named_members"
	AudienceExternal         = "external"
	AudiencePublic           = "public"
)

// PublishInput pins one revision. Audience external and public are refused
// unless ExternalShareEnabled is set. That switch defaults off.
type PublishInput struct {
	WorkspaceID uuid.UUID
	ActorID     uuid.UUID
	ArtifactID  uuid.UUID
	RevisionID  uuid.UUID
	Audience    string
	GranteeIDs  []uuid.UUID
	ExpiresAt   *time.Time
}

// ShareGrantToken is returned once. The hash is what the database stores.
type ShareGrantToken struct {
	GranteeUserID uuid.UUID `json:"grantee_user_id"`
	Token         string    `json:"token"`
}

// PublishResult is the snapshot. Token is empty for named-member grants.
// Storage keys are not included.
type PublishResult struct {
	SnapshotID uuid.UUID         `json:"snapshot_id"`
	RevisionID uuid.UUID         `json:"revision_id"`
	Audience   string            `json:"audience"`
	Token      string            `json:"token,omitempty"`
	Grants     []ShareGrantToken `json:"grants,omitempty"`
	ExpiresAt  *time.Time        `json:"expires_at,omitempty"`
}

// ShareFile is the pinned revision or one manifest path. It has no storage key.
type ShareFile struct {
	SnapshotID uuid.UUID
	RevisionID uuid.UUID
	Filename   string
	MediaType  string
	Data       []byte
}

type shareManifest struct {
	RevisionID string              `json:"revision_id"`
	Files      []shareManifestFile `json:"files"`
}

type shareManifestFile struct {
	Path   string `json:"path"`
	BlobID string `json:"blob_id"`
}

// Publish stores a snapshot of one revision and a manifest of that file only.
// A later commit does not change the snapshot. Referenced paths that are not
// this revision are not added.
func (s *Service) Publish(ctx context.Context, in PublishInput) (PublishResult, error) {
	if err := s.configured(); err != nil {
		return PublishResult{}, err
	}
	in.Audience = strings.TrimSpace(in.Audience)
	if in.Audience == "" {
		in.Audience = AudienceWorkspaceMembers
	}
	switch in.Audience {
	case AudienceWorkspaceMembers:
		if len(in.GranteeIDs) != 0 {
			return PublishResult{}, fmt.Errorf("%w: workspace audience has no grantee list", ErrInvalidInput)
		}
	case AudienceNamedMembers:
		if len(in.GranteeIDs) == 0 {
			return PublishResult{}, fmt.Errorf("%w: named audience needs a grantee", ErrInvalidInput)
		}
	case AudienceExternal, AudiencePublic:
		if !s.ExternalShareEnabled {
			return PublishResult{}, ErrExternalShareDisabled
		}
	default:
		return PublishResult{}, fmt.Errorf("%w: audience", ErrInvalidInput)
	}
	if in.WorkspaceID == uuid.Nil || in.ActorID == uuid.Nil || in.ArtifactID == uuid.Nil || in.RevisionID == uuid.Nil {
		return PublishResult{}, fmt.Errorf("%w: publish", ErrInvalidInput)
	}

	q, err := s.queries()
	if err != nil {
		return PublishResult{}, err
	}
	art, err := q.GetArtifactByID(ctx, pgUUID(in.ArtifactID))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && uuid.UUID(art.WorkspaceID.Bytes) != in.WorkspaceID) {
		return PublishResult{}, ErrNotFound
	}
	if err != nil {
		return PublishResult{}, err
	}
	rev, err := q.GetArtifactRevisionRecord(ctx, db.GetArtifactRevisionRecordParams{
		ID:         pgUUID(in.RevisionID),
		ArtifactID: pgUUID(in.ArtifactID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return PublishResult{}, ErrNotFound
	}
	if err != nil {
		return PublishResult{}, err
	}
	filename := rev.Filename
	if filename == "" {
		filename = "file"
	}
	manifest := shareManifest{
		RevisionID: in.RevisionID.String(),
		Files: []shareManifestFile{{
			Path:   filename,
			BlobID: uuid.UUID(rev.BlobID.Bytes).String(),
		}},
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return PublishResult{}, err
	}
	sum := sha256.Sum256(body)
	snapshotID, err := uuid.NewV7()
	if err != nil {
		return PublishResult{}, err
	}
	manifestBlobID, err := uuid.NewV7()
	if err != nil {
		return PublishResult{}, err
	}
	key := fmt.Sprintf("workspaces/%s/artifact-manifests/%s", in.WorkspaceID, manifestBlobID)
	if _, err := s.Store.Upload(ctx, key, body, "application/json", "manifest.json"); err != nil {
		return PublishResult{}, err
	}
	snapshotToken, snapshotHash, err := newShareToken()
	if err != nil {
		s.Store.Delete(ctx, key)
		return PublishResult{}, err
	}
	grants := make([]ShareGrantToken, 0, len(in.GranteeIDs))
	type grantRow struct {
		id    uuid.UUID
		user  uuid.UUID
		hash  string
		token string
	}
	rows := make([]grantRow, 0, len(in.GranteeIDs))
	for _, userID := range in.GranteeIDs {
		if userID == uuid.Nil {
			s.Store.Delete(ctx, key)
			return PublishResult{}, fmt.Errorf("%w: grantee", ErrInvalidInput)
		}
		token, hash, err := newShareToken()
		if err != nil {
			s.Store.Delete(ctx, key)
			return PublishResult{}, err
		}
		id, err := uuid.NewV7()
		if err != nil {
			s.Store.Delete(ctx, key)
			return PublishResult{}, err
		}
		rows = append(rows, grantRow{id: id, user: userID, hash: hash, token: token})
		grants = append(grants, ShareGrantToken{GranteeUserID: userID, Token: token})
	}
	if in.Audience == AudienceNamedMembers {
		snapshotToken = ""
	}

	tx, err := s.DB.Begin(ctx)
	if err != nil {
		s.Store.Delete(ctx, key)
		return PublishResult{}, err
	}
	defer tx.Rollback(ctx)
	qx := db.New(tx)
	if _, err := qx.InsertArtifactBlob(ctx, db.InsertArtifactBlobParams{
		ID:                pgUUID(manifestBlobID),
		StorageKey:        key,
		SizeBytes:         int64(len(body)),
		Sha256:            hex.EncodeToString(sum[:]),
		DetectedMediaType: "application/json",
		ScanStatus:        "clean",
	}); err != nil {
		s.Store.Delete(ctx, key)
		return PublishResult{}, err
	}
	var expires pgtype.Timestamptz
	if in.ExpiresAt != nil {
		expires = pgtype.Timestamptz{Time: in.ExpiresAt.UTC(), Valid: true}
	}
	if err := qx.InsertArtifactPublishSnapshot(ctx, db.InsertArtifactPublishSnapshotParams{
		ID:             pgUUID(snapshotID),
		WorkspaceID:    pgUUID(in.WorkspaceID),
		ArtifactID:     pgUUID(in.ArtifactID),
		RevisionID:     pgUUID(in.RevisionID),
		ManifestBlobID: pgUUID(manifestBlobID),
		Audience:       in.Audience,
		TokenHash:      snapshotHash,
		ExpiresAt:      expires,
		CreatedBy:      pgUUID(in.ActorID),
	}); err != nil {
		s.Store.Delete(ctx, key)
		return PublishResult{}, err
	}
	for _, row := range rows {
		if err := qx.InsertArtifactShareGrant(ctx, db.InsertArtifactShareGrantParams{
			ID:            pgUUID(row.id),
			WorkspaceID:   pgUUID(in.WorkspaceID),
			SnapshotID:    pgUUID(snapshotID),
			GranteeUserID: pgUUID(row.user),
			TokenHash:     row.hash,
		}); err != nil {
			s.Store.Delete(ctx, key)
			return PublishResult{}, err
		}
	}
	if err := insertAudit(ctx, qx, in.WorkspaceID, &in.ActorID, "share.publish", snapshotID, "allowed"); err != nil {
		s.Store.Delete(ctx, key)
		return PublishResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		s.Store.Delete(ctx, key)
		return PublishResult{}, err
	}
	return PublishResult{
		SnapshotID: snapshotID,
		RevisionID: in.RevisionID,
		Audience:   in.Audience,
		Token:      snapshotToken,
		Grants:     grants,
		ExpiresAt:  in.ExpiresAt,
	}, nil
}

const (
	shareView shareAccess = iota
	shareDownload
	shareResource
)

type shareAccess int

// OpenShare reads the pinned revision for a share token and records share.view.
// A session cookie is not an input. Revoked and expired snapshots fail before
// the file is read.
func (s *Service) OpenShare(ctx context.Context, token string) (ShareFile, error) {
	return s.openShare(ctx, token, "", shareView)
}

// OpenShareContent reads the pinned revision and records share.download.
func (s *Service) OpenShareContent(ctx context.Context, token string) (ShareFile, error) {
	return s.openShare(ctx, token, "", shareDownload)
}

// OpenShareResource serves one path from the snapshot manifest. A path that
// was not listed is not read, including a file the HTML merely names.
func (s *Service) OpenShareResource(ctx context.Context, token, path string) (ShareFile, error) {
	return s.openShare(ctx, token, path, shareResource)
}

func (s *Service) openShare(ctx context.Context, token, path string, access shareAccess) (ShareFile, error) {
	if err := s.configured(); err != nil {
		return ShareFile{}, err
	}
	q, err := s.queries()
	if err != nil {
		return ShareFile{}, err
	}
	snap, grantUser, err := s.lookupShare(ctx, token)
	if err != nil {
		return ShareFile{}, err
	}
	action := "share.view"
	if access != shareView {
		action = "share.download"
	}
	workspaceID := uuid.UUID(snap.WorkspaceID.Bytes)
	snapshotID := uuid.UUID(snap.ID.Bytes)
	deny := func() (ShareFile, error) {
		_ = insertAudit(ctx, q, workspaceID, grantUser, action, snapshotID, "denied")
		return ShareFile{}, ErrNotFound
	}
	if snap.RevokedAt.Valid || (snap.ExpiresAt.Valid && !snap.ExpiresAt.Time.After(time.Now())) {
		return deny()
	}
	manifestBlob, err := q.GetArtifactBlob(ctx, snap.ManifestBlobID)
	if err != nil {
		return deny()
	}
	raw, err := readStorage(ctx, s.Store, manifestBlob.StorageKey)
	if err != nil {
		return deny()
	}
	var manifest shareManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return deny()
	}
	rev, err := q.GetArtifactRevisionRecord(ctx, db.GetArtifactRevisionRecordParams{
		ID:         snap.RevisionID,
		ArtifactID: snap.ArtifactID,
	})
	if err != nil {
		return deny()
	}
	want := rev.Filename
	if want == "" {
		want = "file"
	}
	if access == shareResource {
		want = strings.TrimSpace(path)
	}
	var listed bool
	for _, file := range manifest.Files {
		if file.Path == want && file.BlobID == uuid.UUID(rev.BlobID.Bytes).String() {
			listed = true
			break
		}
	}
	if !listed {
		return deny()
	}
	blob, err := q.GetArtifactBlob(ctx, rev.BlobID)
	if err != nil {
		return deny()
	}
	data, err := readStorage(ctx, s.Store, blob.StorageKey)
	if err != nil {
		return deny()
	}
	if err := insertAudit(ctx, q, workspaceID, grantUser, action, snapshotID, "allowed"); err != nil {
		return ShareFile{}, err
	}
	return ShareFile{
		SnapshotID: snapshotID,
		RevisionID: uuid.UUID(snap.RevisionID.Bytes),
		Filename:   rev.Filename,
		MediaType:  rev.MediaType,
		Data:       data,
	}, nil
}

// RevokeShare clears the snapshot and its grants. The outbox row carries ids only.
func (s *Service) RevokeShare(ctx context.Context, workspaceID, actorID, snapshotID uuid.UUID) error {
	if err := s.configured(); err != nil {
		return err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	snap, err := q.GetArtifactPublishSnapshotByID(ctx, db.GetArtifactPublishSnapshotByIDParams{
		ID:          pgUUID(snapshotID),
		WorkspaceID: pgUUID(workspaceID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if snap.RevokedAt.Valid {
		return nil
	}
	n, err := q.RevokeArtifactPublishSnapshot(ctx, db.RevokeArtifactPublishSnapshotParams{
		ID:          pgUUID(snapshotID),
		WorkspaceID: pgUUID(workspaceID),
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
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
	if err := insertShareEvent(ctx, q, workspaceID, artID, revID, snapshotID, EventShareRevoked); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return nil
}

func newShareToken() (string, string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	token := hex.EncodeToString(buf)
	sum := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(sum[:]), nil
}

// lookupShare resolves a raw token to a snapshot. Named-member snapshots are
// reached only through a grant token. The snapshot token hash is not a grant.
func (s *Service) lookupShare(ctx context.Context, token string) (db.ArtifactPublishSnapshot, *uuid.UUID, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return db.ArtifactPublishSnapshot{}, nil, ErrNotFound
	}
	q, err := s.queries()
	if err != nil {
		return db.ArtifactPublishSnapshot{}, nil, err
	}
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	grant, err := q.GetArtifactShareGrantByTokenHash(ctx, hash)
	if err == nil {
		snap, snapErr := q.GetArtifactPublishSnapshotByID(ctx, db.GetArtifactPublishSnapshotByIDParams{
			ID:          grant.SnapshotID,
			WorkspaceID: grant.WorkspaceID,
		})
		if errors.Is(snapErr, pgx.ErrNoRows) {
			return db.ArtifactPublishSnapshot{}, nil, ErrNotFound
		}
		if snapErr != nil {
			return db.ArtifactPublishSnapshot{}, nil, snapErr
		}
		user := uuid.UUID(grant.GranteeUserID.Bytes)
		return snap, &user, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.ArtifactPublishSnapshot{}, nil, err
	}
	snap, err := q.GetArtifactPublishSnapshotByTokenHash(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.ArtifactPublishSnapshot{}, nil, ErrNotFound
	}
	if err != nil {
		return db.ArtifactPublishSnapshot{}, nil, err
	}
	if snap.Audience == AudienceNamedMembers {
		return db.ArtifactPublishSnapshot{}, nil, ErrNotFound
	}
	return snap, nil, nil
}

func insertAudit(ctx context.Context, q *db.Queries, workspaceID uuid.UUID, actorID *uuid.UUID, action string, target uuid.UUID, result string) error {
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	return q.InsertArtifactAuditEvent(ctx, db.InsertArtifactAuditEventParams{
		ID:          pgUUID(id),
		WorkspaceID: pgUUID(workspaceID),
		ActorID:     pgUUIDPtr(actorID),
		Action:      action,
		TargetID:    pgUUID(target),
		Result:      result,
		RequestID:   "",
	})
}

func insertShareEvent(ctx context.Context, q *db.Queries, workspaceID, artifactID, revisionID, snapshotID uuid.UUID, kind string) error {
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	return q.InsertArtifactEvent(ctx, db.InsertArtifactEventParams{
		ID:          pgUUID(id),
		WorkspaceID: pgUUID(workspaceID),
		ArtifactID:  pgUUID(artifactID),
		RevisionID:  pgUUIDPtr(&revisionID),
		EventType:   kind,
		SnapshotID:  pgUUID(snapshotID),
	})
}

func readStorage(ctx context.Context, store interface {
	GetReader(context.Context, string) (io.ReadCloser, error)
}, key string) ([]byte, error) {
	if store == nil || key == "" {
		return nil, ErrNotFound
	}
	reader, err := store.GetReader(ctx, key)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	const maxShareBytes = 100 << 20
	data, err := io.ReadAll(io.LimitReader(reader, maxShareBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxShareBytes {
		return nil, ErrNotFound
	}
	return data, nil
}
