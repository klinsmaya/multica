package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/marketcatalog"
)

func TestWriteCatalogErrorCodes(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("get: %w", marketcatalog.ErrListingNotFound), http.StatusNotFound, "listing_not_found"},
		{fmt.Errorf("decode: %w", marketcatalog.ErrInvalidListing), http.StatusBadGateway, "catalog_invalid_listing"},
		{fmt.Errorf("dial: %w", marketcatalog.ErrCatalogUnavailable), http.StatusBadGateway, "catalog_unavailable"},
		{errors.New("anything else"), http.StatusBadGateway, "catalog_unavailable"},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		writeCatalogError(w, httptest.NewRequest(http.MethodGet, "/api/market/listings", nil), tc.err)
		var body map[string]string
		_ = json.NewDecoder(w.Body).Decode(&body)
		if w.Code != tc.status || body["code"] != tc.code {
			t.Errorf("%v: got %d %q, want %d %q", tc.err, w.Code, body["code"], tc.status, tc.code)
		}
	}
}

func TestDecodePreflightInputRejectsBadRequests(t *testing.T) {
	cases := []struct {
		name, listingID, body string
	}{
		{"listing id", "bad id!", `{"version":"1.0.0"}`},
		{"not json", "acme.notes", `version=1.0.0`},
		{"missing version", "acme.notes", `{"runtime_id":"` + testWorkspaceID + `"}`},
		{"bad version", "acme.notes", `{"version":"../1"}`},
		{"bad runtime", "acme.notes", `{"version":"1.0.0","runtime_id":"not-a-uuid"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			if _, ok := decodePreflightInput(w, preflightRequestFor(tc.listingID, tc.body)); ok {
				t.Fatal("decoded, want 400")
			}
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400", w.Code)
			}
		})
	}
}

func TestDecodePreflightInputDropsActorFields(t *testing.T) {
	w := httptest.NewRecorder()
	in, ok := decodePreflightInput(w, preflightRequestFor("acme.notes",
		`{"version":"1.0.0","model":" claude-sonnet-4-5 ","actor":"someone","user_id":"`+testWorkspaceID+`"}`))
	if !ok {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	want := preflightInput{Listing: "acme.notes", Version: "1.0.0", Choice: modelChoice{ModelID: "claude-sonnet-4-5"}}
	if in != want {
		t.Fatalf("input = %+v, want %+v", in, want)
	}
}

func TestPublicSourceNeverNamesTheAdapter(t *testing.T) {
	for source, want := range map[marketcatalog.Source]string{
		marketcatalog.SourceNocoBase: "live",
		marketcatalog.SourceStub:     "stub",
		"":                           "unknown",
		"other":                      "unknown",
	} {
		if got := publicSource(source); got != want {
			t.Errorf("publicSource(%q) = %q, want %q", source, got, want)
		}
	}
}

func preflightRequestFor(listingID, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/market/listings/x/preflight", strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("listingId", listingID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}
