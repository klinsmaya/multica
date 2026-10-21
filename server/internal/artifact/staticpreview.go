package artifact

import (
	"bytes"
	"errors"
	"path"
	"regexp"
	"strings"
	"sync"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
)

const (
	MaxStaticPreviewBytes = 1 << 20

	// StaticPreviewCSP is the only policy a static preview response may send.
	// The document bytes never choose this header. sandbox without
	// allow-scripts or allow-same-origin is not a network boundary by itself.
	StaticPreviewCSP = "sandbox; default-src 'none'; connect-src 'none'; img-src 'none'; font-src 'none'; style-src 'none'; script-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'"

	StaticPreviewReferrerPolicy    = "no-referrer"
	StaticPreviewPermissionsPolicy = "accelerometer=(), autoplay=(), camera=(), display-capture=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()"
)

var (
	ErrNotPreviewable        = errors.New("not a static preview")
	ErrStaticPreviewTooLarge = errors.New("static preview too large")
	ErrResourcePath          = errors.New("resource path")
)

// Document is a sanitized HTML or SVG fragment. Kind is html or svg.
type Document struct {
	ContentType string
	Kind        string
	Body        []byte
}

// PackageEntry is one path inside a revision package. A symlink is refused.
type PackageEntry struct {
	Path    string
	Symlink bool
}

// Manifest is the set of package-internal paths a revision may serve.
// A single HTML or SVG file has an empty set, so authorizing that file
// does not authorize any other workspace object.
type Manifest struct {
	paths map[string]struct{}
}

func SingleFileManifest() Manifest {
	return Manifest{paths: map[string]struct{}{}}
}

func (m Manifest) Allows(raw string) bool {
	cleaned, err := NormalizePackagePath(raw)
	if err != nil {
		return false
	}
	_, ok := m.paths[cleaned]
	return ok
}

// NormalizePackagePath accepts one relative package path. Absolute paths,
// URLs, and parent segments are refused.
func NormalizePackagePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsRune(raw, 0) || strings.Contains(raw, "\\") {
		return "", ErrResourcePath
	}
	if strings.Contains(raw, "://") || strings.HasPrefix(raw, "//") || strings.Contains(raw, ":") {
		return "", ErrResourcePath
	}
	if strings.HasPrefix(raw, "/") || path.IsAbs(raw) {
		return "", ErrResourcePath
	}
	cleaned := path.Clean(raw)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return "", ErrResourcePath
	}
	if strings.Contains(cleaned, "..") {
		return "", ErrResourcePath
	}
	return cleaned, nil
}

// BuildManifest registers package paths. Two names that clean to one path,
// or any symlink, refuse the whole manifest.
func BuildManifest(entries []PackageEntry) (Manifest, error) {
	m := Manifest{paths: map[string]struct{}{}}
	for _, entry := range entries {
		if entry.Symlink {
			return Manifest{}, ErrResourcePath
		}
		cleaned, err := NormalizePackagePath(entry.Path)
		if err != nil {
			return Manifest{}, err
		}
		if _, exists := m.paths[cleaned]; exists {
			return Manifest{}, ErrResourcePath
		}
		m.paths[cleaned] = struct{}{}
	}
	return m, nil
}

// SanitizeStaticDocument strips active HTML and SVG down to a static fragment.
func SanitizeStaticDocument(mediaType, filename string, raw []byte) (Document, error) {
	contentType, kind, ok := staticPreviewType(mediaType, filename)
	if !ok {
		return Document{}, ErrNotPreviewable
	}
	if len(raw) > MaxStaticPreviewBytes {
		return Document{}, ErrStaticPreviewTooLarge
	}
	cleaned := bytes.ReplaceAll(raw, []byte{0}, nil)
	rendered := renderStatic(cleaned)
	fragment := staticPolicy().Sanitize(string(rendered))
	body := renderStatic([]byte(fragment))
	return Document{ContentType: contentType, Kind: kind, Body: body}, nil
}

// StaticPreviewKind reports html or svg for an active document.
func StaticPreviewKind(mediaType, filename string) (string, bool) {
	_, kind, ok := staticPreviewType(mediaType, filename)
	return kind, ok
}

func staticPreviewType(mediaType, filename string) (contentType, kind string, ok bool) {
	if !staticPreviewMedia(mediaType, filename) {
		return "", "", false
	}
	media := canonicalPreviewMedia(mediaType)
	ext := previewExt(filename)
	if media == "image/svg+xml" || (ext == "svg" && media != "text/html" && media != "application/xhtml+xml") {
		return "image/svg+xml", "svg", true
	}
	return "text/html; charset=utf-8", "html", true
}

func staticPreviewMedia(mediaType, filename string) bool {
	switch canonicalPreviewMedia(mediaType) {
	case "text/html", "application/xhtml+xml", "image/svg+xml":
		return true
	}
	switch previewExt(filename) {
	case "html", "htm", "xhtml", "svg":
		return true
	default:
		return false
	}
}

func canonicalPreviewMedia(mt string) string {
	mt = strings.ToLower(strings.TrimSpace(mt))
	if i := strings.IndexByte(mt, ';'); i >= 0 {
		mt = strings.TrimSpace(mt[:i])
	}
	return mt
}

func previewExt(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if i := strings.LastIndex(name, "."); i >= 0 && i < len(name)-1 {
		return name[i+1:]
	}
	return ""
}

var (
	policyOnce sync.Once
	policy     *bluemonday.Policy
)

func staticPolicy() *bluemonday.Policy {
	policyOnce.Do(func() {
		p := bluemonday.NewPolicy()
		p.AllowElements(
			"a", "b", "blockquote", "br", "code", "div", "em", "h1", "h2", "h3", "h4", "h5", "h6",
			"hr", "i", "li", "ol", "p", "pre", "span", "strong", "table", "tbody", "td", "th", "thead", "tr", "ul",
			"svg", "g", "path", "rect", "circle", "ellipse", "line", "polyline", "polygon", "text", "tspan", "title", "desc",
		)
		p.AllowAttrs("href").Matching(fragmentHref).OnElements("a")
		p.AllowAttrs("xmlns").Matching(regexp.MustCompile(`^http://www\.w3\.org/2000/svg$`)).OnElements("svg")
		p.AllowAttrs("viewbox").Matching(svgViewBox).OnElements("svg")
		p.AllowAttrs("width", "height").Matching(svgNumber).OnElements("svg", "rect")
		p.AllowAttrs("cx", "cy", "r", "rx", "ry", "x", "y", "x1", "y1", "x2", "y2").Matching(svgNumber).OnElements("circle", "ellipse", "rect", "line", "text", "tspan")
		p.AllowAttrs("fill", "stroke").Matching(svgColor).OnElements("circle", "ellipse", "rect", "line", "path", "text", "tspan")
		p.AllowAttrs("points").Matching(svgPoints).OnElements("polyline", "polygon")
		p.AllowAttrs("d").Matching(regexp.MustCompile(`^[A-Za-z0-9,.\+\-\s]+$`)).OnElements("path")
		policy = p
	})
	return policy
}

var (
	fragmentHref = regexp.MustCompile(`^#[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	svgNumber    = regexp.MustCompile(`^[0-9]{1,6}(\.[0-9]{1,4})?$`)
	svgViewBox   = regexp.MustCompile(`^[0-9. ]{1,64}$`)
	svgColor     = regexp.MustCompile(`^(none|currentColor|#[0-9A-Fa-f]{3,8}|[a-zA-Z]{1,20})$`)
	svgPoints    = regexp.MustCompile(`^[0-9.,\s]+$`)
)

var staticElements = map[string]struct{}{
	"a": {}, "b": {}, "blockquote": {}, "br": {}, "code": {}, "div": {}, "em": {},
	"h1": {}, "h2": {}, "h3": {}, "h4": {}, "h5": {}, "h6": {}, "hr": {}, "i": {},
	"li": {}, "ol": {}, "p": {}, "pre": {}, "span": {}, "strong": {}, "table": {},
	"tbody": {}, "td": {}, "th": {}, "thead": {}, "tr": {}, "ul": {},
	"svg": {}, "g": {}, "path": {}, "rect": {}, "circle": {}, "ellipse": {},
	"line": {}, "polyline": {}, "polygon": {}, "text": {}, "tspan": {}, "title": {}, "desc": {},
}

var dropWithChildren = map[string]struct{}{
	"script": {}, "style": {}, "iframe": {}, "object": {}, "embed": {}, "form": {},
	"link": {}, "meta": {}, "base": {}, "noscript": {}, "template": {}, "button": {},
	"input": {}, "textarea": {}, "select": {}, "option": {}, "video": {}, "audio": {},
	"canvas": {}, "marquee": {}, "foreignobject": {}, "animate": {}, "set": {}, "use": {},
	"image": {},
}

func renderStatic(raw []byte) []byte {
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	var buf bytes.Buffer
	writeStatic(&buf, doc)
	return buf.Bytes()
}

func writeStatic(buf *bytes.Buffer, n *html.Node) {
	if n == nil {
		return
	}
	switch n.Type {
	case html.TextNode:
		buf.WriteString(html.EscapeString(n.Data))
	case html.ElementNode:
		name := strings.ToLower(n.Data)
		if _, drop := dropWithChildren[name]; drop {
			return
		}
		if _, ok := staticElements[name]; !ok {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				writeStatic(buf, c)
			}
			return
		}
		buf.WriteByte('<')
		buf.WriteString(name)
		for _, attr := range n.Attr {
			value, ok := staticAttr(name, attr.Key, attr.Val)
			if !ok {
				continue
			}
			buf.WriteByte(' ')
			buf.WriteString(strings.ToLower(attr.Key))
			buf.WriteString(`="`)
			buf.WriteString(html.EscapeString(value))
			buf.WriteByte('"')
		}
		buf.WriteByte('>')
		if name == "br" || name == "hr" {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			writeStatic(buf, c)
		}
		buf.WriteString("</")
		buf.WriteString(name)
		buf.WriteByte('>')
	default:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			writeStatic(buf, c)
		}
	}
}

func staticAttr(element, key, value string) (string, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	value = strings.TrimSpace(value)
	if key == "" || strings.HasPrefix(key, "on") || strings.Contains(key, ":") {
		return "", false
	}
	lower := strings.ToLower(value)
	if strings.Contains(lower, "javascript:") || strings.Contains(lower, "url(") || strings.Contains(lower, "data:") {
		return "", false
	}
	switch element {
	case "a":
		if key == "href" && fragmentHref.MatchString(value) {
			return value, true
		}
	case "svg":
		switch key {
		case "xmlns":
			if value == "http://www.w3.org/2000/svg" {
				return value, true
			}
		case "viewbox":
			if svgViewBox.MatchString(value) {
				return value, true
			}
		case "width", "height":
			if svgNumber.MatchString(value) {
				return value, true
			}
		}
	case "circle", "ellipse", "rect", "line", "text", "tspan":
		switch key {
		case "cx", "cy", "r", "rx", "ry", "x", "y", "x1", "y1", "x2", "y2", "width", "height":
			if svgNumber.MatchString(value) {
				return value, true
			}
		case "fill", "stroke":
			if svgColor.MatchString(value) && !strings.Contains(lower, "url") {
				return value, true
			}
		}
	case "polyline", "polygon":
		if key == "points" && len(value) <= 4096 && svgPoints.MatchString(value) {
			return value, true
		}
	case "path":
		if key == "d" && safePathData(value) {
			return value, true
		}
		if (key == "fill" || key == "stroke") && svgColor.MatchString(value) {
			return value, true
		}
	}
	return "", false
}

func safePathData(value string) bool {
	if value == "" || len(value) > 8192 {
		return false
	}
	lower := strings.ToLower(value)
	if strings.Contains(lower, "url") || strings.Contains(lower, "http") || strings.Contains(lower, "script") {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == ' ' || r == ',' || r == '.' || r == '+' || r == '-' || r == '\n' || r == '\t':
			continue
		default:
			return false
		}
	}
	return true
}
