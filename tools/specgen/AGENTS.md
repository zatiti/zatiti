# Specification maintenance

This directory owns the deterministic, standard-library Python specification renderer. This is a maintenance scope, not a Go implementation package and not a concurrent child of a root integration write assignment.

`model.py` authors the frozen operation schemas and internal caller allowlists. `packages.py` authors disjoint ownership roots and package briefs. `render.py` reads those plus authored `docs/implementation/contracts.md`, `requirements.json`, `acceptance.json` and `adapter-schemas.json`. It emits committed package AGENTS.md files and the operation/package/coverage indexes. The renderer must not read the RFC, access the network, invoke a model or depend on an execution harness.

For an explicitly assigned specification revision, coordinate edits to the authored docs inputs and regenerate affected prompts together. Product implementation agents do not revise their own acceptance criteria. Preserve exact shared Go signatures, schema references, current-authority checks, transaction boundaries and unknown-outcome semantics. A new operation needs owner, exact input/output schema, mode/effect, public CLI/MCP mapping or internal caller allowlist, behavior and acceptance ownership.

Validate with `python3 tools/specgen/render.py --check`. Root/import/caller/reference/coverage checks must work with standard Python. JSON Schema 2020-12 validation may additionally use an already-installed jsonschema library, with that distinction reported. Tests must verify meaningful drift, missing references/owners, collisions, cycles and RFC-independent rendering; do not claim structural validation proves product behavior.

Do not create product code here or change unrelated user files. Generated output must be deterministic, use repository-relative context and retain full local prompt requirements without external RFC references standing in for behavior.
