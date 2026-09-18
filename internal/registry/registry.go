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

	// current maps an operation ID to its highest registered version: the
	// version Lookup resolves when the caller passes version 0.
	current map[string]int64

	// localIO maps each registered local IO operation to the owning module's
	// own contract.LocalIO implementation.
	localIO map[string]contract.LocalIO

	publicList []*entry // public operations, sorted by ID

	capabilityDescriptors []contract.Descriptor
}

// versionKey identifies one operation at one version.
type versionKey struct {
	id      string
	version int64
}

// trustedCallers are the caller identities that are not domain modules:
// application's own ports view and the local controller entering through
// application.Internal. Every other caller in an internal allowlist must be
// an assembled module name.
var trustedCallers = map[string]bool{
	"application": true,
	"controller":  true,
}

// entry is one registered operation: its descriptor with bare schema bodies
// (the published form), the self-contained schema documents used wherever a
// schema is evaluated, and its dispatch handler.
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
//   - internal descriptors never require a submission key, and their
//     caller allowlists name calling owners: assembled modules or the
//     trusted application/controller identities,
//   - schemas arrive bare or self-contained (see catalog.resolveSchema); a
//     delivered definition never redefines a shared one,
//   - registered local-IO operations are owned by modules implementing
//     contract.LocalIO; LocalIOFor hands that implementation to the
//     dispatcher, which sequences Prepare/Perform/Finish itself.
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
		current:    make(map[string]int64, len(cat.document.Operations)),
		localIO:    make(map[string]contract.LocalIO, len(localIOOperations)),
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
	// Internal caller allowlists name calling owners. A name that is neither
	// an assembled module nor a trusted caller could never be bound by the
	// port router, so it is a descriptor defect, not a dormant entry.
	for _, e := range r.entries {
		for _, caller := range e.descriptor.Callers {
			if !seen[caller] && !trustedCallers[caller] {
				return nil, fmt.Errorf("registry: operation %s allows unknown caller %q: not an assembled module, application or controller", e.descriptor.ID, caller)
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

	inputBody, mergedInput, err := r.catalog.resolveSchema(d.InputSchema)
	if err != nil {
		return fmt.Errorf("registry: operation %s: input schema: %w", d.ID, err)
	}
	outputBody, mergedOutput, err := r.catalog.resolveSchema(d.OutputSchema)
	if err != nil {
		return fmt.Errorf("registry: operation %s: output schema: %w", d.ID, err)
	}
	var completionBody, mergedCompletion json.RawMessage
	if len(d.CompletionSchema) != 0 {
		completionBody, mergedCompletion, err = r.catalog.resolveSchema(d.CompletionSchema)
		if err != nil {
			return fmt.Errorf("registry: operation %s: completion schema: %w", d.ID, err)
		}
	}
	// The registered descriptor carries the bare bodies: the form the frozen
	// catalog prints and every discovery surface publishes.
	registered := cloneDescriptor(d)
	registered.InputSchema = inputBody
	registered.OutputSchema = outputBody
	registered.CompletionSchema = completionBody
	d = &registered

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

	if err := checkSchemaDocument(mergedInput); err != nil {
		return fmt.Errorf("registry: operation %s: input schema: %w", d.ID, err)
	}
	if err := checkSchemaDocument(mergedOutput); err != nil {
		return fmt.Errorf("registry: operation %s: output schema: %w", d.ID, err)
	}
	if len(mergedCompletion) != 0 {
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
		e.handler = wrapLocalIO(e.descriptor, e.mergedInput)
		r.localIO[d.ID] = local
	} else {
		e.handler = wrapModule(e.descriptor, e.mergedInput, module)
	}
	r.entries[key] = e
	r.registered[d.ID] = true
	if d.Version > r.current[d.ID] {
		r.current[d.ID] = d.Version
	}
	return nil
}

// Lookup returns the descriptor and handler for one operation.
//
// Version 0 means the current version: the highest version registered for
// the ID. The returned descriptor reports the resolved version, and the
// handler executes only that version, so the dispatcher stamps
// Descriptor.Version on the invocation before calling it. Any other version
// is matched exactly. Unknown operations, unregistered versions and negative
// versions return a not_found fault.
//
// The descriptor's schemas are the self-contained documents (bare body plus
// the definitions it reaches), so a caller can evaluate them exactly as
// delivered; Public returns the bare published bodies. Internal operations
// are resolvable for internal callers through the same typed boundary;
// keeping them away from public routing is the dispatcher's decision.
func (r *Registry) Lookup(id string, version int64) (contract.Descriptor, contract.Handler, error) {
	if version == 0 {
		version = r.current[id]
	}
	e, ok := r.entries[versionKey{id, version}]
	if !ok || version < 1 {
		return contract.Descriptor{}, nil, fault(contract.CodeNotFound,
			"operation %s version %d is not registered", id, version)
	}
	d := cloneDescriptor(&e.descriptor)
	d.InputSchema = append(json.RawMessage(nil), e.mergedInput...)
	d.OutputSchema = append(json.RawMessage(nil), e.mergedOutput...)
	d.CompletionSchema = append(json.RawMessage(nil), e.mergedCompl...)
	return d, e.handler, nil
}

// LocalIOFor returns the owning module's own contract.LocalIO for a
// registered local IO operation. The dispatcher runs Prepare inside its
// admission transaction, Perform outside any transaction and Finish inside
// the completion transaction; the registry only routes. Every other
// operation, including the other operations of the same owners, reports
// false.
func (r *Registry) LocalIOFor(operation string) (contract.LocalIO, bool) {
	local, ok := r.localIO[operation]
	return local, ok
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

// wrapLocalIO builds the handler of a registered local-IO operation. A
// Handler runs inside one Unit, and Perform must never run inside a Unit, so
// no single handler can execute the Prepare/Perform/Finish seam: after the
// common checks it fails closed and names the route the dispatcher must
// take instead.
func wrapLocalIO(d contract.Descriptor, mergedInput json.RawMessage) contract.Handler {
	id := d.ID
	return wrapHandler(d, mergedInput, func(context.Context, contract.Unit, contract.Invocation) (contract.Payload, error) {
		return contract.Payload{}, fault(contract.CodeInternalError,
			"local IO operation %s executes through LocalIOFor as Prepare, Perform outside the unit, then Finish; it has no single-unit handler", id)
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
