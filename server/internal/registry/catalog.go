package registry

import (
	"encoding/json"
	"strings"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Catalog formats. The public read API serves standards where one exists, so
// clients that already speak them need no adapter: marketplace.json for Claude
// Code, and our own listing JSON for everything a standard does not carry
// (enforced, changelog, review state).

// Marketplace is a Claude Code marketplace.json document.
type Marketplace struct {
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Owner       Person             `json:"owner"`
	Plugins     []MarketplaceEntry `json:"plugins"`
}

type MarketplaceEntry struct {
	Name        string            `json:"name"`
	Source      MarketplaceSource `json:"source"`
	Version     string            `json:"version"`
	DisplayName string            `json:"displayName,omitempty"`
	Description string            `json:"description,omitempty"`
	Author      *Person           `json:"author,omitempty"`
	Category    string            `json:"category,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Keywords    []string          `json:"keywords,omitempty"`
	Homepage    string            `json:"homepage,omitempty"`
	License     string            `json:"license,omitempty"`
}

// MarketplaceSource is Claude Code's `archive` plugin source: a zip over
// HTTPS pinned by sha256.
type MarketplaceSource struct {
	Source string `json:"source"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// MarketplaceName turns "@self" into a name Claude Code accepts as a
// marketplace identifier.
func MarketplaceName(namespace string) string {
	return "multica-" + strings.TrimPrefix(NormalizeNamespace(namespace), "@")
}

// BundlePath is where one version's archive is served, relative to the
// registry API root.
func BundlePath(namespace, name, version string) string {
	return "/listings/" + NormalizeNamespace(namespace) + "/" + name + "/versions/" + version + "/bundle"
}

// BuildMarketplace renders the visible listings of one namespace. apiRoot is
// the absolute registry root (…/registry/v1) bundle URLs hang off.
func BuildMarketplace(namespace string, rows []db.ListVisibleRegistryListingsRow, apiRoot string) Marketplace {
	out := Marketplace{
		Name:        MarketplaceName(namespace),
		Description: "Multica template marketplace " + NormalizeNamespace(namespace),
		Owner:       Person{Name: "Multica"},
		Plugins:     make([]MarketplaceEntry, 0, len(rows)),
	}
	for _, row := range rows {
		var manifest PluginManifest
		_ = json.Unmarshal(row.Manifest, &manifest)
		out.Plugins = append(out.Plugins, MarketplaceEntry{
			// Claude Code requires the entry name to equal plugin.json's name.
			Name: row.Name,
			Source: MarketplaceSource{
				Source: "archive",
				URL:    strings.TrimRight(apiRoot, "/") + BundlePath(row.Namespace, row.Name, row.Version),
				SHA256: row.Sha256,
			},
			Version:     row.Version,
			DisplayName: row.DisplayName,
			Description: row.Description,
			Author:      manifest.Author,
			Category:    row.Category,
			Tags:        row.Tags,
			Keywords:    manifest.Keywords,
			Homepage:    manifest.Homepage,
			License:     manifest.License,
		})
	}
	return out
}

// Listing is one entry of the registry's own listing format.
type Listing struct {
	ID          string   `json:"id"`
	Namespace   string   `json:"namespace"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Tags        []string `json:"tags"`
	SourceKind  string   `json:"source_kind"`
	Public      bool     `json:"public"`
	Enforced    bool     `json:"enforced"`
	Version     Version  `json:"version"`
}

// Version is the newest approved version of a listing, with what a consumer
// needs to decide whether it can install it before downloading anything.
type Version struct {
	ID          string          `json:"id"`
	Version     string          `json:"version"`
	SHA256      string          `json:"sha256"`
	SizeBytes   int64           `json:"size_bytes"`
	Changelog   string          `json:"changelog"`
	PublishedAt string          `json:"published_at"`
	BundleURL   string          `json:"bundle_url"`
	Manifest    json.RawMessage `json:"manifest"`
}

func BuildListings(rows []db.ListVisibleRegistryListingsRow, apiRoot string) []Listing {
	out := make([]Listing, 0, len(rows))
	for _, row := range rows {
		tags := row.Tags
		if tags == nil {
			tags = []string{}
		}
		out = append(out, Listing{
			ID:          uuidString(row.ID),
			Namespace:   row.Namespace,
			Name:        row.Name,
			Kind:        row.Kind,
			DisplayName: row.DisplayName,
			Description: row.Description,
			Category:    row.Category,
			Tags:        tags,
			SourceKind:  row.SourceKind,
			Public:      row.Public,
			Enforced:    row.Enforced,
			Version: Version{
				ID:          uuidString(row.VersionID),
				Version:     row.Version,
				SHA256:      row.Sha256,
				SizeBytes:   row.SizeBytes,
				Changelog:   row.Changelog,
				PublishedAt: row.VersionCreatedAt.Time.UTC().Format(time.RFC3339),
				BundleURL:   strings.TrimRight(apiRoot, "/") + BundlePath(row.Namespace, row.Name, row.Version),
				Manifest:    json.RawMessage(row.Manifest),
			},
		})
	}
	return out
}
