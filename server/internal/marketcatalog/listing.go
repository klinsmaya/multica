// Package marketcatalog reads agent listings from the Market catalog. It parses
// the catalog's wire shape into the domain types below and decides nothing
// about permissions.
package marketcatalog

import (
	"errors"
	"fmt"
	"regexp"
)

// ListingID is the catalog's key for a listing. ParseListingID is the only
// way to make one, so a handler cannot pass a raw path segment to the catalog.
type ListingID string

var listingIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func ParseListingID(s string) (ListingID, error) {
	if !listingIDPattern.MatchString(s) {
		return "", fmt.Errorf("marketcatalog: invalid listing id %q", s)
	}
	return ListingID(s), nil
}

// Version is one exact listing version. Preflight always pins one.
type Version string

var versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+-]{0,63}$`)

func ParseVersion(s string) (Version, error) {
	if !versionPattern.MatchString(s) {
		return "", fmt.Errorf("marketcatalog: invalid version %q", s)
	}
	return Version(s), nil
}

// Source says where a listing came from. Every value a Client returns carries
// it, so a sample listing can never look live.
type Source string

const (
	SourceNocoBase Source = "nocobase"
	SourceStub     Source = "stub"
)

type ListingSummary struct {
	ID            ListingID
	Name          string
	Summary       string
	Publisher     string
	IconURL       string // https only. "" when the catalog sent none or anything else.
	LatestVersion Version
}

type ListingPage struct {
	Source   Source
	Items    []ListingSummary // items that failed to parse are dropped, not zero-valued
	Page     int
	PageSize int
	Total    int
}

// Listing is one version of one listing. Manifest always describes Version.
type Listing struct {
	Source      Source
	Summary     ListingSummary
	Version     Version
	Description string // markdown, per version
	Manifest    Manifest
}

// Manifest is one installable agent. An agent runs on one runtime, so Model is
// a value. Tools are unique by server name, connections by provider and
// toolkit, knowledge by source and name, and Unsupported by kind.
// Requirements of a kind the adapter does not know land in Unsupported
// instead of being dropped.
type Manifest struct {
	Agent       AgentTemplate
	Model       ModelNeed
	Tools       []ToolNeed
	Connections []ConnectionNeed
	Knowledge   []KnowledgeNeed
	Unsupported []UnsupportedNeed
}

type AgentTemplate struct {
	Name         string // never ""
	Description  string
	Instructions string
}

type ModelNeed struct {
	Provider string   // matches db.AgentRuntime.Provider. Never "".
	Models   []string // acceptable ids in preference order. Empty means the runtime default is fine.
}

// ToolNeed is a workspace MCP library entry the agent needs attached.
type ToolNeed struct {
	ServerName string       // never ""
	Template   *McpTemplate // nil means the listing cannot create the entry
}

// McpTemplate carries no secrets. Collecting secret inputs belongs to M03.
type McpTemplate struct {
	Transport string
	URL       string
}

type ConnectionProvider string

const ConnectionComposio ConnectionProvider = "composio"

// ConnectionNeed keeps any provider the catalog names. Preflight blocks every
// provider except ConnectionComposio, because Multica has no other connection
// resource to check.
type ConnectionNeed struct {
	Provider ConnectionProvider
	Toolkit  string // never ""
}

type KnowledgeSource string

const KnowledgeSkill KnowledgeSource = "skill"

// KnowledgeNeed keeps any source the catalog names. Preflight blocks every
// source except KnowledgeSkill, because no other knowledge store has an ACL.
type KnowledgeNeed struct {
	Source KnowledgeSource
	Name   string       // skill name when Source is KnowledgeSkill. Never "".
	Bundle *SkillBundle // nil means the skill must already exist
}

type SkillBundle struct {
	Description string
	Content     string
}

type UnsupportedNeed struct {
	Kind string // the wire kind, such as "squad" or "credential"
	Name string
}

var (
	ErrListingNotFound    = errors.New("marketcatalog: listing not found")
	ErrCatalogUnavailable = errors.New("marketcatalog: catalog unavailable")
	ErrInvalidListing     = errors.New("marketcatalog: catalog sent an invalid listing")
)
