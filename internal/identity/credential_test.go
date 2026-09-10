package identity

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/storage"
)

// TestCredentialCustody proves Z01.credential_custody: credential operations
// return only opaque references and permitted metadata; the raw token and its
// digest never leave the database row; and a token that already exists — live
// or revoked — can never be re-registered, so a restored backup cannot
// resurrect authority with replayed material.
func TestCredentialCustody(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	agent := env.mustCreatePrincipal("Agent", contract.KindClientAgent)
	agentToken := "zt-agent-token-" + string(contract.NewID())
	if _, err := env.secrets.Put(ctx, "store/agent-token", []byte(agentToken)); err != nil {
		t.Fatal(err)
	}

	payload := env.mustCall(env.owner, opCredProvision, credentialProvisionInput{
		Scope:       contract.Scope{InstallationID: env.inst},
		PrincipalID: agent.ID,
		StoreRef:    "store/agent-token",
	})
	var out resourceOut[credentialOut]
	decode(t, payload, &out)
	if out.Resource.PrincipalID != agent.ID || out.Resource.StoreRef != "store/agent-token" || out.Resource.Revoked {
		t.Fatalf("provision returned unexpected metadata: %+v", out.Resource)
	}

	// The wire shape carries only permitted metadata: no digest, no material.
	var keys map[string]json.RawMessage
	resourceJSON, err := json.Marshal(out.Resource)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(resourceJSON, &keys); err != nil {
		t.Fatal(err)
	}
	for k := range keys {
		switch k {
		case "id", "version", "principal_id", "store_ref", "revoked":
		default:
			t.Fatalf("provision output exposes unauthorized field %q", k)
		}
	}
	digest := string(contract.Hash([]byte(agentToken)))
	body := string(payload.Data)
	if strings.Contains(body, agentToken) {
		t.Fatal("provision output leaks raw credential material")
	}
	if strings.Contains(body, digest) {
		t.Fatal("provision output leaks the token digest")
	}

	// The stored row carries only the one-way digest, never the bytes.
	var storedDigest, storedRef string
	err = env.db.Read(ctx, bootActor, contract.Scope{InstallationID: env.inst}, func(unit contract.Unit) error {
		return unit.QueryRowContext(ctx,
			`SELECT token_digest, store_ref FROM identity_credentials WHERE id = ?`, string(out.Resource.ID)).
			Scan(&storedDigest, &storedRef)
	})
	if err != nil {
		t.Fatal(err)
	}
	if storedRef != "store/agent-token" {
		t.Fatalf("store_ref = %q", storedRef)
	}
	if storedDigest != digest || len(digest) != 64 {
		t.Fatalf("token_digest does not equal the one-way hash of the custodied bytes: %q", storedDigest)
	}

	// The same material under a second reference is a conflict: the digest is
	// unique across the installation.
	if _, err := env.secrets.Put(ctx, "store/copy", []byte(agentToken)); err != nil {
		t.Fatal(err)
	}
	env.wantFault(env.owner, opCredProvision, credentialProvisionInput{
		Scope:       contract.Scope{InstallationID: env.inst},
		PrincipalID: agent.ID,
		StoreRef:    "store/copy",
	}, contract.CodeConflict)

	// Revoking the credential does not release its token: re-registration of
	// the revoked token is refused, so a restored backup cannot replay it.
	env.mustCall(env.owner, opCredRevoke, credentialRevokeInput{
		Scope: contract.Scope{InstallationID: env.inst}, ID: out.Resource.ID, ExpectedVersion: 1,
	})
	env.wantFault(env.owner, opCredProvision, credentialProvisionInput{
		Scope:       contract.Scope{InstallationID: env.inst},
		PrincipalID: agent.ID,
		StoreRef:    "store/agent-token",
	}, contract.CodeConflict)

	// The revocation record is immutable append-only history.
	var revocations int64
	if err := env.db.Read(ctx, bootActor, contract.Scope{InstallationID: env.inst}, func(unit contract.Unit) error {
		return unit.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM identity_revocations WHERE entity_kind = 'credential' AND entity_id = ?`,
			string(out.Resource.ID)).Scan(&revocations)
	}); err != nil {
		t.Fatal(err)
	}
	if revocations != 1 {
		t.Fatalf("expected one revocation record, found %d", revocations)
	}
}

// TestExpiredCredentialFailsAuthentication proves an expired credential stops
// authenticating immediately: expiry is rechecked against the clock on every
// authentication, and material that expired before provisioning never works.
func TestExpiredCredentialFailsAuthentication(t *testing.T) {
	env := newTestEnv(t)
	agent := env.mustCreatePrincipal("Agent", contract.KindClientAgent)
	token := "zt-agent-token-" + string(contract.NewID())
	if _, err := env.secrets.Put(context.Background(), "store/agent-token", []byte(token)); err != nil {
		t.Fatal(err)
	}
	expires := env.clock.Now().Add(time.Hour)
	env.mustCall(env.owner, opCredProvision, credentialProvisionInput{
		Scope:       contract.Scope{InstallationID: env.inst},
		PrincipalID: agent.ID,
		StoreRef:    "store/agent-token",
		ExpiresAt:   &expires,
	})

	actor, err := env.authenticate(token)
	if err != nil {
		t.Fatalf("authenticate inside the validity window: %v", err)
	}
	if actor.PrincipalID != agent.ID {
		t.Fatalf("authenticated as %+v, want the agent", actor)
	}

	// Advance past expiry: authentication fails like any other failure.
	env.clock.Add(2 * time.Hour)
	if _, err := env.authenticate(token); err == nil {
		t.Fatal("expired credential still authenticates")
	} else {
		var fault *contract.Fault
		if !errors.As(err, &fault) || fault.Code != contract.CodeVerificationFailed {
			t.Fatalf("expected verification_failed, got %v", err)
		}
	}

	// Material whose expiry already passed at provisioning never works.
	stale := env.clock.Now().Add(-time.Hour)
	if _, err := env.secrets.Put(context.Background(), "store/stale-token", []byte("zt-stale-token")); err != nil {
		t.Fatal(err)
	}
	env.mustCall(env.owner, opCredProvision, credentialProvisionInput{
		Scope:       contract.Scope{InstallationID: env.inst},
		PrincipalID: agent.ID,
		StoreRef:    "store/stale-token",
		ExpiresAt:   &stale,
	})
	if _, err := env.authenticate("zt-stale-token"); err == nil {
		t.Fatal("credential expiring in the past authenticates")
	}
}

// TestSecretStoreRequired proves the nil secret store fails closed:
// bootstrap cannot initialize an installation, and provisioning cannot
// register material, both with prerequisite_missing and no partial state.
func TestSecretStoreRequired(t *testing.T) {
	ctx := context.Background()
	// Bootstrap without a secret store: refused, nothing persists.
	svc, err := New(contract.Dependencies{Clock: &fakeClock{t: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)}, IDs: idSource{}})
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
	inst := contract.NewID()
	err = db.Write(ctx, bootActor, contract.Scope{InstallationID: inst}, func(unit contract.Unit) error {
		raw, _ := json.Marshal(bootstrapInput{
			OwnerID:        contract.NewID(),
			CredentialID:   contract.NewID(),
			StoreRef:       "store/owner-token",
			Name:           "Ada Owner",
			InstallationID: inst,
		})
		_, err := svc.Handle(ctx, unit, contract.Invocation{
			Operation: opBootstrap, Version: descriptorVersion, Input: raw,
		})
		return err
	})
	var fault *contract.Fault
	if !errors.As(err, &fault) || fault.Code != contract.CodePrerequisiteMissing {
		t.Fatalf("bootstrap without a secret store: expected prerequisite_missing, got %v", err)
	}
	var principals int64
	if err := db.Read(ctx, bootActor, contract.Scope{InstallationID: inst}, func(unit contract.Unit) error {
		return unit.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM identity_principals WHERE installation_id = ?`, string(inst)).Scan(&principals)
	}); err != nil {
		t.Fatal(err)
	}
	if principals != 0 {
		t.Fatalf("failed bootstrap left %d principals behind", principals)
	}

	// Provisioning through a service without a secret store also fails closed.
	env := newTestEnv(t)
	bare, err := New(contract.Dependencies{Clock: env.clock, IDs: idSource{}})
	if err != nil {
		t.Fatal(err)
	}
	agent := env.mustCreatePrincipal("Agent", contract.KindClientAgent)
	err = env.db.Write(ctx, env.owner, contract.Scope{InstallationID: env.inst}, func(unit contract.Unit) error {
		raw, _ := json.Marshal(credentialProvisionInput{
			Scope:       contract.Scope{InstallationID: env.inst},
			PrincipalID: agent.ID,
			StoreRef:    "store/agent-token",
		})
		_, err := bare.Handle(ctx, unit, contract.Invocation{
			Operation: opCredProvision, Version: descriptorVersion, Input: raw,
		})
		return err
	})
	if !errors.As(err, &fault) || fault.Code != contract.CodePrerequisiteMissing {
		t.Fatalf("provision without a secret store: expected prerequisite_missing, got %v", err)
	}
}

// TestCredentialRevocationSurvivesReopen proves revoked restore semantics for
// credentials: after revocation, closing and reopening the database leaves the
// credential dead and its token unregistrable — a restored process inherits
// the revocation because revocation records are append-only.
func TestCredentialRevocationSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reopen.db")
	env := newEnvOnDB(t, path)
	agent := env.mustCreatePrincipal("Agent", contract.KindClientAgent)
	token := "zt-agent-token-" + string(contract.NewID())
	if _, err := env.secrets.Put(context.Background(), "store/agent-token", []byte(token)); err != nil {
		t.Fatal(err)
	}
	payload := env.mustCall(env.owner, opCredProvision, credentialProvisionInput{
		Scope:       contract.Scope{InstallationID: env.inst},
		PrincipalID: agent.ID,
		StoreRef:    "store/agent-token",
	})
	var out resourceOut[credentialOut]
	decode(t, payload, &out)

	if _, err := env.authenticate(token); err != nil {
		t.Fatalf("authenticate before revoke: %v", err)
	}
	env.mustCall(env.owner, opCredRevoke, credentialRevokeInput{
		Scope: contract.Scope{InstallationID: env.inst}, ID: out.Resource.ID, ExpectedVersion: 1,
	})

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

	if _, err := env2.authenticate(token); err == nil {
		t.Fatal("revoked credential authenticates after reopen")
	}
	// The token still cannot be re-registered after the restore.
	env2.wantFault(env.owner, opCredProvision, credentialProvisionInput{
		Scope:       contract.Scope{InstallationID: env.inst},
		PrincipalID: agent.ID,
		StoreRef:    "store/agent-token",
	}, contract.CodeConflict)
}
