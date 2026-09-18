package controller

import (
	"context"
	"reflect"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Z06: crash at every boundary around the provider call, restart, and count.
// The provider's physical call count and the owner's attempt records must
// agree exactly, and no recovery path may repeat a possibly sent effect.
func TestCrashAtEveryBoundaryNeverResends(t *testing.T) {
	retryable := &contract.Fault{Code: contract.CodeControllerUnavailable, Message: "busy", Retryable: true}
	cases := []struct {
		name string
		// arm injects the crash into the first lifetime.
		arm func(f *fx, provider *fakeAdapter)
		// callsAtCrash is the provider call count when the process dies.
		callsAtCrash int
		// After restart and recovery:
		calls        int
		attempts     int64
		state        string
		observations []string
		obligations  []string
		ambiguous    int64
	}{
		{
			name: "before admit commits",
			arm: func(f *fx, _ *fakeAdapter) {
				f.arm("_effects.admit", injection{fail: retryable, crash: true})
			},
			callsAtCrash: 0,
			// Nothing was admitted: the next generation dispatches it, once.
			calls: 1, attempts: 1, state: "succeeded", observations: []string{"physical:succeeded"},
		},
		{
			name: "after admit commits",
			arm: func(f *fx, _ *fakeAdapter) {
				f.arm("_effects.admit", injection{crash: true})
			},
			callsAtCrash: 0,
			// The attempt exists but cannot be named through any allowed call:
			// it is retained as a stranded admission and is never dispatched.
			calls: 0, attempts: 1, state: "ready", observations: nil,
			obligations: []string{"admission:prerequisite_missing"},
		},
		{
			name: "before claim commits",
			arm: func(f *fx, _ *fakeAdapter) {
				f.arm("_effects.claim", injection{fail: retryable, crash: true})
			},
			callsAtCrash: 0,
			// Unclaimed and never dispatched: authoritative non-execution.
			calls: 0, attempts: 1, state: "failed", observations: []string{"physical:not_sent"},
		},
		{
			name: "after claim commits",
			arm: func(f *fx, _ *fakeAdapter) {
				f.arm("_effects.claim", injection{crash: true})
			},
			callsAtCrash: 0,
			// The controller knows it never invoked the adapter, but the claim
			// is consumed, so the owner keeps the outcome unknown.
			calls: 0, attempts: 1, state: "outcome_unknown", observations: []string{"physical:not_sent"},
		},
		{
			name: "at network return",
			arm: func(f *fx, provider *fakeAdapter) {
				provider.reply = func(context.Context, contract.Dispatch) (contract.Observation, error) {
					f.crash()
					return succeeded(nil), nil
				}
			},
			callsAtCrash: 1,
			// The provider acted; the observation died with the process.
			calls: 1, attempts: 1, state: "outcome_unknown", observations: []string{"physical:unknown"}, ambiguous: 1,
		},
		{
			name: "before record commits",
			arm: func(f *fx, _ *fakeAdapter) {
				f.arm("_effects.record", injection{fail: retryable, crash: true})
			},
			callsAtCrash: 1,
			// The observation was journaled before the record: it is recorded
			// as observed, not downgraded, and the call is not repeated.
			calls: 1, attempts: 1, state: "succeeded", observations: []string{"physical:succeeded"},
		},
		{
			name: "after record commits",
			arm: func(f *fx, _ *fakeAdapter) {
				f.arm("_effects.record", injection{crash: true})
			},
			callsAtCrash: 1,
			// The record is replayed identically; history gains nothing.
			calls: 1, attempts: 1, state: "succeeded", observations: []string{"physical:succeeded"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFx(t)
			provider := f.adapter("synthetic")
			op := f.prepare("synthetic", nil)
			first, sess := f.started()
			tc.arm(f, provider)
			if err := f.pass(first, sess); err != nil {
				t.Fatalf("tick: %v", err)
			}
			if got := provider.calls(); got != tc.callsAtCrash {
				t.Fatalf("provider calls at crash: %d, want %d", got, tc.callsAtCrash)
			}
			before := f.generation()

			provider.reply = nil
			f.restart()
			if got := f.generation(); got != before+1 {
				t.Fatalf("generation %d after restart, want %d", got, before+1)
			}
			second, next := f.started()
			for i := 0; i < 4; i++ {
				if err := f.pass(second, next); err != nil {
					t.Fatalf("tick after restart: %v", err)
				}
			}
			if got := provider.calls(); got != tc.calls {
				t.Fatalf("provider calls after recovery: %d, want %d", got, tc.calls)
			}
			if got := f.attempts(op); got != tc.attempts {
				t.Fatalf("attempt records: %d, want %d", got, tc.attempts)
			}
			if got := f.opState(op); got != tc.state {
				t.Fatalf("operation is %s, want %s", got, tc.state)
			}
			if got := f.observations(op); !reflect.DeepEqual(got, tc.observations) {
				t.Fatalf("observations %v, want %v", got, tc.observations)
			}
			status := second.Status()
			if got := obligationKinds(status); !reflect.DeepEqual(got, append([]string{}, tc.obligations...)) {
				t.Fatalf("obligations %v, want %v", got, tc.obligations)
			}
			if status.Ambiguous != tc.ambiguous {
				t.Fatalf("ambiguous %d, want %d", status.Ambiguous, tc.ambiguous)
			}
			if provider.reconciles() != 0 {
				t.Fatal("recovery must not reconcile on its own authority")
			}
		})
	}
}

// Startup crash generation (Z10): a restart advances the persisted
// generation, fences the old one before anything is admitted, and records
// the old generation's claimed attempt under the generation that claimed it.
func TestRestartFencesOldGenerationBeforeAdmitting(t *testing.T) {
	f := newFx(t)
	provider := f.adapter("synthetic")
	provider.reply = func(context.Context, contract.Dispatch) (contract.Observation, error) {
		f.crash()
		return succeeded(nil), nil
	}
	op := f.prepare("synthetic", nil)
	first, sess := f.started()
	if err := f.pass(first, sess); err != nil {
		t.Fatalf("tick: %v", err)
	}
	old := sess.generation
	provider.reply = nil
	f.restart()
	fresh := f.prepare("synthetic", nil)

	// The fence is the first owner call of the new lifetime.
	admitsBefore := f.called("_effects.admit")
	second, next := f.started()
	if next.generation != old+1 {
		t.Fatalf("generation %d, want %d", next.generation, old+1)
	}
	if got := f.queryInt(`SELECT generation FROM execution_fences ORDER BY seq DESC LIMIT 1`); got != next.generation {
		t.Fatalf("fenced generation %d, want %d", got, next.generation)
	}
	if f.called("_effects.admit") != admitsBefore {
		t.Fatal("startup must not admit before fencing and recovery complete")
	}
	if got := f.opState(op); got != "outcome_unknown" {
		t.Fatalf("old claimed attempt is %s, want outcome_unknown", got)
	}
	// The unknown was recorded against the attempt's own generation.
	if got := f.queryInt(`SELECT generation FROM effects_attempts WHERE operation_id = ?`, string(op)); got != old {
		t.Fatalf("old attempt generation %d, want %d", got, old)
	}
	if err := f.pass(second, next); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if got := f.opState(fresh); got != "succeeded" {
		t.Fatalf("new work is %s, want succeeded", got)
	}
	if got := f.queryInt(`SELECT generation FROM effects_attempts WHERE operation_id = ?`, string(fresh)); got != next.generation {
		t.Fatalf("new attempt generation %d, want %d", got, next.generation)
	}
	if provider.calls() != 2 {
		t.Fatalf("provider calls %d, want 2 (one per operation)", provider.calls())
	}
}

// A controller whose generation was superseded stops admitting and reports
// why; it does not race the newer generation.
func TestSupersededGenerationStopsAdmitting(t *testing.T) {
	f := newFx(t)
	provider := f.adapter("synthetic")
	c, sess := f.started()
	if _, err := f.raw.StartGeneration(context.Background()); err != nil {
		t.Fatalf("StartGeneration: %v", err)
	}
	op := f.prepare("synthetic", nil)
	err := f.pass(c, sess)
	wantFault(t, err, contract.CodeControllerUnavailable)
	if provider.calls() != 0 || f.opState(op) != "prepared" {
		t.Fatal("a superseded controller must not admit")
	}
}

// Record write loss: a record that fails, or whose acknowledgement is lost,
// is retried as a record. The provider is never called again, and a durable
// refusal keeps the observation as evidence instead of resending.
func TestRecordWriteLossNeverAuthorizesAnotherSend(t *testing.T) {
	t.Run("acknowledgement lost after commit", func(t *testing.T) {
		f := newFx(t)
		provider := f.adapter("synthetic")
		op := f.prepare("synthetic", nil)
		c, sess := f.started()
		f.arm("_effects.record", injection{loseAck: true})
		for i := 0; i < 3; i++ {
			if err := f.pass(c, sess); err != nil {
				t.Fatalf("tick: %v", err)
			}
		}
		if provider.calls() != 1 {
			t.Fatalf("provider calls %d, want 1", provider.calls())
		}
		if got := f.observations(op); !reflect.DeepEqual(got, []string{"physical:succeeded"}) {
			t.Fatalf("observations %v", got)
		}
		if got := f.called("_effects.record"); got != 2 {
			t.Fatalf("record called %d times, want the original and one replay", got)
		}
	})
	t.Run("record unavailable for many ticks", func(t *testing.T) {
		f := newFx(t)
		provider := f.adapter("synthetic")
		op := f.prepare("synthetic", nil)
		c, sess := f.started()
		f.arm("_effects.record", injection{
			fail:   &contract.Fault{Code: contract.CodeControllerUnavailable, Message: "busy", Retryable: true},
			sticky: true,
		})
		for i := 0; i < 5; i++ {
			if err := f.pass(c, sess); err != nil {
				t.Fatalf("tick: %v", err)
			}
		}
		if provider.calls() != 1 || f.attempts(op) != 1 {
			t.Fatalf("calls %d attempts %d; a failed record must not cause another send", provider.calls(), f.attempts(op))
		}
		if got := f.opState(op); got != "executing" {
			t.Fatalf("operation is %s while the record is unavailable", got)
		}
		f.disarm("_effects.record")
		if err := f.pass(c, sess); err != nil {
			t.Fatalf("tick: %v", err)
		}
		if got := f.observations(op); !reflect.DeepEqual(got, []string{"physical:succeeded"}) {
			t.Fatalf("the journaled observation must be recorded as observed: %v", got)
		}
		if provider.calls() != 1 {
			t.Fatalf("provider calls %d, want 1", provider.calls())
		}
	})
	t.Run("record durably refused", func(t *testing.T) {
		f := newFx(t)
		provider := f.adapter("synthetic")
		op := f.prepare("synthetic", nil)
		c, sess := f.started()
		f.arm("_effects.record", injection{fail: &contract.Fault{Code: contract.CodeConflict, Message: "refused"}, sticky: true})
		for i := 0; i < 4; i++ {
			if err := f.pass(c, sess); err != nil {
				t.Fatalf("tick: %v", err)
			}
		}
		if provider.calls() != 1 || f.attempts(op) != 1 {
			t.Fatalf("calls %d attempts %d", provider.calls(), f.attempts(op))
		}
		if got := f.called("_effects.record"); got != 1 {
			t.Fatalf("a durable refusal was retried %d times", got)
		}
		if got := obligationKinds(c.Status()); !reflect.DeepEqual(got, []string{"record:conflict"}) {
			t.Fatalf("obligations %v", got)
		}
		// The refused observation survives compaction and restart as evidence.
		if err := sess.journal.compact(); err != nil {
			t.Fatalf("compact: %v", err)
		}
		f.restart()
		_, next := f.started()
		var kept *entry
		for _, e := range next.journal.snapshot() {
			if e.OperationID == op {
				e := e
				kept = &e
			}
		}
		if kept == nil || kept.Phase != phaseRefused || kept.Observation == nil ||
			kept.Observation.Disposition != contract.DispositionSucceeded {
			t.Fatalf("refused observation was not retained: %+v", kept)
		}
		if provider.calls() != 1 {
			t.Fatalf("provider calls %d after restart, want 1", provider.calls())
		}
	})
}
