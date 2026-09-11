package effects

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Lifecycle tests drive the controller flow — stage, admit, claim, record,
// reconcile — against the real storage-backed state machine with fake peers,
// asserting exact states, peer-call order, settlements, obligations and
// events.

func TestPrepareStagesOperation(t *testing.T) {
	env := newEnv(t)
	o := env.staged()
	if o.State != opStatePrepared {
		t.Fatalf("prepared operation state %q, want %q", o.State, opStatePrepared)
	}
	if o.ActionDigest == "" {
		t.Fatal("prepared operation carries an empty action digest")
	}
	if len(o.AttemptIDs) != 0 {
		t.Fatalf("prepared operation carries %d attempts, want 0", len(o.AttemptIDs))
	}
}

func TestPrepareReplaysSameSource(t *testing.T) {
	env := newEnv(t)
	action := env.action()
	source := env.ids.New()
	first := env.prepareOp(env.scope, action, source)
	second := env.prepareOp(env.scope, action, source)
	if first.ID != second.ID {
		t.Fatalf("same source replay produced %s then %s", first.ID, second.ID)
	}
	if second.State != opStatePrepared {
		t.Fatalf("replayed operation state %q, want %q", second.State, opStatePrepared)
	}
}

func TestPrepareRejectsReboundSource(t *testing.T) {
	env := newEnv(t)
	action := env.action()
	source := env.ids.New()
	env.prepareOp(env.scope, action, source)
	other := action
	other.Destination = "https://api.example.com/v1/other"
	_ = env.expectFault(opPrepare, prepareInput{
		Scope: unitScope(env.scope), Action: other, SourceID: source,
	}, contract.CodeSubmissionConflict)
}

func TestProposeStagesWithoutSourceKey(t *testing.T) {
	env := newEnv(t)
	o := env.proposeOp(env.scope, env.action())
	if o.State != opStatePrepared {
		t.Fatalf("proposed operation state %q, want %q", o.State, opStatePrepared)
	}
}

func TestAdmitCallsPeersInOrder(t *testing.T) {
	env := newEnv(t)
	o := env.staged()
	// Staging consults policy on its own; bracket admit's own peer calls.
	env.ports.resetCalls()
	env.admitOp(o.ID, o.Version)
	ops := env.ports.opsCalled()
	want := []string{
		opIdentityAuthority, opPolicyCheck, opConfigSnapshot,
		opAccountingInspect, opConnectionsResolve, opAccountingReserve,
	}
	if len(ops) != len(want) {
		t.Fatalf("admit called peers %v, want %v", ops, want)
	}
	for i, op := range want {
		if ops[i] != op {
			t.Fatalf("admit called peers %v, want %v", ops, want)
		}
	}
}

func TestAdmitTransitionsReadyAndReserves(t *testing.T) {
	env := newEnv(t)
	o, attempt := env.admitted()
	if o.State != opStateReady {
		t.Fatalf("admitted operation state %q, want %q", o.State, opStateReady)
	}
	if o.Version != 2 {
		t.Fatalf("admitted operation version %d, want 2", o.Version)
	}
	attempts := env.attemptsOf(o.ID)
	if len(attempts) != 1 || attempts[0].ID != attempt {
		t.Fatalf("admitted operation has attempts %v, want one %s", attempts, attempt)
	}
	if attempts[0].State != attemptStatePrepared {
		t.Fatalf("new attempt state %q, want %q", attempts[0].State, attemptStatePrepared)
	}
	reserves := env.reserveCalls()
	if len(reserves) != 1 {
		t.Fatalf("admit made %d reserve calls, want 1", len(reserves))
	}
	if reserves[0].OperationID != o.ID {
		t.Fatalf("reserve for operation %s, want %s", reserves[0].OperationID, o.ID)
	}
	if reserves[0].Amount.Currency != "USD" || reserves[0].Amount.MicroUnits != 1000 {
		t.Fatalf("reserve amount %+v, want the action cost bound", reserves[0].Amount)
	}
}

func TestAdmitIsVersionFenced(t *testing.T) {
	env := newEnv(t)
	o := env.staged()
	env.admitOp(o.ID, o.Version)
	_ = env.expectFault(opAdmit, admitInput{OperationID: o.ID, ExpectedVersion: o.Version},
		contract.CodeStaleVersion)
}

func TestAdmitRejectsUnknownOperation(t *testing.T) {
	env := newEnv(t)
	_ = env.expectFault(opAdmit, admitInput{OperationID: env.ids.New(), ExpectedVersion: 1},
		contract.CodeNotFound)
}

func TestAdmitDeniesByPolicy(t *testing.T) {
	env := newEnv(t)
	o := env.staged()
	env.ports.policyDecision = policyDeny
	env.ports.policyReasons = []string{"capability disabled"}
	o = env.admitOp(o.ID, o.Version)
	if o.State != opStateDenied {
		t.Fatalf("denied operation state %q, want %q", o.State, opStateDenied)
	}
	if len(env.reserveCalls()) != 0 {
		t.Fatal("denied admission reserved budget")
	}
}

func TestAdmitPrerequisiteMissingByPolicy(t *testing.T) {
	env := newEnv(t)
	o := env.staged()
	env.ports.policyDecision = policyPrerequisiteMissing
	_ = env.expectFault(opAdmit, admitInput{OperationID: o.ID, ExpectedVersion: o.Version},
		contract.CodePrerequisiteMissing)
}

func TestAdmitReviewGate(t *testing.T) {
	env := newEnv(t)
	env.ports.policyDecision = policyReview
	env.ports.policyReasons = []string{"irreversible action"}
	o := env.staged()
	if o.State != opStateAwaitingReview {
		t.Fatalf("review-staged operation state %q, want %q", o.State, opStateAwaitingReview)
	}
	if len(env.ports.callsOf(opReviewsEnsure)) != 1 {
		t.Fatalf("staging made %d review.ensure calls, want 1", len(env.ports.callsOf(opReviewsEnsure)))
	}
	// An unsatisfied review fails closed with review_required; the fault
	// rolls the transaction back, so the operation stays awaiting_review.
	_ = env.expectFault(opAdmit, admitInput{OperationID: o.ID, ExpectedVersion: o.Version},
		contract.CodeReviewRequired)
	refused := env.getOp(env.scope, o.ID)
	if refused.State != opStateAwaitingReview {
		t.Fatalf("review-refused operation state %q, want %q", refused.State, opStateAwaitingReview)
	}

	// A recorded decision that is not an approval denies the operation.
	env.ports.reviewEligible = true
	reject := approvedDecision(o.ActionDigest)
	reject.Decision = "reject"
	env.ports.reviewDecision = reject
	denied := env.staged()
	denied = env.admitOp(denied.ID, denied.Version)
	if denied.State != opStateDenied {
		t.Fatalf("rejected-review operation state %q, want %q", denied.State, opStateDenied)
	}

	// A satisfied approve decision admits a fresh operation.
	env.ports.reviewDecision = approvedDecision(o.ActionDigest)
	approved := env.staged()
	approved = env.admitOp(approved.ID, approved.Version)
	if approved.State != opStateReady {
		t.Fatalf("approved operation state %q, want %q", approved.State, opStateReady)
	}
}

func TestAdmitReviewNonApproveDenies(t *testing.T) {
	env := newEnv(t)
	env.ports.policyDecision = policyReview
	o := env.staged()
	env.ports.reviewEligible = true
	env.ports.reviewDecision = &wireDecision{
		ID: env.ids.New(), ReviewID: env.ids.New(), ReviewVersion: 1,
		ActionDigest: o.ActionDigest, ReviewerID: env.ids.New(),
		Decision: "reject", At: env.clock.Now(),
	}
	o = env.admitOp(o.ID, o.Version)
	if o.State != opStateDenied {
		t.Fatalf("rejected operation state %q, want %q", o.State, opStateDenied)
	}
}

func TestAdmitRejectsRevokedAuthority(t *testing.T) {
	env := newEnv(t)
	env.ports.principalRevoked = true
	o := env.staged()
	_ = env.expectFault(opAdmit, admitInput{OperationID: o.ID, ExpectedVersion: o.Version},
		contract.CodePermissionDenied)
}

func TestAdmitRejectsRestrictedAuthority(t *testing.T) {
	env := newEnv(t)
	env.ports.restrictions = []string{"no_external_mutation"}
	o := env.staged()
	_ = env.expectFault(opAdmit, admitInput{OperationID: o.ID, ExpectedVersion: o.Version},
		contract.CodePermissionDenied)
}

func TestAdmitRejectsStaleConfigurationRevision(t *testing.T) {
	env := newEnv(t)
	o := env.staged()
	env.ports.configRevision = 8
	_ = env.expectFault(opAdmit, admitInput{OperationID: o.ID, ExpectedVersion: o.Version},
		contract.CodeStaleVersion)
}

func TestAdmitExpiresStaleAction(t *testing.T) {
	env := newEnv(t)
	o := env.staged()
	env.clock.Advance(2 * time.Hour)
	o = env.admitOp(o.ID, o.Version)
	if o.State != opStateExpired {
		t.Fatalf("expired operation state %q, want %q", o.State, opStateExpired)
	}
}

func TestAdmitRejectsNotYetValidAction(t *testing.T) {
	env := newEnv(t)
	action := env.action()
	action.NotBefore = env.clock.Now().Add(time.Hour)
	o := env.prepareOp(env.scope, action, env.ids.New())
	_ = env.expectFault(opAdmit, admitInput{OperationID: o.ID, ExpectedVersion: o.Version},
		contract.CodeConflict)
}

func TestStageRejectsUnsupportedPrecondition(t *testing.T) {
	env := newEnv(t)
	action := env.action()
	action.Preconditions = json.RawMessage(`{"quantum_lock":{"qubit":7}}`)
	_ = env.expectFault(opPrepare, prepareInput{
		Scope: unitScope(env.scope), Action: action, SourceID: env.ids.New(),
	}, contract.CodeCapabilityUnsupported)
}

func TestAdmitRequiresValidConnection(t *testing.T) {
	for _, state := range []string{connStateInvalid, connStateExpired, connStateRevoked} {
		t.Run(state, func(t *testing.T) {
			env := newEnv(t)
			env.ports.connState = state
			o := env.staged()
			_ = env.expectFault(opAdmit, admitInput{OperationID: o.ID, ExpectedVersion: o.Version},
				contract.CodePrerequisiteMissing)
		})
	}
}

func TestAdmitRequiresUnexpiredConnection(t *testing.T) {
	env := newEnv(t)
	past := env.clock.Now().Add(-time.Minute)
	env.ports.connValidUntil = &past
	o := env.staged()
	_ = env.expectFault(opAdmit, admitInput{OperationID: o.ID, ExpectedVersion: o.Version},
		contract.CodePrerequisiteMissing)
}

func TestAdmitRequiresAvailableArtifacts(t *testing.T) {
	// Each case drives its own env: the staged operation must live in the
	// same database the admission runs against.
	staged := func(t *testing.T) (*testEnv, wireArtifactRef, wireOperation) {
		t.Helper()
		env := newEnv(t)
		action := env.action()
		ref := wireArtifactRef{ID: env.ids.New(), Digest: contract.Hash([]byte("artifact bytes"))}
		action.Content = []wireArtifactRef{ref}
		return env, ref, env.prepareOp(env.scope, action, env.ids.New())
	}

	t.Run("missing", func(t *testing.T) {
		env, ref, o := staged(t)
		env.ports.artifactsOmit = map[contract.ID]bool{ref.ID: true}
		_ = env.expectFault(opAdmit, admitInput{OperationID: o.ID, ExpectedVersion: o.Version},
			contract.CodePrerequisiteMissing)
	})
	t.Run("wrong state", func(t *testing.T) {
		env, ref, o := staged(t)
		env.ports.artifactStates = map[contract.ID]string{ref.ID: "uploading"}
		_ = env.expectFault(opAdmit, admitInput{OperationID: o.ID, ExpectedVersion: o.Version},
			contract.CodePrerequisiteMissing)
	})
	t.Run("digest mismatch", func(t *testing.T) {
		env, ref, o := staged(t)
		env.ports.artifactDigests = map[contract.ID]contract.Digest{
			ref.ID: contract.Hash([]byte("other bytes")),
		}
		_ = env.expectFault(opAdmit, admitInput{OperationID: o.ID, ExpectedVersion: o.Version},
			contract.CodePrerequisiteMissing)
	})
}

func TestClaimReturnsDispatch(t *testing.T) {
	env := newEnv(t)
	o, attempt := env.admitted()
	d := env.claimOp(o.ID, attempt, env.generation())
	if d.OperationID != o.ID || d.AttemptID != attempt {
		t.Fatalf("dispatch targets %s/%s, want %s/%s", d.OperationID, d.AttemptID, o.ID, attempt)
	}
	if d.Generation != env.generation() {
		t.Fatalf("dispatch generation %d, want %d", d.Generation, env.generation())
	}
	if d.Adapter != "test-adapter" {
		t.Fatalf("dispatch adapter %q, want test-adapter", d.Adapter)
	}
	if d.CredentialRef == "" {
		t.Fatal("dispatch carries an empty credential ref")
	}
	var action wireAction
	if err := json.Unmarshal(d.Action, &action); err != nil {
		t.Fatalf("decode dispatch action: %v", err)
	}
	if action.Destination != "https://api.example.com/v1/items" {
		t.Fatalf("dispatch action destination %q", action.Destination)
	}
	stored := env.mustFindOperation(o.ID)
	if stored.State != opStateExecuting {
		t.Fatalf("claimed operation state %q, want %q", stored.State, opStateExecuting)
	}
	if got := env.claimOf(attempt); got == nil || !got.Consumed {
		t.Fatalf("claim after claim: %+v, want consumed", got)
	}
	attempts := env.attemptsOf(o.ID)
	if attempts[0].State != attemptStateClaimed {
		t.Fatalf("claimed attempt state %q, want %q", attempts[0].State, attemptStateClaimed)
	}
	if d.Deadline.Before(env.clock.Now()) {
		t.Fatalf("dispatch deadline %s precedes now", d.Deadline)
	}
}

func TestClaimIsSingleUse(t *testing.T) {
	env := newEnv(t)
	o, attempt := env.admitted()
	env.claimOp(o.ID, attempt, env.generation())
	_ = env.expectFault(opClaim, claimInput{
		OperationID: o.ID, AttemptID: attempt, Generation: env.generation(),
	}, contract.CodeConflict)
}

func TestClaimRequiresMatchingGeneration(t *testing.T) {
	env := newEnv(t)
	o, attempt := env.admitted()
	_ = env.expectFault(opClaim, claimInput{OperationID: o.ID, AttemptID: attempt, Generation: 2},
		contract.CodeConflict)
}

func TestClaimRejectsUnknownAttempt(t *testing.T) {
	env := newEnv(t)
	o, _ := env.admitted()
	_ = env.expectFault(opClaim, claimInput{
		OperationID: o.ID, AttemptID: env.ids.New(), Generation: env.generation(),
	}, contract.CodeNotFound)
}

func TestRecordSucceeded(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	o = env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispSucceeded))
	if o.State != opStateSucceeded {
		t.Fatalf("recorded operation state %q, want %q", o.State, opStateSucceeded)
	}
	settles := env.settleCalls()
	if len(settles) != 1 {
		t.Fatalf("record made %d settle calls, want 1", len(settles))
	}
	if settles[0].Nonexecution {
		t.Fatal("successful record settled as non-execution")
	}
	if settles[0].Usage.Spent != 100 {
		t.Fatalf("settled usage %+v, want the observation usage", settles[0].Usage)
	}
	if obs := env.observationsOf(o.ID); len(obs) != 1 || obs[0].Kind != obsKindPhysical {
		t.Fatalf("observations %+v, want one physical row", obs)
	}
}

func TestRecordAcceptedOpensConfirmObligation(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	o = env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispAccepted))
	if o.State != opStateAwaitingConfirmation {
		t.Fatalf("accepted operation state %q, want %q", o.State, opStateAwaitingConfirmation)
	}
	open := env.openObligations(o.ID)
	if len(open) != 1 || open[0].Kind != oblConfirm {
		t.Fatalf("open obligations %+v, want one confirm", open)
	}
}

func TestRecordFailedFails(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	o = env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispFailed))
	if o.State != opStateFailed {
		t.Fatalf("recorded operation state %q, want %q", o.State, opStateFailed)
	}
	if len(env.settleCalls()) != 1 {
		t.Fatal("failed record did not settle the reservation")
	}
}

func TestRecordUnknownRetainsUncertainty(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	o = env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispUnknown))
	if o.State != opStateOutcomeUnknown {
		t.Fatalf("recorded operation state %q, want %q", o.State, opStateOutcomeUnknown)
	}
	if len(env.settleCalls()) != 0 {
		t.Fatal("uncertain record settled the reservation; R10-006 retains it")
	}
	open := env.openObligations(o.ID)
	if len(open) != 1 || open[0].Kind != oblReconcile {
		t.Fatalf("open obligations %+v, want one reconcile", open)
	}
}

func TestRecordNotSentUnclaimedProvesNonexecution(t *testing.T) {
	env := newEnv(t)
	o, attempt := env.admitted()
	o = env.recordOp(o.ID, attempt, env.generation(), env.observation(dispNotSent))
	if o.State != opStateFailed {
		t.Fatalf("unsent operation state %q, want %q", o.State, opStateFailed)
	}
	settles := env.settleCalls()
	if len(settles) != 1 || !settles[0].Nonexecution {
		t.Fatalf("settlements %+v, want one authoritative non-execution", settles)
	}
}

func TestRecordNotSentClaimedKeepsUncertainty(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	o = env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispNotSent))
	if o.State != opStateOutcomeUnknown {
		t.Fatalf("claimed not-sent operation state %q, want %q", o.State, opStateOutcomeUnknown)
	}
	if len(env.settleCalls()) != 0 {
		t.Fatal("claimed not-sent settled the reservation; R10-006 retains it")
	}
	if len(env.openObligations(o.ID)) != 1 {
		t.Fatal("claimed not-sent opened no reconcile obligation")
	}
}

func TestRecordSameDispositionIsIdempotent(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispSucceeded))
	again := env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispSucceeded))
	if again.State != opStateSucceeded {
		t.Fatalf("replayed operation state %q, want %q", again.State, opStateSucceeded)
	}
	if obs := env.observationsOf(o.ID); len(obs) != 1 {
		t.Fatalf("replay appended observations, %d rows", len(obs))
	}
}

func TestRecordContradictionOpensDispute(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispSucceeded))
	contradicted := env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispFailed))
	if contradicted.State != opStateSucceeded {
		t.Fatalf("disputed operation state %q, want settled succeeded", contradicted.State)
	}
	obs := env.observationsOf(o.ID)
	if len(obs) != 2 || obs[1].Kind != obsKindDispute {
		t.Fatalf("observations %d rows, want a physical row and a dispute", len(obs))
	}
	open := env.openObligations(o.ID)
	if len(open) != 1 || open[0].Kind != oblDispute {
		t.Fatalf("open obligations %+v, want one dispute", open)
	}
}

func TestRecordCorrectionResolvesUnknown(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispUnknown))
	o = env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispSucceeded))
	if o.State != opStateSucceeded {
		t.Fatalf("corrected operation state %q, want %q", o.State, opStateSucceeded)
	}
	obs := env.observationsOf(o.ID)
	if len(obs) != 2 || obs[1].Kind != obsKindCorrection {
		t.Fatalf("observations %d rows, want a physical row and a correction", len(obs))
	}
	if len(env.settleCalls()) != 1 {
		t.Fatal("correction did not settle the retained reservation")
	}
	if len(env.openObligations(o.ID)) != 0 {
		t.Fatal("correction left the reconcile obligation open")
	}
	if resolved := env.obligationsOf(o.ID); len(resolved) != 1 || resolved[0].ResolvedAt.IsZero() {
		t.Fatalf("obligations %+v, want one resolved reconcile", resolved)
	}
}

func TestRecordCorrectionFailureResolvesWhenOnlyUnknown(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispUnknown))
	o = env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispFailed))
	if o.State != opStateFailed {
		t.Fatalf("corrected operation state %q, want %q", o.State, opStateFailed)
	}
	if len(env.openObligations(o.ID)) != 0 {
		t.Fatal("corrected failure left the reconcile obligation open")
	}
}

func TestRecordCorrectionFailureKeepsOtherOperationsUnknown(t *testing.T) {
	// Correcting one operation's uncertainty into failure leaves every other
	// still-unknown operation untouched: R10-006 uncertainty resolves only on
	// its own attempt's evidence.
	env := newEnv(t)
	first := env.staged()
	first = env.admitOp(first.ID, first.Version)
	d1 := env.claimOp(first.ID, first.AttemptIDs[0], env.generation())
	env.recordOp(first.ID, d1.AttemptID, d1.Generation, env.observation(dispUnknown))

	second := env.staged()
	second = env.admitOp(second.ID, second.Version)
	d2 := env.claimOp(second.ID, second.AttemptIDs[0], env.generation())
	env.recordOp(second.ID, d2.AttemptID, d2.Generation, env.observation(dispUnknown))

	second = env.recordOp(second.ID, d2.AttemptID, d2.Generation, env.observation(dispFailed))
	if second.State != opStateFailed {
		t.Fatalf("corrected operation state %q, want %q", second.State, opStateFailed)
	}
	firstRow := env.mustFindOperation(first.ID)
	if firstRow.State != opStateOutcomeUnknown {
		t.Fatalf("older unknown operation state %q, want %q", firstRow.State, opStateOutcomeUnknown)
	}
}

func TestRecordForwardsConnectionInvalidation(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	obs := env.observation(dispSucceeded)
	obs.Evidence = json.RawMessage(`{"reason":"provider-reported","connection_id":"` +
		string(o.Action.Connection.ID) + `","connection_version":1,"validation_state":"revoked"}`)
	env.recordOp(o.ID, d.AttemptID, d.Generation, obs)
	calls := env.validationRecordCalls()
	if len(calls) != 1 {
		t.Fatalf("record made %d validation.record calls, want 1", len(calls))
	}
	if calls[0].ConnectionID != o.Action.Connection.ID {
		t.Fatalf("validation record for connection %s, want %s",
			calls[0].ConnectionID, o.Action.Connection.ID)
	}
}

func TestRecordWithoutNormalizedEvidenceSkipsForwarding(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispSucceeded))
	if len(env.validationRecordCalls()) != 0 {
		t.Fatal("plain evidence was forwarded to the connections owner")
	}
}

func TestRecordRejectsUnknownDisposition(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	_ = env.expectFault(opRecord, recordInput{
		OperationID: o.ID, AttemptID: d.AttemptID, Generation: d.Generation,
		Observation: env.observation("terminated"),
	}, contract.CodeInvalidInput)
}

func TestRecordRejectsGenerationMismatch(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	_ = env.expectFault(opRecord, recordInput{
		OperationID: o.ID, AttemptID: d.AttemptID, Generation: 2,
		Observation: env.observation(dispSucceeded),
	}, contract.CodeConflict)
}

func TestRecordRejectsUnknownAttempt(t *testing.T) {
	env := newEnv(t)
	o, _ := env.admitted()
	_ = env.expectFault(opRecord, recordInput{
		OperationID: o.ID, AttemptID: env.ids.New(), Generation: env.generation(),
		Observation: env.observation(dispSucceeded),
	}, contract.CodeNotFound)
}

func TestReconcileCreatesJobAndObligation(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	o = env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispUnknown))
	job := env.reconcileOp(env.scope, o.ID, o.Version)
	if job.Operation != opReconcile {
		t.Fatalf("reconcile job operation %q, want %q", job.Operation, opReconcile)
	}
	calls := env.jobCreateCalls()
	if len(calls) != 1 {
		t.Fatalf("reconcile made %d job.create calls, want 1", len(calls))
	}
	if calls[0].Owner != ownerName || calls[0].Operation != opReconcile {
		t.Fatalf("job create %+v, want owner %s operation %s", calls[0], ownerName, opReconcile)
	}
	var body map[string]any
	if err := json.Unmarshal(calls[0].Input, &body); err != nil {
		t.Fatalf("decode job input: %v", err)
	}
	if body["action_digest"] != o.ActionDigest {
		t.Fatalf("job input action digest %v, want %s", body["action_digest"], o.ActionDigest)
	}
	// Reconciling an outcome_unknown operation runs alongside the jobless
	// reconcile obligation record-unknown already opened; the job-bound
	// obligation must be open too.
	var jobObligation bool
	for _, ob := range env.openObligations(o.ID) {
		if ob.Kind == oblReconcile && strings.Contains(ob.DetailJSON, string(job.ID)) {
			jobObligation = true
		}
	}
	if !jobObligation {
		t.Fatalf("open obligations %v, want one bound to job %s",
			env.openObligations(o.ID), job.ID)
	}
}

func TestReconcileRejectsConcludedOperation(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	o = env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispSucceeded))
	_ = env.expectFault(opReconcile, reconcileInput{
		Scope: unitScope(env.scope), ID: o.ID, ExpectedVersion: o.Version,
	}, contract.CodeConflict)
}

func TestGetReturnsOperationForItsScope(t *testing.T) {
	env := newEnv(t)
	o := env.staged()
	got := env.getOp(env.scope, o.ID)
	if got.ID != o.ID {
		t.Fatalf("get returned %s, want %s", got.ID, o.ID)
	}
	foreign := env.scope
	foreign.InstallationID = env.ids.New()
	_ = env.expectFaultAs(env.actor, foreign, opGet,
		getOperationInput{Scope: unitScope(foreign), ID: o.ID}, contract.CodeNotFound)
}

func TestGetRejectsUnknownOperation(t *testing.T) {
	env := newEnv(t)
	_ = env.expectFault(opGet, getOperationInput{Scope: unitScope(env.scope), ID: env.ids.New()},
		contract.CodeNotFound)
}

func TestFullFlowEmitsLifecycleEvents(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispSucceeded))
	var kinds []string
	for _, ev := range env.events() {
		if strings.HasPrefix(ev.Kind, "effects.") {
			kinds = append(kinds, ev.Kind)
		}
	}
	want := []string{
		eventOperationPrepared, eventOperationReady, eventOperationExecuting,
		eventAttemptClaimed, eventAttemptRecorded, eventOperationSucceeded,
	}
	if len(kinds) != len(want) {
		t.Fatalf("events %v, want %v", kinds, want)
	}
	for i, kind := range want {
		if kinds[i] != kind {
			t.Fatalf("event %d is %s, want %s (all: %v)", i, kinds[i], kind, kinds)
		}
	}
}

func TestLinkedProposeRequiresConcludedOriginal(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	o = env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispSucceeded))
	compensation := env.linkedProposeOp(opCompensationPropose, env.scope, o.ID, o.Version, env.action())
	if compensation.Relationship == nil || *compensation.Relationship != "compensation" {
		t.Fatalf("compensation relationship %v, want compensation", compensation.Relationship)
	}
	if compensation.LinkedOperationID == nil || *compensation.LinkedOperationID != o.ID {
		t.Fatalf("compensation link %v, want %s", compensation.LinkedOperationID, o.ID)
	}
}

func TestLinkedProposeRejectsUnconcludedOriginal(t *testing.T) {
	env := newEnv(t)
	o := env.staged()
	_ = env.expectFault(opCompensationPropose, linkedProposeInput{
		Scope: unitScope(env.scope), ID: o.ID, ExpectedVersion: o.Version, Action: env.action(),
	}, contract.CodeConflict)
}
