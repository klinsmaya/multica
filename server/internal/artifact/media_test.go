package artifact

import "testing"

func TestResolveMediaTypeDoesNotTrustClientAlone(t *testing.T) {
	markdown := []byte("# report v1\n")
	got, ok := resolveMediaType(markdown, "text/markdown")
	if !ok || got != "text/markdown" {
		t.Fatalf("markdown = %q ok=%v", got, ok)
	}

	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	got, ok = resolveMediaType(png, "text/markdown")
	if ok {
		t.Fatalf("png declared as markdown was accepted as %q", got)
	}
	if got != "image/png" {
		t.Fatalf("sniffed type = %q, want image/png", got)
	}

	got, ok = resolveMediaType(png, "image/png")
	if !ok || got != "image/png" {
		t.Fatalf("png = %q ok=%v", got, ok)
	}
}
