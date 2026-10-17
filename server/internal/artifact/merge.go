package artifact

import (
	"context"
	"strings"
	"unicode/utf8"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const maxMergeBytes = 1 << 20

// mergeRecognizedText keeps a same-length line edit when the two sides do not
// touch the same line. Any other shape is not recognized and is not merged.
func mergeRecognizedText(base, current, incoming []byte) ([]byte, bool) {
	if !utf8.Valid(base) || !utf8.Valid(current) || !utf8.Valid(incoming) {
		return nil, false
	}
	if len(base) > maxMergeBytes || len(current) > maxMergeBytes || len(incoming) > maxMergeBytes {
		return nil, false
	}
	b := strings.Split(string(base), "\n")
	c := strings.Split(string(current), "\n")
	in := strings.Split(string(incoming), "\n")
	if len(b) != len(c) || len(b) != len(in) {
		return nil, false
	}
	out := make([]string, len(b))
	for i := range b {
		switch {
		case c[i] == in[i]:
			out[i] = c[i]
		case c[i] == b[i]:
			out[i] = in[i]
		case in[i] == b[i]:
			out[i] = c[i]
		default:
			return nil, false
		}
	}
	return []byte(strings.Join(out, "\n")), true
}

func textMergeable(mediaType, filename string) bool {
	name := strings.ToLower(strings.TrimSpace(filename))
	if strings.HasSuffix(name, ".html") || strings.HasSuffix(name, ".htm") ||
		strings.HasSuffix(name, ".xhtml") || strings.HasSuffix(name, ".svg") {
		return false
	}
	media := strings.ToLower(strings.TrimSpace(mediaType))
	if i := strings.IndexByte(media, ';'); i >= 0 {
		media = strings.TrimSpace(media[:i])
	}
	if media == "text/html" || media == "application/xhtml+xml" || media == "image/svg+xml" {
		return false
	}
	ext := ""
	if i := strings.LastIndex(name, "."); i >= 0 && i < len(name)-1 {
		ext = name[i+1:]
	}
	switch ext {
	case "md", "markdown", "txt", "json", "csv":
		return true
	}
	switch media {
	case "text/markdown", "text/x-markdown", "text/plain", "application/json", "text/csv":
		return true
	default:
		return false
	}
}

func (s *Service) mergeStaleText(ctx context.Context, q *db.Queries, art db.Artifact, up db.ArtifactUpload, incoming []byte) ([]byte, bool) {
	if s == nil || s.Store == nil || !art.HeadRevisionID.Valid || !up.BaseRevisionID.Valid {
		return nil, false
	}
	if !textMergeable(up.DeclaredMediaType, up.Filename) {
		return nil, false
	}
	head, err := q.GetArtifactRevisionRecord(ctx, db.GetArtifactRevisionRecordParams{
		ID:         art.HeadRevisionID,
		ArtifactID: art.ID,
	})
	if err != nil || !textMergeable(head.MediaType, head.Filename) {
		return nil, false
	}
	ancestor, err := q.GetArtifactRevisionRecord(ctx, db.GetArtifactRevisionRecordParams{
		ID:         up.BaseRevisionID,
		ArtifactID: art.ID,
	})
	if err != nil || !textMergeable(ancestor.MediaType, ancestor.Filename) {
		return nil, false
	}
	currentBytes, err := s.revisionBytes(ctx, q, head)
	if err != nil {
		return nil, false
	}
	baseBytes, err := s.revisionBytes(ctx, q, ancestor)
	if err != nil {
		return nil, false
	}
	return mergeRecognizedText(baseBytes, currentBytes, incoming)
}

func (s *Service) revisionBytes(ctx context.Context, q *db.Queries, rev db.ArtifactRevision) ([]byte, error) {
	if rev.SizeBytes > maxMergeBytes {
		return nil, ErrInvalidInput
	}
	blob, err := q.GetArtifactBlob(ctx, rev.BlobID)
	if err != nil {
		return nil, err
	}
	data, err := readObjectLimited(ctx, s.Store, blob.StorageKey, blob.SizeBytes)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != blob.SizeBytes {
		return nil, ErrDigestMismatch
	}
	return data, nil
}
