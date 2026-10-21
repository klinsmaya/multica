package artifact

import (
	"net/http"
	"strings"
)

// resolveMediaType returns the type to store. A generic sniff
// (text/plain or application/octet-stream) may keep a more specific
// declared type. A specific sniff that contradicts the declaration is
// rejected so the client MIME type is not authority by itself.
func resolveMediaType(data []byte, declared string) (string, bool) {
	sniffed := canonicalMediaType(http.DetectContentType(data))
	declared = canonicalMediaType(declared)
	if sniffed == "" || declared == "" {
		return sniffed, false
	}
	if sniffed == declared {
		return sniffed, true
	}
	switch sniffed {
	case "text/plain":
		if isTextLike(declared) {
			return declared, true
		}
		return sniffed, false
	case "text/xml":
		if declared == "image/svg+xml" || declared == "application/xhtml+xml" || declared == "application/xml" || declared == "text/xml" {
			return declared, true
		}
		return sniffed, false
	case "application/octet-stream":
		return declared, true
	case "application/zip":
		switch declared {
		case MediaTypeDocx, MediaTypeXlsx, MediaTypePptx:
			return declared, true
		default:
			return sniffed, false
		}
	}
	if declared == "text/plain" || declared == "application/octet-stream" {
		return sniffed, true
	}
	return sniffed, false
}

func canonicalMediaType(mt string) string {
	mt = strings.ToLower(strings.TrimSpace(mt))
	if i := strings.Index(mt, ";"); i >= 0 {
		mt = strings.TrimSpace(mt[:i])
	}
	return mt
}

func isTextLike(mt string) bool {
	switch mt {
	case "text/plain", "text/markdown", "text/x-markdown", "text/csv",
		"text/html", "text/xml", "text/json", "text/yaml", "text/x-yaml",
		"application/json", "application/xml", "application/yaml", "application/x-yaml",
		"application/xhtml+xml", "image/svg+xml":
		return true
	default:
		return strings.HasPrefix(mt, "text/")
	}
}
