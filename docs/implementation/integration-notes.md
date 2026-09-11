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
  the inherited-deny test surface. Discharged on the policy landing:
  `TestCheckDenyGrantPrecision` covers deny-over-allow (same-scope deny
  wins, wildcard deny, foreign-scope deny inert); the integration
  mutation proved the scope-precision fence load-bearing (a foreign-org
  deny leaked into an unrelated request when `denyApplies` was bypassed).

## Wave-1 process facts

- In-process subagents do not appear as separate OS processes; `ps` scans
  for `--agent-id` do not detect them, and a "dead" agent may still wake on
  a later message. Two agents (original + resume) converged on the
  configuration worktree from one shared tree; the landed commit contained
  both agents' fixes and tests, verified against main.
- The build-lease claim script dedupes by holder; a holder whose session
  re-claims under a fresh token must release with that fresh SHA, or the
  claim expires by TTL only.
- zsh pipeline gotcha (agent lease scripts): `status` is a read-only
  special variable — `status=$?` aborts the script right after a lease
  claim, orphaning it until TTL or manual release. Use `rc=$?`. A lease
  pipeline must release unconditionally after the run (success or fail),
  and the claim must sit one step before the command it protects.

## internal/tasks (wave 2)

- **Evidence comes from the task's evidence rows, not the transition
  payload.** `_tasks.transition` ignores wire `evidence_ids` for
  structural success evaluation; the digest fence scans the artifacts
  registered during the run. Fence tests must register artifacts through
  the run helpers (`runToVerifying`), not through the transition payload.
- **Manual label red needs the empty-payload nuance.** Removing the
  `if manual` fence in `evaluateSuccess` still fails the digest fence
  when evidence is empty, so a mutation red requires the success-path
  fixture (pinned-digest artifact + verifier result registered), exactly
  as `TestFenceSuccessRequiresEvidence/a_manual_label_is_not_verification`
  sets it up.

## internal/reviews (wave 2)

- **Pre-commit hook runs module-wide `go test ./...` unleased.** A commit
  therefore needs the lease held across `git commit` so the hook's test
  pass honors the one-heavy-lane rule, and the commit output must be
  captured to a file — piping it through `tail` swallowed the failing
  test name when the hook first rejected the reviews commit.
- **Hook failure was transient.** One module-wide run failed with no
  visible test name; an immediate re-run passed. If it recurs, capture
  the full `go test ./...` output before concluding anything about the
  package under review.

## internal/connections (wave 2)

- **Applied event kind is `connections.connection.applied`**
  (handlers_internal.go:77), not `connections.applied`: storage's
  `validateEvent` requires the owner.entity.transition shape. Registry/apply
  integrations should subscribe to the three-segment kind.
- **Scope reachability is `scopeCovers`**
  (handlers_connection.go:448): installation must match, every
  explicitly-set request dimension (org/project/worker/task) must agree
  with the row, and unset dimensions constrain nothing. Integration
  mutation proved the dimension fence load-bearing: neutralizing the
  org check made `TestConnectionGetScopeRules/
  unrelated-scope-refuses-not-found` red (`expected not_found fault,
  got completed`) — a foreign-org request would otherwise read another
  org's connection. Fence returns not-found, deliberately (existence in
  a scope you cannot reach is not distinguishable).
- **Validation-freshness window is 24h** (`validationFreshness`,
  handlers_internal.go:315): a validation record older than that does not
  satisfy activation. Activation replay protection lives in the
  `connections_applied_plans` table keyed by `plan_id` (store.go:375) —
  an identical re-activate returns the recorded versions instead of
  re-applying.

## Wave-2 process facts (continued)

- **A "dead" lane agent can wake and re-enter its worktree.** The
  execution lane's original agent died on an upstream timeout, a resume
  agent was dispatched into the same worktree, and the original then woke
  from its idle state and wrote the same directory concurrently — the
  second convergence on one worktree this effort (after configuration in
  wave 1). Detection: one agent reporting "unidentified session" files in
  its own worktree. Resolution: stop the original, reconcile to the
  resume agent's spec-derived versions, preserve any real production
  fixes the original had already made. Never let two writers share a
  worktree — the mutation-proof byte-zero check and pre-commit run are
  both corrupted by concurrent edits.

## internal/execution (wave 2)

- **Fenced leases deadlocked generation restart.** `fenceAttempt` moved
  the run to waiting but left the task "running" forever (nothing else
  can clear it), so `run.claim`'s task fence refused every replacement
  attempt. Fixed on landing: `fenceAttempt` (controller_ops.go) now
  returns a stale-running task to waiting with the fence reason, mirroring
  its run transition; a task already moved to verifying or terminal by
  another path is left alone. Attempt generations count prior attempts
  (`maxAttemptGeneration`, run_ops.go) instead of hardcoding 1 — a
  replacement claim is a new generation and cannot revive the old one.
- **Worker-identity protection is defense-in-depth.** `narrowScope`'s
  worker dimension (scope.go:31) and `checkWorkerCall`'s bound-worker
  check (scope.go:107) both refuse a foreign worker on heartbeat/
  checkpoint/report; mutating either alone is green because the other
  holds. The load-bearing single fence for the no-revival guarantee is
  `checkWorkerCall`'s state fence (scope.go:101): neutralizing it let a
  cancelled attempt's heartbeat extend its lease and revive dead work
  (`TestAttemptHeartbeatOnStoppedAttempt`: expected conflict, got
  completed).
- **The independence fence is layered.** The wire schema pins
  `independent` as `const: true`, so a non-independent verification result
  is refused at bind (`invalid_input` naming `/result/independent`) before
  the handler's `verification_failed` fence can fire. The handler fence
  stays as defense-in-depth; tests must assert the bind refusal.
- **Untracked worktrees have no git revert.** A gate mutation in a
  not-yet-committed tree must be reverted by exact edit — `git checkout
  --` has nothing to revert to. Revert correctness is then verified by
  re-running the named test green plus statics, not by a diff count.
