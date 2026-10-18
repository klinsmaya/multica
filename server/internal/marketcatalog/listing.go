package marketcatalog

import (
	"errors"
	"fmt"
	"regexp"
)

type ListingID string

var listingIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func ParseListingID(s string) (ListingID, error) {
	if !listingIDPattern.MatchString(s) {
		return "", fmt.Errorf("marketcatalog: invalid listing id %q", s)
	}
	return ListingID(s), nil
}

type Version string

var versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+-]{0,63}$`)

func ParseVersion(s string) (Version, error) {
	if !versionPattern.MatchString(s) {
		return "", fmt.Errorf("marketcatalog: invalid version %q", s)
	}
	return Version(s), nil
}

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
	IconURL       string
	LatestVersion Version
}

type ListingPage struct {
	Source   Source
	Items    []ListingSummary
	Page     int
	PageSize int
	Total    int
}

type Listing struct {
	Source      Source
	Summary     ListingSummary
	Version     Version
	Description string
	Manifest    Manifest
}

type Manifest struct {
	Agent       AgentTemplate
	Model       ModelNeed
	Tools       []ToolNeed
	Connections []ConnectionNeed
	Knowledge   []KnowledgeNeed
	Unsupported []UnsupportedNeed
}

type AgentTemplate struct {
	Name         string
	Description  string
	Instructions string
}

type ModelNeed struct {
	Provider string
	Models   []string
}

type ToolNeed struct {
	ServerName string
	Template   *McpTemplate
}

type McpTemplate struct {
	Transport string
	URL       string
}

type ConnectionProvider string

const ConnectionComposio ConnectionProvider = "composio"

type ConnectionNeed struct {
	Provider ConnectionProvider
	Toolkit  string
}

type KnowledgeSource string

const KnowledgeSkill KnowledgeSource = "skill"

type KnowledgeNeed struct {
	Source KnowledgeSource
	Name   string
	Bundle *SkillBundle
}

type SkillBundle struct {
	Description string
	Content     string
}

type UnsupportedNeed struct {
	Kind string
	Name string
}

var (
	ErrListingNotFound    = errors.New("marketcatalog: listing not found")
	ErrCatalogUnavailable = errors.New("marketcatalog: catalog unavailable")
	ErrInvalidListing     = errors.New("marketcatalog: catalog sent an invalid listing")
)
