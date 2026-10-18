package marketinstall

import (
	"github.com/multica-ai/multica/server/internal/marketcatalog"
)

func compile(plan Plan, admission Admission, listing marketcatalog.Listing) ([]Step, installPayload, bool, error) {
	if plan.Squad != nil {
		return []Step{{
			Key:    "squad",
			Kind:   KindSquad,
			Phase:  PhaseRefused,
			Mode:   ModeRecord,
			Reason: CodeSquadNotRetryable,
			Name:   plan.Squad.Name,
		}}, installPayload{}, false, nil
	}
	agent := listing.Manifest.Agent
	if plan.Agent.Name != agent.Name {
		return nil, installPayload{}, false, Stale{Code: CodeStaleRelease}
	}
	payload := installPayload{
		AgentName:         agent.Name,
		AgentDescription:  agent.Description,
		AgentInstructions: agent.Instructions,
		RuntimeID:         admission.RuntimeID,
		Model:             admission.ModelID,
	}
	for _, need := range listing.Manifest.Connections {
		payload.Toolkits = append(payload.Toolkits, need.Toolkit)
	}
	steps := []Step{{
		Key:    "agent",
		Kind:   KindAgent,
		Phase:  PhasePending,
		Mode:   ModeWrite,
		Name:   agent.Name,
		Digest: ContentDigest(agent.Instructions),
	}}

	skills, err := compileSkills(plan, admission, listing)
	if err != nil {
		return nil, installPayload{}, false, err
	}
	steps = append(steps, skills.steps...)
	payload.Skills = skills.bodies

	mcps, err := compileMCPs(plan, admission, listing)
	if err != nil {
		return nil, installPayload{}, false, err
	}
	steps = append(steps, mcps.steps...)
	payload.MCPs = mcps.bodies

	for _, step := range skills.steps {
		if step.Kind != KindSkill || step.Phase == PhaseRefused || step.Phase == PhaseFailed {
			continue
		}
		steps = append(steps, Step{
			Key:        StepKey("bind_skill:" + step.Name),
			Kind:       KindBindSkill,
			Phase:      PhasePending,
			Mode:       ModeBind,
			Name:       step.Name,
			ParentName: agent.Name,
			ChildName:  step.Name,
			Digest:     step.Digest,
		})
	}
	for _, step := range mcps.steps {
		if step.Kind != KindMCP || step.Phase == PhaseRefused || step.Phase == PhaseFailed {
			continue
		}
		steps = append(steps, Step{
			Key:        StepKey("bind_mcp:" + step.Name),
			Kind:       KindBindMCP,
			Phase:      PhasePending,
			Mode:       ModeBind,
			Name:       step.Name,
			ParentName: agent.Name,
			ChildName:  step.Name,
			Digest:     step.Digest,
		})
	}
	taskName := ""
	if plan.Task != nil {
		taskName = plan.Task.Name
	}
	steps = append(steps, Step{
		Key:    "task",
		Kind:   KindTask,
		Phase:  PhaseRefused,
		Mode:   ModeRecord,
		Reason: CodeTaskNotRetryable,
		Name:   taskName,
	})
	return steps, payload, true, nil
}

type compiledSkills struct {
	steps  []Step
	bodies []skillBody
}

type compiledMCPs struct {
	steps  []Step
	bodies []mcpBody
}

func compileSkills(plan Plan, admission Admission, listing marketcatalog.Listing) (compiledSkills, error) {
	pins := map[string]SkillPin{}
	for _, pin := range plan.Skills {
		pins[pin.Name] = pin
	}
	existing := map[string]ExistingResource{}
	for _, item := range admission.ExistingSkills {
		existing[item.Name] = item
	}
	var out compiledSkills
	seen := map[string]struct{}{}
	for _, need := range listing.Manifest.Knowledge {
		if need.Source != marketcatalog.KnowledgeSkill || need.Bundle == nil {
			continue
		}
		pin, ok := pins[need.Name]
		if !ok {
			return compiledSkills{}, Stale{Code: CodeStaleCapabilities}
		}
		seen[need.Name] = struct{}{}
		digest := SkillDigest(need.Bundle.Content, nil)
		if pin.Digest != digest {
			return compiledSkills{}, Stale{Code: CodeStaleRelease}
		}
		if prior, ok := existing[need.Name]; ok {
			if prior.ID == "" || prior.Digest == "" || prior.Digest != digest {
				return compiledSkills{}, Stale{Code: CodeStaleMapping}
			}
			out.steps = append(out.steps, Step{
				Key:      StepKey("skill:" + need.Name),
				Kind:     KindSkill,
				Phase:    PhasePending,
				Mode:     ModeAdopt,
				Name:     need.Name,
				Digest:   digest,
				OutputID: prior.ID,
			})
			continue
		}
		out.steps = append(out.steps, Step{
			Key:    StepKey("skill:" + need.Name),
			Kind:   KindSkill,
			Phase:  PhasePending,
			Mode:   ModeWrite,
			Name:   need.Name,
			Digest: digest,
		})
		out.bodies = append(out.bodies, skillBody{
			Name:        need.Name,
			Description: need.Bundle.Description,
			Content:     need.Bundle.Content,
			Digest:      digest,
		})
	}
	if len(seen) != len(pins) {
		return compiledSkills{}, Stale{Code: CodeStaleCapabilities}
	}
	return out, nil
}

func compileMCPs(plan Plan, admission Admission, listing marketcatalog.Listing) (compiledMCPs, error) {
	pins := map[string]MCPPin{}
	for _, pin := range plan.MCPs {
		pins[pin.Name] = pin
	}
	existing := map[string]ExistingResource{}
	for _, item := range admission.ExistingMCPs {
		existing[item.Name] = item
	}
	var out compiledMCPs
	seen := map[string]struct{}{}
	for _, need := range listing.Manifest.Tools {
		if need.Template == nil {
			continue
		}
		pin, ok := pins[need.ServerName]
		if !ok {
			return compiledMCPs{}, Stale{Code: CodeStaleCapabilities}
		}
		seen[need.ServerName] = struct{}{}
		if URLHasUserinfo(need.Template.URL) {
			out.steps = append(out.steps, Step{
				Key:    StepKey("mcp:" + need.ServerName),
				Kind:   KindMCP,
				Phase:  PhaseRefused,
				Mode:   ModeRecord,
				Name:   need.ServerName,
				Reason: CodeCredentialInURL,
			})
			continue
		}
		digest := MCPDigest(need.Template.Transport, need.Template.URL)
		if pin.Transport != need.Template.Transport || pin.URLDigest != digest {
			return compiledMCPs{}, Stale{Code: CodeStaleRelease}
		}
		if prior, ok := existing[need.ServerName]; ok {
			if prior.ID == "" || prior.Digest == "" || prior.Digest != digest {
				return compiledMCPs{}, Stale{Code: CodeStaleMapping}
			}
			out.steps = append(out.steps, Step{
				Key:       StepKey("mcp:" + need.ServerName),
				Kind:      KindMCP,
				Phase:     PhasePending,
				Mode:      ModeAdopt,
				Name:      need.ServerName,
				Digest:    digest,
				Transport: need.Template.Transport,
				OutputID:  prior.ID,
			})
			continue
		}
		out.steps = append(out.steps, Step{
			Key:       StepKey("mcp:" + need.ServerName),
			Kind:      KindMCP,
			Phase:     PhasePending,
			Mode:      ModeWrite,
			Name:      need.ServerName,
			Digest:    digest,
			Transport: need.Template.Transport,
		})
		out.bodies = append(out.bodies, mcpBody{
			Name:      need.ServerName,
			Transport: need.Template.Transport,
			URL:       need.Template.URL,
			Digest:    digest,
		})
	}
	if len(seen) != len(pins) {
		return compiledMCPs{}, Stale{Code: CodeStaleCapabilities}
	}
	return out, nil
}

func listingSkillDigests(listing marketcatalog.Listing) map[string]string {
	out := map[string]string{}
	for _, need := range listing.Manifest.Knowledge {
		if need.Bundle == nil {
			continue
		}
		out[need.Name] = SkillDigest(need.Bundle.Content, nil)
	}
	return out
}

func listingMCPDigests(listing marketcatalog.Listing) map[string]string {
	out := map[string]string{}
	for _, need := range listing.Manifest.Tools {
		if need.Template == nil || URLHasUserinfo(need.Template.URL) {
			continue
		}
		out[need.ServerName] = MCPDigest(need.Template.Transport, need.Template.URL)
	}
	return out
}
