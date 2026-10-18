package marketinstall

import "time"

const maxInserts = 3

const (
	CodeStaleCapabilities   = "stale.capabilities"
	CodeStaleTargetRevision = "stale.target_revision"
	CodeStaleMapping        = "stale.mapping"
	CodeStalePermission     = "stale.permission"
	CodeStalePolicy         = "stale.policy"
	CodeStaleRelease        = "stale.release"
	CodePlanHashMismatch    = "plan.hash_mismatch"
	CodeTaskNotRetryable    = "task_create_not_uniquely_retryable"
	CodeSquadNotRetryable   = "squad_create_not_uniquely_retryable"
	CodeNameCollision       = "name_collision"
	CodeContentMismatch     = "content_mismatch"
	CodePendingConfirmation = "pending_confirmation"
	CodeBlockedDependency   = "blocked_dependency"
	CodeCredentialInURL     = "credential_in_catalog_url"
	CodeLookupUnsupported   = "lookup_unsupported"
	CodeNativeWrite         = "native_write_failed"
)

type StaleCode string

type RefuseReason string

type Stale struct{ Code StaleCode }

func (s Stale) Error() string { return string(s.Code) }

type (
	WorkspaceID    string
	MemberID       string
	UserID         string
	OperationID    string
	InstanceID     string
	CorrelationID  string
	IdempotencyKey string
	PlanHash       string
	StepKey        string
	FenceToken     string
)

type Kind string

const (
	KindAgent     Kind = "agent"
	KindSkill     Kind = "skill"
	KindMCP       Kind = "mcp"
	KindBindSkill Kind = "bind_skill"
	KindBindMCP   Kind = "bind_mcp"
	KindTask      Kind = "task"
	KindSquad     Kind = "squad"
)

type Phase string

const (
	PhasePending Phase = "pending"
	PhaseRunning Phase = "running"
	PhaseArmed   Phase = "armed"
	PhaseUnknown Phase = "unknown"
	PhaseApplied Phase = "applied"
	PhaseRefused Phase = "refused"
	PhaseFailed  Phase = "failed"
)

type Mode string

const (
	ModeWrite  Mode = "write"
	ModeAdopt  Mode = "adopt"
	ModeBind   Mode = "bind"
	ModeRecord Mode = "record"
)

type Status string

const (
	StatusPlanned            Status = "planned"
	StatusRunning            Status = "running"
	StatusReconciling        Status = "reconciling"
	StatusSucceeded          Status = "succeeded"
	StatusPartiallySucceeded Status = "partially_succeeded"
	StatusFailed             Status = "failed"
)

type Readiness string

const (
	ReadinessReady    Readiness = "ready"
	ReadinessNotReady Readiness = "not_ready"
)

// Step is one recorded fact. Task and squad stay on ModeRecord and a refused
// or failed phase, so the driver has no write path for them.
type Step struct {
	Key        StepKey       `json:"key"`
	Kind       Kind          `json:"kind"`
	Phase      Phase         `json:"phase"`
	Mode       Mode          `json:"mode"`
	OutputID   CorrelationID `json:"output_id,omitempty"`
	Parent     CorrelationID `json:"parent_id,omitempty"`
	Child      CorrelationID `json:"child_id,omitempty"`
	Attempt    int           `json:"attempt,omitempty"`
	Reason     RefuseReason  `json:"reason,omitempty"`
	Name       string        `json:"name,omitempty"`
	Digest     string        `json:"digest,omitempty"`
	Transport  string        `json:"transport,omitempty"`
	ParentName string        `json:"parent_name,omitempty"`
	ChildName  string        `json:"child_name,omitempty"`
	ObservedAt time.Time     `json:"observed_at,omitempty"`
}

type Presence struct {
	Found      bool
	Definitive bool
	Digest     string
}

func installKind(kind Kind) bool {
	switch kind {
	case KindAgent, KindSkill, KindMCP, KindBindSkill, KindBindMCP:
		return true
	default:
		return false
	}
}

func statusOf(steps []Step) Status {
	pending, applied, refused := false, false, false
	sawInstall := false
	for _, step := range steps {
		if !installKind(step.Kind) {
			continue
		}
		sawInstall = true
		switch step.Phase {
		case PhaseArmed, PhaseUnknown:
			return StatusReconciling
		case PhaseRunning:
			return StatusRunning
		case PhasePending:
			pending = true
		case PhaseApplied:
			applied = true
		case PhaseRefused, PhaseFailed:
			refused = true
		}
	}
	if pending {
		if applied {
			return StatusRunning
		}
		return StatusPlanned
	}
	if refused && applied {
		return StatusPartiallySucceeded
	}
	if refused || !sawInstall {
		return StatusFailed
	}
	return StatusSucceeded
}

func readinessOf(steps []Step) Readiness {
	saw := false
	for _, step := range steps {
		if !installKind(step.Kind) {
			continue
		}
		saw = true
		if step.Phase != PhaseApplied {
			return ReadinessNotReady
		}
	}
	if !saw {
		return ReadinessNotReady
	}
	return ReadinessReady
}

func terminal(status Status) bool {
	switch status {
	case StatusSucceeded, StatusPartiallySucceeded, StatusFailed:
		return true
	default:
		return false
	}
}

// afterLookup is the unknown transition. A definitive miss re-arms for a
// later tick and does not insert in this call.
func afterLookup(step Step, saw Presence, now time.Time) (Step, error) {
	if step.Phase != PhaseUnknown {
		return step, errPhase
	}
	step.ObservedAt = now
	if !saw.Definitive {
		return step, nil
	}
	if saw.Found {
		step.Phase = PhaseApplied
		step.Reason = ""
		return step, nil
	}
	if step.Attempt+1 >= maxInserts {
		step.Phase = PhaseRefused
		step.Mode = ModeRecord
		step.Reason = CodePendingConfirmation
		return step, nil
	}
	step.Phase = PhaseArmed
	step.Attempt++
	step.Reason = ""
	return step, nil
}

func decideInsert(step Step, saw Presence, catalogDigest string) (bool, RefuseReason) {
	if step.Phase != PhaseArmed || !saw.Definitive || saw.Found {
		return false, CodePendingConfirmation
	}
	if step.Attempt >= maxInserts {
		return false, CodePendingConfirmation
	}
	if step.Kind == KindSkill || step.Kind == KindMCP {
		if catalogDigest == "" || catalogDigest != step.Digest {
			return false, RefuseReason(CodeStaleRelease)
		}
	}
	return true, ""
}

func mayInsert(step Step, saw Presence, catalogDigest string) bool {
	ok, _ := decideInsert(step, saw, catalogDigest)
	return ok
}
