package service

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/plugincontract"
)

// Agent and squad resources.
//
// Like skills, a plugin's agents and squads are ordinary rows in the existing
// tables, marked with the installation that contributed them. Two differences
// from skills follow from what the rows are:
//
//   - An agent needs a runtime, and a package cannot know the workspace's
//     machines. Agents are created unbound, which the dispatcher reports as
//     "runtime required" until a member binds one in the agent's settings.
//   - Agents and squads accumulate history (tasks, comments, memberships), so
//     uninstall and upgrade pruning archive them instead of deleting. The
//     ownership link stays, and a reinstall or a later version that brings the
//     name back re-adopts the archived row under its original identity.
//
// Resource keys are the names, as for skills: the consent screen listed them,
// and a file that disagrees must not install under another name.

// invocationTargetWorkspace mirrors the handler's constant: a template agent
// is invocable by every member of the workspace that installed it.
const invocationTargetWorkspace = "workspace"

// InstallAgentResources writes the manifest's agent resources and archives the
// ones this installation no longer declares. Called inside the install
// transaction, after InstallSkillResources so skill links resolve.
func (s *PluginService) InstallAgentResources(ctx context.Context, queries *db.Queries, installation db.PluginInstallation, manifest plugincontract.Manifest, userID pgtype.UUID) error {
	resources := resourcesOfType(manifest, plugincontract.ResourceAgent)
	keep := make([]string, 0, len(resources))
	for _, resource := range resources {
		keep = append(keep, resource.Key)
	}
	if _, err := queries.ArchivePluginAgentsNotIn(ctx, db.ArchivePluginAgentsNotInParams{
		PluginInstallationID: installation.ID,
		ArchivedBy:           userID,
		KeepNames:            keep,
	}); err != nil {
		return &PluginError{Kind: PluginErrorUnavailable, Message: "prune plugin agents", Err: err}
	}
	if len(resources) == 0 {
		return nil
	}

	skills, err := queries.ListPluginSkills(ctx, installation.ID)
	if err != nil {
		return &PluginError{Kind: PluginErrorUnavailable, Message: "list plugin skills", Err: err}
	}
	skillIDs := make(map[string]pgtype.UUID, len(skills))
	for _, skill := range skills {
		skillIDs[skill.Name] = skill.ID
	}

	for _, resource := range resources {
		raw, err := s.packageFile(ctx, queries, installation.PackageVersionID, resource.Entry)
		if err != nil {
			return err
		}
		definition, err := plugincontract.ParseAgentDefinition(raw)
		if err != nil {
			return pluginErrf(PluginErrorInvalid, "agent %q: %v", resource.Key, err)
		}
		description := definition.Description
		if description == "" {
			description = "Provided by the " + manifest.Name + " Plugin."
		}
		model := pgtype.Text{String: definition.Model, Valid: definition.Model != ""}
		thinking := pgtype.Text{String: definition.ThinkingLevel, Valid: definition.ThinkingLevel != ""}

		agent, err := s.upsertPluginAgent(ctx, queries, installation, resource.Key, description, definition.Instructions, model, thinking, userID)
		if err != nil {
			return err
		}
		if err := queries.CreateAgentInvocationTarget(ctx, db.CreateAgentInvocationTargetParams{
			AgentID:    agent.ID,
			TargetType: invocationTargetWorkspace,
			TargetID:   installation.WorkspaceID,
			CreatedBy:  userID,
		}); err != nil {
			return &PluginError{Kind: PluginErrorUnavailable, Message: "grant plugin agent access", Err: err}
		}
		// The template owns the skill set: links are rebuilt from the
		// definition so a skill the new version drops is unlinked too.
		if err := queries.RemoveAllAgentSkills(ctx, agent.ID); err != nil {
			return &PluginError{Kind: PluginErrorUnavailable, Message: "reset plugin agent skills", Err: err}
		}
		for _, name := range definition.Skills {
			skillID, ok := skillIDs[name]
			if !ok {
				return pluginErrf(PluginErrorInvalid, "agent %q uses skill %q, which this plugin does not contribute", resource.Key, name)
			}
			if err := queries.AddAgentSkill(ctx, db.AddAgentSkillParams{AgentID: agent.ID, SkillID: skillID}); err != nil {
				return &PluginError{Kind: PluginErrorUnavailable, Message: "link plugin agent skill", Err: err}
			}
		}
	}
	return nil
}

// upsertPluginAgent creates the agent or, when a row of that name already
// belongs to a plugin installation, updates and re-adopts it. A row a person
// created fails the install loudly, as a skill name clash does.
func (s *PluginService) upsertPluginAgent(ctx context.Context, queries *db.Queries, installation db.PluginInstallation, name, description, instructions string, model, thinking pgtype.Text, userID pgtype.UUID) (db.Agent, error) {
	existing, err := queries.GetWorkspaceAgentByName(ctx, db.GetWorkspaceAgentByNameParams{WorkspaceID: installation.WorkspaceID, Name: name})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		agent, err := queries.CreatePluginAgent(ctx, db.CreatePluginAgentParams{
			WorkspaceID:          installation.WorkspaceID,
			Name:                 name,
			Description:          description,
			OwnerID:              userID,
			Instructions:         instructions,
			Model:                model,
			ThinkingLevel:        thinking,
			PluginInstallationID: installation.ID,
		})
		if err != nil {
			if isUniqueViolation(err) {
				return db.Agent{}, pluginErrf(PluginErrorConflict, "an agent named %q already exists in this workspace", name)
			}
			return db.Agent{}, &PluginError{Kind: PluginErrorUnavailable, Message: "install plugin agent", Err: err}
		}
		return agent, nil
	case err != nil:
		return db.Agent{}, &PluginError{Kind: PluginErrorUnavailable, Message: "load plugin agent", Err: err}
	}
	if !existing.PluginInstallationID.Valid {
		return db.Agent{}, pluginErrf(PluginErrorConflict, "an agent named %q already exists in this workspace", name)
	}
	if existing.PluginInstallationID != installation.ID && !existing.ArchivedAt.Valid {
		return db.Agent{}, pluginErrf(PluginErrorConflict, "an agent named %q is provided by another installed plugin", name)
	}
	agent, err := queries.UpdatePluginAgent(ctx, db.UpdatePluginAgentParams{
		ID:                   existing.ID,
		Description:          description,
		Instructions:         instructions,
		Model:                model,
		ThinkingLevel:        thinking,
		PluginInstallationID: installation.ID,
	})
	if err != nil {
		return db.Agent{}, &PluginError{Kind: PluginErrorUnavailable, Message: "upgrade plugin agent", Err: err}
	}
	return agent, nil
}

// InstallSquadResources writes the manifest's squad resources over the agents
// InstallAgentResources just created. Members the workspace added by hand are
// kept; members the template lists are ensured present.
func (s *PluginService) InstallSquadResources(ctx context.Context, queries *db.Queries, installation db.PluginInstallation, manifest plugincontract.Manifest, userID pgtype.UUID) error {
	resources := resourcesOfType(manifest, plugincontract.ResourceSquad)
	keep := make([]string, 0, len(resources))
	definitions := make([]plugincontract.SquadDefinition, 0, len(resources))
	for _, resource := range resources {
		raw, err := s.packageFile(ctx, queries, installation.PackageVersionID, resource.Entry)
		if err != nil {
			return err
		}
		definition, err := plugincontract.ParseSquadDefinition(raw)
		if err != nil {
			return pluginErrf(PluginErrorInvalid, "squad %q: %v", resource.Key, err)
		}
		if definition.Name == "" {
			definition.Name = resource.Key
		}
		definitions = append(definitions, definition)
		keep = append(keep, definition.Name)
	}
	if _, err := queries.ArchivePluginSquadsNotIn(ctx, db.ArchivePluginSquadsNotInParams{
		PluginInstallationID: installation.ID,
		ArchivedBy:           userID,
		KeepNames:            keep,
	}); err != nil {
		return &PluginError{Kind: PluginErrorUnavailable, Message: "prune plugin squads", Err: err}
	}
	if len(definitions) == 0 {
		return nil
	}

	agents, err := queries.ListPluginAgents(ctx, installation.ID)
	if err != nil {
		return &PluginError{Kind: PluginErrorUnavailable, Message: "list plugin agents", Err: err}
	}
	agentIDs := make(map[string]pgtype.UUID, len(agents))
	archived := make(map[pgtype.UUID]bool)
	for _, agent := range agents {
		if agent.ArchivedAt.Valid {
			archived[agent.ID] = true
		} else {
			agentIDs[agent.Name] = agent.ID
		}
	}
	owned, err := queries.ListPluginSquads(ctx, installation.ID)
	if err != nil {
		return &PluginError{Kind: PluginErrorUnavailable, Message: "list plugin squads", Err: err}
	}
	byName := make(map[string]db.Squad, len(owned))
	for _, squad := range owned {
		byName[squad.Name] = squad
		// An agent this version dropped was archived by InstallAgentResources;
		// it leaves the template's squads too.
		members, err := queries.ListSquadMembers(ctx, squad.ID)
		if err != nil {
			return &PluginError{Kind: PluginErrorUnavailable, Message: "list plugin squad members", Err: err}
		}
		for _, member := range members {
			if member.MemberType == "agent" && archived[member.MemberID] {
				if _, err := queries.RemoveSquadMember(ctx, db.RemoveSquadMemberParams{SquadID: squad.ID, MemberType: "agent", MemberID: member.MemberID}); err != nil {
					return &PluginError{Kind: PluginErrorUnavailable, Message: "remove plugin squad member", Err: err}
				}
			}
		}
	}

	for _, definition := range definitions {
		leaderID, ok := agentIDs[definition.Lead]
		if !ok {
			return pluginErrf(PluginErrorInvalid, "squad %q: lead agent %q is not part of this plugin", definition.Name, definition.Lead)
		}
		var squad db.Squad
		existing, ok := byName[definition.Name]
		if !ok {
			archivedSquad, lookupErr := queries.GetArchivedPluginSquadByName(ctx, db.GetArchivedPluginSquadByNameParams{WorkspaceID: installation.WorkspaceID, Name: definition.Name})
			if lookupErr == nil {
				existing, ok = archivedSquad, true
			} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
				return &PluginError{Kind: PluginErrorUnavailable, Message: "load plugin squad", Err: lookupErr}
			}
		}
		if ok {
			squad, err = queries.UpdatePluginSquad(ctx, db.UpdatePluginSquadParams{
				ID:                   existing.ID,
				Name:                 definition.Name,
				Description:          definition.Description,
				LeaderID:             leaderID,
				PluginInstallationID: installation.ID,
			})
		} else {
			squad, err = queries.CreatePluginSquad(ctx, db.CreatePluginSquadParams{
				WorkspaceID:          installation.WorkspaceID,
				Name:                 definition.Name,
				Description:          definition.Description,
				LeaderID:             leaderID,
				CreatorID:            userID,
				PluginInstallationID: installation.ID,
			})
		}
		if err != nil {
			return &PluginError{Kind: PluginErrorUnavailable, Message: "install plugin squad", Err: err}
		}
		if err := s.reconcilePluginSquadMembers(ctx, queries, squad, leaderID, definition, agentIDs); err != nil {
			return err
		}
	}
	return nil
}

// reconcilePluginSquadMembers makes the lead the leader member and every
// template member a member. Other members are the workspace's business.
func (s *PluginService) reconcilePluginSquadMembers(ctx context.Context, queries *db.Queries, squad db.Squad, leaderID pgtype.UUID, definition plugincontract.SquadDefinition, agentIDs map[string]pgtype.UUID) error {
	current, err := queries.ListSquadMembers(ctx, squad.ID)
	if err != nil {
		return &PluginError{Kind: PluginErrorUnavailable, Message: "list plugin squad members", Err: err}
	}
	roles := make(map[pgtype.UUID]string, len(current))
	for _, member := range current {
		if member.MemberType == "agent" {
			roles[member.MemberID] = member.Role
		}
	}
	ensure := func(agentID pgtype.UUID, role string) error {
		if have, ok := roles[agentID]; ok {
			if have == role {
				return nil
			}
			_, err := queries.UpdateSquadMemberRole(ctx, db.UpdateSquadMemberRoleParams{SquadID: squad.ID, MemberType: "agent", MemberID: agentID, Role: role})
			return err
		}
		_, err := queries.AddSquadMember(ctx, db.AddSquadMemberParams{SquadID: squad.ID, MemberType: "agent", MemberID: agentID, Role: role})
		return err
	}
	// A previous lead that is now a plain member keeps its membership with
	// the member role; one leader at a time.
	for agentID, role := range roles {
		if role == "leader" && agentID != leaderID {
			if _, err := queries.UpdateSquadMemberRole(ctx, db.UpdateSquadMemberRoleParams{SquadID: squad.ID, MemberType: "agent", MemberID: agentID, Role: "member"}); err != nil {
				return &PluginError{Kind: PluginErrorUnavailable, Message: "update plugin squad member", Err: err}
			}
		}
	}
	if err := ensure(leaderID, "leader"); err != nil {
		return &PluginError{Kind: PluginErrorUnavailable, Message: "add plugin squad leader", Err: err}
	}
	for _, name := range definition.Members {
		agentID, ok := agentIDs[name]
		if !ok {
			return pluginErrf(PluginErrorInvalid, "squad %q: member agent %q is not part of this plugin", definition.Name, name)
		}
		if agentID == leaderID {
			continue
		}
		if err := ensure(agentID, "member"); err != nil {
			return &PluginError{Kind: PluginErrorUnavailable, Message: "add plugin squad member", Err: err}
		}
	}
	return nil
}

// ArchiveAgentResources archives the agents and squads an installation
// contributed. Called inside the uninstall transaction, before its skills are
// deleted, so the agent-skill links can be removed first.
func (s *PluginService) ArchiveAgentResources(ctx context.Context, queries *db.Queries, installation db.PluginInstallation) error {
	if err := queries.RemoveAgentSkillsByInstallationSkills(ctx, installation.ID); err != nil {
		return &PluginError{Kind: PluginErrorUnavailable, Message: "unlink plugin agent skills", Err: err}
	}
	if _, err := queries.ArchivePluginSquadsByInstallation(ctx, db.ArchivePluginSquadsByInstallationParams{PluginInstallationID: installation.ID}); err != nil {
		return &PluginError{Kind: PluginErrorUnavailable, Message: "archive plugin squads", Err: err}
	}
	if _, err := queries.ArchivePluginAgentsByInstallation(ctx, db.ArchivePluginAgentsByInstallationParams{PluginInstallationID: installation.ID}); err != nil {
		return &PluginError{Kind: PluginErrorUnavailable, Message: "archive plugin agents", Err: err}
	}
	return nil
}

func resourcesOfType(manifest plugincontract.Manifest, resourceType string) []plugincontract.Resource {
	resources := make([]plugincontract.Resource, 0, len(manifest.Contributes.Resources))
	for _, resource := range manifest.Contributes.Resources {
		if resource.Type == resourceType {
			resources = append(resources, resource)
		}
	}
	return resources
}
