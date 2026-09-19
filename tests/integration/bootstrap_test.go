package integration_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// TestBootstrapInitializesEveryOwnerAtomically (Z17 atomic organization and
// chief creation; stage-1 bootstrap gate): installation.init through the
// real application commits identity, the root organization and chief, the
// distinct memory brains, the pinned personal-chief conversation and the
// installation state together, and the custodied owner credential
// authenticates as the human owner.
func TestBootstrapInitializesEveryOwnerAtomically(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	if f.owner.Kind != contract.KindHuman {
		t.Fatalf("owner kind %q, want human", f.owner.Kind)
	}

	res := f.must(f.owner, "installation.status", "", map[string]any{"scope": f.scope()})
	var status struct {
		Resource struct {
			InstallationID contract.ID `json:"installation_id"`
			Initialized    bool        `json:"initialized"`
			Paused         bool        `json:"paused"`
			Generation     int64       `json:"generation"`
		} `json:"resource"`
	}
	decode(t, res.Data, &status)
	if !status.Resource.Initialized || status.Resource.Paused || status.Resource.InstallationID != f.installationID {
		t.Fatalf("status %s does not describe a running installation %s", res.Data, f.installationID)
	}
	if status.Resource.Generation != f.generation {
		t.Fatalf("status generation %d, assembly started generation %d", status.Resource.Generation, f.generation)
	}

	org, chief := f.rootOrganization()
	workers := f.must(f.owner, "worker.list", "", map[string]any{"scope": f.scope()})
	var wl struct {
		Items []struct {
			ID             contract.ID `json:"id"`
			OrganizationID contract.ID `json:"organization_id"`
			Profile        any         `json:"profile"`
		} `json:"items"`
	}
	decode(t, workers.Data, &wl)
	if len(wl.Items) != 1 || wl.Items[0].ID != chief || wl.Items[0].OrganizationID != org {
		t.Fatalf("workers %s, want exactly the chief %s of organization %s", workers.Data, chief, org)
	}
	if wl.Items[0].Profile != nil {
		t.Fatalf("bootstrap chief carries an execution profile; it must not be able to run paid work: %s", workers.Data)
	}

	conversations := f.must(f.owner, "conversation.list", "", map[string]any{"scope": f.scope()})
	var cl struct {
		Items []struct {
			Pinned       bool          `json:"pinned"`
			Participants []contract.ID `json:"participant_ids"`
		} `json:"items"`
	}
	decode(t, conversations.Data, &cl)
	if len(cl.Items) != 1 || !cl.Items[0].Pinned {
		t.Fatalf("conversations %s, want exactly the pinned personal-chief conversation", conversations.Data)
	}
	participants := map[contract.ID]bool{}
	for _, p := range cl.Items[0].Participants {
		participants[p] = true
	}
	if !participants[f.owner.PrincipalID] || !participants[chief] || len(participants) != 2 {
		t.Fatalf("pinned conversation participants %v, want owner %s and chief %s", cl.Items[0].Participants, f.owner.PrincipalID, chief)
	}

	bindings := f.must(f.owner, "memory.binding.list", "", map[string]any{"scope": f.scope()})
	var bl struct {
		Items []struct {
			BrainID contract.ID `json:"brain_id"`
		} `json:"items"`
	}
	decode(t, bindings.Data, &bl)
	brains := map[contract.ID]bool{}
	for _, b := range bl.Items {
		brains[b.BrainID] = true
	}
	if len(brains) != 3 {
		t.Fatalf("bootstrap bound %d distinct brains, want the separate personal-chief, root-organization and installation brains: %s", len(brains), bindings.Data)
	}

	// One bootstrap transaction: every owner's events are present, in one
	// increasing sequence that ends with the installation transition.
	kinds := f.eventKinds()
	owners := map[string]bool{}
	for _, k := range kinds {
		owners[strings.SplitN(k, ".", 2)[0]] = true
	}
	for _, owner := range []string{"identity", "memory", "messaging", "installation"} {
		if !owners[owner] {
			t.Errorf("bootstrap emitted no %s event: %v", owner, kinds)
		}
	}
	if last := kinds[len(kinds)-1]; !strings.HasPrefix(last, "installation.") {
		t.Errorf("last bootstrap event %q, want the installation transition", last)
	}
}

// TestBootstrapEmitsConfigurationEvidence: the root organization, chief and
// revision 1 are observable state transitions, so bootstrap owes them events
// like every other owner's bootstrap slice; an event-following client
// otherwise never learns the hierarchy exists.
func TestBootstrapEmitsConfigurationEvidence(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	kinds := f.eventKinds()
	for _, k := range kinds {
		if strings.HasPrefix(k, "configuration.") {
			return
		}
	}
	failRegressedDefect(t,
		"internal/configuration/handlers.go:979 handleBootstrap creates the root organization, chief and revision 1 without Unit.Emit; "+
			"only the compiler path emits (compiler.go:795 emitRevision)",
		"bootstrap events carry no configuration.* kind: "+strings.Join(kinds, " "))
}

// TestBootstrapReturnsMetadataOnly (Z13): the bootstrap result carries no
// credential bytes and no credential store reference.
func TestBootstrapReturnsMetadataOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	res, err := f.app.Invoke(context.Background(), bootstrapActor(), "installation.init",
		contract.Request{Schema: contract.SchemaRequest, Input: initInput()})
	if err != nil {
		t.Fatalf("installation.init: %v", err)
	}
	ref, ok := f.secrets.refFor(ownerKey)
	if !ok {
		t.Fatal("bootstrap custodied no owner credential")
	}
	token, err := f.secrets.Get(context.Background(), ref)
	if err != nil {
		t.Fatalf("owner credential: %v", err)
	}
	if len(token) < 32 {
		t.Fatalf("owner credential is %d bytes, want at least 32 bytes of entropy", len(token))
	}
	for name, leak := range map[string][]byte{
		"credential bytes": token,
		"credential hex":   []byte(hex.EncodeToString(token)),
		"store reference":  []byte(ref),
	} {
		if bytes.Contains(res.Data, leak) {
			t.Errorf("bootstrap result discloses the owner %s", name)
		}
	}
}

// TestBootstrapRunsExactlyOnce: a second installation.init is refused and
// changes nothing: same installation, same single owner, no new events.
func TestBootstrapRunsExactlyOnce(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	before := f.eventCount()
	_, err := f.app.Invoke(context.Background(), bootstrapActor(), "installation.init",
		contract.Request{Schema: contract.SchemaRequest, Input: initInput()})
	if faultCode(err) != contract.CodeConflict {
		t.Fatalf("second installation.init: %v, want conflict", err)
	}
	if after := f.eventCount(); after != before {
		t.Fatalf("refused bootstrap changed the event log from %d to %d events", before, after)
	}
	f.expectPrincipals()
	// The application still resolves the original installation.
	f.must(f.owner, "installation.status", "", map[string]any{"scope": f.scope()})
}

// TestSecondControllerIsRefused (Z01 duplicate controller): a second
// assembly on a served state directory cannot take installation ownership.
func TestSecondControllerIsRefused(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	_, err := assemble(t, fixtureOptions{stateDir: f.stateDir, keyRef: f.keyRef})
	if err == nil {
		t.Fatal("a second controller assembled on a state directory that is already served")
	}
	if !strings.Contains(err.Error(), "already served") {
		t.Fatalf("second controller failed with %v, want the ownership refusal", err)
	}
	// The first controller is undisturbed.
	f.must(f.owner, "installation.status", "", map[string]any{"scope": f.scope()})
}

// TestRestartKeepsStateAndAdvancesGeneration (stage-1 clean restart gate): a
// controller restart on the same state directory keeps the installation
// identity, the owner credential and retained commands, and advances the
// generation fence exactly once.
func TestRestartKeepsStateAndAdvancesGeneration(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	token := f.ownerToken()
	created := f.must(f.owner, "principal.create", "restart-1", map[string]any{
		"scope": f.scope(), "definition": map[string]any{
			"kind": "client_agent", "name": "restart-agent", "scope": f.scope(), "revoked": false},
	})
	installation, generation, stateDir, keyRef := f.installationID, f.generation, f.stateDir, f.keyRef
	f.close()

	g, err := assemble(t, fixtureOptions{stateDir: stateDir, keyRef: keyRef})
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if g.generation != generation+1 {
		t.Fatalf("restart generation %d, want %d", g.generation, generation+1)
	}
	g.installationID = installation
	g.owner = g.authenticate(token)
	if g.owner.PrincipalID != f.owner.PrincipalID {
		t.Fatalf("restart authenticated principal %s, want the owner %s", g.owner.PrincipalID, f.owner.PrincipalID)
	}
	// Submission keys survive restart: the identical request replays the
	// retained command instead of creating a second principal.
	replay := g.must(g.owner, "principal.create", "restart-1", map[string]any{
		"scope": g.scope(), "definition": map[string]any{
			"kind": "client_agent", "name": "restart-agent", "scope": g.scope(), "revoked": false},
	})
	if replay.CommandID != created.CommandID {
		t.Fatalf("replay after restart returned command %s, the original was %s", replay.CommandID, created.CommandID)
	}
	g.expectPrincipals("restart-agent")
}
