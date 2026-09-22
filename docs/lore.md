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

**FIXED, 2026-09-20 (P30, PR #19, 4645b41).** Both named failures were
root-caused with filesystem fixtures before any implementation change,
per the card's own instruction, and the fix is landed and verified (5x
repeat run clean on both tests; the original card's own instruction asked
for a Linux+macOS repro, this session only had macOS available).

`TestListenPrivateRefusesSymlinkedRunDirectory` was a REAL implementation
bug: `internal/platform/socket.go`'s `ListenPrivate` called
`filepath.EvalSymlinks` on the run directory itself before the strict
no-symlink check (`mkdirPrivate`, which uses `os.Lstat` + an explicit
`ModeSymlink` check) ever saw it -- silently resolving away a planted (or
otherwise unexpected) symlink instead of refusing it. Fixed by resolving
symlinks only in the directory's ancestors, never the directory itself,
so `mkdirPrivate` sees the real, unresolved leaf path. It passed locally
in this session only by coincidence: `t.TempDir()` on this machine
produces paths that push the resulting socket path over the 104-byte
`socketPathMax` guard, so the test failed for an unrelated reason
(path-too-long) before ever reaching the vulnerable code -- confirmed by
direct measurement, not assumed.

`TestBlobTamperedObjectFailsPublishOverExisting` was NOT a real
crypto/implementation defect -- confirmed by an exhaustive XOR-flip probe
across all 219 bytes of a published object (zero undetected corruptions)
and a 3000-iteration mechanistic check showing the count of "pre-existing
byte already equals the fixed tamper value" coincidences (12) exactly
matched the count of false accepts (12). The GCM tamper-detection logic
was always sound; the TEST FIXTURE tampered by overwriting a byte inside
a per-chunk random AES-GCM nonce with a fixed value, which had a ~1/256
chance of being a no-op. Fixed by XOR-flipping the existing byte instead
(guarantees an actual change), applied to this test and two siblings with
the identical latent flaw; the security assertion itself is unchanged
(and the test gained a new assertion that the object wasn't consumed by
a refused republish).

Original correction below (2026-09-20, superseded by this entry) is kept
for the mistake it documents: passing locally is weak evidence against a
CI-cited, audit-documented defect with a specific repro. Two commits in
this session (P07, P08) had retried past an `internal/platform` failure
without investigating -- worth knowing that pattern happened even though
this specific incident turned out to be the (also real) fixture-flake
case for the tamper test and unrelated to what those two commits hit.

Original (WRONG) entry, 2026-09-19: "the same CI run showed
`internal/platform.TestListenPrivateRefusesSymlinkedRunDirectory` failing.
Verified 2026-09-19: this test passes clean against baseline `main`
(pre-P00) run locally, and a later full untruncated `go test ./...` run
didn't reproduce it either -- it's a flake ... not a P00 regression."
This reasoning was too hasty: passing locally is weak evidence against a
CI-run-cited, audit-documented defect with a specific repro command in a
real card. Two commits in this session (P07, P08 -- see docs/roadmap.md)
retried past an `internal/platform` failure assuming it was this same
"flake" without re-reading P30's card first; neither retry investigated
whether the retry was masking the real defect versus hitting unrelated
noise. Worth re-checking those retries' actual failure output against
P30's two named tests specifically once P30 lands.

**The local pre-commit hook (`hooks/pre-commit`, installed to
`.git/hooks/pre-commit`) now tolerates this.** As originally written it ran
full-module `go test ./...` and hard-blocked ANY commit on ANY package
failure, regardless of what the commit actually touched -- which meant once
P00 landed, no one could commit anything at all until every package above
went green. Fixed 2026-09-19 (bd0e1a9): a failing package is only allowed
through if it's listed in `docs/implementation-remediation/expected-red.txt`;
anything else (a genuinely new regression, or a build/compile failure with
no attributable package) still blocks unconditionally. The allowlist
currently holds exactly the 13 structural packages above (not
internal/platform). **When a card lands and its package goes green, remove
that package from expected-red.txt in the same commit** -- the list must
shrink to empty by M1, not calcify into a permanent exception.

## [implementation-remediation] R-build-lease is advisory only -- the pre-commit hook does NOT enforce it, 2026-09-19

**The hook does not check who holds R-build-lease before running its full
`go test -p 2 -timeout 30m ./...`.** The lease is a pure convention: every
agent is TOLD to acquire it before a heavy multi-package command, but
nothing stops `git commit` from triggering the hook's full-module test
regardless of lease state. Confirmed live during wave 3's first 8-lane
dispatch: P37 held the lease and was correctly running its commit-time test,
while P10 (resumed with "acquire the lease and proceed") committed
concurrently without actually holding it, and P09 nearly became a third
concurrent full-module run before self-aborting (killed its own hook
process and lease-retry loop on noticing load 115-165). Two simultaneous
full-module test runs (each spawning the real controller binary, tests/
integration, tests/qualification, etc.) drove 1-minute load from ~26 to
150+ in under 10 minutes -- matching, and nearly exceeding, the exact
failure threshold docs/roadmap.md's "Parallel dispatch protocol" section
documents from the 2026-09-18 incident (10 lanes exhausted the session
limit at load ~130). Neither P37's nor P10's commit actually landed --
both attempts failed or were interrupted under the load spike, leaving
real uncommitted work in both worktrees (recovered afterward, nothing
lost).

**A second, independent failure mode compounded this**: multiple agents,
once done coding, stopped their turn to "wait for load to drop" or claimed
"a background monitor is watching," but had no actual live background
watcher -- each such stop generates a task-notification, and if resumed
naively (or if the agent resumes itself), it re-polls in a tight ~30-90s
loop, each poll re-paying that agent's full accumulated context cost
(300-400K+ tokens per poll observed). This is expensive even when harmless,
and outright dangerous when the poll itself involves spawning a "retry
lease" background loop that starts extra work.

**Until the hook is fixed to actually enforce the lease (or another
mechanism does), the safe protocol for a lead dispatching multiple
concurrent lanes is: serialize the commit step yourself.** Let every lane
finish its coding and package-scoped tests in parallel (cheap, safe,
what wave-based dispatch is for), but once a lane reports "ready to
commit," explicitly clear ONE lane at a time by name, wait for its actual
landing (PR open or a real, understood failure) before clearing the next.
Never resume multiple stalled/waiting agents in the same message with an
instruction like "proceed regardless of load" -- that is exactly what
caused this incident. Never let an agent's own "I'll wait for a monitor"
report go unaddressed for more than one tick; either it has genuinely
stopped (resume it explicitly, one at a time) or it's about to spawn a
wasteful poll loop (tell it to stop and wait for you by name instead).

## [implementation-remediation] expected-red.txt removal must happen AFTER the merge lands on main, never before, 2026-09-20

**The allowlist and the pre-commit hook that reads it are both
main-checkout-relative, not branch-relative.** While landing P14, removed
`internal/execution` from `docs/implementation-remediation/expected-red.txt`
in a standalone commit (3fbdd37) directly on `main` while P14's own fix was
still sitting unmerged on its PR branch. Main's actual `internal/execution`
package still failed its tests at that moment (the revision-3 descriptors
genuinely weren't implemented there yet) -- removing the allowlist entry
early meant the hook's full-module `go test ./...` now saw that failure as
unattributable, and the very next commit on main (an unrelated
`internal/tasks` gofmt fix) was blocked by it.

Caught within one commit cycle because the blocked commit's hook output was
read in full rather than assumed-passed from a background task's exit
code. Fixed by restoring the entry (b56d82d) and only removing it again
(440cc2c) after PR #25 actually merged and `go test ./internal/execution`
was independently re-run against the real post-merge `main` to confirm
green.

**The rule going forward:** an allowlist removal commit is the LAST step
of landing a card, made against `main` only after that card's own PR has
actually merged there -- never staged ahead of the merge, even when the
merge is expected imminently and even when the removal commit and the
card's own PR are for the same package. If a package's fix and its
allowlist removal must be two separate commits (the fix is confined to its
own write-root; the allowlist file is outside every card's write-root),
land the fix first, confirm it green on the real post-merge main, then
remove the line.

## [implementation-remediation] "Report ready, wait for clearance" instructions get read as "poll in a background loop," 2026-09-20

Recurring, not a one-off: across wave-3's last 5 cards and wave 4's first
batch, multiple different dispatched agents (P08, P29, P35 confirmed;
likely others) -- given the explicit instruction "report ready and WAIT
for the lead's clearance... do not self-poll the lease/load in a loop,
report once and wait" -- still started their own background bash loop
checking `uptime`/`R-build-lease` every 15-30s, sometimes for up to 10
minutes, even AFTER their own commit had already succeeded. The
instruction's "wait" is apparently read as "keep checking a condition
locally" rather than "end your turn and let the lead resume you" -- an
LLM default that plain prose reinforcement doesn't reliably override.

Mitigation that actually works, used throughout this session: the LEAD
never trusts "I'm waiting for X" at face value. On every such report,
check the agent's worktree directly (`git log`/`status`, live `ps`
processes) before doing anything else -- more than once the real state
was "already succeeded, agent just hasn't noticed and is now burning
tokens re-checking a finished condition." If a poll loop is found, tell
the agent explicitly to kill it and stop, then the lead takes over the
remaining mechanical steps (push/PR) itself rather than re-explaining and
hoping the next round doesn't repeat the pattern. Do not expect a
stronger prose instruction to fix this on the next dispatch -- verify
instead of asking nicely again.

## Killing a pre-commit-hook subprocess leaks its R-build-lease

The pre-commit hook wraps its `go test -p 2 -timeout 30m ./...` run with
its own R-build-lease claim/release. If you `kill` the underlying `go
test` process directly (e.g. to escape a genuinely wedged run rather than
wait out its full timeout), the hook script itself gets interrupted mid-
flight and never reaches its own release step -- the lease stays held by
your session indefinitely, silently blocking every subsequent claimant
(including your own later dispatches) until something notices.

Found 2026-09-22: killed a hung integration-test process during a
golden-count-fix investigation; ~2.5 hours later a freshly dispatched P01
agent reported stuck "waiting for lease acquisition" with no explanation
of why. `~/.claude/skills/claim/scripts/claim.sh release R-build-lease`
immediately unblocked it. Diagnosed by checking `refs/claims/*` directly
(`git fetch origin "+refs/claims/*:refs/remotes/origin-claims/*"` then
`git for-each-ref`) rather than guessing -- the claim's own committer
timestamp made the staleness obvious once looked at.

Mitigation: after killing any hook-invoked subprocess, explicitly check
for and release any lease that hook manages, in the same turn -- don't
assume the hook's own cleanup ran just because the shell command
returned. When an agent reports "waiting for lease/lock acquisition" and
you don't have an immediate other explanation, check `refs/claims/*`
directly before assuming it's a normal, temporary wait.

## Never carry the "landed cards" set forward by memory across a compaction

A dependency-closure check ("which cards are dispatch-ready?") is only as
good as its landed-set input. This session carried that set forward
conversationally across a context compaction and it silently drifted --
missing P01, P02, P08, P09, P23, P25 by the time it mattered (2026-09-22),
each landed days or hours earlier but never re-added to the list being
checked against. The check itself (comparing plan.json's depends_on
against the set) was correct; the input was stale.

Cost: wave 13 (P31, P43) was reported "blocked" three separate times when
it had actually been dependency-ready since 2026-09-19 -- roughly 2 hours
of lost 2-way parallel dispatch opportunity under an explicit
max-parallelization mandate, plus one fully redundant card dispatch (P01,
re-verified already-shipped work instead of doing anything new).

Mitigation: re-derive the landed set from source EVERY time a dependency-
closure check matters for a real dispatch decision, never from memory or
a prior message in the conversation. `grep -n "LANDED" docs/roadmap.md`
is a reasonable start but is NOT sufficient alone -- phrasing varies
("P23 independently reviewed and opened as PR #39... claim released" has
no literal "P23 LANDED" substring). Cross-check every ID systematically
(a small script iterating P00-P49 against multiple regex patterns), and
for any ID the regex can't confirm, search its bare mention manually and
verify the cited commit SHA is a real ancestor of origin/main via
`git merge-base --is-ancestor <sha> origin/main` before trusting it either
way -- both false negatives (this incident) and false positives (trusting
a claimed SHA that was never actually merged) are real risks.
