package marketcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

const (
	apiPrefix       = "/market/v1/"
	resListingsList = "listings:list"
	resListingsGet  = "listings:get"
	maxBodyBytes    = 1 << 20

	versionsPrefix = "listings/"
	versionsSuffix = "/versions:get"
)

func versionsResource(id ListingID) string {
	return versionsPrefix + url.PathEscape(string(id)) + versionsSuffix
}

func filterByTk(key string) url.Values {
	return url.Values{"filterByTk": {key}}
}

func listQuery(q BrowseQuery) url.Values {
	v := url.Values{
		"page":     {fmt.Sprint(q.Page)},
		"pageSize": {fmt.Sprint(q.PageSize)},
	}
	if q.Text != "" {
		filter, _ := json.Marshal(map[string]any{
			"$or": []map[string]any{
				{"name": map[string]string{"$includes": q.Text}},
				{"summary": map[string]string{"$includes": q.Text}},
			},
		})
		v.Set("filter", string(filter))
	}
	return v
}

type wireEnvelope[T any] struct {
	Data   T           `json:"data"`
	Meta   *wireMeta   `json:"meta,omitempty"`
	Errors []wireError `json:"errors,omitempty"`
}

type wireMeta struct {
	Count     int `json:"count"`
	Page      int `json:"page"`
	PageSize  int `json:"pageSize"`
	TotalPage int `json:"totalPage"`
}

type wireError struct {
	Message string `json:"message"`
}

type wireListing struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Summary       string `json:"summary"`
	Publisher     string `json:"publisher"`
	IconURL       string `json:"iconUrl"`
	LatestVersion string `json:"latestVersion"`
}

type wireVersion struct {
	Version        string       `json:"version"`
	Description    string       `json:"description"`
	Manifest       wireManifest `json:"manifest"`
	PolicyVersion  string       `json:"policyVersion"`
	TargetRevision int64        `json:"targetRevision"`
	ReleaseDigest  string       `json:"releaseDigest"`
	ReleaseState   string       `json:"releaseState"`
}

type wireManifest struct {
	Agent        *wireAgent        `json:"agent"`
	Requirements []wireRequirement `json:"requirements"`
}

type wireAgent struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Instructions string `json:"instructions"`
}

type wireRequirement struct {
	Kind     string           `json:"kind"`
	Name     string           `json:"name,omitempty"`
	Provider string           `json:"provider,omitempty"`
	Models   []string         `json:"models,omitempty"`
	Toolkit  string           `json:"toolkit,omitempty"`
	Source   string           `json:"source,omitempty"`
	Template *wireMcpTemplate `json:"template,omitempty"`
	Bundle   *wireSkillBundle `json:"bundle,omitempty"`
}

type wireMcpTemplate struct {
	Transport string `json:"transport"`
	URL       string `json:"url"`
}

type wireSkillBundle struct {
	Description string `json:"description"`
	Content     string `json:"content"`
}

const (
	kindModel      = "model"
	kindTool       = "tool"
	kindConnection = "connection"
	kindKnowledge  = "knowledge"
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidListing, fmt.Sprintf(format, args...))
}

func catalogErrors(errs []wireError) error {
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: catalog reported %q", ErrCatalogUnavailable, errs[0].Message)
}

func decodePage(body []byte) (ListingPage, error) {
	var env wireEnvelope[[]json.RawMessage]
	if err := json.Unmarshal(body, &env); err != nil {
		return ListingPage{}, invalid("list envelope: %v", err)
	}
	if err := catalogErrors(env.Errors); err != nil {
		return ListingPage{}, err
	}
	if env.Data == nil {
		return ListingPage{}, invalid("list envelope has no data array")
	}
	page := ListingPage{Items: []ListingSummary{}}
	for i, raw := range env.Data {
		var w wireListing
		if err := json.Unmarshal(raw, &w); err != nil {
			slog.Warn("marketcatalog: dropped unreadable listing", "index", i, "error", err)
			continue
		}
		s, err := parseSummary(w)
		if err != nil {
			slog.Warn("marketcatalog: dropped invalid listing", "index", i, "error", err)
			continue
		}
		page.Items = append(page.Items, s)
	}
	page.Total = len(page.Items)
	if env.Meta != nil {
		page.Total = env.Meta.Count
	}
	return page, nil
}

func decodeSummary(body []byte) (ListingSummary, error) {
	var env wireEnvelope[*wireListing]
	if err := json.Unmarshal(body, &env); err != nil {
		return ListingSummary{}, invalid("listing envelope: %v", err)
	}
	if err := catalogErrors(env.Errors); err != nil {
		return ListingSummary{}, err
	}
	if env.Data == nil {
		return ListingSummary{}, ErrListingNotFound
	}
	return parseSummary(*env.Data)
}

func decodeVersion(body []byte) (Version, string, Manifest, error) {
	var env wireEnvelope[*wireVersion]
	if err := json.Unmarshal(body, &env); err != nil {
		return "", "", Manifest{}, invalid("version envelope: %v", err)
	}
	if err := catalogErrors(env.Errors); err != nil {
		return "", "", Manifest{}, err
	}
	if env.Data == nil {
		return "", "", Manifest{}, ErrListingNotFound
	}
	v, err := ParseVersion(env.Data.Version)
	if err != nil {
		return "", "", Manifest{}, invalid("%v", err)
	}
	m, err := parseManifest(env.Data.Manifest)
	if err != nil {
		return "", "", Manifest{}, err
	}
	return v, env.Data.Description, m, nil
}

func decodeReleaseFacts(body []byte) (ReleaseFacts, error) {
	var env wireEnvelope[*wireVersion]
	if err := json.Unmarshal(body, &env); err != nil {
		return ReleaseFacts{}, invalid("version envelope: %v", err)
	}
	if env.Data == nil {
		return ReleaseFacts{}, nil
	}
	return ReleaseFacts{
		PolicyVersion:  env.Data.PolicyVersion,
		TargetRevision: env.Data.TargetRevision,
		ReleaseDigest:  env.Data.ReleaseDigest,
		ReleaseState:   env.Data.ReleaseState,
	}, nil
}

func parseSummary(w wireListing) (ListingSummary, error) {
	id, err := ParseListingID(w.ID)
	if err != nil {
		return ListingSummary{}, invalid("%v", err)
	}
	latest, err := ParseVersion(w.LatestVersion)
	if err != nil {
		return ListingSummary{}, invalid("listing %s: %v", id, err)
	}
	if blank(w.Name) {
		return ListingSummary{}, invalid("listing %s has no name", id)
	}
	icon := ""
	if u, err := url.Parse(w.IconURL); err == nil && u.Scheme == "https" && u.Host != "" {
		icon = w.IconURL
	}
	return ListingSummary{
		ID:            id,
		Name:          w.Name,
		Summary:       w.Summary,
		Publisher:     w.Publisher,
		IconURL:       icon,
		LatestVersion: latest,
	}, nil
}

func blank(s string) bool { return strings.TrimSpace(s) == "" }

type knowledgeKey struct {
	source KnowledgeSource
	name   string
}

func parseManifest(w wireManifest) (Manifest, error) {
	if w.Agent == nil || blank(w.Agent.Name) {
		return Manifest{}, invalid("manifest has no agent name")
	}
	m := Manifest{Agent: AgentTemplate{
		Name:         w.Agent.Name,
		Description:  w.Agent.Description,
		Instructions: w.Agent.Instructions,
	}}
	haveModel := false
	seenTool := map[string]bool{}
	seenConnection := map[ConnectionNeed]bool{}
	seenKnowledge := map[knowledgeKey]bool{}
	seenKind := map[string]bool{}
	for i, r := range w.Requirements {
		switch r.Kind {
		case kindModel:
			if haveModel {
				return Manifest{}, invalid("requirement %d: more than one model", i)
			}
			if blank(r.Provider) {
				return Manifest{}, invalid("requirement %d: model has no provider", i)
			}
			for _, id := range r.Models {
				if blank(id) {
					return Manifest{}, invalid("requirement %d: empty model id", i)
				}
			}
			haveModel = true
			m.Model = ModelNeed{Provider: r.Provider}
			if len(r.Models) > 0 {
				m.Model.Models = r.Models
			}
		case kindTool:
			if blank(r.Name) {
				return Manifest{}, invalid("requirement %d: tool has no name", i)
			}
			need := ToolNeed{ServerName: r.Name}
			if r.Template != nil {
				if blank(r.Template.Transport) || blank(r.Template.URL) {
					return Manifest{}, invalid("requirement %d: tool template needs transport and url", i)
				}
				need.Template = &McpTemplate{Transport: r.Template.Transport, URL: r.Template.URL}
			}
			if !seenTool[need.ServerName] {
				seenTool[need.ServerName] = true
				m.Tools = append(m.Tools, need)
			}
		case kindConnection:
			if blank(r.Provider) || blank(r.Toolkit) {
				return Manifest{}, invalid("requirement %d: connection needs provider and toolkit", i)
			}
			need := ConnectionNeed{Provider: ConnectionProvider(r.Provider), Toolkit: r.Toolkit}
			if !seenConnection[need] {
				seenConnection[need] = true
				m.Connections = append(m.Connections, need)
			}
		case kindKnowledge:
			if blank(r.Source) || blank(r.Name) {
				return Manifest{}, invalid("requirement %d: knowledge needs source and name", i)
			}
			need := KnowledgeNeed{Source: KnowledgeSource(r.Source), Name: r.Name}
			if r.Bundle != nil {
				if blank(r.Bundle.Content) {
					return Manifest{}, invalid("requirement %d: knowledge bundle has no content", i)
				}
				need.Bundle = &SkillBundle{Description: r.Bundle.Description, Content: r.Bundle.Content}
			}
			key := knowledgeKey{source: need.Source, name: need.Name}
			if !seenKnowledge[key] {
				seenKnowledge[key] = true
				m.Knowledge = append(m.Knowledge, need)
			}
		case "":
			return Manifest{}, invalid("requirement %d has no kind", i)
		default:
			if !seenKind[r.Kind] {
				seenKind[r.Kind] = true
				m.Unsupported = append(m.Unsupported, UnsupportedNeed{Kind: r.Kind, Name: r.Name})
			}
		}
	}
	if !haveModel {
		return Manifest{}, invalid("manifest has no model requirement")
	}
	return m, nil
}

func encodeSummary(s ListingSummary) wireListing {
	return wireListing{
		ID:            string(s.ID),
		Name:          s.Name,
		Summary:       s.Summary,
		Publisher:     s.Publisher,
		IconURL:       s.IconURL,
		LatestVersion: string(s.LatestVersion),
	}
}

func encodeVersion(l Listing) wireVersion {
	return wireVersion{
		Version:     string(l.Version),
		Description: l.Description,
		Manifest:    encodeManifest(l.Manifest),
	}
}

func encodeManifest(m Manifest) wireManifest {
	reqs := []wireRequirement{{Kind: kindModel, Provider: m.Model.Provider, Models: m.Model.Models}}
	for _, t := range m.Tools {
		r := wireRequirement{Kind: kindTool, Name: t.ServerName}
		if t.Template != nil {
			r.Template = &wireMcpTemplate{Transport: t.Template.Transport, URL: t.Template.URL}
		}
		reqs = append(reqs, r)
	}
	for _, c := range m.Connections {
		reqs = append(reqs, wireRequirement{Kind: kindConnection, Provider: string(c.Provider), Toolkit: c.Toolkit})
	}
	for _, k := range m.Knowledge {
		r := wireRequirement{Kind: kindKnowledge, Source: string(k.Source), Name: k.Name}
		if k.Bundle != nil {
			r.Bundle = &wireSkillBundle{Description: k.Bundle.Description, Content: k.Bundle.Content}
		}
		reqs = append(reqs, r)
	}
	for _, u := range m.Unsupported {
		reqs = append(reqs, wireRequirement{Kind: u.Kind, Name: u.Name})
	}
	return wireManifest{
		Agent: &wireAgent{
			Name:         m.Agent.Name,
			Description:  m.Agent.Description,
			Instructions: m.Agent.Instructions,
		},
		Requirements: reqs,
	}
}

type nocobaseHTTP struct {
	base   *url.URL
	token  string
	client *http.Client
}

func (n nocobaseHTTP) get(ctx context.Context, resource string, q url.Values) ([]byte, error) {
	u := *n.base
	u.Path = strings.TrimRight(n.base.Path, "/") + apiPrefix + resource
	u.RawPath = ""
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCatalogUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	if n.token != "" {
		req.Header.Set("Authorization", "Bearer "+n.token)
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCatalogUnavailable, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrListingNotFound
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return nil, fmt.Errorf("%w: %s answered %d", ErrCatalogUnavailable, resource, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCatalogUnavailable, err)
	}
	if len(body) > maxBodyBytes {
		return nil, invalid("%s body exceeds %d bytes", resource, maxBodyBytes)
	}
	return body, nil
}

type fixtureFetcher struct {
	listings []Listing
}

func (f fixtureFetcher) get(_ context.Context, resource string, q url.Values) ([]byte, error) {
	key := q.Get("filterByTk")
	switch {
	case resource == resListingsList:
		seen := map[ListingID]bool{}
		items := []wireListing{}
		for _, l := range f.listings {
			if !seen[l.Summary.ID] {
				seen[l.Summary.ID] = true
				items = append(items, encodeSummary(l.Summary))
			}
		}
		meta := &wireMeta{Count: len(items), Page: 1, PageSize: len(items), TotalPage: 1}
		return json.Marshal(wireEnvelope[[]wireListing]{Data: items, Meta: meta})
	case resource == resListingsGet:
		for _, l := range f.listings {
			if string(l.Summary.ID) == key {
				w := encodeSummary(l.Summary)
				return json.Marshal(wireEnvelope[*wireListing]{Data: &w})
			}
		}
		return json.Marshal(wireEnvelope[*wireListing]{})
	case strings.HasPrefix(resource, versionsPrefix) && strings.HasSuffix(resource, versionsSuffix):
		id, err := url.PathUnescape(strings.TrimSuffix(strings.TrimPrefix(resource, versionsPrefix), versionsSuffix))
		if err != nil {
			return nil, ErrListingNotFound
		}
		for _, l := range f.listings {
			if string(l.Summary.ID) == id && string(l.Version) == key {
				w := encodeVersion(l)
				return json.Marshal(wireEnvelope[*wireVersion]{Data: &w})
			}
		}
		return json.Marshal(wireEnvelope[*wireVersion]{})
	}
	return nil, ErrListingNotFound
}

type brokenFetcher struct{ err error }

func (b brokenFetcher) get(context.Context, string, url.Values) ([]byte, error) {
	return nil, fmt.Errorf("%w: %v", ErrCatalogUnavailable, b.err)
}
