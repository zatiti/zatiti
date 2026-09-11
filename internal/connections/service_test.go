package connections

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Constructor and dispatch-boundary tests: dependency validation, descriptor
// composition, strict dispatch, read-only mutation refusal and the local IO
// routing fence.

func TestNewRequiresDependencies(t *testing.T) {
	base := contract.Dependencies{
		Clock: &fakeClock{}, IDs: &seqIDs{}, Ports: newFakePorts(), Secrets: newFakeSecrets(),
	}
	cases := []struct {
		name   string
		mutate func(d *contract.Dependencies)
		want   string // substring of the expected error
	}{
		{"no-clock", func(d *contract.Dependencies) { d.Clock = nil }, "clock"},
		{"no-ids", func(d *contract.Dependencies) { d.IDs = nil }, "identity"},
		{"no-ports", func(d *contract.Dependencies) { d.Ports = nil }, "ports"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := base
			tc.mutate(&d)
			svc, err := New(d)
			if err == nil {
				t.Fatalf("New accepted a service without %s", tc.name)
			}
			if svc != nil {
				t.Fatalf("New returned a service alongside an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestNewSucceedsWithAllDependencies(t *testing.T) {
	svc, err := New(contract.Dependencies{
		Clock: &fakeClock{}, IDs: &seqIDs{}, Ports: newFakePorts(), Secrets: newFakeSecrets(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if svc.Name() != "connections" {
		t.Fatalf("Name() = %q", svc.Name())
	}
	ms := svc.Migrations()
	if len(ms) == 0 {
		t.Fatalf("Migrations() is empty")
	}
	for _, m := range ms {
		if m.Owner != "connections" {
			t.Fatalf("migration owner %q, want connections", m.Owner)
		}
		if len(m.SQL) == 0 {
			t.Fatalf("migration %d carries no SQL", m.Version)
		}
		if m.SHA256 == "" {
			t.Fatalf("migration %d carries no pinned digest", m.Version)
		}
	}
}

func TestDescriptorsCoverEveryOperation(t *testing.T) {
	svc, err := New(contract.Dependencies{
		Clock: &fakeClock{}, IDs: &seqIDs{}, Ports: newFakePorts(), Secrets: newFakeSecrets(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	descriptors := svc.Descriptors()
	byID := map[string]contract.Descriptor{}
	for _, d := range descriptors {
		if _, dup := byID[d.ID]; dup {
			t.Fatalf("duplicate descriptor %s", d.ID)
		}
		byID[d.ID] = d
	}
	if len(byID) != len(opMetas) {
		t.Fatalf("descriptor count %d, want %d", len(byID), len(opMetas))
	}
	for _, m := range opMetas {
		d, ok := byID[m.id]
		if !ok {
			t.Fatalf("operation %s has no descriptor", m.id)
		}
		if d.Version != 1 || d.Owner != "connections" || d.Visibility != m.visibility || d.Mode != m.mode {
			t.Fatalf("descriptor %s carries wrong static fields: %+v", m.id, d)
		}
		if d.Effect != m.effect {
			t.Fatalf("operation %s effect %q, want %q", m.id, d.Effect, m.effect)
		}
		if len(d.ScopeRequired) != 1 || d.ScopeRequired[0] != "installation_id" {
			t.Fatalf("operation %s scope requirement is not installation_id", m.id)
		}
		if len(d.InputSchema) == 0 {
			t.Fatalf("operation %s carries no input schema", m.id)
		}
		if len(d.OutputSchema) == 0 {
			t.Fatalf("operation %s carries no output schema", m.id)
		}
		if d.SubmissionKey != m.submission || d.ExpectedVersion != m.expectedVersion {
			t.Fatalf("operation %s carries wrong submission/expected-version flags", m.id)
		}
		if m.visibility == "public" {
			if !strings.HasPrefix(d.MCP, "zatiti_") {
				t.Fatalf("public operation %s carries MCP name %q", m.id, d.MCP)
			}
			if len(d.CLI) == 0 || d.CLI[0] != "zatiti" {
				t.Fatalf("public operation %s carries CLI path %v", m.id, d.CLI)
			}
			if len(d.Callers) != 0 {
				t.Fatalf("public operation %s declares callers %v", m.id, d.Callers)
			}
		} else if len(d.Callers) == 0 {
			t.Fatalf("internal operation %s declares no callers", m.id)
		}
		if m.completion && len(d.CompletionSchema) == 0 {
			t.Fatalf("asynchronous operation %s carries no completion schema", m.id)
		}
		if !m.completion && len(d.CompletionSchema) != 0 {
			t.Fatalf("synchronous operation %s declares a completion schema", m.id)
		}
		if _, routed := handlers[m.id]; !routed {
			t.Fatalf("operation %s has no handler", m.id)
		}
	}
	if len(handlers) != len(opMetas) {
		t.Fatalf("handler count %d exceeds declared operations %d", len(handlers), len(opMetas))
	}
}

func TestHandleUnknownOperation(t *testing.T) {
	env := newEnv(t)
	f := env.expectFault("nonexistent.operation", map[string]any{}, contract.CodeNotFound)
	if !strings.Contains(f.Message, "unknown operation") {
		t.Fatalf("refusal message %q does not name the dispatch defect", f.Message)
	}
}

func TestHandleRefusesVersionMismatch(t *testing.T) {
	env := newEnv(t)
	raw, err := json.Marshal(connGetIn{Scope: env.scope, ID: env.ids.New()})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var payload contract.Payload
	err = env.db.Write(env.ctx, env.actor, env.scope.toContract(), func(unit contract.Unit) error {
		p, err := env.svc.Handle(env.ctx, unit, contract.Invocation{Operation: "connection.get", Version: 2, Input: raw})
		payload = p
		return err
	})
	if err == nil {
		t.Fatalf("version 2 dispatch succeeded, want refusal")
	}
	fault, ok := err.(*contract.Fault)
	if !ok || fault.Code != contract.CodeInvalidInput {
		t.Fatalf("refusal %v, want invalid_input", err)
	}
	_ = payload
}

func TestMutationRefusesReadOnlySnapshot(t *testing.T) {
	env := newEnv(t)
	in := connCreateIn{Scope: env.scope, Definition: connDefIn{
		Scope: env.scope, Provider: "p", AccountIdentity: "a", CredentialRef: "c",
		Destinations: []string{"api.github.com"}, AllowedScopes: []string{"s"},
	}}
	_, err := env.callReadAs("connection.create", in)
	if err == nil {
		t.Fatalf("mutation succeeded inside a read snapshot")
	}
	f, ok := err.(*contract.Fault)
	if !ok || f.Code != contract.CodeInvalidInput {
		t.Fatalf("refusal %v, want invalid_input", err)
	}
	if !strings.Contains(f.Message, "write transaction") {
		t.Fatalf("refusal message %q does not explain the write requirement", f.Message)
	}
}

func TestQueryRunsInReadOnlySnapshot(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)
	in := connGetIn{Scope: env.scope, ID: conn.ID}
	payload, err := env.callReadAs("connection.get", in)
	if err != nil {
		t.Fatalf("connection.get in a read snapshot: %v", err)
	}
	if payload.Status != contract.StatusCompleted {
		t.Fatalf("status %q", payload.Status)
	}
	var out resourceOut
	env.decode(payload.Data, &out)
	if out.Resource.ID != conn.ID {
		t.Fatalf("read snapshot returned %s, want %s", out.Resource.ID, conn.ID)
	}
}

func TestLocalIORefusesDirectHandleDispatch(t *testing.T) {
	env := newEnv(t)
	for _, op := range []string{"connection.setup.begin", "connection.setup.cancel", "connection.setup.complete"} {
		t.Run(op, func(t *testing.T) {
			f := env.expectFault(op, map[string]any{}, contract.CodeInternalError)
			if !strings.Contains(f.Message, "Prepare/Perform/Finish") {
				t.Fatalf("refusal message %q does not name the routing seam", f.Message)
			}
		})
	}
}

func TestPrepareRefusesUnroutedOperation(t *testing.T) {
	env := newEnv(t)
	err := env.db.Write(env.ctx, env.actor, env.scope.toContract(), func(unit contract.Unit) error {
		_, err := env.svc.Prepare(env.ctx, unit, contract.Invocation{Operation: "connection.get", Version: 1})
		return err
	})
	f, ok := err.(*contract.Fault)
	if !ok || f.Code != contract.CodeInternalError {
		t.Fatalf("Prepare(connection.get) = %v, want internal_error", err)
	}
}
