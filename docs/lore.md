# Lore

Debugging gotchas, system invariants, and landmines. Append-only, topic-ordered,
greppable by tag. Grep this before debugging a surprising failure.

## [implementation-remediation] Expected red: catalog/descriptor-parity tests, 2026-09-19 to (tracked)

**Do not "fix" these tests by reverting or weakening them. Do not treat a fresh
failure of one of these specific tests as a new bug without checking whether
its owning remediation card has landed yet.**

P00 (PR #3, merged b6f2cc2, 2026-09-19) froze revision 3 of the shared
contract (docs/implementation/{contracts.md,requirements.json,acceptance.json,
adapter-schemas.json}, tools/specgen/{model,packages}.py) and regenerated every
package AGENTS.md. Its allowed writes were deliberately restricted to
tools/specgen and docs/implementation/ -- no Go source. That means the newly
declared operations (durable worker turns, `_execution.*`, `_effects.reconciliation.*`,
`_messaging.ready`/`.processed`, `conversation.message.list`, `memory.list`,
`installation.verifier.list`, etc.) exist in the frozen catalog with no Go
package implementing or registering them yet -- by design. Founder decision
(David, 2026-09-19, in-session): merge P00 immediately and track the red
rather than batching cards or holding a separate integration branch, since
this repo has no production deployment yet and the red state is the honest,
correct signal of "not yet implemented" that each downstream card is meant to
resolve -- weakening or skipping these tests to hide it is explicitly
forbidden by the plan ("skipped suites cannot close a card").

As of the P00 merge, CI's `build and test` matrix fails in these packages,
all on catalog/descriptor/schema-parity tests comparing committed Go-side
artifacts against the new revision-3 catalog:

- `internal/registry` (`TestCatalogMatchesFrozenContract`) -- fixed by **P37** (wave 3).
- `internal/identity`, `internal/configuration`, `internal/installation`,
  `internal/effects`, `internal/memory`, `internal/messaging`,
  `internal/adapters/responses` (`TestDescriptorsMatchFrozenCatalog` /
  `TestEmbeddedSchemasMatchTheFrozenCatalog`) -- fixed by their respective P0x
  wave-3 cards (P03, P05, P06, P08/P12, P09, P10, P13 -- check
  docs/implementation-remediation/README.md's assignment index for the exact
  current owner) as each lands.
- `internal/execution` (many missing descriptors) -- fixed by **P14** (wave 4+),
  not sooner; do not expect this one green until execution's own card lands.
- `internal/mcp` (`TestEmbeddedDefsMatchContract`) -- fixed by **P40** (wave 5+).

A package's parity test going green is a real, verifiable signal that card
landed correctly -- use it as a spot-check when reviewing that card's PR.

**Unrelated to P00**: the same CI run showed
`internal/platform.TestListenPrivateRefusesSymlinkedRunDirectory` failing.
Verified 2026-09-19: this test passes clean against baseline `main` (pre-P00)
run locally -- it's a flake (likely CI-runner symlink/TMPDIR environment
specific), not a P00 regression. If it recurs, investigate the CI environment,
not the P00 diff.
