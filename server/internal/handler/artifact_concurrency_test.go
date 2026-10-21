package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/artifact"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestArtifactConcurrencyRestoreAndEvents(t *testing.T) {
	if testPool == nil || dbfx == nil {
		t.Skip("test database not available")
	}
	store := &mockStorage{}
	orig := testHandler.Storage
	testHandler.Storage = store
	t.Cleanup(func() { testHandler.Storage = orig })
	ws := mustUUID(t, testWorkspaceID)
	actor := mustUUID(t, testUserID)
	t.Cleanup(func() {
		_ = db.New(testPool).DeleteWorkspaceArtifacts(context.Background(), artifactPG(ws))
	})
	svc := &artifact.Service{DB: testPool, Store: store, Scanner: artifact.NopScanner{}}
	router := newArtifactConcurrencyRouter()
	member := map[string]string{"X-User-ID": testUserID, "X-Workspace-ID": testWorkspaceID}
	agentID := dbfx.Agent(t, "concurrency-agent", testRuntimeID)
	taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": testRuntimeID})
	taskUUID := mustUUID(t, taskID)

	leased := createHTTPArtifact(t, svc, ws, actor, taskUUID, "task", "Notes")
	v1 := commitHTTPArtifact(t, svc, ws, actor, leased.ID, nil, "con-v1", "notes.md", "text/markdown", []byte("a\nb\nc\n"))
	session := mustOpenEdit(t, router, leased.ID.String(), member)
	draft := editJSON(t, router, http.MethodPut, "/api/artifact-edit-sessions/"+session.EditSessionID+"/draft", member, map[string]any{
		"lease_token":   session.LeaseToken,
		"fencing_token": session.FencingToken,
		"content":       "keep me\n",
	})
	if draft.Code != http.StatusOK {
		t.Fatalf("draft = %d %s", draft.Code, draft.Body.String())
	}
	blocked := postArtifact(t, router, "/api/artifacts/"+leased.ID.String()+"/revisions", taskHeaders(taskID, agentID), map[string]string{
		"base_revision_id": v1.RevisionID.String(),
	}, "notes.md", []byte("a\nb\nC\n"))
	if code := errorCodeOf(t, blocked); blocked.Code != http.StatusConflict || code != artifact.CodeLeaseHeld {
		t.Fatalf("agent while leased = %d %s", blocked.Code, blocked.Body.String())
	}
	if got := artifactGET(t, router, "/api/artifacts/"+leased.ID.String()+"/revisions/"+v1.RevisionID.String()+"/content", testUserID, ""); got.Body.String() != "a\nb\nc\n" {
		t.Fatalf("lease did not keep head bytes: %q", got.Body.String())
	}

	if _, err := testPool.Exec(context.Background(), `UPDATE artifact_edit_sessions SET lease_expires_at = now() - interval '1 minute' WHERE id = $1`, session.EditSessionID); err != nil {
		t.Fatal(err)
	}
	stale := editJSON(t, router, http.MethodPost, "/api/artifact-edit-sessions/"+session.EditSessionID+"/heartbeat", member, map[string]any{
		"lease_token":   session.LeaseToken,
		"fencing_token": session.FencingToken,
	})
	if code := errorCodeOf(t, stale); stale.Code != http.StatusConflict || code != artifact.CodeLeaseExpired {
		t.Fatalf("expired heartbeat = %d %s", stale.Code, stale.Body.String())
	}
	spent := postArtifact(t, router, "/api/artifacts/"+leased.ID.String()+"/revisions", member, map[string]string{
		"base_revision_id": v1.RevisionID.String(),
		"edit_session_id":  session.EditSessionID,
		"fencing_token":    itoa(session.FencingToken),
		"lease_token":      session.LeaseToken,
	}, "notes.md", []byte("stolen\n"))
	if code := errorCodeOf(t, spent); spent.Code != http.StatusConflict || code != artifact.CodeLeaseExpired {
		t.Fatalf("spent fencing = %d %s", spent.Code, spent.Body.String())
	}
	var draftRef string
	if err := testPool.QueryRow(context.Background(), `SELECT draft_ref FROM artifact_edit_sessions WHERE id = $1`, session.EditSessionID).Scan(&draftRef); err != nil || draftRef == "" {
		t.Fatalf("draft_ref = %q, err %v", draftRef, err)
	}
	agentAfter := postArtifact(t, router, "/api/artifacts/"+leased.ID.String()+"/revisions", taskHeaders(taskID, agentID), map[string]string{
		"base_revision_id": v1.RevisionID.String(),
	}, "notes.md", []byte("a\nb\nagent\n"))
	if agentAfter.Code != http.StatusOK {
		t.Fatalf("agent after expiry = %d %s", agentAfter.Code, agentAfter.Body.String())
	}
	again := postArtifact(t, router, "/api/artifacts/"+leased.ID.String()+"/revisions", member, map[string]string{
		"base_revision_id": v1.RevisionID.String(),
		"edit_session_id":  session.EditSessionID,
		"fencing_token":    itoa(session.FencingToken),
		"lease_token":      session.LeaseToken,
	}, "notes.md", []byte("still stolen\n"))
	if code := errorCodeOf(t, again); again.Code != http.StatusConflict || code != artifact.CodeLeaseExpired {
		t.Fatalf("fencing after agent commit = %d %s", again.Code, again.Body.String())
	}

	mergedArt := createHTTPArtifact(t, svc, ws, actor, taskUUID, "task", "Merge")
	baseRev := commitHTTPArtifact(t, svc, ws, actor, mergedArt.ID, nil, "merge-v1", "merge.md", "text/markdown", []byte("a\nb\nc\n"))
	baseID := baseRev.RevisionID
	commitHTTPArtifact(t, svc, ws, actor, mergedArt.ID, &baseID, "merge-v2", "merge.md", "text/markdown", []byte("A\nb\nc\n"))
	merged := postArtifact(t, router, "/api/artifacts/"+mergedArt.ID.String()+"/revisions", taskHeaders(taskID, agentID), map[string]string{
		"base_revision_id": baseRev.RevisionID.String(),
	}, "merge.md", []byte("a\nb\nC\n"))
	if merged.Code != http.StatusOK {
		t.Fatalf("auto-merge = %d %s", merged.Code, merged.Body.String())
	}
	var mergedBody artifactCommandResponse
	if err := json.Unmarshal(merged.Body.Bytes(), &mergedBody); err != nil {
		t.Fatal(err)
	}
	if mergedBody.RevisionNo != 3 {
		t.Fatalf("merged revision = %+v", mergedBody)
	}
	if got := artifactGET(t, router, "/api/artifacts/"+mergedArt.ID.String()+"/revisions/"+mergedBody.RevisionID+"/content", testUserID, ""); got.Body.String() != "A\nb\nC\n" {
		t.Fatalf("merged bytes = %q", got.Body.String())
	}
	var revisions int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM artifact_revisions WHERE artifact_id = $1`, mergedArt.ID).Scan(&revisions); err != nil || revisions != 3 {
		t.Fatalf("history count = %d, err %v", revisions, err)
	}

	overlap := createHTTPArtifact(t, svc, ws, actor, taskUUID, "task", "Overlap")
	ov1 := commitHTTPArtifact(t, svc, ws, actor, overlap.ID, nil, "ov-v1", "overlap.md", "text/markdown", []byte("a\nb\n"))
	ovBase := ov1.RevisionID
	commitHTTPArtifact(t, svc, ws, actor, overlap.ID, &ovBase, "ov-v2", "overlap.md", "text/markdown", []byte("A\nb\n"))
	clash := postArtifact(t, router, "/api/artifacts/"+overlap.ID.String()+"/revisions", taskHeaders(taskID, agentID), map[string]string{
		"base_revision_id": ov1.RevisionID.String(),
	}, "overlap.md", []byte("Z\nb\n"))
	if code := errorCodeOf(t, clash); clash.Code != http.StatusConflict || code != artifact.CodeRevisionConflict {
		t.Fatalf("overlap = %d %s", clash.Code, clash.Body.String())
	}
	var uploadState string
	if err := testPool.QueryRow(context.Background(), `SELECT state FROM artifact_uploads WHERE artifact_id = $1 AND state = 'conflict_draft'`, overlap.ID).Scan(&uploadState); err != nil || uploadState != "conflict_draft" {
		t.Fatalf("overlap conflict_draft = %q, err %v", uploadState, err)
	}

	issueID := uuid.New()
	history := createHTTPArtifact(t, svc, ws, actor, issueID, "issue", "History")
	h1 := commitHTTPArtifact(t, svc, ws, actor, history.ID, nil, "hist-v1", "history.md", "text/markdown", []byte("first\n"))
	h1ID := h1.RevisionID
	h2 := commitHTTPArtifact(t, svc, ws, actor, history.ID, &h1ID, "hist-v2", "history.md", "text/markdown", []byte("second\n"))
	staleRestore := editJSON(t, router, http.MethodPost, "/api/artifacts/"+history.ID.String()+"/restore", member, map[string]any{
		"revision_id":      h1.RevisionID.String(),
		"base_revision_id": h1.RevisionID.String(),
	})
	if code := errorCodeOf(t, staleRestore); staleRestore.Code != http.StatusConflict || code != artifact.CodeRevisionConflict {
		t.Fatalf("stale restore = %d %s", staleRestore.Code, staleRestore.Body.String())
	}
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM artifact_revisions WHERE artifact_id = $1`, history.ID).Scan(&revisions); err != nil || revisions != 2 {
		t.Fatalf("stale restore count = %d, err %v", revisions, err)
	}
	restored := editJSON(t, router, http.MethodPost, "/api/artifacts/"+history.ID.String()+"/restore", member, map[string]any{
		"revision_id":      h1.RevisionID.String(),
		"base_revision_id": h2.RevisionID.String(),
	})
	if restored.Code != http.StatusOK {
		t.Fatalf("restore = %d %s", restored.Code, restored.Body.String())
	}
	var restoredBody artifactCommandResponse
	if err := json.Unmarshal(restored.Body.Bytes(), &restoredBody); err != nil {
		t.Fatal(err)
	}
	if restoredBody.RevisionNo != 3 || restoredBody.RestoredFromRevisionID != h1.RevisionID.String() {
		t.Fatalf("restore body = %+v", restoredBody)
	}
	if got := artifactGET(t, router, "/api/artifacts/"+history.ID.String()+"/revisions/"+restoredBody.RevisionID+"/content", testUserID, ""); got.Body.String() != "first\n" {
		t.Fatalf("restored bytes = %q", got.Body.String())
	}
	if got := artifactGET(t, router, "/api/artifacts/"+history.ID.String()+"/revisions/"+h2.RevisionID.String()+"/content", testUserID, ""); got.Body.String() != "second\n" {
		t.Fatalf("v2 changed = %q", got.Body.String())
	}
	var source, from string
	if err := testPool.QueryRow(context.Background(), `SELECT source, restored_from_revision_id FROM artifact_revisions WHERE id = $1`, restoredBody.RevisionID).Scan(&source, &from); err != nil || source != "restore" || from != h1.RevisionID.String() {
		t.Fatalf("restore row = %s %s, err %v", source, from, err)
	}

	anon := editJSON(t, router, http.MethodPost, "/api/artifacts/"+history.ID.String()+"/restore", nil, map[string]any{
		"revision_id": h1.RevisionID.String(), "base_revision_id": h2.RevisionID.String(),
	})
	if anon.Code != http.StatusUnauthorized {
		t.Fatalf("anon restore = %d", anon.Code)
	}
	stranger := editJSON(t, router, http.MethodPost, "/api/artifacts/"+history.ID.String()+"/restore", map[string]string{
		"X-User-ID": uuid.NewString(), "X-Workspace-ID": testWorkspaceID,
	}, map[string]any{"revision_id": h1.RevisionID.String(), "base_revision_id": restoredBody.RevisionID})
	if stranger.Code != http.StatusNotFound {
		t.Fatalf("non-member restore = %d %s", stranger.Code, stranger.Body.String())
	}
	wrongTask := editJSON(t, router, http.MethodPost, "/api/artifacts/"+history.ID.String()+"/restore", taskHeaders(taskID, agentID), map[string]any{
		"revision_id": h1.RevisionID.String(), "base_revision_id": restoredBody.RevisionID,
	})
	if wrongTask.Code != http.StatusNotFound {
		t.Fatalf("wrong task restore = %d %s", wrongTask.Code, wrongTask.Body.String())
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE artifacts SET status = 'deleted', deleted_at = now() WHERE id = $1`, history.ID); err != nil {
		t.Fatal(err)
	}
	deleted := editJSON(t, router, http.MethodPost, "/api/artifacts/"+history.ID.String()+"/restore", member, map[string]any{
		"revision_id": h1.RevisionID.String(), "base_revision_id": restoredBody.RevisionID,
	})
	if deleted.Code != http.StatusNotFound {
		t.Fatalf("deleted restore = %d %s", deleted.Code, deleted.Body.String())
	}

	var created, committed, preview int
	if err := testPool.QueryRow(context.Background(), `
		SELECT
			count(*) FILTER (WHERE event_type = 'artifact.created'),
			count(*) FILTER (WHERE event_type = 'artifact.revision.committed'),
			count(*) FILTER (WHERE event_type = 'artifact.preview.ready')
		FROM artifact_events WHERE artifact_id = $1`, leased.ID).Scan(&created, &committed, &preview); err != nil {
		t.Fatal(err)
	}
	if created != 1 || committed < 1 || preview != committed {
		t.Fatalf("events created=%d committed=%d preview=%d", created, committed, preview)
	}
	var secretCols int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'artifact_events'
		  AND column_name IN ('storage_key', 'payload', 'body', 'sha256')`).Scan(&secretCols); err != nil || secretCols != 0 {
		t.Fatalf("event secret columns = %d, err %v", secretCols, err)
	}
	var eventID uuid.UUID
	if err := testPool.QueryRow(context.Background(), `SELECT id FROM artifact_events WHERE artifact_id = $1 AND event_type = 'artifact.created'`, leased.ID).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	first, err := svc.ApplyEvent(context.Background(), eventID)
	if err != nil || !first {
		t.Fatalf("first apply = %v, err %v", first, err)
	}
	second, err := svc.ApplyEvent(context.Background(), eventID)
	if err != nil || second {
		t.Fatalf("duplicate apply = %v, err %v", second, err)
	}
	var receipts int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM artifact_event_receipts WHERE event_id = $1`, eventID).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("receipts = %d, err %v", receipts, err)
	}
}

func newArtifactConcurrencyRouter() http.Handler {
	r := chi.NewRouter()
	r.Post("/api/artifacts/{artifactID}/revisions", testHandler.UpdateArtifact)
	r.Post("/api/artifacts/{artifactID}/restore", testHandler.RestoreArtifact)
	r.Post("/api/artifacts/{artifactID}/edit-sessions", testHandler.CreateArtifactEditSession)
	r.Post("/api/artifact-edit-sessions/{sessionID}/heartbeat", testHandler.HeartbeatArtifactEditSession)
	r.Put("/api/artifact-edit-sessions/{sessionID}/draft", testHandler.SaveArtifactEditDraft)
	r.Get("/api/artifacts/{artifactID}/revisions/{revisionID}/content", testHandler.GetArtifactRevisionContent)
	return r
}
