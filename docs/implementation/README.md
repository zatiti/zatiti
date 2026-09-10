# Package implementation specification

Revision 1. **Specification scaffold; no product implementation or executed release qualification is claimed.**

37 disjoint implementation roots; 197 public operations; 67 internal owner methods; 127 source blocks with explicit ownership; 116 named acceptance cases covering Z01–Z21 and release journeys.

Each root already contains a complete committed AGENTS.md: local mission, allowed imports, owned requirements, exact Go interfaces, incoming/outgoing operation schemas, persistence/recovery rules and named acceptance criteria. An agent can implement from that file without the RFC. Scope prompts intentionally repeat necessary contracts; do not edit generated copies independently.

## Start and dispatch

1. Assign the integration owner its **serialized foundation assignment**: create root go.mod/go.sum for module github.com/zatiti/zatiti, Go 1.26.0/toolchain 1.26.2, resolve exact dependency releases/commits/licenses/checksums, and qualify required upstream seams. Record docs/implementation/dependencies.lock.json. This is a specified implementation deliverable, not a preexisting lock or compatibility claim.
2. Implement wave 0 contract/platform/storage against frozen interfaces. A dependency library required by a scope must be pinned before that scope starts. Land the shared contract baseline before dispatching dependent agents; no guessing or independently rewriting shared types.
3. Wave 1 establishes identity/compiler and transport/application foundations. Wave 2 domain services, adapters and presentation transports can generate code concurrently against the baseline, using exact local fakes. The wave numbers are integration order, not a claim that all runtime collaborators already exist.
4. Wave 3 assembles real controller, entrypoints and desktop; replace development fakes with actual modules. Wave 4 proves cross-package transactions, all operations/parity/journeys and real platform/client/provider qualification.
5. Use one isolated worktree per concurrent root and one landing owner. Dispatch only disjoint roots; no ancestor/root assignment concurrently with descendants. A package may add private files in its root but no new ownership roots or exported seams without a coordinated revision.

Root dependency work and the lock report belong only to integration's serialized exception. cmd entrypoints own wiring, tests/integration owns product-wide fixtures, tests/qualification owns external/GUI qualification, packaging owns distribution, and .github/workflows owns CI. The root AGENTS.md is stable repository guidance, not a concurrently implementable root task. There is no Kazi/apply dependency and no assumption about coding harness.

## Frozen ownership

| Scope | Kind | Wave | Ownership |
|---|---|---:|---|
| [`internal/contract`](../../internal/contract/AGENTS.md) | foundation | 0 | Own shared Go types, strict wire validation, canonicalization, schema-derived primitives and stable faults. |
| [`internal/platform`](../../internal/platform/AGENTS.md) | foundation | 0 | Own OS locks, protected local configuration, secret custody, encrypted blob files and client secure storage. |
| [`internal/storage`](../../internal/storage/AGENTS.md) | foundation | 0 | Own the SQLite writer, consistent reads, transaction Unit, migrations and atomic event outbox. |
| [`internal/identity`](../../internal/identity/AGENTS.md) | domain | 1 | Own principals, grants, authentication metadata, credential references and immediate revocation. |
| [`internal/configuration`](../../internal/configuration/AGENTS.md) | domain | 1 | Own organizations, chiefs, teams, projects, worker definitions, bindings, drafts, plans, revisions and the sole definition compiler. |
| [`internal/skills`](../../internal/skills/AGENTS.md) | domain | 2 | Own immutable skill versions, safe import validation and sealed evaluation jobs. |
| [`internal/connections`](../../internal/connections/AGENTS.md) | domain | 2 | Own trusted tool contracts, connection definitions, validation freshness and credential setup challenges. |
| [`internal/policy`](../../internal/policy/AGENTS.md) | domain | 2 | Own deterministic authority intersection, standing/exact-review policy and earned autonomy. |
| [`internal/reviews`](../../internal/reviews/AGENTS.md) | domain | 2 | Own exact reviews, eligibility checks, immutable decisions and delegated review authority. |
| [`internal/accounting`](../../internal/accounting/AGENTS.md) | domain | 2 | Own budget definitions, atomic reservations and exact cost/uncertainty ledgers. |
| [`internal/tasks`](../../internal/tasks/AGENTS.md) | domain | 2 | Own durable outcomes, immutable accepted contracts, dependency graph and narrowing delegation. |
| [`internal/scheduling`](../../internal/scheduling/AGENTS.md) | domain | 2 | Own durable schedules, responsibility reasoning cycles, occurrence identities and wake conditions. |
| [`internal/messaging`](../../internal/messaging/AGENTS.md) | domain | 2 | Own conversations, durable mailboxes, participant disclosure and meaningful activity projections. |
| [`internal/execution`](../../internal/execution/AGENTS.md) | domain | 2 | Own runs, executor attempts, leases, hosted loop context, cooperative protocol and verification coordination. |
| [`internal/effects`](../../internal/effects/AGENTS.md) | domain | 2 | Own immutable actions, logical effects, physical attempts, dispatch claims and reconciliation history. |
| [`internal/memory`](../../internal/memory/AGENTS.md) | domain | 2 | Own scoped memory bindings, governed Serenity jobs and promotion/retraction lineage. |
| [`internal/artifacts`](../../internal/artifacts/AGENTS.md) | domain | 2 | Own immutable artifact metadata, resumable bounded uploads and integrity-visible byte access. |
| [`internal/evidence`](../../internal/evidence/AGENTS.md) | domain | 2 | Own durable command replay, authorized event reads and receipts projected from actual evidence. |
| [`internal/installation`](../../internal/installation/AGENTS.md) | domain | 2 | Own bootstrap, installation restriction/maintenance state, health, encrypted backup and paused restore jobs. |
| [`internal/registry`](../../internal/registry/AGENTS.md) | infrastructure | 1 | Own typed operation registration, collision checks, discovery, schema/OpenAPI generation and parity enumeration. |
| [`internal/application`](../../internal/application/AGENTS.md) | infrastructure | 1 | Own authenticated operation execution, transaction composition, internal port allowlists and durable submission replay. |
| [`internal/controller`](../../internal/controller/AGENTS.md) | infrastructure | 3 | Own one controller lifetime, adapter dispatch, durable background work and recovery coordination. |
| [`internal/server`](../../internal/server/AGENTS.md) | infrastructure | 2 | Own private socket HTTP/JSON and explicit remote-desktop mutual TLS transport. |
| [`internal/client`](../../internal/client/AGENTS.md) | infrastructure | 1 | Own shared controller client, secure credential plumbing, reconnect and explicit submission-key retry. |
| [`internal/cli`](../../internal/cli/AGENTS.md) | infrastructure | 2 | Own Cobra command generation, structured input, deterministic JSON output and process exit mapping. |
| [`internal/mcp`](../../internal/mcp/AGENTS.md) | infrastructure | 2 | Own stdio MCP adapter over the common authenticated controller client. |
| [`internal/desktop`](../../internal/desktop/AGENTS.md) | infrastructure | 3 | Own native Fyne chat-first human workspace and acknowledged controller-derived views. |
| [`internal/adapters/responses`](../../internal/adapters/responses/AGENTS.md) | adapter | 2 | Qualified hosted Responses model adapter. |
| [`internal/adapters/github`](../../internal/adapters/github/AGENTS.md) | adapter | 2 | Qualified GitHub repository artifact/publication adapter. |
| [`internal/adapters/httpread`](../../internal/adapters/httpread/AGENTS.md) | adapter | 2 | Qualified bounded public HTTP reads for research. |
| [`internal/adapters/serenity`](../../internal/adapters/serenity/AGENTS.md) | adapter | 2 | Qualified public Serenity protocol/read-facade adapter and writer capability report. |
| [`cmd/zatiti`](../../cmd/zatiti/AGENTS.md) | entrypoint | 3 | Assemble controller/CLI/MCP binary and local bootstrap/helper mechanics. |
| [`cmd/zatiti-desktop`](../../cmd/zatiti-desktop/AGENTS.md) | entrypoint | 3 | Assemble the separately packaged native desktop client. |
| [`tests/integration`](../../tests/integration/AGENTS.md) | verification | 4 | Own real cross-package fixtures, atomicity, transport parity and end-to-end journeys. |
| [`tests/qualification`](../../tests/qualification/AGENTS.md) | verification | 4 | Own executable external adapter, real desktop/platform/client qualification and release evidence. |
| [`packaging`](../../packaging/AGENTS.md) | support | 4 | Own release manifests, service launchers, secure helper and Serenity distribution lifecycle. |
| [`.github/workflows`](../../.github/workflows/AGENTS.md) | support | 4 | Own CI workflow validation and release qualification orchestration. |

## Sources and validation

- [Shared contracts](contracts.md): exact Go boundary, transactions, wire/error/replay/IO rules and selected implementation families.
- [Operation catalog](operations.json): every public/internal operation, strict JSON Schemas, owner, allowed callers, CLI/MCP mapping and behavior.
- [Adapter schemas](adapter-schemas.json): exact local context/model/tool/evidence/verifier/backup seams; upstream translation is explicitly qualified.
- [Requirements](requirements.json): retained full source requirement blocks, owner and participants. The renderer does not read docs/rfc.md.
- [Coverage](coverage.json): source block → implementation/verification roots and named-case ownership.
- [Acceptance](acceptance.json): named setups/actions/expected observations, not claims of tests already run.
- [Package manifest](packages.json): frozen roots, imports, missions and implementation decisions.

Authoritative edits: tools/specgen/model.py and packages.py, contracts.md, requirements.json, acceptance.json, adapter-schemas.json. Rendered: every package AGENTS.md, operations.json, packages.json, coverage.json and this index. The root AGENTS.md and ADR are authored stable guidance.

```sh
python3 tools/specgen/render.py
python3 tools/specgen/render.py --check
```

Renderer checks unique/disjoint roots, acyclic allowed Go imports, operation/schema ownership, public name collisions, internal caller allowlists, reference closure, requirement/case ownership, all Z01–Z21 gates and byte-for-byte prompt freshness. When Python jsonschema is available it also validates JSON Schema 2020-12 syntax. Structural checks do not prove semantic implementation correctness or replace real integration tests.

Regeneration can run with only the authoritative inputs above and no RFC. --output-dir DIR renders an independent tree for review. Contract changes update all copies together and require affected-dependency review; implementers cannot lower acceptance criteria.

## Qualification boundary

The RFC deliberately leaves external versions and protocol capability qualification open. This specification selects library families/framework/provider and freezes local interfaces; integration must resolve and test exact upstream pins before dependent implementation. In particular Serenity command reconciliation/cost enforcement and provider hard caps are requirements to establish, never invented capabilities. An unavailable required guarantee blocks enabling that mode and its release gate; it must not become fake success or an undocumented substitute.
