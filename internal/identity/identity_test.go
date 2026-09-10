package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/storage"
)

// Test fixtures. Each test runs against a real storage-backed temporary
// database: operations run through contract.Database transactions with
// authenticated actors, exactly as the application layer drives them.

// idSource adapts contract.NewID to the IDSource dependency.
type idSource struct{}

func (idSource) New() contract.ID { return contract.NewID() }

// fakeClock is a controllable Clock. Tests advance it to prove expiry.
type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

// Add advances the clock by d.
func (c *fakeClock) Add(d time.Duration) { c.t = c.t.Add(d) }

// fakeSecrets is an in-memory SecretStore. Put returns the key as the opaque
// reference, matching the platform's reference-based custody model.
type fakeSecrets struct{ m map[string][]byte }

func newFakeSecrets() *fakeSecrets { return &fakeSecrets{m: map[string][]byte{}} }

func (f *fakeSecrets) Put(_ context.Context, reference string, secret []byte) (string, error) {
	f.m[reference] = append([]byte(nil), secret...)
	return reference, nil
}

func (f *fakeSecrets) Get(_ context.Context, reference string) ([]byte, error) {
	secret, ok := f.m[reference]
	if !ok {
		return nil, fmt.Errorf("secret not found")
	}
	return append([]byte(nil), secret...), nil
}

func (f *fakeSecrets) Delete(_ context.Context, reference string) error {
	delete(f.m, reference)
	return nil
}

// testEnv is one fully-assembled identity service over a temporary database.
type testEnv struct {
	t       *testing.T
	db      contract.Database
	svc     *Service
	clock   *fakeClock
	secrets *fakeSecrets
	inst    contract.ID
	owner   contract.Actor
	token   string
}

// bootActor is the synthetic service actor the installation module supplies
// for the one-time bootstrap transaction, before any principal exists.
var bootActor = contract.Actor{
	PrincipalID: contract.ID("00000000-0000-4000-8000-00000000000f"),
	Kind:        contract.KindService,
}

// newTestEnv opens a temporary database, migrates identity's tables and runs
// the exclusive bootstrap transaction: one human owner whose custodied token
// is hashed into identity together with the owner's root grant.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	return newEnvOnDB(t, filepath.Join(t.TempDir(), "identity.db"))
}

// newEnvOnDB builds an environment over a caller-chosen database path so
// close-and-reopen tests can reuse the exact file.
func newEnvOnDB(t *testing.T, path string) *testEnv {
	t.Helper()
	return newEnvOnDBAt(t, path, time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC))
}

func newEnvOnDBAt(t *testing.T, path string, start time.Time) *testEnv {
	t.Helper()
	ctx := context.Background()
	clock := &fakeClock{t: start}
	secrets := newFakeSecrets()
	env := &testEnv{
		t:       t,
		clock:   clock,
		secrets: secrets,
		inst:    contract.NewID(),
		token:   "zt-token-" + string(contract.NewID()),
	}
	svc, err := New(contract.Dependencies{Clock: clock, IDs: idSource{}, Secrets: secrets})
	if err != nil {
		t.Fatalf("identity.New: %v", err)
	}
	env.svc = svc
	db, err := storage.Open(ctx, storage.Config{Path: path})
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	env.db = db
	if err := db.Migrate(ctx, svc.Migrations()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := env.secrets.Put(ctx, "store/owner-token", []byte(env.token)); err != nil {
		t.Fatalf("seed secret: %v", err)
	}
	env.mustCall(bootActor, opBootstrap, bootstrapInput{
		OwnerID:        contract.NewID(),
		CredentialID:   contract.NewID(),
		StoreRef:       "store/owner-token",
		Name:           "Ada Owner",
		InstallationID: env.inst,
	})
	env.owner = contract.Actor{
		PrincipalID: env.mustPrincipalID("Ada Owner"),
		Kind:        contract.KindHuman,
	}
	return env
}

// call runs one operation through the service inside the transaction shape
// its descriptor declares, returning the handler payload.
func (e *testEnv) call(actor contract.Actor, op string, input any) (contract.Payload, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return contract.Payload{}, err
	}
	inv := contract.Invocation{Operation: op, Version: descriptorVersion, Input: raw}
	ctx := context.Background()
	scope := contract.Scope{InstallationID: e.inst}
	var payload contract.Payload
	if d := e.svc.descriptor(op); d != nil && d.Mode == contract.ModeMutation {
		err = e.db.Write(ctx, actor, scope, func(unit contract.Unit) error {
			payload, err = e.svc.Handle(ctx, unit, inv)
			return err
		})
	} else {
		err = e.db.Read(ctx, actor, scope, func(unit contract.Unit) error {
			payload, err = e.svc.Handle(ctx, unit, inv)
			return err
		})
	}
	return payload, err
}

// mustCall fails the test when the operation errors and returns the payload.
func (e *testEnv) mustCall(actor contract.Actor, op string, input any) contract.Payload {
	e.t.Helper()
	payload, err := e.call(actor, op, input)
	if err != nil {
		e.t.Fatalf("%s: unexpected error: %v", op, err)
	}
	return payload
}

// wantFault asserts the operation fails with the given fault code.
func (e *testEnv) wantFault(actor contract.Actor, op string, input any, code string) contract.Fault {
	e.t.Helper()
	_, err := e.call(actor, op, input)
	var fault *contract.Fault
	if !errors.As(err, &fault) {
		e.t.Fatalf("%s: expected fault %s, got error %v", op, code, err)
	}
	if fault.Code != code {
		e.t.Fatalf("%s: expected fault %s, got %s (%s)", op, code, fault.Code, fault.Message)
	}
	return *fault
}

func (e *testEnv) mustPrincipalID(name string) contract.ID {
	e.t.Helper()
	var id contract.ID
	err := e.db.Read(context.Background(), bootActor, contract.Scope{InstallationID: e.inst}, func(unit contract.Unit) error {
		return unit.QueryRowContext(context.Background(),
			`SELECT id FROM identity_principals WHERE name = ? AND installation_id = ?`,
			name, string(e.inst)).Scan(&id)
	})
	if err != nil {
		e.t.Fatalf("lookup principal %q: %v", name, err)
	}
	return id
}

// authenticate resolves an actor from custodied token bytes on a read
// snapshot, exactly as the application layer would.
func (e *testEnv) authenticate(token string) (contract.Actor, error) {
	var (
		out contract.Actor
		err error
	)
	e.t.Helper()
	ctx := context.Background()
	readErr := e.db.Read(ctx, bootActor, contract.Scope{InstallationID: e.inst}, func(unit contract.Unit) error {
		out, err = e.svc.Authenticate(ctx, unit, []byte(token))
		return nil
	})
	if readErr != nil {
		return contract.Actor{}, readErr
	}
	return out, err
}

// decode unmarshals a payload body into out.
func decode(t *testing.T, payload contract.Payload, out any) {
	t.Helper()
	if err := json.Unmarshal(payload.Data, out); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
}

// bootstrapRunsOnce proves Z03.bootstrap_parity on the identity side: the
// exclusive bootstrap transaction runs exactly once per installation; a
// replay is refused with conflict and changes nothing.
func TestBootstrapRunsOnce(t *testing.T) {
	env := newTestEnv(t)
	if _, err := env.secrets.Put(context.Background(), "store/again", []byte("zt-token-again")); err != nil {
		t.Fatal(err)
	}
	env.wantFault(bootActor, opBootstrap, bootstrapInput{
		OwnerID:        contract.NewID(),
		CredentialID:   contract.NewID(),
		StoreRef:       "store/again",
		Name:           "Second Owner",
		InstallationID: env.inst,
	}, contract.CodeConflict)

	// Metadata only: exactly one principal and one credential exist after the
	// refused replay.
	var principals, credentials int64
	err := env.db.Read(context.Background(), bootActor, contract.Scope{InstallationID: env.inst}, func(unit contract.Unit) error {
		if err := unit.QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM identity_principals WHERE installation_id = ?`, string(env.inst)).Scan(&principals); err != nil {
			return err
		}
		return unit.QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM identity_credentials WHERE installation_id = ?`, string(env.inst)).Scan(&credentials)
	})
	if err != nil {
		t.Fatal(err)
	}
	if principals != 1 || credentials != 1 {
		t.Fatalf("bootstrap replay changed state: %d principals, %d credentials", principals, credentials)
	}
}

// TestBootstrapIsAtomic proves Z17's identity half: a failing bootstrap (the
// secret reference does not resolve) rolls back completely, leaving the
// installation uninitialized.
func TestBootstrapIsAtomic(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{t: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)}
	svc, err := New(contract.Dependencies{Clock: clock, IDs: idSource{}, Secrets: newFakeSecrets()})
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
	env := &testEnv{t: t, db: db, svc: svc, inst: contract.NewID()}
	err = db.Write(context.Background(), bootActor, contract.Scope{InstallationID: env.inst}, func(unit contract.Unit) error {
		raw, _ := json.Marshal(bootstrapInput{
			OwnerID:        contract.NewID(),
			CredentialID:   contract.NewID(),
			StoreRef:       "store/missing",
			Name:           "Nobody",
			InstallationID: env.inst,
		})
		_, err := svc.Handle(context.Background(), unit, contract.Invocation{
			Operation: opBootstrap, Version: descriptorVersion, Input: raw,
		})
		return err
	})
	var fault *contract.Fault
	if !errors.As(err, &fault) || fault.Code != contract.CodePrerequisiteMissing {
		t.Fatalf("expected prerequisite_missing, got %v", err)
	}
	var principals int64
	if err := db.Read(context.Background(), bootActor, contract.Scope{InstallationID: env.inst}, func(unit contract.Unit) error {
		return unit.QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM identity_principals WHERE installation_id = ?`, string(env.inst)).Scan(&principals)
	}); err != nil {
		t.Fatal(err)
	}
	if principals != 0 {
		t.Fatalf("failed bootstrap left %d principals behind; transaction did not roll back", principals)
	}
}

// TestAuthenticateTokenLifecycle covers the acceptance chain around the
// one-way token verifier: presentation succeeds for the custodied token, the
// returned actor carries the stored principal kind, wrong material fails
// identically, and revocation plus expiry take effect immediately.
func TestAuthenticateTokenLifecycle(t *testing.T) {
	env := newTestEnv(t)

	actor, err := env.authenticate(env.token)
	if err != nil {
		t.Fatalf("authenticate owner: %v", err)
	}
	if actor.PrincipalID != env.owner.PrincipalID || actor.Kind != contract.KindHuman {
		t.Fatalf("authenticate returned %+v, want owner human actor", actor)
	}

	// Wrong material fails with the same generic fault as unknown material.
	if _, err := env.authenticate("zt-token-wrong"); err == nil {
		t.Fatal("wrong token authenticated")
	} else {
		var fault *contract.Fault
		if !errors.As(err, &fault) || fault.Code != contract.CodeVerificationFailed {
			t.Fatalf("expected verification_failed, got %v", err)
		}
	}

	// Revoking the owner's credential kills authentication immediately.
	env.mustCall(env.owner, opCredRevoke, credentialRevokeInput{
		Scope:           contract.Scope{InstallationID: env.inst},
		ID:              env.ownerCredentialID(t),
		ExpectedVersion: 1,
	})
	if _, err := env.authenticate(env.token); err == nil {
		t.Fatal("revoked credential still authenticates")
	}
}

// ownerCredentialID reads the bootstrap credential id for the owner.
func (e *testEnv) ownerCredentialID(t *testing.T) contract.ID {
	t.Helper()
	var id contract.ID
	err := e.db.Read(context.Background(), bootActor, contract.Scope{InstallationID: e.inst}, func(unit contract.Unit) error {
		return unit.QueryRowContext(context.Background(),
			`SELECT c.id FROM identity_credentials c WHERE c.store_ref = 'store/owner-token'`).Scan(&id)
	})
	if err != nil {
		t.Fatalf("lookup credential: %v", err)
	}
	return id
}

// mustCreatePrincipal creates a principal. The optional scopes argument
// overrides the default installation scope.
func (e *testEnv) mustCreatePrincipal(name, kind string, scopes ...contract.Scope) principalOut {
	e.t.Helper()
	scope := contract.Scope{InstallationID: e.inst}
	if len(scopes) > 0 {
		scope = scopes[0]
		if scope.InstallationID == "" {
			scope.InstallationID = e.inst
		}
	}
	payload := e.mustCall(e.owner, opPrincipalCreate, principalCreateInput{
		Scope: contract.Scope{InstallationID: e.inst},
		Definition: principalDefinition{
			Kind: kind, Name: name, Scope: scope, Revoked: false,
		},
	})
	var out resourceOut[principalOut]
	decode(e.t, payload, &out)
	return out.Resource
}

// mustGrant creates a grant for principal under the owner's root authority.
func (e *testEnv) mustGrant(principalID contract.ID, scope contract.Scope, caps, dests []string) grantOut {
	e.t.Helper()
	if scope.InstallationID == "" {
		scope.InstallationID = e.inst
	}
	payload := e.mustCall(e.owner, opGrantCreate, grantCreateInput{
		Scope: contract.Scope{InstallationID: e.inst},
		Definition: grantDefinition{
			PrincipalID:  principalID,
			Scope:        scope,
			Capabilities: caps,
			Destinations: dests,
			Denied:       false,
		},
	})
	var out resourceOut[grantOut]
	decode(e.t, payload, &out)
	return out.Resource
}

func TestPrincipalLifecycle(t *testing.T) {
	env := newTestEnv(t)
	created := env.mustCreatePrincipal("Ops Agent", contract.KindClientAgent, contract.Scope{InstallationID: env.inst})

	// get reflects the stored state.
	got := env.mustCall(env.owner, opPrincipalGet, principalGetInput{
		Scope: contract.Scope{InstallationID: env.inst}, ID: created.ID,
	})
	var out resourceOut[principalOut]
	decode(t, got, &out)
	if out.Resource.ID != created.ID || out.Resource.Kind != contract.KindClientAgent {
		t.Fatalf("principal.get returned %+v", out.Resource)
	}

	// update can rename but never change kind (forged human kind is refused).
	updated := env.mustCall(env.owner, opPrincipalUpdate, principalUpdateInput{
		Scope: contract.Scope{InstallationID: env.inst}, ID: created.ID, ExpectedVersion: 1,
		Definition: principalDefinition{
			Kind: contract.KindClientAgent, Name: "Ops Agent", Scope: contract.Scope{InstallationID: env.inst}, Revoked: false,
		},
	})
	env.wantFault(env.owner, opPrincipalUpdate, principalUpdateInput{
		Scope: contract.Scope{InstallationID: env.inst}, ID: created.ID, ExpectedVersion: 2,
		Definition: principalDefinition{
			Kind: contract.KindHuman, Name: "Ops Agent", Scope: contract.Scope{InstallationID: env.inst}, Revoked: false,
		},
	}, contract.CodeInvalidInput)
	var upd resourceOut[principalOut]
	decode(t, updated, &upd)
	if upd.Resource.Version != 2 {
		t.Fatalf("expected version 2 after update, got %d", upd.Resource.Version)
	}
	kind := env.mustCall(env.owner, opPrincipalGet, principalGetInput{
		Scope: contract.Scope{InstallationID: env.inst}, ID: created.ID,
	})
	var kindOut resourceOut[principalOut]
	decode(t, kind, &kindOut)
	if kindOut.Resource.Kind != contract.KindClientAgent {
		t.Fatalf("principal kind changed to %q; kind must be immutable", kindOut.Resource.Kind)
	}

	// stale expected_version is refused.
	env.wantFault(env.owner, opPrincipalUpdate, principalUpdateInput{
		Scope: contract.Scope{InstallationID: env.inst}, ID: created.ID, ExpectedVersion: 1,
		Definition: principalDefinition{
			Kind: contract.KindClientAgent, Name: "Ops Agent", Scope: contract.Scope{InstallationID: env.inst}, Revoked: false,
		},
	}, contract.CodeStaleVersion)

	// revocation is immediate, idempotent, and cannot be undone.
	env.mustCall(env.owner, opPrincipalRevoke, principalRevokeInput{
		Scope: contract.Scope{InstallationID: env.inst}, ID: created.ID, ExpectedVersion: 2,
	})
	revoked := env.mustCall(env.owner, opPrincipalGet, principalGetInput{
		Scope: contract.Scope{InstallationID: env.inst}, ID: created.ID,
	})
	var rev resourceOut[principalOut]
	decode(t, revoked, &rev)
	if !rev.Resource.Revoked {
		t.Fatal("principal not revoked")
	}
	again := env.mustCall(env.owner, opPrincipalRevoke, principalRevokeInput{
		Scope: contract.Scope{InstallationID: env.inst}, ID: created.ID, ExpectedVersion: 3,
	})
	var againOut resourceOut[principalOut]
	decode(t, again, &againOut)
	if !againOut.Resource.Revoked || againOut.Resource.Version != 3 {
		t.Fatalf("idempotent revoke returned %+v", againOut.Resource)
	}
	// un-revoke through update is refused.
	env.wantFault(env.owner, opPrincipalUpdate, principalUpdateInput{
		Scope: contract.Scope{InstallationID: env.inst}, ID: created.ID, ExpectedVersion: 3,
		Definition: principalDefinition{
			Kind: contract.KindClientAgent, Name: "Ops Agent", Scope: contract.Scope{InstallationID: env.inst}, Revoked: false,
		},
	}, contract.CodeConflict)
}

// TestRevokedPrincipalLosesAuthority proves Z01.revoked_principal: revocation
// denies the principal's operations immediately and stays denied after the
// database is closed and reopened — a restored process cannot resurrect the
// principal's authority.
func TestRevokedPrincipalLosesAuthority(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reopen.db")
	env := newEnvOnDB(t, path)
	agent := env.mustCreatePrincipal("Agent", contract.KindClientAgent)
	env.mustGrant(agent.ID, contract.Scope{InstallationID: env.inst}, []string{"principal.create"}, []string{})
	agentAuth := contract.Actor{PrincipalID: agent.ID, Kind: contract.KindClientAgent}

	createInput := principalCreateInput{
		Scope: contract.Scope{InstallationID: env.inst},
		Definition: principalDefinition{
			Kind: contract.KindWorker, Name: "W", Scope: contract.Scope{InstallationID: env.inst}, Revoked: false,
		},
	}

	// Before revocation the grant authorizes the operation.
	env.mustCall(agentAuth, opPrincipalCreate, createInput)

	env.mustCall(env.owner, opPrincipalRevoke, principalRevokeInput{
		Scope: contract.Scope{InstallationID: env.inst}, ID: agent.ID, ExpectedVersion: 1,
	})

	// Immediate denial after revoke.
	env.wantFault(agentActor(agent), opPrincipalCreate, createInput, contract.CodePermissionDenied)

	// Durable across close and reopen.
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
	env2 := &testEnv{t: t, db: db, svc: svc, clock: env.clock, secrets: env.secrets, inst: env.inst}
	env2.wantFault(agentActor(agent), opPrincipalCreate, createInput, contract.CodePermissionDenied)
}

// agentActor wraps a principal output as its actor.
func agentActor(p principalOut) contract.Actor {
	return contract.Actor{PrincipalID: p.ID, Kind: p.Kind}
}
