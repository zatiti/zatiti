package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/controller"
)

// This file proves the controller's fail-closed restore boundary through a
// REAL running controller, extending TestBackupRestoresActualBytes
// (defects_test.go), which already proves backup and restore-request
// admission for real but never attaches a controller, so it stops at
// "restore job accepted, awaiting the controller" -- exactly where
// installation.restore's own job state ("running or pending while it awaits
// the controller") says it should stop without one.
//
// History, because this file's own premise changed: until P50 the actual
// backup-to-restore DATA MERGE could not complete anywhere on this tree.
// No operation handed entrypoint assembly a pending restore job's
// backup-artifact reference or its published recovery overlay, and no owner
// declared a merge operation for folding a RecoveryOverlay's obligations
// back into its own tables, so cmd/zatiti's own restoreLifecycle failed
// closed with two prerequisite_missing faults rather than fabricating
// success. P50 closed all of that: production now resolves a real encrypted
// bundle and a real sealed overlay and merges through _effects/_identity/
// _memory.restore.merge, and cmd/zatiti's own tests exercise the completed
// path end to end (cmd/zatiti/restore_merge_test.go).
//
// What remains worth proving here, and what this file now proves, is the
// boundary itself rather than the gap it used to stand for:
// Collaborators.RestoreLifecycle's own contract says "a restore job
// observed without one attached is recorded failed with
// prerequisite_missing, never guessed at". A capability that cannot stage a
// candidate -- because a prerequisite is genuinely absent in this process --
// must leave the installation paused with the job recorded failed, and must
// never touch the database file. That is a permanent property of the
// protocol, not a placeholder for a missing feature.

// refusingRestoreLifecycle is a controller.RestoreLifecycle that cannot
// stage a candidate. It stands for any process where a restore
// prerequisite is genuinely unavailable -- no blob or secret store, an
// unresolvable key reference, a bundle whose bytes are gone -- all of which
// the production capability reports as the same fail-closed
// prerequisite_missing at the same boundary. It is deliberately NOT a copy
// of production: production does real work now (cmd/zatiti/restore.go), and
// this file tests the controller's reaction to a refusal, not the
// capability's own resolution logic.
type refusingRestoreLifecycle struct{}

var _ controller.RestoreLifecycle = refusingRestoreLifecycle{}

// StageCandidate implements controller.RestoreLifecycle by refusing.
func (refusingRestoreLifecycle) StageCandidate(_ context.Context, restoreJobID contract.ID, _ json.RawMessage, _ string) (controller.RestoreCandidate, error) {
	return controller.RestoreCandidate{}, &contract.Fault{
		Code: contract.CodePrerequisiteMissing,
		Message: "this process cannot resolve restore job " + string(restoreJobID) +
			"'s backup artifact into a decrypted candidate database image",
	}
}

// MergeOverlay implements controller.RestoreLifecycle by refusing.
// Unreachable in this test: StageCandidate's own failure means performSwap
// never calls storage.Restorable.PrepareRestore/CommitRestore, so
// MergeOverlay is never invoked -- the failure is safe, not merely honest.
func (refusingRestoreLifecycle) MergeOverlay(_ context.Context, _ contract.Unit, restoreJobID contract.ID, _ controller.RestoreOverlayRef) error {
	return &contract.Fault{
		Code:    contract.CodePrerequisiteMissing,
		Message: "this process cannot open restore job " + string(restoreJobID) + "'s recovery overlay",
	}
}

// TestControllerHonestlyRefusesRestoreMergeAndStaysSafelyPaused (P46 item 6,
// the CRITICAL CONTEXT restore ceiling; Z14.paused_restore): with a restore
// lifecycle that cannot stage a candidate, a real controller driving a real
// accepted installation.restore job records it failed with
// prerequisite_missing at the StageCandidate boundary -- never guessed at,
// never fabricated into a resumed installation -- and the installation
// stays paused in maintenance. No database file is ever touched:
// StageCandidate fails before performSwap ever calls
// storage.Restorable.PrepareRestore/CommitRestore
// (internal/controller/restore.go), so the failure is safe, not merely
// honest.
func TestControllerHonestlyRefusesRestoreMergeAndStaysSafelyPaused(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	ctl, err := controller.New(controller.Config{StateDir: f.stateDir, TickInterval: 15 * time.Millisecond}, f.app, f.db, f.own, map[string]contract.Adapter{}, f.clock)
	if err != nil {
		t.Fatalf("controller.New: %v", err)
	}
	if err := ctl.Attach(controller.Collaborators{
		Identity:         f.controllerIdentity(),
		Blobs:            f.plat.Blobs(),
		Jobs:             f.jobRunners(),
		Operator:         f.app,
		RestoreLifecycle: refusingRestoreLifecycle{},
	}); err != nil {
		t.Fatalf("controller.Attach: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ctl.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Errorf("controller did not stop within 10s of cancellation")
		}
	})

	// Backup is a synchronous LocalIO mutation (no database swap, no
	// controller involvement needed): identical to defects_test.go's
	// TestBackupRestoresActualBytes, its job is already succeeded the
	// instant installation.backup returns.
	f.must(f.owner, "installation.pause", "restore2-pause", map[string]any{"scope": f.scope(), "expected_version": 1})
	backup := f.must(f.owner, "installation.backup", "restore2-backup", map[string]any{"scope": f.scope()})
	var backupJob struct {
		Resource struct {
			ID     contract.ID     `json:"id"`
			State  string          `json:"state"`
			Result json.RawMessage `json:"result"`
		} `json:"resource"`
	}
	decode(t, backup.Data, &backupJob)
	inspected := f.must(f.owner, "installation.job.get", "", map[string]any{"scope": f.scope(), "id": backupJob.Resource.ID})
	decode(t, inspected.Data, &backupJob)
	if backupJob.Resource.State != "succeeded" {
		t.Fatalf("backup job state %q, want succeeded: %s", backupJob.Resource.State, inspected.Data)
	}
	var result struct {
		Resource struct {
			Artifact artifactRef `json:"artifact"`
		} `json:"resource"`
	}
	decode(t, backupJob.Resource.Result, &result)
	bundle := result.Resource.Artifact
	if bundle.ID == "" {
		t.Fatalf("backup job result names no artifact: %s", backupJob.Resource.Result)
	}

	f.must(f.owner, "installation.maintenance.enter", "restore2-maintenance", map[string]any{"scope": f.scope(), "expected_version": 2})
	restore := f.must(f.owner, "installation.restore", "restore2-restore", map[string]any{
		"scope": f.scope(), "backup_artifact": map[string]any{"id": bundle.ID, "digest": bundle.Digest}, "expected_version": 3,
	})
	var restoreJob struct {
		Resource struct {
			ID contract.ID `json:"id"`
		} `json:"resource"`
	}
	decode(t, restore.Data, &restoreJob)

	// The real controller claims the accepted restore job (_execution.job.
	// pending finds it "running", restoreWork/runRestore drives it to
	// StageCandidate) and records the refusal as the job's own durable
	// disposition -- never guessed at, never a fabricated success.
	waitFor(t, 15*time.Second, "the controller to record the restore job failed with prerequisite_missing", func() bool {
		res := f.must(f.owner, "installation.job.get", "", map[string]any{"scope": f.scope(), "id": restoreJob.Resource.ID})
		var out struct {
			Resource struct {
				State        string `json:"state"`
				Requirements []struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"requirements"`
			} `json:"resource"`
		}
		decode(t, res.Data, &out)
		if out.Resource.State != "failed" {
			return false
		}
		for _, r := range out.Resource.Requirements {
			if r.Code == contract.CodePrerequisiteMissing {
				return true
			}
		}
		return false
	})

	// The installation stays safely paused in maintenance -- never silently
	// resumed, never corrupted -- because StageCandidate refused before any
	// database file was ever touched.
	status := f.must(f.owner, "installation.status", "", map[string]any{"scope": f.scope()})
	var st struct {
		Resource struct {
			Paused      bool `json:"paused"`
			Maintenance bool `json:"maintenance"`
		} `json:"resource"`
	}
	decode(t, status.Data, &st)
	if !st.Resource.Paused || !st.Resource.Maintenance {
		t.Fatalf("installation after the honestly-refused restore %s, want paused in maintenance (never silently resumed)", status.Data)
	}
	// The owner can still inspect the installation over the same real
	// application while it stays paused -- the honest refusal did not
	// wedge the whole controller, only the restore protocol.
	f.must(f.owner, "usage.get", "", map[string]any{"scope": f.scope()})
}
