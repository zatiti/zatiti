# Roadmap

RFC implementation status (docs/rfc.md). Integration lead: the `zatiti`
session; packages land only through its gate (build, vet, lint, stub scan,
tests, race under lease, mutation red→green, rebase, ff-merge).

## Shipped

- 2026-09-10 — wave 1 complete: contract, storage, platform, client,
  identity, registry, application, configuration (all merged to main;
  module-wide `go test ./...` green at configuration landing).
- 2026-09-10 — registry re-landed in wave 2 (f5657e8), application
  (ab71c8b), configuration (1488db3), skills (169e5be), tasks (8cda814),
  reviews (b1516e2), policy (4051f67). Integration notes:
  docs/implementation/integration-notes.md.
- 2026-09-11 — scheduling landed (3e6f665) and connections landed
  (90f676a): both races green under lease, integration mutations proved
  the scope fences load-bearing (scheduling scopeContains org dimension;
  connections scopeCovers org dimension), module-wide 14-package verify
  green post-merge, worktrees removed.
- 2026-09-11 — messaging landed (f09bbe9): integration mutation proved
  the conversation.create caller-participation fence load-bearing; lane
  agent's mutation covered the mailbox.ack principal fence; 46 behavioral
  tests; two suite-forced production fixes (event scope stamping from the
  unit, fresh-delivery recipient hydration). Worktree removed.
- 2026-09-11 — accounting landed (bc7f58d): wave 2 is 16 of 37 packages.
  Integration mutation proved the settle terminal-replay fence
  load-bearing (a settled reservation with a different usage report would
  otherwise replay as if identical — history rewrite). Module-wide
  16-package verify green post-merge.
- 2026-09-11 — effects landed (dd200cd), 17 of 37. Both wave-2 tail lanes
  had stalled silently (agents idle 11h, zero processes, free lease);
  the lead took over. Gate finding: operation.get's org-dimension fence
  (handlers.go:1204) had no test coverage — the missing
  TestOperationGetOrgDimension was added and then proved the fence
  load-bearing by mutation (neutralized fence lets a foreign-org request
  read another org's operation: expected not_found, got completed).
  Module-wide 17-package verify green post-merge.
- 2026-09-11 — execution landed (c80504d), 18 of 37. The stalled lane's
  tree did not even vet (undefined f in a collision-reconciled test) and
  two suite failures surfaced two real production fixes on landing:
  fenceAttempt now returns a stale-running task to waiting with the fence
  reason (a fenced lease previously deadlocked generation restart — the
  task state could never clear), and attempt generations count prior
  attempts (maxAttemptGeneration) instead of hardcoding 1. Integration
  mutation proved the checkWorkerCall state fence load-bearing (a
  cancelled attempt's heartbeat would otherwise extend its lease and
  revive dead work: expected conflict, got completed); the worker-identity
  dimension is defense-in-depth (narrowScope + checkWorkerCall both guard
  it, single-fence mutation is green by design). Independence fence test
  corrected to the actual layered refusal (bind rejects a
  non-independent result at /result/independent with invalid_input before
  the handler's verification_failed fence can fire). Race 221s green;
  module-wide 18-package verify green post-merge.

## In progress
- 2026-09-11 — wave-2 tail (9 packages): memory, artifacts, evidence,
  installation, server, cli, mcp, adapters/github, adapters/httpread
  dispatching staggered (two-heavy-lane cap).

## Planned

- Wave 3: controller, desktop, cmd/zatiti, cmd/zatiti-desktop.
- Wave 4: tests/integration, tests/qualification, packaging,
  .github/workflows.

## Blocked

- 2026-09-10 — internal/adapters/responses and internal/adapters/serenity:
  external endpoints not resolved per docs/implementation/dependencies.lock.json.
- 2026-09-10 — OpenRouter key 403 resolved: the earlier weekly-limit deaths
  did not recur; the two resume lanes (connections, accounting) completed
  full cycles past the previous death window, and fresh dispatches
  (messaging) are running. No top-up needed unless a fresh 403 appears.
