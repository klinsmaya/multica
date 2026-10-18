package artifact_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/artifact"
	"github.com/multica-ai/multica/server/internal/storage"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type readWatch struct {
	storage.Storage
	reads []string
}

func (w *readWatch) GetReader(ctx context.Context, key string) (io.ReadCloser, error) {
	w.reads = append(w.reads, key)
	return w.Storage.GetReader(ctx, key)
}

func commitMedia(t *testing.T, svc *artifact.Service, ws, actor, art uuid.UUID, base *uuid.UUID, key, filename, media string, body []byte) artifact.CommitResult {
	t.Helper()
	got, err := svc.Commit(context.Background(), artifact.CommitInput{
		WorkspaceID:    ws,
		ActorID:        actor,
		ArtifactID:     art,
		BaseRevisionID: base,
		IdempotencyKey: key,
		Filename:       filename,
		MediaType:      media,
		SHA256:         digest(body),
		Source:         "agent",
		Data:           body,
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestPublishSnapshotStaysOnFirstRevision(t *testing.T) {
	pool, h, ws, actor := setup(t)
	created := createArtifact(t, h.svc, ws, actor, "Pinned note")
	v1 := []byte("version-one")
	first := commitBytes(t, h.svc, ws, actor, created.ID, nil, "pin-v1", "note.md", v1)
	published, err := h.svc.Publish(context.Background(), artifact.PublishInput{
		WorkspaceID: ws,
		ActorID:     actor,
		ArtifactID:  created.ID,
		RevisionID:  first.RevisionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if published.Token == "" || published.Audience != artifact.AudienceWorkspaceMembers {
		t.Fatalf("publish = %+v", published)
	}
	base := first.RevisionID
	second := commitBytes(t, h.svc, ws, actor, created.ID, &base, "pin-v2", "note.md", []byte("version-two"))
	if second.RevisionID == first.RevisionID {
		t.Fatal("second commit did not create a revision")
	}
	opened, err := h.svc.OpenShare(context.Background(), published.Token)
	if err != nil {
		t.Fatal(err)
	}
	if string(opened.Data) != string(v1) || opened.RevisionID != first.RevisionID {
		t.Fatalf("opened revision %s body %q", opened.RevisionID, opened.Data)
	}
	var pinned uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		SELECT revision_id FROM artifact_publish_snapshots WHERE id = $1
	`, published.SnapshotID).Scan(&pinned); err != nil {
		t.Fatal(err)
	}
	if pinned != first.RevisionID {
		t.Fatalf("snapshot revision = %s, want %s", pinned, first.RevisionID)
	}
}

func TestPublishManifestOmitsUnlistedResource(t *testing.T) {
	_, h, ws, actor := setup(t)
	watch := &readWatch{Storage: h.store}
	h.svc.Store = watch
	page := createArtifact(t, h.svc, ws, actor, "Page")
	html := []byte(`<html><img src="secret.png"></html>`)
	pageRev := commitMedia(t, h.svc, ws, actor, page.ID, nil, "html-v1", "page.html", "text/html", html)
	secretArt := createArtifact(t, h.svc, ws, actor, "Secret")
	secret := commitMedia(t, h.svc, ws, actor, secretArt.ID, nil, "secret-v1", "secret.png", "text/plain", []byte("secret-bytes"))
	published, err := h.svc.Publish(context.Background(), artifact.PublishInput{
		WorkspaceID: ws,
		ActorID:     actor,
		ArtifactID:  page.ID,
		RevisionID:  pageRev.RevisionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	watch.reads = nil
	_, err = h.svc.OpenShareResource(context.Background(), published.Token, "secret.png")
	if !errors.Is(err, artifact.ErrNotFound) {
		t.Fatalf("secret resource error = %v", err)
	}
	for _, key := range watch.reads {
		if key == secret.StorageKey || key == pageRev.StorageKey {
			t.Fatalf("unlisted resource read %s", key)
		}
	}
	if len(watch.reads) == 0 {
		t.Fatal("manifest was not read")
	}
	watch.reads = nil
	got, err := h.svc.OpenShareResource(context.Background(), published.Token, "page.html")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Data) != string(html) {
		t.Fatalf("page = %q", got.Data)
	}
	sawPage := false
	for _, key := range watch.reads {
		if key == secret.StorageKey {
			t.Fatal("listed page read the secret blob")
		}
		if key == pageRev.StorageKey {
			sawPage = true
		}
	}
	if !sawPage {
		t.Fatal("listed page was not read")
	}
}

func TestPublishRevokeWritesAuditAndOutbox(t *testing.T) {
	pool, h, ws, actor := setup(t)
	created := createArtifact(t, h.svc, ws, actor, "Shared note")
	first := commitBytes(t, h.svc, ws, actor, created.ID, nil, "revoke-v1", "note.md", []byte("shared"))
	published, err := h.svc.Publish(context.Background(), artifact.PublishInput{
		WorkspaceID: ws,
		ActorID:     actor,
		ArtifactID:  created.ID,
		RevisionID:  first.RevisionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.RevokeShare(context.Background(), ws, actor, published.SnapshotID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.OpenShare(context.Background(), published.Token); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatalf("open after revoke = %v", err)
	}
	rows, err := db.New(pool).ListArtifactAuditEventsForTarget(context.Background(), db.ListArtifactAuditEventsForTargetParams{
		WorkspaceID: pgUUID(ws),
		TargetID:    pgUUID(published.SnapshotID),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"share.publish:allowed", "share.revoke:allowed", "share.view:denied"}
	if len(rows) != len(want) {
		t.Fatalf("audit = %+v", rows)
	}
	for i, row := range rows {
		got := row.Action + ":" + row.Result
		if got != want[i] {
			t.Fatalf("audit[%d] = %s, want %s", i, got, want[i])
		}
		if row.RequestID != "" {
			t.Fatalf("request id = %q", row.RequestID)
		}
	}
	var eventType string
	var snapshotID, revisionID uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		SELECT event_type, snapshot_id, revision_id
		FROM artifact_events
		WHERE artifact_id = $1 AND event_type = 'artifact.share.revoked'
	`, created.ID).Scan(&eventType, &snapshotID, &revisionID); err != nil {
		t.Fatal(err)
	}
	if snapshotID != published.SnapshotID || revisionID != first.RevisionID {
		t.Fatalf("event snapshot %s revision %s", snapshotID, revisionID)
	}
	var grants int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM artifact_share_grants WHERE snapshot_id = $1
	`, published.SnapshotID).Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if grants != 0 {
		t.Fatalf("grants = %d", grants)
	}
}

func TestPublishRejectsExternalAndPublicWhileDisabled(t *testing.T) {
	pool, h, ws, actor := setup(t)
	created := createArtifact(t, h.svc, ws, actor, "Private note")
	first := commitBytes(t, h.svc, ws, actor, created.ID, nil, "ext-v1", "note.md", []byte("private"))
	for _, audience := range []string{artifact.AudienceExternal, artifact.AudiencePublic} {
		_, err := h.svc.Publish(context.Background(), artifact.PublishInput{
			WorkspaceID: ws,
			ActorID:     actor,
			ArtifactID:  created.ID,
			RevisionID:  first.RevisionID,
			Audience:    audience,
		})
		if !errors.Is(err, artifact.ErrExternalShareDisabled) {
			t.Fatalf("%s error = %v", audience, err)
		}
	}
	assertSnapshotCount(t, pool, created.ID, 0)
	h.svc.ExternalShareEnabled = true
	published, err := h.svc.Publish(context.Background(), artifact.PublishInput{
		WorkspaceID: ws,
		ActorID:     actor,
		ArtifactID:  created.ID,
		RevisionID:  first.RevisionID,
		Audience:    artifact.AudienceExternal,
	})
	if err != nil {
		t.Fatal(err)
	}
	if published.Audience != artifact.AudienceExternal || published.Token == "" {
		t.Fatalf("enabled publish = %+v", published)
	}
	h.svc.ExternalShareEnabled = false
}

func TestPublishNamedMemberGrantDoesNotUseSnapshotToken(t *testing.T) {
	_, h, ws, actor := setup(t)
	created := createArtifact(t, h.svc, ws, actor, "Named note")
	first := commitBytes(t, h.svc, ws, actor, created.ID, nil, "named-v1", "note.md", []byte("for-one"))
	grantee := uuid.New()
	published, err := h.svc.Publish(context.Background(), artifact.PublishInput{
		WorkspaceID: ws,
		ActorID:     actor,
		ArtifactID:  created.ID,
		RevisionID:  first.RevisionID,
		Audience:    artifact.AudienceNamedMembers,
		GranteeIDs:  []uuid.UUID{grantee},
	})
	if err != nil {
		t.Fatal(err)
	}
	if published.Token != "" || len(published.Grants) != 1 || published.Grants[0].Token == "" {
		t.Fatalf("named publish = %+v", published)
	}
	opened, err := h.svc.OpenShare(context.Background(), published.Grants[0].Token)
	if err != nil {
		t.Fatal(err)
	}
	if string(opened.Data) != "for-one" {
		t.Fatalf("grant body = %q", opened.Data)
	}
	if _, err := h.svc.OpenShare(context.Background(), "not-the-grant"); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatalf("missing token error = %v", err)
	}
}

func TestPublishExpiryDeniesBeforeRead(t *testing.T) {
	pool, h, ws, actor := setup(t)
	watch := &readWatch{Storage: h.store}
	h.svc.Store = watch
	created := createArtifact(t, h.svc, ws, actor, "Expired note")
	first := commitBytes(t, h.svc, ws, actor, created.ID, nil, "exp-v1", "note.md", []byte("old"))
	past := time.Now().Add(-time.Minute)
	published, err := h.svc.Publish(context.Background(), artifact.PublishInput{
		WorkspaceID: ws,
		ActorID:     actor,
		ArtifactID:  created.ID,
		RevisionID:  first.RevisionID,
		ExpiresAt:   &past,
	})
	if err != nil {
		t.Fatal(err)
	}
	watch.reads = nil
	if _, err := h.svc.OpenShareContent(context.Background(), published.Token); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatalf("expired open = %v", err)
	}
	if len(watch.reads) != 0 {
		t.Fatalf("expired share read %v", watch.reads)
	}
	rows, err := db.New(pool).ListArtifactAuditEventsForTarget(context.Background(), db.ListArtifactAuditEventsForTargetParams{
		WorkspaceID: pgUUID(ws),
		TargetID:    pgUUID(published.SnapshotID),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[1].Action != "share.download" || rows[1].Result != "denied" {
		t.Fatalf("audit = %+v", rows)
	}
}

func assertSnapshotCount(t *testing.T, pool *pgxpool.Pool, artifactID uuid.UUID, want int) {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM artifact_publish_snapshots WHERE artifact_id = $1
	`, artifactID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != want {
		t.Fatalf("snapshots = %d, want %d", n, want)
	}
}
