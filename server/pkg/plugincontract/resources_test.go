package plugincontract_test

import (
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/plugincontract"
)

const agentBundleManifest = `{
  "manifest_version": 1,
  "key": "com.example.support",
  "name": "Support",
  "version": "1.0.0",
  "author": { "name": "Example" },
  "scopes": ["issues:read"],
  "contributes": {
    "resources": [
      { "type": "skill", "key": "triage", "entry": "skills/triage/SKILL.md" },
      { "type": "agent", "key": "triager", "entry": "agents/triager.md" },
      { "type": "agent", "key": "responder", "entry": "agents/responder.md" },
      { "type": "squad", "key": "support", "entry": "squads/support.json" }
    ]
  }
}`

const triagerAgent = "---\nname: triager\ndescription: Sorts tickets.\nmodel: claude-sonnet-5-5\nskills: [triage]\ntools: Read, Grep\n---\n\nTriage every ticket.\n"

func TestParseAgentDefinitionReadsFrontmatterAndInstructions(t *testing.T) {
	def, err := plugincontract.ParseAgentDefinition([]byte(triagerAgent))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if def.Description != "Sorts tickets." || def.Model != "claude-sonnet-5-5" || strings.Join(def.Skills, ",") != "triage" {
		t.Fatalf("definition = %+v", def)
	}
	if def.Instructions != "Triage every ticket." {
		t.Fatalf("instructions = %q", def.Instructions)
	}

	plain, err := plugincontract.ParseAgentDefinition([]byte("Just do it.\n"))
	if err != nil || plain.Instructions != "Just do it." || plain.Skills != nil {
		t.Fatalf("plain = %+v, err = %v", plain, err)
	}

	block, err := plugincontract.ParseAgentDefinition([]byte("---\nskills:\n  - a\n  - b\n---\nGo.\n"))
	if err != nil || strings.Join(block.Skills, ",") != "a,b" {
		t.Fatalf("block = %+v, err = %v", block, err)
	}
}

func TestParseAgentDefinitionRejections(t *testing.T) {
	for name, content := range map[string]string{
		"unclosed frontmatter": "---\nname: x\n",
		"empty instructions":   "---\nname: x\n---\n\n",
		"invalid skill name":   "---\nskills: [Not Valid]\n---\nGo.\n",
		"skills not a list":    "---\nskills: {a: 1}\n---\nGo.\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := plugincontract.ParseAgentDefinition([]byte(content)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestParseSquadDefinitionDedupesMembersAndRequiresLead(t *testing.T) {
	def, err := plugincontract.ParseSquadDefinition([]byte(`{"name":"Support","lead":"triager","members":["responder","triager","responder"]}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if def.Lead != "triager" || strings.Join(def.Members, ",") != "responder" {
		t.Fatalf("definition = %+v", def)
	}
	if _, err := plugincontract.ParseSquadDefinition([]byte(`{"members":["a"]}`)); err == nil {
		t.Fatal("a squad without a lead was accepted")
	}
	if _, err := plugincontract.ParseSquadDefinition([]byte(`{"lead":"a","leader":"b"}`)); err == nil {
		t.Fatal("an unknown field was accepted")
	}
}

func TestParseBundleReadsAgentAndSquadResources(t *testing.T) {
	files := map[string]string{
		plugincontract.ManifestFilename: agentBundleManifest,
		"skills/triage/SKILL.md":        "---\nname: triage\n---\n\nSort tickets.\n",
		"agents/triager.md":             triagerAgent,
		"agents/responder.md":           "Reply politely.\n",
		"squads/support.json":           `{"lead":"triager","members":["responder"]}`,
	}
	bundle, err := plugincontract.ParseBundle(zipBundle(t, files))
	if err != nil {
		t.Fatalf("ParseBundle: %v", err)
	}
	if len(bundle.Files) != 4 {
		t.Fatalf("files = %d, want 4", len(bundle.Files))
	}
	if err := bundle.Manifest.CheckCapabilities(plugincontract.HostCapabilities()); err != nil {
		t.Fatalf("agent and squad resources must be runnable by this host: %v", err)
	}

	cases := map[string]func(map[string]string){
		"squad names an unknown agent": func(f map[string]string) {
			f["squads/support.json"] = `{"lead":"ghost"}`
		},
		"agent file is empty": func(f map[string]string) {
			f["agents/responder.md"] = "\n"
		},
		"agent frontmatter is broken": func(f map[string]string) {
			f["agents/responder.md"] = "---\nskills: [\n---\nGo."
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			broken := map[string]string{}
			for k, v := range files {
				broken[k] = v
			}
			mutate(broken)
			if _, err := plugincontract.ParseBundle(zipBundle(t, broken)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestManifestResourceEntriesAreFixedPerType(t *testing.T) {
	manifest := strings.Replace(agentBundleManifest, `"entry": "squads/support.json"`, `"entry": "squads/other.json"`, 1)
	if _, _, err := plugincontract.ParseManifest([]byte(manifest)); err == nil || !strings.Contains(err.Error(), "squads/support.json") {
		t.Fatalf("err = %v", err)
	}
	shared := strings.Replace(agentBundleManifest, `"key": "triager", "entry": "agents/triager.md"`, `"key": "triage", "entry": "agents/triage.md"`, 1)
	if _, _, err := plugincontract.ParseManifest([]byte(shared)); err != nil {
		t.Fatalf("a skill and an agent may share a key: %v", err)
	}
}
