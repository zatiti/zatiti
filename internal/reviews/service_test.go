package reviews

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Construction, descriptor and dispatch-level behavior.

func TestNewRequiresDependencies(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)}
	ids := &seqIDs{}
	ports := newFakePorts()

	if _, err := New(contract.Dependencies{}); err == nil {
		t.Fatal("New without clock: expected error")
	}
	if _, err := New(contract.Dependencies{Clock: clock}); err == nil {
		t.Fatal("New without ids: expected error")
	}
	if _, err := New(contract.Dependencies{Clock: clock, IDs: ids}); err == nil {
		t.Fatal("New without ports: expected error")
	}
	s, err := New(contract.Dependencies{Clock: clock, IDs: ids, Ports: ports})
	if err != nil {
		t.Fatalf("New with all dependencies: %v", err)
	}
	if s.Name() != "reviews" {
		t.Fatalf("Name() = %q, want reviews", s.Name())
	}
}

func TestMigrationsAreOwnerPinned(t *testing.T) {
	migs := migrations()
	if len(migs) != 1 {
		t.Fatalf("len(migrations()) = %d, want 1", len(migs))
	}
	m := migs[0]
	if m.Owner != "reviews" || m.Version != 1 {
		t.Fatalf("migration owner/version = %s/%d, want reviews/1", m.Owner, m.Version)
	}
	if m.SHA256 != contract.Hash([]byte(m.SQL)) {
		t.Fatal("migration SHA256 does not pin its SQL")
	}
	for _, table := range []string{"reviews_requests", "reviews_decisions", "reviews_delegations", "reviews_reviewers"} {
		if !strings.Contains(m.SQL, "CREATE TABLE "+table) {
			t.Fatalf("migration SQL does not create %s", table)
		}
	}
}

func TestDescriptorsMirrorCatalog(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)}
	svc, err := New(contract.Dependencies{Clock: clock, IDs: &seqIDs{}, Ports: newFakePorts()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	descs := svc.Descriptors()
	if len(descs) != 6 {
		t.Fatalf("len(Descriptors()) = %d, want 6", len(descs))
	}
	want := map[string]contract.Descriptor{
		opCheck: {
			Visibility: contract.VisibilityInternal, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			ScopeRequired: []string{"installation_id"},
			Callers:       []string{"effects", "configuration", "policy", "tasks"},
		},
		opEnsure: {
			Visibility: contract.VisibilityInternal, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			ScopeRequired: []string{"installation_id"},
			Callers:       []string{"effects", "configuration", "policy"},
		},
		opDecide: {
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI: []string{"review", "decide"}, MCP: "zatiti_review_decide",
			ScopeRequired: []string{"installation_id"}, SubmissionKey: true, ExpectedVersion: true,
		},
		opDelegate: {
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI: []string{"review", "delegate"}, MCP: "zatiti_review_delegate",
			ScopeRequired: []string{"installation_id"}, SubmissionKey: true, ExpectedVersion: true,
		},
		opGet: {
			Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			CLI: []string{"review", "get"}, MCP: "zatiti_review_get",
			ScopeRequired: []string{"installation_id"},
		},
		opList: {
			Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			CLI: []string{"review", "list"}, MCP: "zatiti_review_list",
			ScopeRequired: []string{"installation_id"},
		},
	}
	seen := map[string]bool{}
	for _, d := range descs {
		w, ok := want[d.ID]
		if !ok {
			t.Fatalf("unexpected descriptor %s", d.ID)
		}
		seen[d.ID] = true
		if d.Owner != "reviews" {
			t.Fatalf("%s owner = %q, want reviews", d.ID, d.Owner)
		}
		if d.Version != descriptorVersion {
			t.Fatalf("%s version = %d, want %d", d.ID, d.Version, descriptorVersion)
		}
		if d.Visibility != w.Visibility || d.Mode != w.Mode || d.Effect != w.Effect {
			t.Fatalf("%s visibility/mode/effect = %s/%s/%s, want %s/%s/%s",
				d.ID, d.Visibility, d.Mode, d.Effect, w.Visibility, w.Mode, w.Effect)
		}
		if d.MCP != w.MCP {
			t.Fatalf("%s mcp = %q, want %q", d.ID, d.MCP, w.MCP)
		}
		if len(d.CLI) != len(w.CLI) {
			t.Fatalf("%s cli = %v, want %v", d.ID, d.CLI, w.CLI)
		}
		for i := range d.CLI {
			if d.CLI[i] != w.CLI[i] {
				t.Fatalf("%s cli = %v, want %v", d.ID, d.CLI, w.CLI)
			}
		}
		if len(d.ScopeRequired) != len(w.ScopeRequired) || (len(w.ScopeRequired) > 0 && d.ScopeRequired[0] != w.ScopeRequired[0]) {
			t.Fatalf("%s scope_required = %v, want %v", d.ID, d.ScopeRequired, w.ScopeRequired)
		}
		if d.SubmissionKey != w.SubmissionKey || d.ExpectedVersion != w.ExpectedVersion {
			t.Fatalf("%s submission_key=%v expected_version=%v, want %v/%v",
				d.ID, d.SubmissionKey, d.ExpectedVersion, w.SubmissionKey, w.ExpectedVersion)
		}
		if (d.Callers == nil) != (w.Callers == nil) || len(d.Callers) != len(w.Callers) {
			t.Fatalf("%s callers = %v, want %v", d.ID, d.Callers, w.Callers)
		}
		for i := range d.Callers {
			if d.Callers[i] != w.Callers[i] {
				t.Fatalf("%s callers = %v, want %v", d.ID, d.Callers, w.Callers)
			}
		}
		// Public operations must not declare callers; internal ones must.
		if d.Visibility == contract.VisibilityPublic && len(d.Callers) != 0 {
			t.Fatalf("%s public descriptor declares callers %v", d.ID, d.Callers)
		}
		if d.Visibility == contract.VisibilityInternal && len(d.Callers) == 0 {
			t.Fatalf("%s internal descriptor declares no callers", d.ID)
		}
		// Public mutations carry submission keys for idempotent replays;
		// internal operations (including internal mutations like ensure)
		// and queries do not, matching the catalog.
		if d.Visibility == contract.VisibilityPublic && d.Mode == contract.ModeMutation && !d.SubmissionKey {
			t.Fatalf("%s public mutation lacks a submission key", d.ID)
		}
		if (d.Visibility == contract.VisibilityInternal || d.Mode == contract.ModeQuery) && d.SubmissionKey {
			t.Fatalf("%s internal or query operation carries a submission key", d.ID)
		}
		if len(d.InputSchema) == 0 || len(d.OutputSchema) == 0 {
			t.Fatalf("%s missing schemas", d.ID)
		}
		if !strings.Contains(string(d.InputSchema), `"$defs"`) {
			t.Fatalf("%s input schema does not splice the shared $defs", d.ID)
		}
	}
	for id := range want {
		if !seen[id] {
			t.Fatalf("descriptor %s missing", id)
		}
	}
}

func TestHandleDispatch(t *testing.T) {
	e := newEnv(t)
	actor := e.actorFor(e.principal(contract.KindService))

	// Unknown operation.
	if _, err := e.svc.Handle(e.ctx, nil, contract.Invocation{Operation: "review.unknown", Version: 1}); err == nil {
		t.Fatal("unknown operation: expected error")
	} else if f := faultCode(err); f != contract.CodeInvalidInput {
		t.Fatalf("unknown operation fault = %s, want invalid_input", f)
	}

	// Version pinning.
	if _, err := e.svc.Handle(e.ctx, nil, contract.Invocation{Operation: opDecide, Version: 2}); err == nil {
		t.Fatal("unsupported version: expected error")
	} else if f := faultCode(err); f != contract.CodeInvalidInput {
		t.Fatalf("version fault = %s, want invalid_input", f)
	}

	// Schema rejection: unknown fields are refused, so an approved_by_human
	// assertion can never enter the pipeline at all.
	if _, err := e.callAs(actor, e.scope, opCheck, map[string]any{
		"scope": e.scope, "action_digest": strings.Repeat("a", 64), "approved_by_human": true,
	}); err == nil {
		t.Fatal("unknown input field: expected error")
	} else if f := faultCode(err); f != contract.CodeInvalidInput {
		t.Fatalf("unknown field fault = %s, want invalid_input", f)
	}

	// Digest pattern enforcement.
	if _, err := e.callAs(actor, e.scope, opCheck, map[string]any{
		"scope": e.scope, "action_digest": "not-a-digest",
	}); err == nil {
		t.Fatal("malformed digest: expected error")
	} else if f := faultCode(err); f != contract.CodeInvalidInput {
		t.Fatalf("malformed digest fault = %s, want invalid_input", f)
	}
}

func TestMutationRefusedOnReadSnapshot(t *testing.T) {
	e := newEnv(t)
	actor := e.actorFor(e.principal(contract.KindService))
	human := e.principal(contract.KindHuman)
	in := ensureInputFor(t, e.scope,
		actionFixture(e.scope, "https://api.example.com/v1/deploy"),
		requirementFixture([]contract.ID{human}, true))

	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	err = e.db.Read(e.ctx, actor, e.scope, func(unit contract.Unit) error {
		_, err := e.svc.Handle(e.ctx, unit, contract.Invocation{Operation: opEnsure, Version: 1, Input: raw})
		if err == nil {
			t.Fatal("ensure on read snapshot: expected error")
		}
		if f := faultCode(err); f != contract.CodeInvalidInput {
			t.Fatalf("read snapshot fault = %s, want invalid_input", f)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read transaction: %v", err)
	}
}
