package memory

import (
	"context"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Memory's half of the P00-011 offline restore protocol.
//
// _memory.restore.merge folds a RecoveryOverlay's memory slice back into
// this owner's own tables after the controller has swapped the live database
// file back to an older image. The obligations it receives are what
// _memory.manifest reported unresolved at the moment the overlay was sealed:
// an open reconciliation, or a writer intent that never reached a terminal
// disposition. Both mean the same thing after a rewind -- a Serenity write
// may or may not have landed upstream, and the rows that recorded which went
// away with the rewind -- so both are folded back as an explicit, open
// reconciliation obligation against the rewound state.
//
// What this merge deliberately does NOT do:
//
//   - It never claims a provider write happened, and never claims one did
//     not. It records only that the outcome is unresolved, which is the one
//     thing the overlay actually establishes.
//   - It never re-sends anything. No writer intent is created, no adapter
//     command id is minted and no claim row is written; a resend after a
//     rewind is exactly the blind retry memory.remember's own contract
//     forbids.
//   - It never resolves an obligation. Resolution needs real upstream
//     evidence, which a restore does not produce.
//
// Each obligation is folded at most once, keyed by its own obligation id,
// which becomes the memory_reconciliation primary key. A repeated call --
// exactly what a crash between storage's overlay write and its resume
// produces -- therefore applies nothing twice, and the installation ends up
// with one obligation per captured obligation, never two.
//
// The caller check is deliberately its own: this operation is invoked by
// cmd/zatiti directly on this module (contract.Module.Handle) inside
// storage's paused restore-overlay transaction, because at that moment no
// *application.Application exists over the freshly reopened database to
// route an ordinary Internal call through. That bypasses application
// dispatch's centralized caller allowlist, so this handler re-establishes
// what it can for itself; see requireControllerActor for exactly how much
// that is.

// obligationMemoryWrite is the obligation kind memory owns inside a
// BackupManifest or RecoveryOverlay.
const obligationMemoryWrite = "memory_write"

// reconcileRestoreOverlay is the memory_reconciliation kind one merged
// recovery obligation opens: a writer outcome this installation carried into
// a restore whose real disposition the rewind put out of reach.
const reconcileRestoreOverlay = "restore_overlay"

// recoveryObligation mirrors $defs/RecoveryObligation: one retained
// obligation exactly as installation captured and sealed it.
type recoveryObligation struct {
	ID              contract.ID     `json:"id"`
	Owner           string          `json:"owner"`
	Kind            string          `json:"kind"`
	ResourceID      contract.ID     `json:"resource_id"`
	ResourceVersion int64           `json:"resource_version"`
	RecordArtifact  wireArtifactRef `json:"record_artifact"`
	RecordDigest    contract.Digest `json:"record_digest"`
	State           string          `json:"state"`
	RecordedAt      string          `json:"recorded_at"`
}

// restoreMergeInput is the _memory.restore.merge input.
type restoreMergeInput struct {
	RestoreJobID     contract.ID          `json:"restore_job_id"`
	CapturedAt       string               `json:"captured_at"`
	SourceGeneration int64                `json:"source_generation"`
	Obligations      []recoveryObligation `json:"obligations"`
}

// wireRecoveryMerge mirrors $defs/RecoveryMerge.
type wireRecoveryMerge struct {
	RestoreJobID contract.ID   `json:"restore_job_id"`
	Owner        string        `json:"owner"`
	Applied      []contract.ID `json:"applied"`
	Skipped      []contract.ID `json:"skipped"`
}

// recoveryMergeOutput is the _memory.restore.merge output envelope.
type recoveryMergeOutput struct {
	Resource wireRecoveryMerge `json:"resource"`
}

// handleRestoreMerge implements _memory.restore.merge. See this file's doc
// comment for what it does and, more importantly, what it refuses to do.
func (s *Service) handleRestoreMerge(ctx context.Context, unit contract.Unit, in restoreMergeInput) (contract.Outcome[recoveryMergeOutput], error) {
	if err := requireControllerActor(unit); err != nil {
		return contract.Outcome[recoveryMergeOutput]{}, err
	}
	capturedAt, err := time.Parse(time.RFC3339Nano, in.CapturedAt)
	if err != nil {
		return contract.Outcome[recoveryMergeOutput]{}, invalidInput(
			"recovery overlay capture timestamp is not a valid instant")
	}
	merge := wireRecoveryMerge{
		RestoreJobID: in.RestoreJobID, Owner: ownerName,
		Applied: []contract.ID{}, Skipped: []contract.ID{},
	}
	now := s.deps.Clock.Now()
	for _, ob := range in.Obligations {
		if ob.Owner != ownerName || ob.Kind != obligationMemoryWrite {
			// Another owner's slice of the same overlay.
			merge.Skipped = append(merge.Skipped, ob.ID)
			continue
		}
		recordedAt, err := time.Parse(time.RFC3339Nano, ob.RecordedAt)
		if err != nil {
			return contract.Outcome[recoveryMergeOutput]{}, invalidInput(
				"obligation %s carries an invalid recorded_at timestamp", ob.ID)
		}
		if recordedAt.After(capturedAt) {
			return contract.Outcome[recoveryMergeOutput]{}, invalidInput(
				"obligation %s was recorded after the recovery overlay's own capture point; it cannot be merged", ob.ID)
		}
		applied, err := s.foldObligation(ctx, unit, in, ob, now)
		if err != nil {
			return contract.Outcome[recoveryMergeOutput]{}, err
		}
		if applied {
			merge.Applied = append(merge.Applied, ob.ID)
		} else {
			merge.Skipped = append(merge.Skipped, ob.ID)
		}
	}
	return completedOutcome(recoveryMergeOutput{Resource: merge})
}

// foldObligation opens one merged obligation against the rewound state and
// reports whether it actually changed anything.
func (s *Service) foldObligation(ctx context.Context, unit contract.Unit, in restoreMergeInput, ob recoveryObligation, now time.Time) (bool, error) {
	var existing int64
	if err := unit.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memory_reconciliation WHERE id = ?`, string(ob.ID)).Scan(&existing); err != nil {
		return false, fmt.Errorf("memory: check merged recovery obligation: %w", err)
	}
	if existing > 0 {
		// Idempotent replay: this exact obligation was folded by an earlier
		// call of the same merge.
		return false, nil
	}
	r := &reconciliationRow{
		ID: ob.ID, Kind: reconcileRestoreOverlay, InstallationID: unit.Scope().InstallationID,
		Reason: "recovery overlay of restore job " + string(in.RestoreJobID) +
			" retained an unresolved writer obligation (" + ob.State + ") across the database rewind",
		State: reconciliationOpen, CreatedAt: now, UpdatedAt: now,
	}
	// ResourceID names whatever _memory.manifest reported the obligation
	// against -- a promotion for an open reconciliation, a brain for a
	// pending writer intent. It is recorded as the source brain reference
	// only when the restored image still holds that brain, so the merge
	// never asserts a lineage the rewound database cannot back.
	known, err := s.brainExists(ctx, unit, ob.ResourceID)
	if err != nil {
		return false, err
	}
	if known {
		r.SourceBrainID = ob.ResourceID
	}
	if err := insertReconciliation(ctx, unit, r); err != nil {
		return false, fmt.Errorf("memory: open recovery obligation %s: %w", ob.ID, err)
	}
	if err := emit(ctx, unit, eventReconciliationOpened, r.ID, contract.Version(1)); err != nil {
		return false, err
	}
	return true, nil
}

// brainExists reports whether the restored image still holds this brain.
func (s *Service) brainExists(ctx context.Context, unit contract.Unit, id contract.ID) (bool, error) {
	if id == "" {
		return false, nil
	}
	var count int64
	err := unit.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memory_brains WHERE id = ? AND installation_id = ?`,
		string(id), string(unit.Scope().InstallationID)).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("memory: check brain for recovery obligation: %w", err)
	}
	return count > 0, nil
}

// requireControllerActor is the defensive caller check the overlay merge
// performs for itself, because that call path deliberately bypasses
// application dispatch's centralized caller allowlist (see this file's doc
// comment).
//
// What this owner can establish locally, and does: the call carries an
// authenticated principal, and that principal is a service identity -- the
// only kind the controller ever runs under. What it deliberately does NOT
// claim to establish: that the principal is specifically this
// installation's controller. Proving that means resolving the reserved
// controller principal, which lives in identity's own tables; reading them
// from here would be exactly the sibling raw SQL this repository forbids,
// and the ordinary route (a peer call through Ports) needs the live
// Application that provably does not exist at this point in the restore. So
// the strongest check available here is made here, the full name-level check
// is made by identity's own _identity.restore.merge over the same
// transaction's actor, and entrypoint assembly resolves the controller
// principal from identity before it calls any merge at all. A worker, human
// or client-agent actor is refused outright by this check alone.
func requireControllerActor(unit contract.Unit) error {
	actor := unit.Actor()
	if actor.PrincipalID == "" {
		return permissionDenied("authentication is required")
	}
	if actor.Kind != contract.KindService {
		return permissionDenied(
			"only the controller's service principal may merge a recovery overlay; this caller is a %s", actor.Kind)
	}
	return nil
}
