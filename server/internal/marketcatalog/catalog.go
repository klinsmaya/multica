package marketcatalog

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultTimeout = 10 * time.Second

type Config struct {
	BaseURL    string
	Token      string
	Timeout    time.Duration
	HTTPClient *http.Client
}

type Client struct {
	source Source
	fetch  fetcher
}

type fetcher interface {
	get(ctx context.Context, resource string, query url.Values) ([]byte, error)
}

func New(cfg Config) *Client {
	raw := strings.TrimSpace(cfg.BaseURL)
	if raw == "" {
		return NewFixture(sampleListings...)
	}
	base, err := url.Parse(raw)
	if err != nil {
		return &Client{source: SourceNocoBase, fetch: brokenFetcher{err: err}}
	}
	if (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return &Client{source: SourceNocoBase, fetch: brokenFetcher{err: fmt.Errorf("catalog URL needs an http or https host")}}
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = defaultTimeout
		}
		httpClient = &http.Client{Timeout: timeout}
	}
	return &Client{
		source: SourceNocoBase,
		fetch:  nocobaseHTTP{base: base, token: cfg.Token, client: httpClient},
	}
}

func NewFixture(listings ...Listing) *Client {
	return &Client{source: SourceStub, fetch: fixtureFetcher{listings: listings}}
}

func FixtureHandler(listings ...Listing) http.Handler {
	f := fixtureFetcher{listings: listings}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resource, ok := strings.CutPrefix(r.URL.Path, apiPrefix)
		if !ok || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		body, err := f.get(r.Context(), resource, r.URL.Query())
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
}

type BrowseQuery struct {
	Text     string
	Page     int
	PageSize int
}

const (
	defaultPageSize = 24
	maxPageSize     = 50
)

func (q BrowseQuery) clamped() BrowseQuery {
	q.Text = strings.TrimSpace(q.Text)
	if q.Page < 1 {
		q.Page = 1
	}
	switch {
	case q.PageSize == 0:
		q.PageSize = defaultPageSize
	case q.PageSize < 1:
		q.PageSize = 1
	case q.PageSize > maxPageSize:
		q.PageSize = maxPageSize
	}
	return q
}

func (c *Client) Browse(ctx context.Context, q BrowseQuery) (ListingPage, error) {
	q = q.clamped()
	body, err := c.fetch.get(ctx, resListingsList, listQuery(q))
	if errors.Is(err, ErrListingNotFound) {
		return ListingPage{}, fmt.Errorf("%w: list resource answered not found", ErrCatalogUnavailable)
	}
	if err != nil {
		return ListingPage{}, err
	}
	page, err := decodePage(body)
	if err != nil {
		return ListingPage{}, err
	}
	page.Source = c.source
	page.Page = q.Page
	page.PageSize = q.PageSize
	return page, nil
}

func (c *Client) Listing(ctx context.Context, id ListingID, v Version) (Listing, error) {
	body, err := c.fetch.get(ctx, resListingsGet, filterByTk(string(id)))
	if err != nil {
		return Listing{}, err
	}
	summary, err := decodeSummary(body)
	if err != nil {
		return Listing{}, err
	}
	if summary.ID != id {
		return Listing{}, fmt.Errorf("%w: asked for %s, got %s", ErrInvalidListing, id, summary.ID)
	}
	if v == "" {
		v = summary.LatestVersion
	}
	body, err = c.fetch.get(ctx, versionsResource(id), filterByTk(string(v)))
	if err != nil {
		return Listing{}, err
	}
	got, description, manifest, err := decodeVersion(body)
	if err != nil {
		return Listing{}, err
	}
	if got != v {
		return Listing{}, fmt.Errorf("%w: asked for version %s, got %s", ErrInvalidListing, v, got)
	}
	return Listing{
		Source:      c.source,
		Summary:     summary,
		Version:     v,
		Description: description,
		Manifest:    manifest,
	}, nil
}
