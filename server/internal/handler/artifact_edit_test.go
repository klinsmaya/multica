package handler

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/artifact"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestArtifactEditSessionLeaseDraftAndCommit(t *testing.T) {
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
	created := createHTTPArtifact(t, svc, ws, actor, uuid.New(), "issue", "Weekly report")
	v1 := commitHTTPArtifact(t, svc, ws, actor, created.ID, nil, "edit-v1", "report.md", "text/markdown", []byte("# report v1\n"))
	router := newArtifactEditRouter()
	member := map[string]string{"X-User-ID": testUserID, "X-Workspace-ID": testWorkspaceID}

	opened := editJSON(t, router, http.MethodPost, "/api/artifacts/"+created.ID.String()+"/edit-sessions", member, nil)
	if opened.Code != http.StatusCreated {
		t.Fatalf("create session = %d %s", opened.Code, opened.Body.String())
	}
	var session editSessionResponse
	if err := json.Unmarshal(opened.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if !session.Editable || session.LeaseToken == "" || session.FencingToken < 1 || session.BaseRevisionID != v1.RevisionID.String() || session.EditorType != "human" {
		t.Fatalf("session = %+v", session)
	}
	if strings.Contains(opened.Body.String(), "artifact-drafts") || strings.Contains(opened.Body.String(), "storage_key") {
		t.Fatalf("session leaked storage: %s", opened.Body.String())
	}

	_, _ = testPool.Exec(context.Background(), `UPDATE artifact_edit_sessions SET lease_expires_at = now() + interval '5 seconds' WHERE id = $1`, session.EditSessionID)
	beat := editJSON(t, router, http.MethodPost, "/api/artifact-edit-sessions/"+session.EditSessionID+"/heartbeat", member, map[string]any{
		"lease_token":   session.LeaseToken,
		"fencing_token": session.FencingToken,
	})
	if beat.Code != http.StatusOK {
		t.Fatalf("heartbeat = %d %s", beat.Code, beat.Body.String())
	}
	var extended bool
	if err := testPool.QueryRow(context.Background(), `SELECT lease_expires_at > now() + interval '30 seconds' FROM artifact_edit_sessions WHERE id = $1`, session.EditSessionID).Scan(&extended); err != nil || !extended {
		t.Fatalf("lease extended = %v, err %v", extended, err)
	}

	draft := editJSON(t, router, http.MethodPut, "/api/artifact-edit-sessions/"+session.EditSessionID+"/draft", member, map[string]any{
		"lease_token":   session.LeaseToken,
		"fencing_token": session.FencingToken,
		"content":       "keep me\n",
	})
	if draft.Code != http.StatusOK {
		t.Fatalf("draft = %d %s", draft.Code, draft.Body.String())
	}
	body := artifactGET(t, router, "/api/artifacts/"+created.ID.String()+"/revisions/"+v1.RevisionID.String()+"/content", testUserID, "")
	if body.Body.String() != "# report v1\n" {
		t.Fatalf("draft changed the revision: %q", body.Body.String())
	}

	_, _ = testPool.Exec(context.Background(), `UPDATE artifact_edit_sessions SET lease_expires_at = now() - interval '1 minute' WHERE id = $1`, session.EditSessionID)
	staleBeat := editJSON(t, router, http.MethodPost, "/api/artifact-edit-sessions/"+session.EditSessionID+"/heartbeat", member, map[string]any{
		"lease_token":   session.LeaseToken,
		"fencing_token": session.FencingToken,
	})
	if code := errorCodeOf(t, staleBeat); staleBeat.Code != http.StatusConflict || code != artifact.CodeLeaseExpired {
		t.Fatalf("expired heartbeat = %d %s", staleBeat.Code, staleBeat.Body.String())
	}
	staleCommit := postArtifact(t, router, "/api/artifacts/"+created.ID.String()+"/revisions", member, map[string]string{
		"base_revision_id": v1.RevisionID.String(),
		"edit_session_id":  session.EditSessionID,
		"fencing_token":    itoa(session.FencingToken),
		"lease_token":      session.LeaseToken,
	}, "report.md", []byte("# stolen\n"))
	if code := errorCodeOf(t, staleCommit); staleCommit.Code != http.StatusConflict || code != artifact.CodeLeaseExpired {
		t.Fatalf("expired commit = %d %s", staleCommit.Code, staleCommit.Body.String())
	}
	var draftRef string
	if err := testPool.QueryRow(context.Background(), `SELECT draft_ref FROM artifact_edit_sessions WHERE id = $1`, session.EditSessionID).Scan(&draftRef); err != nil || draftRef == "" {
		t.Fatalf("expired draft_ref = %q, err %v", draftRef, err)
	}

	reopened := editJSON(t, router, http.MethodPost, "/api/artifacts/"+created.ID.String()+"/edit-sessions", member, nil)
	if reopened.Code != http.StatusCreated {
		t.Fatalf("reopen = %d %s", reopened.Code, reopened.Body.String())
	}
	var again editSessionResponse
	if err := json.Unmarshal(reopened.Body.Bytes(), &again); err != nil {
		t.Fatal(err)
	}
	if again.FencingToken <= session.FencingToken || again.BaseRevisionID != v1.RevisionID.String() || again.DraftContent != "keep me\n" || again.LeaseToken == "" {
		t.Fatalf("reopened = %+v", again)
	}

	mismatch := postArtifact(t, router, "/api/artifacts/"+created.ID.String()+"/revisions", withHeader(member, "If-Match", `"not-the-base"`), map[string]string{
		"base_revision_id": v1.RevisionID.String(),
		"edit_session_id":  again.EditSessionID,
		"fencing_token":    itoa(again.FencingToken),
		"lease_token":      again.LeaseToken,
	}, "report.md", []byte("# report v2\n"))
	if code := errorCodeOf(t, mismatch); mismatch.Code != http.StatusConflict || code != artifact.CodeRevisionConflict {
		t.Fatalf("if-match = %d %s", mismatch.Code, mismatch.Body.String())
	}
	if got := artifactGET(t, router, "/api/artifacts/"+created.ID.String()+"/revisions/"+v1.RevisionID.String()+"/content", testUserID, ""); got.Body.String() != "# report v1\n" {
		t.Fatalf("if-match wrote bytes: %q", got.Body.String())
	}

	saved := postArtifact(t, router, "/api/artifacts/"+created.ID.String()+"/revisions", withHeader(member, "If-Match", `"`+v1.RevisionID.String()+`"`), map[string]string{
		"base_revision_id": v1.RevisionID.String(),
		"edit_session_id":  again.EditSessionID,
		"fencing_token":    itoa(again.FencingToken),
		"lease_token":      again.LeaseToken,
	}, "report.md", []byte("# report v2\n"))
	if saved.Code != http.StatusOK {
		t.Fatalf("commit = %d %s", saved.Code, saved.Body.String())
	}
	var committed artifactCommandResponse
	if err := json.Unmarshal(saved.Body.Bytes(), &committed); err != nil {
		t.Fatal(err)
	}
	if committed.State != "committed" || committed.RevisionID == "" || committed.RevisionNo != 2 {
		t.Fatalf("commit body = %+v", committed)
	}
	if got := artifactGET(t, router, "/api/artifacts/"+created.ID.String()+"/revisions/"+committed.RevisionID+"/content", testUserID, ""); got.Body.String() != "# report v2\n" {
		t.Fatalf("v2 = %q", got.Body.String())
	}
	var source, sessionState string
	if err := testPool.QueryRow(context.Background(), `SELECT source FROM artifact_revisions WHERE id = $1`, committed.RevisionID).Scan(&source); err != nil || source != "human_edit" {
		t.Fatalf("source = %q, err %v", source, err)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT state FROM artifact_edit_sessions WHERE id = $1`, again.EditSessionID).Scan(&sessionState); err != nil || sessionState != "committed" {
		t.Fatalf("session state = %q, err %v", sessionState, err)
	}
	spent := editJSON(t, router, http.MethodPost, "/api/artifact-edit-sessions/"+again.EditSessionID+"/heartbeat", member, map[string]any{
		"lease_token":   again.LeaseToken,
		"fencing_token": again.FencingToken,
	})
	if code := errorCodeOf(t, spent); spent.Code != http.StatusConflict || code != artifact.CodeLeaseExpired {
		t.Fatalf("spent fencing = %d %s", spent.Code, spent.Body.String())
	}

	conflictSession := mustOpenEdit(t, router, created.ID.String(), member)
	draft = editJSON(t, router, http.MethodPut, "/api/artifact-edit-sessions/"+conflictSession.EditSessionID+"/draft", member, map[string]any{
		"lease_token":   conflictSession.LeaseToken,
		"fencing_token": conflictSession.FencingToken,
		"content":       "still here\n",
	})
	if draft.Code != http.StatusOK {
		t.Fatalf("conflict draft = %d %s", draft.Code, draft.Body.String())
	}
	moved := v1.RevisionID
	if _, err := testPool.Exec(context.Background(), `UPDATE artifacts SET head_revision_id = $2 WHERE id = $1`, created.ID, moved); err != nil {
		t.Fatal(err)
	}
	conflict := postArtifact(t, router, "/api/artifacts/"+created.ID.String()+"/revisions", member, map[string]string{
		"base_revision_id": conflictSession.BaseRevisionID,
		"edit_session_id":  conflictSession.EditSessionID,
		"fencing_token":    itoa(conflictSession.FencingToken),
		"lease_token":      conflictSession.LeaseToken,
	}, "report.md", []byte("# conflict\n"))
	if code := errorCodeOf(t, conflict); conflict.Code != http.StatusConflict || code != artifact.CodeRevisionConflict {
		t.Fatalf("stale base = %d %s", conflict.Code, conflict.Body.String())
	}
	var uploadState, conflictState, conflictDraft string
	if err := testPool.QueryRow(context.Background(), `SELECT state FROM artifact_uploads WHERE artifact_id = $1 AND state = 'conflict_draft'`, created.ID).Scan(&uploadState); err != nil || uploadState != "conflict_draft" {
		t.Fatalf("conflict_draft = %q, err %v", uploadState, err)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT state, draft_ref FROM artifact_edit_sessions WHERE id = $1`, conflictSession.EditSessionID).Scan(&conflictState, &conflictDraft); err != nil || conflictState != "expired" || conflictDraft == "" {
		t.Fatalf("conflict session = %s %q, err %v", conflictState, conflictDraft, err)
	}
	reader, err := store.GetReader(context.Background(), conflictDraft)
	if err != nil {
		t.Fatal(err)
	}
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(reader)
	_ = reader.Close()
	if buf.String() != "still here\n" {
		t.Fatalf("conflict draft bytes = %q", buf.String())
	}
	old := postArtifact(t, router, "/api/artifacts/"+created.ID.String()+"/revisions", member, map[string]string{
		"base_revision_id": conflictSession.BaseRevisionID,
		"edit_session_id":  conflictSession.EditSessionID,
		"fencing_token":    itoa(conflictSession.FencingToken),
		"lease_token":      conflictSession.LeaseToken,
	}, "report.md", []byte("# again\n"))
	if code := errorCodeOf(t, old); old.Code != http.StatusConflict || code != artifact.CodeLeaseExpired {
		t.Fatalf("old fencing after conflict = %d %s", old.Code, old.Body.String())
	}
	restored := mustOpenEdit(t, router, created.ID.String(), member)
	if restored.BaseRevisionID != moved.String() || restored.DraftContent != "still here\n" || restored.FencingToken <= conflictSession.FencingToken {
		t.Fatalf("restored = %+v, moved %s", restored, moved)
	}

	other := dbfx.Insert(t, "user", testutil.Cols{"name": "Other Editor", "email": "edit-other-" + uuid.NewString() + "@example.test"})
	dbfx.Member(t, testWorkspaceID, other, "member")
	held := editJSON(t, router, http.MethodPost, "/api/artifacts/"+created.ID.String()+"/edit-sessions", map[string]string{
		"X-User-ID": other, "X-Workspace-ID": testWorkspaceID,
	}, nil)
	if code := errorCodeOf(t, held); held.Code != http.StatusConflict || code != artifact.CodeLeaseHeld {
		t.Fatalf("second editor = %d %s", held.Code, held.Body.String())
	}
	anon := editJSON(t, router, http.MethodPost, "/api/artifacts/"+created.ID.String()+"/edit-sessions", nil, nil)
	if anon.Code != http.StatusUnauthorized {
		t.Fatalf("anon = %d", anon.Code)
	}
	stranger := editJSON(t, router, http.MethodPost, "/api/artifacts/"+created.ID.String()+"/edit-sessions", map[string]string{
		"X-User-ID": uuid.NewString(), "X-Workspace-ID": testWorkspaceID,
	}, nil)
	if stranger.Code != http.StatusNotFound {
		t.Fatalf("non-member = %d %s", stranger.Code, stranger.Body.String())
	}
	agentID := dbfx.Agent(t, "edit-outsider", testRuntimeID)
	taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": testRuntimeID})
	wrongTask := editJSON(t, router, http.MethodPost, "/api/artifacts/"+created.ID.String()+"/edit-sessions", taskHeaders(taskID, agentID), nil)
	if wrongTask.Code != http.StatusNotFound {
		t.Fatalf("wrong task = %d %s", wrongTask.Code, wrongTask.Body.String())
	}

	htmlArt := createHTTPArtifact(t, svc, ws, actor, uuid.New(), "issue", "Page")
	commitHTTPArtifact(t, svc, ws, actor, htmlArt.ID, nil, "edit-html", "page.html", "text/html", []byte("<!DOCTYPE html><p>hi</p>"))
	htmlSession := editJSON(t, router, http.MethodPost, "/api/artifacts/"+htmlArt.ID.String()+"/edit-sessions", member, nil)
	if code := errorCodeOf(t, htmlSession); htmlSession.Code != http.StatusUnsupportedMediaType || code != artifact.CodeNotEditable {
		t.Fatalf("html session = %d %s", htmlSession.Code, htmlSession.Body.String())
	}
	var sessions int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM artifact_edit_sessions WHERE artifact_id = $1`, htmlArt.ID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("html sessions = %d, err %v", sessions, err)
	}
}

func TestArtifactDocxEditKeepsPartsAndBlocksTheRest(t *testing.T) {
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
	router := newArtifactEditRouter()
	member := map[string]string{"X-User-ID": testUserID, "X-Workspace-ID": testWorkspaceID}
	plain := docxEditPackage(t, map[string]string{
		"word/document.xml":   `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Hello artifact</w:t></w:r></w:p></w:body></w:document>`,
		"customXml/item1.xml": `<root>KEEP-PART</root>`,
	})

	created := createHTTPArtifact(t, svc, ws, actor, uuid.New(), "issue", "Notes")
	v1 := commitHTTPArtifact(t, svc, ws, actor, created.ID, nil, "edit-docx", "notes.docx", artifact.MediaTypeDocx, plain)
	opened := mustOpenEdit(t, router, created.ID.String(), member)
	if opened.BaseRevisionID != v1.RevisionID.String() {
		t.Fatalf("docx session = %+v", opened)
	}
	draft := editJSON(t, router, http.MethodPut, "/api/artifact-edit-sessions/"+opened.EditSessionID+"/draft", member, map[string]any{
		"lease_token":   opened.LeaseToken,
		"fencing_token": opened.FencingToken,
		"content":       "not a docx",
	})
	if draft.Code != http.StatusBadRequest {
		t.Fatalf("docx draft = %d %s", draft.Code, draft.Body.String())
	}
	dropped := docxEditPackage(t, map[string]string{
		"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Hello artifact</w:t></w:r></w:p></w:body></w:document>`,
	})
	rejected := postArtifact(t, router, "/api/artifacts/"+created.ID.String()+"/revisions", member, map[string]string{
		"base_revision_id": opened.BaseRevisionID,
		"edit_session_id":  opened.EditSessionID,
		"fencing_token":    itoa(opened.FencingToken),
		"lease_token":      opened.LeaseToken,
	}, "notes.docx", dropped)
	if code := errorCodeOf(t, rejected); rejected.Code != http.StatusConflict || code != artifact.CodeOfficeFeatureDropped {
		t.Fatalf("dropped part = %d %s", rejected.Code, rejected.Body.String())
	}
	var revisions int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM artifact_revisions WHERE artifact_id = $1`, created.ID).Scan(&revisions); err != nil || revisions != 1 {
		t.Fatalf("revisions after drop = %d, err %v", revisions, err)
	}
	saved := postArtifact(t, router, "/api/artifacts/"+created.ID.String()+"/revisions", member, map[string]string{
		"base_revision_id": opened.BaseRevisionID,
		"edit_session_id":  opened.EditSessionID,
		"fencing_token":    itoa(opened.FencingToken),
		"lease_token":      opened.LeaseToken,
	}, "notes.docx", plain)
	if saved.Code != http.StatusOK {
		t.Fatalf("kept parts = %d %s", saved.Code, saved.Body.String())
	}

	sheet := createHTTPArtifact(t, svc, ws, actor, uuid.New(), "issue", "Budget")
	commitHTTPArtifact(t, svc, ws, actor, sheet.ID, nil, "edit-xlsx", "budget.xlsx", artifact.MediaTypeXlsx, docxEditPackage(t, map[string]string{
		"xl/workbook.xml": `<workbook/>`,
	}))
	xlsxSession := editJSON(t, router, http.MethodPost, "/api/artifacts/"+sheet.ID.String()+"/edit-sessions", member, nil)
	if code := errorCodeOf(t, xlsxSession); xlsxSession.Code != http.StatusUnsupportedMediaType || code != artifact.CodeNotEditable {
		t.Fatalf("xlsx session = %d %s", xlsxSession.Code, xlsxSession.Body.String())
	}

	macro := createHTTPArtifact(t, svc, ws, actor, uuid.New(), "issue", "Macro")
	commitHTTPArtifact(t, svc, ws, actor, macro.ID, nil, "edit-macro", "macro.docx", artifact.MediaTypeDocx, docxEditPackage(t, map[string]string{
		"word/document.xml":   `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"/>`,
		"word/vbaProject.bin": "not-executed",
	}))
	macroSession := editJSON(t, router, http.MethodPost, "/api/artifacts/"+macro.ID.String()+"/edit-sessions", member, nil)
	if code := errorCodeOf(t, macroSession); macroSession.Code != http.StatusUnsupportedMediaType || code != artifact.CodeOfficeFeatureBlocked {
		t.Fatalf("macro session = %d %s", macroSession.Code, macroSession.Body.String())
	}
	var sessions int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM artifact_edit_sessions WHERE artifact_id = $1`, macro.ID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("macro sessions = %d, err %v", sessions, err)
	}
}

func docxEditPackage(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newArtifactEditRouter() http.Handler {
	r := chi.NewRouter()
	r.Post("/api/artifacts/{artifactID}/edit-sessions", testHandler.CreateArtifactEditSession)
	r.Post("/api/artifact-edit-sessions/{sessionID}/heartbeat", testHandler.HeartbeatArtifactEditSession)
	r.Put("/api/artifact-edit-sessions/{sessionID}/draft", testHandler.SaveArtifactEditDraft)
	r.Post("/api/artifacts/{artifactID}/revisions", testHandler.UpdateArtifact)
	r.Get("/api/artifacts/{artifactID}/revisions/{revisionID}/content", testHandler.GetArtifactRevisionContent)
	return r
}

func editJSON(t *testing.T, router http.Handler, method, path string, headers map[string]string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func mustOpenEdit(t *testing.T, router http.Handler, artifactID string, headers map[string]string) editSessionResponse {
	t.Helper()
	rec := editJSON(t, router, http.MethodPost, "/api/artifacts/"+artifactID+"/edit-sessions", headers, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("open session = %d %s", rec.Code, rec.Body.String())
	}
	var session editSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	return session
}

func errorCodeOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Code
}

func withHeader(base map[string]string, key, value string) map[string]string {
	out := make(map[string]string, len(base)+1)
	for k, v := range base {
		out[k] = v
	}
	out[key] = value
	return out
}

func itoa(n int64) string {
	return strconvItoa(n)
}

func strconvItoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
