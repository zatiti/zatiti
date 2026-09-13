package installation

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Z03.bootstrap_parity: init composes identity, configuration, memory and
// messaging in one transaction, returns metadata only (no owner token), and
// leaves paid execution unavailable (a provider-less chief).
func TestBootstrapComposesEveryOwnerAndReturnsNoToken(t *testing.T) {
	e := newEnv(t)
	scope := contract.Scope{InstallationID: e.install}
	st, err := e.bootstrap(scope, initInput{CredentialStore: "os", OwnerName: "Ada Owner"})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if !st.Initialized || st.Paused || st.Maintenance {
		t.Fatalf("unexpected status after bootstrap: %+v", st)
	}
	if st.InstallationID != e.install {
		t.Fatalf("status installation_id = %s, want %s", st.InstallationID, e.install)
	}
	if st.Version != 1 {
		t.Fatalf("status version = %d, want 1", st.Version)
	}

	for _, op := range []string{peerIdentityBootstrap, peerConfigurationBootstrap, peerMemoryBootstrap, peerMessagingBootstrap} {
		if len(e.ports.callsOf(op)) != 1 {
			t.Fatalf("expected exactly one %s call, got %d", op, len(e.ports.callsOf(op)))
		}
	}

	// Provider-less chief: configuration.bootstrap was called with no
	// execution profile input at all -- bootstrap never configures a paid
	// model. The chief the fake peer returns also carries a null profile.
	confCalls := e.ports.callsOf(peerConfigurationBootstrap)
	var confIn configurationBootstrapInput
	if err := json.Unmarshal(confCalls[0].Input, &confIn); err != nil {
		t.Fatalf("decode configuration.bootstrap input: %v", err)
	}
	if confIn.OwnerID == "" || confIn.OrganizationID == "" || confIn.ChiefID == "" {
		t.Fatalf("configuration.bootstrap input missing identities: %+v", confIn)
	}

	// No owner token anywhere in the result: only Status fields are present.
	raw, _ := json.Marshal(st)
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("re-marshal status: %v", err)
	}
	for _, forbidden := range []string{"token", "secret", "credential", "store_ref"} {
		if _, ok := generic[forbidden]; ok {
			t.Fatalf("bootstrap result unexpectedly carries field %q", forbidden)
		}
	}

	// The secret store custodied exactly one owner credential; its bytes
	// never appear in the identity.bootstrap call except as an opaque
	// store_ref.
	idCalls := e.ports.callsOf(peerIdentityBootstrap)
	var idIn identityBootstrapInput
	if err := json.Unmarshal(idCalls[0].Input, &idIn); err != nil {
		t.Fatalf("decode identity.bootstrap input: %v", err)
	}
	if idIn.StoreRef == "" {
		t.Fatalf("identity.bootstrap input carries no store_ref")
	}
	if _, err := e.secrets.Get(e.ctx, idIn.StoreRef); err != nil {
		t.Fatalf("owner secret was not actually custodied under its store_ref: %v", err)
	}
}

// Reinitialization must be refused once bootstrap has completed, even though
// every attempt mints an unrelated fresh installation_id (as the real
// application dispatcher does for every submission-key-less mutation).
func TestBootstrapRefusesReinitialization(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()

	secondScope := contract.Scope{InstallationID: e.ids.New()}
	_, err := e.bootstrap(secondScope, initInput{CredentialStore: "os", OwnerName: "Someone Else"})
	if err == nil {
		t.Fatalf("expected reinitialization to be refused")
	}
	var f *contract.Fault
	if !errors.As(err, &f) || f.Code != contract.CodeConflict {
		t.Fatalf("reinitialization error = %v, want a conflict fault", err)
	}
	if len(e.ports.callsOf(peerIdentityBootstrap)) != 1 {
		t.Fatalf("a refused reinitialization must not touch identity")
	}
}

// A crash between the trusted helper (which custodies the owner secret
// outside any transaction) and the bootstrap transaction leaves an opaque
// pending intent. The next init attempt must detect and retire it, then
// succeed under its own (different) minted installation_id, without ever
// completing two installations or leaking the abandoned attempt's secret.
func TestBootstrapRecoversFromCrashBetweenHelperAndTransaction(t *testing.T) {
	e := newEnv(t)

	firstScope := contract.Scope{InstallationID: e.ids.New()}
	firstInv := contract.Invocation{Operation: opInit, Version: 1}
	raw, err := json.Marshal(initInput{CredentialStore: "os", OwnerName: "Crashed Owner"})
	if err != nil {
		t.Fatalf("marshal init input: %v", err)
	}
	firstInv.Input = raw

	var plan contract.IOPlan
	if err := e.db.Write(e.ctx, e.actor, firstScope, func(unit contract.Unit) error {
		p, err := e.svc.Prepare(e.ctx, unit, firstInv)
		if err != nil {
			return err
		}
		plan = p
		return nil
	}); err != nil {
		t.Fatalf("first attempt Prepare: %v", err)
	}
	if _, err := e.svc.Perform(e.ctx, plan); err != nil {
		t.Fatalf("first attempt Perform: %v", err)
	}
	// The process "crashes" here: Finish never runs. One pending, opaque,
	// tokenless intent row is all that remains.

	var pendingCount int64
	if err := e.db.Read(e.ctx, e.actor, firstScope, func(unit contract.Unit) error {
		return unit.QueryRowContext(e.ctx,
			`SELECT COUNT(*) FROM installation_bootstrap_intents WHERE state = 'pending'`).Scan(&pendingCount)
	}); err != nil {
		t.Fatalf("count pending intents: %v", err)
	}
	if pendingCount != 1 {
		t.Fatalf("pending intents after crash = %d, want 1", pendingCount)
	}

	secondScope := contract.Scope{InstallationID: e.ids.New()}
	st, err := e.bootstrap(secondScope, initInput{CredentialStore: "os", OwnerName: "Recovered Owner"})
	if err != nil {
		t.Fatalf("second attempt bootstrap: %v", err)
	}
	if st.InstallationID != secondScope.InstallationID {
		t.Fatalf("recovered installation_id = %s, want %s", st.InstallationID, secondScope.InstallationID)
	}
	if st.InstallationID == firstScope.InstallationID {
		t.Fatalf("recovered installation reused the crashed attempt's id")
	}

	var states []string
	if err := e.db.Read(e.ctx, e.actor, secondScope, func(unit contract.Unit) error {
		rows, err := unit.QueryContext(e.ctx, `SELECT state FROM installation_bootstrap_intents ORDER BY created_at`)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				return err
			}
			states = append(states, s)
		}
		return rows.Err()
	}); err != nil {
		t.Fatalf("read intent states: %v", err)
	}
	if len(states) != 2 || states[0] != "abandoned" || states[1] != "completed" {
		t.Fatalf("intent states = %v, want [abandoned completed]", states)
	}

	// A third attempt is refused: the destination is now truly initialized.
	thirdScope := contract.Scope{InstallationID: e.ids.New()}
	if _, err := e.bootstrap(thirdScope, initInput{CredentialStore: "os", OwnerName: "Third"}); err == nil {
		t.Fatalf("expected a third attempt to be refused after successful recovery")
	}
}

// A peer refusal during Finish also leaves the intent "pending" (the same
// recoverable trace a crash would), rather than a silently completed or
// silently lost bootstrap.
func TestBootstrapPeerFailureLeavesRecoverablePendingIntent(t *testing.T) {
	e := newEnv(t)
	e.ports.set(peerConfigurationBootstrap, func(contract.Invocation) (contract.Payload, error) {
		return failPayload(contract.CodeConflict, "configuration refused")
	})

	scope := contract.Scope{InstallationID: e.ids.New()}
	if _, err := e.bootstrap(scope, initInput{CredentialStore: "os", OwnerName: "Doomed Owner"}); err == nil {
		t.Fatalf("expected the doomed attempt to fail")
	}

	var pendingCount int64
	if err := e.db.Read(e.ctx, e.actor, scope, func(unit contract.Unit) error {
		return unit.QueryRowContext(e.ctx,
			`SELECT COUNT(*) FROM installation_bootstrap_intents WHERE state = 'pending'`).Scan(&pendingCount)
	}); err != nil {
		t.Fatalf("count pending intents: %v", err)
	}
	if pendingCount != 1 {
		t.Fatalf("pending intents after peer failure = %d, want 1", pendingCount)
	}

	var stateCount int64
	if err := e.db.Read(e.ctx, e.actor, scope, func(unit contract.Unit) error {
		return unit.QueryRowContext(e.ctx, `SELECT COUNT(*) FROM installation_state`).Scan(&stateCount)
	}); err != nil {
		t.Fatalf("count installation_state: %v", err)
	}
	if stateCount != 0 {
		t.Fatalf("a doomed attempt must never commit installation_state")
	}

	delete(e.ports.handlers, peerConfigurationBootstrap)
	secondScope := contract.Scope{InstallationID: e.ids.New()}
	if _, err := e.bootstrap(secondScope, initInput{CredentialStore: "os", OwnerName: "Recovered"}); err != nil {
		t.Fatalf("recovery attempt after peer failure: %v", err)
	}
}

func TestBootstrapRequiresHeadlessKeyRefForHeadlessStore(t *testing.T) {
	e := newEnv(t)
	scope := contract.Scope{InstallationID: e.ids.New()}
	_, err := e.bootstrap(scope, initInput{CredentialStore: "headless", OwnerName: "Headless Owner"})
	if err == nil {
		t.Fatalf("expected missing headless_key_ref to be refused")
	}
	var f *contract.Fault
	if !errors.As(err, &f) || f.Code != contract.CodePrerequisiteMissing {
		t.Fatalf("error = %v, want prerequisite_missing", err)
	}
}

func TestBootstrapMissingSecretStoreFailsHonestly(t *testing.T) {
	e := newEnv(t)
	svc, err := New(contract.Dependencies{Clock: e.clock, IDs: e.ids, Ports: e.ports})
	if err != nil {
		t.Fatalf("New without secrets: %v", err)
	}
	e.svc = svc
	scope := contract.Scope{InstallationID: e.ids.New()}
	_, err = e.bootstrap(scope, initInput{CredentialStore: "os", OwnerName: "No Secrets"})
	if err == nil {
		t.Fatalf("expected bootstrap without a secret store to fail")
	}
	var f *contract.Fault
	if !errors.As(err, &f) || f.Code != contract.CodePrerequisiteMissing {
		t.Fatalf("error = %v, want prerequisite_missing", err)
	}
}
