# ADR 002: Flutter desktop client over the controller's local transport

## Status

Accepted. Decided by the founder on 2026-09-15 (Flutter) and 2026-09-18
(placement and transport). Supersedes the desktop portions of the frozen
implementation contract, which named Fyne v2 and a Go desktop package.

## Date

2026-09-18

## Context

The frozen contract selected "Fyne v2 native Go desktop" with two Go roots:
`internal/desktop` (exposing `desktop.New(Config, contract.Operator)`) and
the `cmd/zatiti-desktop` entrypoint. Neither was implemented. The adopted
design study at `internal/desktop/design/` targets a chat-first client whose
layout, typography and interaction detail the founder wants built in Flutter.
A Flutter application is a Dart toolchain and cannot be a Go package, so the
Go roots, their imports in `tests/qualification`, and the desktop parts of
`packaging` no longer describe the plan.

## Decision

1. The desktop client is a Flutter application at `apps/desktop/` in this
   repository. No new repository. The Go module does not import it.
2. The client talks to the controller directly over the transport
   `internal/server` already serves: the private Unix socket locally, and the
   mutual-TLS listener for an explicitly configured remote desktop. It uses
   the versioned JSON request/result envelopes and the generated operation
   catalog. There is no Go code in the client, no sidecar, and no WebView
   wrapper presented as the Flutter implementation.
3. `internal/desktop` and `cmd/zatiti-desktop` are retired as implementation
   roots. `internal/desktop/design/` remains as the design reference until
   the spec change relocates it.
4. Client credentials live in the operating system's secure storage through
   a Flutter plugin. A credential profile is startup configuration and never
   an operation argument, exactly as for the CLI and MCP surfaces.
5. The desktop client holds no business logic and no database access. Every
   displayed state derives from controller snapshots and events; approval,
   pause and creation are shown only after controller acknowledgment, and
   unknown acknowledgments are queried before any retry.
6. The contract change is made through the authored spec inputs and the
   renderer (`tools/specgen`), not by hand-editing generated prompts.

## Consequences

The client cannot reuse `internal/client`'s reconnect and submission-key
retry logic; the Dart client reimplements those semantics against the same
wire contract, and transport parity tests must cover it. Windows has no Unix
socket path convention identical to macOS/Linux; the transport decision for
Windows (AF_UNIX support in Dart versus the mutual-TLS loopback listener) is
qualification work, not assumed. Packaging gains a Flutter build per
platform and CI gains a Flutter job. `tests/qualification` qualifies the real
Flutter client instead of importing a Go desktop package. Release targets,
accessibility and platform qualification for the client remain unproven
until exercised; the design study's browser validation is not evidence for
the Flutter build.
