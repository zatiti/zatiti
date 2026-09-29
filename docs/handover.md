# Handover — 2026-09-28T07:20Z, session zatiti (bd5dee) — browser-extension plan /ship+/apply

## TL;DR
Planned the Zatiti Chrome extension (plan v3, 66 tasks, 14 waves, ADRs 003–007)
and ran /apply: wave 1 shipped the extension scaffold, wave 2 shipped the page
command executor, then /apply drained — every remaining task chains off two
kind:human coordination-gate issues that were never answered (4 multiple-choice
asks, all timed out). Single next action: create those two coordination-gate
issues (draft-and-approve recommended), then re-run /apply.

## Done & VERIFIED
- Plan v3 rendered and pushed: docs/plan.md + docs/plans/*.md + ADRs 003–007
  (commit e65075d; pushed again after rebase as part of 5fe4851 chain). Renderer
  sanity clean: 66 tasks, all 14 fields, zero dangling deps, waves ≤8, deps in
  strictly-earlier waves.
- Ship gate PASSED via AskUserQuestion: Proceed / revision 21 first (tool-gap
  epic E0T) / minimal ferro v0.2.0. Decisions (e) and (f) are FINAL.
- T8.1 (extension scaffold: pinned id, loopback-only CSP, vendored ferro
  placeholders + sha256 MANIFEST) — PR #81 merged a5213b6, CI 9/9, acc verified
  in-worktree (15 node tests).
- T8.2 (page command executor: CDP hit-test click fail-closed, focus-verified
  key/fill, named-keys-only, sensitive refusal incl. reads, href/form-action
  identity) — PR #82 merged 9c9ffbd, CI 9/9, acc verified in-worktree (54 tests).
- Both claims (T8.1, T8.2) released; both worktrees torn down; roadmap updated
  at each step; ajent.social entries appended.

## Done but UNVERIFIED
- Nothing. All shipped work was observed merged (gh pr view) with green CI
  (gh pr checks) and locally-run acceptance commands.

## In flight
- None. /apply drained cleanly. No branches, no WIP.

## Blocked
- T0.1 (coordination gate: 14 decisions — WebSocket hub ownership, revision
  order 21→22, tool slots c7–c9 reserved, desktop-file owner, ship-gate
  decisions recorded) — unblocks on: a GitHub issue on zatiti/zatiti (label
  browser-milestone) + one on sirerun/ferro (label zatiti-library). Owner: David.
- T0T.1 (coordination gate: 8 decisions for the tool-gap epic — responses/
  controller owners, messaging owner, billed live-verification runner) —
  unblocks on: one GitHub issue on zatiti/zatiti (label tool-gap). Owner: David.
- Downstream: all wave-2 remainder (T0T.2 spec revision 21, T2.4a dependency
  lock, T1.2a ferro page package, T2.1 spec ADRs, T0T.9 relay protocol types)
  and wave 3 chain from these two issues.

## Running processes left alive
- None. No kazi runs, no background tasks.

## Landmines & context
- The repo opted out of kazi by design (docs/roadmap.md:3084) — engineering
  tasks here take the subagent lane, not the kazi lane, despite kazi being on
  PATH.
- This repo's own planning pipeline lives OUTSIDE the repo: the finalized plan
  JSON + renderer are in
  ~/.claude/projects/-Users-dndungu-Code-zatiti-zatiti/a2c94f91-b311-4e2c-9277-c2b627953719/finalize/
  (plan-final2.json is the source of truth; render_plan.py renders it). The
  repo's docs/plans/*.md are RENDER OUTPUT — do not hand-edit them to change
  the plan; edit the JSON and re-render, or the next re-render clobbers you.
- After editing plan-final2.json (e.g. marking tasks done), re-run render + the
  [x] markers land in docs/. The previous session marked T8.1/T8.2 [x] by
  hand-editing both the JSON-side and the rendered side — next renderer run
  must start from the updated JSON.
- NEVER commit the primary checkout's untracked files: .ox-recovery-current,
  .ox-recovery-wbBwJcGZ/, codex.sh, index.html, logo.svg — they belong to
  another session.
- My session repeatedly got auto-switched INTO subagent worktrees (EnterWorktree
  side-effect); coordinator-side commits must wait until the subagent finishes,
  then ExitWorktree (keep) + remove the worktree.
- ajent.social carries an UNRELATED warning from another session about a local
  unmerged candidate (mcp-probe evidence schema mismatch vs revision-4 adapter
  contract) — do not conflate it with this plan; it belongs to another effort.
- The ferro handoff doc (personal/business content) must never enter git.

## How to resume
1. git fetch origin && git checkout main && git pull --rebase origin main
   (handover branch == main; no divergence expected).
2. Read this file, docs/roadmap.md ("In progress" top entry), docs/plan.md §1,
   and .claude-checkpoint.md (repo root, local untracked).
3. Resolve the two coordination gates (see Blocked above).
4. Re-run /apply (pool mode is the default). Wave 2's remaining five tasks
   claim via the normal pool flow. Local-mac tasks (T3.4, T9.2, T8.6,
   T10.3) need the shared Mac-mini build lease
   (CLAIM_REMOTE=/Users/Shared/mini-build-lease.git
   ~/.agents/skills/claim/scripts/claim.sh claim R-build-lease ...).
5. The plan JSON lives in the finalize dir (see Landmines); update it when
   tasks complete, then re-render if you want docs/plans/ to reflect it.
