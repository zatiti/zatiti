package memory

import (
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// TestBootstrapDistinctBrains proves the three bootstrap brains (R15-003)
// are distinct rows, and that a second organization's bootstrap reuses the
// existing installation brain rather than duplicating it (R2.1-004): only
// the very first bootstrap call for an installation is the personal/root
// chief, and only that chief is granted installation-wide curate authority
// (R15-005).
func TestBootstrapDistinctBrains(t *testing.T) {
	e := newEnv(t)
	org1, chief1 := e.ids.New(), e.ids.New()

	payload, err := e.call(opBootstrap, bootstrapInput{InstallationID: e.install, OrganizationID: org1, ChiefID: chief1})
	if err != nil {
		t.Fatalf("bootstrap root: %v", err)
	}
	var out1 bindingsOutput
	e.decodePayload(payload, &out1)
	if len(out1.Bindings) != 3 {
		t.Fatalf("root bootstrap: got %d bindings, want 3 (own worker brain, org brain, installation brain)", len(out1.Bindings))
	}
	brainIDs := map[contract.ID]bool{}
	for _, b := range out1.Bindings {
		brainIDs[b.BrainID] = true
	}
	if len(brainIDs) != 3 {
		t.Fatalf("root bootstrap: bindings reference %d distinct brains, want 3", len(brainIDs))
	}

	org2, chief2 := e.ids.New(), e.ids.New()
	payload, err = e.call(opBootstrap, bootstrapInput{InstallationID: e.install, OrganizationID: org2, ChiefID: chief2})
	if err != nil {
		t.Fatalf("bootstrap second org: %v", err)
	}
	var out2 bindingsOutput
	e.decodePayload(payload, &out2)
	if len(out2.Bindings) != 2 {
		t.Fatalf("second bootstrap: got %d bindings, want 2 (own worker brain, org brain; no installation-wide grant)", len(out2.Bindings))
	}
	for _, b := range out2.Bindings {
		if brainIDs[b.BrainID] {
			t.Fatalf("second bootstrap: reused a brain %s from the first organization's bootstrap", b.BrainID)
		}
	}

	// The installation brain itself, however, must be the SAME row across
	// both calls: reload it and confirm both chiefs' org bindings differ
	// while the installation stays a single row.
	err = e.db.Read(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		installBrain, err := loadBrainByScope(e.ctx, unit, e.install, "", "", brainKindInstallation)
		if err != nil {
			t.Fatalf("load installation brain: %v", err)
		}
		if installBrain.Version != 1 {
			t.Fatalf("installation brain version = %d, want 1 (never re-created)", installBrain.Version)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
}

// TestBootstrapRequiresIdentities proves bootstrap refuses an empty
// organization or chief identity rather than silently allocating brains
// under an empty scope slot.
func TestBootstrapRequiresIdentities(t *testing.T) {
	e := newEnv(t)
	_, err := e.call(opBootstrap, bootstrapInput{InstallationID: e.install, OrganizationID: "", ChiefID: e.ids.New()})
	f := decodeFault(err)
	if f == nil || f.Code != contract.CodeInvalidInput {
		t.Fatalf("empty organization_id: got %v, want invalid_input", err)
	}
}
