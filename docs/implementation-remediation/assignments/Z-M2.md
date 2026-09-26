# Z-M2 — Generic MCP client connection adapter (`internal/adapters/mcpclient`, adapter name `mcp`)

Status: integrated revision 4 candidate; independent review and required CI pending. Priority: P1. Owner: `internal/adapters/mcpclient` (new root) + `internal/connections` (three new operations) + `cmd/zatiti` (constructor registration only) + `tests/qualification` (controlled-server qualification).

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
  at most three distinct primary HTTP requests, all in `evidence.handshake`; a declared
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
  server-to-client request answered method-not-found only within the explicit admitted control-reply allowance; exhaustion aborts the session; resource links are
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

## Postiz endpoint shape and tonight's acceptance (verified live 2026-09-23 with the real key; chief-developer's dispatch call 23:4x PT)

On our deployment the MCP endpoint lives under the backend path, not `/mcp`
(which hits the frontend). The adapter's shape is **`POST
https://post.sire.blog/api/mcp` with `Authorization: Bearer <key>`**,
verified with the real key by chief-architect and chief-operator
independently: `initialize` 200 (server "Postiz MCP" 1.0.0), `tools/list`
200 with **11 tools** (integrationList, groupList, integrationSchema,
triggerTool, integrationSchedulePostTool, generateVideoOptions,
videoFunctionTool, generateVideoTool, generateImageTool, uploadFromUrlTool,
ask_postiz; the design doc's "13" came from Postiz docs, discovery records
what is served). A placeholder Bearer returns 401 "Invalid API Key or OAuth
token"; no header returns 401 "Missing Authorization header". The
credential-in-path form `/api/mcp/<key>` also exists and is exactly what
this contract refuses as `capability_unsupported`: never the fixture, the
profile endpoint or a default. Routing works through Cloudflare with no
reverse-proxy change.

Transport facts, all representable in the frozen seam:
- Postiz echoes whatever `protocolVersion` the client offers. go-sdk
  `Connect`'s fallback `initialize` offers `2025-11-25`, so a profile with
  `protocol_version: 2025-11-25 or 2026-07-28` negotiates cleanly; evidence records the
  negotiated value.
- Replies are `application/json` even when `text/event-stream` is
  accepted; the SDK's streamable HTTP client handles both, the adapter
  must not require SSE.
- No `mcp-session-id` header: record `session_state: stateless` and send
  no session header on later calls.

**Tonight's acceptance (raised from the placeholder-401 path):** the
adapter proven against the real deployment with the real key read from the
installation's `SecretStore` via `Dispatch.CredentialRef` (operator seeds
it from hq `.env` `POSTIZ_API_KEY` / Secrets Manager
`sire/postiz/public-api-key`; the value is never in a test file, log,
fixture, PR body or evidence): one admitted `open_session` (handshake
exchanges recorded in order, negotiated `2025-11-25`, `session_state`
stateless) and one admitted `list_tools` returning the 11 tools with pinned
schema digests, evidence and staged request record byte-checked for the
absence of the key (`Z13.mcp_credential_confined` against a real secret).
Read-only discovery only: **no `call_tool` tonight**, nothing with an
effect (schedule, video, image, upload) runs before Z-M3's reviewed
end-to-end post. The placeholder-401 typed-refusal case stays as a unit
test, not as the acceptance.

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

Revision 4 integrated acceptance additionally requires the governed probe intent, exact recorded callback provenance, full composed schema/profile/session pins, claim-time catalog rejection, all physical exchanges and bounded separately costed refusal replies described in docs/implementation/contracts.md. Tests without callbacks cannot qualify the control-reply count.


## Integrated landing handoff (revision 4)

The serialized landing candidate combines the contract revision and implementation from PRs #67/#69; those PRs remain untouched while this candidate is reviewed. The delegated integration owner may amend authored specification inputs and affected owner seams, regenerate prompts, and validate the consolidated branch. This supersedes the child-package-only write limits above for this integration assignment alone.

Review the complete main-to-candidate diff, including the previous adapter safe fixes. Required review targets: exact durable probe intent and linked callback; full discovered-tool envelope and catalog/profile/session pins; claim-time freshness and policy checks; pre-send contexts and no-response handshake evidence; explicit bounded per-reply cost/evidence; verified publication mappings; missing-job deferral. Initial stdio and generic same-account credential rotation remain capability_unsupported. Synthetic tests do not qualify Postiz, live publishing, deployment, or a release. Only the parent landing owner may merge after independent review and required checks.
