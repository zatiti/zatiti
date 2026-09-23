package effects

import (
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Tests for _effects.restore.merge, driven through the real registered
// handler over real storage, against operations created by this package's
// own real propose/admit/claim/record path. Nothing about the merge is
// stubbed.

// restoreObligation builds the RecoveryObligation installation's own
// snapshotObligations produces for one pending effect.
func (e *testEnv) restoreObligation(kind string, id contract.ID, version int64, state string, recordedAt time.Time) recoveryObligation {
	e.t.Helper()
	return recoveryObligation{
		ID: e.ids.New(), Owner: ownerName, Kind: kind, ResourceID: id, ResourceVersion: version,
		RecordArtifact: wireArtifactRef{
			ID:     e.ids.New(),
			Digest: contract.Digest("0000000000000000000000000000000000000000000000000000000000000001"),
		},
		RecordDigest: "0000000000000000000000000000000000000000000000000000000000000001",
		State:        state, RecordedAt: recordedAt.UTC().Format(time.RFC3339Nano),
	}
}

func (e *testEnv) mergeOverlay(actor contract.Actor, obligations []recoveryObligation) (contract.Payload, error) {
	e.t.Helper()
	return e.callAs(actor, e.scope, opRestoreMerge, restoreMergeInput{
		RestoreJobID:     e.ids.New(),
		CapturedAt:       e.clock.Now().UTC().Format(time.RFC3339Nano),
		SourceGeneration: e.generation(),
		Obligations:      obligations,
	})
}

func (e *testEnv) mustMerge(obligations []recoveryObligation) wireRecoveryMerge {
	e.t.Helper()
	payload, err := e.mergeOverlay(e.actor, obligations)
	if err != nil {
		e.t.Fatalf("%s failed: %v", opRestoreMerge, err)
	}
	if payload.Status != contract.StatusCompleted || payload.Error != nil {
		e.t.Fatalf("%s did not complete: status %q error %v", opRestoreMerge, payload.Status, payload.Error)
	}
	var out recoveryMergeBody
	e.decode(payload.Data, &out)
	return out.Resource
}

// TestRestoreMergeCarriesAnInFlightDispatchToOutcomeUnknownWithoutResending
// proves the central rule: an operation the overlay captured mid-flight is
// carried to outcome_unknown -- never back to ready or executing, which is
// the only state that would let the controller send the provider call a
// second time -- and the reason is recorded as an open obligation against
// the operation.
func TestRestoreMergeCarriesAnInFlightDispatchToOutcomeUnknownWithoutResending(t *testing.T) {
	e := newEnv(t)
	op, _ := e.dispatched()
	before := e.mustFindOperation(op.ID)
	if before.State != opStateExecuting {
		t.Fatalf("fixture operation is %q, want executing so the merge has an in-flight dispatch to fold", before.State)
	}

	ob := e.restoreObligation(obligationClaimedEffect, op.ID, before.Version, before.State, e.clock.Now().Add(-time.Second))
	merge := e.mustMerge([]recoveryObligation{ob})
	if len(merge.Applied) != 1 || merge.Applied[0] != ob.ID {
		t.Fatalf("merge applied %v, want exactly the captured obligation %s", merge.Applied, ob.ID)
	}

	after := e.mustFindOperation(op.ID)
	if after.State != opStateOutcomeUnknown {
		t.Fatalf("merged operation is %q, want outcome_unknown (never a resendable state)", after.State)
	}
	open := e.openObligations(op.ID)
	if len(open) != 1 || open[0].ID != ob.ID || open[0].Kind != oblRestoreOverlay {
		t.Fatalf("open obligations after merge = %+v, want one %s obligation keyed by %s", open, oblRestoreOverlay, ob.ID)
	}
	if open[0].State != obligationStateOpen {
		t.Fatalf("merged obligation state = %q, want %q", open[0].State, obligationStateOpen)
	}
}

// TestRestoreMergeIsIdempotentUnderReplay proves the crash-resume property:
// the same merge run twice folds nothing twice. A recovered retry after a
// crash between storage's overlay write and its resume calls MergeOverlay
// again with the same obligations, so a second effects_obligations row (or
// a second state transition) would be a real duplicate.
func TestRestoreMergeIsIdempotentUnderReplay(t *testing.T) {
	e := newEnv(t)
	op, _ := e.dispatched()
	row := e.mustFindOperation(op.ID)
	ob := e.restoreObligation(obligationUnknownEffect, op.ID, row.Version, row.State, e.clock.Now().Add(-time.Second))

	first := e.mustMerge([]recoveryObligation{ob})
	if len(first.Applied) != 1 {
		t.Fatalf("first merge applied %v, want the obligation", first.Applied)
	}
	versionAfterFirst := e.mustFindOperation(op.ID).Version

	second := e.mustMerge([]recoveryObligation{ob})
	if len(second.Applied) != 0 || len(second.Skipped) != 1 || second.Skipped[0] != ob.ID {
		t.Fatalf("replayed merge applied %v / skipped %v, want nothing applied and the obligation skipped",
			second.Applied, second.Skipped)
	}
	if got := e.obligationsOf(op.ID); len(got) != 1 {
		t.Fatalf("replay produced %d obligation rows, want exactly 1", len(got))
	}
	if got := e.mustFindOperation(op.ID).Version; got != versionAfterFirst {
		t.Fatalf("replay bumped the operation version to %d, want it unchanged at %d", got, versionAfterFirst)
	}
}

// TestRestoreMergeNeverReopensASettledOperation proves the merge cannot
// resurrect a completed dispatch: an operation the restored image already
// records succeeded keeps that outcome, and no obligation is opened against
// it.
func TestRestoreMergeNeverReopensASettledOperation(t *testing.T) {
	e := newEnv(t)
	op, dispatch := e.dispatched()
	settled := e.recordOp(op.ID, dispatch.AttemptID, e.generation(), e.observation(dispSucceeded))
	if settled.State != opStateSucceeded {
		t.Fatalf("fixture operation is %q, want succeeded", settled.State)
	}

	row := e.mustFindOperation(op.ID)
	ob := e.restoreObligation(obligationClaimedEffect, op.ID, row.Version, "executing", e.clock.Now().Add(-time.Second))
	merge := e.mustMerge([]recoveryObligation{ob})
	if len(merge.Applied) != 0 || len(merge.Skipped) != 1 {
		t.Fatalf("merge applied %v / skipped %v, want the settled operation skipped", merge.Applied, merge.Skipped)
	}
	if got := e.mustFindOperation(op.ID); got.State != opStateSucceeded {
		t.Fatalf("settled operation was reopened to %q; a merge must never undo a recorded outcome", got.State)
	}
	for _, o := range e.obligationsOf(op.ID) {
		if o.Kind == oblRestoreOverlay {
			t.Fatalf("merge opened a recovery obligation against an already-settled operation: %+v", o)
		}
	}
}

// TestRestoreMergeNeverResurrectsAnOperationTheRestoredImageDoesNotHold
// proves the monotonic floor on the other side: an obligation naming an
// operation the rewound database no longer contains is skipped, no row is
// invented from the obligation alone, and the merge still succeeds.
func TestRestoreMergeNeverResurrectsAnOperationTheRestoredImageDoesNotHold(t *testing.T) {
	e := newEnv(t)
	missing := e.ids.New()
	ob := e.restoreObligation(obligationUnknownEffect, missing, 1, "outcome_unknown", e.clock.Now().Add(-time.Second))

	merge := e.mustMerge([]recoveryObligation{ob})
	if len(merge.Applied) != 0 || len(merge.Skipped) != 1 {
		t.Fatalf("merge applied %v / skipped %v, want the absent operation skipped", merge.Applied, merge.Skipped)
	}
	var operations, obligations int64
	if err := e.write(func(unit contract.Unit) error {
		if err := unit.QueryRowContext(e.ctx,
			`SELECT COUNT(*) FROM effects_operations WHERE id = ?`, string(missing)).Scan(&operations); err != nil {
			return err
		}
		return unit.QueryRowContext(e.ctx,
			`SELECT COUNT(*) FROM effects_obligations WHERE id = ?`, string(ob.ID)).Scan(&obligations)
	}); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if operations != 0 || obligations != 0 {
		t.Fatalf("the merge invented %d operations and %d obligations from an obligation alone", operations, obligations)
	}
}

// TestRestoreMergeAcceptsAnOverlayWithNothingToMerge is the required
// behavior "nothing to merge is not an error": an installation with no
// pending obligations still completes its overlay merge.
func TestRestoreMergeAcceptsAnOverlayWithNothingToMerge(t *testing.T) {
	e := newEnv(t)
	merge := e.mustMerge([]recoveryObligation{})
	if len(merge.Applied) != 0 || len(merge.Skipped) != 0 {
		t.Fatalf("empty overlay merge reported applied %v / skipped %v, want both empty", merge.Applied, merge.Skipped)
	}
	if merge.Owner != ownerName {
		t.Fatalf("merge result names owner %q, want %q", merge.Owner, ownerName)
	}
}

// TestRestoreMergeSkipsAnotherOwnersSliceOfTheSameOverlay proves each owner
// folds only what it owns: the overlay is one document carrying every
// owner's obligations, and effects must leave identity's and memory's
// alone rather than misinterpreting them.
func TestRestoreMergeSkipsAnotherOwnersSliceOfTheSameOverlay(t *testing.T) {
	e := newEnv(t)
	op, _ := e.dispatched()
	row := e.mustFindOperation(op.ID)
	mine := e.restoreObligation(obligationClaimedEffect, op.ID, row.Version, row.State, e.clock.Now().Add(-time.Second))
	foreign := e.restoreObligation("revoked_credential", e.ids.New(), 2, "revoked", e.clock.Now().Add(-time.Second))
	foreign.Owner = "identity"

	merge := e.mustMerge([]recoveryObligation{mine, foreign})
	if len(merge.Applied) != 1 || merge.Applied[0] != mine.ID {
		t.Fatalf("merge applied %v, want only this owner's obligation %s", merge.Applied, mine.ID)
	}
	if len(merge.Skipped) != 1 || merge.Skipped[0] != foreign.ID {
		t.Fatalf("merge skipped %v, want the other owner's obligation %s", merge.Skipped, foreign.ID)
	}
}

// TestRestoreMergeRefusesAnObligationNewerThanTheOverlayCapturePoint proves
// the merge cannot back-date evidence into the rewound state.
func TestRestoreMergeRefusesAnObligationNewerThanTheOverlayCapturePoint(t *testing.T) {
	e := newEnv(t)
	op, _ := e.dispatched()
	row := e.mustFindOperation(op.ID)
	ob := e.restoreObligation(obligationClaimedEffect, op.ID, row.Version, row.State, e.clock.Now().Add(time.Minute))

	_ = e.expectFault(opRestoreMerge, restoreMergeInput{
		RestoreJobID: e.ids.New(), CapturedAt: e.clock.Now().UTC().Format(time.RFC3339Nano),
		SourceGeneration: e.generation(), Obligations: []recoveryObligation{ob},
	}, contract.CodeInvalidInput)

	if got := e.mustFindOperation(op.ID); got.State != row.State {
		t.Fatalf("a refused merge still transitioned the operation to %q", got.State)
	}
}

// TestRestoreMergeRefusesANonControllerActor is the required behavior
// "calling _effects.restore.merge with a non-controller actor is refused".
// The merge runs outside application dispatch, so its caller allowlist is
// never consulted; this handler's own check is the whole defence, and it
// must refuse every actor kind the controller never runs under.
func TestRestoreMergeRefusesANonControllerActor(t *testing.T) {
	e := newEnv(t)
	op, _ := e.dispatched()
	row := e.mustFindOperation(op.ID)
	ob := e.restoreObligation(obligationClaimedEffect, op.ID, row.Version, row.State, e.clock.Now().Add(-time.Second))
	input := restoreMergeInput{
		RestoreJobID: e.ids.New(), CapturedAt: e.clock.Now().UTC().Format(time.RFC3339Nano),
		SourceGeneration: e.generation(), Obligations: []recoveryObligation{ob},
	}

	for _, kind := range []string{contract.KindWorker, contract.KindHuman, contract.KindClientAgent} {
		_ = e.expectFaultAs(contract.Actor{PrincipalID: e.ids.New(), Kind: kind}, e.scope,
			opRestoreMerge, input, contract.CodePermissionDenied)
	}
	// An actor carrying no principal at all never reaches this handler:
	// storage refuses to open the transaction for it (invalid_input,
	// "actor principal is required"), which is a stronger guarantee than
	// this handler's own empty-principal branch and is why that branch is
	// a floor rather than the defence.
	if _, err := e.mergeOverlay(contract.Actor{Kind: contract.KindService}, []recoveryObligation{ob}); err == nil {
		t.Fatal("a principal-less actor opened a write transaction")
	}

	if got := e.mustFindOperation(op.ID); got.State != row.State {
		t.Fatalf("a refused merge still transitioned the operation to %q", got.State)
	}
	if got := e.obligationsOf(op.ID); len(got) != 0 {
		t.Fatalf("a refused merge still opened obligations: %+v", got)
	}
}
