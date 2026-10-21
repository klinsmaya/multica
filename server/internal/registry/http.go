package registry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// HTTP surface of the registry: the public read API every consumer uses, and
// the operator API the host mounts under its own admin auth and audit.

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// ErrorStatus maps a registry error to an HTTP status and message. Anything
// that is not a registry error is a 500 the caller should log.
func ErrorStatus(err error) (int, string) {
	var regErr *Error
	if !errors.As(err, &regErr) {
		return http.StatusInternalServerError, "registry request failed"
	}
	switch regErr.Kind {
	case ErrInvalid:
		return http.StatusBadRequest, regErr.Message
	case ErrNotFound:
		return http.StatusNotFound, regErr.Message
	case ErrConflict:
		return http.StatusConflict, regErr.Message
	case ErrForbidden:
		return http.StatusForbidden, regErr.Message
	default:
		return http.StatusInternalServerError, "registry request failed"
	}
}

func writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	status, message := ErrorStatus(err)
	if status >= 500 {
		slog.Warn("registry request failed", "path", r.URL.Path, "error", err)
	}
	writeError(w, status, message)
}

// ---------------------------------------------------------------------------
// Public read API
// ---------------------------------------------------------------------------

// Public serves /registry/v1. A request without an API key is anonymous and
// sees public listings only; with a key it sees what is targeted at that
// consumer, narrowed by the optional X-Tenant-ID and X-Tenant-Groups headers.
type Public struct {
	Service *Service
	// BaseURL is the absolute origin the registry is reachable at, used for
	// bundle URLs. Empty derives it from the request.
	BaseURL string
}

type callerCtxKey struct{}

// Routes mounts the public API on r, which the host places at /registry/v1.
func (p *Public) Routes(r chi.Router) {
	r.Use(p.identify)
	r.Get("/{namespace}/marketplace.json", p.marketplace)
	r.Get("/listings", p.listings)
	r.Get("/listings/{namespace}/{name}/versions/{version}/bundle", p.bundle)
	r.Post("/installs", p.installs)
}

func apiKeyFromRequest(r *http.Request) string {
	if key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return strings.TrimSpace(key)
	}
	return strings.TrimSpace(r.Header.Get("X-Api-Key"))
}

func (p *Public) identify(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := Caller{}
		if key := apiKeyFromRequest(r); key != "" {
			consumer, err := p.Service.Authenticate(r.Context(), key)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "invalid API key")
				return
			}
			caller.ConsumerID = consumer.ID
			caller.TenantID = strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
			for _, group := range strings.Split(r.Header.Get("X-Tenant-Groups"), ",") {
				if group = strings.TrimSpace(group); group != "" {
					caller.TenantGroups = append(caller.TenantGroups, group)
				}
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerCtxKey{}, caller)))
	})
}

func callerFrom(ctx context.Context) Caller {
	caller, _ := ctx.Value(callerCtxKey{}).(Caller)
	return caller
}

func (p *Public) apiRoot(r *http.Request) string {
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	return base + "/registry/v1"
}

func (p *Public) marketplace(w http.ResponseWriter, r *http.Request) {
	namespace := chi.URLParam(r, "namespace")
	rows, err := p.Service.Visible(r.Context(), callerFrom(r.Context()), namespace)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, BuildMarketplace(namespace, rows, p.apiRoot(r)))
}

func (p *Public) listings(w http.ResponseWriter, r *http.Request) {
	rows, err := p.Service.Visible(r.Context(), callerFrom(r.Context()), r.URL.Query().Get("namespace"))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	kind := r.URL.Query().Get("kind")
	tag := strings.ToLower(r.URL.Query().Get("tag"))
	filtered := rows[:0]
	for _, row := range rows {
		if kind != "" && row.Kind != kind {
			continue
		}
		if tag != "" && !containsString(row.Tags, tag) {
			continue
		}
		filtered = append(filtered, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"listings": BuildListings(filtered, p.apiRoot(r))})
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (p *Public) bundle(w http.ResponseWriter, r *http.Request) {
	versionID, err := p.Service.VersionIDByName(r.Context(), chi.URLParam(r, "namespace"), chi.URLParam(r, "name"), chi.URLParam(r, "version"))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	archive, sha, err := p.Service.Archive(r.Context(), callerFrom(r.Context()), versionID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("ETag", `"`+sha+`"`)
	w.Header().Set("X-Checksum-Sha256", sha)
	w.Header().Set("Cache-Control", "private, max-age=3600, immutable")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(archive)
}

func (p *Public) installs(w http.ResponseWriter, r *http.Request) {
	caller := callerFrom(r.Context())
	if !caller.ConsumerID.Valid {
		writeError(w, http.StatusUnauthorized, "an API key is required")
		return
	}
	var report InstallReport
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&report); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if report.TenantID == "" {
		report.TenantID = caller.TenantID
	}
	if err := p.Service.ReportInstall(r.Context(), caller.ConsumerID, report); err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Operator API
// ---------------------------------------------------------------------------

// AdminAction describes one audited operator write.
type AdminAction struct {
	Action     string
	TargetType string
	TargetID   string
	Reason     string
	Payload    map[string]any
}

// AdminWrite performs the write on transaction-bound queries and returns the
// status and body to send. A non-2xx status rolls the transaction back.
type AdminWrite func(q *db.Queries) (int, any, error)

// AdminHost is what the hosting product provides: operator identity, and a
// runner that commits the write together with its audit record.
type AdminHost interface {
	RunAdminAction(w http.ResponseWriter, r *http.Request, action AdminAction, write AdminWrite)
	AdminActorID(r *http.Request) pgtype.UUID
}

// TenantResolver validates a tenant id of one consumer and returns a label
// for display. Tenants are opaque to the registry; only the consumer can say
// whether one exists. Nil accepts any id.
type TenantResolver func(ctx context.Context, consumerKey, tenantID string) (string, error)

// Admin serves the operator API.
type Admin struct {
	Service *Service
	Host    AdminHost
	Tenants TenantResolver
}

const (
	adminReasonMaxLen = 500
	adminPageSizeMax  = 100
	adminPageDefault  = 25
	// multipartOverhead is headroom for the form's own boundaries and fields.
	multipartOverhead = 64 * 1024
)

func (a *Admin) readReason(w http.ResponseWriter, r *http.Request, dest any, reason func() string) (string, bool) {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(dest); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return "", false
	}
	return checkReason(w, reason())
}

func checkReason(w http.ResponseWriter, reason string) (string, bool) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		writeError(w, http.StatusBadRequest, "reason is required")
		return "", false
	}
	if len(reason) > adminReasonMaxLen {
		writeError(w, http.StatusBadRequest, "reason is too long")
		return "", false
	}
	return reason, true
}

// writeResult converts a service error into the runner's result.
func writeResult(err error) (int, any, error) {
	status, message := ErrorStatus(err)
	if status >= 500 {
		return 0, nil, err
	}
	return status, map[string]string{"error": message}, nil
}

func formatTime(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.UTC().Format(time.RFC3339)
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// --- consumers ---

type ConsumerResponse struct {
	ID         string `json:"id"`
	Key        string `json:"key"`
	Name       string `json:"name"`
	Builtin    bool   `json:"builtin"`
	Enabled    bool   `json:"enabled"`
	HasAPIKey  bool   `json:"has_api_key"`
	WebhookURL string `json:"webhook_url"`
	LastUsedAt string `json:"last_used_at"`
	CreatedAt  string `json:"created_at"`
}

func consumerResponse(c db.RegistryConsumer) ConsumerResponse {
	return ConsumerResponse{
		ID: uuidString(c.ID), Key: c.Key, Name: c.Name, Builtin: c.Builtin, Enabled: c.Enabled,
		HasAPIKey: c.ApiKeyHash.Valid, WebhookURL: c.WebhookUrl,
		LastUsedAt: formatTime(c.LastUsedAt), CreatedAt: formatTime(c.CreatedAt),
	}
}

// ConsumerKeyResponse is the only response carrying an API key; the key is
// not stored and cannot be read again.
type ConsumerKeyResponse struct {
	Consumer ConsumerResponse `json:"consumer"`
	APIKey   string           `json:"api_key"`
}

func (a *Admin) ListConsumers(w http.ResponseWriter, r *http.Request) {
	rows, err := a.Service.Queries.ListRegistryConsumers(r.Context())
	if err != nil {
		writeServiceError(w, r, unavailable("list registry consumers", err))
		return
	}
	items := make([]ConsumerResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, consumerResponse(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *Admin) CreateConsumer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key        string `json:"key"`
		Name       string `json:"name"`
		WebhookURL string `json:"webhook_url"`
		Reason     string `json:"reason"`
	}
	reason, ok := a.readReason(w, r, &req, func() string { return req.Reason })
	if !ok {
		return
	}
	actor := a.Host.AdminActorID(r)
	a.Host.RunAdminAction(w, r, AdminAction{
		Action: "registry.consumer.create", TargetType: "registry_consumer", TargetID: req.Key, Reason: reason,
	}, func(q *db.Queries) (int, any, error) {
		consumer, key, err := a.Service.CreateConsumer(r.Context(), q, actor, req.Key, req.Name, req.WebhookURL)
		if err != nil {
			return writeResult(err)
		}
		return http.StatusCreated, ConsumerKeyResponse{Consumer: consumerResponse(consumer), APIKey: key}, nil
	})
}

func (a *Admin) RotateConsumerKey(w http.ResponseWriter, r *http.Request) {
	consumerID := chi.URLParam(r, "id")
	var req struct {
		Reason string `json:"reason"`
	}
	reason, ok := a.readReason(w, r, &req, func() string { return req.Reason })
	if !ok {
		return
	}
	a.Host.RunAdminAction(w, r, AdminAction{
		Action: "registry.consumer.rotate_key", TargetType: "registry_consumer", TargetID: consumerID, Reason: reason,
	}, func(q *db.Queries) (int, any, error) {
		consumer, key, err := a.Service.RotateConsumerKey(r.Context(), q, consumerID)
		if err != nil {
			return writeResult(err)
		}
		return http.StatusOK, ConsumerKeyResponse{Consumer: consumerResponse(consumer), APIKey: key}, nil
	})
}

type toggleRequest struct {
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason"`
}

func (a *Admin) SetConsumerEnabled(w http.ResponseWriter, r *http.Request) {
	consumerID := chi.URLParam(r, "id")
	var req toggleRequest
	reason, ok := a.readReason(w, r, &req, func() string { return req.Reason })
	if !ok {
		return
	}
	action := "registry.consumer.disable"
	if req.Enabled {
		action = "registry.consumer.enable"
	}
	a.Host.RunAdminAction(w, r, AdminAction{
		Action: action, TargetType: "registry_consumer", TargetID: consumerID, Reason: reason,
	}, func(q *db.Queries) (int, any, error) {
		consumer, err := a.Service.SetConsumerEnabled(r.Context(), q, consumerID, req.Enabled)
		if err != nil {
			return writeResult(err)
		}
		return http.StatusOK, consumerResponse(consumer), nil
	})
}

// --- sources ---

type SourceResponse struct {
	ID           string `json:"id"`
	Namespace    string `json:"namespace"`
	Kind         string `json:"kind"`
	DisplayName  string `json:"display_name"`
	URL          string `json:"url"`
	Enabled      bool   `json:"enabled"`
	PackageCount int64  `json:"package_count"`
	LastSyncedAt string `json:"last_synced_at"`
	CreatedAt    string `json:"created_at"`
}

func sourceResponse(s db.RegistrySource, packageCount int64) SourceResponse {
	return SourceResponse{
		ID: uuidString(s.ID), Namespace: s.Namespace, Kind: s.Kind, DisplayName: s.DisplayName,
		URL: s.Url, Enabled: s.Enabled, PackageCount: packageCount,
		LastSyncedAt: formatTime(s.LastSyncedAt), CreatedAt: formatTime(s.CreatedAt),
	}
}

func (a *Admin) ListSources(w http.ResponseWriter, r *http.Request) {
	rows, err := a.Service.Queries.ListRegistrySources(r.Context())
	if err != nil {
		writeServiceError(w, r, unavailable("list registry sources", err))
		return
	}
	items := make([]SourceResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, sourceResponse(db.RegistrySource{
			ID: row.ID, Namespace: row.Namespace, Kind: row.Kind, DisplayName: row.DisplayName,
			Url: row.Url, Enabled: row.Enabled, LastSyncedAt: row.LastSyncedAt, CreatedAt: row.CreatedAt,
		}, row.PackageCount))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *Admin) CreateSource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Namespace   string `json:"namespace"`
		Kind        string `json:"kind"`
		DisplayName string `json:"display_name"`
		URL         string `json:"url"`
		Reason      string `json:"reason"`
	}
	reason, ok := a.readReason(w, r, &req, func() string { return req.Reason })
	if !ok {
		return
	}
	actor := a.Host.AdminActorID(r)
	a.Host.RunAdminAction(w, r, AdminAction{
		Action: "registry.source.create", TargetType: "registry_source", TargetID: NormalizeNamespace(req.Namespace), Reason: reason,
		Payload: map[string]any{"kind": req.Kind, "url": req.URL},
	}, func(q *db.Queries) (int, any, error) {
		source, err := a.Service.CreateSource(r.Context(), q, actor, SourceInput{
			Namespace: req.Namespace, Kind: req.Kind, DisplayName: req.DisplayName, URL: req.URL,
		})
		if err != nil {
			return writeResult(err)
		}
		return http.StatusCreated, sourceResponse(source, 0), nil
	})
}

func (a *Admin) SetSourceEnabled(w http.ResponseWriter, r *http.Request) {
	sourceID := chi.URLParam(r, "id")
	var req toggleRequest
	reason, ok := a.readReason(w, r, &req, func() string { return req.Reason })
	if !ok {
		return
	}
	action := "registry.source.disable"
	if req.Enabled {
		action = "registry.source.enable"
	}
	a.Host.RunAdminAction(w, r, AdminAction{
		Action: action, TargetType: "registry_source", TargetID: sourceID, Reason: reason,
	}, func(q *db.Queries) (int, any, error) {
		source, err := a.Service.SetSourceEnabled(r.Context(), q, sourceID, req.Enabled)
		if err != nil {
			return writeResult(err)
		}
		return http.StatusOK, sourceResponse(source, 0), nil
	})
}

// --- listings ---

type ListingSummary struct {
	ID            string   `json:"id"`
	SourceID      string   `json:"source_id"`
	Namespace     string   `json:"namespace"`
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	DisplayName   string   `json:"display_name"`
	Description   string   `json:"description"`
	Category      string   `json:"category"`
	Tags          []string `json:"tags"`
	Status        string   `json:"status"`
	Public        bool     `json:"public"`
	Enforced      bool     `json:"enforced"`
	LatestVersion string   `json:"latest_version"`
	PendingCount  int64    `json:"pending_count"`
	UpdatedAt     string   `json:"updated_at"`
}

type ListingPage struct {
	Items    []ListingSummary `json:"items"`
	Total    int64            `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
}

func pageParams(r *http.Request) (int32, int32, int) {
	page := atoiDefault(r.URL.Query().Get("page"), 1)
	if page < 1 {
		page = 1
	}
	size := atoiDefault(r.URL.Query().Get("page_size"), adminPageDefault)
	if size < 1 {
		size = adminPageDefault
	}
	if size > adminPageSizeMax {
		size = adminPageSizeMax
	}
	return int32(size), int32((page - 1) * size), page
}

func atoiDefault(value string, fallback int) int {
	n := 0
	if value == "" {
		return fallback
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return fallback
		}
		n = n*10 + int(c-'0')
		if n > 1_000_000 {
			return fallback
		}
	}
	return n
}

func (a *Admin) ListListings(w http.ResponseWriter, r *http.Request) {
	limit, offset, page := pageParams(r)
	query := r.URL.Query()
	var sourceID pgtype.UUID
	if raw := strings.TrimSpace(query.Get("source_id")); raw != "" {
		id, err := parseID(raw, "source")
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid source_id")
			return
		}
		sourceID = id
	}
	status := optionalText(strings.TrimSpace(query.Get("status")))
	q := optionalText(strings.TrimSpace(query.Get("q")))
	pendingOnly := query.Get("pending") == "true"
	rows, err := a.Service.Queries.AdminListRegistryListings(r.Context(), db.AdminListRegistryListingsParams{
		SourceID: sourceID, Status: status, Q: q, PendingOnly: pendingOnly, PageLimit: limit, PageOffset: offset,
	})
	if err != nil {
		writeServiceError(w, r, unavailable("list registry listings", err))
		return
	}
	total, err := a.Service.Queries.AdminCountRegistryListings(r.Context(), db.AdminCountRegistryListingsParams{
		SourceID: sourceID, Status: status, Q: q, PendingOnly: pendingOnly,
	})
	if err != nil {
		writeServiceError(w, r, unavailable("count registry listings", err))
		return
	}
	items := make([]ListingSummary, 0, len(rows))
	for _, row := range rows {
		items = append(items, ListingSummary{
			ID: uuidString(row.ID), SourceID: uuidString(row.SourceID), Namespace: row.Namespace,
			Name: row.Name, Kind: row.Kind, DisplayName: row.DisplayName, Description: row.Description,
			Category: row.Category, Tags: nonNil(row.Tags), Status: row.Status, Public: row.Public,
			Enforced: row.Enforced, LatestVersion: row.LatestVersion, PendingCount: row.PendingCount,
			UpdatedAt: formatTime(row.UpdatedAt),
		})
	}
	writeJSON(w, http.StatusOK, ListingPage{Items: items, Total: total, Page: page, PageSize: int(limit)})
}

// VersionResponse shows a reviewer what a version would be allowed to do,
// not only its name: skills, agents, MCP endpoints and consumer metadata.
type VersionResponse struct {
	ID            string                     `json:"id"`
	Version       string                     `json:"version"`
	SHA256        string                     `json:"sha256"`
	SizeBytes     int64                      `json:"size_bytes"`
	Changelog     string                     `json:"changelog"`
	ReviewStatus  string                     `json:"review_status"`
	ReviewReason  string                     `json:"review_reason"`
	ReviewedAt    string                     `json:"reviewed_at"`
	CreatedAt     string                     `json:"created_at"`
	Installations []VersionInstallCount      `json:"installations"`
	Skills        []string                   `json:"skills"`
	Agents        []string                   `json:"agents"`
	MCPServers    []MCPServer                `json:"mcp_servers"`
	Metadata      map[string]json.RawMessage `json:"metadata"`
}

type VersionInstallCount struct {
	Consumer string `json:"consumer"`
	Count    int64  `json:"count"`
}

type TargetResponse struct {
	Consumer     string `json:"consumer"`
	ConsumerName string `json:"consumer_name"`
	TenantID     string `json:"tenant_id"`
	TenantGroup  string `json:"tenant_group"`
	TenantLabel  string `json:"tenant_label,omitempty"`
}

type ListingDetail struct {
	ListingSummary
	SourceKind         string            `json:"source_kind"`
	SourceEnabled      bool              `json:"source_enabled"`
	Versions           []VersionResponse `json:"versions"`
	Targeting          []TargetResponse  `json:"targeting"`
	InstallationsTotal int64             `json:"installations_total"`
}

func (a *Admin) versionResponse(ctx context.Context, row db.GetRegistryPackageVersionRow, counts []VersionInstallCount) VersionResponse {
	var manifest PluginManifest
	_ = json.Unmarshal(row.Manifest, &manifest)
	resp := VersionResponse{
		ID: uuidString(row.ID), Version: row.Version, SHA256: row.Sha256, SizeBytes: row.SizeBytes,
		Changelog: row.Changelog, ReviewStatus: row.ReviewStatus, ReviewReason: row.ReviewReason,
		ReviewedAt: formatTime(row.ReviewedAt), CreatedAt: formatTime(row.CreatedAt),
		Installations: counts, Skills: []string{}, Agents: []string{}, MCPServers: []MCPServer{},
		Metadata: manifest.Metadata,
	}
	if resp.Installations == nil {
		resp.Installations = []VersionInstallCount{}
	}
	if resp.Metadata == nil {
		resp.Metadata = map[string]json.RawMessage{}
	}
	files, err := a.Service.Queries.ListRegistryPackageFiles(ctx, row.ID)
	if err == nil {
		converted := make([]File, 0, len(files))
		for _, f := range files {
			converted = append(converted, File{Path: f.Path, Content: f.Content})
		}
		if pkg, err := ParseFiles(converted); err == nil {
			for _, skill := range pkg.Skills {
				resp.Skills = append(resp.Skills, skill.Name)
			}
			resp.Agents = nonNil(pkg.Agents)
			if pkg.MCPServers != nil {
				resp.MCPServers = pkg.MCPServers
			}
		}
	}
	return resp
}

func (a *Admin) GetListing(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(chi.URLParam(r, "id"), "listing")
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	ctx := r.Context()
	listing, err := a.Service.Queries.GetRegistryListing(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "listing not found")
			return
		}
		writeServiceError(w, r, unavailable("load registry listing", err))
		return
	}
	versions, err := a.Service.Queries.ListRegistryPackageVersions(ctx, listing.PackageID)
	if err != nil {
		writeServiceError(w, r, unavailable("list registry versions", err))
		return
	}
	counts, err := a.Service.Queries.CountRegistryInstallReportsByVersion(ctx, id)
	if err != nil {
		writeServiceError(w, r, unavailable("count install reports", err))
		return
	}
	targeting, err := a.Service.Queries.ListRegistryTargeting(ctx, id)
	if err != nil {
		writeServiceError(w, r, unavailable("list registry targeting", err))
		return
	}

	byVersion := map[string][]VersionInstallCount{}
	var total int64
	for _, c := range counts {
		key := uuidString(c.VersionID)
		byVersion[key] = append(byVersion[key], VersionInstallCount{Consumer: c.ConsumerKey, Count: c.Installations})
		total += c.Installations
	}
	latest := ""
	var pending int64
	rendered := make([]VersionResponse, 0, len(versions))
	for _, v := range versions {
		if latest == "" && v.ReviewStatus == ReviewApproved {
			latest = v.Version
		}
		if v.ReviewStatus == ReviewPending {
			pending++
		}
		rendered = append(rendered, a.versionResponse(ctx, db.GetRegistryPackageVersionRow(v), byVersion[uuidString(v.ID)]))
	}
	targets := make([]TargetResponse, 0, len(targeting))
	for _, t := range targeting {
		target := TargetResponse{Consumer: t.ConsumerKey, ConsumerName: t.ConsumerName, TenantID: t.TenantID.String, TenantGroup: t.TenantGroup.String}
		if t.TenantID.Valid && a.Tenants != nil {
			if label, err := a.Tenants(ctx, t.ConsumerKey, t.TenantID.String); err == nil {
				target.TenantLabel = label
			}
		}
		targets = append(targets, target)
	}
	writeJSON(w, http.StatusOK, ListingDetail{
		ListingSummary: ListingSummary{
			ID: uuidString(listing.ID), SourceID: uuidString(listing.SourceID), Namespace: listing.Namespace,
			Name: listing.Name, Kind: listing.Kind, DisplayName: listing.DisplayName, Description: listing.Description,
			Category: listing.Category, Tags: nonNil(listing.Tags), Status: listing.Status, Public: listing.Public,
			Enforced: listing.Enforced, LatestVersion: latest, PendingCount: pending, UpdatedAt: formatTime(listing.UpdatedAt),
		},
		SourceKind:         listing.SourceKind,
		SourceEnabled:      listing.SourceEnabled,
		Versions:           rendered,
		Targeting:          targets,
		InstallationsTotal: total,
	})
}

// UploadVersion — multipart/form-data: source_id, bundle (zip), reason, and
// optional changelog, category and tags (comma separated).
func (a *Admin) UploadVersion(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxArchiveSize+multipartOverhead)
	if err := r.ParseMultipartForm(MaxArchiveSize + multipartOverhead); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "the package upload is too large or malformed")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	reason, ok := checkReason(w, r.FormValue("reason"))
	if !ok {
		return
	}
	file, _, err := r.FormFile("bundle")
	if err != nil {
		writeError(w, http.StatusBadRequest, "a package file is required")
		return
	}
	defer file.Close()
	archive, err := io.ReadAll(io.LimitReader(file, MaxArchiveSize+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read the package")
		return
	}
	if len(archive) > MaxArchiveSize {
		writeError(w, http.StatusRequestEntityTooLarge, "the package is too large")
		return
	}
	var tags []string
	if raw := strings.TrimSpace(r.FormValue("tags")); raw != "" {
		tags = strings.Split(raw, ",")
	}
	sourceID := r.FormValue("source_id")
	actor := a.Host.AdminActorID(r)
	a.Host.RunAdminAction(w, r, AdminAction{
		Action: "registry.version.upload", TargetType: "registry_source", TargetID: sourceID, Reason: reason,
	}, func(q *db.Queries) (int, any, error) {
		result, err := a.Service.UploadVersion(r.Context(), q, actor, UploadInput{
			SourceID: sourceID, Archive: archive, Changelog: r.FormValue("changelog"),
			Category: r.FormValue("category"), Tags: tags,
		})
		if err != nil {
			return writeResult(err)
		}
		return http.StatusCreated, result, nil
	})
}

func (a *Admin) SetListingStatus(w http.ResponseWriter, r *http.Request) {
	listingID := chi.URLParam(r, "id")
	var req struct {
		Status string `json:"status"`
		Public bool   `json:"public"`
		Reason string `json:"reason"`
	}
	reason, ok := a.readReason(w, r, &req, func() string { return req.Reason })
	if !ok {
		return
	}
	a.Host.RunAdminAction(w, r, AdminAction{
		Action: "registry.listing.status", TargetType: "registry_listing", TargetID: listingID, Reason: reason,
		Payload: map[string]any{"status": req.Status, "public": req.Public},
	}, func(q *db.Queries) (int, any, error) {
		listing, err := a.Service.SetListingStatus(r.Context(), q, listingID, req.Status, req.Public)
		if err != nil {
			return writeResult(err)
		}
		return http.StatusOK, map[string]any{"id": uuidString(listing.ID), "status": listing.Status, "public": listing.Public}, nil
	})
}

func (a *Admin) SetTargeting(w http.ResponseWriter, r *http.Request) {
	listingID := chi.URLParam(r, "id")
	var req struct {
		Targets []Target `json:"targets"`
		Reason  string   `json:"reason"`
	}
	reason, ok := a.readReason(w, r, &req, func() string { return req.Reason })
	if !ok {
		return
	}
	// Tenants are checked with the consumer that owns them before the write.
	if a.Tenants != nil {
		for _, target := range req.Targets {
			if strings.TrimSpace(target.TenantID) == "" {
				continue
			}
			if _, err := a.Tenants(r.Context(), strings.TrimSpace(target.Consumer), strings.TrimSpace(target.TenantID)); err != nil {
				writeServiceError(w, r, err)
				return
			}
		}
	}
	actor := a.Host.AdminActorID(r)
	a.Host.RunAdminAction(w, r, AdminAction{
		Action: "registry.listing.targeting", TargetType: "registry_listing", TargetID: listingID, Reason: reason,
		Payload: map[string]any{"targets": req.Targets},
	}, func(q *db.Queries) (int, any, error) {
		if err := a.Service.ReplaceTargeting(r.Context(), q, actor, listingID, req.Targets); err != nil {
			return writeResult(err)
		}
		return http.StatusOK, map[string]any{"targets": req.Targets}, nil
	})
}

func (a *Admin) ReviewVersion(w http.ResponseWriter, r *http.Request) {
	versionID := chi.URLParam(r, "id")
	var req struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	reason, ok := a.readReason(w, r, &req, func() string { return req.Reason })
	if !ok {
		return
	}
	actor := a.Host.AdminActorID(r)
	a.Host.RunAdminAction(w, r, AdminAction{
		Action: "registry.version." + req.Decision, TargetType: "registry_package_version", TargetID: versionID, Reason: reason,
	}, func(q *db.Queries) (int, any, error) {
		version, err := a.Service.ReviewVersion(r.Context(), q, actor, versionID, req.Decision, reason)
		if err != nil {
			return writeResult(err)
		}
		return http.StatusOK, map[string]string{"id": uuidString(version.ID), "review_status": version.ReviewStatus}, nil
	})
}
