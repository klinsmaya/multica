package artifact

import "context"

// ScanVerdict is the scanner's decision for one staged object.
type ScanVerdict string

const (
	// ScanClean means the bytes may be sealed.
	ScanClean ScanVerdict = "clean"
	// ScanPending means the scanner has not decided. Pending is never
	// promoted to ready, including when a scanner reports a timeout.
	ScanPending ScanVerdict = "pending"
	// ScanRejected means the bytes must not be sealed.
	ScanRejected ScanVerdict = "rejected"
)

// ScanObject is the staged object a scanner may read. The key is private
// staging, not the sealed blob.
type ScanObject struct {
	Key       string
	Size      int64
	SHA256    string
	MediaType string
}

// Scanner classifies staged bytes. Implementations must not treat an
// inconclusive result as clean.
type Scanner interface {
	Scan(ctx context.Context, object ScanObject) (ScanVerdict, error)
}

// NopScanner is the A04 stand-in for a malware scanner. It returns clean
// immediately. That is an explicit allow, not a timeout default.
type NopScanner struct{}

// Scan implements Scanner.
func (NopScanner) Scan(context.Context, ScanObject) (ScanVerdict, error) {
	return ScanClean, nil
}
