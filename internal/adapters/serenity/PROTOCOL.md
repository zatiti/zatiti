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
| Commit | `f5a5154e1c4d808e10b495fca3bd50d842f0aa92` |
| Commit date | 2026-09-07 |
| Branch | `main`, equal to `origin/main` when inspected |
| `git describe` | `v0.1.1-240-gf5a5154` |
| Go directive | `go 1.26` |
| License | Apache License 2.0 (`LICENSE`) |
| Memory protocol | MEMORY_VERBS v1, domain `protocol_version` integer `1` |
| Transport protocol | MCP `2025-11-25` (`internal/server/mcp/tools.go:19`) |
| Upstream protocol origin | gbrain `upstream-verbs.ts`, commit `d35c9c9e441e6cfc86dd5e84b0b168c6b18ee775` |

Pin caveats:

- **No release contains this protocol.** The newest tag, `v0.1.1`
  (`a27c43e5`, 2026-08-28), has neither `internal/server/mcp/http.go` nor
  `internal/server/memory/remember.go`. The memory protocol exists only on
  untagged `main`, 240 commits past the tag.
- **A running build cannot prove its commit.** `internal/cli/root.go:11`
  declares `var Version = "dev"`, and the `Makefile` sets no version linker
  flag. MCP `initialize` returns that string as `serverInfo.version`
  (`internal/server/mcp/server.go:152`). No public call reports the source
  commit, so an adapter cannot verify at run time that it talks to this pin.
- **Upstream documents disagree at this commit.** `README.md:147-149` says the
  MCP tool registry "is currently empty; memory and direction tools are not
  exposed yet", and `internal/server/memory/README.md:57-58` says its checks
  "do not establish an HTTP MCP endpoint". `docs/operator/mcp.md`,
  `docs/protocol/MEMORY_VERBS_v1.md`, and the code
  (`internal/cli/serve.go:105-146`, `internal/cli/serve.go:178-231`) show five
  tools served over both transports. This document follows the code.

## What Serenity serves

### Transports

`serenity serve` requires exactly one of two flags
(`internal/cli/serve.go:28-50`):

- `--stdio`: newline-delimited JSON-RPC MCP over the process's standard
  streams. No authentication; the spawning process is the trust boundary.
- `--http`: MCP Streamable HTTP at the single route `/mcp`
  (`internal/cli/serve.go:123-125`), loopback by default, with optional LAN
  bind and mutual TLS (`docs/operator/server.md`).

Both transports serve the same registry: the five MEMORY_VERBS v1 tools
`recall`, `remember`, `entity`, `synthesize`, `forget`
(`internal/server/memory/`). No other memory operation is served.

Two more protocols are documented, DISPOSITION v1 and DIRECTION v1
(`docs/protocol/`), but `--http` does not serve them:
`docs/operator/mcp.md:79-83` calls their daemon assembly "separate,
still-outstanding work". They are not part of this pin.

### HTTP transport rules

Source: `internal/server/mcp/http.go`, `internal/server/server.go:105-130`,
`docs/operator/mcp.md`.

- Every route requires `Authorization: Bearer <token>`, compared in constant
  time; a missing or wrong token gets `401`. The token is minted by
  `serenity init` into the OS keychain of the Serenity host and is read on
  every request. `serenity connect` reports only whether the token exists
  (`internal/cli/connect.go:59-62`); no public command prints it.
- Only `POST` and `DELETE` are implemented; `GET` gets `405`. A request with an
  `Origin` header gets `403`. `Content-Type` must be `application/json`, else
  `415`. The body limit is 1 MiB (`internal/server/mcp/server.go:15`), else
  `413`.
- Responses are always one JSON document. The transport never upgrades to an
  event stream and has no resumption.
- **A tool call needs a three-request session handshake.** A `POST` without
  `Mcp-Session-Id` must be `initialize`, else `400`
  (`internal/server/mcp/http.go:241-250`). The response carries a fresh
  `Mcp-Session-Id`. The client must then `POST` a
  `notifications/initialized` message on that session (`202`), because
  `tools/call` is refused with JSON-RPC `-32600 "Initialization required"`
  until the session reaches state 2 (`internal/server/mcp/server.go:80-82`,
  `internal/server/mcp/server.go:185-189`). Only the third request can reach a
  tool. The stdio transport runs the same state machine.
- Sessions live in the server process's memory. The server evicts a session
  after 30 idle minutes, holds at most 64, and loses all of them on restart
  (`internal/server/mcp/http.go:46-57`). An unknown session gets `404`.
- **A dropped connection does not stop a call.** An accepted `tools/call` runs
  under the handler's lifetime context, not the request's. When the client
  disconnects, the call keeps running and its result is undeliverable
  (`internal/server/mcp/http.go:77-87`, `internal/server/mcp/http.go:226-232`).
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

`remember` (`internal/server/memory/remember.go:15-33`). Request: `fact` and
`provenance` (both required; provenance is free text of at most 500
characters), optional `ttl`, `entity`, `kind`, `visibility`. Response:
`protocol_version`, `id`, `status` (`inserted`, `duplicate`, or `superseded`),
`status_text`, `entity_slug`, `valid_until`, and `degraded_dedup`, which this
server always sets to `true` (`internal/server/memory/remember.go:146`). The
returned `id` is the lowercase hexadecimal SHA-256 of the stored source
(`internal/server/memory/README.md:16-18`).

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
`error`, `message`, `suggestion`, optional `detail`. Codes: `invalid_params`,
`provenance_required`, `not_found`, `scope_denied`, `unavailable`,
`budget_unsatisfiable` (reserved, never emitted), `internal`.

### Command identity and idempotency

No MEMORY_VERBS request carries a caller-chosen command identity, idempotency
key, or request id that the server stores. The JSON-RPC `id` lives only while
the call is in flight.

`remember` has exact-content deduplication, not command idempotency. The key
covers fact text, provenance, entity type and slug, kind, visibility, and the
resolved expiry instant (`internal/writer/memoryfact.go:90`). It compares only
against facts that are still active (`internal/store/memoryfact.go:421-436`).
Three consequences:

- An identical replay while the first fact is active returns
  `status: "duplicate"` with the existing id.
- An identical replay after that fact was forgotten, or after its expiry,
  inserts a second fact. A blind repeat can therefore resurrect retracted
  content.
- A request with a relative `ttl` resolves to a different expiry instant on
  each call, so its replay never deduplicates.

Deduplication also holds only inside one writer process: "Independent writer
processes do not share this queue" (`internal/server/memory/README.md:28-30`).

`forget` is idempotent by design.

### Reconciling a lost acknowledgement

No call answers "did command X commit?". The closest read is `recall`, whose
facts echo `provenance`. It is not an authoritative lookup: it returns only
active, world-visible facts, newest first, cut at `limit`, with no filter on
provenance or fact id. Finding a fact proves it exists. Not finding one proves
nothing, because the fact may be expired, private, beyond the cap, or still
being written by a call that outlived its connection.

### Single writer

Each `serve` process owns one in-process writer queue
(`internal/cli/serve.go:189-198`). ADR 012 states the rule that exactly one
process writes a brain (`docs/adr/012-embedded-read-facade-single-writer.md`,
decision 4). Nothing at this pin enforces it for `serve`: the only pidfile
guard belongs to the scheduler daemon (`internal/server/daemon.go:153-166`),
and `serenity sync` or a second `serve` can open another queue on the same
brain. One writer per brain is a deployment obligation, not an upstream
guarantee. A `serve` process also serves exactly one brain root and takes no
brain selector on any request, so one endpoint is one brain.

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
facade exists only at an untagged commit.

## Required capability versus the pin

"Provided" means the pinned public interface supports the requirement as this
adapter's frozen Zatiti-side schemas state it. "Partly" means a related
upstream call exists but cannot satisfy the frozen schema truthfully.

| # | Requirement from `AGENTS.md` | Provided | Evidence |
|---|---|---|---|
| 1 | One `Invoke` is one accounted physical request; no preflight inside it | **No** | Every tool call needs `initialize`, then `notifications/initialized`, then `tools/call`. The frozen action kinds include no session action, so the handshake cannot be its own governed attempt. |
| 2 | `recall` | **Partly** | The verb exists. Its facts cannot populate `MemoryClaim`: `id` must be a UUID and upstream ids are 64-character SHA-256 strings; `confidence` and `freshness` are required and absent upstream. |
| 3 | `remember` | **Partly** | The verb exists. The result has a SHA-256 id, no version, and no UUID, so the memory owner receives no addressable claim. Sources must collapse into 500 characters of free-text provenance. |
| 4 | `inspect` one claim by id and version | **No** | No fetch-by-id verb; `entity` reads entity pages. |
| 5 | `promote` with source brain, claim, curator, and redaction lineage | **No** | No promotion verb and no lineage fields. `remember` could carry a pointer in provenance only. |
| 6 | `retract`, active recall only | **Partly** | `forget` matches the semantics, and history is preserved. It needs the SHA-256 fact id, and the frozen `SerenityRetract` action carries only a UUID `VersionRef`. No single call resolves one to the other. |
| 7 | `export_revision` and backup revision protocol | **No** | No revision is reported or exported. |
| 8 | Brain selected per request by `brain_id` | **No** | One `serve` process serves one brain root; brain identity is the endpoint. A profile mapping of brain to endpoint covers this only by deployment. |
| 9 | Stable `adapter_command_id` honored upstream | **No** | No request field stores a caller identity. |
| 10 | Authoritative command status lookup after a lost acknowledgement | **No** | No lookup. `recall` absence proves nothing. Calls outlive dropped connections. |
| 11 | Safe idempotent replay | **No** | Content deduplication covers active facts in one process only; replay after a forget inserts again. |
| 12 | Enforceable per-call cost bound | **No** | No bound parameter; the handlers do not consult `internal/spend`. |
| 13 | Enforceable disclosure destinations | **No** | Provider endpoints are Serenity-side configuration; responses do not name the provider that received data. |
| 14 | Usage reporting | **Partly** | `synthesize` alone reports best-effort tokens and a floating-point dollar estimate. `recall` reports none even when it embeds the query. |
| 15 | Source, scope, version, confidence, and freshness on results | **Partly** | Free-text `provenance` and `visibility` only. |
| 16 | Source and index revision, enforceable minimum freshness | **No** | Not reported; ADR 012 decision 6 disclaims it. |
| 17 | Exactly one writer per brain | **No** | Documented rule; no lock enforces it for `serve`. |
| 18 | Verifiable running version or commit | **No** | `serverInfo.version` is `dev`. |
| 19 | Go read facade for compatible reads | **Partly** | Exists upstream; unusable under this package's import allowlist and the current `go.mod`. |
| 20 | Credential through a Zatiti `credential_ref` | **Partly** | Bearer token is supported on the wire. No public command releases the token from the Serenity host's keychain, so provisioning it into Zatiti's secret store has no documented path. |

## Consequence for this adapter

Row 1 blocks every operation, independently of rows 2 to 20: against this pin,
no `Invoke` can reach a Serenity tool in one physical request. Rows 2 to 7
also show that no operation's result fits the frozen evidence schema without
inventing identifiers or values. This adapter therefore sends nothing. It
validates and digest-binds the profile, rejects any profile that claims a
capability this table marks as not provided, and refuses each operation with a
named fault after the local checks that can run. `Reconcile` reports the
outcome as unknown with a non-authoritative lookup and sends nothing, because
no lookup exists.

This document's scope ends at what upstream would need for each row to become
"Provided". The smallest set is: a sessionless call path or a contract that
admits an accounted handshake; a stored caller command identity with a status
lookup; UUID-compatible or contract-accepted claim identities with version,
confidence, and observed time on results; a revision report; per-call spend
and destination bounds or a truthful report of what was spent and where; and a
build that reports its commit.
