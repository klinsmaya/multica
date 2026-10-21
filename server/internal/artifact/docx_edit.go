package artifact

import (
	"bytes"
	"path"
	"sort"
	"strings"
)

const (
	// CodeOfficeFeatureBlocked means this DOCX stays read only.
	// Macros, comments, and tracked changes are not edited in this slice.
	CodeOfficeFeatureBlocked = "OFFICE_FEATURE_BLOCKED"
	// CodeOfficeFeatureDropped means a save would omit a package part from
	// the base revision. The revision is not sealed.
	CodeOfficeFeatureDropped = "OFFICE_FEATURE_DROPPED"
)

// DocxEditBlock reports why a DOCX package must stay read only.
// An empty reason means an edit session may open. The bytes are not executed.
func DocxEditBlock(data []byte) (string, error) {
	if int64(len(data)) > MaxOfficeInputBytes {
		return "too_large", nil
	}
	files, err := readOfficeZip(data)
	if err != nil {
		return "unreadable", nil
	}
	if _, ok := files["word/document.xml"]; !ok {
		return "unreadable", nil
	}
	for name := range files {
		base := path.Base(name)
		if base == "vbaProject.bin" || base == "vbaData.xml" || base == "vbaProjectSignature.bin" {
			return "macro", nil
		}
		if strings.HasPrefix(name, "word/comments") {
			return "comments", nil
		}
	}
	for name, raw := range files {
		if !docxStoryXML(name) {
			continue
		}
		if docxHasTrackedChange(raw) {
			return "tracked_changes", nil
		}
		if hasWordElement(raw, "commentRangeStart") || hasWordElement(raw, "commentReference") || hasWordElement(raw, "commentRangeEnd") {
			return "comments", nil
		}
	}
	return "", nil
}

// DocxDroppedParts lists base zip entry names that next does not contain.
// A rewritten part that keeps its name is not a drop. Directory entries are ignored.
func DocxDroppedParts(base, next []byte) ([]string, error) {
	before, err := readOfficeZip(base)
	if err != nil {
		return nil, err
	}
	after, err := readOfficeZip(next)
	if err != nil {
		return nil, err
	}
	missing := make([]string, 0)
	for name := range before {
		if _, ok := after[name]; !ok {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing, nil
}

func docxStoryXML(name string) bool {
	switch path.Base(name) {
	case "document.xml", "footnotes.xml", "endnotes.xml":
		return strings.HasPrefix(name, "word/")
	}
	base := path.Base(name)
	if strings.HasPrefix(name, "word/header") || strings.HasPrefix(name, "word/footer") {
		return strings.HasSuffix(base, ".xml")
	}
	return false
}

func docxHasTrackedChange(raw []byte) bool {
	return hasWordElement(raw, "ins") || hasWordElement(raw, "del") || hasWordElement(raw, "moveFrom") || hasWordElement(raw, "moveTo")
}

func hasWordElement(raw []byte, local string) bool {
	needle := []byte("<w:" + local)
	rest := raw
	for {
		i := bytes.Index(rest, needle)
		if i < 0 {
			return false
		}
		after := i + len(needle)
		if after >= len(rest) {
			return true
		}
		switch rest[after] {
		case '>', '/', ' ', '\t', '\n', '\r':
			return true
		}
		rest = rest[after:]
	}
}
