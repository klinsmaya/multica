package openconnector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/runtimeapps"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/openconnector"
)

// mcpOverlayServerName is the key under `mcpServers` for the open-connector
// MCP server in the merged per-task config.
const mcpOverlayServerName = "open-connector"

// runtimeTokenNamePrefix marks tokens minted by Multica so a janitor can tell
// them from operator-created ones.
const runtimeTokenNamePrefix = "multica-task-"

// maxTokenListLen is open-connector's per-list limit on runtime tokens.
const maxTokenListLen = 128

type mcpServer struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

type overlayPayload struct {
	MCPServers map[string]mcpServer `json:"mcpServers"`
}

var _ runtimeapps.ConnectorProvider = (*Service)(nil)

// Name implements runtimeapps.ConnectorProvider.
func (s *Service) Name() string { return ProviderName }

// BuildTaskOverlay mints a runtime token scoped to (agent owner's active
// connections ∩ agent.connector_service_allowlist) and returns the MCP overlay
// that mounts open-connector with it. A zero result means no overlay.
//
// The token's allowedConnections is exactly the set of connection ids listed in
// ConnectedApps; allowedProxies and allowedTriggers are empty. The returned
// ResourceHandle is the token id, which ReleaseTaskResources deletes. The
// plaintext token appears only in the overlay and is never logged.
//
// The task id is not known while the overlay is built (the queue row is
// inserted afterwards), so originatorUserID is unused: like Composio the
// overlay follows the agent owner's connections.
func (s *Service) BuildTaskOverlay(ctx context.Context, _ pgtype.UUID, _ pgtype.UUID, agent db.Agent) (runtimeapps.MCPOverlayResult, error) {
	if !agent.OwnerID.Valid {
		return runtimeapps.MCPOverlayResult{}, nil
	}
	allow := allowlistSet(agent.ConnectorServiceAllowlist)
	if len(allow) == 0 {
		return runtimeapps.MCPOverlayResult{}, nil
	}
	rows, err := s.store.ListUserConnectorConnections(ctx, db.ListUserConnectorConnectionsParams{Provider: ProviderName, UserID: agent.OwnerID})
	if err != nil {
		return runtimeapps.MCPOverlayResult{}, fmt.Errorf("open-connector: build task overlay: list connections: %w", err)
	}
	var picked []db.UserConnectorConnection
	for _, r := range rows {
		if r.Status != statusActive {
			continue
		}
		if _, ok := allow[normalizeService(r.Service)]; !ok {
			continue
		}
		picked = append(picked, r)
	}
	if len(picked) == 0 {
		return runtimeapps.MCPOverlayResult{}, nil
	}
	// Stable order: by service, default first, then oldest.
	sort.SliceStable(picked, func(i, j int) bool {
		a, b := picked[i], picked[j]
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		if a.IsDefault != b.IsDefault {
			return a.IsDefault
		}
		return a.ConnectedAt.Time.Before(b.ConnectedAt.Time)
	})

	var (
		actions []string
		connIDs = make([]string, 0, len(picked))
		seenSvc = map[string]struct{}{}
	)
	for _, r := range picked {
		connIDs = append(connIDs, r.ConnectionID)
		if _, dup := seenSvc[r.Service]; !dup {
			seenSvc[r.Service] = struct{}{}
			actions = append(actions, r.Service+".*")
		}
	}
	if len(connIDs) > maxTokenListLen {
		return runtimeapps.MCPOverlayResult{}, fmt.Errorf("open-connector: build task overlay: %d connections exceed the runtime token limit of %d", len(connIDs), maxTokenListLen)
	}

	created, err := s.sdk.CreateRuntimeToken(ctx, openconnector.CreateRuntimeTokenRequest{
		Name:               runtimeTokenNamePrefix + util.UUIDToString(dbid.NewV7()),
		AllowedActions:     actions,
		BlockedActions:     []string{},
		AllowedProxies:     []string{},
		AllowedTriggers:    []string{},
		AllowedConnections: connIDs,
	})
	if err != nil {
		return runtimeapps.MCPOverlayResult{}, fmt.Errorf("open-connector: build task overlay: create runtime token: %w", err)
	}
	if created == nil || created.Token == "" || created.Record.ID == "" {
		return runtimeapps.MCPOverlayResult{}, errors.New("open-connector: build task overlay: empty runtime token response")
	}

	raw, err := json.Marshal(overlayPayload{MCPServers: map[string]mcpServer{
		mcpOverlayServerName: {
			Type:    "http",
			URL:     s.mcpURL,
			Headers: map[string]string{"Authorization": "Bearer " + created.Token},
		},
	}})
	if err != nil {
		// Do not leave the just-minted token behind.
		_ = s.sdk.DeleteRuntimeToken(ctx, created.Record.ID)
		return runtimeapps.MCPOverlayResult{}, fmt.Errorf("open-connector: marshal task overlay: %w", err)
	}

	apps := make([]runtimeapps.ConnectedApp, 0, len(picked))
	for _, r := range picked {
		name := r.Label
		if name == "" {
			name = runtimeapps.DisplayNameForToolkitSlug(r.Service)
		}
		apps = append(apps, runtimeapps.ConnectedApp{
			Provider:       ProviderName,
			ServerName:     mcpOverlayServerName,
			ToolkitSlug:    r.Service,
			ToolkitName:    name,
			ConnectionName: r.ConnectionName,
			IsDefault:      r.IsDefault,
		})
	}
	return runtimeapps.MCPOverlayResult{MCPOverlay: raw, ConnectedApps: apps, ResourceHandle: created.Record.ID}, nil
}

// ReleaseTaskResources deletes the task's runtime token (handle is its id). An
// already deleted token (404) counts as released, so the call is idempotent.
func (s *Service) ReleaseTaskResources(ctx context.Context, _ pgtype.UUID, handle string) error {
	if handle == "" {
		return nil
	}
	err := s.sdk.DeleteRuntimeToken(ctx, handle)
	if err != nil && !errors.Is(err, openconnector.ErrNotFound) {
		return fmt.Errorf("open-connector: delete runtime token: %w", err)
	}
	return nil
}

func allowlistSet(allow []string) map[string]struct{} {
	if len(allow) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(allow))
	for _, s := range allow {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out[s] = struct{}{}
		}
	}
	return out
}
