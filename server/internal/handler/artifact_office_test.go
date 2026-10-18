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
	"time"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/artifact"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func handlerOfficePackage(files map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			panic(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			panic(err)
		}
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func TestOfficePreviewIsolatesConversionAndKeepsOriginal(t *testing.T) {
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
	body := handlerOfficePackage(map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>Hello artifact</w:t></w:r></w:p>
    <w:p><w:r><w:br w:type="page"/></w:r></w:p>
    <w:p><w:r><w:t>Second page</w:t></w:r></w:p>
  </w:body>
</w:document>`,
	})
	created := createHTTPArtifact(t, svc, ws, actor, uuid.New(), "issue", "Notes")
	committed := commitHTTPArtifact(t, svc, ws, actor, created.ID, nil, "office-v1", "notes.docx", artifact.MediaTypeDocx, body)

	router := newArtifactReadRouter()
	preview := artifactGET(t, router, "/api/artifacts/"+created.ID.String()+"/revisions/"+committed.RevisionID.String()+"/preview", testUserID, "")
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status = %d body %s", preview.Code, preview.Body.String())
	}
	if strings.Contains(preview.Body.String(), "storage_key") || strings.Contains(preview.Body.String(), "artifact-derivatives") {
		t.Fatalf("preview leaked storage: %s", preview.Body.String())
	}
	var entry artifactPreviewResponse
	if err := json.Unmarshal(preview.Body.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.PreviewKind != "docx" || entry.OfficeStatus != artifact.OfficeReady || entry.PageCount != 2 {
		t.Fatalf("preview = %+v", entry)
	}
	if entry.OfficePreviewPath == "" || entry.ActiveContent {
		t.Fatalf("preview path = %+v", entry)
	}
	if !strings.Contains(entry.ContentDisposition, "attachment") {
		t.Fatalf("disposition = %s", entry.ContentDisposition)
	}

	req := httptest.NewRequest(http.MethodGet, entry.OfficePreviewPath, nil)
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("Cookie", "session=office-secret")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("office status = %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Hello artifact") || !strings.Contains(rec.Body.String(), "Second page") {
		t.Fatalf("office body = %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "office-secret") || rec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("cookie leaked: body %s set-cookie %q", rec.Body.String(), rec.Header().Get("Set-Cookie"))
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("nosniff = %q", rec.Header().Get("X-Content-Type-Options"))
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "script-src 'none'") {
		t.Fatalf("csp = %q", rec.Header().Get("Content-Security-Policy"))
	}

	content := artifactGET(t, router, entry.ContentPath, testUserID, "")
	if content.Code != http.StatusOK || !bytes.Equal(content.Body.Bytes(), body) {
		t.Fatalf("original status = %d len %d", content.Code, content.Body.Len())
	}

	again := artifactGET(t, router, "/api/artifacts/"+created.ID.String()+"/revisions/"+committed.RevisionID.String()+"/preview", testUserID, "")
	if again.Code != http.StatusOK {
		t.Fatalf("second preview = %d", again.Code)
	}
	var n int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM artifact_derivatives WHERE revision_id = $1`, committed.RevisionID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("derivative rows = %d", n)
	}
	var expires *time.Time
	if err := testPool.QueryRow(context.Background(), `SELECT expires_at FROM artifact_derivatives WHERE revision_id = $1`, committed.RevisionID).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if expires != nil {
		t.Fatalf("ready derivative expires at %v", expires)
	}

	keys, err := db.New(testPool).ListArtifactStorageKeysByWorkspace(context.Background(), artifactPG(ws))
	if err != nil {
		t.Fatal(err)
	}
	var derivativeKey bool
	for _, key := range keys {
		if strings.Contains(key, "/artifact-derivatives/") {
			derivativeKey = true
		}
	}
	if !derivativeKey {
		t.Fatalf("keys = %v", keys)
	}
	if err := db.New(testPool).DeleteWorkspaceArtifacts(context.Background(), artifactPG(ws)); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM artifact_derivatives WHERE workspace_id = $1`, ws).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("derivative rows left = %d", left)
	}
}

func TestOfficeConversionFailureKeepsOriginalDownload(t *testing.T) {
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
	body := handlerOfficePackage(map[string]string{
		"[Content_Types].xml": "<Types></Types>",
	})
	created := createHTTPArtifact(t, svc, ws, actor, uuid.New(), "issue", "Broken")
	committed := commitHTTPArtifact(t, svc, ws, actor, created.ID, nil, "office-bad", "broken.docx", artifact.MediaTypeDocx, body)
	router := newArtifactReadRouter()
	preview := artifactGET(t, router, "/api/artifacts/"+created.ID.String()+"/revisions/"+committed.RevisionID.String()+"/preview", testUserID, "")
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status = %d body %s", preview.Code, preview.Body.String())
	}
	var entry artifactPreviewResponse
	if err := json.Unmarshal(preview.Body.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.OfficeStatus != artifact.OfficeFailed || entry.OfficeError == "" || entry.OfficePreviewPath != "" {
		t.Fatalf("failed preview = %+v", entry)
	}
	denied := artifactGET(t, router, "/api/artifacts/"+created.ID.String()+"/revisions/"+committed.RevisionID.String()+"/office-preview", testUserID, "")
	if denied.Code != http.StatusNotFound {
		t.Fatalf("failed derivative status = %d", denied.Code)
	}
	content := artifactGET(t, router, entry.ContentPath, testUserID, "")
	if content.Code != http.StatusOK || !bytes.Equal(content.Body.Bytes(), body) {
		t.Fatalf("original after failure status = %d", content.Code)
	}
	var expires *time.Time
	if err := testPool.QueryRow(context.Background(), `SELECT expires_at FROM artifact_derivatives WHERE revision_id = $1`, committed.RevisionID).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if expires == nil || time.Until(*expires) < 6*24*time.Hour || time.Until(*expires) > 8*24*time.Hour {
		t.Fatalf("expires_at = %v", expires)
	}
}

func TestOfficePreviewRequiresMembership(t *testing.T) {
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
	body := handlerOfficePackage(map[string]string{
		"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:t>secret office</w:t></w:document>`,
	})
	created := createHTTPArtifact(t, svc, foreign, actor, uuid.New(), "issue", "Foreign notes")
	committed := commitHTTPArtifact(t, svc, foreign, actor, created.ID, nil, "office-foreign", "secret.docx", artifact.MediaTypeDocx, body)
	router := newArtifactReadRouter()
	path := "/api/artifacts/" + created.ID.String() + "/revisions/" + committed.RevisionID.String() + "/office-preview"
	got := artifactGET(t, router, path, testUserID, "")
	if got.Code != http.StatusNotFound {
		t.Fatalf("non-member status = %d body %s", got.Code, got.Body.String())
	}
	if strings.Contains(got.Body.String(), "secret office") {
		t.Fatal("non-member saw the preview")
	}
	anon := artifactGET(t, router, path, "", "")
	if anon.Code != http.StatusUnauthorized {
		t.Fatalf("anon status = %d", anon.Code)
	}
	content := artifactGET(t, router, "/api/artifacts/"+created.ID.String()+"/revisions/"+committed.RevisionID.String()+"/content", testUserID, "")
	if content.Code != http.StatusNotFound {
		t.Fatalf("non-member content = %d", content.Code)
	}
}
