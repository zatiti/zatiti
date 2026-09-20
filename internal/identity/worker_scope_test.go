package identity

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/storage"
)

// Tests for the P03 card's three required behaviors: a worker cannot obtain
// the owner identity or another worker's scope; revocation between context
// construction and dispatch denies the action; and bootstrap/replay creates
// exactly one chief principal. identity.current -- the operation the third
// behavior's CLI/MCP/desktop-parity half depends on -- is not yet a frozen
// catalog operation (see the P03 PR description for the tracked contract
// gap back to P00/contract-proposals.md section 2); these tests prove the
// identity-owned invariants that operation would report from once it lands.

// TestWorkerCannotObtainOwnerIdentityOrAnotherWorkersScope proves that a
// worker principal granted a capability at its own scope only cannot use
// that capability to read, update or revoke a principal, grant or credential
// outside its own scope -- neither the owner's installation-wide identity
// nor a sibling worker's -- even when the request's own `scope` field is
// deliberately set to fall inside its own granted envelope. The capability
// check authorizes the REQUEST scope; the by-ID target's own scope must be
// checked independently, or a narrowly granted caller could name any
// principal, grant or credential in the installation by ID and act on it.
func TestWorkerCannotObtainOwnerIdentityOrAnotherWorkersScope(t *testing.T) {
	env := newTestEnv(t)
	workerAResource := contract.NewID()
	workerBResource := contract.NewID()
	workerAScope := contract.Scope{InstallationID: env.inst, WorkerID: workerAResource}
	workerBScope := contract.Scope{InstallationID: env.inst, WorkerID: workerBResource}

	workerA := env.mustCreatePrincipal("Worker A", contract.KindWorker, workerAScope)
	workerB := env.mustCreatePrincipal("Worker B", contract.KindWorker, workerBScope)
	env.mustGrant(workerA.ID, workerAScope,
		[]string{"principal.get", "principal.update", "principal.revoke", "grant.get", "grant.revoke", "credential.provision"},
		[]string{})
	workerAAuth := contract.Actor{PrincipalID: workerA.ID, Kind: contract.KindWorker}

	// Worker A can act on its own principal: the grant is not inert.
	env.mustCall(workerAAuth, opPrincipalGet, principalGetInput{Scope: workerAScope, ID: workerA.ID})

	// Reading the owner's identity is refused, naming Worker A's OWN scope
	// as the request scope (which alone satisfies `authorize`) and the
	// owner's principal ID as the target.
	env.wantFault(workerAAuth, opPrincipalGet, principalGetInput{
		Scope: workerAScope, ID: env.owner.PrincipalID,
	}, contract.CodeNotFound)

	// Reading a sibling worker's identity is refused identically.
	env.wantFault(workerAAuth, opPrincipalGet, principalGetInput{
		Scope: workerAScope, ID: workerB.ID,
	}, contract.CodeNotFound)

	// The owner's principal cannot be updated or revoked through Worker A's
	// narrow grant either.
	env.wantFault(workerAAuth, opPrincipalUpdate, principalUpdateInput{
		Scope: workerAScope, ID: env.owner.PrincipalID, ExpectedVersion: 1,
		Definition: principalDefinition{
			Kind: contract.KindHuman, Name: "Hijacked", Scope: contract.Scope{InstallationID: env.inst}, Revoked: false,
		},
	}, contract.CodeNotFound)
	env.wantFault(workerAAuth, opPrincipalRevoke, principalRevokeInput{
		Scope: workerAScope, ID: env.owner.PrincipalID, ExpectedVersion: 1,
	}, contract.CodeNotFound)

	// A sibling worker's principal is equally out of reach for revoke.
	env.wantFault(workerAAuth, opPrincipalRevoke, principalRevokeInput{
		Scope: workerAScope, ID: workerB.ID, ExpectedVersion: 1,
	}, contract.CodeNotFound)

	// The owner's root grant cannot be read or revoked by ID either, even
	// though Worker A separately holds grant.get/grant.revoke.
	ownerRootGrant := env.ownerRootGrantID(t)
	env.wantFault(workerAAuth, opGrantGet, grantGetInput{
		Scope: workerAScope, ID: ownerRootGrant,
	}, contract.CodeNotFound)
	env.wantFault(workerAAuth, opGrantRevoke, grantRevokeInput{
		Scope: workerAScope, ID: ownerRootGrant, ExpectedVersion: 1,
	}, contract.CodeNotFound)

	// Worker A cannot mint a credential for the owner -- an authentication
	// escalation, not merely a read -- through its own credential.provision
	// grant.
	env.wantFault(workerAAuth, opCredProvision, credentialProvisionInput{
		Scope: workerAScope, PrincipalID: env.owner.PrincipalID, StoreRef: "store/owner-token",
	}, contract.CodeNotFound)
}

// ownerRootGrantID looks up the bootstrap owner's installation-wide root
// grant id.
func (e *testEnv) ownerRootGrantID(t *testing.T) contract.ID {
	t.Helper()
	var id contract.ID
	err := e.db.Read(context.Background(), bootActor, contract.Scope{InstallationID: e.inst}, func(unit contract.Unit) error {
		return unit.QueryRowContext(context.Background(),
			`SELECT id FROM identity_grants WHERE principal_id = ? AND installation_id = ?`,
			string(e.owner.PrincipalID), string(e.inst)).Scan(&id)
	})
	if err != nil {
		t.Fatalf("lookup owner root grant: %v", err)
	}
	return id
}

// TestRevocationBetweenContextConstructionAndDispatchDeniesAction proves that
// a principal authorized when a request's context was constructed -- the
// _identity.authority read a controller performs while preparing a worker
// turn or dispatch -- but revoked before the action actually dispatches is
// denied at dispatch time. Identity performs no caching: every admission and
// every _identity.authority read re-evaluates current stored state, never a
// snapshot taken earlier in the same logical operation. This is the
// identity-owned half of the card's "recheck revocation on every resolution
// and dispatch" requirement.
func TestRevocationBetweenContextConstructionAndDispatchDeniesAction(t *testing.T) {
	env := newTestEnv(t)
	worker := env.mustCreatePrincipal("Worker", contract.KindWorker)
	install := contract.Scope{InstallationID: env.inst}
	grant := env.mustGrant(worker.ID, install, []string{"principal.create"}, []string{})
	workerAuth := contract.Actor{PrincipalID: worker.ID, Kind: contract.KindWorker}

	// Context construction: resolve the worker's current authority, exactly
	// as execution's turn/context pipeline resolves a worker's standing
	// before building a dispatch. The capability is present.
	before := env.authorityOf(t, workerAuth, worker.ID, install)
	if !containsCap(before.Grants[0].Capabilities, "principal.create") {
		t.Fatalf("worker's constructed context lacks principal.create: %+v", before)
	}

	// The dispatch the constructed context authorized succeeds while the
	// grant still stands.
	env.mustCall(workerAuth, opPrincipalCreate, principalCreateInput{
		Scope: install,
		Definition: principalDefinition{
			Kind: contract.KindService, Name: "before-revoke", Scope: install, Revoked: false,
		},
	})

	// Revocation happens strictly between the constructed context and the
	// next dispatch attempt.
	env.mustCall(env.owner, opGrantRevoke, grantRevokeInput{Scope: install, ID: grant.ID, ExpectedVersion: 1})

	// A fresh authority resolution immediately reflects the revocation...
	after := env.authorityOf(t, workerAuth, worker.ID, install)
	if len(after.Grants) != 0 {
		t.Fatalf("authority after revoke = %+v, want no grants", after)
	}

	// ...and the actual dispatch -- the same protected mutation the earlier
	// context authorized -- is denied. Identity never acts on a context
	// snapshot taken before the revocation.
	env.wantFault(workerAuth, opPrincipalCreate, principalCreateInput{
		Scope: install,
		Definition: principalDefinition{
			Kind: contract.KindService, Name: "after-revoke", Scope: install, Revoked: false,
		},
	}, contract.CodePermissionDenied)
}

// TestRevocationBetweenContextConstructionAndDispatchDeniesCredentialedAction
// proves the same recheck for the credential path: a credential valid when
// context was constructed but revoked before dispatch fails authentication
// on the next attempt, so a controller cannot dispatch under a stale
// authenticated actor either.
func TestRevocationBetweenContextConstructionAndDispatchDeniesCredentialedAction(t *testing.T) {
	env := newTestEnv(t)
	agent := env.mustCreatePrincipal("Agent", contract.KindClientAgent)
	token := "zt-agent-token-" + string(contract.NewID())
	if _, err := env.secrets.Put(context.Background(), "store/agent-token", []byte(token)); err != nil {
		t.Fatal(err)
	}
	payload := env.mustCall(env.owner, opCredProvision, credentialProvisionInput{
		Scope: contract.Scope{InstallationID: env.inst}, PrincipalID: agent.ID, StoreRef: "store/agent-token",
	})
	var cred resourceOut[credentialOut]
	decode(t, payload, &cred)

	// Context construction: authenticate the presented credential, exactly
	// as the application layer resolves an actor before building a request.
	actor, err := env.authenticate(token)
	if err != nil {
		t.Fatalf("authenticate before revoke: %v", err)
	}
	if actor.PrincipalID != agent.ID {
		t.Fatalf("authenticated as %+v, want the agent", actor)
	}

	// Revocation lands strictly between that constructed context and the
	// dispatch that would have used it.
	env.mustCall(env.owner, opCredRevoke, credentialRevokeInput{
		Scope: contract.Scope{InstallationID: env.inst}, ID: cred.Resource.ID, ExpectedVersion: 1,
	})

	// Dispatch -- re-authenticating the same presented bytes, as a fresh
	// request would -- is denied; the earlier constructed actor cannot be
	// replayed past the revocation.
	if _, err := env.authenticate(token); err == nil {
		t.Fatal("revoked credential still authenticates a dispatch after context construction")
	}
}

// TestBootstrapReplayCreatesOneChiefPrincipal proves the identity-owned half
// of Z03.bootstrap_parity for the personal chief specifically: bootstrap
// creates exactly one human owner ("chief") principal, and every later
// bootstrap attempt against the same installation -- a byte-identical replay
// of the original input or a wholly different owner -- is refused without
// ever creating, renaming or duplicating it. _identity.bootstrap declares no
// submission key, so this refusal is unconditional, not a submission-key
// idempotent replay.
func TestBootstrapReplayCreatesOneChiefPrincipal(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{t: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)}
	secrets := newFakeSecrets()
	svc, err := New(contract.Dependencies{Clock: clock, IDs: idSource{}, Secrets: secrets})
	if err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(ctx, storage.Config{Path: filepath.Join(t.TempDir(), "identity.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Migrate(ctx, svc.Migrations()); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.Put(ctx, "store/owner-token", []byte("zt-token-chief")); err != nil {
		t.Fatal(err)
	}
	inst := contract.NewID()
	env := &testEnv{t: t, db: db, svc: svc, clock: clock, secrets: secrets, inst: inst}

	original := bootstrapInput{
		OwnerID:        contract.NewID(),
		CredentialID:   contract.NewID(),
		StoreRef:       "store/owner-token",
		Name:           "Ada Owner",
		InstallationID: inst,
	}
	env.mustCall(bootActor, opBootstrap, original)
	chiefID := env.mustPrincipalID("Ada Owner")

	// A byte-identical replay of the exact original input is still refused.
	env.wantFault(bootActor, opBootstrap, original, contract.CodeConflict)

	// A replay with entirely different identity references is refused
	// identically.
	env.wantFault(bootActor, opBootstrap, bootstrapInput{
		OwnerID:        contract.NewID(),
		CredentialID:   contract.NewID(),
		StoreRef:       "store/owner-token",
		Name:           "Someone Else",
		InstallationID: inst,
	}, contract.CodeConflict)

	// Exactly one human "chief" principal exists in the installation, and it
	// is still the original bootstrap owner: neither replay attempt created,
	// renamed or duplicated it.
	var humans int64
	var soleID contract.ID
	if err := db.Read(ctx, bootActor, contract.Scope{InstallationID: inst}, func(unit contract.Unit) error {
		if err := unit.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM identity_principals WHERE installation_id = ? AND kind = 'human'`, string(inst)).Scan(&humans); err != nil {
			return err
		}
		return unit.QueryRowContext(ctx,
			`SELECT id FROM identity_principals WHERE installation_id = ? AND kind = 'human'`, string(inst)).Scan(&soleID)
	}); err != nil {
		t.Fatal(err)
	}
	if humans != 1 {
		t.Fatalf("bootstrap replay left %d chief principals, want exactly 1", humans)
	}
	if soleID != chiefID {
		t.Fatalf("the sole chief principal is %s, want the original bootstrap owner %s", soleID, chiefID)
	}

	// identity.current is not yet a frozen catalog operation for this
	// package to serve or test (see the P03 PR description); the
	// CLI/MCP/desktop agreement half of this required behavior is therefore
	// blocked on that operation landing, not on the invariant proven here.
}
