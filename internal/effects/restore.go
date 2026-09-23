package effects

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Effects' half of the P00-011 offline restore protocol.
//
// _effects.restore.merge folds a RecoveryOverlay's effects slice back into
// this owner's own tables after the controller has swapped the live database
// file back to an older image. The obligations it receives are the pending
// operations installation captured through _effects.pending at the moment
// the overlay was sealed: claimed_effect for an operation still in flight,
// unknown_effect for one whose outcome was already unknown.
//
// What "folding back" may and may not mean here is the whole design:
//
//   - It may not resurrect. An operation absent from the restored image was
//     created after the backup; its immutable action, its attempts and its
//     accounting reservation went with the rewound bytes. Recreating a row
//     from an obligation would invent an action this owner never
//     canonicalized. Such an obligation is skipped, and the restore does not
//     fail over it.
//   - It may not reopen a settled outcome. An operation the restored image
//     already records terminal (succeeded, failed, denied, expired,
//     cancelled) keeps that outcome: the overlay's claim that it was in
//     flight at capture time is older evidence than the terminal record the
//     restored image carries.
//   - It may not make anything resendable. An operation still in flight in
//     the restored image is carried to outcome_unknown -- never back to
//     ready or executing -- which is exactly the discipline _effects.pending
//     already documents ("claimed attempts after restart become unknown, not
//     ready for resend"). The physical call may or may not have happened;
//     after a rewind this owner cannot tell, and outcome_unknown is the only
//     honest position.
//
// Each obligation is folded at most once, keyed by its own obligation id,
// which becomes the effects_obligations primary key. A repeated call --
// exactly what a crash between storage's overlay write and its resume
// produces -- therefore applies nothing twice.
//
// The caller check is deliberately its own: this operation is invoked by
// cmd/zatiti directly on this module (contract.Module.Handle) inside
// storage's paused restore-overlay transaction, because at that moment no
// *application.Application exists over the freshly reopened database to
// route an ordinary Internal call through. That bypasses application
// dispatch's centralized caller allowlist, so this handler re-establishes
// what it can for itself. See requireControllerActor for exactly how much
// that is, and what it deliberately does not claim.

// Obligation kinds effects owns inside a BackupManifest or RecoveryOverlay.
const (
	obligationClaimedEffect = "claimed_effect"
	obligationUnknownEffect = "unknown_effect"
)

// oblRestoreOverlay is the effects_obligations kind one merged recovery
// obligation opens: an operation this installation carried into a restore
// whose real outcome the rewind put out of reach.
const oblRestoreOverlay = "restore_overlay"

// obligationStateOpen is the unresolved state a merged obligation opens in.
const obligationStateOpen = "open"

// recoveryObligation mirrors $defs/RecoveryObligation: one retained
// obligation exactly as installation captured and sealed it.
type recoveryObligation struct {
	ID              contract.ID     `json:"id"`
	Owner           string          `json:"owner"`
	Kind            string          `json:"kind"`
	ResourceID      contract.ID     `json:"resource_id"`
	ResourceVersion int64           `json:"resource_version"`
	RecordArtifact  wireArtifactRef `json:"record_artifact"`
	RecordDigest    string          `json:"record_digest"`
	State           string          `json:"state"`
	RecordedAt      string          `json:"recorded_at"`
}

// restoreMergeInput is the _effects.restore.merge input.
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

// recoveryMergeBody is the _effects.restore.merge output envelope.
type recoveryMergeBody struct {
	Resource wireRecoveryMerge `json:"resource"`
}

// mergeDetail is the inert JSON one merged obligation records alongside
// itself: what the overlay observed, so a later reconciliation can tell this
// obligation apart from one an ordinary dispatch opened.
type mergeDetail struct {
	RestoreJobID     contract.ID `json:"restore_job_id"`
	ObligationKind   string      `json:"obligation_kind"`
	CapturedState    string      `json:"captured_state"`
	CapturedAt       string      `json:"captured_at"`
	SourceGeneration int64       `json:"source_generation"`
	RecordDigest     string      `json:"record_digest"`
}

// handleRestoreMerge implements _effects.restore.merge. See this file's doc
// comment for the three rules it enforces.
func (s *Service) handleRestoreMerge(ctx context.Context, unit contract.Unit, in restoreMergeInput) (contract.Outcome[recoveryMergeBody], error) {
	if err := requireControllerActor(unit); err != nil {
		return contract.Outcome[recoveryMergeBody]{}, err
	}
	capturedAt, err := time.Parse(time.RFC3339Nano, in.CapturedAt)
	if err != nil {
		return contract.Outcome[recoveryMergeBody]{}, invalidInput(
			"recovery overlay capture timestamp is not a valid instant")
	}
	merge := wireRecoveryMerge{
		RestoreJobID: in.RestoreJobID, Owner: ownerName,
		Applied: []contract.ID{}, Skipped: []contract.ID{},
	}
	now := s.now()
	for _, ob := range in.Obligations {
		if ob.Owner != ownerName || (ob.Kind != obligationClaimedEffect && ob.Kind != obligationUnknownEffect) {
			// Another owner's slice of the same overlay.
			merge.Skipped = append(merge.Skipped, ob.ID)
			continue
		}
		recordedAt, err := time.Parse(time.RFC3339Nano, ob.RecordedAt)
		if err != nil {
			return contract.Outcome[recoveryMergeBody]{}, invalidInput(
				"obligation %s carries an invalid recorded_at timestamp", ob.ID)
		}
		if recordedAt.After(capturedAt) {
			return contract.Outcome[recoveryMergeBody]{}, invalidInput(
				"obligation %s was recorded after the recovery overlay's own capture point; it cannot be merged", ob.ID)
		}
		applied, err := s.foldObligation(ctx, unit, in, ob, now)
		if err != nil {
			return contract.Outcome[recoveryMergeBody]{}, err
		}
		if applied {
			merge.Applied = append(merge.Applied, ob.ID)
		} else {
			merge.Skipped = append(merge.Skipped, ob.ID)
		}
	}
	return completedOutcome(recoveryMergeBody{Resource: merge})
}

// foldObligation folds one captured obligation into this owner's tables and
// reports whether it actually changed anything.
func (s *Service) foldObligation(ctx context.Context, unit contract.Unit, in restoreMergeInput, ob recoveryObligation, now time.Time) (bool, error) {
	merged, err := obligationAlreadyMerged(ctx, unit, ob.ID)
	if err != nil || merged {
		// Idempotent replay: this exact obligation was folded by an earlier
		// call of the same merge.
		return false, err
	}
	op, err := loadOperation(ctx, unit, ob.ResourceID)
	if err != nil {
		return false, fmt.Errorf("effects: load operation for recovery obligation %s: %w", ob.ID, err)
	}
	if op == nil {
		// Never resurrect: the operation postdates the restored image.
		return false, nil
	}
	if op.InstallID != unit.Scope().InstallationID {
		return false, permissionDenied("recovery obligation %s names an operation outside this installation", ob.ID)
	}
	if terminalOperationState(op.State) {
		// Never reopen a settled outcome.
		return false, nil
	}
	detail, err := json.Marshal(mergeDetail{
		RestoreJobID: in.RestoreJobID, ObligationKind: ob.Kind, CapturedState: ob.State,
		CapturedAt: in.CapturedAt, SourceGeneration: in.SourceGeneration, RecordDigest: ob.RecordDigest,
	})
	if err != nil {
		return false, fmt.Errorf("effects: encode recovery obligation detail: %w", err)
	}
	if err := insertObligation(ctx, unit, &obligationRow{
		ID: ob.ID, OperationID: op.ID, Kind: oblRestoreOverlay,
		State: obligationStateOpen, DetailJSON: string(detail), CreatedAt: now,
	}); err != nil {
		return false, fmt.Errorf("effects: open recovery obligation %s: %w", ob.ID, err)
	}
	if err := emitTransition(ctx, unit, eventObligationOpened, ob.ID, 1); err != nil {
		return false, err
	}
	if op.State == opStateOutcomeUnknown {
		// Already the only honest position; the obligation records why.
		return true, nil
	}
	// Never resendable: outcome_unknown, never back to ready or executing.
	if err := updateOperationState(ctx, unit, op, opStateOutcomeUnknown, op.JobID, now); err != nil {
		return false, err
	}
	if err := emitTransition(ctx, unit, eventOperationUnknown, op.ID, contract.Version(op.Version)); err != nil {
		return false, err
	}
	return true, nil
}

// obligationAlreadyMerged reports whether an earlier merge already folded
// this exact obligation. The recovery obligation's own id is the
// effects_obligations primary key, so the check is exact rather than
// heuristic.
func obligationAlreadyMerged(ctx context.Context, unit contract.Unit, id contract.ID) (bool, error) {
	var count int64
	err := unit.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM effects_obligations WHERE id = ?`, string(id)).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("effects: check merged recovery obligation: %w", err)
	}
	return count > 0, nil
}

// terminalOperationState reports whether an operation has already settled.
func terminalOperationState(state string) bool {
	switch state {
	case opStateSucceeded, opStateFailed, opStateDenied, opStateExpired, opStateCancelled:
		return true
	}
	return false
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
// and the ordinary route (_identity.authority through Ports) needs the live
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
