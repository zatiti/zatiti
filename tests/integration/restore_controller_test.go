package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/controller"
)

// This file proves P46's honest restore ceiling through a REAL running
// controller, extending TestBackupRestoresActualBytes (defects_test.go),
// which already proves backup and restore-request admission for real but
// never attaches a controller, so it stops at "restore job accepted,
// awaiting the controller" -- exactly where installation.restore's own job
// state ("running or pending while it awaits the controller") says it
// should stop without one.
//
// Per this card's own briefing and cmd/zatiti/restore.go's package doc
// comment: the actual backup-to-restore DATA MERGE never completes
// anywhere on this tree today. No operation, public or internal, hands
// entrypoint assembly a pending restore job's original backup-artifact
// reference, and no owner-defined merge operation exists in internal/
// effects, internal/identity or internal/memory to fold a RecoveryOverlay's
// obligations back into their own tables. cmd/zatiti's own restoreLifecycle{}
// (restore.go) is production's answer to that gap: it fails closed with two
// specific prerequisite_missing faults rather than fabricating success or
// silently resuming a partially-restored installation. This is not a bug
// this package could route around -- it is a genuine, already-known,
// honestly documented production limitation -- so this file reproduces
// restoreLifecycle{} verbatim (the identical fault text production ships,
// cited to the same source) and proves, through a real controller, that a
// restore job it observes is recorded failed with exactly that
// prerequisite_missing disposition and the installation stays safely
// paused rather than silently resumed or corrupted.

// restoreLifecycle mirrors cmd/zatiti/restore.go's restoreLifecycle{}
// verbatim. Entrypoint assembly alone has direct Go access to internal/
// installation's private backup-bundle/key-resolution/overlay code (see
// that file's own package doc comment for the full account); neither
// cmd/zatiti nor this test package can duplicate it without inventing a
// second, unsynced implementation of secret-bearing AEAD framing, exactly
// what this remediation plan's "never invent a seam" rule forbids. Both
// methods fail closed with the identical fault text cmd/zatiti ships in
// production, so this test proves the real documented fallback behavior,
// not a weaker stand-in for it.
type restoreLifecycle struct{}

var _ controller.RestoreLifecycle = restoreLifecycle{}

// StageCandidate implements controller.RestoreLifecycle. See this file's
// doc comment for why it fails closed.
func (restoreLifecycle) StageCandidate(_ context.Context, restoreJobID contract.ID, _ string) (controller.RestoreCandidate, error) {
	return controller.RestoreCandidate{}, &contract.Fault{
		Code: contract.CodePrerequisiteMissing,
		Message: "no operation exposes restore job " + string(restoreJobID) +
			"'s backup artifact reference to entrypoint assembly, and internal/installation's " +
			"bundle-decrypt/key-resolution code is unexported; StageCandidate cannot resolve a " +
			"decrypted candidate database image (see cmd/zatiti/restore.go and " +
			"docs/implementation-remediation/assignments/P33.md's handoff)",
	}
}

// MergeOverlay implements controller.RestoreLifecycle. See this file's doc
// comment for why it fails closed. Unreachable in this test:
// StageCandidate's own failure means performSwap never calls
// storage.Restorable.PrepareRestore/CommitRestore, so MergeOverlay is never
// invoked -- the failure is safe, not merely honest.
func (restoreLifecycle) MergeOverlay(_ context.Context, _ contract.Unit, restoreJobID contract.ID) error {
	return &contract.Fault{
		Code: contract.CodePrerequisiteMissing,
		Message: "no owner-defined merge operation exists in internal/effects, internal/identity or " +
			"internal/memory to fold restore job " + string(restoreJobID) + "'s recovery overlay into " +
			"their own tables, and credential/grant revocation obligations are never captured into the " +
			"overlay in the first place; MergeOverlay cannot safely resume this restore (see " +
			"cmd/zatiti/restore.go and docs/implementation-remediation/assignments/P33.md's handoff)",
	}
}

// TestControllerHonestlyRefusesRestoreMergeAndStaysSafelyPaused (P46 item 6,
// the CRITICAL CONTEXT restore ceiling; Z14.paused_restore): with the real
// restoreLifecycle{} attached (production's own fail-closed implementation,
// reproduced verbatim above), a real controller driving a real accepted
// installation.restore job records it failed with prerequisite_missing at
// the documented StageCandidate boundary -- never guessed at, never
// fabricated into a resumed installation -- and the installation stays
// paused in maintenance. No database file is ever touched: StageCandidate
// fails before performSwap ever calls storage.Restorable.PrepareRestore/
// CommitRestore (internal/controller/restore.go), so the failure is safe,
// not merely honest.
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
		RestoreLifecycle: restoreLifecycle{},
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
	// StageCandidate) and it fails closed exactly as production's own
	// restoreLifecycle{} does -- never guessed at, never a fabricated
	// success.
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
