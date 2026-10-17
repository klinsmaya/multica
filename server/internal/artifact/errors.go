package artifact

import "errors"

const (
	// CodeRevisionConflict is the stable error_code stored when head moved.
	CodeRevisionConflict = "REVISION_CONFLICT"
	// CodeLeaseHeld means another editor, or a mismatched token, holds the lease.
	CodeLeaseHeld = "LEASE_HELD"
	// CodeLeaseExpired means the fencing token is no longer allowed to submit.
	CodeLeaseExpired = "LEASE_EXPIRED"
	// CodeNotEditable means this revision is not a text format the editor opens.
	CodeNotEditable = "NOT_EDITABLE"
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
	// ErrDigestMismatch means the bytes do not match the declared sha256 or size.
	ErrDigestMismatch = errors.New("digest mismatch")
	// ErrMediaTypeMismatch means the sniffed type contradicts the declared type.
	ErrMediaTypeMismatch = errors.New("media type mismatch")
	// ErrInvalidInput means the commit or create request is malformed.
	ErrInvalidInput = errors.New("invalid artifact request")
	// ErrOperationPending means a same-key operation was left unfinished.
	ErrOperationPending = errors.New("artifact operation pending")
	// ErrUploadConsumed means this upload already sealed a revision.
	ErrUploadConsumed = errors.New("upload consumed")
	// ErrUploadNotReady means finalize ran before the upload reached ready.
	ErrUploadNotReady = errors.New("upload not ready")
	// ErrScanPending means the scanner has not cleared the bytes.
	// Pending is not approval. A timeout stays pending.
	ErrScanPending = errors.New("scan pending")
	// ErrUploadRejected means verify or the scanner rejected the bytes.
	ErrUploadRejected = errors.New("upload rejected")
	// ErrQuotaExceeded means the workspace or actor reservation does not fit.
	ErrQuotaExceeded = errors.New("artifact quota exceeded")
	// ErrUploadExpired means the staging binding passed its deadline.
	ErrUploadExpired = errors.New("upload expired")
	// ErrUploadCancelled means the caller cancelled the staging binding.
	ErrUploadCancelled = errors.New("upload cancelled")
	// ErrLeaseHeld means the open lease does not match this caller.
	ErrLeaseHeld = errors.New("edit lease held")
	// ErrLeaseExpired means the fencing token can no longer submit.
	ErrLeaseExpired = errors.New("edit lease expired")
	// ErrNotEditable means the head revision is not markdown, text, JSON, or CSV.
	ErrNotEditable = errors.New("artifact is not editable")
)
