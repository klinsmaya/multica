package artifact_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/artifact"
	"github.com/multica-ai/multica/server/internal/storage"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestCreateAndCommitV1(t *testing.T) {
	pool, h, ws, actor := setup(t)
	svc := h.svc
	created := createArtifact(t, svc, ws, actor, "report.md")
	if created.HeadRevisionID != nil {
		t.Fatal("new artifact has a head revision")
	}
	body := []byte("# report v1\n")
	got := commitBytes(t, svc, ws, actor, created.ID, nil, "key-v1", "report.md", body)
	if got.RevisionNo != 1 {
		t.Fatalf("revision_no = %d, want 1", got.RevisionNo)
	}
	if got.Replayed {
		t.Fatal("first commit was replayed")
	}
	if got.HeadRevisionID != got.RevisionID {
		t.Fatalf("head %s, revision %s", got.HeadRevisionID, got.RevisionID)
	}
	if got.RowVersion != 1 {
		t.Fatalf("row_version = %d, want 1", got.RowVersion)
	}
	if strings.Contains(got.StorageKey, "report.md") {
		t.Fatalf("storage key %q contains the filename", got.StorageKey)
	}
	if !strings.HasPrefix(got.StorageKey, "workspaces/"+ws.String()+"/artifacts/") {
		t.Fatalf("storage key %q is not under the workspace artifact prefix", got.StorageKey)
	}
	assertRevisionCount(t, pool, created.ID, 1)
	assertHead(t, pool, created.ID, got.RevisionID, 1)
	assertStoredBytes(t, h.store, got.StorageKey, body)
	if h.store.uploads != 2 {
		t.Fatalf("storage uploads = %d, want staging plus seal", h.store.uploads)
	}
	if state := uploadStateByRevision(t, pool, got.RevisionID); state != "committed" {
		t.Fatalf("upload state = %s, want committed", state)
	}
}

func TestCommitMatchingBaseAdvancesHead(t *testing.T) {
	pool, h, ws, actor := setup(t)
	svc := h.svc
	created := createArtifact(t, svc, ws, actor, "notes.md")
	v1 := commitBytes(t, svc, ws, actor, created.ID, nil, "notes-v1", "notes.md", []byte("one"))
	base := v1.RevisionID
	v2 := commitBytes(t, svc, ws, actor, created.ID, &base, "notes-v2", "notes.md", []byte("two"))
	if v2.RevisionNo != 2 {
		t.Fatalf("revision_no = %d, want 2", v2.RevisionNo)
	}
	if v2.RevisionID == v1.RevisionID {
		t.Fatal("second commit reused the first revision id")
	}
	if v2.RowVersion != 2 {
		t.Fatalf("row_version = %d, want 2", v2.RowVersion)
	}
	assertRevisionCount(t, pool, created.ID, 2)
	assertHead(t, pool, created.ID, v2.RevisionID, 2)
	var v1SHA string
	if err := pool.QueryRow(context.Background(), `SELECT sha256 FROM artifact_revisions WHERE id = $1`, v1.RevisionID).Scan(&v1SHA); err != nil {
		t.Fatal(err)
	}
	if v1SHA != digest([]byte("one")) {
		t.Fatalf("v1 sha256 changed to %s", v1SHA)
	}
}

func TestCommitWrongBaseConflicts(t *testing.T) {
	pool, h, ws, actor := setup(t)
	svc := h.svc
	created := createArtifact(t, svc, ws, actor, "plan.md")
	v1 := commitBytes(t, svc, ws, actor, created.ID, nil, "plan-v1", "plan.md", []byte("v1"))
	base := v1.RevisionID
	v2 := commitBytes(t, svc, ws, actor, created.ID, &base, "plan-v2", "plan.md", []byte("v2"))

	stale := v1.RevisionID
	_, err := svc.Commit(context.Background(), commitInput(ws, actor, created.ID, &stale, "plan-stale", "plan.md", []byte("stale")))
	if !errors.Is(err, artifact.ErrRevisionConflict) {
		t.Fatalf("stale base error = %v, want revision conflict", err)
	}
	_, err = svc.Commit(context.Background(), commitInput(ws, actor, created.ID, &stale, "plan-stale", "plan.md", []byte("stale")))
	if !errors.Is(err, artifact.ErrRevisionConflict) {
		t.Fatalf("retry error = %v, want the same revision conflict", err)
	}
	unknown := uuid.Must(uuid.NewV7())
	_, err = svc.Commit(context.Background(), commitInput(ws, actor, created.ID, &unknown, "plan-unknown", "plan.md", []byte("other")))
	if !errors.Is(err, artifact.ErrRevisionConflict) {
		t.Fatalf("unknown base error = %v, want revision conflict", err)
	}
	assertRevisionCount(t, pool, created.ID, 2)
	assertHead(t, pool, created.ID, v2.RevisionID, 2)
}

func TestCommitIdempotentRetryDoesNotCreateSecondRevision(t *testing.T) {
	pool, h, ws, actor := setup(t)
	svc := h.svc
	created := createArtifact(t, svc, ws, actor, "retry.md")
	body := []byte("same bytes")
	in := commitInput(ws, actor, created.ID, nil, "retry-key", "retry.md", body)
	first, err := svc.Commit(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Commit(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed {
		t.Fatal("retry was not marked replayed")
	}
	if second.RevisionID != first.RevisionID || second.RevisionNo != 1 || second.OperationID != first.OperationID {
		t.Fatalf("retry result %+v, first %+v", second, first)
	}
	assertRevisionCount(t, pool, created.ID, 1)
	if h.store.uploads != 2 {
		t.Fatalf("storage uploads = %d, want 2", h.store.uploads)
	}
}

func TestCommitSameKeyDifferentRequestConflicts(t *testing.T) {
	pool, h, ws, actor := setup(t)
	svc := h.svc
	created := createArtifact(t, svc, ws, actor, "once.md")
	first := commitBytes(t, svc, ws, actor, created.ID, nil, "once-key", "once.md", []byte("original"))
	_, err := svc.Commit(context.Background(), commitInput(ws, actor, created.ID, nil, "once-key", "once.md", []byte("different")))
	if !errors.Is(err, artifact.ErrIdempotencyReused) {
		t.Fatalf("error = %v, want idempotency key reused", err)
	}
	assertRevisionCount(t, pool, created.ID, 1)
	assertHead(t, pool, created.ID, first.RevisionID, 1)
	if h.store.uploads != 2 {
		t.Fatalf("storage uploads = %d, want 2", h.store.uploads)
	}
}

func TestSameFilenameDoesNotMergeArtifacts(t *testing.T) {
	pool, h, ws, actor := setup(t)
	svc := h.svc
	first := createArtifact(t, svc, ws, actor, "report.md")
	second := createArtifact(t, svc, ws, actor, "report.md")
	if first.ID == second.ID {
		t.Fatal("same title reused one artifact id")
	}
	commitBytes(t, svc, ws, actor, first.ID, nil, "name-a", "report.md", []byte("alpha"))
	commitBytes(t, svc, ws, actor, second.ID, nil, "name-b", "report.md", []byte("beta"))
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM artifacts WHERE workspace_id = $1 AND title = $2`,
		ws, "report.md").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("artifacts titled report.md = %d, want 2", n)
	}
	assertRevisionCount(t, pool, first.ID, 1)
	assertRevisionCount(t, pool, second.ID, 1)
}

func TestRevisionRowIsImmutable(t *testing.T) {
	pool, h, ws, actor := setup(t)
	created := createArtifact(t, h.svc, ws, actor, "fixed.md")
	got := commitBytes(t, h.svc, ws, actor, created.ID, nil, "fixed-v1", "fixed.md", []byte("keep"))
	_, err := pool.Exec(context.Background(),
		`UPDATE artifact_revisions SET filename = 'changed.md' WHERE id = $1`, got.RevisionID)
	if err == nil || !strings.Contains(err.Error(), "artifact revisions are immutable") {
		t.Fatalf("update error = %v", err)
	}
	var filename string
	if err := pool.QueryRow(context.Background(),
		`SELECT filename FROM artifact_revisions WHERE id = $1`, got.RevisionID).Scan(&filename); err != nil {
		t.Fatal(err)
	}
	if filename != "fixed.md" {
		t.Fatalf("filename = %q", filename)
	}
}

func TestCommitRejectsDigestMismatch(t *testing.T) {
	_, h, ws, actor := setup(t)
	svc := h.svc
	created := createArtifact(t, svc, ws, actor, "bad.md")
	in := commitInput(ws, actor, created.ID, nil, "bad-key", "bad.md", []byte("bytes"))
	in.SHA256 = digest([]byte("other"))
	_, err := svc.Commit(context.Background(), in)
	if !errors.Is(err, artifact.ErrDigestMismatch) {
		t.Fatalf("error = %v, want digest mismatch", err)
	}
	if h.store.uploads != 0 {
		t.Fatalf("storage uploads = %d, want 0", h.store.uploads)
	}
}

func TestDeleteWorkspaceArtifactsRemovesRowsAndKeys(t *testing.T) {
	pool, h, ws, actor := setup(t)
	svc := h.svc
	created := createArtifact(t, svc, ws, actor, "gone.md")
	got := commitBytes(t, svc, ws, actor, created.ID, nil, "gone-v1", "gone.md", []byte("bye"))
	ctx := context.Background()
	keys, err := db.New(pool).ListArtifactStorageKeysByWorkspace(ctx, pgUUID(ws))
	if err != nil {
		t.Fatal(err)
	}
	var sealed, staging bool
	for _, key := range keys {
		switch key {
		case got.StorageKey:
			sealed = true
		default:
			if strings.Contains(key, "/artifact-staging/") {
				staging = true
			}
		}
	}
	if len(keys) != 2 || !sealed || !staging {
		t.Fatalf("keys = %v, want sealed %s and its staging key", keys, got.StorageKey)
	}
	if err := db.New(pool).DeleteWorkspaceArtifacts(ctx, pgUUID(ws)); err != nil {
		t.Fatal(err)
	}
	assertRevisionCount(t, pool, created.ID, 0)
	for _, table := range []string{"artifacts", "artifact_uploads", "artifact_operations"} {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE workspace_id = $1`, ws).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s rows left = %d", table, n)
		}
	}
	var blobs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM artifact_blobs WHERE storage_key = $1`, got.StorageKey).Scan(&blobs); err != nil {
		t.Fatal(err)
	}
	if blobs != 0 {
		t.Fatalf("blob rows left = %d", blobs)
	}
}

func TestStagingReadyThenCommit(t *testing.T) {
	pool, h, ws, actor := setup(t)
	created := createArtifact(t, h.svc, ws, actor, "staged.md")
	body := []byte("# report v1\n")
	up := stageReady(t, pool, h.svc, ws, actor, created.ID, nil, "staged.md", body)
	got, err := h.svc.Finalize(context.Background(), artifact.FinalizeInput{
		WorkspaceID:    ws,
		ActorID:        actor,
		UploadID:       up.ID,
		IdempotencyKey: "stage-v1",
		Source:         "agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.RevisionNo != 1 || got.Replayed {
		t.Fatalf("result = %+v", got)
	}
	if uploadState(t, pool, up.ID) != "committed" {
		t.Fatalf("state = %s", uploadState(t, pool, up.ID))
	}
	assertStoredBytes(t, h.store, got.StorageKey, body)
	if _, err := h.store.GetReader(context.Background(), up.StagingKey); err == nil {
		t.Fatal("staging object still readable after seal")
	}
	_, err = pool.Exec(context.Background(), `UPDATE artifact_uploads SET filename = 'other.md' WHERE id = $1`, up.ID)
	if err == nil || !strings.Contains(err.Error(), "artifact upload binding is immutable") {
		t.Fatalf("binding update error = %v", err)
	}
	var scan string
	if err := pool.QueryRow(context.Background(), `SELECT scan_status FROM artifact_blobs WHERE id = $1`, got.BlobID).Scan(&scan); err != nil {
		t.Fatal(err)
	}
	if scan != "clean" {
		t.Fatalf("scan_status = %s, want clean", scan)
	}
	assertRevisionCount(t, pool, created.ID, 1)
}

func TestVerifyDigestMismatchRejects(t *testing.T) {
	pool, h, ws, actor := setup(t)
	created := createArtifact(t, h.svc, ws, actor, "mismatch.md")
	body := []byte("bytes")
	up, err := h.svc.CreateUpload(context.Background(), artifact.CreateUploadInput{
		WorkspaceID:    ws,
		ActorID:        actor,
		ArtifactID:     created.ID,
		ExpectedSize:   int64(len(body)),
		ExpectedSHA256: digest([]byte("other")),
		Filename:       "mismatch.md",
		MediaType:      "text/markdown",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.WriteStaging(context.Background(), ws, up.ID, body); err != nil {
		t.Fatal(err)
	}
	err = h.svc.VerifyUpload(context.Background(), ws, up.ID)
	if !errors.Is(err, artifact.ErrDigestMismatch) {
		t.Fatalf("error = %v, want digest mismatch", err)
	}
	if uploadState(t, pool, up.ID) != "rejected" {
		t.Fatalf("state = %s", uploadState(t, pool, up.ID))
	}
	if _, err := h.store.GetReader(context.Background(), up.StagingKey); err == nil {
		t.Fatal("rejected staging object is still readable")
	}
	assertRevisionCount(t, pool, created.ID, 0)
	if h.store.uploads != 1 {
		t.Fatalf("storage uploads = %d, want the staging write only", h.store.uploads)
	}
}

func TestFinalizeWrongBaseKeepsConflictDraft(t *testing.T) {
	pool, h, ws, actor := setup(t)
	created := createArtifact(t, h.svc, ws, actor, "draft.md")
	v1 := commitBytes(t, h.svc, ws, actor, created.ID, nil, "draft-v1", "draft.md", []byte("v1"))
	base := v1.RevisionID
	v2 := commitBytes(t, h.svc, ws, actor, created.ID, &base, "draft-v2", "draft.md", []byte("v2"))
	stale := v1.RevisionID
	body := []byte("stale")
	up := stageReady(t, pool, h.svc, ws, actor, created.ID, &stale, "draft.md", body)

	finalize := func(key string) error {
		_, err := h.svc.Finalize(context.Background(), artifact.FinalizeInput{
			WorkspaceID:    ws,
			ActorID:        actor,
			UploadID:       up.ID,
			IdempotencyKey: key,
			Source:         "agent",
		})
		return err
	}
	if err := finalize("draft-stale"); !errors.Is(err, artifact.ErrRevisionConflict) {
		t.Fatalf("error = %v, want revision conflict", err)
	}
	if uploadState(t, pool, up.ID) != "conflict_draft" {
		t.Fatalf("state = %s", uploadState(t, pool, up.ID))
	}
	assertStoredBytes(t, h.store, up.StagingKey, body)
	var expires time.Time
	if err := pool.QueryRow(context.Background(), `SELECT expires_at FROM artifact_uploads WHERE id = $1`, up.ID).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if expires.Before(time.Now().Add(6 * 24 * time.Hour)) {
		t.Fatalf("conflict draft expires at %s", expires)
	}
	if err := finalize("draft-stale"); !errors.Is(err, artifact.ErrRevisionConflict) {
		t.Fatalf("retry error = %v, want revision conflict", err)
	}
	if err := finalize("draft-other"); !errors.Is(err, artifact.ErrRevisionConflict) {
		t.Fatalf("other key error = %v, want revision conflict", err)
	}
	assertRevisionCount(t, pool, created.ID, 2)
	assertHead(t, pool, created.ID, v2.RevisionID, 2)
	if _, err := h.store.GetReader(context.Background(), up.StagingKey); err != nil {
		t.Fatalf("conflict draft staging was dropped: %v", err)
	}
}

func TestFinalizeIdempotent(t *testing.T) {
	pool, h, ws, actor := setup(t)
	created := createArtifact(t, h.svc, ws, actor, "again.md")
	body := []byte("same bytes")
	up := stageReady(t, pool, h.svc, ws, actor, created.ID, nil, "again.md", body)
	in := artifact.FinalizeInput{
		WorkspaceID:    ws,
		ActorID:        actor,
		UploadID:       up.ID,
		IdempotencyKey: "again-key",
		Source:         "agent",
	}
	first, err := h.svc.Finalize(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.svc.Finalize(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed || second.RevisionID != first.RevisionID || second.OperationID != first.OperationID {
		t.Fatalf("second = %+v, first = %+v", second, first)
	}
	assertRevisionCount(t, pool, created.ID, 1)
	if h.store.uploads != 2 {
		t.Fatalf("storage uploads = %d, want staging plus one seal", h.store.uploads)
	}
}

func TestConsumedUploadCannotRecommit(t *testing.T) {
	pool, h, ws, actor := setup(t)
	created := createArtifact(t, h.svc, ws, actor, "once-upload.md")
	up := stageReady(t, pool, h.svc, ws, actor, created.ID, nil, "once-upload.md", []byte("sealed"))
	if _, err := h.svc.Finalize(context.Background(), artifact.FinalizeInput{
		WorkspaceID:    ws,
		ActorID:        actor,
		UploadID:       up.ID,
		IdempotencyKey: "consume-a",
		Source:         "agent",
	}); err != nil {
		t.Fatal(err)
	}
	_, err := h.svc.Finalize(context.Background(), artifact.FinalizeInput{
		WorkspaceID:    ws,
		ActorID:        actor,
		UploadID:       up.ID,
		IdempotencyKey: "consume-b",
		Source:         "agent",
	})
	if !errors.Is(err, artifact.ErrUploadConsumed) {
		t.Fatalf("error = %v, want upload consumed", err)
	}
	assertRevisionCount(t, pool, created.ID, 1)
	if h.store.uploads != 2 {
		t.Fatalf("storage uploads = %d, want no second seal", h.store.uploads)
	}
}

func TestScanRejectedDoesNotCommit(t *testing.T) {
	pool, h, ws, actor := setup(t)
	h.svc.Scanner = fixedScanner{verdict: artifact.ScanRejected}
	created := createArtifact(t, h.svc, ws, actor, "virus.md")
	body := []byte("suspicious")
	up, err := h.svc.CreateUpload(context.Background(), uploadInput(ws, actor, created.ID, nil, "virus.md", body))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.WriteStaging(context.Background(), ws, up.ID, body); err != nil {
		t.Fatal(err)
	}
	err = h.svc.VerifyUpload(context.Background(), ws, up.ID)
	if !errors.Is(err, artifact.ErrUploadRejected) {
		t.Fatalf("verify error = %v, want upload rejected", err)
	}
	if uploadState(t, pool, up.ID) != "rejected" {
		t.Fatalf("state = %s", uploadState(t, pool, up.ID))
	}
	_, err = h.svc.Finalize(context.Background(), artifact.FinalizeInput{
		WorkspaceID:    ws,
		ActorID:        actor,
		UploadID:       up.ID,
		IdempotencyKey: "virus-key",
		Source:         "agent",
	})
	if !errors.Is(err, artifact.ErrUploadRejected) {
		t.Fatalf("finalize error = %v, want upload rejected", err)
	}
	assertRevisionCount(t, pool, created.ID, 0)
}

func TestScanPendingStaysPending(t *testing.T) {
	pool, h, ws, actor := setup(t)
	h.svc.Scanner = fixedScanner{verdict: artifact.ScanPending}
	created := createArtifact(t, h.svc, ws, actor, "wait.md")
	body := []byte("waiting")
	up, err := h.svc.CreateUpload(context.Background(), uploadInput(ws, actor, created.ID, nil, "wait.md", body))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.WriteStaging(context.Background(), ws, up.ID, body); err != nil {
		t.Fatal(err)
	}
	err = h.svc.VerifyUpload(context.Background(), ws, up.ID)
	if !errors.Is(err, artifact.ErrScanPending) {
		t.Fatalf("verify error = %v, want scan pending", err)
	}
	if uploadState(t, pool, up.ID) != "scanning" {
		t.Fatalf("state = %s", uploadState(t, pool, up.ID))
	}
	if err := h.svc.VerifyUpload(context.Background(), ws, up.ID); !errors.Is(err, artifact.ErrScanPending) {
		t.Fatalf("second verify error = %v", err)
	}
	if uploadState(t, pool, up.ID) != "scanning" {
		t.Fatal("pending scan became ready")
	}
	_, err = h.svc.Finalize(context.Background(), artifact.FinalizeInput{
		WorkspaceID:    ws,
		ActorID:        actor,
		UploadID:       up.ID,
		IdempotencyKey: "wait-key",
		Source:         "agent",
	})
	if !errors.Is(err, artifact.ErrScanPending) {
		t.Fatalf("finalize error = %v, want scan pending", err)
	}
	assertRevisionCount(t, pool, created.ID, 0)
}

func TestDisguisedMediaTypeRejected(t *testing.T) {
	pool, h, ws, actor := setup(t)
	created := createArtifact(t, h.svc, ws, actor, "fake.md")
	body := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	up, err := h.svc.CreateUpload(context.Background(), artifact.CreateUploadInput{
		WorkspaceID:    ws,
		ActorID:        actor,
		ArtifactID:     created.ID,
		ExpectedSize:   int64(len(body)),
		ExpectedSHA256: digest(body),
		Filename:       "fake.md",
		MediaType:      "text/markdown",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.WriteStaging(context.Background(), ws, up.ID, body); err != nil {
		t.Fatal(err)
	}
	err = h.svc.VerifyUpload(context.Background(), ws, up.ID)
	if !errors.Is(err, artifact.ErrMediaTypeMismatch) {
		t.Fatalf("error = %v, want media type mismatch", err)
	}
	if uploadState(t, pool, up.ID) != "rejected" {
		t.Fatalf("state = %s", uploadState(t, pool, up.ID))
	}
	assertRevisionCount(t, pool, created.ID, 0)
}

func TestWorkspaceAndActorQuota(t *testing.T) {
	pool, h, ws, actor := setup(t)
	created := createArtifact(t, h.svc, ws, actor, "quota.md")
	h.svc.WorkspaceQuotaBytes = 8
	open, err := h.svc.CreateUpload(context.Background(), sizedUpload(ws, actor, created.ID, 5))
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.svc.CreateUpload(context.Background(), sizedUpload(ws, actor, created.ID, 5))
	if !errors.Is(err, artifact.ErrQuotaExceeded) {
		t.Fatalf("workspace quota error = %v", err)
	}
	if err := h.svc.CancelUpload(context.Background(), ws, open.ID); err != nil {
		t.Fatal(err)
	}

	h.svc.WorkspaceQuotaBytes = 1 << 20
	h.svc.ActorQuotaBytes = 8
	commitBytes(t, h.svc, ws, actor, created.ID, nil, "quota-v1", "quota.md", []byte("12345"))
	_, err = h.svc.CreateUpload(context.Background(), sizedUpload(ws, actor, created.ID, 4))
	if !errors.Is(err, artifact.ErrQuotaExceeded) {
		t.Fatalf("actor quota error = %v", err)
	}
	assertRevisionCount(t, pool, created.ID, 1)
}

func TestCancelAndExpireDropStaging(t *testing.T) {
	pool, h, ws, actor := setup(t)
	created := createArtifact(t, h.svc, ws, actor, "gone-staging.md")
	cancelled := stageReady(t, pool, h.svc, ws, actor, created.ID, nil, "cancel.md", []byte("cancel"))
	if err := h.svc.CancelUpload(context.Background(), ws, cancelled.ID); err != nil {
		t.Fatal(err)
	}
	if uploadState(t, pool, cancelled.ID) != "cancelled" {
		t.Fatalf("state = %s", uploadState(t, pool, cancelled.ID))
	}
	if _, err := h.store.GetReader(context.Background(), cancelled.StagingKey); err == nil {
		t.Fatal("cancelled staging object is still readable")
	}
	_, err := h.svc.Finalize(context.Background(), artifact.FinalizeInput{
		WorkspaceID:    ws,
		ActorID:        actor,
		UploadID:       cancelled.ID,
		IdempotencyKey: "cancel-key",
		Source:         "agent",
	})
	if !errors.Is(err, artifact.ErrUploadCancelled) {
		t.Fatalf("finalize error = %v, want cancelled", err)
	}

	expired := stageReady(t, pool, h.svc, ws, actor, created.ID, nil, "expire.md", []byte("expire"))
	if _, err := pool.Exec(context.Background(),
		`UPDATE artifact_uploads SET expires_at = now() - interval '1 minute' WHERE id = $1`, expired.ID); err != nil {
		t.Fatal(err)
	}
	n, err := h.svc.ExpireUploads(context.Background(), ws, 10)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expired = %d, want 1", n)
	}
	if uploadState(t, pool, expired.ID) != "expired" {
		t.Fatalf("state = %s", uploadState(t, pool, expired.ID))
	}
	if _, err := h.store.GetReader(context.Background(), expired.StagingKey); err == nil {
		t.Fatal("expired staging object is still readable")
	}
	assertRevisionCount(t, pool, created.ID, 0)
}

type countingStore struct {
	*storage.LocalStorage
	uploads int
}

func (c *countingStore) Upload(ctx context.Context, key string, data []byte, contentType, filename string) (string, error) {
	c.uploads++
	return c.LocalStorage.Upload(ctx, key, data, contentType, filename)
}

type harness struct {
	svc   *artifact.Service
	store *countingStore
}

func setup(t *testing.T) (*pgxpool.Pool, *harness, uuid.UUID, uuid.UUID) {
	t.Helper()
	pool := openPool(t)
	h := newHarness(t, pool)
	fx := testutil.New(pool, "", "")
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	userID := fx.User(t, "Artifact User", "artifact-"+suffix+"@example.com")
	wsID := fx.Workspace(t, "Artifact Workspace", "art"+suffix[:12])
	fx.Member(t, wsID, userID, "owner")
	ws := uuid.MustParse(wsID)
	actor := uuid.MustParse(userID)
	t.Cleanup(func() {
		_ = db.New(pool).DeleteWorkspaceArtifacts(context.Background(), pgUUID(ws))
	})
	return pool, h, ws, actor
}

func newHarness(t *testing.T, pool *pgxpool.Pool) *harness {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LOCAL_UPLOAD_DIR", dir)
	t.Setenv("LOCAL_UPLOAD_BASE_URL", "")
	local := storage.NewLocalStorageFromEnv()
	if local == nil {
		t.Fatal("local storage was not created")
	}
	store := &countingStore{LocalStorage: local}
	return &harness{
		store: store,
		svc:   &artifact.Service{DB: pool, Store: store},
	}
}

func createArtifact(t *testing.T, svc *artifact.Service, ws, actor uuid.UUID, title string) artifact.Artifact {
	t.Helper()
	created, err := svc.Create(context.Background(), artifact.CreateInput{
		WorkspaceID: ws,
		OwnerScope:  "workspace",
		OwnerID:     ws,
		Title:       title,
		CreatedBy:   actor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Title != title || created.ID == uuid.Nil {
		t.Fatalf("created = %+v", created)
	}
	return created
}

func commitBytes(t *testing.T, svc *artifact.Service, ws, actor, art uuid.UUID, base *uuid.UUID, key, filename string, body []byte) artifact.CommitResult {
	t.Helper()
	got, err := svc.Commit(context.Background(), commitInput(ws, actor, art, base, key, filename, body))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func commitInput(ws, actor, art uuid.UUID, base *uuid.UUID, key, filename string, body []byte) artifact.CommitInput {
	return artifact.CommitInput{
		WorkspaceID:    ws,
		ActorID:        actor,
		ArtifactID:     art,
		BaseRevisionID: base,
		IdempotencyKey: key,
		Filename:       filename,
		MediaType:      "text/markdown",
		SHA256:         digest(body),
		Source:         "agent",
		Data:           body,
	}
}

func assertRevisionCount(t *testing.T, pool *pgxpool.Pool, artifactID uuid.UUID, want int) {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM artifact_revisions WHERE artifact_id = $1`, artifactID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != want {
		t.Fatalf("revisions = %d, want %d", n, want)
	}
}

func assertHead(t *testing.T, pool *pgxpool.Pool, artifactID, head uuid.UUID, version int64) {
	t.Helper()
	var gotHead uuid.UUID
	var gotVersion int64
	if err := pool.QueryRow(context.Background(),
		`SELECT head_revision_id, row_version FROM artifacts WHERE id = $1`, artifactID).Scan(&gotHead, &gotVersion); err != nil {
		t.Fatal(err)
	}
	if gotHead != head || gotVersion != version {
		t.Fatalf("head = %s version %d, want %s version %d", gotHead, gotVersion, head, version)
	}
}

func assertStoredBytes(t *testing.T, store storage.Storage, key string, want []byte) {
	t.Helper()
	rc, err := store.GetReader(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("stored bytes = %q, want %q", got, want)
	}
}

func stageReady(t *testing.T, pool *pgxpool.Pool, svc *artifact.Service, ws, actor, art uuid.UUID, base *uuid.UUID, filename string, body []byte) artifact.Upload {
	t.Helper()
	up, err := svc.CreateUpload(context.Background(), uploadInput(ws, actor, art, base, filename, body))
	if err != nil {
		t.Fatal(err)
	}
	if up.State != "created" || !strings.Contains(up.StagingKey, "/artifact-staging/") || strings.Contains(up.StagingKey, filename) {
		t.Fatalf("upload = %+v", up)
	}
	if err := svc.WriteStaging(context.Background(), ws, up.ID, body); err != nil {
		t.Fatal(err)
	}
	if uploadState(t, pool, up.ID) != "uploaded" {
		t.Fatalf("state after write = %s", uploadState(t, pool, up.ID))
	}
	if err := svc.VerifyUpload(context.Background(), ws, up.ID); err != nil {
		t.Fatal(err)
	}
	if uploadState(t, pool, up.ID) != "ready" {
		t.Fatalf("state after verify = %s", uploadState(t, pool, up.ID))
	}
	return up
}

func uploadInput(ws, actor, art uuid.UUID, base *uuid.UUID, filename string, body []byte) artifact.CreateUploadInput {
	return artifact.CreateUploadInput{
		WorkspaceID:    ws,
		ActorID:        actor,
		ArtifactID:     art,
		BaseRevisionID: base,
		ExpectedSize:   int64(len(body)),
		ExpectedSHA256: digest(body),
		Filename:       filename,
		MediaType:      "text/markdown",
	}
}

func sizedUpload(ws, actor, art uuid.UUID, n int) artifact.CreateUploadInput {
	return uploadInput(ws, actor, art, nil, "quota.md", bytes.Repeat([]byte{'a'}, n))
}

func uploadState(t *testing.T, pool *pgxpool.Pool, uploadID uuid.UUID) string {
	t.Helper()
	var state string
	if err := pool.QueryRow(context.Background(), `SELECT state FROM artifact_uploads WHERE id = $1`, uploadID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func uploadStateByRevision(t *testing.T, pool *pgxpool.Pool, revisionID uuid.UUID) string {
	t.Helper()
	var state string
	if err := pool.QueryRow(context.Background(),
		`SELECT state FROM artifact_uploads WHERE committed_revision_id = $1`, revisionID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

type fixedScanner struct {
	verdict artifact.ScanVerdict
}

func (f fixedScanner) Scan(context.Context, artifact.ScanObject) (artifact.ScanVerdict, error) {
	return f.verdict, nil
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func pgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte(id), Valid: true}
}

func openPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@127.0.0.1:5432/multica?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("database unavailable: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("database unreachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
