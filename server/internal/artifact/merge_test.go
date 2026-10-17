package artifact

import "testing"

func TestMergeRecognizedText(t *testing.T) {
	got, ok := mergeRecognizedText([]byte("a\nb\nc\n"), []byte("A\nb\nc\n"), []byte("a\nb\nC\n"))
	if !ok || string(got) != "A\nb\nC\n" {
		t.Fatalf("merge = %q, ok %v", got, ok)
	}
	if _, ok := mergeRecognizedText([]byte("a\nb\n"), []byte("A\nb\n"), []byte("Z\nb\n")); ok {
		t.Fatal("overlapping line merged")
	}
	if _, ok := mergeRecognizedText([]byte("a\n"), []byte("a\nb\n"), []byte("A\n")); ok {
		t.Fatal("unequal line counts merged")
	}
	if textMergeable("text/html", "page.html") || textMergeable("image/svg+xml", "pic.svg") {
		t.Fatal("active content is mergeable")
	}
	if !textMergeable("text/markdown", "notes.md") || !textMergeable("text/csv", "rows.csv") {
		t.Fatal("text format was not mergeable")
	}
}
