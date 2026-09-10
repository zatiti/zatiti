package registry

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Registry is the typed operation registry. It is assembled once by New and
// immutable afterwards: Lookup, Public, OpenAPI and the Module surface are
// safe for concurrent use without additional synchronization.
type Registry struct {
	catalog *catalog

	entries    map[versionKey]*entry
	registered map[string]bool // operation IDs with at least one version

	publicList []*entry // public operations, sorted by ID

	capabilityDescriptors []contract.Descriptor
}

// versionKey identifies one operation at one version.
type versionKey struct {
	id      string
	version int64
}

// entry is one registered operation: its descriptor, the merged schemas
// used for invocation-time validation and its dispatch handler.
type entry struct {
	descriptor   contract.Descriptor
	mergedInput  json.RawMessage
	mergedOutput json.RawMessage
	mergedCompl  json.RawMessage
	handler      contract.Handler
	owner        string
	self         bool // registry-owned capabilities operation
}

// New assembles the registry from domain modules and validates the complete
// surface against the embedded frozen catalog:
//
//   - every descriptor carries a well-formed ID, known visibility, mode and
//     effect values, a version >= 1 and an owner equal to its module name,
//   - (operation, version) pairs are unique across all modules,
//   - every public descriptor matches the catalog exactly: owner, mode,
//     effect, input/output/completion schemas, CLI tokens, MCP name,
//     submission-key, expected-version and scope semantics; the CLI/MCP
//     names must equal the names derived from the operation ID, and
//     derived names must not collide across operations,
//   - every catalog operation is registered by exactly one module,
//   - internal descriptors declare no CLI/MCP mappings (they never appear
//     in the generated surface) and never shadow a catalog operation,
//   - internal caller allowlists reference registered operations,
//   - registered local-IO operations are owned by modules implementing
//     contract.LocalIO so they route through Prepare/Perform/Finish.
//
// The registry's own capabilities operations are registered from the
// catalog without self-registration; Registry implements contract.Module
// for them. Assembly fails closed: any violation returns an error and no
// usable Registry is produced.
func New(modules []contract.Module) (*Registry, error) {
	cat, err := loadCatalog()
	if err != nil {
		return nil, fmt.Errorf("registry: embedded catalog: %w", err)
	}
	r := &Registry{
		catalog:    cat,
		entries:    make(map[versionKey]*entry, len(cat.document.Operations)),
		registered: make(map[string]bool, len(cat.document.Operations)),
	}

	// Registry-owned capabilities first so module names cannot shadow them.
	if err := r.registerCapabilities(); err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	for _, module := range modules {
		if module == nil {
			return nil, fmt.Errorf("registry: nil module")
		}
		name := module.Name()
		if !validOwnerName(name) {
			return nil, fmt.Errorf("registry: module %q is not a valid owner name", name)
		}
		if seen[name] {
			return nil, fmt.Errorf("registry: module %q is assembled more than once", name)
		}
		seen[name] = true
		local, _ := module.(contract.LocalIO)
		descriptors := module.Descriptors()
		for i := range descriptors {
			if err := r.register(&descriptors[i], name, module, local); err != nil {
				return nil, err
			}
		}
	}

	// Completeness: the frozen catalog is the whole public surface.
	for i := range cat.document.Operations {
		op := &cat.document.Operations[i]
		if _, ok := r.entries[versionKey{op.ID, op.Version}]; !ok {
			return nil, fmt.Errorf("registry: operation %s v%d is not registered by any module", op.ID, op.Version)
		}
	}
	// Internal caller allowlists may only name registered operations.
	for _, e := range r.entries {
		for _, caller := range e.descriptor.Callers {
			if !r.registered[caller] {
				return nil, fmt.Errorf("registry: operation %s allows unknown caller %q", e.descriptor.ID, caller)
			}
		}
	}

	// Public enumeration in deterministic catalog order.
	for i := range cat.document.Operations {
		op := &cat.document.Operations[i]
		e, ok := r.entries[versionKey{op.ID, op.Version}]
		if !ok || e.descriptor.Visibility != contract.VisibilityPublic {
			continue
		}
		r.publicList = append(r.publicList, e)
	}
	return r, nil
}

// register validates one descriptor against the frozen surface and installs
// its dispatch entry.
func (r *Registry) register(d *contract.Descriptor, ownerName string, module contract.Module, local contract.LocalIO) error {
	if err := validateDescriptorShape(d); err != nil {
		return fmt.Errorf("registry: module %s: operation %q: %w", ownerName, d.ID, err)
	}
	if d.Owner != ownerName {
		return fmt.Errorf("registry: module %s: operation %q declares owner %q", ownerName, d.ID, d.Owner)
	}
	key := versionKey{d.ID, d.Version}
	if _, dup := r.entries[key]; dup {
		return fmt.Errorf("registry: operation %s v%d is registered more than once", d.ID, d.Version)
	}

	frozen, inCatalog := r.catalog.byID[d.ID]
	switch d.Visibility {
	case contract.VisibilityPublic:
		if !inCatalog {
			return fmt.Errorf("registry: public operation %q is not in the frozen catalog", d.ID)
		}
		if err := matchCatalog(d, frozen); err != nil {
			return fmt.Errorf("registry: operation %s: %w", d.ID, err)
		}
	case contract.VisibilityInternal:
		if inCatalog {
			return fmt.Errorf("registry: operation %s is a frozen public operation and cannot be registered as internal", d.ID)
		}
		if len(d.CLI) != 0 || d.MCP != "" {
			return fmt.Errorf("registry: internal operation %s must not declare CLI or MCP mappings", d.ID)
		}
	default:
		return fmt.Errorf("registry: operation %q has unsupported visibility %q", d.ID, d.Visibility)
	}

	mergedInput, err := mergedSchema(r.catalog.defsJSON, d.InputSchema)
	if err != nil {
		return fmt.Errorf("registry: operation %s: input schema: %w", d.ID, err)
	}
	if err := checkSchemaDocument(mergedInput); err != nil {
		return fmt.Errorf("registry: operation %s: input schema: %w", d.ID, err)
	}
	mergedOutput, err := mergedSchema(r.catalog.defsJSON, d.OutputSchema)
	if err != nil {
		return fmt.Errorf("registry: operation %s: output schema: %w", d.ID, err)
	}
	if err := checkSchemaDocument(mergedOutput); err != nil {
		return fmt.Errorf("registry: operation %s: output schema: %w", d.ID, err)
	}
	var mergedCompletion json.RawMessage
	if len(d.CompletionSchema) != 0 {
		mergedCompletion, err = mergedSchema(r.catalog.defsJSON, d.CompletionSchema)
		if err != nil {
			return fmt.Errorf("registry: operation %s: completion schema: %w", d.ID, err)
		}
		if err := checkSchemaDocument(mergedCompletion); err != nil {
			return fmt.Errorf("registry: operation %s: completion schema: %w", d.ID, err)
		}
	}

	if localIOOperations[d.ID] {
		if d.Visibility != contract.VisibilityPublic {
			return fmt.Errorf("registry: local IO operation %s must be public", d.ID)
		}
		if local == nil {
			return fmt.Errorf("registry: local IO operation %s requires its owner %s to implement contract.LocalIO", d.ID, ownerName)
		}
	}

	e := &entry{
		descriptor:   cloneDescriptor(d),
		mergedInput:  mergedInput,
		mergedOutput: mergedOutput,
		mergedCompl:  mergedCompletion,
		owner:        ownerName,
	}
	if localIOOperations[d.ID] {
		e.handler = wrapLocalIO(e.descriptor, e.mergedInput, local)
	} else {
		e.handler = wrapModule(e.descriptor, e.mergedInput, module)
	}
	r.entries[key] = e
	r.registered[d.ID] = true
	return nil
}

// Lookup returns the descriptor and handler for one operation at one exact
// version. Unknown operations, unknown versions and internal operations
// reached with a version that is not registered return a not_found fault;
// internal operations are resolvable for internal callers through the same
// typed boundary and are never accepted from public routing.
func (r *Registry) Lookup(id string, version int64) (contract.Descriptor, contract.Handler, error) {
	e, ok := r.entries[versionKey{id, version}]
	if !ok {
		return contract.Descriptor{}, nil, fault(contract.CodeNotFound,
			"operation %s version %d is not registered", id, version)
	}
	return cloneDescriptor(&e.descriptor), e.handler, nil
}

// Public returns the public descriptors sorted by ID. Internal operations
// never appear. The returned descriptors are copies; mutating them does not
// affect the registry.
func (r *Registry) Public() []contract.Descriptor {
	out := make([]contract.Descriptor, 0, len(r.publicList))
	for _, e := range r.publicList {
		out = append(out, cloneDescriptor(&e.descriptor))
	}
	return out
}

// Name implements contract.Module.
func (r *Registry) Name() string { return "registry" }

// Migrations implements contract.Module. The registry owns no tables; the
// capability surface is derived from the typed registry itself.
func (r *Registry) Migrations() []contract.Migration { return nil }

// Descriptors implements contract.Module with the registry-owned
// capabilities operations.
func (r *Registry) Descriptors() []contract.Descriptor {
	out := make([]contract.Descriptor, len(r.capabilityDescriptors))
	for i := range r.capabilityDescriptors {
		out[i] = cloneDescriptor(&r.capabilityDescriptors[i])
	}
	return out
}

// Handle implements contract.Module for the registry-owned capabilities
// operations. It rejects invocations for operations the registry does not
// own; the application dispatcher routes every other operation to its
// owning module.
func (r *Registry) Handle(ctx context.Context, unit contract.Unit, invocation contract.Invocation) (contract.Payload, error) {
	e, ok := r.entries[versionKey{invocation.Operation, invocation.Version}]
	if !ok || e.owner != r.Name() {
		return contract.Payload{}, fault(contract.CodeNotFound,
			"operation %s version %d is not owned by the registry", invocation.Operation, invocation.Version)
	}
	return e.handler(ctx, unit, invocation)
}

// wrapModule builds the dispatch wrapper for a module-owned operation: it
// verifies the invocation targets this operation/version, rejects mutations
// on read-only units, validates the input against the merged schema before
// execution and only then calls the owning module.
func wrapModule(d contract.Descriptor, mergedInput json.RawMessage, module contract.Module) contract.Handler {
	return wrapHandler(d, mergedInput, module.Handle)
}

// wrapLocalIO builds the dispatch wrapper for a registered local-IO
// operation: after the common checks, the invocation routes through the
// owning module's LocalIO seam — Prepare inside the transaction, Perform
// outside it, Finish back inside to commit result and evidence.
func wrapLocalIO(d contract.Descriptor, mergedInput json.RawMessage, io contract.LocalIO) contract.Handler {
	return wrapHandler(d, mergedInput, func(ctx context.Context, unit contract.Unit, invocation contract.Invocation) (contract.Payload, error) {
		plan, err := io.Prepare(ctx, unit, invocation)
		if err != nil {
			return contract.Payload{}, err
		}
		result, err := io.Perform(ctx, plan)
		if err != nil {
			return contract.Payload{}, err
		}
		return io.Finish(ctx, unit, plan, result)
	})
}

// wrapHandler is the common dispatch wrapper shared by module-routed,
// LocalIO-routed and registry-owned capabilities operations.
func wrapHandler(d contract.Descriptor, mergedInput json.RawMessage, fn contract.Handler) contract.Handler {
	id := d.ID
	version := d.Version
	mutation := d.Mode == contract.ModeMutation
	return func(ctx context.Context, unit contract.Unit, invocation contract.Invocation) (contract.Payload, error) {
		if unit == nil {
			return contract.Payload{}, fault(contract.CodeInternalError, "operation %s requires a transaction unit", id)
		}
		if invocation.Operation != id {
			return contract.Payload{}, fault(contract.CodeInternalError,
				"handler for operation %s received invocation for %q", id, invocation.Operation)
		}
		if invocation.Version != version {
			return contract.Payload{}, fault(contract.CodeNotFound,
				"operation %s version %d is not registered", id, invocation.Version)
		}
		if mutation && unit.ReadOnly() {
			return contract.Payload{}, fault(contract.CodeInvalidInput,
				"operation %s is a mutation and cannot execute on a read-only unit", id)
		}
		if err := contract.ValidateSchema(mergedInput, invocation.Input); err != nil {
			return contract.Payload{}, fault(contract.CodeInvalidInput,
				"operation %s input rejected: %v", id, err)
		}
		return fn(ctx, unit, invocation)
	}
}

// fault builds a contract.Fault error with a formatted message.
func fault(code, format string, args ...any) *contract.Fault {
	return &contract.Fault{Code: code, Message: fmt.Sprintf(format, args...)}
}
