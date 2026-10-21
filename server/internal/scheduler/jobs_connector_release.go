package scheduler

import (
	"context"
	"fmt"
	"time"
)

// JobNameConnectorResourceRelease is the canonical name used in audit rows.
// Stable across releases.
const JobNameConnectorResourceRelease = "connector_resource_release"

// ConnectorResourceReleaser releases per-task connector resources (runtime
// tokens) of terminal tasks whose best-effort release did not complete, and
// purges expired connector OAuth state. *service.TaskService satisfies it.
type ConnectorResourceReleaser interface {
	ReleaseStaleConnectorResources(context.Context) (int, error)
}

// ConnectorResourceReleaseJob retries stale connector releases every 10
// minutes. Release is idempotent and the pending handles are the durable
// queue, so a missed plan only delays the retry.
func ConnectorResourceReleaseJob(releaser ConnectorResourceReleaser) JobSpec {
	return JobSpec{
		Name: JobNameConnectorResourceRelease, Cadence: 10 * time.Minute,
		CatchUpMode: CatchUpLatestOnly, CatchUpWindow: time.Hour,
		RunTimeout: 5 * time.Minute, StaleTimeout: 10 * time.Minute, HeartbeatInterval: 30 * time.Second,
		AllowStaleReentry: true, MaxAttempts: 1, Scopes: StaticScopes(ScopeGlobal),
		Handler: func(ctx context.Context, _ HandlerInput) (HandlerResult, error) {
			released, err := releaser.ReleaseStaleConnectorResources(ctx)
			if err != nil {
				return HandlerResult{}, fmt.Errorf("release stale connector resources: %w", err)
			}
			return HandlerResult{RowsAffected: int64(released)}, nil
		},
	}
}
