package tasks

import (
	"errors"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Service-level behavior: module identity, descriptor exactness against the
// frozen operation table, migration ownership, and strict dispatch.

func TestServiceNameIsOwner(t *testing.T) {
	env := newEnv(t)
	if got := env.svc.Name(); got != "tasks" {
		t.Fatalf("Name() = %q, want tasks", got)
	}
}

func TestServiceImplementsModule(t *testing.T) {
	var _ contract.Module = (*Service)(nil)
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	ports := newFakePorts()
	cases := []struct {
		name string
		deps contract.Dependencies
		want string
	}{
		{"no clock", contract.Dependencies{IDs: &seqIDs{}, Ports: ports}, "clock"},
		{"no ids", contract.Dependencies{Clock: &fakeClock{}, Ports: ports}, "identity source"},
		{"no ports", contract.Dependencies{Clock: &fakeClock{}, IDs: &seqIDs{}}, "ports"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.deps)
			if err == nil {
				t.Fatalf("New succeeded without %s", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.want)
			}
			var f *contract.Fault
			if !asFault(err, &f) || f.Code != contract.CodeInternalError {
				t.Fatalf("constructor fault code %v, want internal_error", f)
			}
		})
	}
}

// asFault extracts a *contract.Fault from err through errors.As.
func asFault(err error, target **contract.Fault) bool {
	return errors.As(err, target)
}

func TestDescriptorsExactness(t *testing.T) {
	env := newEnv(t)
	descs := env.svc.Descriptors()
	if len(descs) != len(opMetas) {
		t.Fatalf("descriptor count %d, want %d", len(descs), len(opMetas))
	}
	byID := map[string]contract.Descriptor{}
	for _, d := range descs {
		if _, dup := byID[d.ID]; dup {
			t.Fatalf("duplicate descriptor %s", d.ID)
		}
		byID[d.ID] = d
		if d.Owner != "tasks" || d.Version != 1 {
			t.Fatalf("%s owner/version %s/%d, want tasks/1", d.ID, d.Owner, d.Version)
		}
		if d.Effect != "local" {
			t.Fatalf("%s effect %q, want local", d.ID, d.Effect)
		}
		if len(d.ScopeRequired) != 1 || d.ScopeRequired[0] != "installation_id" {
			t.Fatalf("%s scope_required %v, want [installation_id]", d.ID, d.ScopeRequired)
		}
		if len(d.InputSchema) == 0 || len(d.OutputSchema) == 0 {
			t.Fatalf("%s carries empty schemas", d.ID)
		}
		if !strings.Contains(string(d.InputSchema), `"$defs"`) {
			t.Fatalf("%s input schema is not composed with $defs", d.ID)
		}
	}
	for _, m := range opMetas {
		d, ok := byID[m.id]
		if !ok {
			t.Fatalf("descriptor %s missing", m.id)
		}
		if d.Visibility != m.visibility || d.Mode != m.mode {
			t.Fatalf("%s visibility/mode %s/%s, want %s/%s", m.id, d.Visibility, d.Mode, m.visibility, m.mode)
		}
		if d.SubmissionKey != m.submission {
			t.Fatalf("%s submission_key %v, want %v", m.id, d.SubmissionKey, m.submission)
		}
		if d.ExpectedVersion != m.expectedVersion {
			t.Fatalf("%s expected_version %v, want %v", m.id, d.ExpectedVersion, m.expectedVersion)
		}
		if len(d.Callers) != len(m.callers) {
			t.Fatalf("%s callers %v, want %v", m.id, d.Callers, m.callers)
		}
		for i, c := range m.callers {
			if d.Callers[i] != c {
				t.Fatalf("%s caller %d = %s, want %s", m.id, i, d.Callers[i], c)
			}
		}
		if m.cli != "" {
			if strings.Join(d.CLI, " ") != m.cli {
				t.Fatalf("%s cli %v, want %q", m.id, d.CLI, m.cli)
			}
		} else if len(d.CLI) != 0 {
			t.Fatalf("%s is internal and must not carry a CLI path: %v", m.id, d.CLI)
		}
		if m.visibility == "public" {
			want := "zatiti_" + strings.ReplaceAll(m.id, ".", "_")
			if d.MCP != want {
				t.Fatalf("%s mcp %q, want %q", m.id, d.MCP, want)
			}
		} else if d.MCP != "" {
			t.Fatalf("%s is internal and must not carry an MCP name: %q", m.id, d.MCP)
		}
	}
	// Spot checks of the frozen decisions the AGENTS.md contract pins.
	frozen := []struct {
		id              string
		mode            string
		submission      bool
		expectedVersion bool
	}{
		{"_tasks.create", "mutation", false, false},
		{"_tasks.ready", "query", false, false},
		{"_tasks.snapshot", "query", false, false},
		{"_tasks.transition", "mutation", false, false},
		{"task.create", "mutation", true, false},
		{"task.delegate", "mutation", true, true},
		{"task.accept", "mutation", true, true},
		{"task.dependencies", "query", false, false},
		{"task.list", "query", false, false},
	}
	for _, f := range frozen {
		d := byID[f.id]
		if d.Mode != f.mode || d.SubmissionKey != f.submission || d.ExpectedVersion != f.expectedVersion {
			t.Fatalf("%s frozen metadata drifted: mode=%s submission=%v expectedVersion=%v",
				f.id, d.Mode, d.SubmissionKey, d.ExpectedVersion)
		}
	}
}

func TestMigrationsShape(t *testing.T) {
	env := newEnv(t)
	migs := env.svc.Migrations()
	if len(migs) != 1 {
		t.Fatalf("migrations count %d, want 1", len(migs))
	}
	m := migs[0]
	if m.Owner != "tasks" || m.Version != 1 {
		t.Fatalf("migration owner/version %s/%d, want tasks/1", m.Owner, m.Version)
	}
	if m.SHA256 == "" || len(m.SHA256) != 64 {
		t.Fatalf("migration sha256 %q is not a digest", m.SHA256)
	}
	for _, table := range []string{
		"CREATE TABLE tasks_tasks", "CREATE TABLE tasks_dependencies",
		"CREATE TABLE tasks_evidence", "CREATE TABLE tasks_manual_decisions",
		"CREATE TABLE tasks_transitions",
	} {
		if !strings.Contains(m.SQL, table) {
			t.Fatalf("migration SQL missing %s", table)
		}
	}
	if strings.Contains(m.SQL, "DROP") || strings.Contains(m.SQL, "ALTER") {
		t.Fatalf("migration SQL must be additive only")
	}
}

func TestHandleStrictDispatch(t *testing.T) {
	env := newEnv(t)

	t.Run("unknown operation is not_found", func(t *testing.T) {
		f := env.expectFault("task.nonsense", map[string]any{}, contract.CodeNotFound)
		if !strings.Contains(f.Message, "unknown operation") {
			t.Fatalf("message %q does not name the unknown operation", f.Message)
		}
	})

	t.Run("version mismatch is invalid_input", func(t *testing.T) {
		var got *contract.Fault
		raw := mustJSON(map[string]any{"scope": env.scope, "id": env.ids.New()})
		err := env.db.Write(env.ctx, env.actor, env.scope.toContract(), func(unit contract.Unit) error {
			_, err := env.svc.Handle(env.ctx, unit, contract.Invocation{Operation: "task.get", Version: 2, Input: raw})
			return err
		})
		asFault(err, &got)
		if got == nil || got.Code != contract.CodeInvalidInput {
			t.Fatalf("version mismatch fault %v, want invalid_input", got)
		}
	})

	t.Run("mutation refused in read-only transaction", func(t *testing.T) {
		raw := mustJSON(map[string]any{"scope": env.scope, "definition": env.taskDef()})
		var got *contract.Fault
		err := env.db.Read(env.ctx, env.actor, env.scope.toContract(), func(unit contract.Unit) error {
			_, err := env.svc.Handle(env.ctx, unit, contract.Invocation{Operation: "task.create", Version: 1, Input: raw})
			return err
		})
		asFault(err, &got)
		if got == nil || got.Code != contract.CodeInvalidInput {
			t.Fatalf("read-only mutation fault %v, want invalid_input", got)
		}
	})

	t.Run("query allowed in read-only transaction", func(t *testing.T) {
		raw := mustJSON(map[string]any{"scope": env.scope, "id": env.ids.New()})
		err := env.db.Read(env.ctx, env.actor, env.scope.toContract(), func(unit contract.Unit) error {
			_, err := env.svc.Handle(env.ctx, unit, contract.Invocation{Operation: "task.get", Version: 1, Input: raw})
			return err
		})
		var got *contract.Fault
		asFault(err, &got)
		if got == nil || got.Code != contract.CodeNotFound {
			t.Fatalf("read-only query fault %v, want not_found for absent task", got)
		}
	})

	t.Run("input outside the operation schema is invalid_input", func(t *testing.T) {
		// task.get with an unexpected extra member: additionalProperties=false.
		_ = env.expectFault("task.get", map[string]any{
			"scope": env.scope, "id": env.ids.New(), "surprise": 1,
		}, contract.CodeInvalidInput)
	})
}
