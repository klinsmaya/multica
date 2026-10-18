package market

import (
	"strings"
	"time"
)

// Kind is a preflight demand. Knowledge has no grant table in Multica, so
// every knowledge demand is denied.
type Kind string

const (
	KindModel      Kind = "model"
	KindTool       Kind = "tool"
	KindConnection Kind = "connection"
	KindKnowledge  Kind = "knowledge"
)

const (
	ScopeReadOnly  = "read-only"
	ScopeReadWrite = "read-write"
)

const (
	StatusReady   = "ready"
	StatusBlocked = "blocked"
)

const (
	CodeModelDenied        = "model.denied"
	CodeToolDenied         = "tool.denied"
	CodeConnectionDenied   = "connection.denied"
	CodeKnowledgeDenied    = "knowledge.denied"
	CodeScopeKind          = "scope.kind"
	CodeScopeMismatch      = "scope.mapping_mismatch"
	CodeConsumerType       = "consumer.type"
	CodeIdentityUnverified = "identity.unverified"
	CodeIdentityExpired    = "identity.expired"
	CodeIdentityExpiry     = "identity.expiry"
	CodeDemandKind         = "demand.kind"
	CodeDemandScope        = "demand.scope"
)

const (
	verificationVerified = "verified"
	consumerMultica      = "multica"
	scopeWorkspace       = "workspace"
)

// Denial is one failed demand. Status is ready only when the list is empty.
type Denial struct {
	Kind Kind   `json:"kind"`
	Ref  string `json:"ref"`
	Code string `json:"code"`
}

// Demand is one capability the catalog resource needs from the workspace.
type Demand struct {
	Kind  Kind   `json:"kind"`
	Ref   string `json:"ref"`
	Scope string `json:"scope"`
}

// TargetScope is the workspace the mapping claims to install into.
type TargetScope struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// ConsumerRef is the protocol identity of the caller. Multica is the only
// consumer this server accepts.
type ConsumerRef struct {
	ConsumerType string `json:"consumerType"`
	ConsumerID   string `json:"consumerId"`
	TenantID     string `json:"tenantId"`
	SubjectID    string `json:"subjectId"`
}

// Identity is the draft-1 identityMapping subset carried on a preflight.
// Actor is not part of it. The handler drops any client actor before this
// value is built.
type Identity struct {
	MappingID    string
	Revision     int64
	ConsumerRef  ConsumerRef
	TargetScope  TargetScope
	Verification string
	Expiry       string
}

// SkillFact is the workspace skill row the checker needs.
type SkillFact struct {
	CreatedBy string
}

// Facts is everything the checker is allowed to know. The handler loads it.
// This package does not touch the database.
type Facts struct {
	ModelIDs      map[string]struct{}
	Skills        map[string]SkillFact
	MCPNames      map[string]struct{}
	ConnectionIDs map[string]struct{}
	VCSAvailable  bool
	ActorUserID   string
	Role          string
	ActorIsAgent  bool
}

// Result is the preflight answer. TaskStarted is always false. Nothing in
// this package inserts a task.
type Result struct {
	Status      string      `json:"status"`
	TaskStarted bool        `json:"taskStarted"`
	ActorUserID string      `json:"actorUserId"`
	TargetScope TargetScope `json:"targetScope"`
	Denials     []Denial    `json:"denials"`
}

// Error is a request the checker will not score. Code is the HTTP body code.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

// Gate checks the identity mapping against the authenticated workspace.
// now is the server clock. An expiry equal to now is expired.
func Gate(id Identity, workspaceID string, now time.Time) error {
	if id.ConsumerRef.ConsumerType != consumerMultica {
		return &Error{Code: CodeConsumerType, Message: "consumerType must be multica"}
	}
	if id.TargetScope.Kind != scopeWorkspace {
		return &Error{Code: CodeScopeKind, Message: "targetScope.kind must be workspace"}
	}
	if id.TargetScope.ID != workspaceID {
		return &Error{Code: CodeScopeMismatch, Message: "targetScope does not match this workspace"}
	}
	if id.Verification != verificationVerified {
		return &Error{Code: CodeIdentityUnverified, Message: "identity is not verified"}
	}
	expiry, err := parseExpiry(id.Expiry)
	if err != nil {
		return &Error{Code: CodeIdentityExpiry, Message: "expiry must be an RFC3339 timestamp"}
	}
	if !expiry.After(now) {
		return &Error{Code: CodeIdentityExpired, Message: "identity is expired"}
	}
	return nil
}

func parseExpiry(raw string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339Nano, raw)
}

// Evaluate scores demands against facts. status is ready only when denials
// is empty. TaskStarted is always false.
func Evaluate(demands []Demand, facts Facts, scope TargetScope) (Result, error) {
	denials := make([]Denial, 0)
	for _, demand := range demands {
		denial, err := checkDemand(demand, facts)
		if err != nil {
			return Result{}, err
		}
		if denial != nil {
			denials = append(denials, *denial)
		}
	}
	status := StatusReady
	if len(denials) > 0 {
		status = StatusBlocked
	}
	return Result{
		Status:      status,
		TaskStarted: false,
		ActorUserID: facts.ActorUserID,
		TargetScope: scope,
		Denials:     denials,
	}, nil
}

func checkDemand(demand Demand, facts Facts) (*Denial, error) {
	switch demand.Kind {
	case KindModel, KindTool, KindConnection, KindKnowledge:
	default:
		return nil, &Error{Code: CodeDemandKind, Message: "unknown demand kind"}
	}
	switch demand.Scope {
	case ScopeReadOnly, ScopeReadWrite:
	default:
		return nil, &Error{Code: CodeDemandScope, Message: "demand scope must be read-only or read-write"}
	}
	switch demand.Kind {
	case KindModel:
		if _, ok := facts.ModelIDs[demand.Ref]; !ok {
			return deny(KindModel, demand.Ref, CodeModelDenied), nil
		}
	case KindTool:
		if !toolAllowed(demand, facts) {
			return deny(KindTool, demand.Ref, CodeToolDenied), nil
		}
	case KindConnection:
		if !connectionAllowed(demand, facts) {
			return deny(KindConnection, demand.Ref, CodeConnectionDenied), nil
		}
	case KindKnowledge:
		return deny(KindKnowledge, demand.Ref, CodeKnowledgeDenied), nil
	}
	return nil, nil
}

func deny(kind Kind, ref, code string) *Denial {
	return &Denial{Kind: kind, Ref: ref, Code: code}
}

func toolAllowed(demand Demand, facts Facts) bool {
	prefix, name, ok := strings.Cut(demand.Ref, ":")
	if !ok || name == "" {
		return false
	}
	switch prefix {
	case "skill":
		skill, exists := facts.Skills[name]
		if !exists {
			return false
		}
		if demand.Scope == ScopeReadOnly {
			return true
		}
		return canManageRole(facts.Role) || (skill.CreatedBy != "" && skill.CreatedBy == facts.ActorUserID)
	case "mcp":
		if _, exists := facts.MCPNames[name]; !exists {
			return false
		}
		if demand.Scope == ScopeReadOnly {
			return true
		}
		if facts.ActorIsAgent || !canManageRole(facts.Role) {
			return false
		}
		return true
	default:
		return false
	}
}

func connectionAllowed(demand Demand, facts Facts) bool {
	if !facts.VCSAvailable {
		return false
	}
	if _, ok := facts.ConnectionIDs[demand.Ref]; !ok {
		return false
	}
	if demand.Scope == ScopeReadOnly {
		return true
	}
	return canManageRole(facts.Role)
}

func canManageRole(role string) bool {
	return role == "owner" || role == "admin"
}
