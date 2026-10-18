package handler

import (
	"crypto/sha1"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/marketcatalog"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var marketTestWorkspace = uuidFor("market-workspace")

func uuidFor(name string) pgtype.UUID {
	sum := sha1.Sum([]byte(name))
	var u pgtype.UUID
	copy(u.Bytes[:], sum[:16])
	u.Valid = true
	return u
}

func memberWithRole(name, role string) db.Member {
	return db.Member{WorkspaceID: marketTestWorkspace, UserID: uuidFor(name), Role: role}
}

// listingNeeding builds a live listing. Needs are ToolNeed, ConnectionNeed,
// KnowledgeNeed, or UnsupportedNeed values.
func listingNeeding(model marketcatalog.ModelNeed, needs ...any) marketcatalog.Listing {
	m := marketcatalog.Manifest{Agent: marketcatalog.AgentTemplate{Name: "Test agent"}, Model: model}
	for _, n := range needs {
		switch n := n.(type) {
		case marketcatalog.ToolNeed:
			m.Tools = append(m.Tools, n)
		case marketcatalog.ConnectionNeed:
			m.Connections = append(m.Connections, n)
		case marketcatalog.KnowledgeNeed:
			m.Knowledge = append(m.Knowledge, n)
		case marketcatalog.UnsupportedNeed:
			m.Unsupported = append(m.Unsupported, n)
		default:
			panic("listingNeeding: unknown need type")
		}
	}
	return marketcatalog.Listing{
		Source:   marketcatalog.SourceNocoBase,
		Summary:  marketcatalog.ListingSummary{ID: "acme.test", Name: "Test", LatestVersion: "1.0.0"},
		Version:  "1.0.0",
		Manifest: m,
	}
}

var usableModel = marketcatalog.ModelNeed{Provider: "claude", Models: []string{"claude-sonnet-4-5"}}

func runtimeFor(name, provider, visibility string, owner pgtype.UUID) db.AgentRuntime {
	return db.AgentRuntime{ID: uuidFor(name), WorkspaceID: marketTestWorkspace, Name: name, Provider: provider, Visibility: visibility, OwnerID: owner}
}

func catalogOf(ids ...string) *ModelCatalogSnapshot {
	snap := &ModelCatalogSnapshot{Supported: true}
	for _, id := range ids {
		snap.Models = append(snap.Models, ModelEntry{ID: id})
	}
	return snap
}

// factsWithUsableRuntime gives member one public claude runtime they own,
// whose catalog runs usableModel.
func factsWithUsableRuntime(member db.Member) preflightFacts {
	rt := runtimeFor("usable-runtime", "claude", "public", member.UserID)
	return preflightFacts{
		member:   member,
		runtimes: []db.AgentRuntime{rt},
		catalogs: map[string]*ModelCatalogSnapshot{uuidToString(rt.ID): catalogOf("claude-sonnet-4-5")},
	}
}

func (f preflightFacts) withComposio(connections ...db.UserComposioConnection) preflightFacts {
	f.composio = &composioFacts{connections: connections}
	return f
}

func TestPreflight_PrivateRuntimeOfAnotherMemberBlocksAdmin(t *testing.T) {
	admin := memberWithRole("admin", "admin")
	dana := uuidFor("dana")
	rt := runtimeFor("Dana's MacBook", "claude", "private", dana)
	facts := preflightFacts{
		member:   admin,
		runtimes: []db.AgentRuntime{rt},
		catalogs: map[string]*ModelCatalogSnapshot{uuidToString(rt.ID): catalogOf("claude-sonnet-4-5")},
	}

	report := evaluatePreflight(listingNeeding(usableModel), modelChoice{}, facts)
	if report.Status() != reportBlocked {
		t.Fatalf("status = %s, want blocked", report.Status())
	}
	if report.Model.Reason != reasonNoUsableRuntime {
		t.Fatalf("model reason = %s, want %s", report.Model.Reason, reasonNoUsableRuntime)
	}
	opt := report.Model.Options[0]
	if opt.Usable || opt.Reason != reasonRuntimePrivate || opt.RuntimeID != rt.ID || opt.Label != "Dana's MacBook" {
		t.Fatalf("option = %+v, want Dana's MacBook unusable as %s", opt, reasonRuntimePrivate)
	}

	chosen := evaluatePreflight(listingNeeding(usableModel), modelChoice{RuntimeID: rt.ID}, facts)
	if chosen.Model.Status != checkBlocked || chosen.Model.Reason != reasonRuntimePrivate {
		t.Fatalf("admin choosing the private runtime: model = %s/%s, want blocked/%s", chosen.Model.Status, chosen.Model.Reason, reasonRuntimePrivate)
	}
}

func TestPreflight_NamesEachDenial(t *testing.T) {
	member := memberWithRole("sam", "member")
	cases := []struct {
		name    string
		listing marketcatalog.Listing
		facts   preflightFacts
		pick    func(preflightReport) blockReason
		want    blockReason
	}{
		{
			"model",
			listingNeeding(marketcatalog.ModelNeed{Provider: "codex"}),
			factsWithUsableRuntime(member),
			func(r preflightReport) blockReason { return r.Model.Reason },
			reasonNoRuntimeForProvider,
		},
		{
			"tool",
			listingNeeding(usableModel, marketcatalog.ToolNeed{ServerName: "linear", Template: &marketcatalog.McpTemplate{Transport: "http", URL: "https://mcp.linear.app"}}),
			factsWithUsableRuntime(member),
			func(r preflightReport) blockReason { return r.Tools[0].Reason },
			reasonMcpLibraryAdminOnly,
		},
		{
			"connection",
			listingNeeding(usableModel, marketcatalog.ConnectionNeed{Provider: marketcatalog.ConnectionComposio, Toolkit: "github"}),
			factsWithUsableRuntime(member).withComposio(db.UserComposioConnection{ToolkitSlug: "slack"}),
			func(r preflightReport) blockReason { return r.Connections[0].Reason },
			reasonConnectionNotConnected,
		},
		{
			"knowledge",
			listingNeeding(usableModel, marketcatalog.KnowledgeNeed{Source: "hermes_memory", Name: "notes"}),
			factsWithUsableRuntime(member),
			func(r preflightReport) blockReason { return r.Knowledge[0].Reason },
			reasonKnowledgeSourceUnsupported,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := evaluatePreflight(tc.listing, modelChoice{}, tc.facts)
			if report.Status() != reportBlocked {
				t.Fatalf("status = %s, want blocked", report.Status())
			}
			if got := tc.pick(report); got != tc.want {
				t.Fatalf("reason = %s, want %s", got, tc.want)
			}
		})
	}
}

func readyListingAndFacts(member db.Member) (marketcatalog.Listing, preflightFacts) {
	l := listingNeeding(usableModel,
		marketcatalog.ToolNeed{ServerName: "github"},
		marketcatalog.ConnectionNeed{Provider: marketcatalog.ConnectionComposio, Toolkit: "github"},
		marketcatalog.KnowledgeNeed{Source: marketcatalog.KnowledgeSkill, Name: "code-review", Bundle: &marketcatalog.SkillBundle{Content: "# Review"}},
	)
	f := factsWithUsableRuntime(member).withComposio(db.UserComposioConnection{ID: uuidFor("conn"), ToolkitSlug: "GitHub"})
	f.mcpServers = []db.WorkspaceMcpServer{{ID: uuidFor("github-server"), Name: "github"}}
	return l, f
}

func TestPreflight_ReadyOnlyWhenEveryCheckPassesAndSourceIsLive(t *testing.T) {
	member := memberWithRole("sam", "member")
	l, f := readyListingAndFacts(member)

	report := evaluatePreflight(l, modelChoice{}, f)
	if report.Status() != reportReady {
		t.Fatalf("status = %s, want ready: %+v", report.Status(), report)
	}
	if report.Model.ModelID != "claude-sonnet-4-5" || report.Model.RuntimeID != uuidFor("usable-runtime") {
		t.Fatalf("model = %+v, want the only runtime and the listing's model", report.Model)
	}
	if b := report.Tools[0].Binding; b.Action != bindUseExisting || b.ID != uuidFor("github-server") {
		t.Fatalf("tool binding = %+v, want the existing github server", b)
	}
	if b := report.Connections[0].Binding; b.Action != bindUseExisting || b.ID != uuidFor("conn") {
		t.Fatalf("connection binding = %+v, want the member's github connection", b)
	}
	if b := report.Knowledge[0].Binding; b.Action != bindProvision {
		t.Fatalf("knowledge binding = %+v, want provision from the bundle", b)
	}
	if report.EvaluatedFor.UserID != member.UserID {
		t.Fatalf("evaluated for %s, want %s", uuidToString(report.EvaluatedFor.UserID), uuidToString(member.UserID))
	}

	l.Source = marketcatalog.SourceStub
	if got := evaluatePreflight(l, modelChoice{}, f).Status(); got != reportBlocked {
		t.Fatalf("stub listing status = %s, want blocked even with every check satisfied", got)
	}
}

func TestPreflight_ChooseAndBlockPrecedence(t *testing.T) {
	member := memberWithRole("sam", "member")
	a := runtimeFor("runtime-a", "claude", "public", member.UserID)
	b := runtimeFor("runtime-b", "claude", "public", uuidFor("someone"))
	f := preflightFacts{
		member:   member,
		runtimes: []db.AgentRuntime{a, b},
		catalogs: map[string]*ModelCatalogSnapshot{
			uuidToString(a.ID): catalogOf("claude-sonnet-4-5"),
			uuidToString(b.ID): catalogOf("claude-sonnet-4-5"),
		},
	}
	report := evaluatePreflight(listingNeeding(usableModel), modelChoice{}, f)
	if report.Status() != reportChoose || len(report.Model.Options) != 2 {
		t.Fatalf("status = %s with %d options, want choose with 2", report.Status(), len(report.Model.Options))
	}

	withBlock := evaluatePreflight(listingNeeding(usableModel, marketcatalog.UnsupportedNeed{Kind: "squad", Name: "s"}), modelChoice{}, f)
	if withBlock.Status() != reportBlocked || withBlock.Unsupported[0].Name != "squad" {
		t.Fatalf("status = %s, unsupported = %+v; want blocked naming squad", withBlock.Status(), withBlock.Unsupported)
	}

	picked := evaluatePreflight(listingNeeding(usableModel), modelChoice{RuntimeID: b.ID}, f)
	if picked.Status() != reportReady || picked.Model.RuntimeID != b.ID {
		t.Fatalf("picking runtime-b: status = %s runtime = %s", picked.Status(), uuidToString(picked.Model.RuntimeID))
	}
}

func TestPreflight_RuntimeChoiceReasons(t *testing.T) {
	member := memberWithRole("sam", "member")
	f := factsWithUsableRuntime(member)
	codex := runtimeFor("codex-runtime", "codex", "public", member.UserID)
	ownerless := runtimeFor("ownerless-runtime", "claude", "public", pgtype.UUID{})
	f.runtimes = append(f.runtimes, codex, ownerless)

	cases := []struct {
		name   string
		choice modelChoice
		want   blockReason
	}{
		{"invisible runtime", modelChoice{RuntimeID: uuidFor("hidden-runtime")}, reasonRuntimeNotFound},
		{"other provider", modelChoice{RuntimeID: codex.ID}, reasonRuntimeProviderMismatch},
		{"ownerless", modelChoice{RuntimeID: ownerless.ID}, reasonRuntimeOwnerless},
		{"model not in listing", modelChoice{ModelID: "claude-opus-4-1"}, reasonModelNotOffered},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := evaluatePreflight(listingNeeding(usableModel), tc.choice, f)
			if r.Model.Status != checkBlocked || r.Model.Reason != tc.want {
				t.Fatalf("model = %s/%s, want blocked/%s", r.Model.Status, r.Model.Reason, tc.want)
			}
		})
	}
}

func TestPreflight_ModelCatalogMatching(t *testing.T) {
	member := memberWithRole("sam", "member")
	rt := runtimeFor("rt", "claude", "public", member.UserID)
	id := uuidToString(rt.ID)
	need := marketcatalog.ModelNeed{Provider: "claude", Models: []string{"sonnet", "opus"}}

	cases := []struct {
		name       string
		snap       *ModelCatalogSnapshot
		wantReason blockReason
		wantModels []string
	}{
		{"unknown catalog", nil, reasonModelCatalogUnknown, nil},
		{"selection unsupported", &ModelCatalogSnapshot{Supported: false, Models: []ModelEntry{{ID: "sonnet"}}}, reasonModelSelectionUnsupported, nil},
		{"not offered", catalogOf("haiku"), reasonModelNotOffered, nil},
		{"only unavailable", &ModelCatalogSnapshot{Supported: true, Models: []ModelEntry{{ID: "haiku"}}, UnavailableModels: []UnavailableModelEntry{{ID: "opus"}}}, reasonModelUnavailable, nil},
		{"qualified id", &ModelCatalogSnapshot{Supported: true, Models: []ModelEntry{{ID: "anthropic/opus", Provider: "anthropic"}, {ID: "haiku"}}}, "", []string{"anthropic/opus"}},
		{"listing order", catalogOf("opus", "sonnet"), "", []string{"sonnet", "opus"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := preflightFacts{member: member, runtimes: []db.AgentRuntime{rt}, catalogs: map[string]*ModelCatalogSnapshot{}}
			if tc.snap != nil {
				f.catalogs[id] = tc.snap
			}
			r := evaluatePreflight(listingNeeding(need), modelChoice{}, f)
			opt := r.Model.Options[0]
			if opt.Reason != tc.wantReason {
				t.Fatalf("option reason = %q, want %q", opt.Reason, tc.wantReason)
			}
			if tc.wantModels != nil {
				if !slices.Equal(opt.Models, tc.wantModels) || r.Model.ModelID != tc.wantModels[0] {
					t.Fatalf("models = %v bound %q, want %v bound to the first", opt.Models, r.Model.ModelID, tc.wantModels)
				}
			}
		})
	}

	f := preflightFacts{member: member, runtimes: []db.AgentRuntime{rt}}
	r := evaluatePreflight(listingNeeding(marketcatalog.ModelNeed{Provider: "claude"}), modelChoice{}, f)
	if r.Model.Status != checkSatisfied || r.Model.ModelID != "" {
		t.Fatalf("listing with no pinned model: model = %+v, want satisfied on the runtime default", r.Model)
	}
}

func TestPreflight_ConnectionAndKnowledgeGaps(t *testing.T) {
	member := memberWithRole("sam", "member")
	f := factsWithUsableRuntime(member)
	l := listingNeeding(usableModel,
		marketcatalog.ConnectionNeed{Provider: "openconnector", Toolkit: "github"},
		marketcatalog.ConnectionNeed{Provider: marketcatalog.ConnectionComposio, Toolkit: "slack"},
		marketcatalog.KnowledgeNeed{Source: marketcatalog.KnowledgeSkill, Name: "missing"},
		marketcatalog.ToolNeed{ServerName: "unknown-server"},
	)
	r := evaluatePreflight(l, modelChoice{}, f)
	got := []blockReason{r.Connections[0].Reason, r.Connections[1].Reason, r.Knowledge[0].Reason, r.Tools[0].Reason}
	want := []blockReason{reasonConnectionProviderUnsupported, reasonConnectionsUnavailable, reasonSkillMissing, reasonMcpServerMissing}
	if !slices.Equal(got, want) {
		t.Fatalf("reasons = %v, want %v", got, want)
	}

	admin := memberWithRole("ada", "admin")
	adminFacts := factsWithUsableRuntime(admin)
	tool := listingNeeding(usableModel, marketcatalog.ToolNeed{ServerName: "linear", Template: &marketcatalog.McpTemplate{Transport: "http", URL: "https://mcp.linear.app"}})
	if c := evaluatePreflight(tool, modelChoice{}, adminFacts).Tools[0]; c.Status != checkSatisfied || c.Binding.Action != bindProvision {
		t.Fatalf("admin tool check = %+v, want provision", c)
	}
}

func TestMarketReaderIsReadOnly(t *testing.T) {
	rt := reflect.TypeOf((*marketReader)(nil)).Elem()
	if rt.NumMethod() == 0 {
		t.Fatal("marketReader has no methods")
	}
	for i := 0; i < rt.NumMethod(); i++ {
		name := rt.Method(i).Name
		if !strings.HasPrefix(name, "Get") && !strings.HasPrefix(name, "List") {
			t.Errorf("marketReader.%s is not a read; preflight must not write", name)
		}
	}
}

func TestBlockReasonsMatchJSON(t *testing.T) {
	raw, err := os.ReadFile("market_block_reasons.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Reasons []string `json:"reasons"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}

	file, err := parser.ParseFile(token.NewFileSet(), "market_preflight.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var consts []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			if ident, ok := vs.Type.(*ast.Ident); !ok || ident.Name != "blockReason" {
				continue
			}
			for _, v := range vs.Values {
				s, err := strconv.Unquote(v.(*ast.BasicLit).Value)
				if err != nil {
					t.Fatal(err)
				}
				consts = append(consts, s)
			}
		}
	}

	slices.Sort(consts)
	jsonReasons := slices.Clone(doc.Reasons)
	slices.Sort(jsonReasons)
	if len(slices.Compact(slices.Clone(jsonReasons))) != len(jsonReasons) {
		t.Fatalf("market_block_reasons.json has duplicates: %v", doc.Reasons)
	}
	if !slices.Equal(consts, jsonReasons) {
		t.Fatalf("blockReason consts and market_block_reasons.json differ\n go: %v\njson: %v", consts, jsonReasons)
	}
}
