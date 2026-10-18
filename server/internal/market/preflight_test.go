package market

import (
	"errors"
	"testing"
	"time"
)

func ownerFacts() Facts {
	return Facts{
		ActorUserID:   "owner-1",
		Role:          "owner",
		ModelIDs:      map[string]struct{}{"claude": {}},
		Skills:        map[string]SkillFact{"notes": {CreatedBy: "member-1"}},
		MCPNames:      map[string]struct{}{"linear": {}},
		ConnectionIDs: map[string]struct{}{"conn-1": {}},
		VCSAvailable:  true,
	}
}

func TestPreflight(t *testing.T) {
	member := ownerFacts()
	member.Role = "member"
	member.ActorUserID = "member-1"

	other := ownerFacts()
	other.Role = "member"
	other.ActorUserID = "member-2"

	agentOwner := ownerFacts()
	agentOwner.ActorIsAgent = true

	noVCS := ownerFacts()
	noVCS.VCSAvailable = false

	scope := TargetScope{Kind: "workspace", ID: "ws-1"}

	pass := []Demand{
		{Kind: KindModel, Ref: "claude", Scope: ScopeReadOnly},
		{Kind: KindTool, Ref: "skill:notes", Scope: ScopeReadWrite},
		{Kind: KindTool, Ref: "mcp:linear", Scope: ScopeReadWrite},
		{Kind: KindConnection, Ref: "conn-1", Scope: ScopeReadWrite},
	}

	tests := []struct {
		name    string
		facts   Facts
		demands []Demand
		want    []Denial
		errCode string
	}{
		{
			name:    "missing model",
			facts:   ownerFacts(),
			demands: []Demand{{Kind: KindModel, Ref: "missing", Scope: ScopeReadOnly}},
			want:    []Denial{{Kind: KindModel, Ref: "missing", Code: CodeModelDenied}},
		},
		{
			name:    "skill read-only missing",
			facts:   member,
			demands: []Demand{{Kind: KindTool, Ref: "skill:missing", Scope: ScopeReadOnly}},
			want:    []Denial{{Kind: KindTool, Ref: "skill:missing", Code: CodeToolDenied}},
		},
		{
			name:    "skill read-write by someone else",
			facts:   other,
			demands: []Demand{{Kind: KindTool, Ref: "skill:notes", Scope: ScopeReadWrite}},
			want:    []Denial{{Kind: KindTool, Ref: "skill:notes", Code: CodeToolDenied}},
		},
		{
			name:    "unknown tool prefix",
			facts:   ownerFacts(),
			demands: []Demand{{Kind: KindTool, Ref: "plugin:notes", Scope: ScopeReadOnly}},
			want:    []Denial{{Kind: KindTool, Ref: "plugin:notes", Code: CodeToolDenied}},
		},
		{
			name:    "mcp read-write for a member",
			facts:   member,
			demands: []Demand{{Kind: KindTool, Ref: "mcp:linear", Scope: ScopeReadWrite}},
			want:    []Denial{{Kind: KindTool, Ref: "mcp:linear", Code: CodeToolDenied}},
		},
		{
			name:    "mcp read-write for an agent owner",
			facts:   agentOwner,
			demands: []Demand{{Kind: KindTool, Ref: "mcp:linear", Scope: ScopeReadWrite}},
			want:    []Denial{{Kind: KindTool, Ref: "mcp:linear", Code: CodeToolDenied}},
		},
		{
			name:    "mcp missing",
			facts:   ownerFacts(),
			demands: []Demand{{Kind: KindTool, Ref: "mcp:missing", Scope: ScopeReadOnly}},
			want:    []Denial{{Kind: KindTool, Ref: "mcp:missing", Code: CodeToolDenied}},
		},
		{
			name:    "connection missing",
			facts:   ownerFacts(),
			demands: []Demand{{Kind: KindConnection, Ref: "missing", Scope: ScopeReadOnly}},
			want:    []Denial{{Kind: KindConnection, Ref: "missing", Code: CodeConnectionDenied}},
		},
		{
			name:    "connection integration unavailable",
			facts:   noVCS,
			demands: []Demand{{Kind: KindConnection, Ref: "conn-1", Scope: ScopeReadOnly}},
			want:    []Denial{{Kind: KindConnection, Ref: "conn-1", Code: CodeConnectionDenied}},
		},
		{
			name:    "connection read-write for a member",
			facts:   member,
			demands: []Demand{{Kind: KindConnection, Ref: "conn-1", Scope: ScopeReadWrite}},
			want:    []Denial{{Kind: KindConnection, Ref: "conn-1", Code: CodeConnectionDenied}},
		},
		{
			name:  "knowledge denies an owner",
			facts: ownerFacts(),
			demands: append(append([]Demand{}, pass...), Demand{
				Kind: KindKnowledge, Ref: "notes", Scope: ScopeReadOnly,
			}),
			want: []Denial{{Kind: KindKnowledge, Ref: "notes", Code: CodeKnowledgeDenied}},
		},
		{
			name:    "model tool and connection pass without knowledge",
			facts:   ownerFacts(),
			demands: pass,
		},
		{
			name:    "skill creator may write",
			facts:   member,
			demands: []Demand{{Kind: KindTool, Ref: "skill:notes", Scope: ScopeReadWrite}},
		},
		{
			name:    "unknown kind",
			facts:   ownerFacts(),
			demands: []Demand{{Kind: "credential", Ref: "x", Scope: ScopeReadOnly}},
			errCode: CodeDemandKind,
		},
		{
			name:    "unknown scope",
			facts:   ownerFacts(),
			demands: []Demand{{Kind: KindModel, Ref: "claude", Scope: "admin"}},
			errCode: CodeDemandScope,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Evaluate(tt.demands, tt.facts, scope)
			if tt.errCode != "" {
				var me *Error
				if !errors.As(err, &me) || me.Code != tt.errCode {
					t.Fatalf("error = %v, want code %s", err, tt.errCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if got.TaskStarted {
				t.Fatal("taskStarted = true")
			}
			if got.ActorUserID != tt.facts.ActorUserID {
				t.Fatalf("actorUserId = %s", got.ActorUserID)
			}
			if (got.Status == StatusReady) != (len(got.Denials) == 0) {
				t.Fatalf("status %s with %d denials", got.Status, len(got.Denials))
			}
			if len(got.Denials) != len(tt.want) {
				t.Fatalf("denials = %+v, want %+v", got.Denials, tt.want)
			}
			for i := range tt.want {
				if got.Denials[i] != tt.want[i] {
					t.Fatalf("denial %d = %+v, want %+v", i, got.Denials[i], tt.want[i])
				}
			}
		})
	}
}

func TestGate(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	valid := Identity{
		MappingID: "map-1",
		Revision:  1,
		ConsumerRef: ConsumerRef{
			ConsumerType: "multica",
			ConsumerID:   "multica",
			TenantID:     "ws-1",
			SubjectID:    "user-1",
		},
		TargetScope:  TargetScope{Kind: "workspace", ID: "ws-1"},
		Verification: "verified",
		Expiry:       now.Add(time.Hour).Format(time.RFC3339),
	}

	tests := []struct {
		name    string
		edit    func(*Identity)
		errCode string
	}{
		{name: "verified workspace", edit: func(*Identity) {}},
		{name: "other consumer", edit: func(id *Identity) { id.ConsumerRef.ConsumerType = "tencent" }, errCode: CodeConsumerType},
		{name: "scope kind", edit: func(id *Identity) { id.TargetScope.Kind = "squad" }, errCode: CodeScopeKind},
		{name: "scope mismatch", edit: func(id *Identity) { id.TargetScope.ID = "ws-2" }, errCode: CodeScopeMismatch},
		{name: "unverified", edit: func(id *Identity) { id.Verification = "unverified" }, errCode: CodeIdentityUnverified},
		{name: "rejected", edit: func(id *Identity) { id.Verification = "rejected" }, errCode: CodeIdentityUnverified},
		{name: "expired", edit: func(id *Identity) { id.Expiry = now.Format(time.RFC3339) }, errCode: CodeIdentityExpired},
		{name: "bad expiry", edit: func(id *Identity) { id.Expiry = "tomorrow" }, errCode: CodeIdentityExpiry},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := valid
			tt.edit(&id)
			err := Gate(id, "ws-1", now)
			if tt.errCode == "" {
				if err != nil {
					t.Fatalf("Gate: %v", err)
				}
				return
			}
			var me *Error
			if !errors.As(err, &me) || me.Code != tt.errCode {
				t.Fatalf("error = %v, want %s", err, tt.errCode)
			}
		})
	}
}
