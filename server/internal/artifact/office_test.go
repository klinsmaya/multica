package artifact

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func officePackage(files map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			panic(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			panic(err)
		}
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func goldenDocx() []byte {
	return officePackage(map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>Hello artifact</w:t></w:r></w:p>
    <w:p><w:r><w:br w:type="page"/></w:r></w:p>
    <w:p><w:r><w:t>Second page</w:t></w:r></w:p>
  </w:body>
</w:document>`,
	})
}

func goldenXlsx() []byte {
	return officePackage(map[string]string{
		"xl/workbook.xml": `<?xml version="1.0" encoding="UTF-8"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <sheets>
    <sheet name="Budget" sheetId="1"/>
    <sheet name="Notes" sheetId="2"/>
  </sheets>
</workbook>`,
		"xl/worksheets/sheet1.xml": `<?xml version="1.0" encoding="UTF-8"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <sheetData><row><c t="inlineStr"><is><t>Rent</t></is></c></row></sheetData>
</worksheet>`,
	})
}

func goldenPptx() []byte {
	slide := func(text string) string {
		return `<?xml version="1.0" encoding="UTF-8"?>
<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">
  <p:cSld><p:spTree><p:sp><p:txBody><a:p><a:r><a:t>` + text + `</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld>
</p:sld>`
	}
	return officePackage(map[string]string{
		"ppt/slides/slide1.xml": slide("Hello slide"),
		"ppt/slides/slide2.xml": slide("Second slide"),
	})
}

func TestOfficeGoldenSamples(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	docx, err := ConvertIsolated(ctx, goldenDocx(), OfficeLimits{Format: FormatDocx})
	if err != nil {
		t.Fatal(err)
	}
	if !docx.OK || docx.PageCount != 2 || docx.AllowNetwork {
		t.Fatalf("docx = %+v", docx)
	}
	if !strings.Contains(docx.Text, "Hello artifact") || !strings.Contains(docx.Text, "Second page") {
		t.Fatalf("docx text = %q", docx.Text)
	}
	if strings.Contains(string(docx.HTML), "<p>2</p>") {
		t.Fatalf("page count leaked into the document: %s", docx.HTML)
	}

	xlsx, err := ConvertIsolated(ctx, goldenXlsx(), OfficeLimits{Format: FormatXlsx})
	if err != nil {
		t.Fatal(err)
	}
	if !xlsx.OK || len(xlsx.SheetNames) != 2 || xlsx.SheetNames[0] != "Budget" || xlsx.SheetNames[1] != "Notes" {
		t.Fatalf("xlsx sheets = %+v ok=%v", xlsx.SheetNames, xlsx.OK)
	}
	if !strings.Contains(xlsx.Text, "Rent") {
		t.Fatalf("xlsx text = %q", xlsx.Text)
	}

	pptx, err := ConvertIsolated(ctx, goldenPptx(), OfficeLimits{Format: FormatPptx})
	if err != nil {
		t.Fatal(err)
	}
	if !pptx.OK || pptx.SlideCount != 2 {
		t.Fatalf("pptx slides = %d ok=%v err=%s", pptx.SlideCount, pptx.OK, pptx.ErrorCode)
	}
	if !strings.Contains(pptx.Text, "Hello slide") || !strings.Contains(pptx.Text, "Second slide") {
		t.Fatalf("pptx text = %q", pptx.Text)
	}
	if strings.Contains(string(pptx.HTML), "<script") {
		t.Fatalf("pptx html = %s", pptx.HTML)
	}
}

func TestOfficeExtractRejectsZipSlipAndEscapesText(t *testing.T) {
	_, err := extractOffice(officePackage(map[string]string{
		"../evil.xml":       "<x/>",
		"word/document.xml": "<w:document><w:t>nope</w:t></w:document>",
	}), FormatDocx)
	if err == nil {
		t.Fatal("zip slip was accepted")
	}

	got, err := extractOffice(officePackage(map[string]string{
		"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:t>&lt;script&gt;alert(1)&lt;/script&gt;</w:t></w:document>`,
	}), FormatDocx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got.HTML), "<script>") || !strings.Contains(string(got.HTML), "&lt;script&gt;") {
		t.Fatalf("html = %s", got.HTML)
	}
}

func TestOfficeChildEnvDropsSecrets(t *testing.T) {
	env := officeChildEnv([]string{
		"DATABASE_URL=postgres://secret",
		"HTTP_PROXY=http://proxy",
		"HTTPS_PROXY=http://proxy",
		"COOKIE=session=abc",
		"AUTHORIZATION=Bearer x",
		"JWT_SECRET=sekret",
		"PATH=/usr/bin",
		"TMPDIR=/tmp",
		"HOME=/root",
	}, OfficeLimits{Mode: "env"}, "")
	joined := strings.Join(env, "\n")
	for _, banned := range []string{"DATABASE_URL=", "HTTP_PROXY=", "HTTPS_PROXY=", "COOKIE=", "AUTHORIZATION=", "JWT_SECRET=", "HOME="} {
		if strings.Contains(joined, banned) {
			t.Fatalf("env kept %s in %s", banned, joined)
		}
	}
	if !strings.Contains(joined, "MULTICA_OFFICE_ALLOW_NETWORK=0") || !strings.Contains(joined, "MULTICA_OFFICE_CHILD=1") {
		t.Fatalf("env = %s", joined)
	}
	if !strings.Contains(joined, "PATH=/usr/bin") {
		t.Fatalf("env dropped PATH: %s", joined)
	}
}

func TestOfficeChildProcessHasNoParentSecrets(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	got, err := ConvertIsolated(ctx, nil, OfficeLimits{Mode: "env", Wall: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.AllowNetwork {
		t.Fatalf("env report = %+v", got)
	}
	allowed := map[string]bool{
		"PATH": true, "TMPDIR": true,
		envOfficeChild: true, envOfficeMode: true, envOfficeInput: true,
		envOfficeFormat: true, envOfficeAllowNetwork: true,
		envOfficeASBytes: true, envOfficeCPUSeconds: true,
	}
	if len(got.EnvKeys) == 0 {
		t.Fatal("child reported no environment")
	}
	for _, key := range got.EnvKeys {
		if !allowed[key] {
			t.Fatalf("child kept %s (%v)", key, got.EnvKeys)
		}
	}
}

func TestOfficeTimeoutAndMemoryLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	timed, err := ConvertIsolated(ctx, nil, OfficeLimits{Mode: "sleep", Wall: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if timed.ErrorCode != CodeConversionTimeout {
		t.Fatalf("timeout = %+v", timed)
	}

	exhausted, err := ConvertIsolated(ctx, nil, OfficeLimits{
		Mode:    "allocate",
		ASBytes: 8 << 20,
		Wall:    20 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if exhausted.OK || exhausted.ErrorCode != CodeConversionExhausted {
		t.Fatalf("memory limit = %+v", exhausted)
	}
}

func TestOfficeOversizeDoesNotNeedAChildResult(t *testing.T) {
	data := bytes.Repeat([]byte{'A'}, int(MaxOfficeInputBytes)+1)
	got, err := ConvertIsolated(context.Background(), data, OfficeLimits{Format: FormatDocx, Wall: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if got.OK || got.ErrorCode != CodeConversionExhausted {
		t.Fatalf("oversize = %+v", got)
	}
}
