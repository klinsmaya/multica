package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/artifact"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestArtifactReadRequiresMembershipAndReturnsCommittedBytes(t *testing.T) {
	if testPool == nil {
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
	owner := uuid.New()
	body := []byte("# report v1\n")
	created := createHTTPArtifact(t, svc, ws, actor, owner, "issue", "Weekly report")
	committed := commitHTTPArtifact(t, svc, ws, actor, created.ID, nil, "read-v1", "report.md", "text/markdown", body)

	router := newArtifactReadRouter()
	list := artifactGET(t, router, "/api/artifacts?owner_scope=issue&owner_id="+owner.String(), testUserID, testWorkspaceID)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, body %s", list.Code, list.Body.String())
	}
	if strings.Contains(list.Body.String(), "storage_key") || strings.Contains(list.Body.String(), "staging_key") || strings.Contains(list.Body.String(), "workspaces/") {
		t.Fatalf("list leaked storage: %s", list.Body.String())
	}
	var listed struct {
		Artifacts []artifactResponse `json:"artifacts"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Artifacts) != 1 || listed.Artifacts[0].Title != "Weekly report" {
		t.Fatalf("list = %+v", listed.Artifacts)
	}
	if listed.Artifacts[0].RevisionNo == nil || *listed.Artifacts[0].RevisionNo != 1 {
		t.Fatalf("revision = %+v", listed.Artifacts[0].RevisionNo)
	}
	if listed.Artifacts[0].Filename != "report.md" || listed.Artifacts[0].UpdatedBy != actor.String() {
		t.Fatalf("metadata = %+v", listed.Artifacts[0])
	}

	meta := artifactGET(t, router, "/api/artifacts/"+created.ID.String(), testUserID, "")
	if meta.Code != http.StatusOK {
		t.Fatalf("meta status = %d, body %s", meta.Code, meta.Body.String())
	}
	if strings.Contains(meta.Body.String(), "storage_key") || strings.Contains(meta.Body.String(), committed.StorageKey) {
		t.Fatalf("metadata leaked storage: %s", meta.Body.String())
	}

	revs := artifactGET(t, router, "/api/artifacts/"+created.ID.String()+"/revisions", testUserID, "")
	if revs.Code != http.StatusOK || !strings.Contains(revs.Body.String(), committed.RevisionID.String()) {
		t.Fatalf("revisions status = %d, body %s", revs.Code, revs.Body.String())
	}

	content := artifactGET(t, router, "/api/artifacts/"+created.ID.String()+"/revisions/"+committed.RevisionID.String()+"/content", testUserID, "")
	if content.Code != http.StatusOK {
		t.Fatalf("content status = %d, body %s", content.Code, content.Body.String())
	}
	if content.Body.String() != string(body) {
		t.Fatalf("content = %q", content.Body.String())
	}
	if !strings.Contains(content.Header().Get("Content-Disposition"), "report.md") {
		t.Fatalf("disposition = %q", content.Header().Get("Content-Disposition"))
	}
	if content.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("cache = %q", content.Header().Get("Cache-Control"))
	}

	ranged := httptest.NewRequest(http.MethodGet, "/api/artifacts/"+created.ID.String()+"/revisions/"+committed.RevisionID.String()+"/content", nil)
	ranged.Header.Set("X-User-ID", testUserID)
	ranged.Header.Set("Range", "bytes=0-3")
	rw := httptest.NewRecorder()
	router.ServeHTTP(rw, ranged)
	if rw.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d, body %s", rw.Code, rw.Body.String())
	}
	if rw.Body.String() != string(body[:4]) {
		t.Fatalf("range body = %q", rw.Body.String())
	}

	missing := artifactGET(t, router, "/api/artifacts/"+uuid.NewString(), testUserID, "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d", missing.Code)
	}
	anon := artifactGET(t, router, "/api/artifacts/"+created.ID.String(), "", "")
	if anon.Code != http.StatusUnauthorized {
		t.Fatalf("anon status = %d", anon.Code)
	}
}

func TestArtifactReadHidesForeignWorkspaceEvenWhenLinked(t *testing.T) {
	if testPool == nil {
		t.Skip("test database not available")
	}
	store := &mockStorage{}
	orig := testHandler.Storage
	testHandler.Storage = store
	t.Cleanup(func() { testHandler.Storage = orig })

	foreign := uuid.New()
	actor := mustUUID(t, testUserID)
	t.Cleanup(func() {
		_ = db.New(testPool).DeleteWorkspaceArtifacts(context.Background(), artifactPG(foreign))
	})
	svc := &artifact.Service{DB: testPool, Store: store, Scanner: artifact.NopScanner{}}
	secret := []byte("secret-bytes")
	created := createHTTPArtifact(t, svc, foreign, actor, uuid.New(), "issue", "Foreign report")
	committed := commitHTTPArtifact(t, svc, foreign, actor, created.ID, nil, "foreign-v1", "secret.txt", "text/plain", secret)

	linkTarget := uuid.New()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO artifact_links (artifact_id, revision_id, target_type, target_id, relation_type, created_by)
		VALUES ($1, $2, 'issue', $3, 'attached_to', $4)
	`, created.ID, committed.RevisionID, linkTarget, actor); err != nil {
		t.Fatalf("link: %v", err)
	}

	router := newArtifactReadRouter()
	got := artifactGET(t, router, "/api/artifacts/"+created.ID.String()+"/revisions/"+committed.RevisionID.String()+"/content", testUserID, "")
	if got.Code != http.StatusNotFound {
		t.Fatalf("foreign content status = %d, body %s", got.Code, got.Body.String())
	}
	if strings.Contains(got.Body.String(), "secret-bytes") {
		t.Fatal("foreign content leaked bytes")
	}
	meta := artifactGET(t, router, "/api/artifacts/"+created.ID.String(), testUserID, "")
	if meta.Code != http.StatusNotFound {
		t.Fatalf("foreign meta status = %d", meta.Code)
	}
	list := artifactGET(t, router, "/api/artifacts?owner_scope=issue&owner_id="+linkTarget.String(), testUserID, testWorkspaceID)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, body %s", list.Code, list.Body.String())
	}
	if strings.Contains(list.Body.String(), "Foreign report") || strings.Contains(list.Body.String(), created.ID.String()) {
		t.Fatalf("link granted a list hit: %s", list.Body.String())
	}
	denied := artifactGET(t, router, "/api/artifacts?owner_scope=issue&owner_id="+linkTarget.String(), testUserID, foreign.String())
	if denied.Code != http.StatusNotFound {
		t.Fatalf("non-member list status = %d, body %s", denied.Code, denied.Body.String())
	}
}

func TestArtifactHTMLPreviewDoesNotServeActiveContent(t *testing.T) {
	if testPool == nil {
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
	body := []byte("<!DOCTYPE html><script>alert(1)</script>")
	created := createHTTPArtifact(t, svc, ws, actor, uuid.New(), "task", "Page")
	committed := commitHTTPArtifact(t, svc, ws, actor, created.ID, nil, "html-v1", "page.html", "text/html", body)

	router := newArtifactReadRouter()
	preview := artifactGET(t, router, "/api/artifacts/"+created.ID.String()+"/revisions/"+committed.RevisionID.String()+"/preview", testUserID, "")
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body %s", preview.Code, preview.Body.String())
	}
	if ct := preview.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("preview content-type = %q", ct)
	}
	if strings.Contains(preview.Body.String(), "<script") || strings.Contains(preview.Body.String(), "<!DOCTYPE") {
		t.Fatalf("preview returned the document: %s", preview.Body.String())
	}
	var entry artifactPreviewResponse
	if err := json.Unmarshal(preview.Body.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.PreviewKind != "html" || entry.ContentDisposition != "attachment" || entry.ActiveContent {
		t.Fatalf("preview = %+v", entry)
	}
	if entry.StaticPreviewPath == "" || strings.Contains(entry.StaticPreviewPath, "workspaces/") {
		t.Fatalf("static preview path = %s", entry.StaticPreviewPath)
	}
	if strings.Contains(entry.ContentPath, "workspaces/") || strings.Contains(entry.ContentPath, committed.StorageKey) {
		t.Fatalf("content path = %s", entry.ContentPath)
	}

	content := artifactGET(t, router, entry.ContentPath, testUserID, "")
	if content.Code != http.StatusOK {
		t.Fatalf("content status = %d, body %s", content.Code, content.Body.String())
	}
	if got := content.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("content-type = %q", got)
	}
	if !strings.Contains(strings.ToLower(content.Header().Get("Content-Disposition")), "attachment") {
		t.Fatalf("disposition = %q", content.Header().Get("Content-Disposition"))
	}
	csp := content.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "sandbox") || !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("csp = %q", csp)
	}
	if content.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("nosniff = %q", content.Header().Get("X-Content-Type-Options"))
	}
	if content.Body.String() != string(body) {
		t.Fatalf("download body = %q", content.Body.String())
	}
}

func TestClassifyArtifactPreview(t *testing.T) {
	cases := []struct {
		media, name, kind string
	}{
		{"text/markdown", "a.md", "markdown"},
		{"text/plain", "notes.txt", "text"},
		{"text/csv", "rows.csv", "table"},
		{"application/pdf", "a.pdf", "pdf"},
		{"image/png", "a.png", "image"},
		{"text/html", "page.html", "download"},
		{"image/svg+xml", "pic.svg", "download"},
		{"text/plain", "pic.svg", "download"},
		{"application/octet-stream", "deck.pptx", "download"},
	}
	for _, tc := range cases {
		kind, _ := classifyArtifactPreview(tc.media, tc.name)
		if kind != tc.kind {
			t.Errorf("%s %s -> %s, want %s", tc.media, tc.name, kind, tc.kind)
		}
	}
}

func newArtifactReadRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/api/artifacts", testHandler.ListArtifacts)
	r.Get("/api/artifacts/{artifactID}", testHandler.GetArtifact)
	r.Get("/api/artifacts/{artifactID}/revisions", testHandler.ListArtifactRevisions)
	r.Get("/api/artifacts/{artifactID}/revisions/{revisionID}/preview", testHandler.GetArtifactRevisionPreview)
	r.Get("/api/artifacts/{artifactID}/revisions/{revisionID}/static-preview", testHandler.GetArtifactStaticPreview)
	r.Get("/api/artifacts/{artifactID}/revisions/{revisionID}/resources/*", testHandler.GetArtifactPreviewResource)
	r.Get("/api/artifacts/{artifactID}/revisions/{revisionID}/content", testHandler.GetArtifactRevisionContent)
	return r
}

func artifactGET(t *testing.T, router http.Handler, path, userID, workspaceID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if userID != "" {
		req.Header.Set("X-User-ID", userID)
	}
	if workspaceID != "" {
		req.Header.Set("X-Workspace-ID", workspaceID)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func createHTTPArtifact(t *testing.T, svc *artifact.Service, ws, actor, owner uuid.UUID, scope, title string) artifact.Artifact {
	t.Helper()
	created, err := svc.Create(context.Background(), artifact.CreateInput{
		WorkspaceID: ws,
		OwnerScope:  scope,
		OwnerID:     owner,
		Title:       title,
		CreatedBy:   actor,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return created
}

func commitHTTPArtifact(t *testing.T, svc *artifact.Service, ws, actor, artifactID uuid.UUID, base *uuid.UUID, key, filename, media string, body []byte) artifact.CommitResult {
	t.Helper()
	sum := sha256.Sum256(body)
	got, err := svc.Commit(context.Background(), artifact.CommitInput{
		WorkspaceID:    ws,
		ActorID:        actor,
		ArtifactID:     artifactID,
		BaseRevisionID: base,
		IdempotencyKey: key,
		Filename:       filename,
		MediaType:      media,
		SHA256:         hex.EncodeToString(sum[:]),
		Source:         "agent",
		Data:           body,
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	return got
}

func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func artifactPG(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}
