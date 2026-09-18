// Package registry is Zatiti's operation registry: one typed surface that
// owns operation registration, collision checks, discovery, schema/OpenAPI
// generation and parity enumeration.
//
// New assembles the registry from domain modules and freezes the public
// surface against the embedded operation catalog: every public descriptor
// must match the catalog exactly (owner, mode, effect, schemas, CLI tokens,
// MCP name, submission-key, expected-version and scope semantics), every
// catalog operation must be registered exactly once, and CLI/MCP name
// collisions are rejected. Internal descriptors are validated structurally
// and never appear in Public, OpenAPI, CLI, MCP or accepted public routing.
//
// Lookup returns the descriptor and handler for one operation/version;
// version 0 resolves the current (highest registered) version. Modules
// deliver schemas bare or self-contained, and Lookup returns self-contained
// documents a caller can evaluate as delivered. The returned handler
// validates the invocation against that input document before executing and
// rejects mutations on read-only units. Registered local-IO operations have
// no single-unit handler: LocalIOFor hands the dispatcher the owning
// module's own LocalIO so it runs Prepare, Perform outside transactions and
// Finish itself. Internal caller allowlists name calling owners. Bind binds a typed Go function to a descriptor: strict decode,
// schema validation before execution, output marshaled and validated
// against the output schema.
//
// The registry also implements contract.Module for its own capabilities
// operations (capabilities.list, capabilities.schema), sourced from the
// embedded catalog without self-registration. OpenAPI emits the
// deterministic OpenAPI 3.1 document for the public surface: local
// component schemas, the common request and result envelopes, per-operation
// request bodies and explicit accepted references.
//
// A Registry is immutable after New returns; Lookup, Public and OpenAPI are
// safe for concurrent use.
package registry
