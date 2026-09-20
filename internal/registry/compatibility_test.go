package registry

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// versionedPair builds two descriptors of one operation ID: v1 with its
// original input schema and v2 with a schema that genuinely changed (a new
// required field), the way a real revision bump changes an operation's
// wire shape under P00's frozen-per-version discipline.
func versionedPair(id string) (v1, v2 contract.Descriptor) {
	v1 = contract.Descriptor{
		ID: id, Version: 1, Owner: "tester",
		Visibility: contract.VisibilityInternal, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
		InputSchema:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"x":{"type":"string"}},"required":["x"]}`),
		OutputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"y":{"type":"string"}}}`),
	}
	v2 = cloneDescriptor(&v1)
	v2.Version = 2
	v2.InputSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"x":{"type":"string"},"z":{"type":"string"}},"required":["x","z"]}`)
	return v1, v2
}

// TestVersionBumpKeepsOldSchemaExplicitWhenBothAreRegistered proves that
// when an owner deliberately keeps an old version registered alongside its
// replacement (the compatible path), the registry serves each version's
// own schema rather than silently reinterpreting an old request under the
// new shape: version 0 (current) resolves to the highest version, the old
// version is still reachable by its exact number, and the two schemas are
// genuinely distinct -- a v2-shaped input is rejected by the v1 handler.
func TestVersionBumpKeepsOldSchemaExplicitWhenBothAreRegistered(t *testing.T) {
	v1, v2 := versionedPair("tester.versioned_compat")
	m := newFakeOwner("tester", []contract.Descriptor{v1, v2})
	reg, err := New(append(catalogModules(), m))
	if err != nil {
		t.Fatalf("assembly with two explicit versions of one operation failed: %v", err)
	}

	current, _, err := reg.Lookup(v1.ID, 0)
	if err != nil || current.Version != 2 {
		t.Fatalf("version 0 resolved to %+v (err %v), want the current version 2", current, err)
	}

	old, oldHandler, err := reg.Lookup(v1.ID, 1)
	if err != nil || old.Version != 1 {
		t.Fatalf("the explicitly kept old version did not resolve: %+v, err %v", old, err)
	}
	if err := sameSchema(old.InputSchema, current.InputSchema, "input"); err == nil {
		t.Fatal("old and current versions report the identical schema; the bump changed nothing observable")
	}

	if _, err := oldHandler(context.Background(), &fakeUnit{}, contract.Invocation{
		Operation: v1.ID, Version: 1, Input: json.RawMessage(`{"x":"ok"}`),
	}); err != nil {
		t.Fatalf("v1 handler rejected its own valid input: %v", err)
	}
	// A v2-shaped input carries the field v1 never declared, so the still
	// bare v1 schema (additionalProperties:false) must refuse it: the two
	// versions are not silently aliased onto one accepted shape.
	if _, err := oldHandler(context.Background(), &fakeUnit{}, contract.Invocation{
		Operation: v1.ID, Version: 1, Input: json.RawMessage(`{"x":"ok","z":"unexpected"}`),
	}); faultCodeOf(t, err) != contract.CodeInvalidInput {
		t.Fatal("v1 handler accepted a v2-shaped input; the version compatibility boundary is not explicit")
	}
}

// TestVersionBumpWithoutTheOldVersionInvalidatesStaleClients proves the
// other half of the card's required behavior: when an owner retires the
// old version at a bump (registers only the new one, the incompatible
// path), a stale client still pinned to the old version number is refused
// as not_found -- an honest "unsupported", never silently served the new
// schema's shape or a fabricated success.
func TestVersionBumpWithoutTheOldVersionInvalidatesStaleClients(t *testing.T) {
	_, v2 := versionedPair("tester.versioned_retired")
	m := newFakeOwner("tester", []contract.Descriptor{v2})
	reg, err := New(append(catalogModules(), m))
	if err != nil {
		t.Fatalf("assembly failed: %v", err)
	}

	current, _, err := reg.Lookup(v2.ID, 0)
	if err != nil || current.Version != 2 {
		t.Fatalf("current version resolved to %+v, err %v; want v2", current, err)
	}
	if _, _, err := reg.Lookup(v2.ID, 1); faultCodeOf(t, err) != contract.CodeNotFound {
		t.Fatal("a retired old version must be refused as not_found, not silently upgraded")
	}
	// The current version's own handler independently refuses an
	// invocation stamped with the retired version number: version
	// mismatch is checked inside the dispatch wrapper too, not only by
	// Lookup's map probe.
	_, currentHandler, err := reg.Lookup(v2.ID, 0)
	if err != nil {
		t.Fatalf("lookup of the current version failed: %v", err)
	}
	if _, err := currentHandler(context.Background(), &fakeUnit{}, contract.Invocation{
		Operation: v2.ID, Version: 1, Input: json.RawMessage(`{"x":"ok","z":"ok"}`),
	}); faultCodeOf(t, err) != contract.CodeNotFound {
		t.Fatal("the current handler accepted an invocation stamped with the retired version")
	}
}
