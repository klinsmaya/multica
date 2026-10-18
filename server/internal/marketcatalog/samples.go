package marketcatalog

var sampleListings = []Listing{
	{
		Source: SourceStub,
		Summary: ListingSummary{
			ID:            "sample.pr-reviewer",
			Name:          "PR Reviewer",
			Summary:       "Reviews pull requests against your team's checklist.",
			Publisher:     "Multica samples",
			LatestVersion: "1.2.0",
		},
		Version:     "1.2.0",
		Description: "Reads the diff, checks it against the `code-review` skill, and comments on the pull request.",
		Manifest: Manifest{
			Agent: AgentTemplate{
				Name:         "PR Reviewer",
				Description:  "Reviews pull requests.",
				Instructions: "Review the pull request against the code-review skill. Comment on concrete problems only.",
			},
			Model: ModelNeed{Provider: "claude", Models: []string{"claude-sonnet-4-5", "claude-opus-4-1"}},
			Tools: []ToolNeed{{
				ServerName: "github",
				Template:   &McpTemplate{Transport: "http", URL: "https://api.githubcopilot.com/mcp/"},
			}},
			Connections: []ConnectionNeed{{Provider: ConnectionComposio, Toolkit: "github"}},
			Knowledge: []KnowledgeNeed{{
				Source: KnowledgeSkill,
				Name:   "code-review",
				Bundle: &SkillBundle{
					Description: "A short checklist for reviewing pull requests.",
					Content:     "# Code review\n\n- Does the change do what the title says?\n- Are errors handled at the boundary?\n- Is there a test for the new behavior?\n",
				},
			}},
		},
	},
	{
		Source: SourceStub,
		Summary: ListingSummary{
			ID:            "sample.pr-reviewer",
			Name:          "PR Reviewer",
			Summary:       "Reviews pull requests against your team's checklist.",
			Publisher:     "Multica samples",
			LatestVersion: "1.2.0",
		},
		Version:     "1.1.0",
		Description: "The earlier release, without the GitHub connection.",
		Manifest: Manifest{
			Agent: AgentTemplate{
				Name:         "PR Reviewer",
				Description:  "Reviews pull requests.",
				Instructions: "Review the pull request against the code-review skill.",
			},
			Model:     ModelNeed{Provider: "claude", Models: []string{"claude-sonnet-4-5"}},
			Tools:     []ToolNeed{{ServerName: "github"}},
			Knowledge: []KnowledgeNeed{{Source: KnowledgeSkill, Name: "code-review"}},
		},
	},
	{
		Source: SourceStub,
		Summary: ListingSummary{
			ID:            "sample.notes",
			Name:          "Meeting Notes",
			Summary:       "Turns a transcript into decisions and action items.",
			Publisher:     "Multica samples",
			LatestVersion: "0.3.0",
		},
		Version:     "0.3.0",
		Description: "Paste a transcript. The agent replies with decisions, owners, and dates.",
		Manifest: Manifest{
			Agent: AgentTemplate{
				Name:         "Meeting Notes",
				Description:  "Summarizes meetings.",
				Instructions: "List decisions, then action items with an owner and a date.",
			},
			Model: ModelNeed{Provider: "claude"},
		},
	},
	{
		Source: SourceStub,
		Summary: ListingSummary{
			ID:            "sample.squad-lead",
			Name:          "Squad Lead",
			Summary:       "Coordinates a squad of agents with shared memory.",
			Publisher:     "Multica samples",
			LatestVersion: "2.0.0",
		},
		Version:     "2.0.0",
		Description: "Needs squads and a memory store, which this workspace cannot provide yet.",
		Manifest: Manifest{
			Agent: AgentTemplate{
				Name:         "Squad Lead",
				Description:  "Coordinates other agents.",
				Instructions: "Split the issue into tasks and assign them to the squad.",
			},
			Model:       ModelNeed{Provider: "codex"},
			Knowledge:   []KnowledgeNeed{{Source: "hermes_memory", Name: "team-memory"}},
			Unsupported: []UnsupportedNeed{{Kind: "squad", Name: "review-squad"}},
		},
	},
}
