package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/artifact"
	"github.com/multica-ai/multica/server/internal/featureflags"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestArtifactTrashHidesContentUntilRestore(t *testing.T) {
	if testPool == nil {
		t.Skip("test database not available")
	}
	store := &mockStorage{}
	orig := testHandler.Storage
	testHandler.Storage = store
	t.Cleanup(func() { testHandler.Storage = orig })
	withFeatureFlag(t, testHandler, featureflags.ArtifactsV1, true)

	ws := mustUUID(t, testWorkspaceID)
	actor := mustUUID(t, testUserID)
	t.Cleanup(func() {
		_ = db.New(testPool).DeleteWorkspaceArtifacts(context.Background(), artifactPG(ws))
	})
	svc := &artifact.Service{DB: testPool, Store: store, Scanner: artifact.NopScanner{}}
	created := createHTTPArtifact(t, svc, ws, actor, uuid.New(), "issue", "Trash page")
	body := []byte("trash-page")
	committed := commitHTTPArtifact(t, svc, ws, actor, created.ID, nil, "trash-http", "note.md", "text/markdown", body)
	published, err := svc.Publish(context.Background(), artifact.PublishInput{
		WorkspaceID: ws,
		ActorID:     actor,
		ArtifactID:  created.ID,
		RevisionID:  committed.RevisionID,
	})
	if err != nil {
		t.Fatal(err)
	}

	router := chi.NewRouter()
	MountArtifactRoutes(router, testHandler)
	shares := chi.NewRouter()
	MountArtifactShareRoutes(shares, testHandler)

	anon := httptest.NewRequest(http.MethodPost, "/api/artifacts/"+created.ID.String()+"/trash", nil)
	anonRec := httptest.NewRecorder()
	router.ServeHTTP(anonRec, anon)
	if anonRec.Code != http.StatusUnauthorized {
		t.Fatalf("anon status = %d", anonRec.Code)
	}

	trash := httptest.NewRequest(http.MethodPost, "/api/artifacts/"+created.ID.String()+"/trash", nil)
	trash.Header.Set("X-User-ID", testUserID)
	trash.Header.Set("X-Workspace-ID", testWorkspaceID)
	trashRec := httptest.NewRecorder()
	router.ServeHTTP(trashRec, trash)
	if trashRec.Code != http.StatusNoContent {
		t.Fatalf("trash status = %d body %s", trashRec.Code, trashRec.Body.String())
	}

	content := artifactGET(t, router, "/api/artifacts/"+created.ID.String()+"/revisions/"+committed.RevisionID.String()+"/content", testUserID, testWorkspaceID)
	if content.Code != http.StatusNotFound {
		t.Fatalf("content after trash = %d", content.Code)
	}
	list := artifactGET(t, router, "/api/artifacts?owner_scope=issue&owner_id="+created.OwnerID.String(), testUserID, testWorkspaceID)
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), created.ID.String()) {
		t.Fatalf("list after trash = %d %s", list.Code, list.Body.String())
	}
	share := httptest.NewRequest(http.MethodGet, "/api/artifact-shares/"+published.Token+"/content", nil)
	share.Header.Set("Cookie", "session=main-app-secret")
	shareRec := httptest.NewRecorder()
	shares.ServeHTTP(shareRec, share)
	if shareRec.Code != http.StatusNotFound || strings.Contains(shareRec.Body.String(), "trash-page") {
		t.Fatalf("share after trash = %d %s", shareRec.Code, shareRec.Body.String())
	}

	restore := httptest.NewRequest(http.MethodPost, "/api/artifacts/"+created.ID.String()+"/trash/restore", nil)
	restore.Header.Set("X-User-ID", testUserID)
	restore.Header.Set("X-Workspace-ID", testWorkspaceID)
	restoreRec := httptest.NewRecorder()
	router.ServeHTTP(restoreRec, restore)
	if restoreRec.Code != http.StatusNoContent {
		t.Fatalf("restore status = %d body %s", restoreRec.Code, restoreRec.Body.String())
	}
	again := artifactGET(t, router, "/api/artifacts/"+created.ID.String()+"/revisions/"+committed.RevisionID.String()+"/content", testUserID, testWorkspaceID)
	if again.Code != http.StatusOK || again.Body.String() != string(body) {
		t.Fatalf("content after restore = %d %q", again.Code, again.Body.String())
	}
	shareRec = httptest.NewRecorder()
	shares.ServeHTTP(shareRec, share)
	if shareRec.Code != http.StatusNotFound {
		t.Fatalf("share after restore = %d", shareRec.Code)
	}

	ops := artifactGET(t, router, "/api/artifact-ops", testUserID, testWorkspaceID)
	if ops.Code != http.StatusOK {
		t.Fatalf("ops status = %d %s", ops.Code, ops.Body.String())
	}
	var health struct {
		RetentionClass string `json:"retention_class"`
		DriftBytes     int64  `json:"drift_bytes"`
		GCPaused       bool   `json:"gc_paused"`
	}
	if err := json.Unmarshal(ops.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if health.RetentionClass != "ops_default" || health.DriftBytes != 0 || health.GCPaused {
		t.Fatalf("ops = %+v", health)
	}
	if strings.Contains(ops.Body.String(), "storage_key") || strings.Contains(ops.Body.String(), committed.StorageKey) {
		t.Fatalf("ops leaked storage: %s", ops.Body.String())
	}
}
