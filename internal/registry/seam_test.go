package registry

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Seam tests: the registry against descriptors in the form the landed domain
// modules and internal/application actually use. The registry may import
// only internal/contract, so the fixtures below restate real descriptors:
// the metadata (ID, owner, visibility, mode, effect, flags, callers) is
// copied field for field from internal/identity, internal/tasks and
// internal/installation, and the schema documents keep the landed form
// (operation body plus the module's own $defs) with the bodies those
// modules declare. The unedited sixteen-module assembly is proven in
// internal/application's real-assembly test.

// ioLookup restates application.IOLookup, which the registry cannot import.
type ioLookup interface {
	LocalIOFor(operation string) (contract.LocalIO, bool)
}

// candidateDef is an owner-private definition the shared catalog $defs do
// not carry; identity's internal activation schemas reference it.
const candidateDef = `{"type":"object","additionalProperties":false,"properties":{"kind":{"type":"string","maxLength":8192},"expected_version":{"type":"integer","minimum":0}},"required":["kind"]}`

// selfContained renders a schema the way the landed modules deliver it: the
// operation body with a root $defs member holding the shared definitions
// plus any owner-private ones.
func selfContained(t *testing.T, body string, private map[string]string) json.RawMessage {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &root); err != nil {
		t.Fatalf("schema body is not an object: %v", err)
	}
	var defs map[string]json.RawMessage
	if err := json.Unmarshal(mustCatalog(t).defsJSON, &defs); err != nil {
		t.Fatalf("shared definitions: %v", err)
	}
	for name, def := range private {
		defs[name] = json.RawMessage(def)
	}
	rawDefs, err := json.Marshal(defs)
	if err != nil {
		t.Fatalf("definitions do not marshal: %v", err)
	}
	root["$defs"] = rawDefs
	out, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("schema does not marshal: %v", err)
	}
	return out
}

// Landed internal descriptors, field for field.

func identityActivate(t *testing.T) contract.Descriptor {
	return contract.Descriptor{
		ID: "_identity.activate", Version: 1, Owner: "identity",
		Visibility: contract.VisibilityInternal, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
		InputSchema: selfContained(t,
			`{"additionalProperties":false,"properties":{"candidate":{"$ref":"#/$defs/Candidate"}},"required":["candidate"],"type":"object"}`,
			map[string]string{"Candidate": candidateDef}),
		OutputSchema: selfContained(t,
			`{"additionalProperties":false,"properties":{"versions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096}},"required":["versions"],"type":"object"}`,
			map[string]string{"Candidate": candidateDef}),
		ScopeRequired: []string{"installation_id"},
		Callers:       []string{"configuration", "application"},
	}
}

func identityBootstrap(t *testing.T) contract.Descriptor {
	return contract.Descriptor{
		ID: "_identity.bootstrap", Version: 1, Owner: "identity",
		Visibility: contract.VisibilityInternal, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
		InputSchema: selfContained(t,
			`{"additionalProperties":false,"properties":{"owner_id":{"type":"string","format":"uuid"},"credential_id":{"type":"string","format":"uuid"},"store_ref":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"installation_id":{"type":"string","format":"uuid"}},"required":["owner_id","credential_id","store_ref","name","installation_id"],"type":"object"}`,
			nil),
		OutputSchema: selfContained(t,
			`{"additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Principal"}},"required":["resource"],"type":"object"}`,
			nil),
		ScopeRequired: []string{"installation_id"},
		Callers:       []string{"installation"},
	}
}

func tasksReady(t *testing.T) contract.Descriptor {
	return contract.Descriptor{
		ID: "_tasks.ready", Version: 1, Owner: "tasks",
		Visibility: contract.VisibilityInternal, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
		InputSchema: selfContained(t,
			`{"additionalProperties":false,"properties":{"limit":{"type":"integer","minimum":1,"maximum":100}},"required":["limit"],"type":"object"}`,
			nil),
		OutputSchema: selfContained(t,
			`{"additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Task"},"maxItems":500}},"required":["items"],"type":"object"}`,
			nil),
		ScopeRequired: []string{"installation_id"},
		Callers:       []string{"execution", "controller"},
	}
}

// withDescriptors returns the frozen catalog modules with extra descriptors
// appended to their owning modules and every descriptor passed through edit.
func withDescriptors(t *testing.T, extra []contract.Descriptor, edit func(*contract.Descriptor)) []contract.Module {
	t.Helper()
	modules := catalogModules()
	for _, d := range extra {
		placed := false
		for _, m := range modules {
			if owner, ok := fakeOwnerOf(m); ok && owner.name == d.Owner {
				owner.descriptors = append(owner.descriptors, d)
				placed = true
			}
		}
		if !placed {
			t.Fatalf("no catalog module is named %s", d.Owner)
		}
	}
	if edit != nil {
		for _, m := range modules {
			owner, _ := fakeOwnerOf(m)
			for i := range owner.descriptors {
				edit(&owner.descriptors[i])
			}
		}
	}
	return modules
}

// bare strips the landed $defs so a test isolates one seam rule at a time.
func bare(t *testing.T, d contract.Descriptor) contract.Descriptor {
	t.Helper()
	strip := func(schema json.RawMessage) json.RawMessage {
		if len(schema) == 0 {
			return schema
		}
		var root map[string]json.RawMessage
		if err := json.Unmarshal(schema, &root); err != nil {
			t.Fatalf("schema is not an object: %v", err)
		}
		delete(root, "$defs")
		out, err := json.Marshal(root)
		if err != nil {
			t.Fatalf("schema does not marshal: %v", err)
		}
		return out
	}
	d.InputSchema = strip(d.InputSchema)
	d.OutputSchema = strip(d.OutputSchema)
	d.CompletionSchema = strip(d.CompletionSchema)
	return d
}

// Seam 1: internal mutations carry no submission key. They join their
// caller's unit and command; only public mutations bind a key.
func TestSeamInternalMutationNeedsNoSubmissionKey(t *testing.T) {
	d := bare(t, identityBootstrap(t))
	d.Callers = nil
	if _, err := New(withDescriptors(t, []contract.Descriptor{d}, nil)); err != nil {
		t.Fatalf("landed internal mutation without a submission key rejected: %v", err)
	}

	// An internal operation never requires one: no internal caller can
	// supply it.
	keyed := d
	keyed.SubmissionKey = true
	_, err := New(withDescriptors(t, []contract.Descriptor{keyed}, nil))
	if err == nil || !strings.Contains(err.Error(), "internal operation never requires a submission key") {
		t.Fatalf("internal mutation requiring a submission key accepted: %v", err)
	}

	// The public rule is unchanged: every public mutation binds a key,
	// except the one-time init.
	_, err = New([]contract.Module{mutatedModule(t, "artifact.export", func(d *contract.Descriptor) { d.SubmissionKey = false })})
	if err == nil || !strings.Contains(err.Error(), "requires a submission key") {
		t.Fatalf("public mutation without a submission key accepted: %v", err)
	}
}

// Seam 2: Descriptor.Callers names calling OWNERS, the identities
// application's port router binds, not operation IDs.
func TestSeamCallersAreOwnerNames(t *testing.T) {
	ready := bare(t, tasksReady(t))
	reg, err := New(withDescriptors(t, []contract.Descriptor{ready}, nil))
	if err != nil {
		t.Fatalf("landed owner-named caller allowlist rejected: %v", err)
	}
	d, _, err := reg.Lookup("_tasks.ready", 1)
	if err != nil {
		t.Fatalf("lookup failed: %v", err)
	}
	if len(d.Callers) != 2 || d.Callers[0] != "execution" || d.Callers[1] != "controller" {
		t.Fatalf("caller allowlist %v did not survive registration", d.Callers)
	}

	for name, caller := range map[string]string{
		"unassembled owner": "ghost",
		"operation id":      "artifact.get",
		"malformed":         "Not An Owner",
		"empty":             "",
	} {
		bad := ready
		bad.Callers = []string{"execution", caller}
		if _, err := New(withDescriptors(t, []contract.Descriptor{bad}, nil)); err == nil ||
			!strings.Contains(err.Error(), "caller") {
			t.Fatalf("%s: caller %q accepted: %v", name, caller, err)
		}
	}
}

// Seam 3: version 0 addresses the current version, the way application
// dispatches every operation.
func TestSeamLookupVersionZeroIsCurrent(t *testing.T) {
	if d, _, err := mustRegistry(t).Lookup("artifact.get", 0); err != nil || d.Version != 1 {
		t.Fatalf("Lookup(artifact.get, 0) = v%d, %v; want the current version 1", d.Version, err)
	}

	v1 := bare(t, tasksReady(t))
	v2 := v1
	v2.Version = 2
	v2.Callers = []string{"controller"}
	// Registration order must not decide which version is current.
	reg, err := New(withDescriptors(t, []contract.Descriptor{v2, v1}, nil))
	if err != nil {
		t.Fatalf("assembly failed: %v", err)
	}

	current, handler, err := reg.Lookup("_tasks.ready", 0)
	if err != nil {
		t.Fatalf("Lookup(id, 0) failed: %v", err)
	}
	if current.Version != 2 || len(current.Callers) != 1 {
		t.Fatalf("Lookup(id, 0) resolved v%d callers %v, want the highest registered version 2", current.Version, current.Callers)
	}
	// The current handler executes the version the descriptor reports.
	if _, err := handler(context.Background(), &fakeUnit{ro: true}, contract.Invocation{
		Operation: current.ID, Version: current.Version, Input: json.RawMessage(`{"limit":1}`),
	}); err != nil {
		t.Fatalf("current-version handler rejected its own version: %v", err)
	}
	// An unresolved zero never reaches a module.
	if _, err := handler(context.Background(), &fakeUnit{ro: true}, contract.Invocation{
		Operation: current.ID, Version: 0, Input: json.RawMessage(`{"limit":1}`),
	}); faultCodeOf(t, err) != contract.CodeNotFound {
		t.Fatalf("handler accepted an unresolved version 0: %v", err)
	}

	old, _, err := reg.Lookup("_tasks.ready", 1)
	if err != nil || old.Version != 1 || len(old.Callers) != 2 {
		t.Fatalf("exact lookup of v1 returned v%d callers %v: %v", old.Version, old.Callers, err)
	}
	if _, _, err := reg.Lookup("_tasks.ready", 3); faultCodeOf(t, err) != contract.CodeNotFound {
		t.Fatalf("unregistered version must be not_found: %v", err)
	}
	if _, _, err := reg.Lookup("_tasks.ready", -1); faultCodeOf(t, err) != contract.CodeNotFound {
		t.Fatalf("negative version must be not_found: %v", err)
	}
	if _, _, err := reg.Lookup("ghost.op", 0); faultCodeOf(t, err) != contract.CodeNotFound {
		t.Fatalf("unknown operation at version 0 must be not_found: %v", err)
	}

	// Every public operation resolves at version 0 to its frozen version.
	for _, d := range reg.Public() {
		got, h, err := reg.Lookup(d.ID, 0)
		if err != nil || h == nil || got.Version != d.Version {
			t.Fatalf("Lookup(%s, 0) = v%d, %v; want v%d", d.ID, got.Version, err, d.Version)
		}
	}
}

// Seam 4: the registry hands application the owning module's real LocalIO
// for the registered local IO operations, so application sequences Prepare,
// Perform outside transactions and Finish itself.
func TestSeamLocalIORouting(t *testing.T) {
	modules := catalogModules()
	owners := map[string]*fakeOwnerIO{}
	for _, m := range modules {
		if io, ok := m.(*fakeOwnerIO); ok {
			owners[io.Name()] = io
		}
	}
	reg, err := New(modules)
	if err != nil {
		t.Fatalf("assembly failed: %v", err)
	}
	lookup, ok := any(reg).(ioLookup)
	if !ok {
		t.Fatal("*Registry does not expose LocalIOFor(operation string) (contract.LocalIO, bool)")
	}

	for id := range localIOOperations {
		d, handler, err := reg.Lookup(id, 0)
		if err != nil {
			t.Fatalf("lookup of %s failed: %v", id, err)
		}
		io, ok := lookup.LocalIOFor(id)
		if !ok || io == nil {
			t.Fatalf("LocalIOFor(%s) has no route", id)
		}
		if io != contract.LocalIO(owners[d.Owner]) {
			t.Fatalf("LocalIOFor(%s) is not the owning module %s itself", id, d.Owner)
		}
		// The single-handler path would run Perform inside the unit; it
		// fails closed and touches no phase.
		before := len(owners[d.Owner].io.prepared)
		_, err = handler(context.Background(), &fakeUnit{}, contract.Invocation{
			Operation: id, Version: d.Version,
			Input: validInstance(t, id, mustCatalog(t).byID[id].Input),
		})
		if faultCodeOf(t, err) != contract.CodeInternalError {
			t.Fatalf("local IO operation %s ran through the single-unit handler: %v", id, err)
		}
		if len(owners[d.Owner].io.prepared) != before || len(owners[d.Owner].io.performed) != 0 {
			t.Fatalf("local IO operation %s reached a LocalIO phase inside the unit", id)
		}
	}

	// Only the registered local IO operations route there, even for owners
	// that implement LocalIO.
	for _, id := range []string{"artifact.get", "installation.status", "capabilities.list", "ghost.op", ""} {
		if io, ok := lookup.LocalIOFor(id); ok || io != nil {
			t.Fatalf("LocalIOFor(%q) routed a non-local-IO operation", id)
		}
	}
}

// Seam 5: the landed modules deliver self-contained schema documents;
// application validates Descriptor.InputSchema exactly as Lookup returns it.
func TestSeamSelfContainedSchemas(t *testing.T) {
	landed := func(d *contract.Descriptor) {
		if d.Visibility != contract.VisibilityPublic {
			return
		}
		d.InputSchema = selfContained(t, string(d.InputSchema), nil)
		d.OutputSchema = selfContained(t, string(d.OutputSchema), nil)
		if len(d.CompletionSchema) != 0 {
			d.CompletionSchema = selfContained(t, string(d.CompletionSchema), nil)
		}
	}
	internal := []contract.Descriptor{identityActivate(t), identityBootstrap(t), tasksReady(t)}
	reg, err := New(withDescriptors(t, internal, landed))
	if err != nil {
		t.Fatalf("landed self-contained schemas rejected: %v", err)
	}

	// The public surface stays the frozen bare form.
	cat := mustCatalog(t)
	for _, d := range reg.Public() {
		if err := sameSchema(d.InputSchema, cat.byID[d.ID].Input, "input"); err != nil {
			t.Fatalf("Public() %s: %v", d.ID, err)
		}
		if err := sameSchema(d.OutputSchema, cat.byID[d.ID].Output, "output"); err != nil {
			t.Fatalf("Public() %s: %v", d.ID, err)
		}
	}

	// Lookup returns documents that validate as delivered, including
	// owner-private definitions.
	d, handler, err := reg.Lookup("_identity.activate", 0)
	if err != nil {
		t.Fatalf("lookup failed: %v", err)
	}
	valid := json.RawMessage(`{"candidate":{"kind":"principal"}}`)
	if err := contract.ValidateSchema(d.InputSchema, valid); err != nil {
		t.Fatalf("Lookup input schema does not validate as delivered: %v", err)
	}
	if err := contract.ValidateSchema(d.InputSchema, json.RawMessage(`{"candidate":{"kind":7}}`)); err == nil {
		t.Fatal("Lookup input schema lost the owner-private definition")
	}
	if _, err := handler(context.Background(), &fakeUnit{}, contract.Invocation{
		Operation: d.ID, Version: d.Version, Input: valid,
	}); err != nil {
		t.Fatalf("handler rejected a schema-valid internal input: %v", err)
	}
	if _, err := handler(context.Background(), &fakeUnit{}, contract.Invocation{
		Operation: d.ID, Version: d.Version, Input: json.RawMessage(`{"candidate":{"kind":7}}`),
	}); faultCodeOf(t, err) != contract.CodeInvalidInput {
		t.Fatalf("handler accepted an input violating the owner-private definition: %v", err)
	}
	for _, id := range []string{"task.create", "artifact.get", "installation.init"} {
		pd, _, err := reg.Lookup(id, 0)
		if err != nil {
			t.Fatalf("lookup of %s failed: %v", id, err)
		}
		if err := contract.ValidateSchema(pd.InputSchema, validInstance(t, id, cat.byID[id].Input)); err != nil {
			t.Fatalf("Lookup(%s) input schema does not validate as delivered: %v", id, err)
		}
	}

	// Bare bodies resolve the same way: five landed modules deliver them.
	bareReg := mustRegistry(t)
	pd, _, err := bareReg.Lookup("task.create", 0)
	if err != nil {
		t.Fatalf("lookup failed: %v", err)
	}
	if err := contract.ValidateSchema(pd.InputSchema, validInstance(t, "task.create", cat.byID["task.create"].Input)); err != nil {
		t.Fatalf("bare schema does not validate as Lookup delivers it: %v", err)
	}

	// A module cannot redefine a shared definition, and a changed body still
	// fails the frozen comparison.
	_, err = New([]contract.Module{mutatedModule(t, "artifact.get", func(d *contract.Descriptor) {
		d.InputSchema = selfContained(t, string(d.InputSchema), map[string]string{"Scope": `{"type":"object"}`})
	})})
	if err == nil || !strings.Contains(err.Error(), "redefines the shared definition") {
		t.Fatalf("redefined shared definition accepted: %v", err)
	}
	_, err = New([]contract.Module{mutatedModule(t, "artifact.get", func(d *contract.Descriptor) {
		d.InputSchema = selfContained(t, `{"type":"object"}`, nil)
	})})
	if err == nil || !strings.Contains(err.Error(), "does not match the frozen schema") {
		t.Fatalf("self-contained schema with a drifted body accepted: %v", err)
	}
	// The landed modules attach one $defs document to every operation, so a
	// public schema may carry owner-private definitions its frozen body
	// cannot reach. They are dropped, never published.
	extra, err := New(withDescriptors(t, nil, func(d *contract.Descriptor) {
		if d.ID == "artifact.get" {
			d.InputSchema = selfContained(t, string(d.InputSchema), map[string]string{"Candidate": candidateDef})
		}
	}))
	if err != nil {
		t.Fatalf("unreachable owner-private definition rejected: %v", err)
	}
	pd, _, err = extra.Lookup("artifact.get", 0)
	if err != nil {
		t.Fatalf("lookup failed: %v", err)
	}
	if strings.Contains(string(pd.InputSchema), "Candidate") {
		t.Fatal("Lookup document carries an unreachable owner-private definition")
	}
	if !strings.Contains(string(pd.InputSchema), `"Scope"`) {
		t.Fatal("Lookup document lost a reachable shared definition")
	}
}

// The landed shape in one assembly: every seam rule at once.
func TestSeamLandedShapeAssembles(t *testing.T) {
	landed := func(d *contract.Descriptor) {
		d.InputSchema = selfContained(t, string(bare(t, *d).InputSchema), map[string]string{"Candidate": candidateDef})
	}
	internal := []contract.Descriptor{identityActivate(t), identityBootstrap(t), tasksReady(t)}
	// Private definitions ride only on internal descriptors.
	reg, err := New(withDescriptors(t, internal, func(d *contract.Descriptor) {
		if d.Visibility == contract.VisibilityInternal {
			landed(d)
		}
	}))
	if err != nil {
		t.Fatalf("landed-shape assembly failed: %v", err)
	}
	if len(reg.Public()) != 203 {
		t.Fatalf("Public() holds %d operations, want 203", len(reg.Public()))
	}
	for _, d := range reg.Public() {
		if strings.HasPrefix(d.ID, "_") {
			t.Fatalf("internal operation %s appears in Public()", d.ID)
		}
	}
}
