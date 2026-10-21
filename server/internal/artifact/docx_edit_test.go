package artifact

import "testing"

func TestDocxEditBlockAndDroppedParts(t *testing.T) {
	plain := officePackage(map[string]string{
		"word/document.xml":   `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Hello artifact</w:t></w:r></w:p></w:body></w:document>`,
		"customXml/item1.xml": `<root>KEEP-PART</root>`,
	})
	reason, err := DocxEditBlock(plain)
	if err != nil || reason != "" {
		t.Fatalf("plain = %q %v", reason, err)
	}
	missing, err := DocxDroppedParts(plain, plain)
	if err != nil || len(missing) != 0 {
		t.Fatalf("same package missing = %v %v", missing, err)
	}

	macro := officePackage(map[string]string{
		"word/document.xml":   `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"/>`,
		"word/vbaProject.bin": "not-executed",
	})
	if reason, _ := DocxEditBlock(macro); reason != "macro" {
		t.Fatalf("macro = %q", reason)
	}
	comments := officePackage(map[string]string{
		"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"/>`,
		"word/comments.xml": `<w:comments xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"/>`,
	})
	if reason, _ := DocxEditBlock(comments); reason != "comments" {
		t.Fatalf("comments = %q", reason)
	}
	tracked := officePackage(map[string]string{
		"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:ins w:id="1"><w:r><w:t>x</w:t></w:r></w:ins></w:body></w:document>`,
	})
	if reason, _ := DocxEditBlock(tracked); reason != "tracked_changes" {
		t.Fatalf("tracked = %q", reason)
	}
	inside := officePackage(map[string]string{
		"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:insideH w:val="0"/></w:document>`,
	})
	if reason, _ := DocxEditBlock(inside); reason != "" {
		t.Fatalf("insideH = %q", reason)
	}

	dropped := officePackage(map[string]string{
		"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"/>`,
	})
	missing, err = DocxDroppedParts(plain, dropped)
	if err != nil || len(missing) != 1 || missing[0] != "customXml/item1.xml" {
		t.Fatalf("dropped = %v %v", missing, err)
	}
}
