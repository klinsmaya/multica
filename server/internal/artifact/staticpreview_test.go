package artifact

import (
	"strings"
	"testing"
)

func TestSanitizeStaticDocumentStripsActiveHTML(t *testing.T) {
	payloads := []string{
		`<!DOCTYPE html><script>alert(1)</script><p>Hello</p>`,
		`<scr<script>ipt>alert(1)</scr<script>ipt><p>Hello</p>`,
		`<img src=x onerror=alert(1)>`,
		`<svg><script>alert(1)</script><a href="https://evil.example">x</a></svg>`,
		`<a href="javascript:alert(1)">click</a>`,
		`<a href="JaVaScRiPt:alert(1)">click</a>`,
		`<a href="&#106;avascript:alert(1)">click</a>`,
		`<iframe src="https://evil.example/frame"></iframe>`,
		`<object data="https://evil.example/obj"></object>`,
		`<embed src="https://evil.example/embed">`,
		`<form action="https://evil.example/post"><input name="password"></form>`,
		`<meta http-equiv="refresh" content="0;url=https://evil.example">`,
		`<meta http-equiv="Content-Security-Policy" content="default-src *">`,
		`<base href="https://evil.example/">`,
		`<link rel="stylesheet" href="https://evil.example/a.css">`,
		`<style>body{background:url(https://evil.example/a.png)}</style>`,
		`<div style="background:url(javascript:alert(1))">styled</div>`,
		`<math><mtext><table><mglyph><style><!--</style><img title="--&gt;&lt;img src=x onerror=alert(1)&gt;">`,
		`<p>+ADw-script+AD4-alert(1)+ADw-/script+AD4-</p>`,
		"<p>a\x00b</p><script>alert(1)</script>",
		`<img src="../secret.png"><img src="/workspaces/abc/secret"><img src="https://evil.example/a.png">`,
	}
	for _, payload := range payloads {
		doc, err := SanitizeStaticDocument("text/html", "page.html", []byte(payload))
		if err != nil {
			t.Fatalf("sanitize: %v", err)
		}
		body := string(doc.Body)
		lower := strings.ToLower(body)
		if doc.ContentType != "text/html; charset=utf-8" {
			t.Fatalf("content type %q", doc.ContentType)
		}
		for _, banned := range []string{
			"<script", "onerror", "javascript:", "https://", "http://", "url(", "<iframe",
			"<object", "<embed", "<form", "<meta", "<base", "<link", "<style", "default-src",
			"../", "/workspaces/",
		} {
			if strings.Contains(lower, banned) {
				t.Fatalf("payload %q left %q in %q", payload, banned, body)
			}
		}
	}
}

func TestSanitizeStaticDocumentKeepsStaticFragment(t *testing.T) {
	htmlDoc, err := SanitizeStaticDocument("text/html; charset=utf-7", "page.html", []byte(`<h1>Title</h1><p>Hello</p><a href="#notes">Notes</a><a href="https://evil.example">away</a>`))
	if err != nil {
		t.Fatal(err)
	}
	body := string(htmlDoc.Body)
	if htmlDoc.ContentType != "text/html; charset=utf-8" {
		t.Fatalf("html type %q", htmlDoc.ContentType)
	}
	for _, want := range []string{"<h1>Title</h1>", "<p>Hello</p>", `href="#notes"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in %s", want, body)
		}
	}
	if strings.Contains(body, "evil.example") {
		t.Fatalf("external link survived: %s", body)
	}

	svgDoc, err := SanitizeStaticDocument("image/svg+xml", "pic.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script><circle cx="10" cy="10" r="4" fill="#00f"></circle></svg>`))
	if err != nil {
		t.Fatal(err)
	}
	if svgDoc.Kind != "svg" || svgDoc.ContentType != "image/svg+xml" {
		t.Fatalf("svg doc %+v", svgDoc)
	}
	svg := string(svgDoc.Body)
	if !strings.Contains(svg, "<circle") || !strings.Contains(svg, `cx="10"`) || strings.Contains(strings.ToLower(svg), "<script") {
		t.Fatalf("svg = %s", svg)
	}
}

func TestStaticPreviewCSPIsServerOwned(t *testing.T) {
	if strings.Contains(StaticPreviewCSP, "allow-scripts") || strings.Contains(StaticPreviewCSP, "allow-same-origin") {
		t.Fatal(StaticPreviewCSP)
	}
	for _, directive := range []string{
		"sandbox", "default-src 'none'", "connect-src 'none'", "img-src 'none'",
		"font-src 'none'", "style-src 'none'", "script-src 'none'", "frame-src 'none'",
		"form-action 'none'", "base-uri 'none'", "frame-ancestors 'self'",
	} {
		if !strings.Contains(StaticPreviewCSP, directive) {
			t.Fatalf("csp missing %s: %s", directive, StaticPreviewCSP)
		}
	}
}

func TestPackageManifestRejectsEscapeAndDuplicates(t *testing.T) {
	ok, err := BuildManifest([]PackageEntry{{Path: "assets/plot.png"}, {Path: "notes/readme.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if !ok.Allows("assets/plot.png") || !ok.Allows("assets/./plot.png") {
		t.Fatal("registered path was refused")
	}
	if ok.Allows("assets/other.png") || SingleFileManifest().Allows("assets/plot.png") {
		t.Fatal("unregistered path was allowed")
	}
	bad := []string{
		"../secret.png",
		"/etc/passwd",
		"https://evil.example/a.png",
		"//evil.example/a.png",
		`assets\plot.png`,
		"C:/secret.png",
		"",
		"../assets/plot.png",
	}
	for _, path := range bad {
		if _, err := NormalizePackagePath(path); err == nil {
			t.Fatalf("accepted %q", path)
		}
		if ok.Allows(path) {
			t.Fatalf("manifest allowed %q", path)
		}
	}
	if _, err := BuildManifest([]PackageEntry{{Path: "assets/plot.png"}, {Path: "assets/./plot.png"}}); err == nil {
		t.Fatal("duplicate clean path was registered")
	}
	if _, err := BuildManifest([]PackageEntry{{Path: "assets/plot.png", Symlink: true}}); err == nil {
		t.Fatal("symlink was registered")
	}
}
