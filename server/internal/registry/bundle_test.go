package registry

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func zipFiles(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buffer.Bytes()
}

const testManifest = `{"name":"pr-helper","version":"1.0.0","displayName":"PR Helper","description":"Reviews pull requests.","metadata":{"multica":{"scopes":["issues:read"]}}}`

const testSkill = "---\nname: pr-review\ndescription: Review a pull request.\n---\n\nRead the diff.\n"

func TestParseArchiveReadsClaudeCodeLayout(t *testing.T) {
	pkg, err := ParseArchive(zipFiles(t, map[string]string{
		"pr-helper/.claude-plugin/plugin.json": testManifest,
		"pr-helper/skills/pr-review/SKILL.md":  testSkill,
		"pr-helper/agents/reviewer.md":         "# Reviewer\n",
		"pr-helper/.mcp.json":                  `{"mcpServers":{"docs":{"type":"http","url":"https://mcp.example.com/mcp"}}}`,
		"__MACOSX/pr-helper/._plugin.json":     "noise",
	}))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if pkg.Manifest.Name != "pr-helper" || pkg.Manifest.Version != "1.0.0" {
		t.Fatalf("manifest = %+v", pkg.Manifest)
	}
	if len(pkg.Skills) != 1 || pkg.Skills[0].Name != "pr-review" || pkg.Skills[0].Description != "Review a pull request." {
		t.Fatalf("skills = %+v", pkg.Skills)
	}
	if len(pkg.Agents) != 1 || pkg.Agents[0] != "reviewer" {
		t.Fatalf("agents = %v", pkg.Agents)
	}
	if len(pkg.MCPServers) != 1 || pkg.MCPServers[0].URL != "https://mcp.example.com/mcp" {
		t.Fatalf("mcp servers = %+v", pkg.MCPServers)
	}
	if _, ok := pkg.File("__MACOSX/pr-helper/._plugin.json"); ok {
		t.Fatal("archiver noise was kept")
	}
}

func TestCanonicalArchiveIsStableAcrossUploads(t *testing.T) {
	files := map[string]string{
		".claude-plugin/plugin.json": testManifest,
		"skills/pr-review/SKILL.md":  testSkill,
	}
	first, err := ParseArchive(zipFiles(t, files))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	nested := map[string]string{}
	for name, content := range files {
		nested["wrapped/"+name] = content
	}
	second, err := ParseArchive(zipFiles(t, nested))
	if err != nil {
		t.Fatalf("parse nested: %v", err)
	}
	if first.SHA256 != second.SHA256 || !bytes.Equal(first.Archive, second.Archive) {
		t.Fatal("the same files produced different archives")
	}
	reparsed, err := ParseArchive(first.Archive)
	if err != nil {
		t.Fatalf("parse canonical archive: %v", err)
	}
	if reparsed.SHA256 != first.SHA256 {
		t.Fatal("re-reading the canonical archive changed its sha256")
	}
}

func TestParseArchiveRefusesUnsafePackages(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"no manifest", map[string]string{"skills/a/SKILL.md": testSkill}, "must contain"},
		{"hooks", map[string]string{".claude-plugin/plugin.json": testManifest, "hooks/hooks.json": "{}"}, "hooks/ is not allowed"},
		{"commands", map[string]string{".claude-plugin/plugin.json": testManifest, "commands/run.md": "x"}, "commands/ is not allowed"},
		{"inline hooks", map[string]string{".claude-plugin/plugin.json": `{"name":"a","version":"1.0.0","hooks":{}}`}, `"hooks" is not allowed`},
		{"bad name", map[string]string{".claude-plugin/plugin.json": `{"name":"Bad Name","version":"1.0.0"}`}, "name must be"},
		{"bad version", map[string]string{".claude-plugin/plugin.json": `{"name":"a","version":"1"}`}, "semver"},
		{"stdio mcp", map[string]string{".claude-plugin/plugin.json": testManifest, ".mcp.json": `{"mcpServers":{"x":{"command":"npx"}}}`}, `"command"`},
		{"mcp credentials", map[string]string{".claude-plugin/plugin.json": testManifest, ".mcp.json": `{"x":{"url":"https://a.example.com","headers":{"Authorization":"t"}}}`}, `"headers"`},
		{"plain http mcp", map[string]string{".claude-plugin/plugin.json": testManifest, ".mcp.json": `{"x":{"url":"http://a.example.com"}}`}, "https"},
		{"skill name mismatch", map[string]string{".claude-plugin/plugin.json": testManifest, "skills/other/SKILL.md": testSkill}, "must equal the directory"},
		{"skill without SKILL.md", map[string]string{".claude-plugin/plugin.json": testManifest, "skills/a/notes.md": "x"}, "has no SKILL.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseArchive(zipFiles(t, tc.files))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestBuildMarketplaceMatchesClaudeCodeSchema(t *testing.T) {
	if got := MarketplaceName("@self"); got != "multica-self" {
		t.Fatalf("marketplace name = %q", got)
	}
	if got := BundlePath("self", "pr-helper", "1.0.0"); got != "/listings/@self/pr-helper/versions/1.0.0/bundle" {
		t.Fatalf("bundle path = %q", got)
	}
}
