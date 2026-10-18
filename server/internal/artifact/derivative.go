package artifact

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// FailedDerivativeRetention is the ops default stored on a failed row.
// SweepRetention may drop the object after that time. The row stays.
const FailedDerivativeRetention = 7 * 24 * time.Hour

// OfficeDerivative is one rebuildable preview for a revision. It is not a
// new revision.
type OfficeDerivative struct {
	Status     string
	ErrorCode  string
	PageCount  int
	SheetNames []string
	SlideCount int
	ExpiresAt  *time.Time
}

// OfficeDerivativeInput is the sealed revision the parent converts. Format
// is docx, xlsx, or pptx. Data is the original bytes.
type OfficeDerivativeInput struct {
	WorkspaceID uuid.UUID
	ArtifactID  uuid.UUID
	RevisionID  uuid.UUID
	Format      string
	Data        []byte
	// Oversize skips the child and records CONVERSION_EXHAUSTED.
	Oversize bool
}

// EnsureOfficeDerivative returns the stored preview, building it when the
// revision has no ready derivative. The child process does not receive this
// service and does not write the database.
func (s *Service) EnsureOfficeDerivative(ctx context.Context, in OfficeDerivativeInput) (OfficeDerivative, error) {
	if s == nil || s.DB == nil || s.Store == nil {
		return OfficeDerivative{}, ErrInvalidInput
	}
	if _, ok := OfficeFormat(mediaForFormat(in.Format), ""); !ok {
		return OfficeDerivative{}, ErrInvalidInput
	}
	q, err := s.queries()
	if err != nil {
		return OfficeDerivative{}, err
	}
	row, inserted, err := claimOfficeDerivative(ctx, q, in)
	if err != nil {
		return OfficeDerivative{}, err
	}
	if row.Status == OfficeReady && row.StorageKey != "" {
		return officeDerivativeFromRow(row), nil
	}
	if !inserted && row.Status == OfficePending && officePendingFresh(row.CreatedAt) {
		return officeDerivativeFromRow(row), nil
	}
	if !inserted {
		if err := q.ClaimArtifactDerivativeRetry(ctx, row.ID); err != nil {
			return OfficeDerivative{}, err
		}
		row, err = q.GetArtifactDerivative(ctx, db.GetArtifactDerivativeParams{
			RevisionID: pgUUID(in.RevisionID),
			Format:     in.Format,
		})
		if err != nil {
			return OfficeDerivative{}, err
		}
		if row.Status == OfficeReady && row.StorageKey != "" {
			return officeDerivativeFromRow(row), nil
		}
	}
	return s.finishOfficeDerivative(ctx, q, row, in)
}

func claimOfficeDerivative(ctx context.Context, q *db.Queries, in OfficeDerivativeInput) (db.ArtifactDerivative, bool, error) {
	row, err := q.GetArtifactDerivative(ctx, db.GetArtifactDerivativeParams{
		RevisionID: pgUUID(in.RevisionID),
		Format:     in.Format,
	})
	if err == nil {
		return row, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.ArtifactDerivative{}, false, err
	}
	row, err = q.InsertArtifactDerivativePending(ctx, db.InsertArtifactDerivativePendingParams{
		ID:          pgUUID(uuid.New()),
		WorkspaceID: pgUUID(in.WorkspaceID),
		ArtifactID:  pgUUID(in.ArtifactID),
		RevisionID:  pgUUID(in.RevisionID),
		Format:      in.Format,
	})
	if err == nil {
		return row, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.ArtifactDerivative{}, false, err
	}
	row, err = q.GetArtifactDerivative(ctx, db.GetArtifactDerivativeParams{
		RevisionID: pgUUID(in.RevisionID),
		Format:     in.Format,
	})
	return row, false, err
}

func (s *Service) finishOfficeDerivative(ctx context.Context, q *db.Queries, row db.ArtifactDerivative, in OfficeDerivativeInput) (OfficeDerivative, error) {
	var result OfficeResult
	var err error
	if in.Oversize || int64(len(in.Data)) > MaxOfficeInputBytes {
		result = OfficeResult{ErrorCode: CodeConversionExhausted}
	} else {
		result, err = ConvertIsolated(ctx, in.Data, OfficeLimits{Format: in.Format})
		if err != nil {
			result = OfficeResult{ErrorCode: CodeConversionFailed}
		}
	}
	status := OfficeFailed
	mediaType := ""
	storageKey := ""
	var expires pgtype.Timestamptz
	if result.OK {
		doc, serr := SanitizeStaticDocument("text/html", "preview.html", result.HTML)
		if serr != nil || len(doc.Body) == 0 {
			result.OK = false
			result.ErrorCode = CodeConversionFailed
		} else {
			key := derivativeStorageKey(in.WorkspaceID, uuid.UUID(row.ID.Bytes))
			if _, uerr := s.Store.Upload(ctx, key, doc.Body, officePreviewMediaType, "preview.html"); uerr != nil {
				result.OK = false
				result.ErrorCode = CodeConversionFailed
			} else {
				status = OfficeReady
				mediaType = officePreviewMediaType
				storageKey = key
				result.HTML = doc.Body
			}
		}
	}
	if status != OfficeReady {
		if result.ErrorCode == "" {
			result.ErrorCode = CodeConversionFailed
		}
		expires = pgtype.Timestamptz{Time: time.Now().Add(s.retention().FailedDerivative), Valid: true}
	}
	finished, err := q.FinishArtifactDerivative(ctx, db.FinishArtifactDerivativeParams{
		Status:     status,
		MediaType:  mediaType,
		StorageKey: storageKey,
		ErrorCode:  result.ErrorCode,
		PageCount:  int32(result.PageCount),
		SheetNames: strings.Join(result.SheetNames, "\n"),
		SlideCount: int32(result.SlideCount),
		ExpiresAt:  expires,
		ID:         row.ID,
	})
	if err != nil {
		if storageKey != "" {
			s.Store.Delete(ctx, storageKey)
		}
		return OfficeDerivative{}, err
	}
	return officeDerivativeFromRow(finished), nil
}

func officePendingFresh(ts pgtype.Timestamptz) bool {
	if !ts.Valid {
		return false
	}
	age := time.Since(ts.Time)
	return age >= 0 && age < DefaultOfficeWall
}

func officeDerivativeFromRow(row db.ArtifactDerivative) OfficeDerivative {
	out := OfficeDerivative{
		Status:     row.Status,
		ErrorCode:  row.ErrorCode,
		PageCount:  int(row.PageCount),
		SlideCount: int(row.SlideCount),
		SheetNames: splitSheetNames(row.SheetNames),
	}
	if row.ExpiresAt.Valid {
		t := row.ExpiresAt.Time
		out.ExpiresAt = &t
	}
	return out
}

func splitSheetNames(raw string) []string {
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "\n")
}

func derivativeStorageKey(workspaceID, derivativeID uuid.UUID) string {
	return "workspaces/" + workspaceID.String() + "/artifact-derivatives/" + derivativeID.String()
}

func mediaForFormat(format string) string {
	switch format {
	case FormatDocx:
		return MediaTypeDocx
	case FormatXlsx:
		return MediaTypeXlsx
	case FormatPptx:
		return MediaTypePptx
	default:
		return ""
	}
}
