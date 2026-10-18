package artifact

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	EventCreated           = "artifact.created"
	EventRevisionCommitted = "artifact.revision.committed"
	EventPreviewReady      = "artifact.preview.ready"
	EventShareRevoked      = "artifact.share.revoked"
)

func insertEvents(ctx context.Context, q *db.Queries, workspaceID, artifactID uuid.UUID, revisionID, operationID *uuid.UUID, kinds ...string) error {
	for _, kind := range kinds {
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("new artifact event id: %w", err)
		}
		if err := q.InsertArtifactEvent(ctx, db.InsertArtifactEventParams{
			ID:          pgUUID(id),
			WorkspaceID: pgUUID(workspaceID),
			ArtifactID:  pgUUID(artifactID),
			RevisionID:  pgUUIDPtr(revisionID),
			OperationID: pgUUIDPtr(operationID),
			EventType:   kind,
		}); err != nil {
			return err
		}
	}
	return nil
}

// ApplyEvent records one consumer delivery. A repeated event id applies once.
func (s *Service) ApplyEvent(ctx context.Context, eventID uuid.UUID) (bool, error) {
	if eventID == uuid.Nil {
		return false, fmt.Errorf("%w: event id", ErrInvalidInput)
	}
	q, err := s.queries()
	if err != nil {
		return false, err
	}
	n, err := q.ClaimArtifactEventReceipt(ctx, pgUUID(eventID))
	if err != nil {
		return false, err
	}
	return n == 1, nil
}
