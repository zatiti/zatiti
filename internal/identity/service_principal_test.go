package identity

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/storage"
)

// Tests for the scoped service identity bootstrap creates alongside the
// owner: the controller's service principal, its standing authority, the
// Go API entrypoint assembly resolves it through, and the revocation
// semantics that must hold for it exactly as for every other principal.

// controllerActor resolves the bootstrapped controller principal through the
// same Go API entrypoint assembly uses.
func (e *testEnv) controllerActor(t *testing.T) contract.Actor {
	t.Helper()
	var (
		actor contract.Actor
		err   error
	)
	readErr := e.db.Read(context.Background(), bootActor, contract.Scope{InstallationID: e.inst}, func(unit contract.Unit) error {
		actor, err = e.svc.ControllerPrincipal(context.Background(), unit)
		return nil
	})
	if readErr != nil {
		t.Fatalf("read controller principal: %v", readErr)
	}
	if err != nil {
		t.Fatalf("ControllerPrincipal: %v", err)
	}
	return actor
}

// controllerFault resolves the controller principal expecting a fault.
func (e *testEnv) controllerFault(t *testing.T, code string) {
	t.Helper()
	var err error
	readErr := e.db.Read(context.Background(), bootActor, contract.Scope{InstallationID: e.inst}, func(unit contract.Unit) error {
		_, err = e.svc.ControllerPrincipal(context.Background(), unit)
		return nil
	})
	if readErr != nil {
		t.Fatalf("read controller principal: %v", readErr)
	}
	var fault *contract.Fault
	if !errors.As(err, &fault) || fault.Code != code {
		t.Fatalf("ControllerPrincipal: expected fault %s, got %v", code, err)
	}
}

// authorityOf resolves a principal's authority view at scope under the
// given caller.
func (e *testEnv) authorityOf(t *testing.T, caller contract.Actor, principal contract.ID, scope contract.Scope) authorityOut {
	t.Helper()
	payload := e.mustCall(caller, opAuthority, authorityInput{PrincipalID: principal, Scope: scope})
	var out resourceOut[authorityOut]
	decode(t, payload, &out)
	return out.Resource
}

// TestBootstrapProvisionsControllerPrincipal proves the bootstrap transaction
// creates the controller's scoped service identity with exactly the standing
// authority the internal dispatch path checks: the installation-wide
// _identity.authority capability, nothing else, no destinations, no expiry,
// no restrictions. The controller resolves its own authority under its own
// actor, which is what application's revalidation does on every Internal
// call, at the installation scope and at any narrower scope.
func TestBootstrapProvisionsControllerPrincipal(t *testing.T) {
	env := newTestEnv(t)
	ctrl := env.controllerActor(t)
	if ctrl.Kind != contract.KindService || ctrl.PrincipalID == "" {
		t.Fatalf("controller actor = %+v, want a service principal", ctrl)
	}
	if ctrl.PrincipalID == env.owner.PrincipalID {
		t.Fatal("controller principal is the owner")
	}

	install := contract.Scope{InstallationID: env.inst}
	view := env.authorityOf(t, ctrl, ctrl.PrincipalID, install)
	if view.Principal.Kind != contract.KindService || view.Principal.Name != ControllerPrincipalName || view.Principal.Revoked {
		t.Fatalf("controller principal view = %+v", view.Principal)
	}
	if view.Principal.Scope != install {
		t.Fatalf("controller principal scope = %+v, want installation-wide", view.Principal.Scope)
	}
	if len(view.Grants) != 1 {
		t.Fatalf("controller holds %d grants, want exactly 1: %+v", len(view.Grants), view.Grants)
	}
	g := view.Grants[0]
	if len(g.Capabilities) != 1 || g.Capabilities[0] != opAuthority {
		t.Fatalf("controller capabilities = %v, want exactly [%s]", g.Capabilities, opAuthority)
	}
	if g.Denied || g.ExpiresAt != nil || g.ParentGrantID != nil || len(g.Destinations) != 0 || g.Scope != install {
		t.Fatalf("controller grant = %+v, want an unbounded installation-wide allow grant", g)
	}
	if len(view.Restrictions) != 0 {
		t.Fatalf("controller restrictions = %v, want none", view.Restrictions)
	}

	// Narrower request scopes are covered by the installation-wide grant.
	deep := contract.Scope{InstallationID: env.inst, OrganizationID: contract.NewID(), ProjectID: contract.NewID(), WorkerID: contract.NewID(), TaskID: contract.NewID()}
	if got := env.authorityOf(t, ctrl, ctrl.PrincipalID, deep); len(got.Grants) != 1 {
		t.Fatalf("controller authority at a task scope returned %d grants, want 1", len(got.Grants))
	}

	// The controller's authority view is also what other principals see.
	if got := env.authorityOf(t, env.owner, ctrl.PrincipalID, install); len(got.Grants) != 1 {
		t.Fatalf("owner's view of the controller returned %d grants, want 1", len(got.Grants))
	}
}

// TestControllerPrincipalHoldsNothingElse proves the service principal has no
// owner-level or public authority: it cannot administer principals, grants
// or credentials, cannot widen itself, and cannot enter the policy-only
// internal operations.
func TestControllerPrincipalHoldsNothingElse(t *testing.T) {
	env := newTestEnv(t)
	ctrl := env.controllerActor(t)
	install := contract.Scope{InstallationID: env.inst}

	env.wantFault(ctrl, opPrincipalCreate, principalCreateInput{
		Scope: install,
		Definition: principalDefinition{
			Kind: contract.KindWorker, Name: "W", Scope: install, Revoked: false,
		},
	}, contract.CodePermissionDenied)
	env.wantFault(ctrl, opPrincipalList, listInput{Scope: install}, contract.CodePermissionDenied)
	env.wantFault(ctrl, opGrantCreate, grantCreateInput{
		Scope: install,
		Definition: grantDefinition{
			PrincipalID: ctrl.PrincipalID, Scope: install,
			Capabilities: []string{capWildcard}, Destinations: []string{}, Denied: false,
		},
	}, contract.CodePermissionDenied)
	env.wantFault(ctrl, opCredProvision, credentialProvisionInput{
		Scope: install, PrincipalID: ctrl.PrincipalID, StoreRef: "store/owner-token",
	}, contract.CodePermissionDenied)
	env.wantFault(ctrl, opRestrict, restrictInput{
		PrincipalID: env.owner.PrincipalID, Capability: "principal.create", Reason: "x",
	}, contract.CodePermissionDenied)
	env.wantFault(ctrl, opPrincipalRevoke, principalRevokeInput{
		Scope: install, ID: env.owner.PrincipalID, ExpectedVersion: 1,
	}, contract.CodePermissionDenied)

	// No credential authenticates as the controller: it has none.
	var credentials int64
	err := env.db.Read(context.Background(), bootActor, install, func(unit contract.Unit) error {
		return unit.QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM identity_credentials WHERE principal_id = ?`, string(ctrl.PrincipalID)).Scan(&credentials)
	})
	if err != nil {
		t.Fatal(err)
	}
	if credentials != 0 {
		t.Fatalf("controller principal has %d credentials; bootstrap has no input for a service credential", credentials)
	}
}

// TestControllerPrincipalRevocation proves revocation semantics hold for the
// service principal: once the owner revokes it, its very next authority
// resolution is refused, the Go API refuses to hand it out, the revocation
// survives close-and-reopen, and revoking only its grant strips the
// capability without touching the principal.
func TestControllerPrincipalRevocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controller.db")
	env := newEnvOnDB(t, path)
	ctrl := env.controllerActor(t)
	install := contract.Scope{InstallationID: env.inst}

	// Grant revocation alone: the principal stands but holds nothing, so it
	// cannot even resolve authority any more.
	view := env.authorityOf(t, ctrl, ctrl.PrincipalID, install)
	env.mustCall(env.owner, opGrantRevoke, grantRevokeInput{
		Scope: install, ID: view.Grants[0].ID, ExpectedVersion: 1,
	})
	env.wantFault(ctrl, opAuthority, authorityInput{PrincipalID: ctrl.PrincipalID, Scope: install}, contract.CodePermissionDenied)
	if got := env.authorityOf(t, env.owner, ctrl.PrincipalID, install); len(got.Grants) != 0 || got.Principal.Revoked {
		t.Fatalf("after grant revoke the controller view = %+v, want no grants and an unrevoked principal", got)
	}
	// The Go API still resolves an unrevoked principal; authority is the
	// dispatcher's concern.
	if again := env.controllerActor(t); again != ctrl {
		t.Fatalf("controller actor changed after grant revoke: %+v", again)
	}

	// Principal revocation: immediate and durable.
	env.mustCall(env.owner, opPrincipalRevoke, principalRevokeInput{
		Scope: install, ID: ctrl.PrincipalID, ExpectedVersion: 1,
	})
	env.wantFault(ctrl, opAuthority, authorityInput{PrincipalID: ctrl.PrincipalID, Scope: install}, contract.CodePermissionDenied)
	env.controllerFault(t, contract.CodePermissionDenied)

	if err := env.db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.Config{Path: path})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = db.Close() }()
	svc, err := New(contract.Dependencies{Clock: env.clock, IDs: idSource{}, Secrets: env.secrets})
	if err != nil {
		t.Fatal(err)
	}
	env2 := &testEnv{t: t, db: db, svc: svc, clock: env.clock, secrets: env.secrets, inst: env.inst, owner: env.owner}
	env2.wantFault(ctrl, opAuthority, authorityInput{PrincipalID: ctrl.PrincipalID, Scope: install}, contract.CodePermissionDenied)
	env2.controllerFault(t, contract.CodePermissionDenied)
}

// TestBootstrapEmitsControllerEvents proves the bootstrap transaction records
// the controller principal and its grant as ordinary identity transitions,
// so the event log accounts for every principal and grant that exists.
func TestBootstrapEmitsControllerEvents(t *testing.T) {
	env := newTestEnv(t)
	ctrl := env.controllerActor(t)
	view := env.authorityOf(t, ctrl, ctrl.PrincipalID, contract.Scope{InstallationID: env.inst})
	events, err := env.db.Events(context.Background(), 0, 64)
	if err != nil {
		t.Fatal(err)
	}
	var principal, grant bool
	for _, ev := range events {
		switch {
		case ev.Kind == eventPrincipalCreated && ev.ResourceID == ctrl.PrincipalID:
			principal = true
		case ev.Kind == eventGrantCreated && ev.ResourceID == view.Grants[0].ID:
			grant = true
		}
	}
	if !principal || !grant {
		t.Fatalf("bootstrap events lack the controller principal (%v) or grant (%v) transitions", principal, grant)
	}
}

// TestBootstrapRefusesReservedOwnerName proves the owner cannot take the
// controller's reserved name: the whole bootstrap is refused up front and
// nothing is persisted.
func TestBootstrapRefusesReservedOwnerName(t *testing.T) {
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
	if _, err := secrets.Put(ctx, "store/owner-token", []byte("zt-token-reserved")); err != nil {
		t.Fatal(err)
	}
	env := &testEnv{t: t, db: db, svc: svc, clock: clock, secrets: secrets, inst: contract.NewID()}

	// Before any bootstrap there is no controller principal to resolve.
	env.controllerFault(t, contract.CodePrerequisiteMissing)

	env.wantFault(bootActor, opBootstrap, bootstrapInput{
		OwnerID:        contract.NewID(),
		CredentialID:   contract.NewID(),
		StoreRef:       "store/owner-token",
		Name:           ControllerPrincipalName,
		InstallationID: env.inst,
	}, contract.CodeInvalidInput)
	var principals int64
	if err := db.Read(ctx, bootActor, contract.Scope{InstallationID: env.inst}, func(unit contract.Unit) error {
		return unit.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM identity_principals WHERE installation_id = ?`, string(env.inst)).Scan(&principals)
	}); err != nil {
		t.Fatal(err)
	}
	if principals != 0 {
		t.Fatalf("refused bootstrap left %d principals behind", principals)
	}
	env.controllerFault(t, contract.CodePrerequisiteMissing)
}

// TestBootstrapCreatesServicePrincipal proves bootstrap creates exactly one
// service principal (the controller's) and that it can resolve its own
// authority, which is what application's Internal revalidation requires.
func TestBootstrapCreatesServicePrincipal(t *testing.T) {
	env := newTestEnv(t)
	var ids []contract.ID
	err := env.db.Read(context.Background(), bootActor, contract.Scope{InstallationID: env.inst}, func(unit contract.Unit) error {
		rows, err := unit.QueryContext(context.Background(),
			`SELECT id FROM identity_principals WHERE installation_id = ? AND kind = 'service' AND revoked = 0`, string(env.inst))
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id contract.ID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Fatalf("bootstrap left %d service principals, want exactly 1 (the controller)", len(ids))
	}
	ctrl := contract.Actor{PrincipalID: ids[0], Kind: contract.KindService}
	env.mustCall(ctrl, opAuthority, authorityInput{PrincipalID: ctrl.PrincipalID, Scope: contract.Scope{InstallationID: env.inst}})
}
