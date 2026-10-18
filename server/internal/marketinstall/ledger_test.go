package marketinstall

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/marketcatalog"
)

func TestCommitIsIdempotent(t *testing.T) {
	ledger, native := testLedger(t, Admission{Ready: true, RuntimeID: "runtime-1"})
	doc := testDocument(t, testListing(), nil)
	first, err := ledger.Commit(context.Background(), testActor(), doc)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ledger.Commit(context.Background(), testActor(), doc)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("operation ids = %s and %s", first.ID, second.ID)
	}
	if native.agentWrites != 1 || native.skillWrites != 1 || native.mcpWrites != 1 || native.bindWrites != 2 {
		t.Fatalf("writes agent=%d skill=%d mcp=%d bind=%d", native.agentWrites, native.skillWrites, native.mcpWrites, native.bindWrites)
	}
	if native.taskWrites != 0 {
		t.Fatalf("task writes = %d", native.taskWrites)
	}
}

func TestUnknownReconcileDoesNotDoubleInstall(t *testing.T) {
	ledger, native := testLedger(t, Admission{Ready: true})
	native.push(KindAgent, "ambiguous-after")
	doc := testDocument(t, testListing(), nil)
	first, err := ledger.Commit(context.Background(), testActor(), doc)
	if err != nil {
		t.Fatal(err)
	}
	if native.agentWrites != 1 {
		t.Fatalf("agent writes after ambiguous insert = %d", native.agentWrites)
	}
	if phaseOf(first, KindAgent) != PhaseUnknown {
		t.Fatalf("agent phase = %s", phaseOf(first, KindAgent))
	}
	second, err := ledger.Reconcile(context.Background(), testActor(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if native.agentWrites != 1 {
		t.Fatalf("agent writes after reconcile = %d", native.agentWrites)
	}
	if phaseOf(second, KindAgent) != PhaseApplied {
		t.Fatalf("agent phase = %s", phaseOf(second, KindAgent))
	}
	if second.Readiness() != ReadinessReady {
		t.Fatalf("readiness = %s", second.Readiness())
	}
}

func TestUnknownMissRearmsWithoutInsert(t *testing.T) {
	ledger, native := testLedger(t, Admission{Ready: true})
	native.push(KindAgent, "ambiguous-none")
	doc := testDocument(t, testListing(), nil)
	first, err := ledger.Commit(context.Background(), testActor(), doc)
	if err != nil {
		t.Fatal(err)
	}
	if native.agentWrites != 0 || phaseOf(first, KindAgent) != PhaseUnknown {
		t.Fatalf("writes=%d phase=%s", native.agentWrites, phaseOf(first, KindAgent))
	}
	second, err := ledger.Reconcile(context.Background(), testActor(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if native.agentWrites != 0 || phaseOf(second, KindAgent) != PhaseArmed {
		t.Fatalf("reconcile writes=%d phase=%s", native.agentWrites, phaseOf(second, KindAgent))
	}
	if _, err := ledger.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if native.agentWrites != 1 {
		t.Fatalf("tick writes = %d", native.agentWrites)
	}
}

func TestReadyDoesNotCreateTask(t *testing.T) {
	ledger, native := testLedger(t, Admission{Ready: true})
	op, err := ledger.Commit(context.Background(), testActor(), testDocument(t, testListing(), nil))
	if err != nil {
		t.Fatal(err)
	}
	if op.Readiness() != ReadinessReady || op.Status() != StatusSucceeded {
		t.Fatalf("status=%s readiness=%s", op.Status(), op.Readiness())
	}
	task := stepOf(op, KindTask)
	if task.Phase != PhaseRefused || task.Reason != CodeTaskNotRetryable {
		t.Fatalf("task step = %+v", task)
	}
	if native.taskWrites != 0 {
		t.Fatalf("task writes = %d", native.taskWrites)
	}
	instances, err := ledger.ListInstances(context.Background(), testActor())
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].Readiness() != ReadinessReady {
		t.Fatalf("instances = %+v", instances)
	}
}

func TestSquadBlocksWithoutNativeInsert(t *testing.T) {
	ledger, native := testLedger(t, Admission{Ready: true})
	listing := testListing()
	doc := testDocument(t, listing, func(plan map[string]any) {
		plan["squad"] = map[string]any{"name": "Review squad"}
	})
	op, err := ledger.Commit(context.Background(), testActor(), doc)
	if err != nil {
		t.Fatal(err)
	}
	if native.agentWrites != 0 || native.skillWrites != 0 || native.mcpWrites != 0 || native.bindWrites != 0 {
		t.Fatalf("writes agent=%d skill=%d mcp=%d bind=%d", native.agentWrites, native.skillWrites, native.mcpWrites, native.bindWrites)
	}
	if len(op.Steps) != 1 || op.Steps[0].Kind != KindSquad || op.Steps[0].Reason != CodeSquadNotRetryable {
		t.Fatalf("steps = %+v", op.Steps)
	}
	if op.Status() != StatusFailed || op.InstanceID != "" {
		t.Fatalf("status=%s instance=%s", op.Status(), op.InstanceID)
	}
}

func TestIdempotencyConflictDoesNotDrive(t *testing.T) {
	ledger, native := testLedger(t, Admission{Ready: true})
	listing := testListing()
	firstDoc := testDocument(t, listing, nil)
	first, err := ledger.Commit(context.Background(), testActor(), firstDoc)
	if err != nil {
		t.Fatal(err)
	}
	secondDoc := testDocument(t, listing, func(plan map[string]any) {
		plan["modelId"] = "other-model"
	})
	second, err := ledger.Commit(context.Background(), testActor(), secondDoc)
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("err = %v", err)
	}
	if second.ID != first.ID || native.agentWrites != 1 {
		t.Fatalf("id=%s writes=%d", second.ID, native.agentWrites)
	}
}

func TestStaleGateWritesNoRow(t *testing.T) {
	ledger, native := testLedger(t, Admission{Ready: false, Code: CodeStaleMapping})
	_, err := ledger.Commit(context.Background(), testActor(), testDocument(t, testListing(), nil))
	var stale Stale
	if !errors.As(err, &stale) || stale.Code != CodeStaleMapping {
		t.Fatalf("err = %v", err)
	}
	if native.agentWrites != 0 {
		t.Fatalf("writes = %d", native.agentWrites)
	}
	if _, err := ledger.Get(context.Background(), testActor(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get err = %v", err)
	}
}

func TestMissingPolicyWritesNoRow(t *testing.T) {
	native := newScriptedNative()
	listing := testListing()
	listing.PolicyVersion = ""
	ledger := openMemory(newMemStore(), native, staticGate{admission: Admission{Ready: true}}, staticCatalog{listing: listing})
	_, err := ledger.Commit(context.Background(), testActor(), testDocument(t, testListing(), nil))
	var stale Stale
	if !errors.As(err, &stale) || stale.Code != CodeStalePolicy {
		t.Fatalf("err = %v", err)
	}
	if native.agentWrites != 0 {
		t.Fatal("insert ran without a policy pin")
	}
}

func TestNativePortHasNoTaskOrSquad(t *testing.T) {
	typ := reflect.TypeOf((*nativePort)(nil)).Elem()
	for i := 0; i < typ.NumMethod(); i++ {
		name := strings.ToLower(typ.Method(i).Name)
		if strings.Contains(name, "task") || strings.Contains(name, "squad") {
			t.Fatalf("native port method %s", typ.Method(i).Name)
		}
	}
}

type staticGate struct{ admission Admission }

func (g staticGate) Admit(context.Context, Actor, Plan, marketcatalog.Listing) (Admission, error) {
	return g.admission, nil
}

type staticCatalog struct{ listing marketcatalog.Listing }

func (c staticCatalog) Listing(context.Context, string, string) (marketcatalog.Listing, error) {
	return c.listing, nil
}

func testLedger(t *testing.T, admission Admission) (*Ledger, *scriptedNative) {
	t.Helper()
	native := newScriptedNative()
	ledger := openMemory(newMemStore(), native, staticGate{admission: admission}, staticCatalog{listing: testListing()})
	return ledger, native
}

func testActor() Actor {
	return Actor{WorkspaceID: "ws-1", MemberID: "member-1", UserID: "user-1"}
}

func testListing() marketcatalog.Listing {
	return marketcatalog.Listing{
		Source:         marketcatalog.SourceNocoBase,
		Version:        "1.0.0",
		PolicyVersion:  "pol-1",
		TargetRevision: 7,
		ReleaseDigest:  "rel-1",
		ReleaseState:   "published",
		Summary:        marketcatalog.ListingSummary{ID: "acme.reviewer", Name: "Reviewer"},
		Manifest: marketcatalog.Manifest{
			Agent: marketcatalog.AgentTemplate{Name: "Reviewer", Description: "Reviews diffs", Instructions: "be brief"},
			Knowledge: []marketcatalog.KnowledgeNeed{{
				Source: marketcatalog.KnowledgeSkill,
				Name:   "code-review",
				Bundle: &marketcatalog.SkillBundle{Description: "review", Content: "review the diff"},
			}},
			Tools: []marketcatalog.ToolNeed{{
				ServerName: "github",
				Template:   &marketcatalog.McpTemplate{Transport: "http", URL: "https://example.test/mcp"},
			}},
		},
	}
}

func testDocument(t *testing.T, listing marketcatalog.Listing, mutate func(plan map[string]any)) CommitDocument {
	t.Helper()
	plan := map[string]any{
		"listingId":      string(listing.Summary.ID),
		"version":        string(listing.Version),
		"policyVersion":  listing.PolicyVersion,
		"targetRevision": json.Number("7"),
		"releaseDigest":  listing.ReleaseDigest,
		"runtimeId":      "runtime-1",
		"modelId":        "model-1",
		"agent": map[string]any{
			"name":               listing.Manifest.Agent.Name,
			"instructionsDigest": ContentDigest(listing.Manifest.Agent.Instructions),
		},
		"skills": []any{map[string]any{
			"name":   "code-review",
			"digest": SkillDigest("review the diff", nil),
		}},
		"mcps": []any{map[string]any{
			"name":      "github",
			"transport": "http",
			"urlDigest": MCPDigest("http", "https://example.test/mcp"),
		}},
	}
	if mutate != nil {
		mutate(plan)
	}
	hash, err := hashObject(plan)
	if err != nil {
		t.Fatal(err)
	}
	plan["planHash"] = string(hash)
	body, err := json.Marshal(map[string]any{
		"planRef":        "plan-ref",
		"approvalRef":    "approval-ref",
		"idempotencyKey": "idempotency-key-1",
		"effect":         "none",
		"plan":           plan,
		"approval": map[string]any{
			"ref":           "approval-ref",
			"planHash":      string(hash),
			"policyVersion": listing.PolicyVersion,
			"expiresAt":     time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
		},
		"current": map[string]any{
			"policyVersion":           listing.PolicyVersion,
			"targetRevision":          json.Number("7"),
			"releaseDigest":           listing.ReleaseDigest,
			"releaseState":            "published",
			"agentInstructionsDigest": ContentDigest(listing.Manifest.Agent.Instructions),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ParseCommitDocument(body)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func phaseOf(op Operation, kind Kind) Phase {
	return stepOf(op, kind).Phase
}

func stepOf(op Operation, kind Kind) Step {
	for _, step := range op.Steps {
		if step.Kind == kind {
			return step
		}
	}
	return Step{}
}

func mustNumber(raw string) json.Number { return json.Number(raw) }
