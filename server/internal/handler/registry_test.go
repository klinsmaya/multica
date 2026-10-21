package handler

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/registry"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
)

const registryTestSkill = "---\nname: pr-review\ndescription: Review a pull request.\n---\n\nRead the diff.\n"

func registryTestArchive(t *testing.T, version string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range map[string]string{
		"pr-helper/.claude-plugin/plugin.json": `{"name":"pr-helper","version":"` + version + `","displayName":"PR Helper","description":"Reviews pull requests.","metadata":{"multica":{"scopes":["issues:read"]}}}`,
		"pr-helper/skills/pr-review/SKILL.md":  registryTestSkill,
	} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buffer.Bytes()
}

// setupRegistryTest enables the marketplace, makes the test user a super
// admin, and clears every registry row the test creates.
func setupRegistryTest(t *testing.T) {
	t.Helper()
	withFeatureFlags(t, testHandler, map[string]bool{featureflags.PluginsV1: true, featureflags.MarketplaceV1: true})
	withHostCapabilities(t)
	cleanupPluginInstallations(t)
	grantPlatformAdmin(t, testUserID, PlatformRoleSuperAdmin)
	clear := func() {
		ctx := context.Background()
		for _, statement := range []string{
			`DELETE FROM registry_install_report`,
			`DELETE FROM registry_targeting`,
			`DELETE FROM registry_listing`,
			`DELETE FROM registry_package_file`,
			`DELETE FROM registry_package_version`,
			`DELETE FROM registry_package`,
			`DELETE FROM registry_source`,
			`DELETE FROM registry_consumer WHERE NOT builtin`,
		} {
			if _, err := testPool.Exec(ctx, statement); err != nil {
				t.Fatalf("clear registry: %v", err)
			}
		}
	}
	clear()
	t.Cleanup(clear)
}

func registryPublicRouter() http.Handler {
	router := chi.NewRouter()
	router.Route("/registry/v1", func(r chi.Router) {
		r.Use(testHandler.RequireMarketplaceV1)
		testHandler.RegistryPublic().Routes(r)
	})
	return router
}

func registryPublicGet(t *testing.T, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	registryPublicRouter().ServeHTTP(recorder, request)
	return recorder
}

func registryAdminCall(t *testing.T, role string, handler http.HandlerFunc, method, path string, body any, params ...string) *testutil.Response {
	t.Helper()
	request := withURLParams(newRequest(method, path, body), params...)
	return testutil.Call(t, adminRoute(role, handler), request)
}

func registryUpload(t *testing.T, sourceID string, archive []byte) registry.UploadResult {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	_ = form.WriteField("source_id", sourceID)
	_ = form.WriteField("reason", "new template")
	_ = form.WriteField("changelog", "Initial release")
	part, err := form.CreateFormFile("bundle", "pr-helper.zip")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	_, _ = part.Write(archive)
	_ = form.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/registry/listings", &body)
	request.Header.Set("X-User-ID", testUserID)
	request.Header.Set("Content-Type", form.FormDataContentType())
	var result registry.UploadResult
	testutil.Call(t, adminRoute(PlatformRoleOperator, testHandler.RegistryAdmin().UploadVersion), request).
		Want(http.StatusCreated).JSON(&result)
	return result
}

func registryApprove(t *testing.T, versionID string) {
	t.Helper()
	registryAdminCall(t, PlatformRoleOperator, testHandler.RegistryAdmin().ReviewVersion, "POST",
		"/api/admin/registry/versions/"+versionID+"/review", map[string]string{"decision": "approved", "reason": "reviewed"}, "id", versionID).
		Want(http.StatusOK)
}

func registryTarget(t *testing.T, listingID string, targets []registry.Target) {
	t.Helper()
	registryAdminCall(t, PlatformRoleOperator, testHandler.RegistryAdmin().SetTargeting, "PUT",
		"/api/admin/registry/listings/"+listingID+"/targeting", map[string]any{"targets": targets, "reason": "rollout"}, "id", listingID).
		Want(http.StatusOK)
}

func registryPublish(t *testing.T, listingID string, public bool) {
	t.Helper()
	registryAdminCall(t, PlatformRoleOperator, testHandler.RegistryAdmin().SetListingStatus, "PUT",
		"/api/admin/registry/listings/"+listingID+"/status", map[string]any{"status": "published", "public": public, "reason": "launch"}, "id", listingID).
		Want(http.StatusOK)
}

func registryCreateSource(t *testing.T) registry.SourceResponse {
	t.Helper()
	var source registry.SourceResponse
	registryAdminCall(t, PlatformRoleSuperAdmin, testHandler.RegistryAdmin().CreateSource, "POST", "/api/admin/registry/sources",
		map[string]string{"namespace": "self", "kind": "self", "display_name": "Our templates", "reason": "set up"}).
		Want(http.StatusCreated).JSON(&source)
	if source.Namespace != "@self" {
		t.Fatalf("namespace = %q, want @self", source.Namespace)
	}
	return source
}

func workspaceMarketplace(t *testing.T) []service.RegistryCatalogEntry {
	t.Helper()
	recorder := httptest.NewRecorder()
	testHandler.ListWorkspaceMarketplace(recorder, pluginHandlerRequest(http.MethodGet, "/plugins/marketplace", nil, map[string]string{"id": testWorkspaceID}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("workspace marketplace: %d %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Listings []service.RegistryCatalogEntry `json:"listings"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body.Listings
}

func installFromRegistry(t *testing.T, versionID string) {
	t.Helper()
	params := map[string]string{"id": testWorkspaceID}
	previewBody, _ := json.Marshal(map[string]string{"registry_version_id": versionID})
	recorder := httptest.NewRecorder()
	testHandler.PreviewPlugin(recorder, pluginHandlerRequest(http.MethodPost, "/plugins/preview", previewBody, params))
	if recorder.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", recorder.Code, recorder.Body.String())
	}
	var preview service.PluginPreview
	if err := json.Unmarshal(recorder.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if preview.RegistryVersionID != versionID || preview.Manifest.Key != "self.pr-helper" {
		t.Fatalf("preview = %+v", preview)
	}
	installBody, _ := json.Marshal(map[string]any{"registry_version_id": versionID, "granted_scopes": preview.Scopes})
	recorder = httptest.NewRecorder()
	testHandler.InstallPlugin(recorder, pluginHandlerRequest(http.MethodPost, "/plugins", installBody, params))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("install: %d %s", recorder.Code, recorder.Body.String())
	}
}

func registryListingDetail(t *testing.T, listingID string) registry.ListingDetail {
	t.Helper()
	var detail registry.ListingDetail
	registryAdminCall(t, PlatformRoleViewer, testHandler.RegistryAdmin().GetListing, "GET", "/api/admin/registry/listings/"+listingID, nil, "id", listingID).
		Want(http.StatusOK).JSON(&detail)
	return detail
}

func TestRegistryPublicMarketplaceServesClaudeCode(t *testing.T) {
	setupRegistryTest(t)
	source := registryCreateSource(t)
	upload := registryUpload(t, source.ID, registryTestArchive(t, "1.0.0"))

	// Drafts and pending versions are invisible.
	var empty registry.Marketplace
	if recorder := registryPublicGet(t, "/registry/v1/@self/marketplace.json", nil); recorder.Code != http.StatusOK {
		t.Fatalf("marketplace: %d", recorder.Code)
	} else if err := json.Unmarshal(recorder.Body.Bytes(), &empty); err != nil || len(empty.Plugins) != 0 {
		t.Fatalf("draft listing leaked: %s", recorder.Body.String())
	}

	registryApprove(t, upload.VersionID)
	registryPublish(t, upload.ListingID, true)

	recorder := registryPublicGet(t, "/registry/v1/@self/marketplace.json", nil)
	var doc map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode marketplace: %v", err)
	}
	if doc["name"] != "multica-self" {
		t.Fatalf("marketplace name = %v", doc["name"])
	}
	plugins, _ := doc["plugins"].([]any)
	if len(plugins) != 1 {
		t.Fatalf("plugins = %v", doc["plugins"])
	}
	entry := plugins[0].(map[string]any)
	sourceField := entry["source"].(map[string]any)
	if entry["name"] != "pr-helper" || entry["version"] != "1.0.0" || sourceField["source"] != "archive" {
		t.Fatalf("entry = %v", entry)
	}
	bundleURL := sourceField["url"].(string)
	if !strings.HasSuffix(bundleURL, "/registry/v1/listings/@self/pr-helper/versions/1.0.0/bundle") {
		t.Fatalf("bundle url = %s", bundleURL)
	}

	bundle := registryPublicGet(t, "/registry/v1/listings/@self/pr-helper/versions/1.0.0/bundle", nil)
	if bundle.Code != http.StatusOK || bundle.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("bundle: %d %s", bundle.Code, bundle.Header().Get("Content-Type"))
	}
	sum := sha256.Sum256(bundle.Body.Bytes())
	if got := hex.EncodeToString(sum[:]); got != sourceField["sha256"] || got != bundle.Header().Get("X-Checksum-Sha256") {
		t.Fatalf("bundle sha256 %s does not match marketplace %v", got, sourceField["sha256"])
	}
	if _, err := registry.ParseArchive(bundle.Body.Bytes()); err != nil {
		t.Fatalf("served bundle is not a valid package: %v", err)
	}

	// A private listing disappears for anonymous callers, bundle included.
	registryPublish(t, upload.ListingID, false)
	if recorder := registryPublicGet(t, "/registry/v1/listings/@self/pr-helper/versions/1.0.0/bundle", nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("private bundle: %d", recorder.Code)
	}

	// The flag hides the API entirely.
	withFeatureFlags(t, testHandler, map[string]bool{featureflags.PluginsV1: true, featureflags.MarketplaceV1: false})
	if recorder := registryPublicGet(t, "/registry/v1/listings", nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("flag off: %d", recorder.Code)
	}
}

func TestRegistryWorkspaceInstallAndUpgrade(t *testing.T) {
	setupRegistryTest(t)
	source := registryCreateSource(t)
	first := registryUpload(t, source.ID, registryTestArchive(t, "1.0.0"))
	registryApprove(t, first.VersionID)
	registryPublish(t, first.ListingID, false)

	// Not targeted at Multica: the workspace sees nothing.
	if listings := workspaceMarketplace(t); len(listings) != 0 {
		t.Fatalf("untargeted listing visible: %+v", listings)
	}
	registryTarget(t, first.ListingID, []registry.Target{{Consumer: registry.ConsumerMultica, TenantID: testWorkspaceID}})

	listings := workspaceMarketplace(t)
	if len(listings) != 1 || listings[0].State != service.RegistryStateNotInstalled || listings[0].PluginKey != "self.pr-helper" {
		t.Fatalf("listings = %+v", listings)
	}
	installFromRegistry(t, first.VersionID)
	if listings := workspaceMarketplace(t); listings[0].State != service.RegistryStateCurrent || listings[0].InstalledVersion != "1.0.0" {
		t.Fatalf("after install = %+v", listings[0])
	}
	if count := dbfx.Count(t, `SELECT count(*) FROM skill WHERE workspace_id = $1 AND name = 'pr-review'`, testWorkspaceID); count != 1 {
		t.Fatalf("installed skills = %d, want 1", count)
	}
	// Registry copies are not the workspace's own publishes.
	recorder := httptest.NewRecorder()
	testHandler.ListPluginPackages(recorder, pluginHandlerRequest(http.MethodGet, "/plugins/packages", nil, map[string]string{"id": testWorkspaceID}))
	if strings.Contains(recorder.Body.String(), "self.pr-helper") {
		t.Fatalf("registry copy listed as a workspace package: %s", recorder.Body.String())
	}
	if detail := registryListingDetail(t, first.ListingID); detail.InstallationsTotal != 1 {
		t.Fatalf("installations = %d, want 1", detail.InstallationsTotal)
	}

	// A new approved version shows as an upgrade, and installing it upgrades in place.
	second := registryUpload(t, source.ID, registryTestArchive(t, "1.1.0"))
	if second.ListingID != first.ListingID {
		t.Fatalf("second upload created another listing")
	}
	if listings := workspaceMarketplace(t); listings[0].State != service.RegistryStateCurrent {
		t.Fatalf("a pending version must not offer an upgrade: %+v", listings[0])
	}
	registryApprove(t, second.VersionID)
	if listings := workspaceMarketplace(t); listings[0].State != service.RegistryStateOutdated || listings[0].Version != "1.1.0" {
		t.Fatalf("after approval = %+v", listings[0])
	}
	installFromRegistry(t, second.VersionID)
	listings = workspaceMarketplace(t)
	if listings[0].State != service.RegistryStateCurrent || listings[0].InstalledVersion != "1.1.0" {
		t.Fatalf("after upgrade = %+v", listings[0])
	}
	if count := dbfx.Count(t, `SELECT count(*) FROM plugin_installation WHERE workspace_id = $1`, testWorkspaceID); count != 1 {
		t.Fatalf("installations = %d, want 1", count)
	}

	// Uninstalling clears the workspace's install report.
	recorder = httptest.NewRecorder()
	testHandler.UninstallPlugin(recorder, pluginHandlerRequest(http.MethodDelete, "/plugins/x", nil, map[string]string{"id": testWorkspaceID, "installationId": listings[0].InstallationID}))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("uninstall: %d %s", recorder.Code, recorder.Body.String())
	}
	if detail := registryListingDetail(t, first.ListingID); detail.InstallationsTotal != 0 {
		t.Fatalf("installations after uninstall = %d, want 0", detail.InstallationsTotal)
	}
}

func TestRegistryRefusesKeyOwnedByWorkspacePublish(t *testing.T) {
	setupRegistryTest(t)
	source := registryCreateSource(t)
	upload := registryUpload(t, source.ID, registryTestArchive(t, "1.0.0"))
	registryApprove(t, upload.VersionID)
	registryPublish(t, upload.ListingID, false)
	registryTarget(t, upload.ListingID, []registry.Target{{Consumer: registry.ConsumerMultica}})

	// The workspace published its own plugin under the same key first.
	dbfx.Exec(t, `INSERT INTO plugin_package (workspace_id, plugin_key, name, created_by) VALUES ($1, 'self.pr-helper', 'Ours', $2)`, testWorkspaceID, testUserID)
	params := map[string]string{"id": testWorkspaceID}
	body, _ := json.Marshal(map[string]any{"registry_version_id": upload.VersionID, "granted_scopes": []string{"issues:read"}})
	recorder := httptest.NewRecorder()
	testHandler.InstallPlugin(recorder, pluginHandlerRequest(http.MethodPost, "/plugins", body, params))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("install over a workspace publish: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestRegistryExternalConsumerSeesOnlyItsTargeting(t *testing.T) {
	setupRegistryTest(t)
	source := registryCreateSource(t)
	upload := registryUpload(t, source.ID, registryTestArchive(t, "1.0.0"))
	registryApprove(t, upload.VersionID)
	registryPublish(t, upload.ListingID, false)

	var created registry.ConsumerKeyResponse
	registryAdminCall(t, PlatformRoleSuperAdmin, testHandler.RegistryAdmin().CreateConsumer, "POST", "/api/admin/registry/consumers",
		map[string]string{"key": "acme", "name": "Acme portal", "reason": "partner"}).
		Want(http.StatusCreated).JSON(&created)
	if !strings.HasPrefix(created.APIKey, "rk_") || !created.Consumer.HasAPIKey {
		t.Fatalf("consumer = %+v", created)
	}
	// Viewers cannot create consumers.
	grantPlatformAdmin(t, testUserID, PlatformRoleOperator)
	registryAdminCall(t, PlatformRoleSuperAdmin, testHandler.RegistryAdmin().CreateConsumer, "POST", "/api/admin/registry/consumers",
		map[string]string{"key": "other", "name": "Other", "reason": "x"}).Want(http.StatusForbidden)

	registryTarget(t, upload.ListingID, []registry.Target{{Consumer: "acme", TenantID: "tenant-1"}})
	key := map[string]string{"Authorization": "Bearer " + created.APIKey}
	listingsFor := func(headers map[string]string) []registry.Listing {
		recorder := registryPublicGet(t, "/registry/v1/listings", headers)
		if recorder.Code != http.StatusOK {
			t.Fatalf("listings: %d %s", recorder.Code, recorder.Body.String())
		}
		var body struct {
			Listings []registry.Listing `json:"listings"`
		}
		_ = json.Unmarshal(recorder.Body.Bytes(), &body)
		return body.Listings
	}
	if got := listingsFor(nil); len(got) != 0 {
		t.Fatalf("anonymous sees private listing: %+v", got)
	}
	if got := listingsFor(map[string]string{"Authorization": key["Authorization"], "X-Tenant-ID": "tenant-2"}); len(got) != 0 {
		t.Fatalf("other tenant sees targeted listing: %+v", got)
	}
	got := listingsFor(map[string]string{"Authorization": key["Authorization"], "X-Tenant-ID": "tenant-1"})
	if len(got) != 1 || got[0].Version.Version != "1.0.0" {
		t.Fatalf("targeted tenant listings = %+v", got)
	}
	if recorder := registryPublicGet(t, "/registry/v1/listings", map[string]string{"Authorization": "Bearer rk_wrong"}); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("bad key: %d", recorder.Code)
	}

	// The consumer reports what its tenant runs.
	report, _ := json.Marshal(registry.InstallReport{TenantID: "tenant-1", VersionID: upload.VersionID, State: "current"})
	request := httptest.NewRequest(http.MethodPost, "/registry/v1/installs", bytes.NewReader(report))
	request.Header.Set("Authorization", key["Authorization"])
	recorder := httptest.NewRecorder()
	registryPublicRouter().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("install report: %d %s", recorder.Code, recorder.Body.String())
	}
	detail := registryListingDetail(t, upload.ListingID)
	if detail.InstallationsTotal != 1 || len(detail.Versions) != 1 || detail.Versions[0].Installations[0].Consumer != "acme" {
		t.Fatalf("detail = %+v", detail)
	}

	// Revoking the version withdraws it from every consumer.
	registryAdminCall(t, PlatformRoleOperator, testHandler.RegistryAdmin().ReviewVersion, "POST",
		"/api/admin/registry/versions/"+upload.VersionID+"/review", map[string]string{"decision": "revoked", "reason": "security issue"}, "id", upload.VersionID).
		Want(http.StatusOK)
	if got := listingsFor(map[string]string{"Authorization": key["Authorization"], "X-Tenant-ID": "tenant-1"}); len(got) != 0 {
		t.Fatalf("revoked version still offered: %+v", got)
	}
}

func TestRegistryAdminWritesAreAudited(t *testing.T) {
	setupRegistryTest(t)
	registryCreateSource(t)
	if count := dbfx.Count(t, `SELECT count(*) FROM admin_audit_log WHERE actor_user_id = $1 AND action = 'registry.source.create'`, testUserID); count != 1 {
		t.Fatalf("audit rows = %d, want 1", count)
	}
	// A refused write leaves no audit row.
	registryAdminCall(t, PlatformRoleSuperAdmin, testHandler.RegistryAdmin().CreateSource, "POST", "/api/admin/registry/sources",
		map[string]string{"namespace": "self", "kind": "self", "display_name": "Dup", "reason": "again"}).Want(http.StatusConflict)
	if count := dbfx.Count(t, `SELECT count(*) FROM admin_audit_log WHERE actor_user_id = $1 AND action = 'registry.source.create'`, testUserID); count != 1 {
		t.Fatalf("audit rows after conflict = %d, want 1", count)
	}
	registryAdminCall(t, PlatformRoleSuperAdmin, testHandler.RegistryAdmin().CreateSource, "POST", "/api/admin/registry/sources",
		map[string]string{"namespace": "other", "kind": "self", "display_name": "No reason"}).Want(http.StatusBadRequest)
}
