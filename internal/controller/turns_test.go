package controller

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// replyEvidence/reportOutputsEvidence build the controlled model responses
// these tests deliver through the real adapter interface. They use this
// fixture's own fxToolProposal convenience shape (owners_test.go) -- the
// controller's own code (turns.go, observeTurnDelivery) only ever reads the
// "id" field generically; deciding what a proposal means is execution's
// job, faked here exactly as documented in turns_fixture_test.go's header.
func replyEvidence(t *testing.T, text string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"tool_proposals": []fxToolProposal{{ID: "p1", Kind: "reply", Text: text}},
	})
	if err != nil {
		t.Fatalf("encode reply evidence: %v", err)
	}
	return raw
}

func reportOutputsEvidence(t *testing.T, digest string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"tool_proposals": []fxToolProposal{{ID: "p1", Kind: "report_outputs", Digest: digest}},
	})
	if err != nil {
		t.Fatalf("encode report_outputs evidence: %v", err)
	}
	return raw
}

// modelObservation is a succeeded model_step Observation carrying the given
// evidence, matching what a real qualified provider adapter (P13) would
// hand the controller after one physical call.
func modelObservation(evidence json.RawMessage) contract.Observation {
	return contract.Observation{
		Disposition: contract.DispositionSucceeded, ProviderReference: "provider-ref-model",
		Evidence: evidence, Usage: json.RawMessage(fxUsage),
	}
}

// callbackTurnID reads the worker_turn callback route's turn_id off a
// dispatched action -- the same explicit, stored routing information P15/
// P16 attach when preparing an effect (contracts.md, "Effects callback
// routing"), which routeFor (deliver.go) reads back to route delivery.
func callbackTurnID(t *testing.T, d contract.Dispatch) contract.ID {
	t.Helper()
	var cb wireCallbackRoute
	if len(d.CallbackRoute) == 0 {
		t.Fatalf("dispatch %s carries no callback_route", d.OperationID)
	}
	if err := json.Unmarshal(d.CallbackRoute, &cb); err != nil {
		t.Fatalf("decode callback_route: %v", err)
	}
	if cb.Kind != "worker_turn" || cb.TurnID == "" {
		t.Fatalf("callback_route %+v does not name a worker_turn", cb)
	}
	return cb.TurnID
}

// TestTickDrivesRealMessageAndTaskAttemptsToReplyAndVerifiedResult is P22's
// first required behavioral test: a real message plus a controlled model
// response reaches a reply AND a verified task result, end to end, with no
// client left open/hanging at the end of the test.
//
// Turn A is task-triggered (already carries its own attempt, exactly as
// _execution.enqueue's automatic turn admission and work.claim's
// autoClaimHostedRun would have left it -- turns_fixture_test.go's f.turn/
// f.turnAttempt set this up directly, the same direct-insert pattern
// fixture_test.go's own f.prepare already uses). A real message is then
// admitted for the same worker before its turn is claimed: turn.admit's
// safe-boundary injection (P00-001) links it into that same active decision
// stream rather than starting a second one, which this test confirms by
// reading messaging_messages.turn_id back. The turn's controlled model
// response is a sealed "reply" decision, ending the turn completed --
// "reaches a reply".
//
// Turn B is a second, independent task-triggered turn whose controlled
// model response is "report_outputs": execution reports its attempt and
// admits a durable verification request, which the controller's own
// verification-driving phase claims and executes through the real
// contract.Verifier collaborator, recording an independently established
// "succeeded" outcome -- "a verified task result".
//
// Both turns dispatch their model_step effect through the SAME generic
// effect pipeline (tick.go/deliver.go) this package already owned before
// this card, extended here to resolve a worker_turn callback route
// (deliver.go, routeFor/routeWorkerTurn) instead of the legacy attempt_id-
// in-parameters inference -- proving that extension, not a shortcut around
// it.
func TestTickDrivesRealMessageAndTaskAttemptsToReplyAndVerifiedResult(t *testing.T) {
	f := newFx(t)
	f.blobs = newFakeBlobs()
	f.verifier = &fakeVerifier{}
	provider := f.adapter(modelAdapterName)

	workerA := contract.NewID()
	workerB := contract.NewID()

	turnA := f.turn(workerA, "task", contract.NewID(), "pending")
	_ = f.turnAttempt(turnA.id2())
	msg := f.message(workerA, "how's it going?")

	turnB := f.turn(workerB, "task", contract.NewID(), "pending")
	attemptB := f.turnAttempt(turnB.id2())

	provider.reply = func(_ context.Context, d contract.Dispatch) (contract.Observation, error) {
		switch callbackTurnID(t, d) {
		case turnA.id2():
			return modelObservation(replyEvidence(t, "doing well, thanks")), nil
		case turnB.id2():
			return modelObservation(reportOutputsEvidence(t, fxDigest)), nil
		default:
			t.Fatalf("model step dispatched for an unrecognized turn")
			return contract.Observation{}, nil
		}
	}

	c, sess := f.started()
	for i := 0; i < 12; i++ {
		if err := f.pass(c, sess); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}

	if got := f.messageTurn(msg); got != string(turnA.id2()) {
		t.Fatalf("message turn_id = %q, want %q (safe-boundary injection into the active task turn)", got, turnA.id2())
	}
	if got := f.turnState(turnA.id2()); got != "completed" {
		t.Fatalf("turn A state = %q, want completed (reply)", got)
	}
	if got := f.turnAttemptState(attemptB); got != "succeeded" {
		t.Fatalf("attempt B state = %q, want succeeded (independently verified)", got)
	}
	if n := f.verificationCount(attemptB); n != 1 {
		t.Fatalf("verification requests admitted for attempt B = %d, want exactly 1", n)
	}
	if got := provider.calls(); got != 2 {
		t.Fatalf("provider calls = %d, want exactly 2 (one model step per turn)", got)
	}

	// No client left open/hanging: Stop drains outstanding work and the
	// journal closes cleanly (registered by f.started's own t.Cleanup).
	if err := c.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestCrashBeforeClaimAfterContextPublicationAndAfterProviderResponseNeverResends
// is P22's second required behavioral test: a simulated crash at three
// different points -- before claim, after context publication, and after
// the provider response was received -- each resumes safely afterward
// without any duplicate model/tool mutation (no double-dispatch, no double
// WorkerOperator call).
func TestCrashBeforeClaimAfterContextPublicationAndAfterProviderResponseNeverResends(t *testing.T) {
	retryable := &contract.Fault{Code: contract.CodeControllerUnavailable, Message: "busy", Retryable: true}

	t.Run("before claim", func(t *testing.T) {
		f := newFx(t)
		f.blobs = newFakeBlobs()
		worker := contract.NewID()
		turn := f.turn(worker, "task", contract.NewID(), "pending")
		_ = f.turnAttempt(turn.id2())
		provider := f.adapter(modelAdapterName)
		provider.reply = func(context.Context, contract.Dispatch) (contract.Observation, error) {
			return modelObservation(replyEvidence(t, "hi")), nil
		}

		c, sess := f.started()
		f.arm("_execution.work.claim", injection{fail: retryable, crash: true})
		if err := f.pass(c, sess); err != nil {
			t.Fatalf("tick before crash: %v", err)
		}
		// Nothing committed: the turn is still pending, and no adapter call
		// happened yet -- there is nothing to recover, only to retry.
		if got := f.turnState(turn.id2()); got != "pending" {
			t.Fatalf("turn state before restart = %q, want pending (claim never committed)", got)
		}
		if got := provider.calls(); got != 0 {
			t.Fatalf("provider calls before restart = %d, want 0", got)
		}

		f.restart()
		second, next := f.started()
		t.Cleanup(func() { _ = second.Stop(context.Background()) })
		for i := 0; i < 8; i++ {
			if err := f.pass(second, next); err != nil {
				t.Fatalf("tick after restart: %v", err)
			}
		}
		if got := f.turnState(turn.id2()); got != "completed" {
			t.Fatalf("turn state after recovery = %q, want completed", got)
		}
		if got := provider.calls(); got != 1 {
			t.Fatalf("provider calls after recovery = %d, want exactly 1 (claimed and dispatched exactly once)", got)
		}
	})

	t.Run("after context publication", func(t *testing.T) {
		f := newFx(t)
		blobs := newFakeBlobs()
		f.blobs = blobs
		worker := contract.NewID()
		turn := f.turn(worker, "task", contract.NewID(), "pending")
		_ = f.turnAttempt(turn.id2())
		provider := f.adapter(modelAdapterName)
		provider.reply = func(context.Context, contract.Dispatch) (contract.Observation, error) {
			return modelObservation(replyEvidence(t, "hi")), nil
		}

		c, sess := f.started()
		if err := f.pass(c, sess); err != nil {
			t.Fatalf("claim tick: %v", err)
		}
		if got := f.turnState(turn.id2()); got != "claimed" {
			t.Fatalf("turn state after claim = %q, want claimed", got)
		}
		// The context bytes are staged and published (a real physical
		// action, journaled write-ahead) before the commit call the
		// controller is about to make is ever attempted. Crashing this
		// call simulates dying right after publication, before the commit
		// is durable.
		f.arm("_execution.context.commit", injection{fail: retryable, crash: true})
		if err := f.pass(c, sess); err != nil {
			t.Fatalf("stage tick: %v", err)
		}
		staged := blobs.count()
		if staged < 1 {
			t.Fatalf("published context artifacts before restart = %d, want at least 1", staged)
		}
		if got := f.turnState(turn.id2()); got != "context_pending" {
			t.Fatalf("turn state before restart = %q, want context_pending (context.prepare committed, commit did not)", got)
		}
		if got := provider.calls(); got != 0 {
			t.Fatalf("provider calls before restart = %d, want 0 (context was never committed, so no effect was ever prepared)", got)
		}

		// A context plan is immutably bound to the generation that built it
		// (turn_ops.go, handleContextCommit's own generation check); once a
		// restart fences this turn back to waiting and it resumes, execution
		// discards that plan and rebuilds a fresh one under the new
		// generation rather than resurrecting the old one (this card's own
		// "a stale generation's claim is invalid, never resume it as if
		// nothing happened") -- so the published-but-never-committed staged
		// bytes are legitimately superseded, never referenced by any turn.
		// What must never duplicate is the MODEL call itself: the model_step
		// effect this test cares about is never even prepared until the
		// commit that follows a fresh stage succeeds, so at most one ever
		// reaches the adapter.
		f.restart()
		second, next := f.started()
		t.Cleanup(func() { _ = second.Stop(context.Background()) })
		for i := 0; i < 8; i++ {
			if err := f.pass(second, next); err != nil {
				t.Fatalf("tick after restart: %v", err)
			}
		}
		if got := f.turnState(turn.id2()); got != "completed" {
			t.Fatalf("turn state after recovery = %q, want completed", got)
		}
		if got := provider.calls(); got != 1 {
			t.Fatalf("provider calls after recovery = %d, want exactly 1 (the model is dispatched exactly once, from the one context that was actually committed)", got)
		}
	})

	t.Run("after the provider response was received", func(t *testing.T) {
		f := newFx(t)
		f.blobs = newFakeBlobs()
		worker := contract.NewID()
		turn := f.turn(worker, "task", contract.NewID(), "pending")
		_ = f.turnAttempt(turn.id2())
		provider := f.adapter(modelAdapterName)
		provider.reply = func(context.Context, contract.Dispatch) (contract.Observation, error) {
			f.crash()
			return modelObservation(replyEvidence(t, "hi")), nil
		}

		c, sess := f.started()
		for i := 0; i < 6; i++ {
			if err := f.pass(c, sess); err != nil {
				t.Fatalf("tick before crash: %v", err)
			}
			if provider.calls() > 0 {
				break
			}
		}
		if got := provider.calls(); got != 1 {
			t.Fatalf("provider calls before restart = %d, want exactly 1", got)
		}

		provider.reply = nil
		f.restart()
		second, next := f.started()
		t.Cleanup(func() { _ = second.Stop(context.Background()) })
		for i := 0; i < 8; i++ {
			if err := f.pass(second, next); err != nil {
				t.Fatalf("tick after restart: %v", err)
			}
		}
		// The claimed effect died with the process before an observation
		// was durable: recovery retains outcome_unknown (Z06's own
		// established behavior for every effect kind) rather than
		// resending. The restart also fences this turn (still generation-
		// stamped by the dead controller) back to waiting and it resumes to
		// claimed exactly as controller_ops.go's real handleFence documents
		// for a model_pending turn -- but this package's own outstanding-
		// effect guard (driveWorkItems) refuses to rebuild a second context
		// and dispatch a second model_step while that first one's outcome
		// is still unresolved, reported as a model_dispatch obligation
		// instead. The turn is never advanced by an unconfirmed response,
		// and the adapter is never invoked a second time for it.
		if got := provider.calls(); got != 1 {
			t.Fatalf("provider calls after recovery = %d, want still exactly 1 (never resent)", got)
		}
		if got := f.turnState(turn.id2()); got != "claimed" {
			t.Fatalf("turn state after recovery = %q, want claimed (fenced and resumed, but never rebuilt while its first model_step is unresolved)", got)
		}
		if got := second.Status().Ambiguous; got != 1 {
			t.Fatalf("ambiguous count = %d, want 1 (the claimed-but-unconfirmed attempt)", got)
		}
	})
}

// TestFairScanSkipsBlockedTurnAndPauseClosesAdmissionForOneWorkerOnly is
// P22's third required behavioral test: blocked work (one turn stuck
// waiting) does not starve an eligible later item in the same batch -- the
// fair scan keeps making progress -- and a pause on one worker closes
// admission for it promptly without affecting others.
func TestFairScanSkipsBlockedTurnAndPauseClosesAdmissionForOneWorkerOnly(t *testing.T) {
	f := newFx(t)
	f.blobs = newFakeBlobs()
	provider := f.adapter(modelAdapterName)
	provider.reply = func(context.Context, contract.Dispatch) (contract.Observation, error) {
		return modelObservation(replyEvidence(t, "hi")), nil
	}

	workerBlocked := contract.NewID()
	workerPaused := contract.NewID()
	workerEligible := contract.NewID()

	// Blocked: already committed context and is model_pending -- this
	// card's own confirmed, out-of-authority contract gap (turns.go header:
	// _effects.prepare's caller allowlist does not include "controller")
	// means it can never be dispatched. stalledModelDispatch reports it as
	// an obligation, permanently, every tick, for this test -- so it never
	// advances and never starves the batch it shares with an eligible item.
	blocked := f.turn(workerBlocked, "task", contract.NewID(), "model_pending")
	_ = f.turnAttempt(blocked.id2())

	// Paused: admission-eligible but its worker is paused before the first
	// tick. It must never be claimed while paused.
	paused := f.turn(workerPaused, "task", contract.NewID(), "pending")
	_ = f.turnAttempt(paused.id2())
	f.pauseWorker(workerPaused)

	// Eligible: ordinary pending work in the very same batch as the
	// blocked and paused items above.
	eligible := f.turn(workerEligible, "task", contract.NewID(), "pending")
	_ = f.turnAttempt(eligible.id2())

	c, sess := f.started()
	t.Cleanup(func() { _ = c.Stop(context.Background()) })
	for i := 0; i < 10; i++ {
		if err := f.pass(c, sess); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}

	if got := f.turnState(eligible.id2()); got != "completed" {
		t.Fatalf("eligible turn state = %q, want completed -- the blocked and paused items in the same batch must not starve it", got)
	}
	if got := f.turnState(blocked.id2()); got != "model_pending" {
		t.Fatalf("blocked turn state = %q, want still model_pending (never advances; the model_step dispatch gap blocks it permanently)", got)
	}
	if got := f.turnState(paused.id2()); got != "pending" {
		t.Fatalf("paused worker's turn state = %q, want still pending -- pause must close admission for it promptly", got)
	}
	if got := provider.calls(); got != 1 {
		t.Fatalf("provider calls = %d, want exactly 1 (only the eligible turn ever reaches model dispatch)", got)
	}
}
