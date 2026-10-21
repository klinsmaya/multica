package plugincontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// AgentDefinition is an agents/<key>.md file: YAML frontmatter describing the
// agent, then the Markdown instructions it runs with. The layout is the Claude
// Code agent file, so the same package installs in both.
//
// Only the fields a host can honor are read. `tools` is accepted and ignored:
// the host grants tools through the plugin's scopes and MCP approvals, not
// through a list the package wrote for itself.
type AgentDefinition struct {
	Description   string   `yaml:"description"`
	Model         string   `yaml:"model"`
	ThinkingLevel string   `yaml:"thinking_level"`
	Skills        []string `yaml:"skills"`
	Instructions  string   `yaml:"-"`
}

// MaxAgentDescriptionBytes bounds the one-line description shown in lists.
const MaxAgentDescriptionBytes = 1024

// ParseAgentDefinition reads an agent file. Frontmatter is optional; a file
// that is only instructions is an agent with defaults.
func ParseAgentDefinition(content []byte) (AgentDefinition, error) {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	var def AgentDefinition
	body := text
	if rest, ok := strings.CutPrefix(text, "---\n"); ok {
		front, after, closed := strings.Cut(rest, "\n---")
		if !closed {
			return AgentDefinition{}, errors.New("frontmatter is not closed")
		}
		var raw struct {
			Name          string `yaml:"name"`
			Description   string `yaml:"description"`
			Model         string `yaml:"model"`
			ThinkingLevel string `yaml:"thinking_level"`
			Skills        any    `yaml:"skills"`
		}
		if err := yaml.Unmarshal([]byte(front), &raw); err != nil {
			return AgentDefinition{}, fmt.Errorf("frontmatter is not valid YAML: %w", err)
		}
		def.Description = strings.TrimSpace(raw.Description)
		def.Model = strings.TrimSpace(raw.Model)
		def.ThinkingLevel = strings.TrimSpace(raw.ThinkingLevel)
		skills, err := stringList(raw.Skills)
		if err != nil {
			return AgentDefinition{}, fmt.Errorf("frontmatter skills: %w", err)
		}
		def.Skills = skills
		body = strings.TrimPrefix(after, "\n")
	}
	def.Instructions = strings.TrimSpace(body)
	if def.Instructions == "" {
		return AgentDefinition{}, errors.New("instructions are empty")
	}
	if len(def.Description) > MaxAgentDescriptionBytes {
		return AgentDefinition{}, fmt.Errorf("description exceeds %d bytes", MaxAgentDescriptionBytes)
	}
	for _, skill := range def.Skills {
		if !contributionKeyPattern.MatchString(skill) {
			return AgentDefinition{}, fmt.Errorf("skill name %q is invalid", skill)
		}
	}
	return def, nil
}

// stringList accepts a YAML sequence or a comma-separated scalar, which is
// how Claude Code agent files write their lists.
func stringList(value any) ([]string, error) {
	switch v := value.(type) {
	case nil:
		return nil, nil
	case string:
		var out []string
		for _, item := range strings.Split(v, ",") {
			if item = strings.TrimSpace(item); item != "" {
				out = append(out, item)
			}
		}
		return out, nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("expected strings, got %T", item)
			}
			if text = strings.TrimSpace(text); text != "" {
				out = append(out, text)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected a list, got %T", value)
	}
}

// SquadDefinition is a squads/<key>.json file: the agent resource that leads
// the squad and the agent resources that are its members. Keys refer to agent
// resources of the same manifest, never to agents that already exist in a
// workspace, so a package cannot capture someone else's agent.
type SquadDefinition struct {
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Lead        string   `json:"lead"`
	Members     []string `json:"members,omitempty"`
}

// ParseSquadDefinition reads a squad file and checks its shape; whether the
// named agents exist is checked against the manifest by the bundle reader.
func ParseSquadDefinition(content []byte) (SquadDefinition, error) {
	var def SquadDefinition
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&def); err != nil {
		return SquadDefinition{}, fmt.Errorf("squad file is invalid: %w", err)
	}
	def.Name = strings.TrimSpace(def.Name)
	def.Description = strings.TrimSpace(def.Description)
	def.Lead = strings.TrimSpace(def.Lead)
	if def.Lead == "" {
		return SquadDefinition{}, errors.New("squad lead is required")
	}
	if len(def.Name) > 200 {
		return SquadDefinition{}, errors.New("squad name exceeds 200 characters")
	}
	if len(def.Description) > MaxAgentDescriptionBytes {
		return SquadDefinition{}, fmt.Errorf("squad description exceeds %d bytes", MaxAgentDescriptionBytes)
	}
	seen := map[string]bool{def.Lead: true}
	members := make([]string, 0, len(def.Members))
	for _, member := range def.Members {
		member = strings.TrimSpace(member)
		if member == "" || seen[member] {
			continue
		}
		seen[member] = true
		members = append(members, member)
	}
	def.Members = members
	return def, nil
}
