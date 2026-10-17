package artifact

import "errors"

const (
	// CodeRevisionConflict is the stable error_code stored when head moved.
	CodeRevisionConflict = "REVISION_CONFLICT"
	// CodeIdempotencyReused is the stable error_code for a key reused with a
	// different request. It is returned to the caller and is not stored on the
	// original operation, which keeps its own result.
	CodeIdempotencyReused = "IDEMPOTENCY_KEY_REUSED"
)

var (
	// ErrNotFound means the artifact is missing in this workspace, or it was
	// soft-deleted. Both look the same so a deleted id is not an oracle.
	ErrNotFound = errors.New("artifact not found")
	// ErrRevisionConflict means head_revision_id was not the caller's base.
	ErrRevisionConflict = errors.New("revision conflict")
	// ErrIdempotencyReused means the key was already used for another request.
	ErrIdempotencyReused = errors.New("idempotency key reused")
	// ErrDigestMismatch means the bytes do not match the declared sha256.
	ErrDigestMismatch = errors.New("digest mismatch")
	// ErrInvalidInput means the commit or create request is malformed.
	ErrInvalidInput = errors.New("invalid artifact request")
	// ErrOperationPending means a same-key operation was left unfinished.
	ErrOperationPending = errors.New("artifact operation pending")
)
