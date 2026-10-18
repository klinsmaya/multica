package handler

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/integrations/composio"
	"github.com/multica-ai/multica/server/internal/marketcatalog"
	"github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type preflightInput struct {
	Listing marketcatalog.ListingID
	Version marketcatalog.Version
	Choice  modelChoice
}

// modelChoice is the only choice a member makes. Tools, connections, and
// knowledge resolve by name because each name is unique in its workspace
// table, so there is nothing to pick.
type modelChoice struct {
	RuntimeID pgtype.UUID // invalid means resolution picks
	ModelID   string      // "" means the listing's first acceptable id, or the runtime default
}

// marketReader is every database read preflight may make. *db.Queries bound to
// any transaction satisfies it. TestMarketReaderIsReadOnly fails if a method
// whose name does not start with Get or List is added.
type marketReader interface {
	runtimeLister
	ListWorkspaceMcpServers(ctx context.Context, workspaceID pgtype.UUID) ([]db.WorkspaceMcpServer, error)
	GetSkillByWorkspaceAndName(ctx context.Context, arg db.GetSkillByWorkspaceAndNameParams) (db.Skill, error)
	ListActiveUserComposioConnections(ctx context.Context, userID pgtype.UUID) ([]db.UserComposioConnection, error)
}

// runMarketRead runs fn in a READ ONLY transaction. Postgres rejects any write
// inside it, and REPEATABLE READ makes the reads share one snapshot.
func (h *Handler) runMarketRead(ctx context.Context, fn func(q marketReader) error) error {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin market read tx: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	// Must be the first statement: the isolation level cannot change once the
	// transaction has taken its snapshot.
	if _, err := tx.Exec(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY"); err != nil {
		return fmt.Errorf("set market read isolation: %w", err)
	}
	if err := fn(h.Queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// preflightFacts is everything evaluatePreflight reads. The loader fills it.
// Tests build it by hand.
type preflightFacts struct {
	member     db.Member
	runtimes   []db.AgentRuntime                // runtimesVisibleTo(member), every provider
	catalogs   map[string]*ModelCatalogSnapshot // runtime id to last good catalog. Absent means unknown.
	mcpServers []db.WorkspaceMcpServer          // loaded only when the manifest needs a tool
	skills     map[string]db.Skill              // by name, only the names the manifest asks for
	composio   *composioFacts                   // nil when Composio is not configured on this server
}

// composioFacts exists only when Composio is configured, so "configured" and
// "the member's connections" cannot disagree.
type composioFacts struct {
	connections []db.UserComposioConnection // the member's active connections, newest first
}

func (h *Handler) loadPreflightFacts(ctx context.Context, q marketReader, member db.Member, m marketcatalog.Manifest) (preflightFacts, error) {
	f := preflightFacts{member: member, catalogs: map[string]*ModelCatalogSnapshot{}, skills: map[string]db.Skill{}}
	runtimes, err := runtimesVisibleTo(ctx, q, member)
	if err != nil {
		return preflightFacts{}, fmt.Errorf("list runtimes: %w", err)
	}
	f.runtimes = runtimes
	for _, rt := range runtimes {
		id := uuidToString(rt.ID)
		if snap := h.cachedModelCatalog(ctx, id); snap != nil {
			f.catalogs[id] = snap
		}
	}
	if len(m.Tools) > 0 {
		if f.mcpServers, err = q.ListWorkspaceMcpServers(ctx, member.WorkspaceID); err != nil {
			return preflightFacts{}, fmt.Errorf("list mcp servers: %w", err)
		}
	}
	for _, need := range m.Knowledge {
		if need.Source != marketcatalog.KnowledgeSkill {
			continue
		}
		skill, err := q.GetSkillByWorkspaceAndName(ctx, db.GetSkillByWorkspaceAndNameParams{WorkspaceID: member.WorkspaceID, Name: need.Name})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return preflightFacts{}, fmt.Errorf("get skill %q: %w", need.Name, err)
		default:
			f.skills[need.Name] = skill
		}
	}
	if len(m.Connections) > 0 && h.Composio != nil {
		connections, err := q.ListActiveUserComposioConnections(ctx, member.UserID)
		if err != nil {
			return preflightFacts{}, fmt.Errorf("list composio connections: %w", err)
		}
		f.composio = &composioFacts{connections: connections}
	}
	return f, nil
}

// preflightInstall is the one entry point for "could this member install this
// listing". The HTTP handler calls it inside runMarketRead. M03 commit calls it
// inside its write transaction and refuses unless the result is ready.
func (h *Handler) preflightInstall(ctx context.Context, q marketReader, member db.Member, l marketcatalog.Listing, choice modelChoice) (preflightReport, error) {
	f, err := h.loadPreflightFacts(ctx, q, member, l.Manifest)
	if err != nil {
		return preflightReport{}, err
	}
	return evaluatePreflight(l, choice, f), nil
}

type checkStatus string

const (
	checkSatisfied checkStatus = "satisfied"
	checkBlocked   checkStatus = "blocked"
	checkChoose    checkStatus = "choose" // model only. More than one usable runtime and no choice.
)

// blockReason names why a check blocks. A missing object and a missing
// permission are both blocks. The reason, not a second status, tells them
// apart, and each reason has exactly one remedy in the UI copy.
//
// The values are the list in market_block_reasons.json. TestBlockReasonsMatchJSON
// parses this file with go/parser, collects every const of type blockReason,
// and fails when that set and the JSON differ.
type blockReason string

const (
	reasonNoRuntimeForProvider      blockReason = "no_runtime_for_provider"
	reasonNoUsableRuntime           blockReason = "no_usable_runtime"
	reasonRuntimeNotFound           blockReason = "runtime_not_found"
	reasonRuntimeProviderMismatch   blockReason = "runtime_provider_mismatch"
	reasonRuntimePrivate            blockReason = "runtime_private"
	reasonRuntimeOwnerless          blockReason = "runtime_ownerless"
	reasonModelCatalogUnknown       blockReason = "model_catalog_unknown"
	reasonModelSelectionUnsupported blockReason = "model_selection_unsupported"
	reasonModelUnavailable          blockReason = "model_unavailable"
	reasonModelNotOffered           blockReason = "model_not_offered"

	reasonMcpAttachForbidden  blockReason = "mcp_attach_forbidden"
	reasonMcpServerMissing    blockReason = "mcp_server_missing"
	reasonMcpLibraryAdminOnly blockReason = "mcp_library_admin_only"

	reasonConnectionProviderUnsupported blockReason = "connection_provider_unsupported"
	reasonConnectionsUnavailable        blockReason = "connections_unavailable"
	reasonConnectionNotConnected        blockReason = "connection_not_connected"

	reasonKnowledgeSourceUnsupported blockReason = "knowledge_source_unsupported"
	reasonSkillBindForbidden         blockReason = "skill_bind_forbidden"
	reasonSkillMissing               blockReason = "skill_missing"

	reasonRequirementUnsupported blockReason = "requirement_unsupported"
)

type bindAction string

const (
	bindUseExisting bindAction = "use_existing"
	bindProvision   bindAction = "provision" // the commit would create it from the listing
)

type binding struct {
	Action bindAction
	ID     pgtype.UUID // valid iff Action is bindUseExisting
}

// check is a tool, connection, knowledge, or unsupported row. Build one with
// satisfied or blocked only.
type check struct {
	Name    string
	Status  checkStatus // checkSatisfied or checkBlocked
	Reason  blockReason // set iff blocked
	Binding binding     // meaningful iff satisfied
}

func satisfied(name string, b binding) check {
	return check{Name: name, Status: checkSatisfied, Binding: b}
}
func blocked(name string, reason blockReason) check {
	return check{Name: name, Status: checkBlocked, Reason: reason}
}

type modelCheck struct {
	Provider  string
	Status    checkStatus     // any of the three
	Reason    blockReason     // set iff blocked
	RuntimeID pgtype.UUID     // valid iff satisfied
	ModelID   string          // "" means the runtime default
	Options   []runtimeOption // every visible runtime for Provider, each with its own verdict
}

type runtimeOption struct {
	RuntimeID pgtype.UUID
	Label     string
	Usable    bool
	Reason    blockReason // set iff !Usable
	Models    []string    // acceptable ids this runtime runs, listing order. All catalog ids when the listing pins none.
}

type preflightReport struct {
	Listing      marketcatalog.ListingSummary
	Version      marketcatalog.Version
	Source       marketcatalog.Source
	EvaluatedFor db.Member
	Model        modelCheck
	Tools        []check
	Connections  []check
	Knowledge    []check
	Unsupported  []check // always blocked
}

type reportStatus string

const (
	reportReady   reportStatus = "ready"
	reportBlocked reportStatus = "blocked"
	reportChoose  reportStatus = "choose"
)

// Status is derived and never stored. Any block wins over a pending choice,
// and a listing that did not come from the live catalog is never ready.
func (r preflightReport) Status() reportStatus {
	if r.Model.Status == checkBlocked || r.Source != marketcatalog.SourceNocoBase {
		return reportBlocked
	}
	for _, list := range [][]check{r.Tools, r.Connections, r.Knowledge, r.Unsupported} {
		for _, c := range list {
			if c.Status != checkSatisfied {
				return reportBlocked
			}
		}
	}
	if r.Model.Status != checkSatisfied {
		return reportChoose
	}
	return reportReady
}

// projectedAgent is the agent a workspace-scope install creates, the row M03
// commit inserts. Tool and knowledge checks judge it with the agent routes'
// own predicates, so preflight and commit agree by construction. It is private,
// so invoke rights and the owner's Composio connections stay with the member.
func projectedAgent(member db.Member) db.Agent {
	return db.Agent{WorkspaceID: member.WorkspaceID, OwnerID: member.UserID, PermissionMode: "private", Kind: "user"}
}

func evaluatePreflight(l marketcatalog.Listing, choice modelChoice, f preflightFacts) preflightReport {
	target := projectedAgent(f.member)
	m := l.Manifest
	r := preflightReport{
		Listing:      l.Summary,
		Version:      l.Version,
		Source:       l.Source,
		EvaluatedFor: f.member,
		Model:        checkModel(m.Model, choice, f),
		Tools:        make([]check, 0, len(m.Tools)),
		Connections:  make([]check, 0, len(m.Connections)),
		Knowledge:    make([]check, 0, len(m.Knowledge)),
		Unsupported:  make([]check, 0, len(m.Unsupported)),
	}
	for _, need := range m.Tools {
		r.Tools = append(r.Tools, checkTool(need, target, f))
	}
	for _, need := range m.Connections {
		r.Connections = append(r.Connections, checkConnection(need, f))
	}
	for _, need := range m.Knowledge {
		r.Knowledge = append(r.Knowledge, checkKnowledge(need, target, f))
	}
	for _, need := range m.Unsupported {
		r.Unsupported = append(r.Unsupported, blocked(need.Kind, reasonRequirementUnsupported))
	}
	return r
}

func checkModel(need marketcatalog.ModelNeed, choice modelChoice, f preflightFacts) modelCheck {
	mc := modelCheck{Provider: need.Provider, Options: []runtimeOption{}}
	for _, rt := range f.runtimes {
		if rt.Provider == need.Provider {
			mc.Options = append(mc.Options, runtimeOptionFor(f.member, rt, f.catalogs[uuidToString(rt.ID)], need))
		}
	}
	block := func(reason blockReason) modelCheck {
		mc.Status, mc.Reason = checkBlocked, reason
		return mc
	}

	var picked runtimeOption
	if choice.RuntimeID.Valid {
		i := slices.IndexFunc(f.runtimes, func(rt db.AgentRuntime) bool { return rt.ID == choice.RuntimeID })
		if i < 0 {
			return block(reasonRuntimeNotFound)
		}
		if f.runtimes[i].Provider != need.Provider {
			return block(reasonRuntimeProviderMismatch)
		}
		j := slices.IndexFunc(mc.Options, func(o runtimeOption) bool { return o.RuntimeID == choice.RuntimeID })
		picked = mc.Options[j]
		if !picked.Usable {
			return block(picked.Reason)
		}
	} else {
		var usable []runtimeOption
		for _, o := range mc.Options {
			if o.Usable {
				usable = append(usable, o)
			}
		}
		switch {
		case len(mc.Options) == 0:
			return block(reasonNoRuntimeForProvider)
		case len(usable) == 0:
			return block(reasonNoUsableRuntime)
		case len(usable) > 1:
			mc.Status = checkChoose
			return mc
		}
		picked = usable[0]
	}

	modelID, reason := bindModel(picked, need, choice.ModelID)
	if reason != "" {
		return block(reason)
	}
	mc.Status, mc.RuntimeID, mc.ModelID = checkSatisfied, picked.RuntimeID, modelID
	return mc
}

// runtimeOptionFor judges one runtime for one need.
func runtimeOptionFor(member db.Member, rt db.AgentRuntime, snap *ModelCatalogSnapshot, need marketcatalog.ModelNeed) runtimeOption {
	opt := runtimeOption{RuntimeID: rt.ID, Label: runtimeLabel(rt), Models: []string{}}
	if !canUseRuntimeForAgent(member, rt) {
		opt.Reason = reasonRuntimePrivate
		if !rt.OwnerID.Valid {
			opt.Reason = reasonRuntimeOwnerless
		}
		return opt
	}
	if len(need.Models) == 0 {
		opt.Usable = true
		if snap != nil && snap.Supported {
			for _, m := range snap.Models {
				opt.Models = append(opt.Models, m.ID)
			}
		}
		return opt
	}
	switch {
	case snap == nil:
		opt.Reason = reasonModelCatalogUnknown
	case !snap.Supported:
		opt.Reason = reasonModelSelectionUnsupported
	default:
		offered, reason := modelsOffered(snap, need.Models)
		if len(offered) == 0 {
			opt.Reason = reason
			return opt
		}
		opt.Usable, opt.Models = true, offered
	}
	return opt
}

func runtimeLabel(rt db.AgentRuntime) string {
	if rt.CustomName.Valid && strings.TrimSpace(rt.CustomName.String) != "" {
		return rt.CustomName.String
	}
	return rt.Name
}

// bindModel applies the member's model pick to a usable option. reason "" means
// bound. modelID "" means the runtime default. checkModel builds the modelCheck.
func bindModel(opt runtimeOption, need marketcatalog.ModelNeed, want string) (modelID string, reason blockReason) {
	switch {
	case want != "" && !slices.Contains(opt.Models, want):
		if len(opt.Models) == 0 {
			return "", reasonModelCatalogUnknown
		}
		return "", reasonModelNotOffered
	case want != "":
		return want, ""
	case len(need.Models) == 0:
		return "", ""
	default:
		return opt.Models[0], ""
	}
}

// modelsOffered returns the acceptable ids the runtime's discovered catalog
// runs, in listing order and in the catalog's spelling. Matching goes through
// agent.QualifyModelID, the rule the daemon applies, so a bare id that the
// catalog namespaces as provider/id still matches. reason is modelUnavailable
// when an acceptable id sits only in snap.UnavailableModels, else
// modelNotOffered.
func modelsOffered(snap *ModelCatalogSnapshot, acceptable []string) (offered []string, reason blockReason) {
	catalog := agent.Catalog{Models: make([]agent.Model, 0, len(snap.Models))}
	runs := map[string]bool{}
	for _, m := range snap.Models {
		catalog.Models = append(catalog.Models, agent.Model{ID: m.ID, Provider: m.Provider})
		runs[m.ID] = true
	}
	unavailable := map[string]bool{}
	for _, m := range snap.UnavailableModels {
		unavailable[m.ID] = true
	}
	sawUnavailable := false
	for _, want := range acceptable {
		id, _ := agent.QualifyModelID(catalog, want)
		switch {
		case runs[id]:
			if !slices.Contains(offered, id) {
				offered = append(offered, id)
			}
		case unavailable[want] || unavailable[id]:
			sawUnavailable = true
		}
	}
	switch {
	case len(offered) > 0:
		return offered, ""
	case sawUnavailable:
		return nil, reasonModelUnavailable
	default:
		return nil, reasonModelNotOffered
	}
}

func checkTool(need marketcatalog.ToolNeed, target db.Agent, f preflightFacts) check {
	if !canViewAgentSecrets(target, uuidToString(f.member.UserID), f.member.Role) {
		return blocked(need.ServerName, reasonMcpAttachForbidden)
	}
	for _, s := range f.mcpServers {
		if s.Name == need.ServerName {
			return satisfied(need.ServerName, binding{Action: bindUseExisting, ID: s.ID})
		}
	}
	if need.Template == nil {
		return blocked(need.ServerName, reasonMcpServerMissing)
	}
	if !canCurateMcpLibrary(f.member) {
		return blocked(need.ServerName, reasonMcpLibraryAdminOnly)
	}
	return satisfied(need.ServerName, binding{Action: bindProvision})
}

// canCurateMcpLibrary mirrors the owner/admin gate on
// POST /api/workspaces/{id}/mcp-servers. TestMarketMcpCuratorsMatchRouter
// fails if the two disagree for any role.
func canCurateMcpLibrary(member db.Member) bool { return roleAllowed(member.Role, "owner", "admin") }

func checkConnection(need marketcatalog.ConnectionNeed, f preflightFacts) check {
	if need.Provider != marketcatalog.ConnectionComposio {
		return blocked(need.Toolkit, reasonConnectionProviderUnsupported)
	}
	if f.composio == nil {
		return blocked(need.Toolkit, reasonConnectionsUnavailable)
	}
	// An empty result means dispatch would skip the overlay and the task
	// would run without the toolkit.
	for _, row := range composio.MountableToolkits(f.composio.connections, []string{need.Toolkit}) {
		return satisfied(need.Toolkit, binding{Action: bindUseExisting, ID: row.ID})
	}
	return blocked(need.Toolkit, reasonConnectionNotConnected)
}

// checkKnowledge marks a missing bundled skill provision because
// POST /api/skills admits every member. TestMarketSkillCreatorsMatchRouter
// fails if the route grows a role gate this check does not know.
func checkKnowledge(need marketcatalog.KnowledgeNeed, target db.Agent, f preflightFacts) check {
	if need.Source != marketcatalog.KnowledgeSkill {
		return blocked(need.Name, reasonKnowledgeSourceUnsupported)
	}
	if !canManageAgentEnv(target, f.member) {
		return blocked(need.Name, reasonSkillBindForbidden)
	}
	if skill, ok := f.skills[need.Name]; ok {
		return satisfied(need.Name, binding{Action: bindUseExisting, ID: skill.ID})
	}
	if need.Bundle == nil {
		return blocked(need.Name, reasonSkillMissing)
	}
	return satisfied(need.Name, binding{Action: bindProvision})
}
