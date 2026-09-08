# First-release use cases

All cases are PLANNED and priority P0 for the RFC's complete first release. They are product outcomes, not claims of implementation. Test names and executable probes are assigned as each epic expands. Tests must exercise real product boundaries; controlled provider fixtures are permitted where the scenario requires deterministic failures.

| ID | User outcome | Required negative/recovery case | RFC gates | Epic |
|---|---|---|---|---|
| UC-001 | Initialize installation and securely connect to personal chief | Rebootstrap, revoked credential, unavailable secure store | Z01, Z13, Z21 | E1, E2 |
| UC-002 | Coding agent discovers and operates every authorized capability through CLI/MCP | Denial parity, lost acknowledgment, stale cursor | Z02, Z03, Z16 | E1, E7 |
| UC-003 | Create child organizations/chiefs and manage hierarchy | Cycle, unauthorized reparenting, duplicate chief, chief replacement | Z04, Z17 | E2 |
| UC-004 | Create/equip workers and activate exact configuration | Unsafe archive, stale plan, changed dependency, self-grant | Z03-Z05, Z15 | E2 |
| UC-005 | Delegate bounded work and receive independently verified artifacts | Missing output, tampered verifier, false success report | Z10-Z12 | E3 |
| UC-006 | Use an external worker with checkpoint and continuation | Stale heartbeat, unsupported capability, conflicting replacement | Z10, Z13, Z16 | E3 |
| UC-007 | Entrust ongoing scheduled and reasoning-driven responsibilities | Duplicate wake, unbounded quiet spend, paused wake | Z09, Z10, Z20 | E5 |
| UC-008 | Connect providers without leaking credentials | Account substitution, unverified binding, consent missing | Z05, Z07, Z13 | E2 |
| UC-009 | Review exact consequential actions in desktop | Changed content/head, expired decision, agent posing as human | Z06, Z07, Z21 | E3, E6 |
| UC-010 | Govern and recover external effects under shared budgets | Success with lost reply, SDK retry, unknown charge, child overspend | Z06, Z08, Z09 | E3 |
| UC-011 | Chiefs and workers coordinate without conflicting assignments | Duplicate mail, changed task version, lost recipient acknowledgment | Z10, Z12, Z20 | E5 |
| UC-012 | Recall worker/org/installation knowledge with sources | Unauthorized brain query, stale knowledge, lost memory acknowledgment | Z13, Z16, Z18 | E4, E6 |
| UC-013 | Chiefs curate shared knowledge automatically | Unauthorized promotion, repeated provenance, source correction/retraction | Z18 | E4 |
| UC-014 | Earn capability-specific autonomy under authorized rules | Self-promotion, irrelevant evidence, changed tool/model, failed qualification | Z07, Z19 | E5 |
| UC-015 | Pause, back up, restore and reconcile installation | Missing artifact, pending dispatch, revoked credential restored, memory revision mismatch | Z01, Z14, Z16 | E1, E6 |
| UC-016 | Use chats to create responsibilities, inspect results and handle decisions | Offline draft, stale card, ambiguous worker organization, UI claiming uncommitted creation | Z03, Z17, Z21 | E2, E6 |

## Release journeys

J1: desktop personal chief -> Marketing and Engineering chiefs -> equipped worker -> bounded task -> verified artifact -> useful chief report.

J2: cooperative worker -> repository task -> checkpoint -> independent patch verification -> exact GitHub action -> changed-head refusal and successful authorized path.

J3: hosted research worker -> bounded sources -> cited brief/draft -> scoped memory lesson -> chief-curated promotion with provenance.

J4: ongoing responsibility -> scheduled wake and repeated reasoning cycle -> delegated work -> durable message -> pause/restart -> no duplicate effect or unbounded spend.

J5: desktop Needs you -> exact approval -> disconnect -> reconnect -> durable disposition; then backup/restore starts paused with unresolved effect and memory obligations preserved.

Run J1-J3 through CLI and MCP as required by the RFC and J1/J5 through the desktop, with cross-interface interruptions. J4 and all negative cases qualify the additional responsibility/autonomy/memory invariants. A scenario's expected completion is a retained observable artifact or durable state, not an agent's assertion.
