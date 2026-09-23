package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/controller"
	"github.com/zatiti/zatiti/internal/storage"
)

// The end-to-end proof of P50: a real installation.backup, real domain
// mutation afterwards, a real installation.restore back to that backup, and
// a real controller driving the swap through the PRODUCTION
// restoreLifecycle -- the same value serve.go attaches, resolving a real
// published encrypted bundle and a real sealed overlay through
// internal/installation's own decrypt pipeline, and merging through each
// owner's own registered merge operation.
//
// Nothing in this file substitutes a double for the behavior under test.
// The only fixtures are the ones every cmd/zatiti test uses: a temporary
// state directory and a headless credential backend.

// restoreFixture is one bootstrapped installation with an authenticated
// owner, ready to drive public operations through the real application.
type restoreFixture struct {
	t     *testing.T
	ctx   context.Context
	cfg   config
	h     *installationHandle
	inst  contract.ID
	owner contract.Actor
}

func newRestoreFixture(t *testing.T) *restoreFixture {
	t.Helper()
	ctx := context.Background()
	cfg := serveConfig(t)
	h := openTestInstallation(t, cfg)
	inst := bootstrapInstallation(t, h)

	profile, err := handOverOwnerCredential(ctx, h)
	if err != nil {
		t.Fatalf("hand over owner credential: %v", err)
	}
	credential, err := (profileCredential{store: profileStore{dir: cfg.profilesDir()}, name: profile}).Credential(ctx)
	if err != nil {
		t.Fatalf("read owner credential: %v", err)
	}
	owner, err := h.app.Authenticate(ctx, credential)
	if err != nil {
		t.Fatalf("authenticate owner: %v", err)
	}
	return &restoreFixture{t: t, ctx: ctx, cfg: cfg, h: h, inst: inst, owner: owner}
}

func (f *restoreFixture) scope() map[string]any {
	return map[string]any{"installation_id": string(f.inst)}
}

// invoke drives one public operation as the owner through the real
// application boundary.
func (f *restoreFixture) invoke(op string, input map[string]any) contract.Result {
	f.t.Helper()
	res, err := f.h.app.Invoke(f.ctx, f.owner, op, contract.Request{
		Schema: contract.SchemaRequest, SubmissionKey: op + "-" + string(contract.NewID()),
		Input: mustJSON(f.t, input),
	})
	if err != nil {
		f.t.Fatalf("%s: %v", op, err)
	}
	return res
}

// resourceID decodes the {"resource": {"id":…, "version":…}} envelope every
// identity operation returns.
func (f *restoreFixture) resourceID(res contract.Result) (contract.ID, int64) {
	f.t.Helper()
	var out struct {
		Resource struct {
			ID      contract.ID `json:"id"`
			Version int64       `json:"version"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(res.Data, &out); err != nil {
		f.t.Fatalf("decode resource: %v (%s)", err, res.Data)
	}
	return out.Resource.ID, out.Resource.Version
}

// count runs one aggregate query against the live database.
func (f *restoreFixture) count(query string, args ...any) int64 {
	f.t.Helper()
	var n int64
	actor := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindService}
	err := f.h.db.Read(f.ctx, actor, contract.Scope{InstallationID: f.inst}, func(u contract.Unit) error {
		return u.QueryRowContext(f.ctx, query, args...).Scan(&n)
	})
	if err != nil {
		f.t.Fatalf("query %q: %v", query, err)
	}
	return n
}

// seedGrant commits one live grant row for principalID, the exact shape an
// approved grant.create leaves behind. See its call site for why the
// creation is seeded rather than driven.
func (f *restoreFixture) seedGrant(principalID contract.ID) contract.ID {
	f.t.Helper()
	id := contract.NewID()
	scopeJSON := string(mustJSON(f.t, contract.Scope{InstallationID: f.inst}))
	now := time.Now().UTC().Format(time.RFC3339Nano)
	actor := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindService}
	err := f.h.db.Write(f.ctx, actor, contract.Scope{InstallationID: f.inst}, func(u contract.Unit) error {
		_, err := u.ExecContext(f.ctx, `
			INSERT INTO identity_grants
				(id, version, principal_id, installation_id, scope_json, capabilities_json,
				 destinations_json, denied, revoked, created_at, updated_at)
			VALUES (?, 1, ?, ?, ?, '["task.create"]', '[]', 0, 0, ?, ?)`,
			string(id), string(principalID), string(f.inst), scopeJSON, now, now)
		return err
	})
	if err != nil {
		f.t.Fatalf("seed grant: %v", err)
	}
	return id
}

// runControllerUntilHandoff attaches the PRODUCTION restore lifecycle and
// runs one controller lifetime, which ends with controller.ErrRestoreHandoff
// once it has performed the swap, merged the overlay and resumed storage.
func (f *restoreFixture) runControllerUntilHandoff() {
	f.t.Helper()
	identity, err := resolveControllerIdentity(f.ctx, f.h, f.inst)
	if err != nil {
		f.t.Fatalf("resolve controller identity: %v", err)
	}
	ctl, err := controller.New(
		controller.Config{StateDir: f.cfg.StateDir, TickInterval: 20 * time.Millisecond},
		f.h.app, f.h.db, f.h.own, map[string]contract.Adapter{}, f.h.clock)
	if err != nil {
		f.t.Fatalf("controller.New: %v", err)
	}
	if err := ctl.Attach(controller.Collaborators{
		Identity:         identity,
		Blobs:            f.h.plat.Blobs(),
		Jobs:             buildJobRunners(f.h.jobRunners),
		Operator:         f.h.app,
		RestoreLifecycle: newRestoreLifecycle(f.h, f.inst),
	}); err != nil {
		f.t.Fatalf("controller.Attach: %v", err)
	}
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- ctl.Run(ctx) }()
	select {
	case err := <-done:
		if !errors.Is(err, controller.ErrRestoreHandoff) {
			f.t.Fatalf("controller ended with %v, want controller.ErrRestoreHandoff", err)
		}
	case <-time.After(60 * time.Second):
		cancel()
		<-done
		f.t.Fatal("the controller never completed the restore handoff")
	}
}

// TestRestoreRewindsDomainStateWhileRevokedCredentialAndGrantStaySuppressed
// is P50's first required behavioral test, end to end through production
// code only.
//
// A real installation.backup is taken. After it, the installation mutates:
// a new principal is created (state that must be rewound away) and a
// credential and a grant that existed in the backup are revoked (state that
// must survive the rewind). A real installation.restore to that backup
// drives an actual controller swap. Afterwards, the reassembled
// installation reads the backed-up state -- the post-backup principal is
// gone -- while both post-backup revocations remain in force.
//
// That second half is the behavior internal/installation/restore_test.go's
// TestRestoreOverlayPreservesPostBackupUnknownEffectAcrossRewind doc comment
// named as unexercisable ("a credential/grant revocation cannot be
// exercised here because no owner port installation may call enumerates
// current revocations"). It is exercisable now because the whole chain
// exists: _identity.revocations captures them, installation seals them into
// the recovery overlay, the controller journals that overlay before the
// swap, and _identity.restore.merge re-applies them after it.
func TestRestoreRewindsDomainStateWhileRevokedCredentialAndGrantStaySuppressed(t *testing.T) {
	f := newRestoreFixture(t)

	// --- Pre-backup state: a principal with a credential and a grant. ---
	beforePrincipalID, _ := f.resourceID(f.invoke("principal.create", map[string]any{
		"scope": f.scope(),
		"definition": map[string]any{
			"kind": "worker", "name": "Before Backup", "scope": f.scope(), "revoked": false,
		},
	}))
	// The trusted helper custodies the credential bytes first; identity only
	// ever sees the opaque reference the store hands back.
	storeRef, err := f.h.secrets.Put(f.ctx, "installation/restore-subject", []byte("zt-restore-subject-token"))
	if err != nil {
		t.Fatalf("custody the subject credential: %v", err)
	}
	credentialID, credentialVersion := f.resourceID(f.invoke("credential.provision", map[string]any{
		"scope": f.scope(), "principal_id": string(beforePrincipalID),
		"store_ref": storeRef,
	}))
	// grant.create is a default review class (internal/policy's
	// defaultReviewRequired: permission expansion needs an eligible owner's
	// decision), and staging a review is not what this test is about, so the
	// pre-backup grant row is seeded directly -- exactly the row an approved
	// grant.create commits. The revocation below, which IS the behavior under
	// test, goes through the real public grant.revoke, which is deliberately
	// not review-gated because it only ever restricts.
	grantID := f.seedGrant(beforePrincipalID)
	grantVersion := int64(1)

	// --- The backup, of a paused installation, as the protocol requires. ---
	var status struct {
		Resource struct {
			Version int64 `json:"version"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(f.invoke("installation.pause", map[string]any{
		"scope": f.scope(), "expected_version": 1,
	}).Data, &status); err != nil {
		t.Fatalf("decode pause: %v", err)
	}
	backup := f.invoke("installation.backup", map[string]any{"scope": f.scope()})
	var backupJob struct {
		Resource struct {
			ID     contract.ID     `json:"id"`
			State  string          `json:"state"`
			Result json.RawMessage `json:"result"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(backup.Data, &backupJob); err != nil {
		t.Fatalf("decode backup: %v", err)
	}
	if backupJob.Resource.State != "succeeded" {
		t.Fatalf("backup job state = %q, want succeeded", backupJob.Resource.State)
	}
	var backupResult struct {
		Resource struct {
			Artifact struct {
				ID     contract.ID     `json:"id"`
				Digest contract.Digest `json:"digest"`
			} `json:"artifact"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(backupJob.Resource.Result, &backupResult); err != nil {
		t.Fatalf("decode backup result: %v", err)
	}
	artifact := backupResult.Resource.Artifact
	if artifact.ID == "" || artifact.Digest == "" {
		t.Fatalf("the backup job names no artifact: %s", backupJob.Resource.Result)
	}
	if err := json.Unmarshal(f.invoke("installation.resume", map[string]any{
		"scope": f.scope(), "expected_version": status.Resource.Version,
	}).Data, &status); err != nil {
		t.Fatalf("decode resume: %v", err)
	}

	// --- Post-backup mutation: one new principal, two revocations. ---
	afterPrincipalID, _ := f.resourceID(f.invoke("principal.create", map[string]any{
		"scope": f.scope(),
		"definition": map[string]any{
			"kind": "worker", "name": "After Backup", "scope": f.scope(), "revoked": false,
		},
	}))
	f.invoke("credential.revoke", map[string]any{
		"scope": f.scope(), "id": string(credentialID), "expected_version": credentialVersion,
	})
	f.invoke("grant.revoke", map[string]any{
		"scope": f.scope(), "id": string(grantID), "expected_version": grantVersion,
	})
	if got := f.count(`SELECT revoked FROM identity_credentials WHERE id = ?`, string(credentialID)); got != 1 {
		t.Fatalf("the credential is not revoked before the restore: %d", got)
	}
	if got := f.count(`SELECT revoked FROM identity_grants WHERE id = ?`, string(grantID)); got != 1 {
		t.Fatalf("the grant is not revoked before the restore: %d", got)
	}

	// --- The restore, of a quiesced installation, as the protocol requires. ---
	if err := json.Unmarshal(f.invoke("installation.maintenance.enter", map[string]any{
		"scope": f.scope(), "expected_version": status.Resource.Version,
	}).Data, &status); err != nil {
		t.Fatalf("decode maintenance.enter: %v", err)
	}
	restore := f.invoke("installation.restore", map[string]any{
		"scope": f.scope(), "expected_version": status.Resource.Version,
		"backup_artifact": map[string]any{"id": string(artifact.ID), "digest": string(artifact.Digest)},
	})
	var restoreJob struct {
		Resource struct {
			ID    contract.ID `json:"id"`
			State string      `json:"state"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(restore.Data, &restoreJob); err != nil {
		t.Fatalf("decode restore: %v", err)
	}
	if restoreJob.Resource.State != "running" {
		t.Fatalf("restore job state = %q, want running (paused, awaiting the controller)", restoreJob.Resource.State)
	}

	// --- The controller performs the real swap, merge and resume. ---
	f.runControllerUntilHandoff()
	if err := f.h.reassembleAfterRestoreHandoff(f.ctx); err != nil {
		t.Fatalf("reassembleAfterRestoreHandoff: %v", err)
	}

	// The rewind really happened: the post-backup principal is gone and the
	// pre-backup one is back.
	if got := f.count(`SELECT COUNT(*) FROM identity_principals WHERE id = ?`, string(afterPrincipalID)); got != 0 {
		t.Fatalf("the principal created after the backup survived the rewind (%d rows); the database was not restored", got)
	}
	if got := f.count(`SELECT COUNT(*) FROM identity_principals WHERE id = ?`, string(beforePrincipalID)); got != 1 {
		t.Fatalf("the principal that existed in the backup is missing after the restore (%d rows)", got)
	}

	// The overlay merge really happened: both post-backup revocations are
	// still in force, even though the rows the rewind restored recorded them
	// as live.
	if got := f.count(`SELECT revoked FROM identity_credentials WHERE id = ?`, string(credentialID)); got != 1 {
		t.Fatal("the credential revoked after the backup came back to life across the restore")
	}
	if got := f.count(`SELECT revoked FROM identity_grants WHERE id = ?`, string(grantID)); got != 1 {
		t.Fatal("the grant revoked after the backup came back to life across the restore")
	}
	// The re-applied revocations are recorded in identity's own immutable
	// ledger, exactly once each, so the evidence survives with them.
	if got := f.count(
		`SELECT COUNT(*) FROM identity_revocations WHERE entity_kind = 'credential' AND entity_id = ?`,
		string(credentialID)); got != 1 {
		t.Fatalf("credential revocation ledger holds %d rows after the merge, want exactly 1", got)
	}
	if got := f.count(
		`SELECT COUNT(*) FROM identity_revocations WHERE entity_kind = 'grant' AND entity_id = ?`,
		string(grantID)); got != 1 {
		t.Fatalf("grant revocation ledger holds %d rows after the merge, want exactly 1", got)
	}

	// A structural consequence of the completed protocol, pinned here so it
	// cannot change silently: installation.restore's own job ledger lives in
	// the database the restore replaces, and the backup necessarily predates
	// the restore that selected it, so after a genuine rewind the restore
	// job is gone along with everything else created after the backup. The
	// final _installation.restore.record disposition therefore has no row to
	// write against on the resumed lifetime. Nothing above depends on that
	// record -- the swap, the merge and the storage resume are all durable
	// before it is attempted -- but any future work that expects a
	// "succeeded" restore job to survive its own restore needs to reckon
	// with this.
	if got := f.count(`SELECT COUNT(*) FROM installation_jobs WHERE id = ?`, string(restoreJob.Resource.ID)); got != 0 {
		t.Fatalf("the restore job survived its own rewind (%d rows); the backup cannot contain the restore that used it", got)
	}

	// Storage is genuinely resumed: the restore completed rather than
	// leaving the installation permanently gated.
	restorable, ok := f.h.db.(storage.Restorable)
	if !ok {
		t.Fatal("the reassembled database does not implement storage.Restorable")
	}
	paused, err := restorable.RestorePaused(f.ctx)
	if err != nil {
		t.Fatalf("RestorePaused: %v", err)
	}
	if paused {
		t.Fatal("the restore never lifted the storage write gate")
	}
	writeErr := f.h.db.Write(f.ctx, contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindService},
		contract.Scope{InstallationID: f.inst}, func(contract.Unit) error { return nil })
	if writeErr != nil {
		t.Fatalf("an ordinary write against the restored database = %v, want it admitted", writeErr)
	}
}

// TestRestoreOfAnInstallationWithNoObligationsStillCompletes is P50's fourth
// required behavioral test: nothing to merge is not an error. A freshly
// bootstrapped installation has no pending effects, no unresolved memory
// obligations and no revocations, so its recovery overlay carries an empty
// obligation list -- and the swap, merge and resume must still complete
// through the same production path.
func TestRestoreOfAnInstallationWithNoObligationsStillCompletes(t *testing.T) {
	f := newRestoreFixture(t)

	var status struct {
		Resource struct {
			Version int64 `json:"version"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(f.invoke("installation.pause", map[string]any{
		"scope": f.scope(), "expected_version": 1,
	}).Data, &status); err != nil {
		t.Fatalf("decode pause: %v", err)
	}
	var backupJob struct {
		Resource struct {
			Result json.RawMessage `json:"result"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(f.invoke("installation.backup", map[string]any{"scope": f.scope()}).Data, &backupJob); err != nil {
		t.Fatalf("decode backup: %v", err)
	}
	var backupResult struct {
		Resource struct {
			Artifact struct {
				ID     contract.ID     `json:"id"`
				Digest contract.Digest `json:"digest"`
			} `json:"artifact"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(backupJob.Resource.Result, &backupResult); err != nil {
		t.Fatalf("decode backup result: %v", err)
	}
	if err := json.Unmarshal(f.invoke("installation.maintenance.enter", map[string]any{
		"scope": f.scope(), "expected_version": status.Resource.Version,
	}).Data, &status); err != nil {
		t.Fatalf("decode maintenance.enter: %v", err)
	}
	artifact := backupResult.Resource.Artifact
	f.invoke("installation.restore", map[string]any{
		"scope": f.scope(), "expected_version": status.Resource.Version,
		"backup_artifact": map[string]any{"id": string(artifact.ID), "digest": string(artifact.Digest)},
	})

	f.runControllerUntilHandoff()
	if err := f.h.reassembleAfterRestoreHandoff(f.ctx); err != nil {
		t.Fatalf("reassembleAfterRestoreHandoff: %v", err)
	}
	restorable, ok := f.h.db.(storage.Restorable)
	if !ok {
		t.Fatal("the reassembled database does not implement storage.Restorable")
	}
	paused, err := restorable.RestorePaused(f.ctx)
	if err != nil {
		t.Fatalf("RestorePaused: %v", err)
	}
	if paused {
		t.Fatal("a restore with nothing to merge left the storage write gate down")
	}
	// The installation identity survived: this was a restore, not a wipe.
	if got := f.count(`SELECT COUNT(*) FROM identity_principals WHERE kind = 'service' AND name = 'controller'`); got != 1 {
		t.Fatalf("the restored image holds %d controller principals, want exactly 1", got)
	}
}

// TestMergeOverlayRefusesANonControllerActorEndToEnd is P50's third
// required behavioral test on the production implementation itself: the
// merge path bypasses application dispatch's centralized caller check, so
// the production capability re-establishes it -- against identity's own
// record of which principal the controller is, resolved inside the same
// transaction. It runs against a real sealed overlay, not a fabricated one.
func TestMergeOverlayRefusesANonControllerActorEndToEnd(t *testing.T) {
	f := newRestoreFixture(t)

	var status struct {
		Resource struct {
			Version int64 `json:"version"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(f.invoke("installation.pause", map[string]any{
		"scope": f.scope(), "expected_version": 1,
	}).Data, &status); err != nil {
		t.Fatalf("decode pause: %v", err)
	}
	var backupJob struct {
		Resource struct {
			Result json.RawMessage `json:"result"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(f.invoke("installation.backup", map[string]any{"scope": f.scope()}).Data, &backupJob); err != nil {
		t.Fatalf("decode backup: %v", err)
	}
	var backupResult struct {
		Resource struct {
			Artifact struct {
				ID     contract.ID     `json:"id"`
				Digest contract.Digest `json:"digest"`
			} `json:"artifact"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(backupJob.Resource.Result, &backupResult); err != nil {
		t.Fatalf("decode backup result: %v", err)
	}
	if err := json.Unmarshal(f.invoke("installation.maintenance.enter", map[string]any{
		"scope": f.scope(), "expected_version": status.Resource.Version,
	}).Data, &status); err != nil {
		t.Fatalf("decode maintenance.enter: %v", err)
	}
	artifact := backupResult.Resource.Artifact
	restore := f.invoke("installation.restore", map[string]any{
		"scope": f.scope(), "expected_version": status.Resource.Version,
		"backup_artifact": map[string]any{"id": string(artifact.ID), "digest": string(artifact.Digest)},
	})
	var restoreJob struct {
		Resource struct {
			ID contract.ID `json:"id"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(restore.Data, &restoreJob); err != nil {
		t.Fatalf("decode restore: %v", err)
	}

	// Resolve the real published overlay reference the controller would
	// journal, through the real internal operation.
	controllerActor, err := resolveControllerIdentity(f.ctx, f.h, f.inst)
	if err != nil {
		t.Fatalf("resolve controller identity: %v", err)
	}
	overlayRes, err := f.h.app.Internal(f.ctx, controllerActor, contract.Scope{InstallationID: f.inst},
		contract.Invocation{
			Operation: "_installation.restore.overlay", Version: 1,
			Input: mustJSON(t, map[string]any{"job_id": string(restoreJob.Resource.ID)}),
		})
	if err != nil {
		t.Fatalf("_installation.restore.overlay: %v", err)
	}
	var overlay struct {
		Artifact struct {
			ID     contract.ID     `json:"id"`
			Digest contract.Digest `json:"digest"`
		} `json:"artifact"`
		Size int64 `json:"size"`
	}
	if err := json.Unmarshal(overlayRes.Data, &overlay); err != nil {
		t.Fatalf("decode overlay reference: %v", err)
	}
	ref := controller.RestoreOverlayRef{
		Artifact: contract.ArtifactRef{ID: overlay.Artifact.ID, Digest: overlay.Artifact.Digest},
		Size:     overlay.Size,
	}

	lifecycle := newRestoreLifecycle(f.h, f.inst)
	restorable, ok := f.h.db.(storage.Restorable)
	if !ok {
		t.Fatal("the test database does not implement storage.Restorable")
	}

	// The owner's own human principal is refused, even though it holds the
	// installation-wide wildcard grant.
	err = restorable.Write(f.ctx, f.owner, contract.Scope{InstallationID: f.inst}, func(u contract.Unit) error {
		return lifecycle.MergeOverlay(f.ctx, u, restoreJob.Resource.ID, ref)
	})
	if faultCode(err) != contract.CodePermissionDenied {
		t.Fatalf("MergeOverlay as the installation owner = %v, want permission_denied", err)
	}

	// A service principal that is not this installation's controller is
	// refused too: the check is identity, not merely kind.
	impostor := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindService}
	err = restorable.Write(f.ctx, impostor, contract.Scope{InstallationID: f.inst}, func(u contract.Unit) error {
		return lifecycle.MergeOverlay(f.ctx, u, restoreJob.Resource.ID, ref)
	})
	if faultCode(err) != contract.CodePermissionDenied {
		t.Fatalf("MergeOverlay as a non-controller service principal = %v, want permission_denied", err)
	}

	// The actor check runs BEFORE the sealed overlay is ever opened: given a
	// reference naming bytes that do not exist, a wrong actor still gets
	// permission_denied rather than the artifact fault that resolving the
	// reference would produce. A wrong caller therefore never causes a
	// secret-bearing bundle to be decrypted at all. This is the one
	// assertion that isolates the capability's own centralized check from
	// the per-owner checks that would otherwise catch the same actor later.
	absent := controller.RestoreOverlayRef{
		Artifact: contract.ArtifactRef{
			ID:     contract.NewID(),
			Digest: contract.Digest("0000000000000000000000000000000000000000000000000000000000000001"),
		},
		Size: 512,
	}
	err = restorable.Write(f.ctx, impostor, contract.Scope{InstallationID: f.inst}, func(u contract.Unit) error {
		return lifecycle.MergeOverlay(f.ctx, u, restoreJob.Resource.ID, absent)
	})
	if faultCode(err) != contract.CodePermissionDenied {
		t.Fatalf("MergeOverlay refused an unresolvable overlay before refusing the actor: %v", err)
	}

	// The real controller principal is accepted against the same real
	// overlay, so the refusals above are about the actor and nothing else.
	// Repeating it proves the production merge is safe to replay, which is
	// exactly what a crash between storage's overlay write and its resume
	// forces.
	for attempt := 1; attempt <= 2; attempt++ {
		if err := restorable.Write(f.ctx, controllerActor, contract.Scope{InstallationID: f.inst},
			func(u contract.Unit) error {
				return lifecycle.MergeOverlay(f.ctx, u, restoreJob.Resource.ID, ref)
			}); err != nil {
			t.Fatalf("MergeOverlay attempt %d as the controller principal: %v", attempt, err)
		}
	}
}

// TestStageCandidateResolvesTheRealPublishedBackupBundle proves the other
// half of P50's third required behavior: the production StageCandidate is
// exercised against a real published, encrypted backup bundle, resolving it
// through the restore job's own original input exactly as the controller
// threads it.
func TestStageCandidateResolvesTheRealPublishedBackupBundle(t *testing.T) {
	f := newRestoreFixture(t)

	var status struct {
		Resource struct {
			Version int64 `json:"version"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(f.invoke("installation.pause", map[string]any{
		"scope": f.scope(), "expected_version": 1,
	}).Data, &status); err != nil {
		t.Fatalf("decode pause: %v", err)
	}
	var backupJob struct {
		Resource struct {
			Result json.RawMessage `json:"result"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(f.invoke("installation.backup", map[string]any{"scope": f.scope()}).Data, &backupJob); err != nil {
		t.Fatalf("decode backup: %v", err)
	}
	var backupResult struct {
		Resource struct {
			Artifact struct {
				ID     contract.ID     `json:"id"`
				Digest contract.Digest `json:"digest"`
			} `json:"artifact"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(backupJob.Resource.Result, &backupResult); err != nil {
		t.Fatalf("decode backup result: %v", err)
	}
	artifact := backupResult.Resource.Artifact

	lifecycle := newRestoreLifecycle(f.h, f.inst)
	jobInput := mustJSON(t, map[string]any{
		"scope":            f.scope(),
		"expected_version": status.Resource.Version,
		"backup_artifact":  map[string]any{"id": string(artifact.ID), "digest": string(artifact.Digest)},
	})
	candidate, err := lifecycle.StageCandidate(f.ctx, contract.NewID(), jobInput, t.TempDir())
	if err != nil {
		t.Fatalf("StageCandidate against a real published bundle: %v", err)
	}
	if candidate.Path == "" || candidate.DatabaseDigest == "" {
		t.Fatalf("StageCandidate returned %+v, want a staged image and its digest", candidate)
	}
	if candidate.InstallationID != f.inst {
		t.Fatalf("staged candidate is bound to %s, want %s", candidate.InstallationID, f.inst)
	}
	// The staged file is a real database: it opens, and it holds this
	// installation's own bootstrap event.
	staged, err := storage.Open(f.ctx, storage.Config{Path: candidate.Path})
	if err != nil {
		t.Fatalf("the staged candidate does not open as a database: %v", err)
	}
	defer func() { _ = staged.Close() }()
	events, err := staged.Events(f.ctx, 0, 1)
	if err != nil || len(events) == 0 {
		t.Fatalf("the staged candidate carries no events: %v (%d)", err, len(events))
	}
	if events[0].Scope.InstallationID != f.inst {
		t.Fatalf("the staged candidate belongs to installation %s, want %s",
			events[0].Scope.InstallationID, f.inst)
	}
	// The schema claim is the destination's own applied migrations, which is
	// what PrepareRestore compares the staged image's real ledger against.
	if len(candidate.SchemaVersions) == 0 {
		t.Fatal("StageCandidate produced no schema claim; PrepareRestore would have nothing to check")
	}

	// A job input naming no backup artifact is refused, not guessed at.
	if _, err := lifecycle.StageCandidate(f.ctx, contract.NewID(),
		mustJSON(t, map[string]any{"scope": f.scope()}), t.TempDir()); faultCode(err) != contract.CodeInvalidInput {
		t.Fatalf("StageCandidate without a backup artifact = %v, want invalid_input", err)
	}
}
