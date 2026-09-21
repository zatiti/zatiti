package controller

import (
	"context"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// TestOutstandingModelDispatchIsNeverReportedAsStalled proves this
// package's own follow-up to the execution-dispatch-model-step gap fix
// (turns.go's header, gap 1): once a model_pending turn's prepare_session/
// model_step effect is actually dispatched (which it now always is, for a
// task-bound turn whose plan names a resolved responses-adapter tool), a
// tick that finds it still awaiting its own observation must never report
// a model_dispatch obligation -- that would mean "the controller has no
// path to dispatch this", which is no longer true once gap 1 closed.
//
// Before this fix, workKindProposal unconditionally called
// stalledModelDispatch for every model_pending turn, so this same scenario
// raised a model_dispatch obligation alongside the (separately legitimate)
// delivery obligation on every tick the effect remained outstanding --
// verified red before this fix landed.
func TestOutstandingModelDispatchIsNeverReportedAsStalled(t *testing.T) {
	f := newFx(t)
	f.blobs = newFakeBlobs()
	worker := contract.NewID()
	turn := f.turn(worker, "task", contract.NewID(), "pending")
	_ = f.turnAttempt(turn.id2())
	provider := f.adapter(modelAdapterName)
	// The adapter genuinely cannot answer yet (e.g. the provider has not
	// replied): the dispatched effect stays outstanding -- prepared, never
	// confirmed -- without abandoning the controller (a crash would instead
	// exercise the already-covered fenced-turn/outcome_unknown path).
	provider.reply = func(context.Context, contract.Dispatch) (contract.Observation, error) {
		return contract.Observation{}, &contract.Fault{
			Code: contract.CodeControllerUnavailable, Message: "provider has not answered yet", Retryable: true,
		}
	}

	c, sess := f.started()
	// Tick 0 only claims the turn (work.pending's own fair claim/context/
	// proposal ordering, turnWork/driveWorkItems); tick 1 advances it
	// through context.prepare/.commit (dispatching the effect) and the
	// effect-delivery phase (the adapter's own failed reply above) in the
	// same pass -- exactly this package's other crash/delivery tests
	// observe the same two-tick shape for a turn with no prior context.
	if err := f.pass(c, sess); err != nil {
		t.Fatalf("claim tick: %v", err)
	}
	if got := f.turnState(turn.id2()); got != "claimed" {
		t.Fatalf("turn state after the claim tick = %q, want claimed", got)
	}
	if err := f.pass(c, sess); err != nil {
		t.Fatalf("dispatch tick: %v", err)
	}
	if got := f.turnState(turn.id2()); got != "model_pending" {
		t.Fatalf("turn state after the dispatch tick = %q, want model_pending (dispatched, awaiting observation)", got)
	}
	if got := provider.calls(); got != 1 {
		t.Fatalf("adapter invoked %d times after the dispatch tick, want exactly 1", got)
	}

	for i := 0; i < 4; i++ {
		_ = f.pass(c, sess)
		if got := f.turnState(turn.id2()); got != "model_pending" {
			t.Fatalf("tick %d: turn state %q, want still model_pending (dispatched, awaiting observation)", i, got)
		}
		if got := provider.calls(); got != 1 {
			t.Fatalf("tick %d: adapter invoked %d times, want exactly 1 (dispatched once, never re-dispatched while outstanding)", i, got)
		}
		for _, o := range c.Status().Obligations {
			if o.Kind == obligationModelDispatch {
				t.Fatalf("tick %d: model_dispatch obligation reported for turn %s, want none -- its dispatched effect "+
					"is outstanding, not stalled: %+v", i, o.ResourceID, o)
			}
		}
	}
}
