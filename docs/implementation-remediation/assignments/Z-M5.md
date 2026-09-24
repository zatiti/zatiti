# Z-M5 — Accept or strip Claude-Code-only SKILL.md frontmatter keys; build a bulk importer

Status: planned. Priority: P2. Owner: `internal/skills` + `cmd/zatiti` (helper mechanics only, no new operation).

This is a coding assignment, not evidence that its behavior exists. Read
`internal/skills/AGENTS.md`, `internal/skills/frontmatter.go`,
`internal/skills/frontmatter_test.go` and `cmd/zatiti/AGENTS.md` before
editing. This card does not touch the frozen contract, does not add a
new operation, and does not create a new ownership root: `internal/skills`
gets private parsing changes only, and the bulk importer is `cmd/zatiti`
"local bootstrap/helper mechanics" (its own AGENTS.md's phrase) that calls
the existing public `skill.import` operation through the ordinary client —
never a new Cobra product command. `internal/cli`'s own AGENTS.md is
explicit that it "cannot register shadow product operations"; do not add a
hand-written subcommand there.

Allowed writes: `internal/skills` (parsing only — no schema/table change),
`cmd/zatiti` (a new helper entrypoint/flag, wired the same way `serve`/`mcp
serve`/`help`/`completion` already sit outside the generated command tree).

Context: `designs/2026-09-23-zatiti-total-migration-gaps.md` item 18 and
`designs/2026-09-23-marketer-org-on-zatiti.md` (dec-1255/dec-1256) — the
marketer's five-worker org needs her `~/.agents/skills` imported before
Z-M6 binds skills to workers. Dispatched by chief-developer under
dec-1065; not a founder-level scope question (confirmed with
master-chief/chief 2026-09-23).

## What is actually broken today

Confirmed by reading `internal/skills/frontmatter.go` and by running a real
count against `~/.agents/skills` (128 directories, this machine,
2026-09-23):

- `knownFrontmatterKey` (frontmatter.go:158-164) accepts exactly `name`,
  `description`, `license`, `allowed-tools`, `dependencies`. Any other key
  fails the whole parse with `"SKILL.md frontmatter key %q is not
  supported"` (line 101) — one unsupported key rejects the entire skill,
  there is no partial import.
- Of 128 skill directories under `~/.agents/skills`, 32 carry at least one
  of `argument-hint`, `disable-model-invocation` or `metadata` and fail
  import today: 18 carry `argument-hint` (a scalar string, e.g.
  `argument-hint: "[--fix] [--repo <path>]"`), 18 carry
  `disable-model-invocation` (a scalar, `true`/`false`), 6 carry `metadata`
  (a **nested YAML map**, not a scalar — e.g. canvas's SKILL.md has
  `metadata:` followed by `surfaces:` (itself a list) and `environments:`
  at one level of indent, sire's/launch's have `metadata:` followed by a
  single `version: 1.0.0`). These three counts overlap (a skill can carry
  more than one).
- The parser has exactly one block-shaped key today: `dependencies`
  (frontmatter.go:109-122), which only understands a flat list of `- item`
  lines directly under the key at the SAME indent level as `dependencies:`
  itself (checked via `strings.TrimSpace`, not real indent tracking — see
  line 114). `metadata`'s shape (an arbitrary-depth nested map) does not
  fit that one special case; a general one is not implemented anywhere in
  this file.
- No bulk-import path exists. `internal/skills/service.go:48` registers
  exactly one public operation, `skill.import` (CLI `skill import`), which
  imports one `SKILL.md` at a time from one source. Nothing in this repo
  loops it over a directory.

## The design

**argument-hint, disable-model-invocation — accept, discard.** These are
Claude-Code-specific extensions with no Zatiti execution-model
counterpart (Zatiti has no CLI argument-hint surface for a skill, and no
existing "disable automatic invocation" concept anywhere in
`internal/skills` or `internal/configuration`'s worker/skill binding —
confirm this by grep before writing, do not assume). Add both to
`knownFrontmatterKey` as scalar keys, add a third branch case in
`applyFrontmatterScalar` (or a shared no-op case) that validates the
value is present and within a bound (reuse `maxDescriptionLength`-class
sizing — do not leave them unbounded) and stores nothing. The
`frontmatter` struct gains no new field for either key: Zatiti's
`skills_versions` row must not silently grow undocumented columns for
data nothing reads. If a later card needs one of these values, add the
field then, against a real consumer.

**metadata — strip a nested block without understanding its shape.**
Extend the parser to recognize `metadata:` as a THIRD block-shaped key
(alongside the existing `dependencies:` special case), but unlike
`dependencies`, do not attempt to model its contents: skip every
following line whose leading whitespace is strictly deeper than
`metadata:`'s own line (2-space YAML indent is a documented Agent Skills
convention; do not assume exactly 2 — track the first indented line's own
column and require every following line to match or exceed it, the same
way a real YAML block-scalar reader would bound a nested map without
parsing it). This must nest correctly for both observed real shapes
(a single flat `key: value` line, and a two-level `key:` then `- item`
list) without needing to actually parse YAML — genuinely reject anything
that does not terminate before frontmatter's own `maxFrontmatterLines`
bound, the same way the unclosed-delimiter case already does. Discard the
skipped lines; `frontmatter` gains no `Metadata` field.

**Non-vacuity requirement (this repo's own convention, L-0026-shaped):**
a fix that only accepts one specific golden fixture and silently
mis-skips a differently-shaped `metadata` block (for example, over- or
under-consuming lines, corrupting the next real key) is not this card's
acceptance. Prove correct skipping against BOTH real shapes actually
observed above (canvas's two-level list-under-map shape, launch's/sire's
single flat line), not one and not a synthetic simplification of either.

**Bulk importer — cmd/zatiti helper, not a product operation.** A new
`cmd/zatiti` entrypoint (name it consistently with the existing
non-product commands, e.g. `zatiti skills import-dir <path>` assembled
directly in `cmd/zatiti`, NOT registered through the descriptor-generated
tree `internal/cli` owns) that: walks `<path>/*/SKILL.md`, calls the
already-authenticated local client's existing `skill.import` operation
once per file (the same call path the generated `skill import` command
already uses — reuse it, do not reimplement request construction), and
reports a summary: imported count, per-file failure with the exact
`skill.import` fault code/message (an unsupported key that survives this
card's own fix is a real, reportable failure, not a silent skip), and a
nonzero process exit if any file failed. Never treat "zero files found"
as success silently — that hides a wrong path the same way an empty
`checkoutRequestBodies` list would (this repo's evidence-over-assumption
convention, matched in Sire's own e2e harness for the same reason).

## Implement in this order

1. `internal/skills/frontmatter_test.go`: add failing tests first —
   `argument-hint`/`disable-model-invocation` accepted and discarded;
   `metadata` (both real shapes above) accepted and discarded; a
   genuinely unsupported key (anything not in the now-five-plus-two
   accepted set) still rejected exactly as today, so this card narrows
   the reject set, it does not remove strictness; an unterminated
   `metadata` block (never dedents before EOF or the line/byte cap) still
   fails closed with a clear fault, not a hang or silent truncation.
2. `internal/skills/frontmatter.go`: implement per "The design" above.
   `go test ./internal/skills/...` green.
3. `cmd/zatiti`: implement the `import-dir` helper per "The design"
   above, reusing the existing client call path `skill import`'s own
   command uses — read that code before writing a second one.
4. A real run against `~/.agents/skills` on this machine (or an
   equivalent multi-directory fixture in a test), reporting exact
   imported/failed counts in the PR body — not a claim, the actual
   numbers from the actual run.

## Required behavioral tests

- Each of the three previously-rejected keys, alone and combined, no
  longer fails `parseFrontmatter`, and the resulting `frontmatter` value
  carries the same `Name`/`Description`/`License`/`AllowedTools`/
  `Dependencies` as if those keys were absent — proving they are truly
  discarded, not silently misparsed into one of the real fields.
- A `metadata` block immediately followed immediately by another real key
  (`allowed-tools`, `dependencies`) at metadata's own indent level: the
  next key parses correctly — proving the skip stops at the right line,
  not one line early or late.
- A skill still fails with a real key-not-supported fault for a key that
  is not in this card's accepted set (regression guard: this parser must
  stay a strict subset parser, not become permissive).
- `import-dir` against a directory with N valid and M now-newly-valid (one
  of the three keys) and K genuinely-broken SKILL.md files reports exactly
  N+M imported, K failed with each fault, and exits nonzero.

Package check: `go test ./internal/skills/... ./cmd/zatiti/...`. Before any
multi-package Go build/test/lint, check `uptime` and claim
`R-build-lease` via `~/.claude/skills/claim/scripts/claim.sh` (hold if
1-minute load exceeds 10), release immediately after with the returned
SHA. Do not run a broad race suite in parallel with another lane.

Handoff: `internal/skills` imports every currently-rejected skill in
`~/.agents/skills` that fails ONLY for one of these three keys, still
rejects everything else it rejects today, and `zatiti skills import-dir`
imports a whole directory in one command with an honest per-file report.
Include changed files, exact commands/results, the real before/after
counts against `~/.agents/skills`, and confirmation `internal/cli` and
the frozen contract (`docs/implementation/operations.json`) are
byte-identical to `main` (this card changes neither). No publishing or
deployment is authorized by this card.
