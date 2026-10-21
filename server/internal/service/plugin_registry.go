package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/registry"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/plugincontract"
)

// PluginRegistry is Multica's adapter onto the template registry, and the only
// code that knows both. The registry hands out neutral Claude Code packages;
// this turns one into a multica.plugin.json bundle, copies the approved version
// into the workspace on install, and from there the ordinary plugin install,
// upgrade and uninstall paths take over. Multica is a consumer like any other:
// it sees what is targeted at the "multica" consumer and the workspace tenant,
// and reports what each workspace runs.
type PluginRegistry struct {
	Registry *registry.Service
	Plugins  *PluginService
}

func NewPluginRegistry(registrySvc *registry.Service, plugins *PluginService) *PluginRegistry {
	return &PluginRegistry{Registry: registrySvc, Plugins: plugins}
}

// MulticaMetadata is the consumer-specific block a package may carry in
// plugin.json under metadata.multica. Other consumers ignore it.
type MulticaMetadata struct {
	Scopes []string        `json:"scopes,omitempty"`
	Squad  json.RawMessage `json:"squad,omitempty"`
}

// Catalog states for one listing in one workspace.
const (
	RegistryStateNotInstalled = "not_installed"
	RegistryStateCurrent      = "current"
	RegistryStateOutdated     = "outdated"
)

// RegistryCatalogEntry is one listing as a workspace sees it.
type RegistryCatalogEntry struct {
	ListingID        string   `json:"listing_id"`
	Namespace        string   `json:"namespace"`
	Name             string   `json:"name"`
	PluginKey        string   `json:"plugin_key"`
	DisplayName      string   `json:"display_name"`
	Description      string   `json:"description"`
	Category         string   `json:"category"`
	Tags             []string `json:"tags"`
	SourceKind       string   `json:"source_kind"`
	VersionID        string   `json:"version_id"`
	Version          string   `json:"version"`
	Changelog        string   `json:"changelog"`
	PublishedAt      string   `json:"published_at"`
	State            string   `json:"state"`
	InstalledVersion string   `json:"installed_version,omitempty"`
	InstallationID   string   `json:"installation_id,omitempty"`
}

func (a *PluginRegistry) caller(ctx context.Context, workspaceID pgtype.UUID) (registry.Caller, error) {
	consumer, err := a.Registry.ConsumerByKey(ctx, registry.ConsumerMultica)
	if err != nil {
		return registry.Caller{}, registryError(err)
	}
	return registry.Caller{ConsumerID: consumer.ID, TenantID: uuidString(workspaceID)}, nil
}

// registryError maps a registry error onto the plugin error kinds the plugin
// handlers already render.
func registryError(err error) error {
	var regErr *registry.Error
	if !errors.As(err, &regErr) {
		return &PluginError{Kind: PluginErrorUnavailable, Message: "template marketplace request failed", Err: err}
	}
	switch regErr.Kind {
	case registry.ErrInvalid:
		return pluginErrf(PluginErrorInvalid, "%s", regErr.Message)
	case registry.ErrNotFound:
		return pluginErrf(PluginErrorNotFound, "%s", regErr.Message)
	case registry.ErrConflict, registry.ErrForbidden:
		return pluginErrf(PluginErrorConflict, "%s", regErr.Message)
	default:
		return &PluginError{Kind: PluginErrorUnavailable, Message: regErr.Message, Err: err}
	}
}

// Catalog lists what the workspace may install, with what it already runs.
func (a *PluginRegistry) Catalog(ctx context.Context, workspaceID pgtype.UUID) ([]RegistryCatalogEntry, error) {
	caller, err := a.caller(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	rows, err := a.Registry.Visible(ctx, caller, "")
	if err != nil {
		return nil, registryError(err)
	}
	installs, err := a.Plugins.Queries.ListWorkspaceRegistryInstallations(ctx, workspaceID)
	if err != nil {
		return nil, &PluginError{Kind: PluginErrorUnavailable, Message: "list marketplace installations", Err: err}
	}
	byListing := make(map[string]db.ListWorkspaceRegistryInstallationsRow, len(installs))
	for _, install := range installs {
		byListing[uuidString(install.RegistryListingID)] = install
	}
	entries := make([]RegistryCatalogEntry, 0, len(rows))
	for _, row := range rows {
		tags := row.Tags
		if tags == nil {
			tags = []string{}
		}
		entry := RegistryCatalogEntry{
			ListingID:   uuidString(row.ID),
			Namespace:   row.Namespace,
			Name:        row.Name,
			PluginKey:   RegistryPluginKey(row.Namespace, row.Name),
			DisplayName: row.DisplayName,
			Description: row.Description,
			Category:    row.Category,
			Tags:        tags,
			SourceKind:  row.SourceKind,
			VersionID:   uuidString(row.VersionID),
			Version:     row.Version,
			Changelog:   row.Changelog,
			PublishedAt: row.VersionCreatedAt.Time.UTC().Format(time.RFC3339),
			State:       RegistryStateNotInstalled,
		}
		if install, ok := byListing[entry.ListingID]; ok {
			entry.InstalledVersion = install.Version
			entry.InstallationID = uuidString(install.InstallationID)
			entry.State = RegistryStateOutdated
			if install.RegistryVersionID == row.VersionID {
				entry.State = RegistryStateCurrent
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// Preview builds the consent screen for a registry version without writing
// anything.
func (a *PluginRegistry) Preview(ctx context.Context, workspaceID pgtype.UUID, registryVersionID string) (*PluginPreview, error) {
	_, bundle, err := a.load(ctx, workspaceID, registryVersionID)
	if err != nil {
		return nil, err
	}
	preview, err := a.Plugins.previewManifest(ctx, workspaceID, bundle.Manifest)
	if err != nil {
		return nil, err
	}
	preview.RegistryVersionID = registryVersionID
	preview.Version = bundle.Manifest.Version
	preview.Digest = BundleDigest(bundle)
	return preview, nil
}

// Install copies the registry version into the workspace (once) and installs
// or upgrades it through the ordinary plugin path.
func (a *PluginRegistry) Install(ctx context.Context, workspaceID, userID pgtype.UUID, registryVersionID string, grantedScopes []string) (db.PluginInstallation, error) {
	version, bundle, err := a.load(ctx, workspaceID, registryVersionID)
	if err != nil {
		return db.PluginInstallation{}, err
	}
	copied, err := a.materialize(ctx, workspaceID, userID, version, bundle)
	if err != nil {
		return db.PluginInstallation{}, err
	}
	installation, err := a.Plugins.InstallPlugin(ctx, workspaceID, userID, uuidString(copied.ID), grantedScopes)
	if err != nil {
		return db.PluginInstallation{}, err
	}
	a.report(ctx, workspaceID, registry.InstallReport{VersionID: registryVersionID, State: RegistryStateCurrent})
	return installation, nil
}

// InstallationRemoved tells the registry a workspace no longer runs a listing.
// Call it after an uninstall commits; installations not from the registry are
// ignored.
func (a *PluginRegistry) InstallationRemoved(ctx context.Context, installation db.PluginInstallation) {
	source, err := a.Plugins.Queries.GetInstallationRegistrySource(ctx, installation.PackageVersionID)
	if err != nil || !source.RegistryVersionID.Valid {
		return
	}
	a.report(ctx, installation.WorkspaceID, registry.InstallReport{VersionID: uuidString(source.RegistryVersionID), State: "removed"})
}

// report records install state for the operator's version distribution. It is
// bookkeeping, so a failure is logged rather than failing the install.
func (a *PluginRegistry) report(ctx context.Context, workspaceID pgtype.UUID, report registry.InstallReport) {
	caller, err := a.caller(ctx, workspaceID)
	if err == nil {
		report.TenantID = caller.TenantID
		err = a.Registry.ReportInstall(ctx, caller.ConsumerID, report)
	}
	if err != nil {
		slog.Warn("report marketplace install", "workspace_id", uuidString(workspaceID), "version_id", report.VersionID, "error", err)
	}
}

func (a *PluginRegistry) load(ctx context.Context, workspaceID pgtype.UUID, registryVersionID string) (db.GetVisibleRegistryVersionRow, plugincontract.Bundle, error) {
	caller, err := a.caller(ctx, workspaceID)
	if err != nil {
		return db.GetVisibleRegistryVersionRow{}, plugincontract.Bundle{}, err
	}
	version, pkg, err := a.Registry.LoadPackage(ctx, caller, registryVersionID)
	if err != nil {
		return db.GetVisibleRegistryVersionRow{}, plugincontract.Bundle{}, registryError(err)
	}
	bundle, err := RegistryBundle(version.Namespace, pkg)
	if err != nil {
		return db.GetVisibleRegistryVersionRow{}, plugincontract.Bundle{}, pluginErrf(PluginErrorIncompatible, "this template cannot be installed in Multica: %v", err)
	}
	return version, bundle, nil
}

// materialize returns the workspace copy of a registry version, creating the
// package and version rows on first install. The copy is what installations,
// surfaces and skills read, exactly as for a workspace's own publish.
func (a *PluginRegistry) materialize(ctx context.Context, workspaceID, userID pgtype.UUID, version db.GetVisibleRegistryVersionRow, bundle plugincontract.Bundle) (db.PluginPackageVersion, error) {
	if err := bundle.Manifest.CheckCapabilities(a.Plugins.Host); err != nil {
		return db.PluginPackageVersion{}, &PluginError{Kind: PluginErrorIncompatible, Message: capabilityMessage(err), Err: err}
	}
	tx, err := a.Plugins.TxStarter.Begin(ctx)
	if err != nil {
		return db.PluginPackageVersion{}, &PluginError{Kind: PluginErrorUnavailable, Message: "begin marketplace copy", Err: err}
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := a.Plugins.Queries.WithTx(tx)

	if err := lockPluginPackageKey(ctx, queries, workspaceID, bundle.Manifest.Key); err != nil {
		return db.PluginPackageVersion{}, err
	}
	existing, err := queries.GetWorkspacePluginPackageVersionByRegistryVersion(ctx, db.GetWorkspacePluginPackageVersionByRegistryVersionParams{
		WorkspaceID:       workspaceID,
		RegistryVersionID: version.ID,
	})
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.PluginPackageVersion{}, &PluginError{Kind: PluginErrorUnavailable, Message: "load marketplace copy", Err: err}
	}

	pkg, err := queries.GetWorkspacePluginPackageByKey(ctx, db.GetWorkspacePluginPackageByKeyParams{
		WorkspaceID: workspaceID,
		PluginKey:   bundle.Manifest.Key,
	})
	switch {
	case err == nil:
		if pkg.RegistryListingID != version.ListingID {
			return db.PluginPackageVersion{}, pluginErrf(PluginErrorConflict, "this workspace already has a plugin with key %q that did not come from this template", bundle.Manifest.Key)
		}
	case errors.Is(err, pgx.ErrNoRows):
		pkg, err = queries.CreateRegistryPluginPackage(ctx, db.CreateRegistryPluginPackageParams{
			WorkspaceID:       workspaceID,
			PluginKey:         bundle.Manifest.Key,
			Name:              bundle.Manifest.Name,
			CreatedBy:         userID,
			RegistryListingID: version.ListingID,
		})
		if err != nil {
			return db.PluginPackageVersion{}, &PluginError{Kind: PluginErrorUnavailable, Message: "create marketplace copy", Err: err}
		}
	default:
		return db.PluginPackageVersion{}, &PluginError{Kind: PluginErrorUnavailable, Message: "load plugin package", Err: err}
	}

	row, err := queries.CreateRegistryPluginPackageVersion(ctx, db.CreateRegistryPluginPackageVersionParams{
		PackageID:         pkg.ID,
		WorkspaceID:       workspaceID,
		Version:           bundle.Manifest.Version,
		Manifest:          bundle.Canonical,
		Digest:            BundleDigest(bundle),
		SizeBytes:         int64(bundle.TotalSize()),
		PublishedBy:       userID,
		RegistryVersionID: version.ID,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return db.PluginPackageVersion{}, pluginErrf(PluginErrorConflict, "version %s of this template is already in this workspace; try again", bundle.Manifest.Version)
		}
		return db.PluginPackageVersion{}, &PluginError{Kind: PluginErrorUnavailable, Message: "copy marketplace version", Err: err}
	}
	if err := storeBundleFiles(ctx, queries, row.ID, bundle); err != nil {
		return db.PluginPackageVersion{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.PluginPackageVersion{}, &PluginError{Kind: PluginErrorUnavailable, Message: "commit marketplace copy", Err: err}
	}
	return row, nil
}

var registryKeySegment = regexp.MustCompile(`[^a-z0-9]+`)

// RegistryPluginKey is the Multica plugin key of a registry package:
// "<namespace>.<name>", with each part folded into a key segment.
func RegistryPluginKey(namespace, name string) string {
	segment := func(value string) string {
		return strings.Trim(registryKeySegment.ReplaceAllString(strings.ToLower(value), "-"), "-")
	}
	return segment(strings.TrimPrefix(namespace, "@")) + "." + segment(name)
}

// RegistryBundle converts a neutral package into a Multica plugin bundle:
// skills become skill resources, .mcp.json servers become agent-invoked mcp
// hooks with the net: scopes their hosts need, and metadata.multica.scopes is
// added to the consent list. Agents and squads are not supported yet.
func RegistryBundle(namespace string, pkg registry.Package) (plugincontract.Bundle, error) {
	var meta MulticaMetadata
	if raw, ok := pkg.Manifest.Metadata["multica"]; ok {
		if err := json.Unmarshal(raw, &meta); err != nil {
			return plugincontract.Bundle{}, fmt.Errorf("metadata.multica is invalid: %w", err)
		}
	}
	if len(meta.Squad) > 0 && string(meta.Squad) != "null" {
		return plugincontract.Bundle{}, errors.New("squad templates are not supported yet")
	}
	if len(pkg.Agents) > 0 {
		return plugincontract.Bundle{}, errors.New("agent templates are not supported yet")
	}

	name := pkg.Manifest.DisplayName
	if name == "" {
		name = pkg.Manifest.Name
	}
	author := plugincontract.Author{Name: strings.TrimPrefix(namespace, "@")}
	if pkg.Manifest.Author != nil && strings.TrimSpace(pkg.Manifest.Author.Name) != "" {
		author.Name = pkg.Manifest.Author.Name
		if strings.HasPrefix(pkg.Manifest.Author.URL, "https://") {
			author.URL = pkg.Manifest.Author.URL
		}
	}
	manifest := plugincontract.Manifest{
		ManifestVersion: plugincontract.ManifestVersion1,
		Key:             RegistryPluginKey(namespace, pkg.Manifest.Name),
		Name:            name,
		Description:     pkg.Manifest.Description,
		Version:         pkg.Manifest.Version,
		Author:          author,
		Scopes:          []string{},
	}
	addScope := func(scope string) {
		for _, existing := range manifest.Scopes {
			if existing == scope {
				return
			}
		}
		manifest.Scopes = append(manifest.Scopes, scope)
	}
	for _, scope := range meta.Scopes {
		addScope(scope)
	}

	files := make([]plugincontract.BundleFile, 0, len(pkg.Skills))
	for _, skill := range pkg.Skills {
		entry := "skills/" + skill.Name + "/SKILL.md"
		content, ok := pkg.File(entry)
		if !ok {
			return plugincontract.Bundle{}, fmt.Errorf("skill %q has no SKILL.md", skill.Name)
		}
		manifest.Contributes.Resources = append(manifest.Contributes.Resources, plugincontract.Resource{
			Type: plugincontract.ResourceSkill, Key: skill.Name, Entry: entry,
		})
		files = append(files, plugincontract.BundleFile{Path: entry, Content: content})
	}
	for _, server := range pkg.MCPServers {
		if server.Type != "http" {
			return plugincontract.Bundle{}, fmt.Errorf("MCP server %q uses the %s transport; Multica connects to streamable HTTP servers only", server.Name, server.Type)
		}
		description := server.Description
		if description == "" {
			description = "Tools from the " + server.Name + " MCP server of " + name + "."
		}
		manifest.Contributes.Hooks = append(manifest.Contributes.Hooks, plugincontract.Hook{
			Key:         strings.Trim(registryKeySegment.ReplaceAllString(strings.ToLower(server.Name), "_"), "_"),
			Name:        server.Name,
			Description: description,
			Triggers:    []string{plugincontract.TriggerAgent},
			Transport:   plugincontract.HookTransport{Type: plugincontract.TransportMCP, URL: server.URL},
		})
		if host := mcpHost(server.URL); host != "" {
			addScope(plugincontract.ScopeNetPrefix + host)
		}
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	raw, err := json.Marshal(manifest)
	if err != nil {
		return plugincontract.Bundle{}, err
	}
	parsed, canonical, err := plugincontract.ParseManifest(raw)
	if err != nil {
		return plugincontract.Bundle{}, err
	}
	return plugincontract.Bundle{Manifest: parsed, Canonical: canonical, Files: files}, nil
}

func mcpHost(rawURL string) string {
	endpoint, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(endpoint.Hostname(), "."))
}
