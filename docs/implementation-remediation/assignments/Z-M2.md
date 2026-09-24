# Z-M2 — Generic MCP client connection adapter (`internal/adapters/mcpclient`, adapter name `mcp`)

Status: dispatchable (coordinated revision landed with this card). Priority: P1. Owner: `internal/adapters/mcpclient` (new root) + `internal/connections` (three new operations) + `cmd/zatiti` (constructor registration only) + `tests/qualification` (controlled-server qualification).

This is a coding assignment, not evidence that its behavior exists. Founder
ruling dec-1256 (hq `designs/2026-09-23-marketer-org-on-zatiti.md`, row
Z-M2) fixed the WHAT; the coordinated revision in this PR fixed the HOW at
the contract level. Read, in this order, before editing:
`docs/implementation/contracts.md` section "MCP client connection adapter
(coordinated revision 2026-09-24)", `internal/adapters/mcpclient/AGENTS.md`
(generated; embeds the frozen `MCPClientProfile`/`MCPClientParameters`/
`MCPClientEvidence` schemas), `internal/adapters/httpread/` and
`internal/adapters/github/` (the two adapters whose shape this one copies:
`faults.go`, `redact.go`, `request.go`/`wire.go`, `resolveCredential`),
`internal/connections/AGENTS.md` (the new `connection.discover`,
`connection.tools`, `_connections.discovery.record` sections and the
existing `_connections.validation.record` it mirrors), and
`docs/implementation/dependencies.lock.json` (go-sdk v1.7.0 is already
pinned; do not bump it).

Allowed writes: `internal/adapters/mcpclient` (new package, not its
generated AGENTS.md), `internal/connections` (the three operations, a
connections-private `connections_mcp_tools` table and migration, the
`mcp` provider row in `providerValidationTool`/`buildValidationAction`, a
built-in tool contract for the adapter action), `cmd/zatiti/adapters.go`
(register `mcpclient.New` under `"mcp"`, nothing else), `tests/qualification`
(controlled MCP server; Postiz qualification is Z-M3's). No edits to
`tools/specgen`, `docs/implementation/*.json`, `contracts.md` or any
AGENTS.md: a contract defect is reported with an affected-dependency list,
not patched locally.

## Dependencies, decided

- **Not dependent on Z-M1.2** (the `connection.setup.complete` receipt-key
  gap, `cmd/zatiti/helper.go` KNOWN GAP): this card proves the adapter
  against a local fake `contract.SecretStore` and a controlled server. The
  real-credential Postiz path is Z-M3's, which depends on Z-M1.2 or on a
  verified `connection.create` carrying the opaque reference
  `SecretStore.Put` actually returns.
- **Not dependent on Z-M1.1** (Unix-socket-only CLI/MCP surface): this
  adapter is an outbound client from the controller to the server's
  `https` endpoint, not a listener.
- go-sdk v1.7.0 is pinned and qualified for protocol `2025-11-25`
  (`dependencies.lock.json`, direct_modules). Use the `mcp` subpackage's
  client (`NewClient`, streamable HTTP transport); disable SDK reconnect
  and retry; do not add a second MCP library.

## Design (frozen in contracts.md; summarized here for the lane)

- **Root and names.** Package `mcpclient`; `Name()` returns `mcp`;
  `Connection.provider` is `mcp`. `internal/mcp` (the stdio server) is
  unrelated and is not imported.
- **Profile** `zatiti.mcp/v1`: `transport` oneOf `streamable_http` (one
  `https` `endpoint`, `allow_private_endpoint`, `max_redirects` const 0) or
  `stdio` (frozen shape; `New` refuses it `capability_unsupported` in this
  card). `protocol_version` const `2025-11-25`. `credential_kind`
  `bearer|none`; credential-in-path and OAuth are `capability_unsupported`
  at construction. `allowed_tools`, `tool_call_cost`, byte/time bounds,
  `classifications`, `capability_evidence` (digest check with the field
  omitted, as the other adapters do).
- **Actions** (`kind`-discriminated): `open_session` (initialize handshake:
  exactly two HTTP requests, both in `evidence.handshake`; the one declared
  exception to one request per attempt), `list_tools` (one `tools/list`
  page; never follow `next_cursor`), `call_tool` (one `tools/call` after
  allowlist + pinned-schema validation of arguments + classification
  check), `close_session` (one DELETE). Sessions live in a bounded
  in-memory table keyed by an opaque handle; the raw `Mcp-Session-Id` never
  leaves the adapter; an unknown handle is `prerequisite_missing`/not_sent.
- **Credential**: `Dispatch.CredentialRef` → `deps.Secrets.Get` outside any
  Unit, bytes only in the `Authorization` header, excluded from the staged
  request record, evidence, faults and logs (copy httpread/github's
  `redact.go` discipline). `account_identity` is `credential:<ref>`.
- **Evidence** `zatiti.mcp.evidence/v1`: `physical_call` with a staged
  request context (purpose `context`) for every disposition including
  not_sent/unknown; tool result staged once as purpose `tool_result` under
  the action's classification; `refused_server_requests` lists every
  server-to-client request answered method-not-found; resource links are
  opaque references, never fetched.
- **Unknowns**: timeout after bytes sent, lost response, or a body above
  `max_response_bytes` (`error_code: response_oversize`) → `unknown`,
  reservation retained. `Reconcile` → `capability_unsupported` for every
  kind. No SDK retry, no reconnect, no redirect, no GET listening stream,
  no resources/prompts/tasks calls.
- **connections**: `connection.discover` admits a `list_tools` read effect
  (callback route kind `connection`, exactly as `connection.validate`
  does); `_connections.discovery.record` writes the observed catalog
  (name, pinned `input_schema`, digest, optional `output_schema`,
  annotations as hints) to a connections-private table and marks tools
  missing from a complete catalog stale; `connection.tools` pages that
  table; `_connections.resolve` composes the returned `Tool` for an mcp
  binding from the recorded catalog so the effect owner and the adapter
  see the same pinned schema and digest. `connection.validate` for
  provider `mcp` is `open_session`. Effect classification of an mcp tool
  binding defaults to `external_mutation`; annotations change nothing.

## Implement in this order

1. `internal/adapters/mcpclient`: `profile.go` (strict decode, capability
   digest, stdio/credential-kind refusals), `faults.go`, `redact.go`,
   `session.go` (bounded handle table), `action.go` (strict `kind`
   decode; argument validation against the pinned `input_schema` through
   `contract.ValidateSchema`), `request.go`/`wire.go` (staged request
   record before any bytes; one request per attempt; handshake recorded),
   `evidence.go`, `adapter.go` (`New`, `Invoke`, `Reconcile`). Tests first,
   against a controlled in-process streamable-HTTP server that counts
   requests and can stall, oversize, redirect, issue server-to-client
   requests and drop the session.
2. `internal/connections`: migration + table, the three operations, the
   `mcp` provider validation action, the built-in tool contract, and the
   `resolve` composition. Tests: discovery installs no authority; resolve
   returns the pinned schema; stale marking.
3. `cmd/zatiti/adapters.go`: one map entry. Nothing else in `cmd`.
4. `tests/qualification`: the five new named cases
   (`Z05.mcp_discovery_grants_nothing`, `Z06.mcp_one_request_per_call`,
   `Z08.mcp_lost_tool_call_response`, `Z13.mcp_credential_confined`,
   `Z05.mcp_server_requests_refused`) against the controlled server, plus
   the existing `Z05.callback_discovery`/`Z06.no_hidden_retries` cases now
   naming this root.

## Required behavioral tests (non-vacuity, this repo's L-0026 shape)

- Exact request counts per kind from the controlled server's own log, not
  from adapter-internal counters: 2 for `open_session`, 1 for each other
  kind, 0 for every refusal.
- A `call_tool` whose arguments pass a looser schema but fail the pinned
  one is refused `invalid_input` with zero requests; the digest in the
  action must match the digest of the schema actually validated against.
- Credential bytes: assert absence in the staged request record bytes, in
  every evidence field, in every fault message and in the test log, and
  presence only in the controlled server's received `Authorization` header.
- Stall after the server applied its effect → `unknown`, `request_sent:
  yes`, staged request context retained, a second `Invoke` never issued.
- Server-initiated sampling/elicitation/ping during a `tools/call` →
  method-not-found on the wire, names in `refused_server_requests`, tool
  result still recorded honestly.

Package check: `go test ./internal/adapters/mcpclient/... ./internal/connections/...`
then `./cmd/zatiti/... ./tests/qualification/...`. Before any
multi-package Go build/test/lint, check `uptime` and claim `R-build-lease`
via `~/.agents/skills/claim/scripts/claim.sh` (hold if the 1-minute load
exceeds 10); release immediately after. No race suite in parallel with
another lane.

Handoff: adapter, connections operations and registration land with the
tests above green, `python3 tools/specgen/render.py --check` still clean
(this card changes no authoritative input), and a PR body listing files
changed, exact commands run with observed results, the controlled-server
request counts, and any contract defect found with its affected-dependency
list. Postiz against `post.sire.blog/mcp` is Z-M3, not this card. No
publishing or deployment is authorized by this card.
