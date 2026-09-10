package skills

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Query operations: scope fencing without cross-installation disclosure,
// strict filter validation, keyset pagination over HMAC-sealed cursors, and
// the archive staging operation.

// decodeList decodes a skill.list payload body.
func (e *testEnv) decodeList(payload contract.Payload) []wireSkill {
	e.t.Helper()
	var out listItems
	e.decode(payload.Data, &out)
	return out.Items
}

func TestGetUnknownSkill(t *testing.T) {
	e := newEnv(t)
	e.expectFault("skill.get", skillGetInput{Scope: e.scope, ID: e.ids.New()}, contract.CodeNotFound)
}

func TestGetCrossScopeNoDisclosure(t *testing.T) {
	e := newEnv(t)
	payload, err := e.runImport(e.importFixture(validArchive()))
	if err != nil || payload.Error != nil {
		e.t.Fatalf("import failed: %v %v", err, payload.Error)
	}
	var out importOutput
	e.decode(payload.Data, &out)

	// A different installation's scope must not surface the resource.
	other := wireScope{InstallationID: e.ids.New()}
	e.expectFaultAs("skill.get", skillGetInput{Scope: other, ID: out.Resource.ID}, contract.CodeNotFound, other)

	// An input scope that disagrees with the transaction scope is refused
	// outright.
	e.expectFault("skill.get", skillGetInput{Scope: other, ID: out.Resource.ID}, contract.CodePermissionDenied)
}

// expectFaultAs runs one operation under a different transaction scope.
func (e *testEnv) expectFaultAs(op string, in any, code string, scope wireScope) {
	e.t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		e.t.Fatalf("marshal input for %s: %v", op, err)
	}
	var payload contract.Payload
	err = e.db.Write(e.ctx, e.actor, scope.toContract(), func(unit contract.Unit) error {
		p, err := e.svc.Handle(e.ctx, unit, contract.Invocation{Operation: op, Version: 1, Input: raw})
		payload = p
		return err
	})
	var f *contract.Fault
	if err != nil {
		if !errors.As(err, &f) {
			e.t.Fatalf("%s: %v", op, err)
		}
	}
	if f == nil {
		f = payload.Error
	}
	if f == nil || f.Code != code {
		e.t.Fatalf("%s: fault %v, want %s", op, f, code)
	}
}

func TestListOrderingAndFilter(t *testing.T) {
	e := newEnv(t)
	e.seedSkill("beta", 1, "draft")
	e.seedSkill("alpha", 1, "active")
	e.seedSkill("alpha", 2, "draft")

	t.Run("ordered by name then version", func(t *testing.T) {
		payload := e.mustOK("skill.list", skillListInput{Scope: e.scope})
		items := e.decodeList(payload)
		if len(items) != 3 {
			t.Fatalf("items = %d, want 3", len(items))
		}
		want := []string{"alpha", "alpha", "beta"}
		for i, name := range want {
			if items[i].Name != name {
				t.Fatalf("item %d = %s, want %s", i, items[i].Name, name)
			}
		}
		if items[0].Version != 1 || items[1].Version != 2 {
			t.Fatalf("versions = %d,%d, want 1,2", items[0].Version, items[1].Version)
		}
	})

	t.Run("state filter", func(t *testing.T) {
		draft := "draft"
		payload := e.mustOK("skill.list", skillListInput{Scope: e.scope, Filter: &skillListFilter{State: &draft}})
		items := e.decodeList(payload)
		if len(items) != 2 {
			t.Fatalf("draft items = %d, want 2", len(items))
		}
		for _, it := range items {
			if it.Name == "alpha" && it.Version != 2 {
				t.Fatalf("filtered out version %d surfaced", it.Version)
			}
		}
	})

	t.Run("unsupported filter field", func(t *testing.T) {
		key := "nope"
		e.expectFault("skill.list", skillListInput{Scope: e.scope, Filter: &skillListFilter{Key: &key}},
			contract.CodeInvalidInput)
	})

	t.Run("unknown state value", func(t *testing.T) {
		state := "published"
		e.expectFault("skill.list", skillListInput{Scope: e.scope, Filter: &skillListFilter{State: &state}},
			contract.CodeInvalidInput)
	})
}

func TestListPagination(t *testing.T) {
	e := newEnv(t)
	e.seedSkill("alpha", 1, "draft")
	e.seedSkill("beta", 1, "draft")
	e.seedSkill("gamma", 1, "draft")

	limit := int64(2)
	first := e.mustOK("skill.list", skillListInput{Scope: e.scope, Limit: &limit})
	items := e.decodeList(first)
	if len(items) != 2 || items[0].Name != "alpha" || items[1].Name != "beta" {
		t.Fatalf("page 1 = %+v", items)
	}
	if first.NextCursor == nil {
		t.Fatal("page 1 carried no cursor")
	}

	secondIn := skillListInput{Scope: e.scope, Limit: &limit, Cursor: first.NextCursor}
	second := e.mustOK("skill.list", secondIn)
	rest := e.decodeList(second)
	if len(rest) != 1 || rest[0].Name != "gamma" {
		t.Fatalf("page 2 = %+v", rest)
	}
	if second.NextCursor != nil {
		t.Fatal("page 2 carried a cursor without a full page")
	}
}

func TestListCursorFences(t *testing.T) {
	e := newEnv(t)
	e.seedSkill("alpha", 1, "draft")
	e.seedSkill("beta", 1, "draft")
	e.seedSkill("gamma", 1, "draft")
	limit := int64(2)
	first := e.mustOK("skill.list", skillListInput{Scope: e.scope, Limit: &limit})
	if first.NextCursor == nil {
		t.Fatal("expected a cursor")
	}
	cursor := *first.NextCursor

	t.Run("tampered cursor", func(t *testing.T) {
		tampered := cursor[:len(cursor)-4] + "AAAA"
		e.expectFault("skill.list", skillListInput{Scope: e.scope, Cursor: &tampered},
			contract.CodeInvalidInput)
	})

	t.Run("filter mismatch", func(t *testing.T) {
		state := "active"
		e.expectFault("skill.list", skillListInput{
			Scope: e.scope, Limit: &limit, Cursor: &cursor,
			Filter: &skillListFilter{State: &state},
		}, contract.CodeInvalidInput)
	})

	t.Run("expired cursor", func(t *testing.T) {
		e.clock.mu.Lock()
		e.clock.now = e.clock.now.Add(16 * time.Minute)
		e.clock.mu.Unlock()
		e.expectFault("skill.list", skillListInput{Scope: e.scope, Limit: &limit, Cursor: &cursor},
			contract.CodeCursorExpired)
	})
}

func TestArchiveStagesChange(t *testing.T) {
	t.Run("draft version stages an archive change", func(t *testing.T) {
		e := newEnv(t)
		payload, err := e.runImport(e.importFixture(validArchive()))
		if err != nil || payload.Error != nil {
			e.t.Fatalf("import failed: %v %v", err, payload.Error)
		}
		var out importOutput
		e.decode(payload.Data, &out)
		res := out.Resource

		got := e.mustOK("skill.archive", skillArchiveInput{
			Scope: e.scope, ID: res.ID, ExpectedVersion: res.Version,
		})
		var arch archiveOutput
		e.decode(got.Data, &arch)
		if arch.Resource.ID != res.ID || arch.Resource.Version != res.Version {
			t.Fatalf("archive resource = %s v%d", arch.Resource.ID, arch.Resource.Version)
		}
		if arch.Draft.ID == "" {
			t.Fatal("archive returned no draft")
		}
		calls := e.ports.callsOf("_configuration.stage")
		if len(calls) != 1 {
			t.Fatalf("stage calls = %d, want 1", len(calls))
		}
		var staged struct {
			Change struct {
				Kind            string      `json:"kind"`
				Action          string      `json:"action"`
				ID              contract.ID `json:"id"`
				ExpectedVersion int64       `json:"expected_version"`
			} `json:"change"`
		}
		if err := json.Unmarshal(calls[0].Input, &staged); err != nil {
			t.Fatalf("decode stage input: %v", err)
		}
		if staged.Change.Kind != "skill" || staged.Change.Action != "archive" ||
			staged.Change.ID != res.ID || staged.Change.ExpectedVersion != int64(res.Version) {
			t.Fatalf("staged change = %+v", staged.Change)
		}
		// Staging does not transition the row; the compiler applies it.
		if state, _ := e.rowState(res.ID, int64(res.Version)); state != "draft" {
			t.Fatalf("staging changed the row state to %q", state)
		}
	})

	t.Run("unknown version", func(t *testing.T) {
		e := newEnv(t)
		e.expectFault("skill.archive", skillArchiveInput{
			Scope: e.scope, ID: e.ids.New(), ExpectedVersion: 1,
		}, contract.CodeNotFound)
	})

	t.Run("already archived", func(t *testing.T) {
		e := newEnv(t)
		row := e.seedSkill("main", 1, "archived")
		e.expectFault("skill.archive", skillArchiveInput{
			Scope: e.scope, ID: row.ID, ExpectedVersion: 1,
		}, contract.CodeConflict)
	})
}
