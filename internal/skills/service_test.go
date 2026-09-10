package skills

import (
	"encoding/json"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Dispatch fences and strict decoding at the Handle boundary: known
// operations only, declared version only, mutations only in write
// transactions, and skill.import served exclusively through the local IO
// phases.

// callVersion invokes one operation with an explicit declared version.
func (e *testEnv) callVersion(op string, version int64, in any) (contract.Payload, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		e.t.Fatalf("marshal input for %s: %v", op, err)
	}
	return e.callRaw(op, version, raw)
}

// callRaw invokes one operation with raw input bytes.
func (e *testEnv) callRaw(op string, version int64, raw json.RawMessage) (contract.Payload, error) {
	var payload contract.Payload
	err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		p, err := e.svc.Handle(e.ctx, unit, contract.Invocation{Operation: op, Version: version, Input: raw})
		payload = p
		return err
	})
	return payload, err
}

func TestServiceContractSurface(t *testing.T) {
	e := newEnv(t)
	if e.svc.Name() != "skills" {
		t.Fatalf("Name() = %q, want skills", e.svc.Name())
	}
	if len(e.svc.Migrations()) == 0 {
		t.Fatal("Migrations() is empty")
	}
	ds := e.svc.Descriptors()
	if len(ds) != len(opMetas) {
		t.Fatalf("descriptors = %d, want %d", len(ds), len(opMetas))
	}
	seen := map[string]bool{}
	for _, d := range ds {
		if seen[d.ID] {
			t.Fatalf("duplicate descriptor %s", d.ID)
		}
		seen[d.ID] = true
		if d.Owner != "skills" || d.Version != 1 {
			t.Fatalf("descriptor %s has owner %q version %d", d.ID, d.Owner, d.Version)
		}
		if len(d.InputSchema) == 0 || len(d.OutputSchema) == 0 {
			t.Fatalf("descriptor %s carries empty schemas", d.ID)
		}
		if d.Visibility == "public" {
			if d.MCP == "" || len(d.CLI) == 0 {
				t.Fatalf("public descriptor %s lacks MCP/CLI bindings", d.ID)
			}
			if len(d.Callers) != 0 {
				t.Fatalf("public descriptor %s declares callers %v", d.ID, d.Callers)
			}
		} else {
			if d.MCP != "" || len(d.Callers) == 0 {
				t.Fatalf("internal descriptor %s: mcp %q callers %v", d.ID, d.MCP, d.Callers)
			}
		}
	}
	for _, id := range []string{"_skills.activate", "_skills.validate", "skill.get", "skill.list",
		"skill.import", "skill.archive", "skill.evaluate", "skill.evaluation.status"} {
		if !seen[id] {
			t.Fatalf("descriptor %s missing", id)
		}
	}
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	base := contract.Dependencies{
		Clock: &fakeClock{}, IDs: &seqIDs{}, Ports: newFakePorts(), Blobs: newFakeBlobs(),
	}
	cases := []struct {
		name string
		mut  func(d *contract.Dependencies)
	}{
		{"no clock", func(d *contract.Dependencies) { d.Clock = nil }},
		{"no ids", func(d *contract.Dependencies) { d.IDs = nil }},
		{"no ports", func(d *contract.Dependencies) { d.Ports = nil }},
		{"no blobs", func(d *contract.Dependencies) { d.Blobs = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := base
			tc.mut(&d)
			if _, err := New(d); err == nil {
				t.Fatal("New accepted missing dependency")
			}
		})
	}
}

func TestHandleDispatchFences(t *testing.T) {
	t.Run("unknown operation", func(t *testing.T) {
		e := newEnv(t)
		e.expectFault("skill.nope", skillGetInput{Scope: e.scope}, contract.CodeNotFound)
	})

	t.Run("version mismatch", func(t *testing.T) {
		e := newEnv(t)
		_, err := e.callVersion("skill.get", 2, skillGetInput{Scope: e.scope})
		var f *contract.Fault
		if err == nil || !asFault(err, &f) || f.Code != contract.CodeInvalidInput {
			t.Fatalf("version mismatch fault = %v, want invalid_input", err)
		}
	})

	t.Run("skill.import is not served through Handle", func(t *testing.T) {
		e := newEnv(t)
		_, err := e.callVersion("skill.import", 1, importInput{Scope: e.scope})
		var f *contract.Fault
		if err == nil || !asFault(err, &f) || f.Code != contract.CodeInternalError {
			t.Fatalf("skill.import via Handle fault = %v, want internal_error", err)
		}
	})

	t.Run("mutation refuses read-only unit", func(t *testing.T) {
		e := newEnv(t)
		raw, err := json.Marshal(skillArchiveInput{Scope: e.scope, ID: e.ids.New(), ExpectedVersion: 1})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.db.Read(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
			_, err := e.svc.Handle(e.ctx, unit, contract.Invocation{Operation: "skill.archive", Version: 1, Input: raw})
			var f *contract.Fault
			if err == nil || !asFault(err, &f) || f.Code != contract.CodeInvalidInput {
				t.Fatalf("mutation on read-only unit: got %v, want invalid_input", err)
			}
			return nil
		}); err != nil {
			t.Fatalf("read: %v", err)
		}
	})

	t.Run("query reaches the handler on a read-only unit", func(t *testing.T) {
		e := newEnv(t)
		raw, err := json.Marshal(skillGetInput{Scope: e.scope, ID: e.ids.New()})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.db.Read(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
			_, err := e.svc.Handle(e.ctx, unit, contract.Invocation{Operation: "skill.get", Version: 1, Input: raw})
			var f *contract.Fault
			if err == nil || !asFault(err, &f) || f.Code != contract.CodeNotFound {
				t.Fatalf("query on read-only unit: got %v, want not_found", err)
			}
			return nil
		}); err != nil {
			t.Fatalf("read: %v", err)
		}
	})
}

func TestHandleStrictDecode(t *testing.T) {
	t.Run("unknown input field", func(t *testing.T) {
		e := newEnv(t)
		e.expectFault("skill.get", map[string]any{
			"scope": e.scope, "id": e.ids.New(), "extra": "field",
		}, contract.CodeInvalidInput)
	})

	t.Run("missing required field", func(t *testing.T) {
		e := newEnv(t)
		e.expectFault("skill.get", map[string]any{"scope": e.scope}, contract.CodeInvalidInput)
	})

	t.Run("malformed json input", func(t *testing.T) {
		e := newEnv(t)
		_, err := e.callRaw("skill.get", 1, json.RawMessage(`{"scope":`))
		var f *contract.Fault
		if err == nil || !asFault(err, &f) || f.Code != contract.CodeInvalidInput {
			t.Fatalf("malformed json fault = %v, want invalid_input", err)
		}
	})

	t.Run("bad limit bound", func(t *testing.T) {
		e := newEnv(t)
		limit := int64(201)
		e.expectFault("skill.list", skillListInput{Scope: e.scope, Limit: &limit}, contract.CodeInvalidInput)
	})
}
