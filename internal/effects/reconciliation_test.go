package effects

import (
	"encoding/json"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// P12 behavioral proofs: durable callback ownership (P00-006) and effect
// reconciliation reaching Adapter.Reconcile through a separately admitted,
// separately authorized and accounted bounded read distinct from the
// original write (R10-008, P00-007), plus fair pending scans so unresolved
// operations cannot permanently hide new dispatchable work.

// reconciliationAttemptOf returns the id of the one reconciliation-kind
// attempt of an operation whose attempts also include the named original
// dispatch attempt.
func (e *testEnv) reconciliationAttemptOf(operationID, dispatchAttemptID contract.ID) contract.ID {
	e.t.Helper()
	for _, a := range e.attemptsOf(operationID) {
		if a.ID != dispatchAttemptID && a.Kind == attemptKindReconciliation {
			return a.ID
		}
	}
	e.t.Fatalf("operation %s has no reconciliation attempt distinct from dispatch attempt %s", operationID, dispatchAttemptID)
	return ""
}

// TestReconcileLostWriteProducesOnlyOneWriteAndAuthorizedReadCalls proves the
// central P12 invariant: a lost write response followed by reconciliation
// never re-dispatches the mutation. Exactly one dispatch-kind (write)
// attempt ever exists for the operation, before and after reconciliation;
// the reconciliation path only ever adds separately admitted,
// separately authorized bounded-read attempts, and a second write is
// refused outright by the ordinary admit state machine.
func TestReconcileLostWriteProducesOnlyOneWriteAndAuthorizedReadCalls(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	o = env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispUnknown))
	if o.State != opStateOutcomeUnknown {
		t.Fatalf("lost write response state %q, want %q", o.State, opStateOutcomeUnknown)
	}

	job := env.reconcileOp(env.scope, o.ID, o.Version)
	if job.Operation != opReconcile {
		t.Fatalf("reconcile job operation %q, want %q", job.Operation, opReconcile)
	}

	attempts := env.attemptsOf(o.ID)
	if len(attempts) != 2 {
		t.Fatalf("attempts after reconcile %d, want 2 (the original write plus one bounded read)", len(attempts))
	}
	dispatchCount := 0
	var reconAttempt *attemptRow
	for _, a := range attempts {
		switch a.Kind {
		case attemptKindDispatch:
			dispatchCount++
		case attemptKindReconciliation:
			reconAttempt = a
		}
	}
	if dispatchCount != 1 {
		t.Fatalf("dispatch-kind (write) attempts %d, want exactly 1: reconciliation must never produce a second write", dispatchCount)
	}
	if reconAttempt == nil {
		t.Fatal("reconcile did not create a reconciliation attempt")
	}
	if reconAttempt.TargetAttemptID != d.AttemptID {
		t.Fatalf("reconciliation attempt targets %s, want the exact original attempt %s", reconAttempt.TargetAttemptID, d.AttemptID)
	}

	// The bounded read claims through the ordinary one-use claim -- a
	// separately admitted, separately authorized dispatch -- but must never
	// force the operation into executing: that state describes the
	// original write, not this read.
	rd := env.claimOp(o.ID, reconAttempt.ID, env.generation())
	if len(rd.Action) == 0 {
		t.Fatal("reconciliation dispatch carries no action")
	}
	afterClaim := env.getOp(env.scope, o.ID)
	if afterClaim.State != opStateOutcomeUnknown {
		t.Fatalf("operation state after reconciliation claim %q, want %q unchanged", afterClaim.State, opStateOutcomeUnknown)
	}

	// A second write is refused outright: admit requires prepared or
	// awaiting_review, and the operation is outcome_unknown.
	_ = env.expectFault(opAdmit, admitInput{OperationID: o.ID, ExpectedVersion: afterClaim.Version}, contract.CodeConflict)

	// The authoritative read settles the original write's reservation and
	// the read's own; neither is a second write.
	env.ports.resetCalls()
	final := env.reconciliationRecordOp(o.ID, reconAttempt.ID, rd.Generation, env.observation(dispSucceeded))
	if final.State != opStateSucceeded {
		t.Fatalf("reconciled operation state %q, want %q", final.State, opStateSucceeded)
	}
	settles := env.settleCalls()
	if len(settles) != 2 {
		t.Fatalf("reconciliation settle calls %d, want 2 (the read's own reservation and the original write's)", len(settles))
	}
	attempts = env.attemptsOf(o.ID)
	dispatchCount = 0
	for _, a := range attempts {
		if a.Kind == attemptKindDispatch {
			dispatchCount++
		}
	}
	if dispatchCount != 1 {
		t.Fatalf("dispatch-kind attempts after reconciliation %d, want still exactly 1", dispatchCount)
	}
}

// TestReconciliationRecordReachesRealTerminalDisposition proves a
// reconciliation job can actually resolve an uncertain operation to a real
// terminal disposition -- not the previously stalled job that nothing ever
// consumed.
func TestReconciliationRecordReachesRealTerminalDisposition(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	o = env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispUnknown))

	o = env.reconciliationPrepareOp(o.ID, o.Version)
	reconAttempt := env.reconciliationAttemptOf(o.ID, d.AttemptID)
	rd := env.claimOp(o.ID, reconAttempt, env.generation())

	resolved := env.reconciliationRecordOp(o.ID, reconAttempt, rd.Generation, env.observation(dispSucceeded))
	if resolved.State != opStateSucceeded {
		t.Fatalf("reconciled operation state %q, want the real terminal state %q", resolved.State, opStateSucceeded)
	}
	if len(env.openObligations(o.ID)) != 0 {
		t.Fatal("a terminal reconciliation left obligations open")
	}
}

// TestReconciliationUnknownDispositionLeavesUncertaintyUntouched proves
// non-authoritative eventual-consistency evidence from a reconciliation read
// never establishes nonexecution (Z08.delayed_confirmation): the original
// (target) write's reservation and the operation's outcome_unknown state and
// reconcile obligation are untouched. Only the read's own nominal bounded-
// read reservation settles, because that physical call did execute.
func TestReconciliationUnknownDispositionLeavesUncertaintyUntouched(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	o = env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispUnknown))
	o = env.reconciliationPrepareOp(o.ID, o.Version)
	reconAttempt := env.reconciliationAttemptOf(o.ID, d.AttemptID)
	rd := env.claimOp(o.ID, reconAttempt, env.generation())

	still := env.reconciliationRecordOp(o.ID, reconAttempt, rd.Generation, env.observation(dispUnknown))
	if still.State != opStateOutcomeUnknown {
		t.Fatalf("non-authoritative reconciliation state %q, want %q", still.State, opStateOutcomeUnknown)
	}
	settles := env.settleCalls()
	if len(settles) != 1 {
		t.Fatalf("non-authoritative reconciliation settle calls %d, want exactly 1 (the read's own reservation only)", len(settles))
	}
	var targetReservation contract.ID
	for _, a := range env.attemptsOf(o.ID) {
		if a.ID == d.AttemptID {
			targetReservation = a.ReservationID
		}
	}
	if settles[0].ReservationID == targetReservation {
		t.Fatal("non-authoritative reconciliation settled the original write's reservation")
	}
	if len(env.openObligations(o.ID)) == 0 {
		t.Fatal("non-authoritative reconciliation closed the reconcile obligation")
	}
}

// TestReconciliationDuplicateCallbackDoesNotSettleTwice proves a
// reconciliation job that reaches a terminal disposition settles exactly
// once even when its callback is delivered twice.
func TestReconciliationDuplicateCallbackDoesNotSettleTwice(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	o = env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispUnknown))
	o = env.reconciliationPrepareOp(o.ID, o.Version)
	reconAttempt := env.reconciliationAttemptOf(o.ID, d.AttemptID)
	rd := env.claimOp(o.ID, reconAttempt, env.generation())

	env.reconciliationRecordOp(o.ID, reconAttempt, rd.Generation, env.observation(dispSucceeded))

	env.ports.resetCalls()
	again := env.reconciliationRecordOp(o.ID, reconAttempt, rd.Generation, env.observation(dispSucceeded))
	if again.State != opStateSucceeded {
		t.Fatalf("replayed reconciliation state %q, want %q", again.State, opStateSucceeded)
	}
	if len(env.settleCalls()) != 0 {
		t.Fatalf("duplicate reconciliation callback settled again: %d calls", len(env.settleCalls()))
	}
	count := 0
	for _, ob := range env.observationsOf(o.ID) {
		if ob.AttemptID == reconAttempt {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("reconciliation attempt observations %d, want 1: a duplicate callback must not append twice", count)
	}
}

// TestReconciliationDisputesContradictoryDuplicateCallback proves a repeat
// callback for the same reconciliation attempt that disagrees with its own
// first answer is recorded as a dispute, never a silent second settlement.
func TestReconciliationDisputesContradictoryDuplicateCallback(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	o = env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispUnknown))
	o = env.reconciliationPrepareOp(o.ID, o.Version)
	reconAttempt := env.reconciliationAttemptOf(o.ID, d.AttemptID)
	rd := env.claimOp(o.ID, reconAttempt, env.generation())

	env.reconciliationRecordOp(o.ID, reconAttempt, rd.Generation, env.observation(dispSucceeded))
	env.ports.resetCalls()
	contradicted := env.reconciliationRecordOp(o.ID, reconAttempt, rd.Generation, env.observation(dispFailed))
	if contradicted.State != opStateSucceeded {
		t.Fatalf("contradicted reconciliation state %q, want the settled %q unchanged", contradicted.State, opStateSucceeded)
	}
	if len(env.settleCalls()) != 0 {
		t.Fatal("a contradictory duplicate callback re-settled the reservation")
	}
	open := env.openObligations(o.ID)
	if len(open) != 1 || open[0].Kind != oblDispute {
		t.Fatalf("open obligations %+v, want one dispute", open)
	}
}

// TestRecordRejectsReconciliationAttempt proves _effects.record refuses a
// reconciliation-kind attempt outright: only _effects.reconciliation.record
// may record its observation, keeping the two dispatch paths separate.
func TestRecordRejectsReconciliationAttempt(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	o = env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispUnknown))
	o = env.reconciliationPrepareOp(o.ID, o.Version)
	reconAttempt := env.reconciliationAttemptOf(o.ID, d.AttemptID)
	rd := env.claimOp(o.ID, reconAttempt, env.generation())

	_ = env.expectFault(opRecord, recordInput{
		OperationID: o.ID, AttemptID: reconAttempt, Generation: rd.Generation,
		Observation: env.observation(dispSucceeded),
	}, contract.CodeInvalidInput)
}

// TestReconciliationPrepareRequiresUncertainOperation proves the bounded
// read admission is refused for an operation that is not uncertain or
// awaiting confirmation, matching operation.reconcile's own gate.
func TestReconciliationPrepareRequiresUncertainOperation(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	o = env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispSucceeded))
	_ = env.expectFault(opReconciliationPrepare, admitInput{OperationID: o.ID, ExpectedVersion: o.Version}, contract.CodeConflict)
}

// TestPendingFairnessManyBlockedOperationsDoNotStarveReadyOperation proves
// the fair pending scan: more than 100 unresolved (outcome_unknown)
// operations, all older than a later ready one, can never hide it beyond
// the batch limit.
func TestPendingFairnessManyBlockedOperationsDoNotStarveReadyOperation(t *testing.T) {
	env := newEnv(t)
	const blocked = 105
	for i := 0; i < blocked; i++ {
		o, d := env.dispatched()
		env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispUnknown))
	}
	ready := env.staged()
	ready = env.admitOp(ready.ID, ready.Version)

	pending := env.pendingOp(100)
	if len(pending) != 100 {
		t.Fatalf("pending page length %d, want the full batch limit 100", len(pending))
	}
	found := false
	for _, o := range pending {
		if o.ID == ready.ID {
			found = true
			if o.State != opStateReady {
				t.Fatalf("ready operation listed with state %q, want %q", o.State, opStateReady)
			}
		}
	}
	if !found {
		t.Fatalf("ready operation %s did not appear among the %d pending operations behind %d blocked ones",
			ready.ID, len(pending), blocked)
	}
}

// TestPrepareCallbackRoutePersistedAndReturnedAtClaim proves _effects.
// prepare's optional callback_route is persisted alongside the immutable
// action and returned unmodified at claim as Dispatch.callback_route
// (P00-006), and never leaks into the adapter-visible action bytes.
func TestPrepareCallbackRoutePersistedAndReturnedAtClaim(t *testing.T) {
	env := newEnv(t)
	source := env.ids.New()
	route := &wireCallbackRoute{Kind: callbackKindWorkerTurn, TurnID: &source}
	payload, err := env.prepareOpWithRoute(env.scope, env.action(), source, route)
	if err != nil || payload.Error != nil {
		t.Fatalf("prepare with callback_route failed: err=%v payload=%+v", err, payload)
	}
	var out operationResourceBody
	env.decode(payload.Data, &out)
	o := out.Resource
	if o.CallbackRoute == nil || o.CallbackRoute.Kind != callbackKindWorkerTurn ||
		o.CallbackRoute.TurnID == nil || *o.CallbackRoute.TurnID != source {
		t.Fatalf("prepared operation callback_route %+v, want kind worker_turn turn_id %s", o.CallbackRoute, source)
	}

	o = env.admitOp(o.ID, o.Version)
	d := env.claimOp(o.ID, o.AttemptIDs[0], env.generation())
	if d.CallbackRoute == nil || d.CallbackRoute.Kind != callbackKindWorkerTurn ||
		d.CallbackRoute.TurnID == nil || *d.CallbackRoute.TurnID != source {
		t.Fatalf("claimed dispatch callback_route %+v, want the same route returned unmodified", d.CallbackRoute)
	}
	var action map[string]any
	if err := json.Unmarshal(d.Action, &action); err != nil {
		t.Fatalf("decode dispatch action: %v", err)
	}
	if _, ok := action["callback_route"]; ok {
		t.Fatal("dispatch action leaked callback_route into adapter-visible bytes")
	}
	if _, ok := action["attempt_id"]; ok {
		t.Fatal("dispatch action leaked attempt_id into adapter-visible bytes")
	}
}

// TestPrepareCallbackRouteRejectsMismatchedSource proves arbitrary JSON
// cannot select an execution callback: a route naming a turn_id other than
// the effect's own source_id is refused (P00-006).
func TestPrepareCallbackRouteRejectsMismatchedSource(t *testing.T) {
	env := newEnv(t)
	source := env.ids.New()
	foreign := env.ids.New()
	route := &wireCallbackRoute{Kind: callbackKindWorkerTurn, TurnID: &foreign}
	_, err := env.prepareOpWithRoute(env.scope, env.action(), source, route)
	f := faultFrom(err)
	if f == nil {
		t.Fatal("expected a fault for a callback_route naming a foreign turn_id")
	}
	if f.Code != contract.CodePermissionDenied {
		t.Fatalf("fault code %s, want %s", f.Code, contract.CodePermissionDenied)
	}
}

// TestPrepareCallbackRouteRejectsCrossKindIDs proves a route cannot mix an
// id field with a kind it does not belong to.
func TestPrepareCallbackRouteRejectsCrossKindIDs(t *testing.T) {
	env := newEnv(t)
	source := env.ids.New()
	route := &wireCallbackRoute{Kind: callbackKindMemory, TurnID: &source}
	_, err := env.prepareOpWithRoute(env.scope, env.action(), source, route)
	f := faultFrom(err)
	if f == nil || f.Code != contract.CodeInvalidInput {
		t.Fatalf("expected invalid_input for a memory route carrying turn_id, got %v", f)
	}
}

// TestRecordSuccessorGenerationNeverClaimedRecordsNotSent proves
// _effects.record's optional current_generation resolves a stray attempt
// whose one-use claim was never actually consumed as not_sent, settling its
// reservation as authoritative non-execution.
func TestRecordSuccessorGenerationNeverClaimedRecordsNotSent(t *testing.T) {
	env := newEnv(t)
	o, attempt := env.admitted()
	attemptGen := env.attemptsOf(o.ID)[0].Generation
	newGen := env.newGeneration()
	payload := env.mustOK(opRecord, recordInput{
		OperationID: o.ID, AttemptID: attempt, Generation: attemptGen,
		Observation:       wireObservation{Disposition: dispNotSent, Evidence: json.RawMessage(`{}`), Usage: wireUsage{Currency: "USD"}},
		CurrentGeneration: &newGen,
	})
	var out operationResourceBody
	env.decode(payload.Data, &out)
	if out.Resource.State != opStateFailed {
		t.Fatalf("never-claimed stray attempt operation state %q, want %q", out.Resource.State, opStateFailed)
	}
	settles := env.settleCalls()
	if len(settles) != 1 || !settles[0].Nonexecution {
		t.Fatalf("settlements %+v, want one authoritative non-execution", settles)
	}
}

// TestRecordSuccessorGenerationClaimedRecordsOutcomeUnknown proves a stray
// attempt that was claimed but never confirmed before a generation change
// records outcome_unknown, never succeeded or failed, and keeps its
// reservation.
func TestRecordSuccessorGenerationClaimedRecordsOutcomeUnknown(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	newGen := env.newGeneration()
	payload := env.mustOK(opRecord, recordInput{
		OperationID: o.ID, AttemptID: d.AttemptID, Generation: d.Generation,
		Observation:       env.observation(dispSucceeded), // asserted disposition must be ignored
		CurrentGeneration: &newGen,
	})
	var out operationResourceBody
	env.decode(payload.Data, &out)
	if out.Resource.State != opStateOutcomeUnknown {
		t.Fatalf("claimed-but-unconfirmed stray attempt state %q, want %q regardless of the caller's asserted disposition",
			out.Resource.State, opStateOutcomeUnknown)
	}
	if len(env.settleCalls()) != 0 {
		t.Fatal("claimed-but-unconfirmed stray attempt settled its reservation; uncertainty must be retained")
	}
	if len(env.openObligations(o.ID)) != 1 {
		t.Fatal("claimed-but-unconfirmed stray attempt opened no reconcile obligation")
	}
}

// TestRecordSuccessorGenerationIsIdempotent proves a stray attempt already
// resolved by an earlier successor-generation call is never resolved twice.
func TestRecordSuccessorGenerationIsIdempotent(t *testing.T) {
	env := newEnv(t)
	o, attempt := env.admitted()
	attemptGen := env.attemptsOf(o.ID)[0].Generation
	newGen := env.newGeneration()
	obs := wireObservation{Disposition: dispNotSent, Evidence: json.RawMessage(`{}`), Usage: wireUsage{Currency: "USD"}}
	env.mustOK(opRecord, recordInput{
		OperationID: o.ID, AttemptID: attempt, Generation: attemptGen,
		Observation: obs, CurrentGeneration: &newGen,
	})
	env.ports.resetCalls()
	payload := env.mustOK(opRecord, recordInput{
		OperationID: o.ID, AttemptID: attempt, Generation: attemptGen,
		Observation: obs, CurrentGeneration: &newGen,
	})
	var out operationResourceBody
	env.decode(payload.Data, &out)
	if out.Resource.State != opStateFailed {
		t.Fatalf("replayed successor-generation resolution state %q, want %q", out.Resource.State, opStateFailed)
	}
	if len(env.settleCalls()) != 0 {
		t.Fatal("replayed successor-generation resolution settled a second time")
	}
}

// TestRecordSuccessorGenerationRejectsGenerationNotAdvanced proves
// current_generation must actually be later than the attempt's own
// recorded generation; it is not a way to bypass the ordinary generation
// fence.
func TestRecordSuccessorGenerationRejectsGenerationNotAdvanced(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	same := d.Generation
	_ = env.expectFault(opRecord, recordInput{
		OperationID: o.ID, AttemptID: d.AttemptID, Generation: d.Generation,
		Observation:       env.observation(dispUnknown),
		CurrentGeneration: &same,
	}, contract.CodeInvalidInput)
	_ = o
}

// TestClaimReconciliationAttemptNeverAdvancesOperationToExecuting proves
// claiming a reconciliation attempt's one-use dispatch never forces the
// operation into executing: that state describes the original write's
// dispatch, not a separately admitted bounded read.
func TestClaimReconciliationAttemptNeverAdvancesOperationToExecuting(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	o = env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispUnknown))
	o = env.reconciliationPrepareOp(o.ID, o.Version)
	reconAttempt := env.reconciliationAttemptOf(o.ID, d.AttemptID)
	env.claimOp(o.ID, reconAttempt, env.generation())
	after := env.getOp(env.scope, o.ID)
	if after.State != opStateOutcomeUnknown {
		t.Fatalf("operation state after claiming a reconciliation attempt %q, want %q unchanged", after.State, opStateOutcomeUnknown)
	}
}
