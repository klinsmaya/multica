package auth

import (
	"context"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// DisabledUsersFromQueries adapts the sqlc Queries to DisabledUserSource.
type DisabledUsersFromQueries struct{ Queries *db.Queries }

func (s DisabledUsersFromQueries) ListDisabledUsers(ctx context.Context) ([]DisabledUserEntry, error) {
	rows, err := s.Queries.ListDisabledUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]DisabledUserEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, DisabledUserEntry{ID: util.UUIDToString(r.ID), Email: r.Email})
	}
	return out, nil
}
