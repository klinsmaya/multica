package market

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrNotFound is a missing catalog resource. Callers map it to 404.
var ErrNotFound = errors.New("market resource not found")

// Resource is one catalog entry. ResourceID is publisher/name.
type Resource struct {
	ResourceID    string   `json:"resourceId"`
	Name          string   `json:"name"`
	PublisherSlug string   `json:"publisherSlug"`
	Kind          string   `json:"kind"`
	Description   string   `json:"description"`
	SourceKey     string   `json:"sourceKey"`
	Demands       []Demand `json:"demands"`
}

// Source lists and loads catalog resources. The stub is the default.
// The HTTP source talks to a NocoBase market only when a base URL is set.
type Source interface {
	List(ctx context.Context) ([]Resource, error)
	Get(ctx context.Context, resourceID string) (Resource, error)
}

const (
	stubSkillID     = "multica/workspace-skill"
	stubKnowledgeID = "multica/workspace-notes"
)

// StubSource is the in-memory catalog used when no base URL is configured
// and in tests. It does not call the network.
type StubSource struct{}

func (StubSource) List(ctx context.Context) ([]Resource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return stubResources(), nil
}

func (StubSource) Get(ctx context.Context, resourceID string) (Resource, error) {
	if err := ctx.Err(); err != nil {
		return Resource{}, err
	}
	for _, resource := range stubResources() {
		if resource.ResourceID == resourceID {
			return resource, nil
		}
	}
	return Resource{}, ErrNotFound
}

func stubResources() []Resource {
	return []Resource{
		{
			ResourceID:    stubSkillID,
			Name:          "Workspace skill",
			PublisherSlug: "multica",
			Kind:          "skill",
			Description:   "One demand of each kind.",
			SourceKey:     "stub",
			Demands: []Demand{
				{Kind: KindModel, Ref: "claude-sonnet", Scope: ScopeReadOnly},
				{Kind: KindTool, Ref: "skill:workspace-skill", Scope: ScopeReadOnly},
				{Kind: KindConnection, Ref: "00000000-0000-4000-8000-000000000001", Scope: ScopeReadOnly},
				{Kind: KindKnowledge, Ref: "workspace-notes", Scope: ScopeReadOnly},
			},
		},
		{
			ResourceID:    stubKnowledgeID,
			Name:          "Workspace notes",
			PublisherSlug: "multica",
			Kind:          "knowledge",
			Description:   "No knowledge grant in this workspace.",
			SourceKey:     "stub",
			Demands: []Demand{
				{Kind: KindKnowledge, Ref: "workspace-notes", Scope: ScopeReadOnly},
			},
		},
	}
}

// HTTPSource reads NocoBase `{data: ...}` list and get endpoints.
type HTTPSource struct {
	BaseURL string
	Client  *http.Client
}

// NewSource returns the stub when baseURL is empty.
func NewSource(baseURL string) Source {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return StubSource{}
	}
	return &HTTPSource{
		BaseURL: baseURL,
		Client:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *HTTPSource) List(ctx context.Context) ([]Resource, error) {
	body, err := s.get(ctx, s.BaseURL+"/api/marketResources:list")
	if err != nil {
		return nil, err
	}
	var rows []wireResource
	if err := decodeData(body, &rows); err != nil {
		return nil, err
	}
	out := make([]Resource, 0, len(rows))
	for _, row := range rows {
		resource, err := row.resource()
		if err != nil {
			return nil, err
		}
		out = append(out, resource)
	}
	return out, nil
}

func (s *HTTPSource) Get(ctx context.Context, resourceID string) (Resource, error) {
	endpoint := s.BaseURL + "/api/marketResources:get?filterByTk=" + url.QueryEscape(resourceID)
	body, err := s.get(ctx, endpoint)
	if err != nil {
		return Resource{}, err
	}
	var row wireResource
	if err := decodeData(body, &row); err != nil {
		return Resource{}, err
	}
	return row.resource()
}

func (s *HTTPSource) get(ctx context.Context, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("market catalog status %d", resp.StatusCode)
	}
	return payload, nil
}

type wireResource struct {
	ResourceID    string   `json:"resourceId"`
	Name          string   `json:"name"`
	PublisherSlug string   `json:"publisherSlug"`
	Kind          string   `json:"kind"`
	Description   string   `json:"description"`
	SourceKey     string   `json:"sourceKey"`
	Demands       []Demand `json:"demands"`
}

func (w wireResource) resource() (Resource, error) {
	if w.ResourceID == "" || w.Kind == "" {
		return Resource{}, errors.New("market catalog resource is missing resourceId or kind")
	}
	demands := w.Demands
	if demands == nil {
		demands = []Demand{}
	}
	for _, demand := range demands {
		if demand.Kind == "" || demand.Ref == "" || demand.Scope == "" {
			return Resource{}, errors.New("market catalog demand is incomplete")
		}
	}
	return Resource{
		ResourceID:    w.ResourceID,
		Name:          w.Name,
		PublisherSlug: w.PublisherSlug,
		Kind:          w.Kind,
		Description:   w.Description,
		SourceKey:     w.SourceKey,
		Demands:       demands,
	}, nil
}

func decodeData(body []byte, dest any) error {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("market catalog response: %w", err)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return errors.New("market catalog response has no data")
	}
	if err := json.Unmarshal(envelope.Data, dest); err != nil {
		return fmt.Errorf("market catalog response: %w", err)
	}
	return nil
}
