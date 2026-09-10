package skills

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// _skills.validate and _skills.activate are compiler-owned internal
// operations. Z19: activation may only transition state — a create invalidates
// nothing, an update supersedes by archiving the old active version and
// invalidating exactly its policy qualifications, and the candidate slice is
// an exact sealed reference that must match its stored rows.

// insertRawDependency forges one stored dependency edge directly, letting
// tests construct stored graphs (including inconsistent ones) that seeding
// alone cannot produce.
func (e *testEnv) insertRawDependency(name string, version int64, depName string, depVersion int64) {
	e.t.Helper()
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		row, err := fetchSkillByNameVersion(e.ctx, unit, e.install, name, version)
		if err != nil {
			return err
		}
		if row == nil {
			return errors.New("insertRawDependency: " + name + " not seeded")
		}
		dep, err := fetchSkillByNameVersion(e.ctx, unit, e.install, depName, depVersion)
		if err != nil {
			return err
		}
		if dep == nil {
			return errors.New("insertRawDependency: " + depName + " not seeded")
		}
		return insertDependency(e.ctx, unit, dependencyRow{
			InstallationID: e.install, SkillID: row.ID, SkillVersion: version,
			DepName: depName, DepID: dep.ID, DepVersion: depVersion,
		})
	}); err != nil {
		e.t.Fatalf("insertRawDependency %s@%d -> %s@%d: %v", name, version, depName, depVersion, err)
	}
}

// mustDecodeValidation decodes a _skills.validate payload.
func (e *testEnv) mustDecodeValidation(payload contract.Payload) wireValidation {
	e.t.Helper()
	var out validationOutput
	e.decode(payload.Data, &out)
	return out.Resource
}

func TestValidateCandidateDiagnostics(t *testing.T) {
	t.Run("valid create has no diagnostics and reports dependencies", func(t *testing.T) {
		e := newEnv(t)
		dep := e.seedSkill("dep-skill", 1, "active")
		row := e.seedSkill("main", 1, "draft", dependencyRef{Name: "dep-skill"})
		payload := e.mustOK("_skills.validate", candidateInputOf(changeOf("create", e.defOfRow(row), 0)))
		v := e.mustDecodeValidation(payload)
		if len(v.Diagnostics) != 0 {
			t.Fatalf("diagnostics = %+v, want none", v.Diagnostics)
		}
		if len(v.Dependencies) != 1 || v.Dependencies[0].ID != dep.ID || v.Dependencies[0].Version != 1 {
			t.Fatalf("dependencies = %+v, want the resolved dep pin", v.Dependencies)
		}
	})

	t.Run("missing row", func(t *testing.T) {
		e := newEnv(t)
		ghost := e.defOfRow(e.seedSkill("main", 1, "draft"))
		ghost.Version = 99
		v := e.mustDecodeValidation(e.mustOK("_skills.validate", candidateInputOf(changeOf("create", ghost, 0))))
		if len(v.Diagnostics) != 1 || v.Diagnostics[0].Code != "not_found" {
			t.Fatalf("diagnostics = %+v, want one not_found", v.Diagnostics)
		}
		if v.Diagnostics[0].Path != "candidate.changes[0]" {
			t.Fatalf("diagnostic path = %q", v.Diagnostics[0].Path)
		}
	})

	t.Run("mismatched definition", func(t *testing.T) {
		e := newEnv(t)
		row := e.seedSkill("main", 1, "draft")
		def := e.defOfRow(row)
		def.ContentDigest = contract.Digest(strings.Repeat("a", 64))
		v := e.mustDecodeValidation(e.mustOK("_skills.validate", candidateInputOf(changeOf("create", def, 0))))
		if len(v.Diagnostics) != 1 || v.Diagnostics[0].Code != "conflict" {
			t.Fatalf("diagnostics = %+v, want one conflict", v.Diagnostics)
		}
	})

	t.Run("duplicate entry", func(t *testing.T) {
		e := newEnv(t)
		row := e.seedSkill("main", 1, "draft")
		def := e.defOfRow(row)
		v := e.mustDecodeValidation(e.mustOK("_skills.validate",
			candidateInputOf(changeOf("create", def, 0), changeOf("create", def, 0))))
		if len(v.Diagnostics) != 1 || v.Diagnostics[0].Code != "conflict" {
			t.Fatalf("diagnostics = %+v, want one conflict for the duplicate", v.Diagnostics)
		}
	})

	t.Run("create with nonzero expected version", func(t *testing.T) {
		e := newEnv(t)
		row := e.seedSkill("main", 1, "draft")
		v := e.mustDecodeValidation(e.mustOK("_skills.validate", candidateInputOf(changeOf("create", e.defOfRow(row), 1))))
		if len(v.Diagnostics) != 1 || v.Diagnostics[0].Code != "invalid_input" {
			t.Fatalf("diagnostics = %+v, want one invalid_input", v.Diagnostics)
		}
	})

	t.Run("update without an active version", func(t *testing.T) {
		e := newEnv(t)
		row := e.seedSkill("main", 1, "draft")
		v := e.mustDecodeValidation(e.mustOK("_skills.validate", candidateInputOf(changeOf("update", e.defOfRow(row), 1))))
		if len(v.Diagnostics) != 1 || v.Diagnostics[0].Code != "conflict" {
			t.Fatalf("diagnostics = %+v, want one conflict", v.Diagnostics)
		}
	})

	t.Run("unsupported action", func(t *testing.T) {
		e := newEnv(t)
		row := e.seedSkill("main", 1, "draft")
		v := e.mustDecodeValidation(e.mustOK("_skills.validate", candidateInputOf(changeOf("delete", e.defOfRow(row), 1))))
		if len(v.Diagnostics) != 1 || v.Diagnostics[0].Code != "invalid_input" {
			t.Fatalf("diagnostics = %+v, want one invalid_input", v.Diagnostics)
		}
	})

	t.Run("non-skill changes are ignored", func(t *testing.T) {
		e := newEnv(t)
		orgID := e.ids.New()
		other := wireChange{Kind: "organization", Action: "create", ID: orgID,
			ExpectedVersion: 0, Definition: rawDef(map[string]any{
				"id": orgID, "version": 1, "key": "org", "name": "Org",
				"chief_id": e.ids.New(),
			})}
		v := e.mustDecodeValidation(e.mustOK("_skills.validate", candidateInputOf(other)))
		if len(v.Diagnostics) != 0 {
			t.Fatalf("diagnostics = %+v, want none", v.Diagnostics)
		}
	})
}

func TestActivateCreateZ19(t *testing.T) {
	e := newEnv(t)
	row := e.seedSkill("main", 1, "draft")
	payload := e.mustOK("_skills.activate", candidateInputOf(changeOf("create", e.defOfRow(row), 0)))
	var out versionsOutput
	e.decode(payload.Data, &out)
	if len(out.Versions) != 1 || out.Versions[0].ID != row.ID || out.Versions[0].Version != 1 {
		t.Fatalf("activated versions = %+v", out.Versions)
	}
	if state, ok := e.rowState(row.ID, 1); !ok || state != "active" {
		t.Fatalf("state = %q (found %v), want active", state, ok)
	}
	// Z19: creation invalidates nothing — there is nothing it replaces.
	if calls := e.ports.callsOf("_policy.invalidate"); len(calls) != 0 {
		t.Fatalf("create invalidated policy %d times, want 0", len(calls))
	}
	events, err := e.db.Events(e.ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != "skills.skill.activated" {
		t.Fatalf("events = %+v, want one skills.skill.activated", events)
	}
}

func TestActivateUpdateSupersedes(t *testing.T) {
	e := newEnv(t)
	old := e.seedSkill("main", 1, "active")
	newRow := e.seedSkill("main", 2, "draft")
	e.mustOK("_skills.activate", candidateInputOf(changeOf("update", e.defOfRow(newRow), 2)))

	if state, _ := e.rowState(old.ID, 1); state != "archived" {
		t.Fatalf("superseded version state = %q, want archived", state)
	}
	if state, _ := e.rowState(newRow.ID, 2); state != "active" {
		t.Fatalf("new version state = %q, want active", state)
	}

	// Exactly one invalidation, naming exactly the superseded version.
	calls := e.ports.callsOf("_policy.invalidate")
	if len(calls) != 1 {
		t.Fatalf("invalidate calls = %d, want 1", len(calls))
	}
	var in struct {
		ChangedDependencies []wireRef `json:"changed_dependencies"`
		Reason              string    `json:"reason"`
	}
	if err := json.Unmarshal(calls[0].Input, &in); err != nil {
		t.Fatalf("decode invalidate input: %v", err)
	}
	if len(in.ChangedDependencies) != 1 ||
		in.ChangedDependencies[0].ID != old.ID || in.ChangedDependencies[0].Version != 1 {
		t.Fatalf("changed dependencies = %+v, want exactly the superseded v1", in.ChangedDependencies)
	}
	if !strings.Contains(in.Reason, "superseded") {
		t.Fatalf("reason = %q", in.Reason)
	}
}

func TestActivateArchive(t *testing.T) {
	t.Run("active version invalidates", func(t *testing.T) {
		e := newEnv(t)
		row := e.seedSkill("main", 1, "active")
		e.mustOK("_skills.activate", candidateInputOf(changeOf("archive", e.defOfRow(row), 1)))
		if state, _ := e.rowState(row.ID, 1); state != "archived" {
			t.Fatalf("state = %q, want archived", state)
		}
		if calls := e.ports.callsOf("_policy.invalidate"); len(calls) != 1 {
			t.Fatalf("invalidate calls = %d, want 1", len(calls))
		}
	})
	t.Run("draft version invalidates nothing", func(t *testing.T) {
		e := newEnv(t)
		row := e.seedSkill("main", 1, "draft")
		e.mustOK("_skills.activate", candidateInputOf(changeOf("archive", e.defOfRow(row), 1)))
		if state, _ := e.rowState(row.ID, 1); state != "archived" {
			t.Fatalf("state = %q, want archived", state)
		}
		if calls := e.ports.callsOf("_policy.invalidate"); len(calls) != 0 {
			t.Fatalf("draft archive invalidated policy %d times, want 0", len(calls))
		}
	})
	t.Run("already archived rejects", func(t *testing.T) {
		e := newEnv(t)
		row := e.seedSkill("main", 1, "archived")
		e.expectFault("_skills.activate", candidateInputOf(changeOf("archive", e.defOfRow(row), 1)), contract.CodeConflict)
	})
}

func TestActivateFences(t *testing.T) {
	t.Run("mismatched definition rejects", func(t *testing.T) {
		e := newEnv(t)
		row := e.seedSkill("main", 1, "draft")
		def := e.defOfRow(row)
		def.License = "Apache-2.0"
		e.expectFault("_skills.activate", candidateInputOf(changeOf("create", def, 0)), contract.CodeConflict)
	})

	t.Run("missing row rejects", func(t *testing.T) {
		e := newEnv(t)
		def := e.defOfRow(e.seedSkill("main", 1, "draft"))
		def.Version = 42
		e.expectFault("_skills.activate", candidateInputOf(changeOf("create", def, 0)), contract.CodeNotFound)
	})

	t.Run("create when an active version exists rejects", func(t *testing.T) {
		e := newEnv(t)
		e.seedSkill("main", 1, "active")
		newRow := e.seedSkill("main", 2, "draft")
		e.expectFault("_skills.activate", candidateInputOf(changeOf("create", e.defOfRow(newRow), 0)), contract.CodeConflict)
	})

	t.Run("update without an active version rejects", func(t *testing.T) {
		e := newEnv(t)
		row := e.seedSkill("main", 1, "draft")
		e.expectFault("_skills.activate", candidateInputOf(changeOf("update", e.defOfRow(row), 1)), contract.CodeConflict)
	})

	t.Run("create on a non-draft rejects", func(t *testing.T) {
		e := newEnv(t)
		row := e.seedSkill("main", 1, "archived")
		e.expectFault("_skills.activate", candidateInputOf(changeOf("create", e.defOfRow(row), 0)), contract.CodeConflict)
	})

	t.Run("stored dependency cycle rejects", func(t *testing.T) {
		e := newEnv(t)
		a := e.seedSkill("a", 1, "active")
		b := e.seedSkill("b", 1, "active")
		// Forge the inconsistent stored graph the fence defends against:
		// a@1 -> b@1 -> a@1.
		e.insertRawDependency("a", 1, "b", 1)
		e.insertRawDependency("b", 1, "a", 1)
		e.expectFault("_skills.activate", candidateInputOf(
			changeOf("archive", e.defOfRow(a), 1),
			changeOf("archive", e.defOfRow(b), 1),
		), contract.CodeInvalidInput)
	})

	t.Run("the slice lands all or nothing", func(t *testing.T) {
		e := newEnv(t)
		good := e.seedSkill("good", 1, "draft")
		bad := e.seedSkill("bad", 1, "draft")
		badDef := e.defOfRow(bad)
		badDef.Name = "rewritten"
		e.expectFault("_skills.activate", candidateInputOf(
			changeOf("create", e.defOfRow(good), 0),
			changeOf("create", badDef, 0),
		), contract.CodeConflict)
		if state, _ := e.rowState(good.ID, 1); state != "draft" {
			t.Fatalf("good skill state = %q, want draft (slice must not partially apply)", state)
		}
	})
}
