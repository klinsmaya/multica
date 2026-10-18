package market

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStubSource(t *testing.T) {
	ctx := context.Background()
	var source Source = StubSource{}
	resources, err := source.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("len = %d, want 2", len(resources))
	}
	skill := resources[0]
	if skill.Kind != "skill" || resources[1].Kind != "knowledge" {
		t.Fatalf("kinds = %s, %s", skill.Kind, resources[1].Kind)
	}
	kinds := map[Kind]int{}
	for _, demand := range skill.Demands {
		kinds[demand.Kind]++
	}
	for _, kind := range []Kind{KindModel, KindTool, KindConnection, KindKnowledge} {
		if kinds[kind] != 1 {
			t.Fatalf("skill demands = %+v", skill.Demands)
		}
	}
	got, err := source.Get(ctx, skill.ResourceID)
	if err != nil || got.ResourceID != skill.ResourceID {
		t.Fatalf("Get skill: %v %+v", err, got)
	}
	if _, err := source.Get(ctx, "missing/resource"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing: %v", err)
	}
}

func TestNewSourceEmptyIsStub(t *testing.T) {
	if _, ok := NewSource("").(StubSource); !ok {
		t.Fatal("empty base URL should use the stub")
	}
}

func TestHTTPSourceParsesEnvelope(t *testing.T) {
	var gotList, gotFilter string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/marketResources:list":
			gotList = r.URL.Path
			w.Write([]byte(`{"data":[{"resourceId":"acme/skill","name":"Skill","publisherSlug":"acme","kind":"skill","description":"","sourceKey":"remote","demands":[{"kind":"model","ref":"m","scope":"read-only"}]}]}`))
		case "/api/marketResources:get":
			gotFilter = r.URL.Query().Get("filterByTk")
			w.Write([]byte(`{"data":{"resourceId":"acme/skill","name":"Skill","publisherSlug":"acme","kind":"skill","description":"","sourceKey":"remote","demands":[]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	source := NewSource(srv.URL)
	resources, err := source.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if gotList != "/api/marketResources:list" || len(resources) != 1 || resources[0].ResourceID != "acme/skill" {
		t.Fatalf("list path %s resources %+v", gotList, resources)
	}
	if len(resources[0].Demands) != 1 || resources[0].Demands[0].Kind != KindModel {
		t.Fatalf("demands %+v", resources[0].Demands)
	}
	got, err := source.Get(context.Background(), "acme/skill")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if gotFilter != "acme/skill" || got.Kind != "skill" || got.Demands == nil {
		t.Fatalf("filter %q resource %+v", gotFilter, got)
	}
}

func TestHTTPSourceRejectsBadResponses(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		miss   bool
	}{
		{name: "status", status: http.StatusBadGateway, body: `{"data":[]}`},
		{name: "malformed", status: http.StatusOK, body: `{"data":{"nope":true}}`},
		{name: "not json", status: http.StatusOK, body: `not-json`},
		{name: "missing", status: http.StatusNotFound, body: `{}`, miss: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			_, err := NewSource(srv.URL).List(context.Background())
			if tt.miss {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("err = %v, want not found", err)
				}
				return
			}
			if err == nil || errors.Is(err, ErrNotFound) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}
