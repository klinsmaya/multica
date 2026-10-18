package marketinstall

import "testing"
import "time"

func TestStatusFoldIgnoresRefusedTask(t *testing.T) {
	steps := []Step{
		{Kind: KindAgent, Phase: PhaseApplied},
		{Kind: KindTask, Phase: PhaseRefused, Reason: CodeTaskNotRetryable},
	}
	if got := statusOf(steps); got != StatusSucceeded {
		t.Fatalf("status = %s", got)
	}
	if got := readinessOf(steps); got != ReadinessReady {
		t.Fatalf("readiness = %s", got)
	}
}

func TestStatusUnknownIsReconciling(t *testing.T) {
	steps := []Step{
		{Kind: KindSkill, Phase: PhaseApplied},
		{Kind: KindAgent, Phase: PhaseUnknown},
	}
	if got := statusOf(steps); got != StatusReconciling {
		t.Fatalf("status = %s", got)
	}
	if readinessOf(steps) != ReadinessNotReady {
		t.Fatal("unknown install step is ready")
	}
}

func TestPartialInstallIsNotReady(t *testing.T) {
	steps := []Step{
		{Kind: KindSkill, Phase: PhaseApplied},
		{Kind: KindAgent, Phase: PhaseRefused, Reason: CodeNameCollision},
		{Kind: KindTask, Phase: PhaseRefused},
	}
	if got := statusOf(steps); got != StatusPartiallySucceeded {
		t.Fatalf("status = %s", got)
	}
	if readinessOf(steps) != ReadinessNotReady {
		t.Fatal("refused install step is ready")
	}
}

func TestSquadOnlyIsFailed(t *testing.T) {
	steps := []Step{{Kind: KindSquad, Phase: PhaseRefused, Reason: CodeSquadNotRetryable}}
	if got := statusOf(steps); got != StatusFailed {
		t.Fatalf("status = %s", got)
	}
	if readinessOf(steps) != ReadinessNotReady {
		t.Fatal("squad refusal is ready")
	}
}

func TestAfterLookupDoesNotInsert(t *testing.T) {
	unknown := Step{Kind: KindAgent, Phase: PhaseUnknown, Mode: ModeWrite, Attempt: 0, OutputID: "id-1"}
	now := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)

	miss, err := afterLookup(unknown, Presence{Definitive: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	if miss.Phase != PhaseArmed || miss.Attempt != 1 {
		t.Fatalf("miss = %+v", miss)
	}
	if mayInsert(unknown, Presence{Definitive: true}, "digest") {
		t.Fatal("unknown step may insert")
	}
	if mayInsert(miss, Presence{Definitive: false}, "digest") {
		t.Fatal("non-definitive read may insert")
	}
	if !mayInsert(miss, Presence{Definitive: true}, "digest") {
		t.Fatal("re-armed miss cannot insert on a later tick")
	}

	hit, err := afterLookup(unknown, Presence{Found: true, Definitive: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	if hit.Phase != PhaseApplied {
		t.Fatalf("hit = %+v", hit)
	}
}

func TestPendingConfirmationStopsRearm(t *testing.T) {
	unknown := Step{Kind: KindAgent, Phase: PhaseUnknown, Attempt: maxInserts - 1}
	got, err := afterLookup(unknown, Presence{Definitive: true}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != PhaseRefused || got.Reason != CodePendingConfirmation {
		t.Fatalf("got = %+v", got)
	}
}
