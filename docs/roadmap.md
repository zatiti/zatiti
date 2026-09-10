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
  reviews (b1516e2). Integration notes: docs/implementation/integration-notes.md.

## In progress

- 2026-09-10 — policy: resume agent finishing 13 uncommitted files
  (autonomy.go was the last production file); worktree zatiti-wt-policy.
- 2026-09-10 — connections: resume agent finishing 20 uncommitted files;
  worktree zatiti-wt-connections.
- 2026-09-10 — accounting: 23 uncommitted files, mid-verification; resume
  agent pending. Worktree zatiti-wt-accounting.

## Planned

- Wave 2 top-up (staggered as slots free): scheduling, messaging,
  execution, effects, memory, artifacts, evidence, installation, server,
  cli, mcp, adapters/github, adapters/httpread.
- Wave 3: controller, desktop, cmd/zatiti, cmd/zatiti-desktop.
- Wave 4: tests/integration, tests/qualification, packaging,
  .github/workflows.

## Blocked

- 2026-09-10 — internal/adapters/responses and internal/adapters/serenity:
  external endpoints not resolved per docs/implementation/dependencies.lock.json.
- 2026-09-10 — OpenRouter key hit its weekly limit (403 "Key limit
  exceeded"), killing three wave-2 agents mid-lane. Respawn capacity under
  test with two resume agents; if they die with the same 403, the key needs
  a top-up before more dispatches.
