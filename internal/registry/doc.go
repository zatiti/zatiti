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
// Lookup returns the descriptor and handler for one operation/version. The
// returned handler validates the invocation against the operation's merged
// input schema (shared $defs plus the operation schema) before executing,
// rejects mutations on read-only units and routes registered local-IO
// operations through the owning module's LocalIO Prepare/Perform/Finish
// seam. Bind binds a typed Go function to a descriptor: strict decode,
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
