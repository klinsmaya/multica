package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/artifact"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestArtifactStaticPreviewAndResources(t *testing.T) {
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
	router := newArtifactReadRouter()

	htmlBody := []byte(`<!DOCTYPE html><script>alert(1)</script><p>Hello</p><a href="https://evil.example/phish">away</a><a href="#notes">Notes</a><img src="../secret.png" onerror="alert(1)"><meta http-equiv="Content-Security-Policy" content="script-src *">`)
	page := createHTTPArtifact(t, svc, ws, actor, uuid.New(), "issue", "Page")
	pageRev := commitHTTPArtifact(t, svc, ws, actor, page.ID, nil, "html-v1", "page.html", "text/html", htmlBody)

	preview := artifactGET(t, router, "/api/artifacts/"+page.ID.String()+"/revisions/"+pageRev.RevisionID.String()+"/preview", testUserID, "")
	if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), `"preview_kind":"html"`) {
		t.Fatalf("preview = %d %s", preview.Code, preview.Body.String())
	}
	if strings.Contains(preview.Body.String(), "<script") || strings.Contains(preview.Body.String(), "secret-token") {
		t.Fatalf("preview leaked document or token: %s", preview.Body.String())
	}

	reqPath := "/api/artifacts/" + page.ID.String() + "/revisions/" + pageRev.RevisionID.String() + "/static-preview"
	staticReq := httptest.NewRequest(http.MethodGet, reqPath, nil)
	staticReq.Header.Set("X-User-ID", testUserID)
	staticReq.Header.Set("Cookie", "session=secret-token")
	staticReq.Header.Set("Authorization", "Bearer secret-token")
	static := httptest.NewRecorder()
	router.ServeHTTP(static, staticReq)
	if static.Code != http.StatusOK {
		t.Fatalf("static status = %d body %s", static.Code, static.Body.String())
	}
	if static.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("content-type = %q", static.Header().Get("Content-Type"))
	}
	if static.Header().Get("Content-Security-Policy") != artifact.StaticPreviewCSP {
		t.Fatalf("csp = %q", static.Header().Get("Content-Security-Policy"))
	}
	if strings.Contains(static.Header().Get("Content-Security-Policy"), "allow-scripts") || strings.Contains(static.Header().Get("Content-Security-Policy"), "script-src *") {
		t.Fatal(static.Header().Get("Content-Security-Policy"))
	}
	for _, header := range []struct{ name, want string }{
		{"X-Content-Type-Options", "nosniff"},
		{"Referrer-Policy", "no-referrer"},
		{"Permissions-Policy", artifact.StaticPreviewPermissionsPolicy},
		{"Cache-Control", "private, no-store"},
	} {
		if got := static.Header().Get(header.name); got != header.want {
			t.Fatalf("%s = %q", header.name, got)
		}
	}
	if static.Header().Get("Set-Cookie") != "" {
		t.Fatalf("set-cookie = %q", static.Header().Get("Set-Cookie"))
	}
	body := static.Body.String()
	if !strings.Contains(body, "<p>Hello</p>") || !strings.Contains(body, `href="#notes"`) {
		t.Fatalf("static body = %s", body)
	}
	lower := strings.ToLower(body)
	for _, banned := range []string{"<script", "onerror", "javascript:", "evil.example", "secret-token", "script-src", "default-src", "../"} {
		if strings.Contains(lower, banned) {
			t.Fatalf("static body contains %q: %s", banned, body)
		}
	}

	content := artifactGET(t, router, "/api/artifacts/"+page.ID.String()+"/revisions/"+pageRev.RevisionID.String()+"/content", testUserID, "")
	if content.Header().Get("Content-Type") != "application/octet-stream" || content.Body.String() != string(htmlBody) {
		t.Fatalf("original download changed: %s %q", content.Header().Get("Content-Type"), content.Body.String())
	}

	svgBody := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script><circle cx="10" cy="10" r="4"></circle></svg>`)
	pic := createHTTPArtifact(t, svc, ws, actor, uuid.New(), "issue", "Pic")
	picRev := commitHTTPArtifact(t, svc, ws, actor, pic.ID, nil, "svg-v1", "pic.svg", "image/svg+xml", svgBody)
	svg := artifactGET(t, router, "/api/artifacts/"+pic.ID.String()+"/revisions/"+picRev.RevisionID.String()+"/static-preview", testUserID, "")
	if svg.Code != http.StatusOK || svg.Header().Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("svg = %d %q %s", svg.Code, svg.Header().Get("Content-Type"), svg.Body.String())
	}
	if !strings.Contains(svg.Body.String(), "<circle") || strings.Contains(strings.ToLower(svg.Body.String()), "<script") {
		t.Fatalf("svg body = %s", svg.Body.String())
	}

	resourceBase := "/api/artifacts/" + page.ID.String() + "/revisions/" + pageRev.RevisionID.String() + "/resources/"
	traversal := artifactGET(t, router, resourceBase+"..%2fsecret.png?url=https://evil.example/a.png", testUserID, "")
	if traversal.Code != http.StatusBadRequest {
		t.Fatalf("traversal = %d %s", traversal.Code, traversal.Body.String())
	}
	missing := artifactGET(t, router, resourceBase+"assets/plot.png?url=https://evil.example/a.png", testUserID, "")
	if missing.Code != http.StatusNotFound || strings.Contains(missing.Body.String(), "Hello") || strings.Contains(missing.Body.String(), pageRev.StorageKey) {
		t.Fatalf("missing resource = %d %s", missing.Code, missing.Body.String())
	}
	outsider := artifactGET(t, router, resourceBase+"assets/plot.png", uuid.NewString(), "")
	if outsider.Code != http.StatusNotFound {
		t.Fatalf("outsider = %d %s", outsider.Code, outsider.Body.String())
	}
	anon := artifactGET(t, router, reqPath, "", "")
	if anon.Code != http.StatusUnauthorized {
		t.Fatalf("anon = %d", anon.Code)
	}
}
