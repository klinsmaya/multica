package service

import (
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/registry"
	"github.com/multica-ai/multica/server/pkg/plugincontract"
)

func registryPackage(t *testing.T, files map[string]string) registry.Package {
	t.Helper()
	list := make([]registry.File, 0, len(files))
	for path, content := range files {
		list = append(list, registry.File{Path: path, Content: []byte(content)})
	}
	pkg, err := registry.ParseFiles(list)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}
	return pkg
}

const registrySkill = "---\nname: pr-review\ndescription: Review a pull request.\n---\n\nRead the diff.\n"

func TestRegistryPluginKeyFoldsIntoKeySegments(t *testing.T) {
	cases := map[[2]string]string{
		{"@self", "pr-helper"}:     "self.pr-helper",
		{"@acme", "docs_tools.v2"}: "acme.docs-tools-v2",
	}
	for in, want := range cases {
		if got := RegistryPluginKey(in[0], in[1]); got != want {
			t.Errorf("RegistryPluginKey(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestRegistryBundleMapsSkillsAndMCPServers(t *testing.T) {
	pkg := registryPackage(t, map[string]string{
		".claude-plugin/plugin.json": `{"name":"pr-helper","version":"1.2.0","displayName":"PR Helper","author":{"name":"Platform"},"metadata":{"multica":{"scopes":["issues:read"]}}}`,
		"skills/pr-review/SKILL.md":  registrySkill,
		".mcp.json":                  `{"mcpServers":{"docs":{"type":"http","url":"https://mcp.example.com/mcp"}}}`,
	})
	bundle, err := RegistryBundle("@self", pkg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	manifest := bundle.Manifest
	if manifest.Key != "self.pr-helper" || manifest.Name != "PR Helper" || manifest.Version != "1.2.0" || manifest.Author.Name != "Platform" {
		t.Fatalf("manifest = %+v", manifest)
	}
	if strings.Join(manifest.Scopes, ",") != "issues:read,net:mcp.example.com" {
		t.Fatalf("scopes = %v", manifest.Scopes)
	}
	if len(manifest.Contributes.Resources) != 1 || manifest.Contributes.Resources[0].Entry != "skills/pr-review/SKILL.md" {
		t.Fatalf("resources = %+v", manifest.Contributes.Resources)
	}
	if content, ok := bundle.File("skills/pr-review/SKILL.md"); !ok || string(content) != registrySkill {
		t.Fatal("skill file was not carried into the bundle")
	}
	hooks := manifest.Contributes.Hooks
	if len(hooks) != 1 || hooks[0].Transport.Type != plugincontract.TransportMCP || hooks[0].Triggers[0] != plugincontract.TriggerAgent {
		t.Fatalf("hooks = %+v", hooks)
	}
}

func TestRegistryBundleAllowsSkillOnlyPackagesWithoutScopes(t *testing.T) {
	bundle, err := RegistryBundle("@self", registryPackage(t, map[string]string{
		".claude-plugin/plugin.json": `{"name":"pr-helper","version":"1.0.0"}`,
		"skills/pr-review/SKILL.md":  registrySkill,
	}))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(bundle.Manifest.Scopes) != 0 || bundle.Manifest.Author.Name != "self" {
		t.Fatalf("manifest = %+v", bundle.Manifest)
	}
}

func TestRegistryBundleRefusesWhatMulticaCannotRunYet(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"agents", map[string]string{".claude-plugin/plugin.json": `{"name":"a","version":"1.0.0"}`, "agents/reviewer.md": "# r", "skills/pr-review/SKILL.md": registrySkill}, "agent templates"},
		{"squad", map[string]string{".claude-plugin/plugin.json": `{"name":"a","version":"1.0.0","metadata":{"multica":{"squad":{"lead":"x"}}}}`, "skills/pr-review/SKILL.md": registrySkill}, "squad templates"},
		{"sse", map[string]string{".claude-plugin/plugin.json": `{"name":"a","version":"1.0.0"}`, ".mcp.json": `{"x":{"type":"sse","url":"https://a.example.com"}}`}, "streamable HTTP"},
		{"nothing to install", map[string]string{".claude-plugin/plugin.json": `{"name":"a","version":"1.0.0"}`}, "contributes"},
		{"unknown scope", map[string]string{".claude-plugin/plugin.json": `{"name":"a","version":"1.0.0","metadata":{"multica":{"scopes":["billing:write"]}}}`, "skills/pr-review/SKILL.md": registrySkill}, "unsupported scope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RegistryBundle("@self", registryPackage(t, tc.files))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
