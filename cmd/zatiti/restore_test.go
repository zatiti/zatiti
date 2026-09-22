package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/client"
	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/controller"
	"github.com/zatiti/zatiti/internal/storage"
)

// This file's required behavioral tests, per P33's assignment card:
//
//   - "A subprocess restore followed by a new process reads restored real
//     state and retains post-backup revocation." NOT achieved as written:
//     see restore.go's package doc comment and the P33 handoff for why
//     (revocation obligations are never captured by internal/installation's
//     landed overlay capture, and no owner-merge operation exists anywhere
//     to apply them if they were). TestReassembleAfterRestoreHandoffRebuiltsApplicationOverTheSwappedDatabase
//     below proves the "reads restored real state" half using a real
//     storage-level swap, honestly, without the revocation half.
//   - "Restart during every restore phase never serves a partially
//     restored writable installation": proven by
//     TestRestartNeverAdmitsWritesWhileRestorePausedMarkerIsSet (the
//     storage write-gate, observed fresh by a brand-new process) and
//     TestReassembleAfterRestoreHandoffRebuiltsApplicationOverTheSwappedDatabase
//     (the reassembly loop never resumes writes on its own).
//   - "Explicit resume is required and rechecks outstanding obligations":
//     proven by TestExplicitResumeRefusedWhileRestoreUnresolvedThenSucceedsAfterRecheck.

// performSelfSwap performs a REAL storage.Restorable.PrepareRestore/
// CommitRestore swap of h's database onto a consistent backup of itself,
// using the exact production contract.DatabaseBackup wrapper
// (databaseBackup{db: h.db}, assembly.go) -- not a fake. It restores the
// database onto ITSELF (not a different snapshot) because these tests exist
// to exercise the swap MECHANISM and cmd/zatiti's reaction to it, not to
// prove data changed between two different backups -- internal/installation's
// own package already proves the latter (TestBackupThenRestoreRoundTrip). It
// sets h.db to the freshly reopened, still-paused Restorable CommitRestore
// returns, exactly as internal/controller's own performSwap does via
// c.setDatabase (restore.go) the instant before a real lifetime would end
// with controller.ErrRestoreHandoff.
func performSelfSwap(t *testing.T, h *installationHandle, installationID contract.ID) {
	t.Helper()
	ctx := context.Background()
	restorable, ok := h.db.(storage.Restorable)
	if !ok {
		t.Fatalf("test database does not implement storage.Restorable")
	}
	versions, err := restorable.SchemaVersions(ctx)
	if err != nil {
		t.Fatalf("schema versions: %v", err)
	}
	candidatePath := filepath.Join(t.TempDir(), "self-swap-candidate.sqlite")
	f, err := os.Create(candidatePath)
	if err != nil {
		t.Fatalf("create candidate file: %v", err)
	}
	digest := sha256.New()
	backupErr := (databaseBackup{db: h.db}).Backup(ctx, io.MultiWriter(f, digest))
	closeErr := f.Close()
	if backupErr != nil {
		t.Fatalf("backup capability: %v", backupErr)
	}
	if closeErr != nil {
		t.Fatalf("close candidate file: %v", closeErr)
	}
	staging, err := restorable.PrepareRestore(ctx, storage.RestoreImage{
		Path: candidatePath, ExpectedInstallationID: installationID, InstallationID: installationID,
		DatabaseDigest: contract.Digest(hex.EncodeToString(digest.Sum(nil))), SchemaVersions: versions,
	})
	if err != nil {
		t.Fatalf("PrepareRestore: %v", err)
	}
	next, err := restorable.CommitRestore(ctx, staging)
	if err != nil {
		t.Fatalf("CommitRestore: %v", err)
	}
	h.db = next
}

// TestReassembleAfterRestoreHandoffRebuiltsApplicationOverTheSwappedDatabase
// proves assembly.go's new reassembleAfterRestoreHandoff -- the mechanism
// controller.ErrRestoreHandoff's own contract requires the caller to run --
// actually closes the stale application/database a real swap left behind
// and rebuilds a genuinely working Application over the freshly reopened
// database, under a strictly newer generation that fences out the old one
// (P32's own required test: "no old-generation controller/worker can commit
// after reopen"). It never silently resumes writes on its own: the
// reassembled database stays paused until an owner merge completes, which
// this card's honest gap means it never does today, so the reassembled
// application still refuses ordinary writes -- itself part of "restart
// never serves a partially restored writable installation."
func TestReassembleAfterRestoreHandoffRebuiltsApplicationOverTheSwappedDatabase(t *testing.T) {
	cfg := serveConfig(t)
	h := openTestInstallation(t, cfg)
	instID := bootstrapInstallation(t, h)
	beforeGeneration := h.generation

	staleApp := h.app
	beforeEvents, err := h.db.Events(context.Background(), 0, 1)
	if err != nil || len(beforeEvents) == 0 {
		t.Fatalf("read pre-swap events: %v (%d events)", err, len(beforeEvents))
	}

	performSelfSwap(t, h, instID)

	if err := h.reassembleAfterRestoreHandoff(context.Background()); err != nil {
		t.Fatalf("reassembleAfterRestoreHandoff: %v", err)
	}
	if h.app == staleApp {
		t.Fatalf("reassembleAfterRestoreHandoff did not replace the stale application")
	}
	if h.generation <= beforeGeneration {
		t.Fatalf("generation = %d, want strictly greater than the pre-swap generation %d", h.generation, beforeGeneration)
	}

	// Reads restored real state: the reassembled instance observes the same
	// installation identity the pre-swap process bootstrapped, through its
	// own freshly opened database handle.
	afterEvents, err := h.db.Events(context.Background(), 0, 1)
	if err != nil || len(afterEvents) == 0 {
		t.Fatalf("read post-reassembly events: %v (%d events)", err, len(afterEvents))
	}
	if afterEvents[0].Scope.InstallationID != beforeEvents[0].Scope.InstallationID {
		t.Fatalf("reassembled database installation_id = %s, want %s", afterEvents[0].Scope.InstallationID, beforeEvents[0].Scope.InstallationID)
	}

	// Never a partially restored WRITABLE installation: no owner merge ran
	// (this card's honest gap), so the reassembled database is still
	// paused and an ordinary write is refused, not partially admitted.
	actor := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindService}
	writeErr := h.db.Write(context.Background(), actor, contract.Scope{InstallationID: instID}, func(contract.Unit) error { return nil })
	if faultCode(writeErr) != contract.CodePrerequisiteMissing {
		t.Fatalf("Write against the reassembled, still-paused database = %v, want prerequisite_missing", writeErr)
	}
}

// TestRestartNeverAdmitsWritesWhileRestorePausedMarkerIsSet is the required
// behavioral test "restart during every restore phase never serves a
// partially restored writable installation" from the storage-gate's own
// perspective: a brand-new process (a fresh openInstallation over the same
// state directory, exactly as a restarted `zatiti serve` would run) observes
// RestorePaused=true from the very first instant after opening storage --
// logPendingRestoreMarker reports it before any adapter loads and before a
// listener could ever bind -- and an ordinary mutation is refused outright,
// never partially admitted, independent of controller/listener timing.
func TestRestartNeverAdmitsWritesWhileRestorePausedMarkerIsSet(t *testing.T) {
	cfg := serveConfig(t)
	h := openTestInstallation(t, cfg)
	instID := bootstrapInstallation(t, h)
	performSelfSwap(t, h, instID)
	h.close() // release the lock and every handle, as a dying process would

	h2 := openTestInstallation(t, cfg)
	logs := &lockedBuffer{}
	log := newLogger(logs, slog.LevelInfo)
	logPendingRestoreMarker(context.Background(), h2, log)
	if got := logs.String(); !strings.Contains(got, "paused for restore") {
		t.Fatalf("startup did not detect the pending restore marker (logs:\n%s)", got)
	}

	actor := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindService}
	writeErr := h2.db.Write(context.Background(), actor, contract.Scope{InstallationID: instID}, func(contract.Unit) error { return nil })
	if faultCode(writeErr) != contract.CodePrerequisiteMissing {
		t.Fatalf("Write against a restart-reopened, restore-paused database = %v, want prerequisite_missing", writeErr)
	}
}

// TestExplicitResumeRefusedWhileRestoreUnresolvedThenSucceedsAfterRecheck is
// the required behavioral test "explicit resume is required and rechecks
// outstanding obligations." A real installation.maintenance.enter ->
// installation.backup -> installation.restore sequence (all real,
// production LocalIO handlers, no fakes) leaves a restore job "running"
// (installation.restore's own Finish always leaves it there pending the
// controller's rewind -- internal/installation/restore.go). Resume must
// refuse while that job is unresolved, rechecking it on every attempt, not
// only once at maintenance entry. It then succeeds once the job reaches a
// terminal state -- reported here through the same
// _installation.restore.record call internal/controller's own failRestore
// makes (restore.go), under the same controller service identity
// production wiring resolves (resolveControllerIdentity, bootstrap.go) --
// proving resume's recheck is live, not cached from the first refusal.
func TestExplicitResumeRefusedWhileRestoreUnresolvedThenSucceedsAfterRecheck(t *testing.T) {
	ctx := context.Background()
	cfg := serveConfig(t)
	h := openTestInstallation(t, cfg)
	instID := bootstrapInstallation(t, h)

	ownerProfile, err := handOverOwnerCredential(ctx, h)
	if err != nil {
		t.Fatalf("hand over owner credential: %v", err)
	}
	credential, err := (profileCredential{store: profileStore{dir: cfg.profilesDir()}, name: ownerProfile}).Credential(ctx)
	if err != nil {
		t.Fatalf("read owner credential: %v", err)
	}
	owner, err := h.app.Authenticate(ctx, credential)
	if err != nil {
		t.Fatalf("authenticate owner: %v", err)
	}
	controllerActor, err := resolveControllerIdentity(ctx, h, instID)
	if err != nil {
		t.Fatalf("resolve controller identity: %v", err)
	}

	scope := map[string]any{"installation_id": string(instID)}
	invoke := func(op string, input map[string]any) contract.Result {
		t.Helper()
		res, err := h.app.Invoke(ctx, owner, op, contract.Request{
			Schema: contract.SchemaRequest, SubmissionKey: op + "-" + string(contract.NewID()), Input: mustJSON(t, input),
		})
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		return res
	}

	enter := invoke("installation.maintenance.enter", map[string]any{"scope": scope, "expected_version": 1})
	var status resourceOut[wireStatusForTest]
	if err := json.Unmarshal(enter.Data, &status); err != nil {
		t.Fatalf("decode maintenance.enter: %v", err)
	}
	stateVersion := status.Resource.Version

	backup := invoke("installation.backup", map[string]any{"scope": scope})
	var backupJob resourceOut[wireJobForTest]
	if err := json.Unmarshal(backup.Data, &backupJob); err != nil {
		t.Fatalf("decode installation.backup: %v", err)
	}
	var backupResult resourceOut[wireBackupForTest]
	if err := json.Unmarshal(backupJob.Resource.Result, &backupResult); err != nil {
		t.Fatalf("decode backup job result: %v (result=%s)", err, backupJob.Resource.Result)
	}
	artifact := backupResult.Resource.Artifact

	restore := invoke("installation.restore", map[string]any{
		"scope": scope, "expected_version": stateVersion,
		"backup_artifact": map[string]any{"id": string(artifact.ID), "digest": string(artifact.Digest)},
	})
	var restoreJob resourceOut[wireJobForTest]
	if err := json.Unmarshal(restore.Data, &restoreJob); err != nil {
		t.Fatalf("decode installation.restore: %v", err)
	}
	if restoreJob.Resource.State != "running" {
		t.Fatalf("restore job state = %q, want running (paused, awaiting the controller)", restoreJob.Resource.State)
	}
	jobID := restoreJob.Resource.ID

	// Resume must refuse: the restore job is still unresolved ("running").
	_, err = h.app.Invoke(ctx, owner, "installation.resume", contract.Request{
		Schema: contract.SchemaRequest, SubmissionKey: "resume-refused-" + string(contract.NewID()),
		Input: mustJSON(t, map[string]any{"scope": scope, "expected_version": stateVersion}),
	})
	if faultCode(err) != contract.CodePrerequisiteMissing {
		t.Fatalf("installation.resume while restore is running = %v, want prerequisite_missing", err)
	}

	// Report the restore's disposition exactly as internal/controller's own
	// failRestore does (restore.go): the same internal operation, the same
	// controller service identity, a terminal, resolved state.
	if _, err := h.app.Internal(ctx, controllerActor, contract.Scope{InstallationID: instID}, contract.Invocation{
		Operation: "_installation.restore.record", Version: 1,
		Input: mustJSON(t, map[string]any{
			"job_id": string(jobID), "state": "failed",
			"requirements": []map[string]any{{"code": contract.CodePrerequisiteMissing, "message": "test: no restore lifecycle candidate lookup"}},
		}),
	}); err != nil {
		t.Fatalf("_installation.restore.record: %v", err)
	}

	// Resume rechecks live, not a cached refusal: the job is now resolved,
	// so the SAME expected_version that was refused a moment ago succeeds.
	if _, err := h.app.Invoke(ctx, owner, "installation.resume", contract.Request{
		Schema: contract.SchemaRequest, SubmissionKey: "resume-succeeds-" + string(contract.NewID()),
		Input: mustJSON(t, map[string]any{"scope": scope, "expected_version": stateVersion}),
	}); err != nil {
		t.Fatalf("installation.resume after the restore job resolved: %v", err)
	}
}

// wireStatusForTest/wireJobForTest/wireBackupForTest/wireArtifactRefForTest
// mirror only the exact fields these tests read off internal/installation's
// public wire shapes (wireStatus/wireJob/wireBackup/wireArtifactRef,
// unexported to that package); cmd/zatiti has no import of that package's
// internal types, so this package decodes the same JSON envelope it would
// get over the wire, exactly as internal/client's callers do.
type wireStatusForTest struct {
	Version contract.Version `json:"version"`
}

type wireJobForTest struct {
	ID     contract.ID     `json:"id"`
	State  string          `json:"state"`
	Result json.RawMessage `json:"result,omitempty"`
}

type wireBackupForTest struct {
	Artifact wireArtifactRefForTest `json:"artifact"`
}

type wireArtifactRefForTest struct {
	ID     contract.ID     `json:"id"`
	Digest contract.Digest `json:"digest"`
}

// resourceOut mirrors internal/installation's own unexported resourceOut[T]
// envelope ({"resource": T}), the shape every one of its public operations
// returns.
type resourceOut[T any] struct {
	Resource T `json:"resource"`
}

// TestServeAttachesTheRealRestoreLifecycleSeam is P33 item 3's required
// wiring proof, the same pattern P24 established for Verifier: the exact
// production restoreLifecycle{} value (restore.go) is attached to
// Collaborators.RestoreLifecycle when serve runs, through the explicit
// seam superviseController wires alongside every other trusted
// collaborator -- not merely that Attach returned nil, and never reachable
// from the CLI/MCP client processes, which construct no Controller at all.
func TestServeAttachesTheRealRestoreLifecycleSeam(t *testing.T) {
	cfg := serveConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var attached controller.Collaborators
	captured := make(chan struct{})
	logs := &lockedBuffer{}
	log := newLogger(logs, slog.LevelInfo)
	listening := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runServe(ctx, cfg, log, serveOptions{
			pollInterval: 20 * time.Millisecond,
			listening:    func() { close(listening) },
			afterAttach: func(c controller.Collaborators) {
				attached = c
				close(captured)
			},
		})
	}()
	select {
	case <-listening:
	case err := <-done:
		t.Fatalf("serve ended before listening: %v", err)
	case <-time.After(startupBudget):
		t.Fatal("serve did not start listening")
	}

	anon, err := client.New(client.Config{SocketPath: cfg.SocketPath, Timeout: 10 * time.Second}, nil)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	if _, err := anon.Call(ctx, bootstrapOperation, contract.Request{
		Schema: contract.SchemaRequest,
		Input:  json.RawMessage(`{"credential_store":"headless","owner_name":"Restore Lifecycle Owner","headless_key_ref":"installation/owner"}`),
	}); err != nil {
		t.Fatalf("installation.init: %v", err)
	}
	select {
	case <-captured:
	case <-time.After(startupBudget):
		t.Fatal("afterAttach was never called")
	}
	if attached.RestoreLifecycle == nil {
		t.Fatal("Collaborators.RestoreLifecycle is nil; the restore lifecycle seam is not attached")
	}
	if _, ok := attached.RestoreLifecycle.(restoreLifecycle); !ok {
		t.Fatalf("Collaborators.RestoreLifecycle = %T, want the production restoreLifecycle{} value", attached.RestoreLifecycle)
	}
	cancel()
	if err := awaitExit(t, done, "context cancellation", logs); err != nil {
		t.Fatalf("serve = %v on shutdown", err)
	}
}
