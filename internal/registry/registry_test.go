package registry

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// internalModule is a module exposing one internal-only operation. Internal
// operations carry no CLI/MCP mappings and never shadow a catalog operation.
func internalModule(name, op string, callers ...string) *fakeOwner {
	d := contract.Descriptor{
		ID: op, Version: 1, Owner: name,
		Visibility: contract.VisibilityInternal, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
		InputSchema:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"x":{"type":"string"}}}`),
		OutputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"y":{"type":"string"}}}`),
		Callers:      callers,
	}
	return newFakeOwner(name, []contract.Descriptor{d})
}

// mutatedModule returns only the module owning opID, with the descriptor
// mutated: descriptor-level rejections fire before surface completeness.
func mutatedModule(t *testing.T, opID string, mutate func(d *contract.Descriptor)) contract.Module {
	t.Helper()
	for _, m := range catalogModules() {
		owner, ok := fakeOwnerOf(m)
		if !ok {
			continue
		}
		for i := range owner.descriptors {
			if owner.descriptors[i].ID == opID {
				clone := cloneDescriptor(&owner.descriptors[i])
				mutate(&clone)
				owner.descriptors[i] = clone
				return m
			}
		}
	}
	t.Fatalf("no module owns operation %s", opID)
	return nil
}

// fakeOwnerOf unwraps the fake owner behind a module, whether it is a plain
// module or a LocalIO wrapper.
func fakeOwnerOf(m contract.Module) (*fakeOwner, bool) {
	switch mm := m.(type) {
	case *fakeOwner:
		return mm, true
	case *fakeOwnerIO:
		return mm.fakeOwner, true
	}
	return nil, false
}

func TestNewAssemblesTheCompleteFrozenSurface(t *testing.T) {
	reg, err := newFakeRegistry()
	if err != nil {
		t.Fatalf("assembly over the frozen catalog failed: %v", err)
	}
	public := reg.Public()
	if len(public) != 197 {
		t.Fatalf("Public() holds %d operations, want the 197 frozen ones", len(public))
	}
	if !slices.IsSortedFunc(public, func(a, b contract.Descriptor) int { return strings.Compare(a.ID, b.ID) }) {
		t.Fatal("Public() is not sorted by operation ID")
	}
	for _, d := range public {
		if d.Visibility != contract.VisibilityPublic {
			t.Fatalf("operation %s is not public", d.ID)
		}
		if len(d.CLI) == 0 || d.MCP == "" {
			t.Fatalf("operation %s lacks its CLI/MCP mappings", d.ID)
		}
		if want := cliTokensFor(d.ID); !slices.Equal(d.CLI, want) {
			t.Fatalf("operation %s: CLI %v does not match the derived mapping", d.ID, d.CLI)
		}
		if want := mcpNameFor(d.ID); d.MCP != want {
			t.Fatalf("operation %s: MCP %q does not match the derived %q", d.ID, d.MCP, want)
		}
		if len(d.InputSchema) == 0 || len(d.OutputSchema) == 0 {
			t.Fatalf("operation %s lacks input/output schemas", d.ID)
		}
	}
}

func TestNewRejectsIncompleteSurface(t *testing.T) {
	modules := catalogModules()
	filtered := modules[:0]
	for _, m := range modules {
		if m.Name() != "tasks" {
			filtered = append(filtered, m)
		}
	}
	_, err := New(filtered)
	if err == nil {
		t.Fatal("assembly accepted a surface that is missing the tasks owner")
	}
	if !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("error %q does not name the unregistered operation", err.Error())
	}
}

func TestNewRejectsBadAssemblies(t *testing.T) {
	modules := catalogModules()
	cases := []struct {
		name string
		mod  func() []contract.Module
		want string
	}{
		{
			name: "nil module",
			mod:  func() []contract.Module { return append(slices.Clone(modules), nil) },
			want: "nil module",
		},
		{
			name: "duplicate module",
			mod:  func() []contract.Module { return append(slices.Clone(modules), modules[0]) },
			want: "assembled more than once",
		},
		{
			name: "invalid module name",
			mod: func() []contract.Module {
				return append(slices.Clone(modules), newFakeOwner("Bad-Owner", nil))
			},
			want: "not a valid owner name",
		},
	}
	for _, tc := range cases {
		_, err := New(tc.mod())
		if err == nil {
			t.Fatalf("%s: assembly succeeded", tc.name)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error %q does not mention %q", tc.name, err.Error(), tc.want)
		}
	}
}

func TestNewRejectsOwnerMismatch(t *testing.T) {
	_, err := New([]contract.Module{mutatedModule(t, "artifact.get", func(d *contract.Descriptor) { d.Owner = "other" })})
	if err == nil {
		t.Fatal("assembly accepted a descriptor whose owner differs from its module")
	}
	if !strings.Contains(err.Error(), "declares owner") {
		t.Fatalf("error %q does not name the owner mismatch", err.Error())
	}
}

func TestNewRejectsCatalogDivergence(t *testing.T) {
	cases := []struct {
		name   string
		opID   string
		mutate func(d *contract.Descriptor)
		want   string
	}{
		{"effect", "artifact.get", func(d *contract.Descriptor) { d.Effect = contract.EffectExternalMutation }, "does not match the frozen effect"},
		{"mode to query", "artifact.export", func(d *contract.Descriptor) { d.Mode = contract.ModeQuery }, "must not require a submission key"},
		{"mode to mutation", "artifact.get", func(d *contract.Descriptor) { d.Mode = contract.ModeMutation }, "requires a submission key"},
		{"version", "artifact.get", func(d *contract.Descriptor) { d.Version = 2 }, "does not match the frozen version"},
		{"cli", "artifact.get", func(d *contract.Descriptor) { d.CLI = []string{"artifact", "fetch"} }, "CLI mapping"},
		{"derived cli", "artifact.get", func(d *contract.Descriptor) { d.CLI = []string{"artifact", "get", "extra"} }, "CLI mapping"},
		{"mcp", "artifact.get", func(d *contract.Descriptor) { d.MCP = "zatiti_other" }, "MCP name"},
		{"submission key", "artifact.get", func(d *contract.Descriptor) { d.SubmissionKey = true }, "submission key"},
		{"expected version", "artifact.get", func(d *contract.Descriptor) { d.ExpectedVersion = true }, "expected-version"},
		{"scope", "artifact.get", func(d *contract.Descriptor) { d.ScopeRequired = []string{"other"} }, "scope requirements"},
		{"input schema", "artifact.get", func(d *contract.Descriptor) { d.InputSchema = json.RawMessage(`{"type":"object"}`) }, "input schema"},
		{"output schema", "artifact.get", func(d *contract.Descriptor) { d.OutputSchema = json.RawMessage(`{"type":"object"}`) }, "output schema"},
		{"completion presence", "artifact.export", func(d *contract.Descriptor) { d.CompletionSchema = nil }, "completion schema"},
	}
	for _, tc := range cases {
		_, err := New([]contract.Module{mutatedModule(t, tc.opID, tc.mutate)})
		if err == nil {
			t.Fatalf("%s: assembly accepted a divergent descriptor", tc.name)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error %q does not mention %q", tc.name, err.Error(), tc.want)
		}
	}
}

func TestNewRejectsQueryWithSubmissionKey(t *testing.T) {
	_, err := New([]contract.Module{mutatedModule(t, "artifact.get", func(d *contract.Descriptor) { d.SubmissionKey = true })})
	if err == nil {
		t.Fatal("assembly accepted a query requiring a submission key")
	}
	if !strings.Contains(err.Error(), "query operation must not require a submission key") {
		t.Fatalf("error %q does not name the mode violation", err.Error())
	}
}

func TestNewRejectsPublicOperationOutsideCatalog(t *testing.T) {
	d := contract.Descriptor{
		ID: "tester.op", Version: 1, Owner: "tester",
		Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
	}
	_, err := New([]contract.Module{newFakeOwner("tester", []contract.Descriptor{d})})
	if err == nil {
		t.Fatal("assembly accepted a public operation outside the frozen catalog")
	}
	if !strings.Contains(err.Error(), "not in the frozen catalog") {
		t.Fatalf("error %q does not name the catalog absence", err.Error())
	}
}

func TestNewRejectsInternalShadowingAndMappings(t *testing.T) {
	if _, err := New([]contract.Module{internalModule("tester", "artifact.get")}); err == nil ||
		!strings.Contains(err.Error(), "frozen public operation") {
		t.Fatal("assembly accepted an internal descriptor shadowing a catalog operation")
	}
	withMappings := internalModule("tester", "tester.hidden")
	withMappings.descriptors[0].CLI = []string{"tester"}
	if _, err := New([]contract.Module{withMappings}); err == nil ||
		!strings.Contains(err.Error(), "must not declare CLI") {
		t.Fatal("assembly accepted an internal operation with CLI mappings")
	}
}

func TestNewRejectsUnknownCallers(t *testing.T) {
	modules := append(catalogModules(), internalModule("tester", "tester.hidden", "ghost.op"))
	_, err := New(modules)
	if err == nil {
		t.Fatal("assembly accepted an unknown internal caller")
	}
	if !strings.Contains(err.Error(), "unknown caller") {
		t.Fatalf("error %q does not name the unknown caller", err.Error())
	}
	// The same surface with a registered caller assembles cleanly.
	modules = append(catalogModules(), internalModule("tester", "tester.hidden", "artifact.get"))
	if _, err := New(modules); err != nil {
		t.Fatalf("registered internal caller rejected: %v", err)
	}
}

func TestNewRejectsDuplicateOperationVersions(t *testing.T) {
	m := internalModule("tester", "tester.hidden")
	m.descriptors = append(m.descriptors, cloneDescriptor(&m.descriptors[0]))
	_, err := New([]contract.Module{m})
	if err == nil {
		t.Fatal("assembly accepted one operation version twice")
	}
	if !strings.Contains(err.Error(), "registered more than once") {
		t.Fatalf("error %q does not name the duplicate", err.Error())
	}
}

func TestNewRequiresLocalIOOwnersToImplementTheSeam(t *testing.T) {
	modules := catalogModules()
	replaced := false
	for i, m := range modules {
		if io, ok := m.(*fakeOwnerIO); ok && io.Name() == "artifacts" {
			modules[i] = newFakeOwner(io.Name(), io.descriptors)
			replaced = true
		}
	}
	if !replaced {
		t.Fatal("catalog modules do not include an artifacts LocalIO owner")
	}
	_, err := New(modules)
	if err == nil {
		t.Fatal("assembly accepted a local-IO operation without a LocalIO owner")
	}
	if !strings.Contains(err.Error(), "contract.LocalIO") {
		t.Fatalf("error %q does not name the LocalIO seam", err.Error())
	}
}

func TestLookupReturnsTypedEntries(t *testing.T) {
	reg, err := newFakeRegistry()
	if err != nil {
		t.Fatalf("assembly failed: %v", err)
	}
	d, handler, err := reg.Lookup("artifact.get", 1)
	if err != nil {
		t.Fatalf("lookup failed: %v", err)
	}
	if d.ID != "artifact.get" || d.Version != 1 || handler == nil {
		t.Fatalf("lookup returned descriptor %+v", d)
	}
	if _, _, err := reg.Lookup("ghost.op", 1); faultCodeOf(t, err) != contract.CodeNotFound {
		t.Fatal("unknown operation must be a not_found fault")
	}
	if _, _, err := reg.Lookup("artifact.get", 99); faultCodeOf(t, err) != contract.CodeNotFound {
		t.Fatal("unknown version must be a not_found fault")
	}
}

func TestLookupAndPublicReturnCopies(t *testing.T) {
	reg, err := newFakeRegistry()
	if err != nil {
		t.Fatalf("assembly failed: %v", err)
	}
	d, _, err := reg.Lookup("artifact.get", 1)
	if err != nil {
		t.Fatalf("lookup failed: %v", err)
	}
	d.CLI[0] = "tampered"
	d.InputSchema = json.RawMessage(`{}`)
	again, _, err := reg.Lookup("artifact.get", 1)
	if err != nil {
		t.Fatalf("re-lookup failed: %v", err)
	}
	if !slices.Equal(again.CLI, cliTokensFor("artifact.get")) || len(again.InputSchema) < 10 {
		t.Fatal("Lookup results are not isolated copies")
	}

	public := reg.Public()
	public[0].CLI[0] = "tampered"
	public[0].InputSchema = json.RawMessage(`{}`)
	fresh := reg.Public()
	if fresh[0].CLI[0] == "tampered" || len(fresh[0].InputSchema) < 10 {
		t.Fatal("Public results are not isolated copies")
	}
}

func TestInternalVisibilityIsTheTypedBoundary(t *testing.T) {
	reg, err := New(append(catalogModules(), internalModule("tester", "tester.hidden")))
	if err != nil {
		t.Fatalf("assembly failed: %v", err)
	}
	for _, d := range reg.Public() {
		if d.ID == "tester.hidden" {
			t.Fatal("internal operation appears in Public()")
		}
	}
	if len(reg.Public()) != 197 {
		t.Fatalf("Public() holds %d operations, want 197", len(reg.Public()))
	}
	// Internal operations resolve through the same typed boundary; routing
	// them away from public transport is the application's decision.
	d, handler, err := reg.Lookup("tester.hidden", 1)
	if err != nil || handler == nil {
		t.Fatalf("internal operation is not resolvable: %v", err)
	}
	payload, err := handler(context.Background(), &fakeUnit{}, contract.Invocation{
		Operation: d.ID,
		Version:   d.Version,
		Input:     json.RawMessage(`{"x":"ok"}`),
	})
	if err != nil || payload.Status != contract.StatusCompleted {
		t.Fatalf("internal invocation failed: %v payload %v", err, payload)
	}
	doc, err := reg.OpenAPI()
	if err != nil {
		t.Fatalf("OpenAPI failed: %v", err)
	}
	if strings.Contains(string(doc), "tester.hidden") {
		t.Fatal("internal operation appears in the OpenAPI document")
	}
}

func TestRegistryImplementsModule(t *testing.T) {
	var m contract.Module = mustRegistry(t)
	if m.Name() != "registry" {
		t.Fatalf("module name %q, want registry", m.Name())
	}
	if m.Migrations() != nil {
		t.Fatal("the registry owns no migrations")
	}
	descriptors := m.Descriptors()
	ids := make([]string, 0, len(descriptors))
	for _, d := range descriptors {
		ids = append(ids, d.ID)
	}
	slices.Sort(ids)
	if len(ids) != 2 || ids[0] != "capabilities.list" || ids[1] != "capabilities.schema" {
		t.Fatalf("module descriptors %v, want the two capabilities operations", ids)
	}
}

func TestRegistryHandleRoutesOnlyItsOwnOperations(t *testing.T) {
	reg := mustRegistry(t)
	payload, err := reg.Handle(context.Background(), &fakeUnit{}, contract.Invocation{
		Operation: "capabilities.list",
		Version:   1,
		Input:     json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("capabilities.list invocation failed: %v", err)
	}
	if payload.Status != contract.StatusCompleted {
		t.Fatalf("status %q, want completed", payload.Status)
	}
	if _, err := reg.Handle(context.Background(), &fakeUnit{}, contract.Invocation{
		Operation: "artifact.get",
		Version:   1,
		Input:     json.RawMessage(`{}`),
	}); faultCodeOf(t, err) != contract.CodeNotFound {
		t.Fatal("foreign operations must be refused with not_found")
	}
	if _, err := reg.Handle(context.Background(), &fakeUnit{}, contract.Invocation{
		Operation: "capabilities.schema",
		Version:   1,
		Input:     json.RawMessage(`{}`),
	}); faultCodeOf(t, err) != contract.CodeInvalidInput {
		t.Fatal("schema lookup without operation/version must be invalid_input")
	}
}

func TestDispatchRejectsUnitAndVersionViolations(t *testing.T) {
	reg := mustRegistry(t)
	// Mutations refuse read-only units.
	d, handler, err := reg.Lookup("artifact.export", 1)
	if err != nil {
		t.Fatalf("lookup failed: %v", err)
	}
	if d.Mode != contract.ModeMutation {
		t.Fatalf("artifact.export mode %q, want mutation", d.Mode)
	}
	readOnly := &fakeUnit{ro: true}
	input := json.RawMessage(`{"scope":{"installation_id":"00000000-0000-4000-8000-000000000001"},"id":"00000000-0000-4000-8000-000000000002"}`)
	if _, err := handler(context.Background(), readOnly, contract.Invocation{Operation: d.ID, Version: d.Version, Input: input}); faultCodeOf(t, err) != contract.CodeInvalidInput {
		t.Fatal("mutation on a read-only unit must be invalid_input")
	}
	// The same invocation succeeds on a writable unit and reaches the owner.
	unit := &fakeUnit{}
	if _, err := handler(context.Background(), unit, contract.Invocation{Operation: d.ID, Version: d.Version, Input: input}); err != nil {
		t.Fatalf("mutation on a writable unit failed: %v", err)
	}
	if len(unit.events) != 0 {
		t.Fatalf("unexpected events: %v", unit.events)
	}
	// Nil units are an internal defect, not a caller fault.
	if _, err := handler(context.Background(), nil, contract.Invocation{Operation: d.ID, Version: d.Version, Input: input}); faultCodeOf(t, err) != contract.CodeInternalError {
		t.Fatal("nil unit must be an internal fault")
	}
	// Queries succeed on read-only units.
	qd, qh, err := reg.Lookup("artifact.get", 1)
	if err != nil {
		t.Fatalf("lookup failed: %v", err)
	}
	if _, err := qh(context.Background(), readOnly, contract.Invocation{Operation: qd.ID, Version: qd.Version, Input: input}); err != nil {
		t.Fatalf("query on a read-only unit failed: %v", err)
	}
}

func TestDispatchValidatesInputBeforeCallingTheOwner(t *testing.T) {
	modules := catalogModules()
	var artifacts *fakeOwner
	for _, m := range modules {
		if m.Name() == "artifacts" {
			artifacts = m.(*fakeOwnerIO).fakeOwner
		}
	}
	if artifacts == nil {
		t.Fatal("no artifacts owner in the catalog modules")
	}
	reg, err := New(modules)
	if err != nil {
		t.Fatalf("assembly failed: %v", err)
	}
	_, handler, err := reg.Lookup("artifact.get", 1)
	if err != nil {
		t.Fatalf("lookup failed: %v", err)
	}
	before := len(artifacts.calls)
	if _, err := handler(context.Background(), &fakeUnit{}, contract.Invocation{
		Operation: "artifact.get",
		Version:   1,
		Input:     json.RawMessage(`{"id":"00000000-0000-4000-8000-000000000001"}`),
	}); faultCodeOf(t, err) != contract.CodeInvalidInput {
		t.Fatal("input missing its scope must be invalid_input")
	}
	if len(artifacts.calls) != before {
		t.Fatal("the owning module was called despite invalid input")
	}
}

func mustRegistry(t *testing.T) *Registry {
	t.Helper()
	reg, err := newFakeRegistry()
	if err != nil {
		t.Fatalf("assembly failed: %v", err)
	}
	return reg
}
