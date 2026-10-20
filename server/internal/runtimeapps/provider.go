package runtimeapps

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ConnectorProvider is one external app-connection backend (Composio,
// open-connector). TaskService asks every registered provider for a per-task
// MCP overlay at enqueue time and merges the results.
type ConnectorProvider interface {
	// Name identifies the provider ("composio" | "open-connector").
	Name() string
	// BuildTaskOverlay returns the overlay for one task. A zero result means
	// "no overlay for this run". Errors are best-effort: callers log and skip.
	BuildTaskOverlay(ctx context.Context, taskID, originatorUserID pgtype.UUID, agent db.Agent) (MCPOverlayResult, error)
	// ReleaseTaskResources frees anything BuildTaskOverlay provisioned for the
	// task. Must be idempotent.
	ReleaseTaskResources(ctx context.Context, taskID pgtype.UUID) error
}
