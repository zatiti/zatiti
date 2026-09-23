package memory

import (
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Tests for _memory.restore.merge, driven through the real registered
// handler over real storage. The obligations are the exact shape
// _memory.manifest reports and internal/installation seals into a recovery
// overlay.

func (e *testEnv) restoreObligation(resourceID contract.ID, state string, recordedAt time.Time) recoveryObligation {
	e.t.Helper()
	return recoveryObligation{
		ID: e.ids.New(), Owner: ownerName, Kind: obligationMemoryWrite,
		ResourceID: resourceID, ResourceVersion: 1,
		RecordArtifact: wireArtifactRef{
			ID:     e.ids.New(),
			Digest: contract.Digest("0000000000000000000000000000000000000000000000000000000000000001"),
		},
		RecordDigest: contract.Digest("0000000000000000000000000000000000000000000000000000000000000001"),
		State:        state, RecordedAt: recordedAt.UTC().Format(time.RFC3339Nano),
	}
}

func (e *testEnv) mergeOverlayAs(actor contract.Actor, obligations []recoveryObligation) (contract.Payload, error) {
	e.t.Helper()
	return e.callAs(actor, e.scope, opRestoreMerge, restoreMergeInput{
		RestoreJobID:     e.ids.New(),
		CapturedAt:       e.clock.Now().UTC().Format(time.RFC3339Nano),
		SourceGeneration: 1,
		Obligations:      obligations,
	})
}

func (e *testEnv) mustMergeOverlay(obligations []recoveryObligation) wireRecoveryMerge {
	e.t.Helper()
	payload, err := e.mergeOverlayAs(e.actor, obligations)
	if err != nil {
		e.t.Fatalf("%s failed: %v", opRestoreMerge, err)
	}
	var out recoveryMergeOutput
	e.decodePayload(payload, &out)
	return out.Resource
}

func (e *testEnv) openReconciliation() []*reconciliationRow {
	e.t.Helper()
	var out []*reconciliationRow
	err := e.db.Read(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		rows, err := listOpenReconciliation(e.ctx, unit, e.install)
		out = rows
		return err
	})
	if err != nil {
		e.t.Fatalf("list open reconciliation: %v", err)
	}
	return out
}

// TestRestoreMergePreservesAnUnresolvedWriterOutcomeAcrossTheRewind proves
// the behavior that makes the merge worth running at all: a writer intent
// whose outcome was unresolved when the overlay was captured survives the
// database rewind as an explicit open obligation, rather than disappearing
// with the rows the rewind replaced.
func TestRestoreMergePreservesAnUnresolvedWriterOutcomeAcrossTheRewind(t *testing.T) {
	e := newEnv(t)
	brain := e.provisionedBrain(brainKindInstallation, "", "")
	e.seedBrain(brain)
	if got := e.openReconciliation(); len(got) != 0 {
		t.Fatalf("fixture already carries %d open obligations; the test would prove nothing", len(got))
	}

	ob := e.restoreObligation(brain.ID, reqWriteUnknown, e.clock.Now().Add(-time.Second))
	merge := e.mustMergeOverlay([]recoveryObligation{ob})
	if len(merge.Applied) != 1 || merge.Applied[0] != ob.ID {
		t.Fatalf("merge applied %v, want exactly the captured obligation %s", merge.Applied, ob.ID)
	}

	open := e.openReconciliation()
	if len(open) != 1 {
		t.Fatalf("open obligations after the merge = %d, want 1", len(open))
	}
	if open[0].ID != ob.ID {
		t.Fatalf("merged obligation id = %s, want the overlay's own %s", open[0].ID, ob.ID)
	}
	if open[0].Kind != reconcileRestoreOverlay {
		t.Fatalf("merged obligation kind = %q, want %q", open[0].Kind, reconcileRestoreOverlay)
	}
	if open[0].SourceBrainID != brain.ID {
		t.Fatalf("merged obligation names brain %s, want the one the restored image still holds (%s)",
			open[0].SourceBrainID, brain.ID)
	}

	// The next backup's manifest sees it: the obligation is not merely a
	// row, it is reported as an unresolved liability again.
	var manifest manifestOutput
	payload, err := e.call(opManifest, manifestInput{Scope: wireScope{InstallationID: e.install}})
	if err != nil {
		t.Fatalf("%s: %v", opManifest, err)
	}
	e.decodePayload(payload, &manifest)
	found := false
	for _, r := range manifest.Obligations {
		if r.Code == reqReconciliation {
			found = true
		}
	}
	if !found {
		t.Fatalf("the merged obligation is invisible to the next backup manifest: %+v", manifest.Obligations)
	}
}

// TestRestoreMergeIsIdempotentUnderReplay proves the crash-resume property:
// a recovered retry calls MergeOverlay again with the same obligations, and
// the installation must end with one obligation per captured obligation,
// never two.
func TestRestoreMergeIsIdempotentUnderReplay(t *testing.T) {
	e := newEnv(t)
	brain := e.provisionedBrain(brainKindInstallation, "", "")
	e.seedBrain(brain)
	ob := e.restoreObligation(brain.ID, reqWriterProvision, e.clock.Now().Add(-time.Second))

	first := e.mustMergeOverlay([]recoveryObligation{ob})
	if len(first.Applied) != 1 {
		t.Fatalf("first merge applied %v, want the obligation", first.Applied)
	}
	second := e.mustMergeOverlay([]recoveryObligation{ob})
	if len(second.Applied) != 0 || len(second.Skipped) != 1 || second.Skipped[0] != ob.ID {
		t.Fatalf("replayed merge applied %v / skipped %v, want nothing applied a second time",
			second.Applied, second.Skipped)
	}
	if got := e.openReconciliation(); len(got) != 1 {
		t.Fatalf("replay produced %d obligations, want exactly 1", len(got))
	}
}

// TestRestoreMergeRecordsNoLineageTheRewoundDatabaseCannotBack proves the
// merge never asserts a reference the restored image does not hold: an
// obligation naming a brain the rewind removed still becomes an explicit
// obligation, but without claiming it belongs to a brain that no longer
// exists.
func TestRestoreMergeRecordsNoLineageTheRewoundDatabaseCannotBack(t *testing.T) {
	e := newEnv(t)
	missing := e.ids.New()
	ob := e.restoreObligation(missing, reqWriteUnknown, e.clock.Now().Add(-time.Second))

	merge := e.mustMergeOverlay([]recoveryObligation{ob})
	if len(merge.Applied) != 1 {
		t.Fatalf("merge applied %v, want the obligation recorded even without its brain", merge.Applied)
	}
	open := e.openReconciliation()
	if len(open) != 1 {
		t.Fatalf("open obligations = %d, want 1", len(open))
	}
	if open[0].SourceBrainID != "" {
		t.Fatalf("merged obligation names brain %s, which the restored image does not hold", open[0].SourceBrainID)
	}
	var brains int64
	if err := e.db.Read(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		return unit.QueryRowContext(e.ctx,
			`SELECT COUNT(*) FROM memory_brains WHERE id = ?`, string(missing)).Scan(&brains)
	}); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if brains != 0 {
		t.Fatal("the merge invented a brain from an obligation alone")
	}
}

// TestRestoreMergeNeverCreatesAWriterIntentOrClaim proves the merge cannot
// become a resend: it records an unresolved outcome and nothing else. No
// writer intent, no adapter command identity and no claim may appear.
func TestRestoreMergeNeverCreatesAWriterIntentOrClaim(t *testing.T) {
	e := newEnv(t)
	brain := e.provisionedBrain(brainKindInstallation, "", "")
	e.seedBrain(brain)
	e.mustMergeOverlay([]recoveryObligation{
		e.restoreObligation(brain.ID, reqWriteUnknown, e.clock.Now().Add(-time.Second)),
	})

	var intents, claims int64
	if err := e.db.Read(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		if err := unit.QueryRowContext(e.ctx,
			`SELECT COUNT(*) FROM memory_intents WHERE installation_id = ?`, string(e.install)).Scan(&intents); err != nil {
			return err
		}
		return unit.QueryRowContext(e.ctx,
			`SELECT COUNT(*) FROM memory_claims WHERE brain_id = ?`, string(brain.ID)).Scan(&claims)
	}); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if intents != 0 || claims != 0 {
		t.Fatalf("the merge created %d writer intents and %d claims; it must never resend or fabricate a write",
			intents, claims)
	}
	if len(e.ports.inputsFor(opEffectsPrepare)) != 0 {
		t.Fatal("the merge reached the effects owner; an overlay merge dispatches nothing")
	}
}

// TestRestoreMergeAcceptsAnOverlayWithNothingToMerge is the required
// behavior "nothing to merge is not an error".
func TestRestoreMergeAcceptsAnOverlayWithNothingToMerge(t *testing.T) {
	e := newEnv(t)
	merge := e.mustMergeOverlay([]recoveryObligation{})
	if len(merge.Applied) != 0 || len(merge.Skipped) != 0 {
		t.Fatalf("empty overlay merge reported applied %v / skipped %v, want both empty", merge.Applied, merge.Skipped)
	}
	if got := e.openReconciliation(); len(got) != 0 {
		t.Fatalf("an empty merge opened %d obligations", len(got))
	}
}

// TestRestoreMergeSkipsAnotherOwnersSliceOfTheSameOverlay proves each owner
// folds only what it owns.
func TestRestoreMergeSkipsAnotherOwnersSliceOfTheSameOverlay(t *testing.T) {
	e := newEnv(t)
	brain := e.provisionedBrain(brainKindInstallation, "", "")
	e.seedBrain(brain)
	mine := e.restoreObligation(brain.ID, reqWriteUnknown, e.clock.Now().Add(-time.Second))
	foreign := e.restoreObligation(e.ids.New(), "outcome_unknown", e.clock.Now().Add(-time.Second))
	foreign.Owner = "effects"
	foreign.Kind = "unknown_effect"

	merge := e.mustMergeOverlay([]recoveryObligation{mine, foreign})
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
	brain := e.provisionedBrain(brainKindInstallation, "", "")
	e.seedBrain(brain)
	ob := e.restoreObligation(brain.ID, reqWriteUnknown, e.clock.Now().Add(time.Minute))

	_, err := e.mergeOverlayAs(e.actor, []recoveryObligation{ob})
	if f := decodeFault(err); f == nil || f.Code != contract.CodeInvalidInput {
		t.Fatalf("merge of a post-capture obligation = %v, want invalid_input", err)
	}
	if got := e.openReconciliation(); len(got) != 0 {
		t.Fatalf("a refused merge still opened %d obligations", len(got))
	}
}

// TestRestoreMergeRefusesANonControllerActor is the required behavior
// "calling _memory.restore.merge with a non-controller actor is refused".
// The merge runs outside application dispatch, so its caller allowlist is
// never consulted; this handler's own check is the whole defence.
func TestRestoreMergeRefusesANonControllerActor(t *testing.T) {
	e := newEnv(t)
	brain := e.provisionedBrain(brainKindInstallation, "", "")
	e.seedBrain(brain)
	obligations := []recoveryObligation{e.restoreObligation(brain.ID, reqWriteUnknown, e.clock.Now().Add(-time.Second))}

	for _, kind := range []string{contract.KindWorker, contract.KindHuman, contract.KindClientAgent} {
		_, err := e.mergeOverlayAs(contract.Actor{PrincipalID: e.ids.New(), Kind: kind}, obligations)
		if f := decodeFault(err); f == nil || f.Code != contract.CodePermissionDenied {
			t.Fatalf("merge as a %s actor = %v, want permission_denied", kind, err)
		}
	}
	if got := e.openReconciliation(); len(got) != 0 {
		t.Fatalf("a refused merge still opened %d obligations", len(got))
	}
}
