package artifact

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"html"
	"io"
	"path"
	"strings"
)

const (
	FormatDocx = "docx"
	FormatXlsx = "xlsx"
	FormatPptx = "pptx"

	MediaTypeDocx = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	MediaTypeXlsx = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	MediaTypePptx = "application/vnd.openxmlformats-officedocument.presentationml.presentation"

	OfficeReady   = "ready"
	OfficePending = "pending"
	OfficeFailed  = "failed"

	CodeConversionTimeout   = "CONVERSION_TIMEOUT"
	CodeConversionExhausted = "CONVERSION_EXHAUSTED"
	CodeConversionFailed    = "CONVERSION_FAILED"

	// MaxOfficeInputBytes is the largest original this converter will open.
	MaxOfficeInputBytes = 20 << 20

	maxOfficeZipFiles      = 128
	maxOfficeUncompressed  = 32 << 20
	maxOfficeText          = 256 << 10
	officePreviewMediaType = "text/html; charset=utf-8"
)

var (
	errOfficeFailed    = errors.New("office conversion failed")
	errOfficeExhausted = errors.New("office conversion exhausted")
)

// OfficeFormat reports docx, xlsx, or pptx from the sealed media type or name.
func OfficeFormat(mediaType, filename string) (string, bool) {
	switch canonicalMediaType(mediaType) {
	case MediaTypeDocx:
		return FormatDocx, true
	case MediaTypeXlsx:
		return FormatXlsx, true
	case MediaTypePptx:
		return FormatPptx, true
	}
	switch fileExt(filename) {
	case FormatDocx:
		return FormatDocx, true
	case FormatXlsx:
		return FormatXlsx, true
	case FormatPptx:
		return FormatPptx, true
	default:
		return "", false
	}
}

func fileExt(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if i := strings.LastIndex(name, "."); i >= 0 && i < len(name)-1 {
		return name[i+1:]
	}
	return ""
}

type officeExtract struct {
	PageCount  int
	SheetNames []string
	SlideCount int
	Text       string
	HTML       []byte
}

// extractOffice reads one OOXML package. It does not follow links, run
// macros, or open the network. Page count is explicit page breaks plus one.
func extractOffice(data []byte, format string) (officeExtract, error) {
	if int64(len(data)) > MaxOfficeInputBytes {
		return officeExtract{}, errOfficeExhausted
	}
	files, err := readOfficeZip(data)
	if err != nil {
		return officeExtract{}, err
	}
	switch format {
	case FormatDocx:
		return extractDocx(files)
	case FormatXlsx:
		return extractXlsx(files)
	case FormatPptx:
		return extractPptx(files)
	default:
		return officeExtract{}, errOfficeFailed
	}
}

func extractDocx(files map[string][]byte) (officeExtract, error) {
	raw, ok := files["word/document.xml"]
	if !ok {
		return officeExtract{}, errOfficeFailed
	}
	text, pages, err := xmlText(raw, true)
	if err != nil {
		return officeExtract{}, err
	}
	out := officeExtract{PageCount: pages, Text: text}
	out.HTML = officeHTML(out)
	return out, nil
}

func extractXlsx(files map[string][]byte) (officeExtract, error) {
	book, ok := files["xl/workbook.xml"]
	if !ok {
		return officeExtract{}, errOfficeFailed
	}
	names, err := xmlSheetNames(book)
	if err != nil {
		return officeExtract{}, err
	}
	var parts []string
	for name, raw := range files {
		if name == "xl/sharedStrings.xml" || (strings.HasPrefix(name, "xl/worksheets/") && strings.HasSuffix(name, ".xml")) {
			text, _, err := xmlText(raw, false)
			if err != nil {
				return officeExtract{}, err
			}
			if text != "" {
				parts = append(parts, text)
			}
		}
	}
	out := officeExtract{SheetNames: names, Text: strings.Join(parts, "\n")}
	out.HTML = officeHTML(out)
	return out, nil
}

func extractPptx(files map[string][]byte) (officeExtract, error) {
	var slides []string
	var parts []string
	for name, raw := range files {
		if !isSlideXML(name) {
			continue
		}
		slides = append(slides, name)
		text, _, err := xmlText(raw, false)
		if err != nil {
			return officeExtract{}, err
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	if len(slides) == 0 {
		return officeExtract{}, errOfficeFailed
	}
	out := officeExtract{SlideCount: len(slides), Text: strings.Join(parts, "\n")}
	out.HTML = officeHTML(out)
	return out, nil
}

func isSlideXML(name string) bool {
	if !strings.HasPrefix(name, "ppt/slides/") {
		return false
	}
	base := path.Base(name)
	if !strings.HasPrefix(base, "slide") || !strings.HasSuffix(base, ".xml") {
		return false
	}
	if strings.Contains(base, "Layout") || strings.Contains(base, "Master") {
		return false
	}
	return true
}

func officeHTML(ex officeExtract) []byte {
	var b strings.Builder
	b.WriteString("<article>")
	if ex.PageCount > 0 {
		b.WriteString("<p>")
		b.WriteString(html.EscapeString(itoa(ex.PageCount)))
		b.WriteString("</p>")
	}
	for _, name := range ex.SheetNames {
		b.WriteString("<p>")
		b.WriteString(html.EscapeString(name))
		b.WriteString("</p>")
	}
	if ex.SlideCount > 0 {
		b.WriteString("<p>")
		b.WriteString(html.EscapeString(itoa(ex.SlideCount)))
		b.WriteString("</p>")
	}
	b.WriteString("<pre>")
	b.WriteString(html.EscapeString(ex.Text))
	b.WriteString("</pre></article>")
	return []byte(b.String())
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func readOfficeZip(data []byte) (map[string][]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errOfficeFailed
	}
	if len(zr.File) > maxOfficeZipFiles {
		return nil, errOfficeExhausted
	}
	out := make(map[string][]byte, len(zr.File))
	var total uint64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		// vbaProject.bin and other parts are stored only so a bad path fails
		// closed. Nothing here executes a macro or fetches a URL.
		if err := safeZipName(f.Name); err != nil {
			return nil, err
		}
		if f.UncompressedSize64 > maxOfficeUncompressed {
			return nil, errOfficeExhausted
		}
		total += f.UncompressedSize64
		if total > maxOfficeUncompressed {
			return nil, errOfficeExhausted
		}
		rc, err := f.Open()
		if err != nil {
			return nil, errOfficeFailed
		}
		buf, err := io.ReadAll(io.LimitReader(rc, maxOfficeUncompressed+1))
		rc.Close()
		if err != nil {
			return nil, errOfficeFailed
		}
		if len(buf) > maxOfficeUncompressed {
			return nil, errOfficeExhausted
		}
		out[f.Name] = buf
	}
	return out, nil
}

func safeZipName(name string) error {
	if name == "" || strings.Contains(name, "\\") || strings.Contains(name, "..") || strings.HasPrefix(name, "/") {
		return errOfficeFailed
	}
	return nil
}

func xmlText(raw []byte, pageBreaks bool) (string, int, error) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	dec.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) {
		return input, nil
	}
	var b strings.Builder
	inT := 0
	pages := 1
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", 0, errOfficeFailed
		}
		switch el := tok.(type) {
		case xml.StartElement:
			if el.Name.Local == "t" {
				inT++
			}
			if el.Name.Local == "p" && b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
				b.WriteByte('\n')
			}
			if pageBreaks && el.Name.Local == "br" {
				for _, attr := range el.Attr {
					if attr.Name.Local == "type" && attr.Value == "page" {
						pages++
					}
				}
			}
		case xml.EndElement:
			if el.Name.Local == "t" && inT > 0 {
				inT--
			}
		case xml.CharData:
			if inT > 0 {
				if b.Len()+len(el) > maxOfficeText {
					return "", 0, errOfficeExhausted
				}
				b.Write(el)
			}
		}
	}
	if !pageBreaks {
		pages = 0
	}
	return strings.TrimSpace(b.String()), pages, nil
}

func xmlSheetNames(raw []byte) ([]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	dec.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) {
		return input, nil
	}
	var names []string
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errOfficeFailed
		}
		el, ok := tok.(xml.StartElement)
		if !ok || el.Name.Local != "sheet" {
			continue
		}
		for _, attr := range el.Attr {
			if attr.Name.Local == "name" && attr.Value != "" {
				names = append(names, attr.Value)
			}
		}
	}
	if len(names) == 0 {
		return nil, errOfficeFailed
	}
	return names, nil
}
