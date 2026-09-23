# Workflows

This directory holds two workflows and the Go command that validates them and
makes their decisions. Neither workflow publishes, releases, signs, tags, or
deploys, and neither references a secret. The release workflow retains unsigned
native build candidates for later review; those files are not release assets.

| File | Starts on | Purpose |
|---|---|---|
| `ci.yml` | Pull requests, pushes to `main` | Specification drift check, workflow validation, static checks, Go build and tests, and the Flutter desktop client on Linux and macOS. |
| `release-qualification.yml` | A version tag push, or a manual dispatch started from a version tag | Decides whether the tagged commit is qualified. Produces a verdict and evidence only. |
| `cigate/` | Called by both workflows with `go run` | Policy validation, release verdict, qualification-evidence enumeration and freshness, bounded test evidence, input resolution, Flutter SDK pin, lock and drift checks. Standard library only. |

The release workflow's native `build` and `flutter` matrix jobs now retain the
actual controller executable and a deterministic archive of the complete
Flutter `.app`, respectively, for each Mac architecture. The workflow records
the source SHA, archive digest and size, license digest, and the digest and CPU
slices of every Mach-O it finds. It refuses a missing or wrong-architecture
controller, app runner, framework, or dylib before upload. The archiver is
`zatiti-pack assemble-bundle`; the audit is `cigate candidate`. The retained
directories also include `LICENSE` and the dependency lock report, with
`pubspec.lock` for the desktop. They are unsigned, lack a matched four-part
release descriptor and installer package, and carry no notarization evidence.

## Validate locally

Run these from the repository root. `go test ./...` does not match directories
that start with a dot, so name the path explicitly.

```sh
go run ./.github/workflows/cigate lint
go vet ./.github/workflows/...
go test -race ./.github/workflows/...
golangci-lint run --max-same-issues=0 ./.github/workflows/...
```

The tests also run `actionlint` when it is installed. CI installs
`actionlint` v1.7.12 and sets `ZATITI_REQUIRE_ACTIONLINT=1`, which turns an
absent linter into a failure instead of a skip.

## Policy the validator enforces

`cigate lint` parses each workflow with a strict YAML subset parser and rejects
the file when a rule fails. `policy_test.go` mutates the committed workflows
one property at a time and requires each rule to fire.

- Only `ci.yml` and `release-qualification.yml` may exist. An unregistered
  workflow file fails validation.
- Top-level `permissions` is exactly `contents: read`. No job may request a
  write permission.
- No `secrets.*`, `github.token`, or `GITHUB_TOKEN` reference anywhere.
- Every `uses:` is `owner/repo@<40-hex commit> # <version>` and matches the
  verified pin table in `cigate/policy.go`. Tags, branches, local actions, and
  container actions are rejected.
- Triggers: `ci.yml` accepts `pull_request` and `push` to `main` only.
  `release-qualification.yml` accepts `workflow_dispatch` with a required
  `version` input and `push` of `v*` tags only. `pull_request_target` and
  every other trigger are rejected.
- Runners are the pinned OS labels `ubuntu-24.04`, `macos-15` (Apple
  Silicon), and `macos-15-intel` (Intel). GitHub documents the two Mac
  architectures in its [hosted-runner reference](https://docs.github.com/en/actions/reference/runners/github-hosted-runners).
  Matrix jobs also check `RUNNER_OS`, `RUNNER_ARCH`, and `uname -m` before
  installing toolchains or running tests. CI retains Linux development jobs;
  the first-release qualification matrix requires both native Mac images.
- Every job has `timeout-minutes` of at most 60, starts with checkout using
  `persist-credentials: false`, verifies the toolchain and dependency lock,
  and ends by retaining evidence with `if: always()`, `if-no-files-found:
  error`, and `retention-days` of at most 30.
- `setup-go` reads the toolchain from `go.mod` and has its implicit cache
  turned off. Go cache keys bind `go.mod` (toolchain), `go.sum`, and
  `docs/implementation/dependencies.lock.json`. The `flutter` job caches only
  `~/.pub-cache`, keyed by the pinned Flutter version and
  `apps/desktop/pubspec.lock`; no other job may cache pub packages and the
  `flutter` job may not restore a Go cache. `restore-keys` is rejected. The
  release workflow uses no cache.
- The `spec` job runs `python3 tools/specgen/render.py --check` as its first
  step after checkout, and every other job depends on it.
- No `continue-on-error`, no job-level `if` except `always()` on the verdict,
  and step conditions only from an allowlist: `always()` (evidence
  retention), `runner.os == 'Linux'`, `runner.os == 'macOS'`, and, in
  `ci.yml` only, those two combined with
  `steps.layout.outputs.integration_test == 'true'`.
- Run scripts contain no `${{ }}` expressions, no `curl`, `wget`, `gh
  release`, `git push`, `git tag`, or registry publish commands (including
  `flutter pub publish`), and every `go install` names an exact version.
- Every `go test` run passes an explicit `-timeout` (`cmd/zatiti` builds a
  binary and runs real controllers; its race run exceeds the default
  10-minute package timeout under load) and `-short` is rejected, because it
  would skip the end-to-end and integration tests that prove the product
  runs.
- The whole-module `go test ./...` run in the `test` job's unit and race
  steps, on both workflows, must pass `cigate gotest -require-tests` naming
  the exact `internal/platform` regression tests a hosted Linux/macOS run
  once observed failing while the local run stayed green (see "Platform
  regression protection" below). A workflow edit that drops or shortens that
  list fails the `platform-regressions` rule.
- The `qualification` gate of `release-qualification.yml` must run `cigate
  qualevidence` (the `release-gates` rule), so a required Z-gate whose case
  identity was deleted outright -- which leaves go test nothing to report, so
  `-strict` cannot see it -- still blocks instead of passing on an empty
  gate.

## Release qualification

The workflow fails closed when its inputs are absent:

- The run must start from a tag that matches `vMAJOR.MINOR.PATCH`. A manual
  dispatch must also type the same version. A dispatch from a branch, a
  missing or mismatched version, or a non-version tag fails with
  `invalid_input`.
- The checked-out commit must be the tagged commit and must be reachable from
  `origin/main`. A tag on an unreviewed branch fails with
  `verification_failed`.

Required gates: `inputs`, `spec`, `workflows`, `static`, `test`, `flutter`,
`qualification`, `build`, `candidate_matrix`. The list is compiled into
`cigate gate`, so removing a job from the workflow produces a `missing` gate
and blocks the verdict. Only `success` passes; `failure`, `cancelled`,
`skipped`, and unrecognized results block. The platform gates run on both
Linux and macOS with `fail-fast: false`.

Release test gates run `cigate gotest -strict` and `cigate fluttertest
-strict`: a skipped test blocks, because a qualification that reports not-run
cannot support a release claim. A test run that executes no test blocks in
both workflows. The `test` gate runs `go test ./...`, which includes
`cmd/zatiti`'s end-to-end binary tests (8-minute internal budget) and
`tests/integration` (about 5 minutes under race); `-short` is never passed.
Those steps set `TMPDIR=/tmp/zt`: the tests create their controller sockets
under the system temporary directory, and `cmd/zatiti` refuses a socket path
of 104 bytes or more, which a runner's default temporary path can exceed.

A gate whose subject is not implemented fails with `prerequisite_missing`
instead of passing on an empty directory. At the time this directory was
written, `tests/qualification` contains no Go tests and
`apps/desktop/integration_test` does not exist, so the `flutter` and
`qualification` gates fail and no tag can be qualified. That is the intended
result, not a defect.

A passing verdict is evidence, not permission. Publishing is a separate,
reviewed, human process that this directory does not implement.

## Qualification evidence enumeration and freshness

`tests/qualification` writes `release-report.json` on every run: a case
record for every named acceptance case it owns a harness identity for
(`passed`, `failed`, or `not_run` with the blocking reason), rolled up per
gate (`Z01`-`Z21`, `JOURNEY`, `QUALIFICATION`). `cigate gotest -strict`
already blocks the `qualification` gate on any case that ran and was skipped
or failed. It cannot catch a case whose test function was deleted outright:
that leaves no pass, fail, or skip event for go test to report at all, so
the process exits clean while the retained report quietly drops the case
from its gate.

`cigate qualevidence` closes that gap by reading the retained report
directly instead of trusting the exit code alone:

1. It refuses evidence whose `versions.source_revision` is not the
   checked-out commit, or whose `versions.module_root_go_mod` digest is not
   the checked-out `go.mod`: evidence from an earlier run or a different
   revision can never stand in for the tree actually being qualified.
2. It requires every case the report does carry to read `passed`.
3. For each gate named with `-require-gate`, it requires the report to
   carry that gate at all, and at its best reachable status,
   `passed_cases_only` -- never `no_case_executed_here` (no case exists),
   `not_run`, or `failed`. `-require-gate` is set explicitly in the workflow
   step, naming only the gates `tests/qualification` currently owns a real
   case for (`Z01`, `Z04`, `Z13`, `Z16`, `Z21`, `JOURNEY`, `QUALIFICATION`,
   per its own committed `beginCase` calls); extending that list as more
   cases land is a reviewable one-line workflow change, not a Go-code
   change, since which gates this package currently owns is that package's
   decision, not this directory's to invent.

`release_claimable` in the raw report is always `false` and every gate
always lands in the report's own `blocking` list by design (its doc comment:
"this report never counts an absent case as a pass") -- `cigate qualevidence`
does not read either field; it evaluates the report's `cases` and `gates`
directly against the explicit, reviewable required-gate list instead.

The `qualification` job sets `ZATITI_QUALIFICATION_EVIDENCE` so
`release-report.json` and every individual case file land under
`$RUNNER_TEMP/evidence/qualification-cases` and are retained by the job's
`actions/upload-artifact` step; without that variable the report goes to a
throwaway temporary directory and is lost after the run.

## Platform regression protection

[PR #2 CI run 35473210732](https://github.com/zatiti/zatiti/actions/runs/35473210732)
observed `internal/platform.TestListenPrivateRefusesSymlinkedRunDirectory`
fail on hosted Linux and macOS, and
`internal/platform.TestBlobTamperedObjectFailsPublishOverExisting`
additionally fail on hosted macOS, while the local run of the same source
stayed green (`docs/implementation-remediation/audit.md`, "Hosted CI
follow-up"). `go test ./...` already runs both tests; nothing before this
change verified that they specifically ran and passed, so a rename or
deletion of either test would not have been caught by the aggregate
pass/fail counts.

The `test` job's unit and race steps, on both `ci.yml` and
`release-qualification.yml`, now pass `cigate gotest -require-tests` naming
both tests as `pkg#Test` identities. A required identity that is never
observed with a `pass` action in the `go test -json` stream blocks the run,
independent of `-strict` and independent of the overall exit code, and is
recorded in `<name>.summary.json`'s `required_missing` field. `cigate lint`
enforces the `-require-tests` argument's presence and contents (rule
`platform-regressions`) on every whole-module `go test ./...` invocation in
the `test` job, so a future edit cannot drop this protection without the
policy failing closed.

## Flutter desktop client

The `flutter` job runs on both platforms and does not touch the Go module
caches; it uses the Go toolchain only to run `cigate`.

1. `cigate flutterpin` reads `flutter_sdk` (`version`, `framework_revision`,
   `dart_sdk`) from `docs/implementation/dependencies.lock.json`, which
   integration owns, and checks that the pin satisfies the `sdks` constraints
   in `apps/desktop/pubspec.lock`. `pubspec.lock` records constraints, not an
   exact SDK, so the lock report is the single exact pin. No pin means
   `prerequisite_missing`.
2. Linux installs the documented desktop prerequisites: `clang`, `cmake`,
   `ninja-build`, `pkg-config`, `libgtk-3-dev`, `libstdc++-12-dev`, the
   Secret Service library `libsecret-1-dev` and `libsecret-1-0` for
   `flutter_secure_storage`, and `xvfb` for a virtual display.
3. The SDK is a shallow `git clone` of the pinned version tag from
   `https://github.com/flutter/flutter.git`; `cigate flutterverify` compares
   `flutter --version --machine` against the pin (version, framework
   revision, Dart SDK) before anything runs.
4. `flutter pub get --enforce-lockfile`, `dart format --output=none
   --set-exit-if-changed .`, `flutter analyze`, and `flutter test` run through
   `cigate bounded` or `cigate fluttertest`. `flutter test` includes
   `test/transport/catalog_parity_test.dart`, the operation-catalog digest
   drift check, which reads `docs/implementation/operations.json`.
5. `flutter build linux --release` or `flutter build macos --release` builds
   the platform application. The build is not uploaded.
6. `integration_test` runs with `xvfb-run` on Linux and directly on macOS.
   In `ci.yml`, `cigate flutterlayout` records whether the directory exists
   and the steps run only when it does; the evidence file names what did not
   run. In the release workflow the directory is required.

## Evidence

Each job writes evidence outside the checkout, under `$RUNNER_TEMP/evidence`,
so it cannot dirty the tree that drift and provenance checks inspect.

| File | Content |
|---|---|
| `environment.json` | Toolchain, platform, digests of `go.mod`, `go.sum`, and the lock report, and an allowlisted set of runner variables. |
| `<name>.summary.json` | Test counts, failed tests with at most 8 KiB of output each, skipped tests, blocking reasons. Lists hold at most 200 entries. |
| `<name>.events.jsonl` | The `go test -json` stream, truncated at 8 MiB. |
| `<name>.stderr.txt` | At most 64 KiB of `go test` standard error. |
| `<name>.log`, `<name>.result.json` | Output of one bounded command (`pub-get`, `dart-format`, `flutter-analyze`, `flutter-build`), truncated at 4 MiB, with its exit code. |
| `flutter-pin.json`, `flutter-version.json`, `flutter-layout.json` | The pinned SDK, the installed SDK as reported by `flutter --version --machine`, and which optional application parts exist. |
| `inputs.json`, `provenance.json`, `verdict.json` | Release inputs, build provenance of the candidate binary, and the verdict. |
| `qualification-cases/release-report.json`, `qualification-cases/<case>.json` | `tests/qualification`'s own per-case evidence and gate rollup (see "Qualification evidence enumeration and freshness"). |
| `qualification-verdict.json` | `cigate qualevidence`'s enumeration-and-freshness verdict against the required gate list. |

Console output is a bounded summary. The release workflow retains unsigned
native candidate binaries and complete desktop archives alongside their
provenance. Its `candidate_matrix` gate downloads the four fixed artifact
names from the same workflow run, checks them against the checked-out commit
and lock files, parses each archive without extraction, and writes a bounded
`unsigned-candidate-matrix.json`. This index is neither a signed release
descriptor nor an installer or publication artifact.

## Verified pins

The original four commits were resolved with `git ls-remote` against their
upstream repositories on 2026-09-18. The download action v8.0.0 tag was
resolved from its upstream repository on 2026-09-23. Actions use immutable
commit pins; the workflow linter checks the version annotation against the
table.

| Action | Version | Commit |
|---|---|---|
| `actions/checkout` | v7.0.1 | `3d3c42e5aac5ba805825da76410c181273ba90b1` |
| `actions/setup-go` | v7.0.0 | `b7ad1dad31e06c5925ef5d2fc7ad053ef454303e` |
| `actions/cache` | v6.1.0 | `55cc8345863c7cc4c66a329aec7e433d2d1c52a9` |
| `actions/upload-artifact` | v7.0.1 | `043fb46d1a93c77aae656e7c1c64a875d1fc6a0a` |
| `actions/download-artifact` | v8.0.0 | `70fc10c6e5e1ce46ad2ea6f2b72d43f7d47b13c3` |

To change a pin:

1. Resolve the commit: `git ls-remote https://github.com/<owner>/<repo>.git
   'refs/tags/<version>' 'refs/tags/<version>^{}'`. When a `^{}` line exists,
   use its commit.
2. Read `action.yml` at that commit and confirm the inputs used here.
3. Update `verifiedPins` in `cigate/policy.go`, the workflow files, and the
   table above in one change.

Tools installed with `go install` are pinned by module version and verified by
the Go checksum database: `actionlint` v1.7.12 and `golangci-lint` v2.11.3.
The Flutter SDK is pinned by the lock report's version tag and framework
commit and verified after checkout; the tag 3.32.1, the version installed on
the machine the client was written with, resolves to
`b25305a8832cfc6ba632a7f87ad455e319dccce8` upstream.

## Not verified

These items are stated so that nobody reads them as qualified:

- Neither workflow has run on GitHub. Syntax is checked by `actionlint` and
  the policy by `cigate`; runner behavior is unobserved.
- `setup-go` is expected to install the `toolchain` version from `go.mod`.
  The `cigate lock` step fails the job if the running toolchain differs, so a
  wrong assumption blocks instead of passing.
- The Linux desktop prerequisites are distribution packages and are not
  version pinned. `flutter build linux`, `xvfb-run`, and secure storage on
  Linux have not been exercised anywhere yet; on macOS, `flutter pub get
  --enforce-lockfile`, `dart format`, `flutter analyze`, `flutter test
  --machine`, and `flutter build macos --release` were run locally with
  Flutter 3.32.1 while writing this directory.
- A Flutter shallow clone relies on the version tag being fetched with the
  branch; `cigate flutterverify` fails the job if the reported version or
  revision differs, so a wrong assumption blocks instead of passing.
- The Flutter SDK's own artifact downloads (Dart SDK, engine) come from the
  SDK's pinned manifests, not from this policy.
- `macos-15` runs native `arm64`, `macos-15-intel` runs native `amd64`,
  and `ubuntu-24.04` runs `amd64`. No job covers `linux/arm64`. Windows,
  remote MCP, and contained runners are not release targets and have no job.
- The `qualification` gate references no credentials. When operator-provided
  test accounts exist, add them through a protected environment in a reviewed
  change to this policy; until then, tests that need them skip and the strict
  gate blocks.
- Build provenance is recorded as evidence. It is not a signed attestation;
  signing needs `id-token: write`, which this policy rejects.
