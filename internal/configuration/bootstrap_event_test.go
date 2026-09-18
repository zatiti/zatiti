package configuration

import (
	"encoding/json"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// eventsOfKind reads the storage outbox and returns the events of one kind.
func eventsOfKind(t *testing.T, env *testEnv, kind string) []contract.Event {
	t.Helper()
	all, err := env.db.Events(env.ctx, 0, 500)
	if err != nil {
		t.Fatalf("db.Events: %v", err)
	}
	var out []contract.Event
	for _, ev := range all {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

// TestBootstrapEmitsCreatedEntities: bootstrap creates the root organization
// and its chief, and each of those state transitions is appended to the
// outbox in the same transaction under the kind an applied create of the
// same entity carries, so an event reader reconstructs the bootstrap state
// exactly like any later organization or worker.
func TestBootstrapEmitsCreatedEntities(t *testing.T) {
	env := newEnv(t)

	for _, want := range []struct {
		kind     string
		resource contract.ID
		entity   string
	}{
		{"configuration.organization.create", env.org, kindOrganization},
		{"configuration.worker.create", env.chief, kindWorker},
	} {
		events := eventsOfKind(t, env, want.kind)
		if len(events) != 1 {
			t.Fatalf("%s events after bootstrap = %d, want exactly 1", want.kind, len(events))
		}
		ev := events[0]
		if ev.ResourceID != want.resource || ev.ResourceVersion != 1 {
			t.Fatalf("%s event names resource %s v%d, want %s v1", want.kind, ev.ResourceID, ev.ResourceVersion, want.resource)
		}
		if ev.Scope.InstallationID != env.install {
			t.Fatalf("%s event scope installation = %s, want %s", want.kind, ev.Scope.InstallationID, env.install)
		}
		var data struct {
			Type   string `json:"type"`
			Kind   string `json:"kind"`
			Action string `json:"action"`
		}
		if err := contract.DecodeStrict(ev.Data, &data); err != nil {
			t.Fatalf("%s event data %s: %v", want.kind, ev.Data, err)
		}
		if data.Type != want.kind || data.Kind != want.entity || data.Action != "create" {
			t.Fatalf("%s event data = %+v", want.kind, data)
		}
	}

	// A later applied worker carries the same kind and data shape, so the
	// two are one event vocabulary.
	workerID := env.createWorker(env.org, "after-bootstrap")
	created := eventsOfKind(t, env, "configuration.worker.create")
	if len(created) != 2 || created[1].ResourceID != workerID {
		t.Fatalf("configuration.worker.create events = %d, want the bootstrap chief and the applied worker", len(created))
	}
	var bootstrapData, appliedData map[string]json.RawMessage
	if err := json.Unmarshal(created[0].Data, &bootstrapData); err != nil {
		t.Fatalf("bootstrap event data: %v", err)
	}
	if err := json.Unmarshal(created[1].Data, &appliedData); err != nil {
		t.Fatalf("applied event data: %v", err)
	}
	if len(bootstrapData) != len(appliedData) {
		t.Fatalf("bootstrap event data %s and applied event data %s differ in shape", created[0].Data, created[1].Data)
	}
	for field := range appliedData {
		if _, ok := bootstrapData[field]; !ok {
			t.Fatalf("bootstrap event data %s lacks field %q of applied event data", created[0].Data, field)
		}
	}
}

// TestRefusedBootstrapEmitsNothing: a second bootstrap is refused and its
// transaction, events included, rolls back.
func TestRefusedBootstrapEmitsNothing(t *testing.T) {
	env := newEnv(t)
	before, err := env.db.Events(env.ctx, 0, 500)
	if err != nil {
		t.Fatalf("db.Events: %v", err)
	}
	_ = env.expectFault("_configuration.bootstrap", bootstrapIn{
		InstallationID: env.install, OwnerID: env.owner, OrganizationID: env.ids.New(), ChiefID: env.ids.New(),
	}, contract.CodeConflict)
	after, err := env.db.Events(env.ctx, 0, 500)
	if err != nil {
		t.Fatalf("db.Events: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("events after a refused bootstrap = %d, want %d", len(after), len(before))
	}
}
