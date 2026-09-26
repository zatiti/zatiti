# Serenity public protocol: inspected pin

This document records what Serenity's public interface actually is at one
inspected commit, and how it compares with what this package's `AGENTS.md`
requires. It is the evidence base for every capability this adapter reports.
Nothing in this package may claim an upstream behavior this document does not
record.

All file references are relative to the Serenity repository root. The
inspection was read-only: nothing was built, run, or written in that
repository, and no live Serenity process was contacted. Every statement below
comes from source, documentation, schemas, and fixtures at the pinned commit.
That is source inspection, not qualification of a running service.

## Pin

| Item | Value |
|---|---|
| Module | `github.com/sirerun/serenity` |
| Remote | `github.com/sirerun/serenity` |
| Commit | `b4febdf7bbc0d3c33f9939c79099dc64cce89e84` |
| Commit date | 2026-09-24 |
| Checkout | Public `main` HEAD supplied for inspection; local shallow checkout contains no `f5a5154` object |
| `git describe` | `v0.1.10-hosted-candidate` (candidate tag at this commit) |
| Go directive | `go 1.26.5` |
| License | Apache License 2.0 (`LICENSE`) |
| Memory protocol | MEMORY_VERBS v1, domain `protocol_version` integer `1` |
| Transport protocol | MCP `2025-11-25` (`internal/server/mcp/tools.go:19`) |
| Upstream protocol origin | gbrain `upstream-verbs.ts`, commit `d35c9c9e441e6cfc86dd5e84b0b168c6b18ee775` |

Pin caveats:

- **A candidate tag is not qualification.** This commit has the tag
  `v0.1.10-hosted-candidate`; its existence does not prove a running binary,
  signed release, or Zatiti's required behavior. The older `f5a5154` object
  is absent from this shallow checkout, so the prior inspected evidence in
  this document, rather than a local Git diff, is the comparison baseline.
- **A running build cannot prove its commit.** `internal/cli/root.go:11`
  declares `var Version = "dev"`; this checkout's `Makefile` does not inject a
  source commit into it. MCP `initialize` returns the value as
  `serverInfo.version`
  (`internal/server/mcp/server.go:152`). No public call reports the source
  commit, so an adapter cannot verify at run time that it talks to this pin.
- **The frozen Zatiti contract has not changed.** New upstream extension
  tools are source-backed observations, not proof that their responses fit
  Zatiti's UUID claim/version/freshness schemas or that an operation can be
  dispatched through this adapter.

## What Serenity serves

### Transports

`serenity serve` requires exactly one of two flags
(`internal/cli/serve.go:28-50`):

- `--stdio`: newline-delimited JSON-RPC MCP over the process's standard
  streams. No authentication; the spawning process is the trust boundary.
- `--http`: MCP Streamable HTTP at the single route `/mcp`
  (`internal/cli/serve.go:163`), loopback by default, with optional LAN
  bind and mutual TLS (`docs/operator/server.md`).

Both transports serve the five MEMORY_VERBS v1 tools `recall`, `remember`,
`entity`, `synthesize`, `forget` plus the additive
`cancel_memory_operation` and `read_memory_fact` tools
(`internal/cli/serve.go:282-283`, `internal/server/memory/cancel.go:24-32`).
The extensions must be discovered; they are not part of the five-verb v1
baseline (`docs/protocol/MEMORY_VERBS_v1.md:277-352`).

`--http` also registers DISPOSITION and DIRECTION routes when a brain and
index are available (`internal/cli/serve.go:164-168`). Those are distinct
protocols, not Zatiti's MEMORY_VERBS action surface.

### HTTP transport rules

Source: `internal/server/mcp/http.go`, `internal/server/server.go:105-130`,
`docs/operator/mcp.md`.

- Every route requires `Authorization: Bearer <token>`, compared in constant
  time; a missing or wrong token gets `401`. The legacy token is minted by
  `serenity init` into the host OS keychain. A named credential profile can
  instead be provisioned with
  `serenity connect --credential-profile NAME --provision-token`, and
  `serve --http --credential-profile NAME` uses that
  separate keychain slot (`internal/cli/connect.go:68-110`,
  `internal/cli/serve.go:145-151`). The profile name is operator-selected,
  not a stored per-brain binding; no public command exports token bytes into
  a Zatiti `credential_ref`.
- Only `POST` and `DELETE` are implemented; `GET` gets `405`. A request with an
  `Origin` header gets `403`. `Content-Type` must be `application/json`, else
  `415`. The body limit is 1 MiB (`internal/server/mcp/server.go:15`), else
  `413`.
- Responses are always one JSON document. The transport never upgrades to an
  event stream and has no resumption.
- **A tool call needs a three-request session handshake.** A `POST` without
  `Mcp-Session-Id` must be `initialize`, else `400`
  (`internal/server/mcp/http.go:230-233,335-379`). The response carries a fresh
  `Mcp-Session-Id`. The client must then `POST` a
  `notifications/initialized` message on that session (`202`), because
  `tools/call` is refused with JSON-RPC `-32600 "Initialization required"`
  until the session reaches state 2 (`internal/server/mcp/server.go:76-86,185-189`). Only the third request can reach a
  tool. The stdio transport runs the same state machine.
- Sessions live in the server process's memory. The server evicts a session
  after 30 idle minutes, holds at most 64, and loses all of them on restart
  (`internal/server/mcp/http.go:46-57`). An unknown session gets `404`.
- **A dropped connection does not stop a call.** An accepted `tools/call` runs
  under the handler's lifetime context, not the request's. When the client
  disconnects, the call keeps running and its result is undeliverable
  (`internal/server/mcp/http.go:77-87,270-272,320-328`).
  A client-side timeout therefore cannot mean the write did not happen.

### Tool result encoding

A tool result is an MCP `tools/call` result whose `content[0]` is
`{"type":"text","text":"<JSON>"}`; the domain object is the JSON inside `text`
(`internal/server/memory/memory.go:328-334`). There is no
`structuredContent`. A domain failure sets `isError: true` and carries the
error envelope in the same position. A JSON-RPC `error` member appears only
for transport-level faults (`docs/operator/mcp.md:39-44`).

### Wire shapes

Normative schemas: `docs/protocol/schemas/memory_verbs_*.schema.json`. A
reflection test (`docs/protocol/schemas/schemas_test.go`) keeps them equal to
the Go structs the server marshals. Conformance fixtures:
`testdata/conformance/memory_verbs/cases.json` (17 cases vendored from gbrain,
checksum-pinned by `MANIFEST`).

`recall` (`internal/server/memory/recall.go:21-59`). Every request field is
optional.

| Request field | Type | Meaning |
|---|---|---|
| `query` | string | Runs the search arm over indexed pages. |
| `entity` | string | Scopes the facts arm to one entity. |
| `budget_tokens` | integer ≥ 0 | Packing budget in estimated tokens (characters / 4). Not a spend bound. |
| `since` | string | ISO 8601 lower bound on fact creation time, facts arm only. |
| `session_id` | string | Accepted; unused by the handler. |
| `limit` | integer ≥ 0 | Per-arm cap; default 50. |

Response: `protocol_version`, `facts[]`, `total`, and optionally `results[]`,
`search_degraded`, `budget_tokens`, `budget_used`, `dropped_count`. A `fact`
has `id` (integer, legacy), `fact_id` (string), `fact`, `kind`, `entity_slug`,
`provenance`, `valid_until`, `visibility`. A `result` has `slug`, `title`,
`chunk`, `evidence`, `create_safety`, `provenance`. No fact or result carries a
creation time, a confidence, a version, a source revision, or an index
revision.

`remember` (`internal/server/memory/remember.go:17-38`). Request: `fact` and
`provenance` (both required; provenance is free text of at most 500
characters), optional `ttl`, `entity`, `kind`, `visibility`, and
`operation_key` (1..128 ASCII letters, digits, dot, colon, underscore or
hyphen). A keyed request must omit `ttl` or use an absolute timestamp.
Response:
`protocol_version`, `id`, `status` (`inserted`, `duplicate`, or `superseded`),
`status_text`, `entity_slug`, `valid_until`, optional `search_state` and
`expired`, and `degraded_dedup` (set to true in this handler). The returned
`id` is the lowercase hexadecimal SHA-256 of the stored source
(`internal/server/memory/README.md:16-18`).

`cancel_memory_operation` (`internal/server/memory/cancel.go:14-49`) accepts
the original `operation_key` and optional `reason`. It durably fences that key
even if no fact exists, and responds with `canceled: true`, the fact SHA-256
`id` or an empty id, and whether an active fact was expired. A matching keyed
remember cannot create a canceled absent fact; a replay of an already written
matching fact can recover its now-expired id. It is not a status lookup for
an unrelated or still-running request.

`read_memory_fact` (`internal/server/memory/readfact.go:12-25,77-165`)
accepts only an exact lowercase 64-character fact SHA-256 id and returns
one live, world-visible fact's content, kind, visibility, entity slug,
provenance, valid-until and `content_untrusted: true`. Missing, private,
expired, forgotten and canceled facts all return `unavailable`; oversized
MCP results are also refused. It does not return Zatiti's claim UUID,
version, confidence, observed time, source revision or index revision.

`forget` (`internal/server/memory/forget.go`). Request: `id` (required, the
SHA-256 fact id) and optional `reason`. Response: `protocol_version`, `id`,
`expired` (`true` when this call expired the fact, `false` when it was already
expired), `reason`. Forget appends an expiry record; the original bytes stay
in the brain's Git history.

`synthesize` (`internal/server/memory/synthesize.go:12-31`). Request:
`question` (required), optional `since`, `until`. Response:
`protocol_version`, `answer`, `sources[]`, optional `gaps[]`, and `cost` with
`model`, `input_tokens`, `output_tokens`, `usd_estimate` (each of the last
three may be `null`). The protocol document describes `cost` as "an honest
signal, not an invoice" (`docs/protocol/MEMORY_VERBS_v1.md:175`).

`entity`. Looks up one entity page by name. Not required by this adapter.

Error envelope (`internal/server/memory/memory.go:57-63`): `protocol_version`,
`error`, `message`, `suggestion`, optional `detail`. Codes include
`invalid_params`, `operation_conflict`, `operation_canceled`,
`provenance_required`, `not_found`, `scope_denied`, `unavailable`,
`budget_unsatisfiable` (reserved, never emitted), `internal`.

### Command identity and idempotency

`remember` now accepts a caller-chosen durable, brain-scoped `operation_key`.
The writer records it in the canonical source and, under its queue, compares
the normalized payload of every replay even after expiry or withdrawal:
matching bytes recover the same source id; changed bytes conflict
(`internal/writer/memoryfact.go:88-119`,
`internal/store/memoryfact.go:83-106`). A keyed relative TTL is rejected
before writing. `cancel_memory_operation` writes a durable cancellation fence
even for a missing key (`internal/writer/memoryfact.go:255-285`). These are
meaningful improvements over the previous pin's active-fact-only dedup.
They cover keyed `remember`, not every Zatiti action; `forget` remains
idempotent by fact id without a command key. The JSON-RPC `id` is still only
an in-flight transport identity.

### Reconciling a lost acknowledgement

No public read answers "did command X commit?" by `operation_key` and returns
an authoritative terminal status. Retrying the same keyed `remember` can
recover an already committed fact id without inserting another, but is a
second physical mutation call and cannot be smuggled into Zatiti's
`Reconcile` read. `cancel_memory_operation` can prevent a future write for
one key, but does not prove whether the original call is still in flight.
`read_memory_fact` requires the fact id that a lost acknowledgement may have
hidden. `recall` absence is still non-authoritative.

### Single writer

`serve` now holds `writer.AcquireBrain(root)` while its writer queue and
index are open (`internal/cli/serve.go:228-249`). This takes an exclusive
nonblocking advisory lock on `.serenity/writer.lock` on Darwin/Linux;
cooperating CLI write commands use the same helper
(`internal/writer/ownership_unix.go:15-55`, `internal/cli/ownership.go:13-54`).
It prevents a second cooperating local writer, including another `serve`,
from owning the brain concurrently. It does not fence arbitrary processes
that bypass the lock or prove cross-host filesystem behavior. Each self-hosted
`serve` still serves one brain root; requests carry no brain selector.

### Cost and disclosure

- `recall` with a `query` embeds the query through the configured embedding
  provider when one exists (`internal/server/memory/recall.go:174`). The
  response reports no cost. `search_degraded` is present only when no embedder
  ran. A caller cannot tell from the response which provider received the
  query.
- `synthesize` calls the composer model and returns the best-effort `cost`
  object above, in floating-point US dollars.
- `remember` and `forget` call no model.
- No request carries a spend ceiling or a list of allowed provider
  destinations. `internal/spend` implements a monthly ceiling
  (`DefaultMonthlyCeilingUSD`), but its own doc comment says configuration
  from `serenity.yml` is a follow-up (`internal/spend/spend.go:38-47`), and
  the MEMORY_VERBS handlers do not call it. Provider endpoints come from
  Serenity's own configuration and environment (`docs/providers.md`).

### Revisions, freshness, export

No served call reports the brain's Git revision, a content digest, the index
revision, or how far the index trails the sources. No served call exports a
revision or restores one. ADR 012 decision 6 says a reader "sees the index as
of its last open" and that a bounded-staleness guarantee would need a public
protocol change.

### Go read facade

`pkg/serenity/serenity.go` exports a read-only in-process facade:
`Open(brainPath)`, `(*Brain).CheckPlan`, `(*Brain).Recall`, `(*Brain).Brief`.
`Recall` runs full-text search and then the composer model, records spend in
Serenity's own ledger, and takes no spend bound. Its citations carry
`claim_id`, `confidence` (a float), and `observed_at`, which is more than the
wire protocol returns. This adapter cannot use it: the package's only allowed
production import is `internal/contract`, integration owns `go.mod`, and the
candidate tag has not been qualified for Zatiti.

## Required capability versus the pin

"Provided" means the pinned public interface supports the requirement as this
adapter's frozen Zatiti-side schemas state it. "Partly" means a related
upstream call exists but cannot satisfy the frozen schema truthfully.

| # | Requirement from `AGENTS.md` | Provided | Evidence |
|---|---|---|---|
| 1 | One `Invoke` is one accounted physical request; no preflight inside it | **No** | Every tool call needs `initialize`, then `notifications/initialized`, then `tools/call`. The frozen action kinds include no session action, so the handshake cannot be its own governed attempt. |
| 2 | `recall` | **Partly** | The verb exists. Its facts cannot populate `MemoryClaim`: `id` must be a UUID and upstream ids are 64-character SHA-256 strings; `confidence` and `freshness` are required and absent upstream. |
| 3 | `remember` | **Partly** | The verb exists. The result has a SHA-256 id, no version, and no UUID, so the memory owner receives no addressable claim. Sources must collapse into 500 characters of free-text provenance. |
| 4 | `inspect` one claim by id and version | **Partly** | `read_memory_fact` returns one active, world-visible fact by exact SHA-256 id without search/model calls (`internal/server/memory/readfact.go:77-165`). It has no Zatiti UUID/version and deliberately returns `unavailable` for private, expired, forgotten or canceled facts. |
| 5 | `promote` with source brain, claim, curator, and redaction lineage | **No** | No promotion verb and no lineage fields. `remember` could carry a pointer in provenance only. |
| 6 | `retract`, active recall only | **Partly** | `forget` matches the semantics, and history is preserved. It needs the SHA-256 fact id, and the frozen `SerenityRetract` action carries only a UUID `VersionRef`. No single call resolves one to the other. |
| 7 | `export_revision` and backup revision protocol | **No** | No revision is reported or exported. |
| 8 | Brain selected per request by `brain_id` | **No** | Self-hosted `serve` serves one brain root; brain identity is the endpoint. A profile mapping of brain to endpoint covers this only by deployment. The separately hosted gateway selects by credential binding, not a self-hosted MEMORY_VERBS `brain_id` field. |
| 9 | Stable `adapter_command_id` honored upstream | **Partly** | `remember.operation_key` durably binds a bounded caller key to normalized payload, with replay/conflict behavior (`internal/server/memory/remember.go:17-24,73-77`; `internal/writer/memoryfact.go:88-119`). Other action kinds have no equivalent caller key. |
| 10 | Authoritative command status lookup after a lost acknowledgement | **No** | No public read/status lookup by `operation_key`. A keyed remember replay is another physical write call; cancellation fences a key but does not establish whether an in-flight original committed. |
| 11 | Safe idempotent replay | **Partly** | A keyed `remember` with absolute/omitted TTL returns the same fact id after expiry/withdrawal and conflicts on changed payload; canceled absent keys stay canceled (`internal/writer/memoryfact.go:88-119,255-285`). This does not cover every Zatiti action or supply read-only reconciliation. |
| 12 | Enforceable per-call cost bound | **No** | No bound parameter; the handlers do not consult `internal/spend`. |
| 13 | Enforceable disclosure destinations | **No** | Provider endpoints are Serenity-side configuration; responses do not name the provider that received data. |
| 14 | Usage reporting | **Partly** | `synthesize` alone reports best-effort tokens and a floating-point dollar estimate. `recall` reports none even when it embeds the query. |
| 15 | Source, scope, version, confidence, and freshness on results | **Partly** | Free-text `provenance` and `visibility` only. |
| 16 | Source and index revision, enforceable minimum freshness | **No** | Not reported; ADR 012 decision 6 disclaims it. |
| 17 | Exactly one writer per brain | **Partly** | `serve` and cooperating CLI writers hold an advisory Darwin/Linux `.serenity/writer.lock` (`internal/cli/serve.go:228-249`; `internal/writer/ownership_unix.go:15-55`). Arbitrary writers and cross-host filesystem behavior are outside that guarantee. |
| 18 | Verifiable running version or commit | **No** | `serverInfo.version` is `dev`. |
| 19 | Go read facade for compatible reads | **Partly** | Exists upstream; unusable under this package's import allowlist and the current `go.mod`. |
| 20 | Credential through a Zatiti `credential_ref` | **Partly** | Bearer auth and named host-Keychain credential profiles exist (`internal/cli/connect.go:68-110`; `internal/cli/serve.go:145-151`), but no public token handoff into Zatiti custody is documented and a profile name is not persistently bound to a brain. |

## Consequence for this adapter

Row 1 blocks every operation, independently of rows 2 to 20: against this pin,
no `Invoke` can reach a Serenity tool in one physical request. Rows 2 to 7
also show that no operation's result fits the frozen evidence schema without
inventing identifiers or values; the exact fact read and keyed write extensions
do not change that. This adapter therefore sends nothing. It validates and
digest-binds the profile, rejects any profile that claims a
capability this table marks as not provided, and refuses each operation with a
named fault after the local checks that can run. `Reconcile` reports the outcome as unknown and sends nothing, because no
authoritative lookup exists.

This document's scope ends at what upstream would need for each row to become
"Provided". The smallest set is: a sessionless call path or a contract that
admits an accounted handshake; an authoritative read-only status lookup for
the now-stored keyed `remember` identity and equivalent identities for other
writes; UUID-compatible or contract-accepted claim identities with version,
confidence, and observed time on results; a revision report; enforceable
per-call spend and destination bounds; and a build that reports its commit.
