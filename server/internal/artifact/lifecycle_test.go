package artifact_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/artifact"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestTrashBlocksUseAndRestoreDoesNotReviveShare(t *testing.T) {
	pool, h, ws, actor := setup(t)
	created := createArtifact(t, h.svc, ws, actor, "Trash note")
	body := []byte("keep-me")
	first := commitBytes(t, h.svc, ws, actor, created.ID, nil, "trash-v1", "note.md", body)
	published, err := h.svc.Publish(context.Background(), artifact.PublishInput{
		WorkspaceID: ws,
		ActorID:     actor,
		ArtifactID:  created.ID,
		RevisionID:  first.RevisionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := h.svc.OpenEditSession(context.Background(), artifact.OpenEditSessionInput{
		WorkspaceID: ws,
		ArtifactID:  created.ID,
		ActorID:     actor,
		EditorType:  "human",
	})
	if err != nil {
		t.Fatal(err)
	}
	lease := artifact.EditLease{
		SessionID:    session.ID,
		ActorID:      actor,
		FencingToken: session.FencingToken,
		TokenHash:    artifact.HashLeaseToken(session.LeaseToken),
	}
	if _, err := h.svc.SaveEditDraft(context.Background(), ws, lease, []byte("draft")); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Trash(context.Background(), ws, actor, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Commit(context.Background(), commitInput(ws, actor, created.ID, &first.RevisionID, "trash-v2", "note.md", []byte("nope"))); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatalf("commit after trash = %v", err)
	}
	if _, err := h.svc.OpenShare(context.Background(), published.Token); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatalf("share after trash = %v", err)
	}
	if _, err := h.svc.HeartbeatEditSession(context.Background(), lease); !errors.Is(err, artifact.ErrLeaseExpired) {
		t.Fatalf("heartbeat after trash = %v", err)
	}
	var listed int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM artifacts WHERE id = $1 AND deleted_at IS NULL AND status = 'active'
	`, created.ID).Scan(&listed); err != nil {
		t.Fatal(err)
	}
	if listed != 0 {
		t.Fatal("trashed artifact is still active")
	}
	if err := h.svc.RestoreTrash(context.Background(), ws, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.OpenShare(context.Background(), published.Token); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatalf("share after restore = %v", err)
	}
	if _, err := h.svc.HeartbeatEditSession(context.Background(), lease); !errors.Is(err, artifact.ErrLeaseExpired) {
		t.Fatalf("old lease after restore = %v", err)
	}
	var state string
	var revoked bool
	if err := pool.QueryRow(context.Background(), `
		SELECT state FROM artifact_edit_sessions WHERE id = $1
	`, session.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "expired" {
		t.Fatalf("session state = %s", state)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT revoked_at IS NOT NULL FROM artifact_publish_snapshots WHERE id = $1
	`, published.SnapshotID).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	if !revoked {
		t.Fatal("restore cleared the share revoke")
	}
	opened, err := h.svc.OpenEditSession(context.Background(), artifact.OpenEditSessionInput{
		WorkspaceID: ws,
		ArtifactID:  created.ID,
		ActorID:     actor,
		EditorType:  "human",
	})
	if err != nil {
		t.Fatal(err)
	}
	if opened.ID == session.ID || opened.State != "open" {
		t.Fatalf("new session = %+v", opened)
	}
	reader, err := h.store.GetReader(context.Background(), first.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(reader)
	reader.Close()
	if string(got) != string(body) {
		t.Fatalf("bytes after restore = %q", got)
	}
}

func TestGCKeepsBlobReferencedElsewhere(t *testing.T) {
	pool, h, ws, actor := setup(t)
	created := createArtifact(t, h.svc, ws, actor, "Shared blob")
	first := commitBytes(t, h.svc, ws, actor, created.ID, nil, "gc-v1", "note.md", []byte("shared-blob"))
	base := first.RevisionID
	restored, err := h.svc.Restore(context.Background(), artifact.RestoreInput{
		WorkspaceID:    ws,
		ActorID:        actor,
		ArtifactID:     created.ID,
		RevisionID:     first.RevisionID,
		BaseRevisionID: base,
	})
	if err != nil {
		t.Fatal(err)
	}
	if restored.BlobID != first.BlobID {
		t.Fatalf("restore blob = %s, want %s", restored.BlobID, first.BlobID)
	}
	if _, err := pool.Exec(context.Background(), `DELETE FROM artifact_revisions WHERE id = $1`, restored.RevisionID); err != nil {
		t.Fatal(err)
	}
	session, err := h.svc.OpenEditSession(context.Background(), artifact.OpenEditSessionInput{
		WorkspaceID: ws,
		ArtifactID:  created.ID,
		ActorID:     actor,
		EditorType:  "human",
	})
	if err != nil {
		t.Fatal(err)
	}
	lease := artifact.EditLease{
		SessionID:    session.ID,
		ActorID:      actor,
		FencingToken: session.FencingToken,
		TokenHash:    artifact.HashLeaseToken(session.LeaseToken),
	}
	if _, err := h.svc.SaveEditDraft(context.Background(), ws, lease, []byte("draft-bytes")); err != nil {
		t.Fatal(err)
	}
	var draftKey string
	if err := pool.QueryRow(context.Background(), `SELECT draft_ref FROM artifact_edit_sessions WHERE id = $1`, session.ID).Scan(&draftKey); err != nil {
		t.Fatal(err)
	}
	draftBlob := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO artifact_blobs (id, storage_key, size_bytes, sha256, detected_media_type, scan_status)
		VALUES ($1, $2, $3, $4, 'text/plain', 'clean')
	`, draftBlob, draftKey, len("draft-bytes"), digest([]byte("draft-bytes"))); err != nil {
		t.Fatal(err)
	}
	orphanKey := "workspaces/" + ws.String() + "/artifacts/" + uuid.Must(uuid.NewV7()).String()
	if _, err := h.store.Upload(context.Background(), orphanKey, []byte("orphan"), "text/plain", "orphan.txt"); err != nil {
		t.Fatal(err)
	}
	orphanID := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO artifact_blobs (id, storage_key, size_bytes, sha256, detected_media_type, scan_status)
		VALUES ($1, $2, 6, $3, 'text/plain', 'clean')
	`, orphanID, orphanKey, digest([]byte("orphan"))); err != nil {
		t.Fatal(err)
	}
	published, err := h.svc.Publish(context.Background(), artifact.PublishInput{
		WorkspaceID: ws,
		ActorID:     actor,
		ArtifactID:  created.ID,
		RevisionID:  first.RevisionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.svc.GCPaused = true
	paused, err := h.svc.CollectGarbage(context.Background(), ws, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !paused.Paused || paused.Deleted != 0 {
		t.Fatalf("paused gc = %+v", paused)
	}
	if _, err := h.store.GetReader(context.Background(), orphanKey); err != nil {
		t.Fatal("paused gc deleted the orphan")
	}
	h.svc.GCPaused = false
	swept, err := h.svc.CollectGarbage(context.Background(), ws, 100)
	if err != nil {
		t.Fatal(err)
	}
	if swept.Deleted != 1 || swept.Kept < 1 {
		t.Fatalf("gc = %+v", swept)
	}
	if _, err := h.store.GetReader(context.Background(), orphanKey); err == nil {
		t.Fatal("orphan blob survived gc")
	}
	if _, err := h.store.GetReader(context.Background(), first.StorageKey); err != nil {
		t.Fatal("blob referenced by the remaining revision was deleted")
	}
	if _, err := h.store.GetReader(context.Background(), draftKey); err != nil {
		t.Fatal("draft key was deleted")
	}
	var manifestKey string
	if err := pool.QueryRow(context.Background(), `
		SELECT artifact_blobs.storage_key
		FROM artifact_blobs
		JOIN artifact_publish_snapshots ON artifact_publish_snapshots.manifest_blob_id = artifact_blobs.id
		WHERE artifact_publish_snapshots.id = $1
	`, published.SnapshotID).Scan(&manifestKey); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.GetReader(context.Background(), manifestKey); err != nil {
		t.Fatal("snapshot manifest blob was deleted")
	}
	health, err := h.svc.OpsHealth(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if health.LastGCDeleted != 1 || health.RetentionClass != "ops_default" {
		t.Fatalf("health = %+v", health)
	}
}

func TestRetentionSweepLeavesConflictDraft(t *testing.T) {
	pool, h, ws, actor := setup(t)
	created := createArtifact(t, h.svc, ws, actor, "Conflict")
	v1 := commitBytes(t, h.svc, ws, actor, created.ID, nil, "ret-v1", "note.md", []byte("v1"))
	base := v1.RevisionID
	commitBytes(t, h.svc, ws, actor, created.ID, &base, "ret-v2", "note.md", []byte("v2"))
	stale := v1.RevisionID
	_, err := h.svc.Commit(context.Background(), commitInput(ws, actor, created.ID, &stale, "ret-stale", "note.md", []byte("stale")))
	if !errors.Is(err, artifact.ErrRevisionConflict) {
		t.Fatalf("stale = %v", err)
	}
	var draftKey string
	var draftExpires time.Time
	if err := pool.QueryRow(context.Background(), `
		SELECT staging_key, expires_at FROM artifact_uploads
		WHERE artifact_id = $1 AND state = 'conflict_draft'
	`, created.ID).Scan(&draftKey, &draftExpires); err != nil {
		t.Fatal(err)
	}
	if draftExpires.Before(time.Now().Add(6 * 24 * time.Hour)) {
		t.Fatalf("conflict draft expires too soon: %s", draftExpires)
	}
	staged, err := h.svc.CreateUpload(context.Background(), artifact.CreateUploadInput{
		WorkspaceID:    ws,
		ActorID:        actor,
		ArtifactID:     created.ID,
		ExpectedSize:   4,
		ExpectedSHA256: digest([]byte("late")),
		Filename:       "late.md",
		MediaType:      "text/markdown",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Upload(context.Background(), staged.StagingKey, []byte("late"), "text/plain", "late.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE artifact_uploads SET expires_at = now() - interval '1 minute' WHERE id = $1
	`, staged.ID); err != nil {
		t.Fatal(err)
	}
	failedKey := "workspaces/" + ws.String() + "/artifact-derivatives/" + uuid.Must(uuid.NewV7()).String()
	if _, err := h.store.Upload(context.Background(), failedKey, []byte("<p>old</p>"), "text/html", "preview.html"); err != nil {
		t.Fatal(err)
	}
	keptKey := "workspaces/" + ws.String() + "/artifact-derivatives/" + uuid.Must(uuid.NewV7()).String()
	if _, err := h.store.Upload(context.Background(), keptKey, []byte("<p>new</p>"), "text/html", "preview.html"); err != nil {
		t.Fatal(err)
	}
	failedID := uuid.Must(uuid.NewV7())
	keptID := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO artifact_derivatives (id, workspace_id, artifact_id, revision_id, format, status, storage_key, expires_at)
		VALUES ($1, $2, $3, $4, 'docx', 'failed', $5, now() - interval '1 hour'),
		       ($6, $2, $3, $4, 'xlsx', 'failed', $7, now() + interval '6 days')
	`, failedID, ws, created.ID, v1.RevisionID, failedKey, keptID, keptKey); err != nil {
		t.Fatal(err)
	}
	swept, err := h.svc.SweepRetention(context.Background(), ws, 100)
	if err != nil {
		t.Fatal(err)
	}
	if swept.StagingExpired != 1 || swept.DerivativesCleared != 1 {
		t.Fatalf("sweep = %+v", swept)
	}
	var state string
	if err := pool.QueryRow(context.Background(), `SELECT state FROM artifact_uploads WHERE id = $1`, staged.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "expired" {
		t.Fatalf("staging state = %s", state)
	}
	if _, err := h.store.GetReader(context.Background(), draftKey); err != nil {
		t.Fatal("conflict draft staging was deleted early")
	}
	if _, err := h.store.GetReader(context.Background(), failedKey); err == nil {
		t.Fatal("expired failed derivative object survived")
	}
	if _, err := h.store.GetReader(context.Background(), keptKey); err != nil {
		t.Fatal("in-retention failed derivative was deleted")
	}
	h.svc.GCPaused = true
	paused, err := h.svc.SweepRetention(context.Background(), ws, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !paused.Paused {
		t.Fatal("paused sweep ran")
	}
}

func TestQuotaSettlementBalances(t *testing.T) {
	_, h, ws, actor := setup(t)
	created := createArtifact(t, h.svc, ws, actor, "Quota")
	body := []byte("committed-bytes")
	commitBytes(t, h.svc, ws, actor, created.ID, nil, "quota-v1", "note.md", body)
	if _, err := h.svc.CreateUpload(context.Background(), artifact.CreateUploadInput{
		WorkspaceID:    ws,
		ActorID:        actor,
		ArtifactID:     created.ID,
		ExpectedSize:   40,
		ExpectedSHA256: digest([]byte("reserved-upload-bytes-pad-to-forty")),
		Filename:       "open.md",
		MediaType:      "text/markdown",
	}); err != nil {
		t.Fatal(err)
	}
	health, err := h.svc.OpsHealth(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if health.ReservedBytes != 40 || health.CommittedBytes != int64(len(body)) || health.DriftBytes != 0 {
		t.Fatalf("health = %+v", health)
	}
	if health.TrashRetention == "" || health.StagingRetention == "" || health.ConflictDraftRetention == "" {
		t.Fatalf("retention = %+v", health)
	}
	if err := h.svc.PurgeExpiredTrash(context.Background(), ws); !errors.Is(err, artifact.ErrPurgeRefused) {
		t.Fatalf("purge = %v", err)
	}
}

func TestBackupRestoreRoundTrip(t *testing.T) {
	pool, h, ws, actor := setup(t)
	created := createArtifact(t, h.svc, ws, actor, "Backup")
	body := []byte("backup-bytes")
	first := commitBytes(t, h.svc, ws, actor, created.ID, nil, "bak-v1", "note.md", body)
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	for _, stmt := range []string{
		`CREATE TEMP TABLE bak_blobs AS SELECT * FROM artifact_blobs WHERE id IN (SELECT blob_id FROM artifact_revisions WHERE artifact_id IN (SELECT id FROM artifacts WHERE workspace_id = $1))`,
		`CREATE TEMP TABLE bak_artifacts AS SELECT * FROM artifacts WHERE workspace_id = $1`,
		`CREATE TEMP TABLE bak_revisions AS SELECT * FROM artifact_revisions WHERE artifact_id IN (SELECT id FROM artifacts WHERE workspace_id = $1)`,
	} {
		if _, err := conn.Exec(ctx, stmt, ws); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.New(conn).DeleteWorkspaceArtifacts(ctx, pgUUID(ws)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO artifact_blobs SELECT * FROM bak_blobs`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO artifacts SELECT * FROM bak_artifacts`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO artifact_revisions SELECT * FROM bak_revisions`); err != nil {
		t.Fatal(err)
	}
	reader, err := h.store.GetReader(ctx, first.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(reader)
	reader.Close()
	if string(got) != string(body) {
		t.Fatalf("restored bytes = %q", got)
	}
	var title string
	if err := conn.QueryRow(ctx, `SELECT title FROM artifacts WHERE id = $1`, created.ID).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Backup" {
		t.Fatalf("title = %s", title)
	}
}

func TestArtifactPastTrashIsNotPurged(t *testing.T) {
	pool, h, ws, actor := setup(t)
	created := createArtifact(t, h.svc, ws, actor, "Old trash")
	commitBytes(t, h.svc, ws, actor, created.ID, nil, "old-v1", "note.md", []byte("old"))
	if err := h.svc.Trash(context.Background(), ws, actor, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE artifacts SET deleted_at = now() - interval '31 days' WHERE id = $1
	`, created.ID); err != nil {
		t.Fatal(err)
	}
	health, err := h.svc.OpsHealth(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if health.TrashPastRetention != 1 {
		t.Fatalf("past retention = %d", health.TrashPastRetention)
	}
	if err := h.svc.PurgeExpiredTrash(context.Background(), ws); !errors.Is(err, artifact.ErrPurgeRefused) {
		t.Fatalf("purge = %v", err)
	}
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM artifacts WHERE id = $1`, created.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("purge removed the artifact")
	}
	_ = pool
}
