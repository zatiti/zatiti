package accounting

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Service assembly and dispatch: construction requirements, the descriptor
// catalog, transport bindings, strict dispatch and the bind-boundary schema
// enforcement.

func TestNewRequiresClockAndIDs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		deps contract.Dependencies
		want string
	}{
		{"missing clock", contract.Dependencies{IDs: &seqIDs{}}, "accounting requires a clock"},
		{"missing ids", contract.Dependencies{Clock: &fakeClock{}}, "accounting requires an identity source"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(tc.deps)
			if err == nil {
				t.Fatalf("New with %s: want fault, got service", tc.name)
			}
			if got := faultCode(err); got != contract.CodeInternalError {
				t.Fatalf("New fault code %q, want internal_error", got)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("New error %q does not mention %q", err.Error(), tc.want)
			}
		})
	}
}

func TestConstructsWithoutPorts(t *testing.T) {
	t.Parallel()
	svc, err := New(contract.Dependencies{Clock: &fakeClock{}, IDs: &seqIDs{}})
	if err != nil {
		t.Fatalf("New without ports: %v", err)
	}
	if svc.Name() != "accounting" {
		t.Fatalf("Name() = %q, want accounting", svc.Name())
	}
	if len(svc.Migrations()) != 1 {
		t.Fatalf("Migrations() returned %d entries, want 1", len(svc.Migrations()))
	}
	if got := svc.Migrations()[0].Owner; got != "accounting" {
		t.Fatalf("migration owner %q, want accounting", got)
	}
}

func TestDescriptors(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	got := map[string]contract.Descriptor{}
	for _, d := range e.svc.Descriptors() {
		got[d.ID] = d
	}
	want := map[string]struct {
		visibility string
		mode       string
		callers    int
		cli        bool
		mcp        bool
		submission bool
	}{
		opActivate:      {visibility: "internal", mode: "mutation", callers: 2, cli: false, mcp: false, submission: false},
		opInspect:       {visibility: "internal", mode: "query", callers: 6, cli: false, mcp: false, submission: false},
		opReserve:       {visibility: "internal", mode: "mutation", callers: 4, cli: false, mcp: false, submission: false},
		opSettle:        {visibility: "internal", mode: "mutation", callers: 3, cli: false, mcp: false, submission: false},
		opValidate:      {visibility: "internal", mode: "query", callers: 2, cli: false, mcp: false, submission: false},
		opBudgetGet:     {visibility: "public", mode: "query", callers: 0, cli: true, mcp: true, submission: false},
		opBudgetPropose: {visibility: "public", mode: "mutation", callers: 0, cli: true, mcp: true, submission: true},
		opUsageGet:      {visibility: "public", mode: "query", callers: 0, cli: true, mcp: true, submission: false},
	}
	if len(got) != len(want) {
		t.Fatalf("Descriptors() returned %d operations, want %d", len(got), len(want))
	}
	for id, w := range want {
		d, ok := got[id]
		if !ok {
			t.Fatalf("Descriptors() missing operation %s", id)
		}
		if d.Visibility != w.visibility || d.Mode != w.mode {
			t.Fatalf("%s: visibility/mode %q/%q, want %q/%q", id, d.Visibility, d.Mode, w.visibility, w.mode)
		}
		if len(d.Callers) != w.callers {
			t.Fatalf("%s: %d callers, want %d", id, len(d.Callers), w.callers)
		}
		if w.cli && len(d.CLI) == 0 {
			t.Fatalf("%s: missing CLI binding", id)
		}
		if !w.cli && len(d.CLI) != 0 {
			t.Fatalf("%s: internal operation carries CLI binding %v", id, d.CLI)
		}
		if w.mcp && d.MCP == "" {
			t.Fatalf("%s: missing MCP tool name", id)
		}
		if !w.mcp && d.MCP != "" {
			t.Fatalf("%s: internal operation carries MCP binding %q", id, d.MCP)
		}
		if d.SubmissionKey != w.submission {
			t.Fatalf("%s: submission key %v, want %v", id, d.SubmissionKey, w.submission)
		}
		if len(d.InputSchema) == 0 || len(d.OutputSchema) == 0 {
			t.Fatalf("%s: missing merged schemas", id)
		}
		if len(d.ScopeRequired) != 1 || d.ScopeRequired[0] != "installation_id" {
			t.Fatalf("%s: scope requirement %v, want [installation_id]", id, d.ScopeRequired)
		}
	}
	if got[opBudgetPropose].MCP != "zatiti_budget_propose" {
		t.Fatalf("budget.propose MCP name %q, want zatiti_budget_propose", got[opBudgetPropose].MCP)
	}
	if len(got[opBudgetGet].CLI) != 3 {
		t.Fatalf("budget.get CLI path %v, want three segments", got[opBudgetGet].CLI)
	}
}

func TestDispatchStrictness(t *testing.T) {
	t.Run("unknown operation is not_found", func(t *testing.T) {
		e := newEnv(t)
		_ = e.expectFault("_accounting.unknown", scopeInput{Scope: e.scope}, contract.CodeNotFound)
	})
	t.Run("version mismatch is invalid_input", func(t *testing.T) {
		e := newEnv(t)
		e.expectVersionFault(opInspect, 2)
	})
	t.Run("mutation in read unit is invalid_input", func(t *testing.T) {
		e := newEnv(t)
		e.expectReadOnlyFault(opReserve)
	})
	t.Run("query in read unit succeeds", func(t *testing.T) {
		e := newEnv(t)
		payload, err := e.readAs(opInspect, scopeInput{Scope: e.scope})
		if err != nil {
			t.Fatalf("inspect in read unit: %v", err)
		}
		if payload.Status != contract.StatusCompleted {
			t.Fatalf("inspect status %q", payload.Status)
		}
	})
}

// expectVersionFault drives an invocation with a wrong version and requires
// the invalid_input fault.
func (e *testEnv) expectVersionFault(op string, version int64) {
	e.t.Helper()
	raw, err := json.Marshal(scopeInput{Scope: e.scope})
	if err != nil {
		e.t.Fatalf("marshal: %v", err)
	}
	_, err = e.svc.Handle(e.ctx, e.writeUnit(), contract.Invocation{Operation: op, Version: version, Input: raw})
	if got := faultCode(err); got != contract.CodeInvalidInput {
		e.t.Fatalf("%s version %d: fault %q, want invalid_input", op, version, got)
	}
}

// expectReadOnlyFault drives a mutation through a read unit and requires the
// invalid_input fault before any handler work.
func (e *testEnv) expectReadOnlyFault(op string) {
	e.t.Helper()
	raw, err := json.Marshal(scopeInput{Scope: e.scope})
	if err != nil {
		e.t.Fatalf("marshal: %v", err)
	}
	_, err = e.svc.Handle(e.ctx, e.readUnit(), contract.Invocation{Operation: op, Version: 1, Input: raw})
	if got := faultCode(err); got != contract.CodeInvalidInput {
		e.t.Fatalf("%s in read-only unit: fault %q, want invalid_input", op, got)
	}
}

// writeUnit opens one write transaction and returns its unit.
func (e *testEnv) writeUnit() contract.Unit {
	e.t.Helper()
	var u contract.Unit
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		u = unit
		return nil
	}); err != nil {
		e.t.Fatalf("open write unit: %v", err)
	}
	return u
}

// readUnit opens one read snapshot and returns its unit.
func (e *testEnv) readUnit() contract.Unit {
	e.t.Helper()
	var u contract.Unit
	if err := e.db.Read(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		u = unit
		return nil
	}); err != nil {
		e.t.Fatalf("open read unit: %v", err)
	}
	return u
}

// readAs runs one operation inside a read transaction.
func (e *testEnv) readAs(op string, in any) (contract.Payload, error) {
	e.t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		e.t.Fatalf("marshal: %v", err)
	}
	var payload contract.Payload
	err = e.db.Read(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		var callErr error
		payload, callErr = e.svc.Handle(e.ctx, unit, contract.Invocation{Operation: op, Version: 1, Input: raw})
		return callErr
	})
	return payload, err
}

func TestBindValidatesInputSchema(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	cases := []struct {
		name string
		raw  json.RawMessage
	}{
		{"unknown input field", json.RawMessage(`{"scope":{"installation_id":"x"},"surprise":1}`)},
		{"missing required scope", json.RawMessage(`{}`)},
		{"wrong type", json.RawMessage(`{"scope":"not-an-object"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.svc.Handle(e.ctx, e.writeUnit(), contract.Invocation{Operation: opInspect, Version: 1, Input: tc.raw})
			if got := faultCode(err); got != contract.CodeInvalidInput {
				t.Fatalf("%s: fault %q, want invalid_input", tc.name, got)
			}
		})
	}
}

func TestBindValidatesOutputShape(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	payload := e.mustOK(opInspect, scopeInput{Scope: e.scope})
	var parsed map[string]any
	if err := json.Unmarshal(payload.Data, &parsed); err != nil {
		t.Fatalf("payload data is not JSON: %v", err)
	}
	if _, ok := parsed["limits"]; !ok {
		t.Fatalf("inspect output missing limits: %s", payload.Data)
	}
	if _, ok := parsed["usage"]; !ok {
		t.Fatalf("inspect output missing usage: %s", payload.Data)
	}
}

func TestHandleFaultPassthrough(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	other := e.ids.New()
	f := e.expectFault(opUsageGet, scopeInput{Scope: wireScope{InstallationID: other}}, contract.CodePermissionDenied)
	if f.Message == "" {
		t.Fatalf("permission fault carries no message")
	}
}

func TestPrerequisiteMissingWithoutPorts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.svc.deps.Ports = nil
	scoped := e.scope
	scoped.OrganizationID = e.ids.New()
	_ = e.expectFault(opInspect, scopeInput{Scope: scoped}, contract.CodePrerequisiteMissing)
}

func TestCanonicalJSONIsStable(t *testing.T) {
	t.Parallel()
	first, err := canonicalJSON(limits("USD", 5, 2))
	if err != nil {
		t.Fatalf("canonicalJSON: %v", err)
	}
	second, err := canonicalJSON(limits("USD", 5, 2))
	if err != nil {
		t.Fatalf("canonicalJSON: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("canonicalJSON is unstable: %s vs %s", first, second)
	}
}
