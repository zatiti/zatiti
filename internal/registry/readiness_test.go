package registry

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// notReadyOwner is a module whose descriptors are the real frozen ones for
// its owner -- New considers every one of them registered and discoverable
// -- but whose Handle always reports the runtime prerequisite a live
// provider, memory backend or job runner would need, honestly, instead of
// pretending the operation executed. It proves that the registry's own
// notion of "available" (structurally registered, discoverable through
// capabilities.list/schema, routable through Lookup) is independent of
// whether the module behind it has anything actually configured: the
// registry is a typed catalog and dispatcher, never a readiness or
// qualification authority for its owners' external dependencies.
type notReadyOwner struct {
	*fakeOwner
	reason string
}

func newNotReadyOwner(name string, descriptors []contract.Descriptor, reason string) *notReadyOwner {
	return &notReadyOwner{fakeOwner: newFakeOwner(name, descriptors), reason: reason}
}

func (m *notReadyOwner) Handle(ctx context.Context, unit contract.Unit, invocation contract.Invocation) (contract.Payload, error) {
	m.calls = append(m.calls, invocation)
	return contract.Payload{}, &contract.Fault{
		Code:    contract.CodePrerequisiteMissing,
		Message: "operation " + invocation.Operation + ": " + m.reason,
	}
}

// TestOperationAvailabilityDoesNotImplyRuntimeReadiness proves the
// distinction the card's step 2 requires: an operation whose owning module
// has no hosted provider, memory backend or job runner configured is still
// registered, still enumerated by capabilities.list/schema and still
// routable by Lookup -- discovery is a property of the frozen descriptor and
// schema, not of the provider behind it -- while actually invoking it
// reports the missing prerequisite honestly (a prerequisite_missing fault
// unchanged, not a silent completed/degraded response and not hidden by a
// not_found as though the operation never existed).
func TestOperationAvailabilityDoesNotImplyRuntimeReadiness(t *testing.T) {
	const targetOwner = "identity"
	const targetOp = "principal.get"

	modules := catalogModules()
	replaced := false
	for i, m := range modules {
		if m.Name() != targetOwner {
			continue
		}
		owner, ok := fakeOwnerOf(m)
		if !ok {
			t.Fatalf("owner %s is not a plain fake owner", targetOwner)
		}
		modules[i] = newNotReadyOwner(targetOwner, owner.descriptors, "no configured hosted provider, memory backend or job runner")
		replaced = true
	}
	if !replaced {
		t.Fatalf("catalog modules do not include owner %s", targetOwner)
	}

	reg, err := New(modules)
	if err != nil {
		t.Fatalf("assembly with an owner reporting missing runtime prerequisites failed: %v", err)
	}

	// Available: the operation is registered, discoverable and carries its
	// real frozen mappings and schemas, exactly as any other operation.
	found := false
	for _, d := range reg.Public() {
		if d.ID == targetOp {
			found = true
			if len(d.CLI) == 0 || d.MCP == "" {
				t.Fatalf("operation %s lost its CLI/MCP mappings when its provider is unready", targetOp)
			}
		}
	}
	if !found {
		t.Fatalf("operation %s is not listed as available despite being structurally registered", targetOp)
	}
	if _, _, err := reg.Lookup(targetOp, 1); err != nil {
		t.Fatalf("Lookup refused an available-but-not-ready operation: %v", err)
	}
	_, items := invokeCapabilitiesList(t, reg, &fakeUnit{})
	ids := make([]string, 0, len(items.Items))
	for _, item := range items.Items {
		ids = append(ids, item.ID)
	}
	if !slices.Contains(ids, targetOp) {
		t.Fatalf("capabilities.list omits %s although the registry still holds it", targetOp)
	}

	// Not ready: invoking it surfaces the honest prerequisite fault, passed
	// through the dispatch wrapper unchanged -- not swallowed into a
	// completed payload and not remapped to a different code.
	_, handler, err := reg.Lookup(targetOp, 1)
	if err != nil {
		t.Fatalf("lookup failed: %v", err)
	}
	input := validInstance(t, targetOp, mustCatalog(t).byID[targetOp].Input)
	_, err = handler(context.Background(), &fakeUnit{ro: true}, contract.Invocation{
		Operation: targetOp, Version: 1, Input: input,
	})
	if faultCodeOf(t, err) != contract.CodePrerequisiteMissing {
		t.Fatalf("operation %s did not report its missing runtime prerequisite honestly: %v", targetOp, err)
	}
}

// TestCapabilitiesSurfaceCarriesNoRuntimeReadinessClaim proves the public
// discovery DTO itself never asserts that a listed operation's provider or
// job runner is configured: the frozen Descriptor projection carries only
// static contract facts (schemas, mappings, effect, submission semantics),
// so a caller cannot mistake "capabilities.list returned this operation"
// for "this operation will succeed." Qualified execution guarantees are an
// execution/installation-owned runtime concern (Status.runtime_ready and
// the execution profile's advisory fields), never a field the registry's
// own capabilities surface fabricates.
func TestCapabilitiesSurfaceCarriesNoRuntimeReadinessClaim(t *testing.T) {
	reg := mustRegistry(t)
	_, items := invokeCapabilitiesList(t, reg, &fakeUnit{})
	if len(items.Items) == 0 {
		t.Fatal("capabilities.list returned no items")
	}
	data, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("items do not marshal: %v", err)
	}
	var raw struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("items do not decode: %v", err)
	}
	for _, item := range raw.Items {
		for _, claim := range []string{"ready", "configured", "qualified", "available_now"} {
			if _, present := item[claim]; present {
				t.Fatalf("capabilities item fabricates a runtime-readiness field %q the frozen Descriptor does not declare", claim)
			}
		}
	}
}
