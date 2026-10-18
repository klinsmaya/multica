package marketcatalog

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestSamplesRoundTrip(t *testing.T) {
	stub := NewFixture(sampleListings...)
	live := httptest.NewServer(FixtureHandler(sampleListings...))
	t.Cleanup(live.Close)
	overHTTP := New(Config{BaseURL: live.URL})

	for _, want := range sampleListings {
		got, err := stub.Listing(context.Background(), want.Summary.ID, want.Version)
		if err != nil {
			t.Fatalf("stub %s@%s: %v", want.Summary.ID, want.Version, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("stub %s@%s round trip\n got %+v\nwant %+v", want.Summary.ID, want.Version, got, want)
		}

		got, err = overHTTP.Listing(context.Background(), want.Summary.ID, want.Version)
		if err != nil {
			t.Fatalf("http %s@%s: %v", want.Summary.ID, want.Version, err)
		}
		if got.Source != SourceNocoBase {
			t.Fatalf("http source = %s, want %s", got.Source, SourceNocoBase)
		}
		got.Source = want.Source
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("http %s@%s round trip\n got %+v\nwant %+v", want.Summary.ID, want.Version, got, want)
		}
	}
}

func TestNewWithoutURLServesStub(t *testing.T) {
	c := New(Config{})
	page, err := c.Browse(context.Background(), BrowseQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Source != SourceStub {
		t.Fatalf("source = %s, want stub", page.Source)
	}
	if len(page.Items) != 3 {
		t.Fatalf("items = %d, want one per sample id", len(page.Items))
	}
	l, err := c.Listing(context.Background(), "sample.pr-reviewer", "")
	if err != nil {
		t.Fatal(err)
	}
	if l.Version != "1.2.0" || l.Source != SourceStub {
		t.Fatalf("latest = %s from %s, want 1.2.0 from stub", l.Version, l.Source)
	}
}

func TestNewWithBadURLIsUnavailableNotStub(t *testing.T) {
	for _, raw := range []string{"ftp://catalog.example", "://nope", "/relative"} {
		c := New(Config{BaseURL: raw})
		_, err := c.Browse(context.Background(), BrowseQuery{})
		if !errors.Is(err, ErrCatalogUnavailable) {
			t.Fatalf("%q: err = %v, want ErrCatalogUnavailable", raw, err)
		}
		if c.source != SourceNocoBase {
			t.Fatalf("%q: source = %s, want nocobase", raw, c.source)
		}
	}
}

const validVersionBody = `{"data":{"version":"1.0.0","description":"d","manifest":{"agent":{"name":"A"},"requirements":[{"kind":"model","provider":"claude"}]}}}`

func TestDecodeVersionRejectsMalformed(t *testing.T) {
	if _, _, _, err := decodeVersion([]byte(validVersionBody)); err != nil {
		t.Fatalf("baseline body: %v", err)
	}
	cases := map[string]string{
		"not json":               `{"data":`,
		"data wrong type":        `{"data":[]}`,
		"bad version":            `{"data":{"version":"","manifest":{"agent":{"name":"A"},"requirements":[{"kind":"model","provider":"claude"}]}}}`,
		"no agent":               `{"data":{"version":"1.0.0","manifest":{"requirements":[{"kind":"model","provider":"claude"}]}}}`,
		"blank agent name":       `{"data":{"version":"1.0.0","manifest":{"agent":{"name":" "},"requirements":[{"kind":"model","provider":"claude"}]}}}`,
		"no model":               `{"data":{"version":"1.0.0","manifest":{"agent":{"name":"A"},"requirements":[]}}}`,
		"two models":             `{"data":{"version":"1.0.0","manifest":{"agent":{"name":"A"},"requirements":[{"kind":"model","provider":"claude"},{"kind":"model","provider":"codex"}]}}}`,
		"model without provider": `{"data":{"version":"1.0.0","manifest":{"agent":{"name":"A"},"requirements":[{"kind":"model"}]}}}`,
		"empty model id":         `{"data":{"version":"1.0.0","manifest":{"agent":{"name":"A"},"requirements":[{"kind":"model","provider":"claude","models":[""]}]}}}`,
		"tool without name":      `{"data":{"version":"1.0.0","manifest":{"agent":{"name":"A"},"requirements":[{"kind":"model","provider":"claude"},{"kind":"tool"}]}}}`,
		"tool template no url":   `{"data":{"version":"1.0.0","manifest":{"agent":{"name":"A"},"requirements":[{"kind":"model","provider":"claude"},{"kind":"tool","name":"gh","template":{"transport":"http"}}]}}}`,
		"connection no toolkit":  `{"data":{"version":"1.0.0","manifest":{"agent":{"name":"A"},"requirements":[{"kind":"model","provider":"claude"},{"kind":"connection","provider":"composio"}]}}}`,
		"knowledge no source":    `{"data":{"version":"1.0.0","manifest":{"agent":{"name":"A"},"requirements":[{"kind":"model","provider":"claude"},{"kind":"knowledge","name":"x"}]}}}`,
		"bundle no content":      `{"data":{"version":"1.0.0","manifest":{"agent":{"name":"A"},"requirements":[{"kind":"model","provider":"claude"},{"kind":"knowledge","source":"skill","name":"x","bundle":{"description":"d"}}]}}}`,
		"requirement no kind":    `{"data":{"version":"1.0.0","manifest":{"agent":{"name":"A"},"requirements":[{"kind":"model","provider":"claude"},{"name":"x"}]}}}`,
		"models wrong type":      `{"data":{"version":"1.0.0","manifest":{"agent":{"name":"A"},"requirements":[{"kind":"model","provider":"claude","models":"claude-sonnet"}]}}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, _, err := decodeVersion([]byte(body))
			if !errors.Is(err, ErrInvalidListing) {
				t.Fatalf("err = %v, want ErrInvalidListing", err)
			}
		})
	}
}

func TestDecodeVersionNullDataIsNotFound(t *testing.T) {
	_, _, _, err := decodeVersion([]byte(`{"data":null}`))
	if !errors.Is(err, ErrListingNotFound) {
		t.Fatalf("err = %v, want ErrListingNotFound", err)
	}
}

func TestParseManifestKeepsUnknownNeedsAsBlockers(t *testing.T) {
	body := `{"data":{"version":"1.0.0","manifest":{"agent":{"name":"A"},"requirements":[
		{"kind":"model","provider":"claude"},
		{"kind":"connection","provider":"composio","toolkit":"github"},
		{"kind":"connection","provider":"openconnector","toolkit":"github"},
		{"kind":"knowledge","source":"skill","name":"notes"},
		{"kind":"knowledge","source":"hermes_memory","name":"notes"},
		{"kind":"tool","name":"gh"},
		{"kind":"tool","name":"gh","template":{"transport":"http","url":"https://x"}},
		{"kind":"squad","name":"a"},
		{"kind":"squad","name":"b"},
		{"kind":"credential","name":"c"}
	]}}}`
	_, _, m, err := decodeVersion([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Connections) != 2 {
		t.Fatalf("connections = %+v, want composio and openconnector both kept", m.Connections)
	}
	if len(m.Knowledge) != 2 {
		t.Fatalf("knowledge = %+v, want skill and hermes_memory both kept", m.Knowledge)
	}
	if len(m.Tools) != 1 || m.Tools[0].Template != nil {
		t.Fatalf("tools = %+v, want the first gh entry only", m.Tools)
	}
	want := []UnsupportedNeed{{Kind: "squad", Name: "a"}, {Kind: "credential", Name: "c"}}
	if !reflect.DeepEqual(m.Unsupported, want) {
		t.Fatalf("unsupported = %+v, want %+v", m.Unsupported, want)
	}
}

func TestDecodePageDropsInvalidItems(t *testing.T) {
	body := `{"data":[
		{"id":"good.one","name":"Good","latestVersion":"1.0.0","iconUrl":"https://cdn.example/i.png"},
		{"id":"","name":"No id","latestVersion":"1.0.0"},
		{"id":"no.version","name":"No version"},
		{"id":"http.icon","name":"Http icon","latestVersion":"1.0.0","iconUrl":"http://cdn.example/i.png"},
		"not an object"
	],"meta":{"count":5}}`
	page, err := decodePage([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("items = %+v, want good.one and http.icon", page.Items)
	}
	if page.Items[0].IconURL != "https://cdn.example/i.png" || page.Items[1].IconURL != "" {
		t.Fatalf("icons = %q, %q; want https kept and http dropped", page.Items[0].IconURL, page.Items[1].IconURL)
	}
	if page.Total != 5 {
		t.Fatalf("total = %d, want meta count 5", page.Total)
	}
	for _, broken := range []string{`{}`, `{"data":null}`, `[]`, `{"data":{}}`} {
		if _, err := decodePage([]byte(broken)); !errors.Is(err, ErrInvalidListing) {
			t.Fatalf("%s: err = %v, want ErrInvalidListing", broken, err)
		}
	}
}

func TestMalformedCatalogBodyIsNeverAListing(t *testing.T) {
	summary := `{"data":{"id":"acme.x","name":"X","latestVersion":"1.0.0"}}`
	versions := map[string]string{
		"truncated":           `{"data":{"version":"1.0.0","manifest":`,
		"missing model":       `{"data":{"version":"1.0.0","manifest":{"agent":{"name":"X"},"requirements":[]}}}`,
		"other version":       `{"data":{"version":"9.9.9","manifest":{"agent":{"name":"X"},"requirements":[{"kind":"model","provider":"claude"}]}}}`,
		"requirements object": `{"data":{"version":"1.0.0","manifest":{"agent":{"name":"X"},"requirements":{"kind":"model"}}}}`,
	}
	for name, versionBody := range versions {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/market/v1/listings:get":
					_, _ = w.Write([]byte(summary))
				case "/market/v1/listings/acme.x/versions:get":
					_, _ = w.Write([]byte(versionBody))
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(srv.Close)
			l, err := New(Config{BaseURL: srv.URL}).Listing(context.Background(), "acme.x", "1.0.0")
			if !errors.Is(err, ErrInvalidListing) {
				t.Fatalf("err = %v, want ErrInvalidListing", err)
			}
			if !reflect.DeepEqual(l, Listing{}) {
				t.Fatalf("listing = %+v, want zero value", l)
			}
		})
	}
}

func TestHTTPStatusMapping(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusNotFound, ErrListingNotFound},
		{http.StatusUnauthorized, ErrCatalogUnavailable},
		{http.StatusForbidden, ErrCatalogUnavailable},
		{http.StatusInternalServerError, ErrCatalogUnavailable},
		{http.StatusBadGateway, ErrCatalogUnavailable},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
		}))
		_, err := New(Config{BaseURL: srv.URL}).Listing(context.Background(), "acme.x", "")
		srv.Close()
		if !errors.Is(err, tc.want) {
			t.Fatalf("status %d: err = %v, want %v", tc.status, err, tc.want)
		}
	}
}

func TestBrowseNotFoundIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	_, err := New(Config{BaseURL: srv.URL}).Browse(context.Background(), BrowseQuery{})
	if !errors.Is(err, ErrCatalogUnavailable) {
		t.Fatalf("err = %v, want ErrCatalogUnavailable", err)
	}
}

func TestHTTPSendsTokenAndQuery(t *testing.T) {
	var gotAuth, gotFilter, gotPageSize string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotFilter = r.URL.Query().Get("filter")
		gotPageSize = r.URL.Query().Get("pageSize")
		_, _ = w.Write([]byte(`{"data":[],"meta":{"count":0}}`))
	}))
	t.Cleanup(srv.Close)
	c := New(Config{BaseURL: srv.URL + "/", Token: "secret-token"})
	page, err := c.Browse(context.Background(), BrowseQuery{Text: " review ", PageSize: 500})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer secret-token" {
		t.Fatalf("authorization = %q", gotAuth)
	}
	if !strings.Contains(gotFilter, `"$includes":"review"`) {
		t.Fatalf("filter = %q, want trimmed text in $includes", gotFilter)
	}
	if gotPageSize != "50" || page.PageSize != 50 || page.Page != 1 {
		t.Fatalf("page size sent %s, page %d size %d; want clamped to 50 on page 1", gotPageSize, page.Page, page.PageSize)
	}
	if page.Source != SourceNocoBase {
		t.Fatalf("source = %s, want nocobase", page.Source)
	}
}

func TestHTTPOversizedBodyIsInvalid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat(" ", maxBodyBytes+1)))
	}))
	t.Cleanup(srv.Close)
	_, err := New(Config{BaseURL: srv.URL}).Listing(context.Background(), "acme.x", "")
	if !errors.Is(err, ErrInvalidListing) {
		t.Fatalf("err = %v, want ErrInvalidListing", err)
	}
}

func TestParseIDs(t *testing.T) {
	for _, ok := range []string{"acme.pr-reviewer", "a", "ns:thing-1"} {
		if _, err := ParseListingID(ok); err != nil {
			t.Fatalf("ParseListingID(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "..", ".hidden", "a/b", "a b", strings.Repeat("a", 129)} {
		if _, err := ParseListingID(bad); err == nil {
			t.Fatalf("ParseListingID(%q) accepted", bad)
		}
	}
	for _, ok := range []string{"1.2.0", "2.0.0-rc.1+build"} {
		if _, err := ParseVersion(ok); err != nil {
			t.Fatalf("ParseVersion(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-1", "1/2", "latest version"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Fatalf("ParseVersion(%q) accepted", bad)
		}
	}
}
