package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/runtimeapps"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type handleProvider struct {
	name      string
	handle    string
	releaseFn func(handle string) error
	released  []string
}

func (p *handleProvider) Name() string { return p.name }

func (p *handleProvider) BuildTaskOverlay(context.Context, pgtype.UUID, pgtype.UUID, db.Agent) (runtimeapps.MCPOverlayResult, error) {
	return runtimeapps.MCPOverlayResult{
		MCPOverlay:     json.RawMessage(`{"mcpServers":{"` + p.name + `":{"type":"http","url":"https://x"}}}`),
		ResourceHandle: p.handle,
	}, nil
}

func (p *handleProvider) ReleaseTaskResources(_ context.Context, _ pgtype.UUID, handle string) error {
	p.released = append(p.released, handle)
	if p.releaseFn != nil {
		return p.releaseFn(handle)
	}
	return nil
}

func TestBuildRuntimeMCPOverlayCollectsConnectorHandles(t *testing.T) {
	withHandle := &handleProvider{name: "open-connector", handle: "tok-1"}
	noHandle := &handleProvider{name: "other"}
	svc := &TaskService{ConnectorProviders: []runtimeapps.ConnectorProvider{withHandle, noHandle}}

	got := svc.buildRuntimeMCPOverlay(context.Background(), pgtype.UUID{}, db.Agent{})
	if string(got.ConnectorHandles) != `{"open-connector":"tok-1"}` {
		t.Fatalf("ConnectorHandles = %s", got.ConnectorHandles)
	}

	none := &TaskService{ConnectorProviders: []runtimeapps.ConnectorProvider{noHandle}}
	if got := none.buildRuntimeMCPOverlay(context.Background(), pgtype.UUID{}, db.Agent{}); got.ConnectorHandles != nil {
		t.Fatalf("expected nil handles, got %s", got.ConnectorHandles)
	}
}

func TestReleaseHandlesCallsEveryProvider(t *testing.T) {
	failing := &handleProvider{name: "a", releaseFn: func(string) error { return errors.New("boom") }}
	ok := &handleProvider{name: "open-connector"}
	svc := &TaskService{ConnectorProviders: []runtimeapps.ConnectorProvider{failing, ok}}

	if svc.releaseHandles(context.Background(), pgtype.UUID{}, map[string]string{"a": "h1", "open-connector": "h2"}) {
		t.Fatal("expected failure to be reported")
	}
	if len(failing.released) != 1 || len(ok.released) != 1 || ok.released[0] != "h2" {
		t.Fatalf("released: a=%v oc=%v", failing.released, ok.released)
	}
	// An unregistered provider is reported as not released.
	if svc.releaseHandles(context.Background(), pgtype.UUID{}, map[string]string{"gone": "h"}) {
		t.Fatal("unregistered provider must not count as released")
	}
}

func TestReleaseConnectorResourcesIgnoresTasksWithoutHandles(t *testing.T) {
	p := &handleProvider{name: "open-connector"}
	svc := &TaskService{ConnectorProviders: []runtimeapps.ConnectorProvider{p}}
	svc.releaseConnectorResources(context.Background(), db.AgentTaskQueue{})
	svc.releaseConnectorResources(context.Background(), db.AgentTaskQueue{RuntimeConnectorHandles: []byte(`not json`)})
	if len(p.released) != 0 {
		t.Fatalf("released = %v", p.released)
	}
	svc.releaseConnectorResources(context.Background(), db.AgentTaskQueue{RuntimeConnectorHandles: []byte(`{"open-connector":"tok-9"}`)})
	if len(p.released) != 1 || p.released[0] != "tok-9" {
		t.Fatalf("released = %v", p.released)
	}
}
