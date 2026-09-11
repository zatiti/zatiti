package connections

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// tool.* behavioral tests: trusted adapter contract discovery, explicit
// binding staging rules and the discovery-grants-no-execution property.

func bindingDef(id contract.ID, scope wireScope, kind string, target contract.ID, permissions, destinations []string) wireBinding {
	return wireBinding{
		ID: id, Version: 1, Scope: scope, Kind: kind, TargetID: target,
		Permissions: permissions, Destinations: destinations,
	}
}

func TestToolGetResolvesSeededContract(t *testing.T) {
	env := newEnv(t)
	payload := env.mustOK("tool.get", connGetIn{Scope: env.scope, ID: toolRESTRead})
	var out resourceToolOut
	env.decode(payload.Data, &out)
	if out.Resource.ID != toolRESTRead || out.Resource.Version != 1 {
		t.Fatalf("resolved %+v", out.Resource)
	}
	if out.Resource.Name != "provider-rest-read" {
		t.Fatalf("resolved contract name %q", out.Resource.Name)
	}
	if out.Resource.Effect != "external_read" {
		t.Fatalf("resolved effect %q", out.Resource.Effect)
	}
	_ = env.expectFault("tool.get", connGetIn{Scope: env.scope, ID: env.ids.New()}, contract.CodeNotFound)
}

func TestToolListIsFixedSnapshot(t *testing.T) {
	env := newEnv(t)
	tools := env.mustOK("tool.list", connListIn{Scope: env.scope})
	var out toolListOut
	env.decode(tools.Data, &out)
	if len(out.Items) != 3 {
		t.Fatalf("catalog carries %d contracts, want the 3 seeded ones", len(out.Items))
	}
	names := map[string]bool{}
	for _, item := range out.Items {
		names[item.Name] = true
	}
	for _, want := range []string{"model-responses", "provider-rest-read", "provider-rest-mutate"} {
		if !names[want] {
			t.Fatalf("catalog missing seeded contract %q: %v", want, names)
		}
	}

	t.Run("filters-refused", func(t *testing.T) {
		filter := listFilter{State: connStateValid}
		_ = env.expectFault("tool.list", connListIn{Scope: env.scope, Filter: &filter}, contract.CodeInvalidInput)
	})

	t.Run("cursors-refused", func(t *testing.T) {
		f := env.expectFault("tool.list", connListIn{Scope: env.scope, Cursor: "anything"}, contract.CodeCursorExpired)
		if !strings.Contains(f.Message, "fixed snapshot") {
			t.Fatalf("refusal message %q does not explain the snapshot rule", f.Message)
		}
	})
}

func TestToolSchemaReturnsInertJSON(t *testing.T) {
	env := newEnv(t)
	payload := env.mustOK("tool.schema", connGetIn{Scope: env.scope, ID: toolModelRead})
	var out schemaOut
	env.decode(payload.Data, &out)
	if len(out.InputSchema) == 0 || len(out.OutputSchema) == 0 {
		t.Fatalf("schema response is empty: %+v", out)
	}
	var probe map[string]any
	if err := json.Unmarshal(out.InputSchema, &probe); err != nil {
		t.Fatalf("input schema is not JSON: %v", err)
	}
	_ = env.expectFault("tool.schema", connGetIn{Scope: env.scope, ID: env.ids.New()}, contract.CodeNotFound)
}

func TestToolBindStagingRules(t *testing.T) {
	env := newEnv(t)
	id := env.ids.New()

	t.Run("stages-qualified-binding", func(t *testing.T) {
		in := bindIn{Scope: env.scope, Binding: bindingDef(id, env.scope, bindingKindTool, toolRESTRead,
			[]string{"connections.call"}, []string{"api.github.com"})}
		payload := env.mustOK("tool.bind", in)
		var out draftOut
		env.decode(payload.Data, &out)
		var staged wireChange
		if err := json.Unmarshal(out.Resource.Changes[0], &staged); err != nil {
			t.Fatalf("decode staged change: %v", err)
		}
		if staged.Kind != kindBinding || staged.Action != actionCreate || staged.ExpectedVersion != 0 {
			t.Fatalf("staged binding shape: %+v", staged)
		}
		var def wireBinding
		if err := json.Unmarshal(staged.Definition, &def); err != nil {
			t.Fatalf("decode staged binding: %v", err)
		}
		if def.TargetID != toolRESTRead || len(def.Permissions) != 1 {
			t.Fatalf("staged binding mismatch: %+v", def)
		}
	})

	t.Run("unknown-adapter-refused", func(t *testing.T) {
		in := bindIn{Scope: env.scope, Binding: bindingDef(env.ids.New(), env.scope, bindingKindTool,
			env.ids.New(), []string{"connections.call"}, nil)}
		f := env.expectFault("tool.bind", in, contract.CodeNotFound)
		if !strings.Contains(f.Message, "unknown adapter") {
			t.Fatalf("refusal message %q does not name the unknown adapter", f.Message)
		}
	})

	t.Run("destination-outside-contract-refused", func(t *testing.T) {
		in := bindIn{Scope: env.scope, Binding: bindingDef(env.ids.New(), env.scope, bindingKindTool, toolRESTRead,
			[]string{"connections.call"}, []string{"outside.example.test"})}
		f := env.expectFault("tool.bind", in, contract.CodeInvalidInput)
		if !strings.Contains(f.Message, "outside") {
			t.Fatalf("refusal message %q does not name the destination defect", f.Message)
		}
	})

	t.Run("executable-kind-demands-permissions", func(t *testing.T) {
		in := bindIn{Scope: env.scope, Binding: bindingDef(env.ids.New(), env.scope, bindingKindTool, toolRESTRead,
			[]string{}, nil)}
		f := env.expectFault("tool.bind", in, contract.CodeInvalidInput)
		if !strings.Contains(f.Message, "permission") {
			t.Fatalf("refusal message %q does not demand permissions", f.Message)
		}
	})

	t.Run("binding-installation-scope-mismatch-refused", func(t *testing.T) {
		b := bindingDef(env.ids.New(), wireScope{InstallationID: env.ids.New()}, bindingKindTool,
			toolRESTRead, []string{"connections.call"}, nil)
		_ = env.expectFault("tool.bind", bindIn{Scope: env.scope, Binding: b}, contract.CodeInvalidInput)
	})
}

func TestToolUnbindStagesArchive(t *testing.T) {
	env := newEnv(t)
	in := bindIn{Scope: env.scope, Binding: wireBinding{
		ID: env.ids.New(), Version: 3, Scope: env.scope, Kind: bindingKindTool,
		TargetID: toolRESTRead, Permissions: []string{"connections.call"},
	}}
	payload := env.mustOK("tool.unbind", in)
	var out draftOut
	env.decode(payload.Data, &out)
	var staged wireChange
	if err := json.Unmarshal(out.Resource.Changes[0], &staged); err != nil {
		t.Fatalf("decode staged change: %v", err)
	}
	if staged.Kind != kindBinding || staged.Action != actionArchive || staged.ExpectedVersion != 3 {
		t.Fatalf("staged unbind shape: %+v", staged)
	}

	t.Run("non-tool-kind-refused", func(t *testing.T) {
		other := in
		other.Binding.Kind = "skill"
		other.Binding.ID = env.ids.New()
		f := env.expectFault("tool.unbind", other, contract.CodeInvalidInput)
		if !strings.Contains(f.Message, "tool") {
			t.Fatalf("refusal message %q does not name the kind restriction", f.Message)
		}
	})
}

// TestExternalPluginDiscoveryGrantsNoExecution is the Z05 local property:
// discovery of a callback, redirect or advertised behavior outside the
// registered reviewed destination/adapter contract confers no authority.
// A discovered identity cannot be registered as a tool at runtime, cannot be
// bound without an existing contract, and executable bindings cannot exist
// without qualified permissions.
func TestExternalPluginDiscoveryGrantsNoExecution(t *testing.T) {
	env := newEnv(t)

	// A freshly discovered tool identity is unknown to the binary: binding it
	// refuses and the contract lookup returns nothing.
	unknown := env.ids.New()
	_ = env.expectFault("tool.bind", bindIn{Scope: env.scope, Binding: bindingDef(
		env.ids.New(), env.scope, bindingKindTool, unknown, []string{"connections.call"}, nil)},
		contract.CodeNotFound)
	_ = env.expectFault("tool.get", connGetIn{Scope: env.scope, ID: unknown}, contract.CodeNotFound)

	// The compiler boundary refuses every non-connection change, so no other
	// owner can register tools through activation either.
	_ = env.expectFault("_connections.activate", candidateInBody{Candidate: candidateBody{
		PlanID:          env.ids.New(),
		BaseRevision:    1,
		CandidateDigest: testDigest("discovery"),
		Changes: []wireChange{{
			Kind: "tool", Action: actionCreate, ID: unknown, ExpectedVersion: 0,
			Definition: mustRaw(map[string]any{"name": toolUnknownName}),
		}},
		Dependencies: []wireRef{},
	}}, contract.CodeInvalidInput)

	// The contract catalog itself is unchanged by all discovery attempts:
	// contracts ship only with a release, and no public operation writes
	// connections_contracts.
	payload := env.mustOK("tool.list", connListIn{Scope: env.scope})
	var out toolListOut
	env.decode(payload.Data, &out)
	if len(out.Items) != 3 {
		t.Fatalf("discovery changed the catalog size to %d", len(out.Items))
	}
	for _, item := range out.Items {
		if item.Version != 1 {
			t.Fatalf("discovery bumped %s to version %d", item.ID, item.Version)
		}
	}
}

// mustRaw marshals v or panics — test fixture helper.
func mustRaw(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic("mustRaw: " + err.Error())
	}
	return raw
}
