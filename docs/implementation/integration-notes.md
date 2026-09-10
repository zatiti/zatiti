# Integration notes

Findings from per-package integration that are not package-local concerns.
Append-only; newest entries last. Each entry names the package and the wave
that surfaced it.

## internal/configuration (wave 1)

- **contract.Bind gap.** The configuration AGENTS.md prose references
  `contract.Bind`, but `internal/contract` does not implement it, and
  `canonicalizeSets` is unexported in `internal/contract/canonical.go`.
  Neither blocked the package. Downstream packages that hit the same prose
  should not invent the symbol; raise it to integration before inventing.
- **Unbootstrapped-install import.** Import into an unbootstrapped
  installation surfaces as a raw SQLite CHECK error (`base_revision >= 1`)
  instead of a domain fault. Bootstrap is the sole uninitialized special
  case, so no contract path reaches it, and package tests bootstrap the
  destination. A clean fault would be better; left as a robustness note.
- **Z17.inherited_denial ownership.** Inherited deny-wins is a policy-engine
  computation. Configuration covers its own side (authority preview under
  pre-apply state, requirements sealed verbatim); the policy package owns
  the inherited-deny test surface.

## Wave-1 process facts

- In-process subagents do not appear as separate OS processes; `ps` scans
  for `--agent-id` do not detect them, and a "dead" agent may still wake on
  a later message. Two agents (original + resume) converged on the
  configuration worktree from one shared tree; the landed commit contained
  both agents' fixes and tests, verified against main.
- The build-lease claim script dedupes by holder; a holder whose session
  re-claims under a fresh token must release with that fresh SHA, or the
  claim expires by TTL only.
