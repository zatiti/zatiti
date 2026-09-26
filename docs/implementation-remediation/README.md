# Zatiti implementation completion plan

**The P00–P50 implementation dispatch has landed in `main`; the product is not launch-ready.** The 2026-09-19 audit below is historical. The current ledger records 46 implemented cards, three blocked on hosted Serenity capabilities, and two whose release tooling exists but still needs real qualification. No card is considered release evidence merely because its code or tests landed.

The remaining ordered gates are in [launch readiness](../launch-readiness.md). In particular, the current execution context fails closed when configured memory recall has not completed; Serenity OAuth and hosted recall are not qualified; and signed/notarized Mac Intel and Apple Silicon installs have not been proven on clean hosts. The historical roadmap and work logs remain intact as historical records.

## Historical audit and dispatch record

The following materials describe the original remediation plan and are retained to explain its scope. They are no longer a dispatch queue. Any new implementation work must be added to a new dated plan after the open gates are resolved.

[plan.json](plan.json) is the current card disposition ledger; [findings.json](findings.json) provides historical evidence pointers. [inventory.json](inventory.json) covers the original 36 roots and 264 baseline operations. [coverage.json](coverage.json) assigns the original requirements and acceptance cases. [baseline.json](baseline.json) records checks from the audit date.

## Original delivery milestones

- **M0–M2:** implementation cards landed; current source includes controller, execution, transport and recovery paths.
- **M3:** partial. Backup/restore code and qualifications exist, but hosted memory recall does not reach model context and the Serenity service path is not qualified.
- **M4:** partial. Packaging and release evidence gates exist; no signed clean-host Mac install-to-first-chat evidence is recorded.

The P00–P50 cards below preserve the original dependency graph and acceptance intent. Their first-line status is synchronized with plan.json; they are retained for traceability, not dispatch.

## Ownership and execution rules

The execution root is the unavoidable serialized critical path (P14 → P15 → P16 → P18 → P20 → P21). The controller and desktop roots have their own serialized chains. Do not dispatch those cards simultaneously merely because the desired end product is parallel. Parallelism comes from independent identity/configuration/connections/policy/accounting/artifacts/tasks/messaging/effects/adapters/storage/platform/transport roots. A later contract-approved split could change ownership, but this plan does not invent one.

The original work used isolated worktrees and a single landing owner. Any future contract revision must use the current spec generator and coordinated ownership map; do not treat these closed cards as authorization to reopen work.

Before any heavy multi-package build/test, check uptime and hold if the one-minute load exceeds 10. Acquire R-build-lease using the canonical claim primitive required by root AGENTS.md, retain the winning SHA, and release immediately after the command; do not infer a won lease from exit code alone. At most two heavy lanes per project, with only one actual shared heavy-build holder. Observe any applicable stricter installed hook/runner rules.

## Assignment index

| ID | Assignment | Write owner | Depends on | Priority |
|---|---|---|---|---|
| [P00](assignments/P00.md) | Freeze the remediation contracts and regenerate assignments | `tools/specgen` | — | P0 |
| [P01](assignments/P01.md) | Implement shared types and strict validation for the new seams | `internal/contract` | P00 | P0 |
| [P02](assignments/P02.md) | Reconcile toolchain and dependency evidence | `foundation` | P01 | P0 |
| [P03](assignments/P03.md) | Bind worker principals and expose authenticated caller metadata | `internal/identity` | P02 | P0 |
| [P04](assignments/P04.md) | Route worker proposals through the common authorization boundary | `internal/application` | P03, P02 | P0 |
| [P05](assignments/P05.md) | Resolve complete immutable worker configuration | `internal/configuration` | P02 | P0 |
| [P06](assignments/P06.md) | Complete connection setup and adapter validation actions | `internal/connections` | P02 | P0 |
| [P07](assignments/P07.md) | Enforce authority on planned model and tool actions | `internal/policy` | P02 | P0 |
| [P08](assignments/P08.md) | Close runtime budget and reservation accounting | `internal/accounting` | P02 | P0 |
| [P09](assignments/P09.md) | Publish named outputs and context artifacts durably | `internal/artifacts` | P02 | P0 |
| [P10](assignments/P10.md) | Add durable intent delivery and complete chat queries | `internal/messaging` | P02 | P0 |
| [P11](assignments/P11.md) | Make tasks startable and unblock dependency progress | `internal/tasks` | P02 | P0 |
| [P12](assignments/P12.md) | Finish effect reconciliation and durable callback ownership | `internal/effects` | P02 | P0 |
| [P13](assignments/P13.md) | Finish Responses-to-runtime integration contract | `internal/adapters/responses` | P02 | P0 |
| [P14](assignments/P14.md) | Implement durable worker turns and automatic hosted claims | `internal/execution` | P03, P05, P08, P10, P11, P02 | P0 |
| [P15](assignments/P15.md) | Build and publish the actual model context | `internal/execution` | P14, P06, P07, P09, P13, P02 | P0 |
| [P16](assignments/P16.md) | Interpret model responses into bounded governed work | `internal/execution` | P15, P04, P12, P02 | P0 |
| [P17](assignments/P17.md) | Complete responsibility reasoning and event wakes | `internal/scheduling` | P14, P11, P02 | P0 |
| [P18](assignments/P18.md) | Drive independent verification and bind real output evidence | `internal/execution` | P16, P09, P11, P02 | P0 |
| [P19](assignments/P19.md) | Run sealed skill evaluations to completion | `internal/skills` | P18, P02 | P0 |
| [P20](assignments/P20.md) | Implement controlled repository verification | `internal/execution` | P18, P02 | P1 |
| [P21](assignments/P21.md) | Complete cooperative recovery and run export jobs | `internal/execution` | P18, P02, P20 | P1 |
| [P22](assignments/P22.md) | Drive turns, contexts and model/tool work in the controller | `internal/controller` | P16, P17, P02 | P0 |
| [P23](assignments/P23.md) | Attach verifier, local jobs and reconciliation driver | `internal/controller` | P22, P18, P19, P21, P12, P02 | P0 |
| [P24](assignments/P24.md) | Wire the implemented runtime and secure setup helper | `cmd/zatiti` | P23, P06, P13, P02 | P0 |
| [P25](assignments/P25.md) | Make first-use guidance reach actual useful work | `internal/installation` | P24, P02 | P0 |
| [P26](assignments/P26.md) | Finish memory projections and governed context integration | `internal/memory` | P02 | P1 |
| [P27](assignments/P27.md) | Implement the missing qualified Serenity service seam | `internal/adapters/serenity` | P26, P02 | P1 |
| [P28](assignments/P28.md) | Complete memory lifecycle against the qualified adapter | `internal/memory` | P27, P16, P02, P26 | P1 |
| [P29](assignments/P29.md) | Add safe storage snapshot and offline restore mechanics | `internal/storage` | P02 | P1 |
| [P30](assignments/P30.md) | Support portable encrypted backup custody and blob restore | `internal/platform` | P02 | P1 |
| [P31](assignments/P31.md) | Build complete backup bundles and monotonic recovery overlays | `internal/installation` | P25, P29, P30, P09, P02 | P1 |
| [P32](assignments/P32.md) | Coordinate real restore outside the running database | `internal/controller` | P23, P31, P02 | P1 |
| [P33](assignments/P33.md) | Wire restore lifecycle and recovery startup | `cmd/zatiti` | P24, P32, P02 | P1 |
| [P34](assignments/P34.md) | Expose complete receipts, event tails and audit projections | `internal/evidence` | P02 | P0 |
| [P35](assignments/P35.md) | Close review lifecycle for live worker proposals | `internal/reviews` | P07, P12, P02 | P1 |
| [P36](assignments/P36.md) | Drive earned-autonomy evidence updates | `internal/policy` | P07, P18, P34, P02 | P1 |
| [P37](assignments/P37.md) | Regenerate complete capabilities and transport catalogs | `internal/registry` | P02 | P0 |
| [P38](assignments/P38.md) | Carry new operation and event recovery semantics in the Go client | `internal/client` | P37, P34, P02 | P1 |
| [P39](assignments/P39.md) | Expose complete CLI work journeys | `internal/cli` | P37, P38, P02 | P1 |
| [P40](assignments/P40.md) | Expose complete MCP work journeys | `internal/mcp` | P37, P38, P02 | P1 |
| [P41](assignments/P41.md) | Verify transport admission and new runtime lifecycle boundaries | `internal/server` | P37, P02 | P1 |
| [P42](assignments/P42.md) | Make desktop chat durable and connected to actual workers | `apps/desktop` | P10, P24, P34, P37, P02 | P0 |
| [P43](assignments/P43.md) | Finish desktop organization and work management flows | `apps/desktop` | P42, P17, P25, P35, P02 | P1 |
| [P44](assignments/P44.md) | Finish desktop memory, files, routines and capability details | `apps/desktop` | P43, P28, P36, P21, P02 | P1 |
| [P45](assignments/P45.md) | Produce usable release and installation tooling | `packaging` | P24, P33, P43, P27, P02 | P1 |
| [P46](assignments/P46.md) | Prove vertical journeys and all-operation behavior | `tests/integration` | P25, P18, P12, P17, P19, P21, P35, P37, P39, P40, P41, P02, P20, P28, P33, P36 | P0 |
| [P47](assignments/P47.md) | Replace unconditional qualification skips with executable harnesses | `tests/qualification` | P46, P20, P28, P33, P44, P45, P02 | P1 |
| [P48](assignments/P48.md) | Enforce full runtime and release gates in CI | `.github/workflows` | P46, P47, P02 | P1 |
| [P49](assignments/P49.md) | Reconcile documentation and close the delivery matrix | `documentation` | P48, P02 | P1 |

## Earliest integration waves

Waves are a dependency aid, not permission to run all builds at once. Start only ready cards whose owner root is idle.

| Wave | Cards ready after earlier waves |
|---|---|
| 0 | P00 |
| 1 | P01 |
| 2 | P02 |
| 3 | P03, P05, P06, P07, P08, P09, P10, P11, P12, P13, P26, P29, P30, P34, P37 |
| 4 | P04, P14, P27, P35, P38, P41 |
| 5 | P15, P17, P39, P40 |
| 6 | P16 |
| 7 | P18, P22, P28 |
| 8 | P19, P20, P36 |
| 9 | P21 |
| 10 | P23 |
| 11 | P24 |
| 12 | P25, P42 |
| 13 | P31, P43 |
| 14 | P32, P44 |
| 15 | P33 |
| 16 | P45, P46 |
| 17 | P47 |
| 18 | P48 |
| 19 | P49 |

## Completion and evidence

Every original requirement/case remains binding. The coverage ledger’s empty test_ids fields are deliberate: they must be filled with **executed tests and evidence**, not inferred from a matching comment or a passing package. Implementers must preserve the exact original setup/action/expected observations in docs/implementation/acceptance.json. Add current source/profile/environment and case verdicts at landing.

For each public operation, P46 needs a legal positive lifecycle and applicable denied/stale/replay/recovery behavior through both CLI and MCP; listing all 197 public names is insufficient. For long jobs, inspect terminal outcome and read actual artifact bytes. For work, inspect the trusted verifier result. For restore, verify restored data after reopen. For desktop, launch the built application. A provider’s inability to guarantee a hard bound blocks the corresponding enforced mode; it does not authorize an undocumented substitute.

No new feature beyond the specified product is required: email adapters, hosted multitenancy, mobile, arbitrary external harness launchers and a new dashboard remain out of scope. Windows source is present but is not a qualified release claim. GitHub/public-HTTP adapters have substantial implementations and do not need speculative rewrites; their actual runtime integration is covered by P12/P16/P20/P46/P47.

This audit ran the Go baseline successfully (34 tested packages, 2,612 passing test/subtest events and 14 skipped qualification test/subtest events) and renderer check successfully. It did not run real providers, paid clients, Flutter or clean-host installation. Those limitations are explicit in the plan rather than counted as finished work.
