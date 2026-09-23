package identity

import (
	"context"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Tests for identity's half of the offline restore protocol:
// _identity.revocations (what a paused backup captures) and
// _identity.restore.merge (what re-applies it after a rewind).
//
// Every test here drives the real registered handlers over a real
// storage-backed transaction, through the same Handle dispatch the
// controller's merge path uses. Nothing is stubbed: the rewind itself is
// simulated the only honest way a unit test can, by putting the rows back
// into the state the older backup image actually holds them in (unrevoked)
// and then asking the merge to do its job.

// rewindRevocation puts one row back into the state the older backup image
// holds it in: not revoked. This is exactly what the controller's atomic
// file swap does to every row the backup predates, which is why a
// revocation taken after the backup needs merging back at all.
func (e *testEnv) rewindRevocation(table string, id contract.ID) {
	e.t.Helper()
	ctx := context.Background()
	err := e.db.Write(ctx, bootActor, contract.Scope{InstallationID: e.inst}, func(unit contract.Unit) error {
		_, err := unit.ExecContext(ctx,
			`UPDATE `+table+` SET revoked = 0, version = 1 WHERE id = ?`, string(id))
		return err
	})
	if err != nil {
		e.t.Fatalf("rewind %s %s: %v", table, id, err)
	}
}

func (e *testEnv) countRevocationRows(kind string, id contract.ID) int64 {
	e.t.Helper()
	ctx := context.Background()
	var n int64
	err := e.db.Read(ctx, bootActor, contract.Scope{InstallationID: e.inst}, func(unit contract.Unit) error {
		return unit.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM identity_revocations WHERE entity_kind = ? AND entity_id = ?`,
			kind, string(id)).Scan(&n)
	})
	if err != nil {
		e.t.Fatalf("count revocation ledger rows: %v", err)
	}
	return n
}

func (e *testEnv) revokedFlag(table string, id contract.ID) int64 {
	e.t.Helper()
	ctx := context.Background()
	var revoked int64
	err := e.db.Read(ctx, bootActor, contract.Scope{InstallationID: e.inst}, func(unit contract.Unit) error {
		return unit.QueryRowContext(ctx, `SELECT revoked FROM `+table+` WHERE id = ?`, string(id)).Scan(&revoked)
	})
	if err != nil {
		e.t.Fatalf("read revoked flag for %s %s: %v", table, id, err)
	}
	return revoked
}

// seedRevokedPair provisions one credential and one grant for a fresh
// principal and revokes both through the real public operations, returning
// their identities.
func (e *testEnv) seedRevokedPair() (credentialID, grantID contract.ID) {
	e.t.Helper()
	ctx := context.Background()

	var principal resourceOut[principalOut]
	decode(e.t, e.mustCall(e.owner, opPrincipalCreate, principalCreateInput{
		Scope: contract.Scope{InstallationID: e.inst},
		Definition: principalDefinition{
			Kind: contract.KindWorker, Name: "Restore Subject",
			Scope: contract.Scope{InstallationID: e.inst}, Revoked: false,
		},
	}), &principal)

	if _, err := e.secrets.Put(ctx, "store/restore-subject", []byte("zt-token-restore-subject")); err != nil {
		e.t.Fatalf("seed subject secret: %v", err)
	}
	var credential resourceOut[credentialOut]
	decode(e.t, e.mustCall(e.owner, opCredProvision, credentialProvisionInput{
		Scope: contract.Scope{InstallationID: e.inst}, PrincipalID: principal.Resource.ID,
		StoreRef: "store/restore-subject",
	}), &credential)

	var grant resourceOut[grantOut]
	decode(e.t, e.mustCall(e.owner, opGrantCreate, grantCreateInput{
		Scope: contract.Scope{InstallationID: e.inst},
		Definition: grantDefinition{
			PrincipalID: principal.Resource.ID, Scope: contract.Scope{InstallationID: e.inst},
			Capabilities: []string{"task.create"}, Destinations: []string{}, Denied: false,
		},
	}), &grant)

	e.mustCall(e.owner, opCredRevoke, credentialRevokeInput{
		Scope: contract.Scope{InstallationID: e.inst}, ID: credential.Resource.ID,
		ExpectedVersion: credential.Resource.Version,
	})
	e.mustCall(e.owner, opGrantRevoke, grantRevokeInput{
		Scope: contract.Scope{InstallationID: e.inst}, ID: grant.Resource.ID,
		ExpectedVersion: grant.Resource.Version,
	})
	return credential.Resource.ID, grant.Resource.ID
}

// obligationFor builds the RecoveryObligation installation's own
// snapshotObligations produces for one revocation.
func obligationFor(kind string, id contract.ID, recordedAt time.Time) recoveryObligation {
	return recoveryObligation{
		ID: contract.NewID(), Owner: owner, Kind: kind, ResourceID: id, ResourceVersion: 2,
		RecordArtifact: contract.ArtifactRef{
			ID:     contract.NewID(),
			Digest: contract.Digest("0000000000000000000000000000000000000000000000000000000000000001"),
		},
		RecordDigest: contract.Digest("0000000000000000000000000000000000000000000000000000000000000001"),
		State:        "revoked", RecordedAt: recordedAt.UTC().Format(time.RFC3339Nano),
	}
}

// TestRevocationsReportsNoneUntilSomethingIsActuallyRevoked is the required
// behavior "_identity.revocations reports zero revocations when the
// installation was bootstrapped and never restricted", plus the other half
// that makes that meaningful: once a credential and a grant really are
// revoked, both appear, by kind and identity.
func TestRevocationsReportsNoneUntilSomethingIsActuallyRevoked(t *testing.T) {
	e := newTestEnv(t)

	var empty revocationsOutput
	decode(t, e.mustCall(e.owner, opRevocations, revocationsInput{
		Scope: contract.Scope{InstallationID: e.inst},
	}), &empty)
	if len(empty.Revocations) != 0 {
		t.Fatalf("a bootstrapped, never-restricted installation reports %d revocations, want none: %+v",
			len(empty.Revocations), empty.Revocations)
	}

	credentialID, grantID := e.seedRevokedPair()

	var after revocationsOutput
	decode(t, e.mustCall(e.owner, opRevocations, revocationsInput{
		Scope: contract.Scope{InstallationID: e.inst},
	}), &after)
	seen := map[string]contract.ID{}
	for _, r := range after.Revocations {
		seen[r.Kind] = r.ID
		if r.Version < 2 {
			t.Errorf("revoked %s %s reports version %d, want the post-revocation version", r.Kind, r.ID, r.Version)
		}
		if r.PrincipalID == "" {
			t.Errorf("revoked %s %s reports no owning principal", r.Kind, r.ID)
		}
	}
	if seen[revocationKindCredential] != credentialID {
		t.Fatalf("revoked credential reported as %s, want %s", seen[revocationKindCredential], credentialID)
	}
	if seen[revocationKindGrant] != grantID {
		t.Fatalf("revoked grant reported as %s, want %s", seen[revocationKindGrant], grantID)
	}
	if len(after.Revocations) != 2 {
		t.Fatalf("reported %d revocations, want exactly the credential and the grant: %+v",
			len(after.Revocations), after.Revocations)
	}
}

// TestRestoreMergeReappliesRewoundRevocationsAndIsIdempotent is the
// identity half of the required behavior "the post-backup credential and
// grant remain revoked" after a rewind, proven against the real rows: both
// are put back to unrevoked exactly as the swapped-in backup file holds
// them, the merge re-applies both, and a repeat of the same merge applies
// nothing and appends no second revocation-ledger row.
func TestRestoreMergeReappliesRewoundRevocationsAndIsIdempotent(t *testing.T) {
	e := newTestEnv(t)
	credentialID, grantID := e.seedRevokedPair()
	capturedAt := e.clock.Now()
	obligations := []recoveryObligation{
		obligationFor(obligationRevokedCredential, credentialID, capturedAt.Add(-time.Second)),
		obligationFor(obligationRevokedGrant, grantID, capturedAt.Add(-time.Second)),
	}

	// The rewind: both rows go back to the state the older backup holds.
	e.rewindRevocation("identity_credentials", credentialID)
	e.rewindRevocation("identity_grants", grantID)
	if e.revokedFlag("identity_credentials", credentialID) != 0 || e.revokedFlag("identity_grants", grantID) != 0 {
		t.Fatal("the simulated rewind did not clear both revocations; the test would prove nothing")
	}
	credentialLedger := e.countRevocationRows(revocationKindCredential, credentialID)
	grantLedger := e.countRevocationRows(revocationKindGrant, grantID)

	controller := e.controllerActor(t)
	var first resourceOut[recoveryMergeOut]
	decode(t, e.mustCall(controller, opRestoreMerge, restoreMergeInput{
		RestoreJobID: contract.NewID(), CapturedAt: capturedAt.UTC().Format(time.RFC3339Nano),
		SourceGeneration: 1, Obligations: obligations,
	}), &first)
	if len(first.Resource.Applied) != 2 || len(first.Resource.Skipped) != 0 {
		t.Fatalf("first merge applied %v / skipped %v, want both obligations applied",
			first.Resource.Applied, first.Resource.Skipped)
	}
	if e.revokedFlag("identity_credentials", credentialID) != 1 {
		t.Fatal("the credential revoked after the backup did not survive the rewind")
	}
	if e.revokedFlag("identity_grants", grantID) != 1 {
		t.Fatal("the grant revoked after the backup did not survive the rewind")
	}

	var second resourceOut[recoveryMergeOut]
	decode(t, e.mustCall(controller, opRestoreMerge, restoreMergeInput{
		RestoreJobID: contract.NewID(), CapturedAt: capturedAt.UTC().Format(time.RFC3339Nano),
		SourceGeneration: 1, Obligations: obligations,
	}), &second)
	if len(second.Resource.Applied) != 0 || len(second.Resource.Skipped) != 2 {
		t.Fatalf("repeated merge applied %v / skipped %v, want nothing applied a second time",
			second.Resource.Applied, second.Resource.Skipped)
	}
	if got := e.countRevocationRows(revocationKindCredential, credentialID); got != credentialLedger+1 {
		t.Fatalf("credential revocation ledger holds %d rows, want exactly one more than before the merge (%d)",
			got, credentialLedger)
	}
	if got := e.countRevocationRows(revocationKindGrant, grantID); got != grantLedger+1 {
		t.Fatalf("grant revocation ledger holds %d rows, want exactly one more than before the merge (%d)",
			got, grantLedger)
	}
}

// TestRestoreMergeNeverCreatesAnIdentityTheRestoredImageDoesNotHold proves
// the monotonic floor: an obligation naming a credential or grant the
// rewound database no longer contains is skipped, never recreated to carry
// a revocation, and the merge as a whole still succeeds.
func TestRestoreMergeNeverCreatesAnIdentityTheRestoredImageDoesNotHold(t *testing.T) {
	e := newTestEnv(t)
	missingCredential := contract.NewID()
	missingGrant := contract.NewID()
	capturedAt := e.clock.Now()

	var out resourceOut[recoveryMergeOut]
	decode(t, e.mustCall(e.controllerActor(t), opRestoreMerge, restoreMergeInput{
		RestoreJobID: contract.NewID(), CapturedAt: capturedAt.UTC().Format(time.RFC3339Nano),
		SourceGeneration: 1,
		Obligations: []recoveryObligation{
			obligationFor(obligationRevokedCredential, missingCredential, capturedAt.Add(-time.Second)),
			obligationFor(obligationRevokedGrant, missingGrant, capturedAt.Add(-time.Second)),
		},
	}), &out)
	if len(out.Resource.Applied) != 0 || len(out.Resource.Skipped) != 2 {
		t.Fatalf("merge applied %v / skipped %v, want both skipped", out.Resource.Applied, out.Resource.Skipped)
	}

	ctx := context.Background()
	var credentials, grants int64
	err := e.db.Read(ctx, bootActor, contract.Scope{InstallationID: e.inst}, func(unit contract.Unit) error {
		if err := unit.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM identity_credentials WHERE id = ?`, string(missingCredential)).Scan(&credentials); err != nil {
			return err
		}
		return unit.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM identity_grants WHERE id = ?`, string(missingGrant)).Scan(&grants)
	})
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if credentials != 0 || grants != 0 {
		t.Fatalf("the merge invented %d credentials and %d grants from obligations alone", credentials, grants)
	}
}

// TestRestoreMergeRefusesAnObligationNewerThanTheOverlayCapturePoint proves
// the merge cannot back-date evidence: an obligation recorded after the
// overlay sealed is refused outright rather than applied to the rewound
// state.
func TestRestoreMergeRefusesAnObligationNewerThanTheOverlayCapturePoint(t *testing.T) {
	e := newTestEnv(t)
	credentialID, _ := e.seedRevokedPair()
	e.rewindRevocation("identity_credentials", credentialID)
	capturedAt := e.clock.Now()

	e.wantFault(e.controllerActor(t), opRestoreMerge, restoreMergeInput{
		RestoreJobID: contract.NewID(), CapturedAt: capturedAt.UTC().Format(time.RFC3339Nano),
		SourceGeneration: 1,
		Obligations: []recoveryObligation{
			obligationFor(obligationRevokedCredential, credentialID, capturedAt.Add(time.Minute)),
		},
	}, contract.CodeInvalidInput)

	if e.revokedFlag("identity_credentials", credentialID) != 0 {
		t.Fatal("the refused merge still wrote; a rolled-back handler must change nothing")
	}
}

// TestRestoreMergeRefusesANonControllerActor is the required behavior
// "calling _identity.restore.merge with a non-controller actor is refused",
// exercised against the two realistic impostors: the installation's own
// human owner (who holds the installation-wide wildcard grant, so no
// capability check would stop it) and a service principal that is simply
// not the controller.
func TestRestoreMergeRefusesANonControllerActor(t *testing.T) {
	e := newTestEnv(t)
	credentialID, _ := e.seedRevokedPair()
	e.rewindRevocation("identity_credentials", credentialID)
	capturedAt := e.clock.Now()
	input := restoreMergeInput{
		RestoreJobID: contract.NewID(), CapturedAt: capturedAt.UTC().Format(time.RFC3339Nano),
		SourceGeneration: 1,
		Obligations: []recoveryObligation{
			obligationFor(obligationRevokedCredential, credentialID, capturedAt.Add(-time.Second)),
		},
	}

	// The installation owner: authenticated, unrevoked, wildcard-granted --
	// and still not the controller.
	e.wantFault(e.owner, opRestoreMerge, input, contract.CodePermissionDenied)

	// A different service principal: right kind, wrong identity.
	var impostor resourceOut[principalOut]
	decode(t, e.mustCall(e.owner, opPrincipalCreate, principalCreateInput{
		Scope: contract.Scope{InstallationID: e.inst},
		Definition: principalDefinition{
			Kind: contract.KindService, Name: "Not The Controller",
			Scope: contract.Scope{InstallationID: e.inst}, Revoked: false,
		},
	}), &impostor)
	e.wantFault(contract.Actor{PrincipalID: impostor.Resource.ID, Kind: contract.KindService},
		opRestoreMerge, input, contract.CodePermissionDenied)

	// An unregistered principal id cannot stand in for the controller either.
	e.wantFault(contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindService},
		opRestoreMerge, input, contract.CodePermissionDenied)

	if e.revokedFlag("identity_credentials", credentialID) != 0 {
		t.Fatal("a refused merge still wrote to identity's tables")
	}
}
