# Zatiti desktop client

The chat-first desktop client for a Zatiti controller, built with Flutter for
macOS, Linux and Windows. It implements
[ADR 002](../../docs/adr/002-flutter-desktop-client.md) and the design of record
in [`internal/desktop/design/`](../../internal/desktop/design/README.md).

The client holds no business logic and no database access. It talks to the
controller directly over the transport that `internal/server` serves: `POST
/v1/operations/{operation_id}` with the versioned JSON envelopes, over the
private Unix socket locally or mutual TLS for a configured remote controller.
There is no Go code, no sidecar and no WebView.

This increment is unqualified. Nothing here is release evidence for
accessibility, platform packaging or controller behavior.

## Run it

Set the connection profile in the environment, then start the app:

```sh
export ZATITI_SOCKET=/path/to/controller.sock
export ZATITI_INSTALLATION_ID=<installation uuid>
flutter run -d macos
```

| Variable | Meaning |
| --- | --- |
| `ZATITI_SOCKET` | Path of the controller's private Unix socket. |
| `ZATITI_REMOTE_URL` | An `https` URL for a remote controller. Use instead of `ZATITI_SOCKET`. |
| `ZATITI_INSTALLATION_ID` | The installation every call is scoped to. Required. |
| `ZATITI_PROFILE` | Credential profile name in secure storage. Defaults to `default`. |
| `ZATITI_PRINCIPAL_ID` | Your own principal, used to read your inbox. Optional. |

The credential is never an environment variable or an operation argument. Add
it in **Workspace settings**. The app stores it in the operating system's
secure storage and sends it only as the `Authorization` header. For a remote
controller, the client certificate, private key and optional pinned roots are
read from secure storage entries `zatiti.<profile>.tls.certificate`,
`zatiti.<profile>.tls.private_key` and `zatiti.<profile>.tls.trusted_roots`.
This increment has no screen that imports them.

With nothing configured, the app explains what is missing. A development build
also offers the demo. A release build never starts the demo unless you compile
it in on purpose:

```sh
flutter run -d macos --dart-define=ZATITI_DEMO=true
```

The demo source is in memory, uses fictional records from the design study,
and shows a banner on every screen that says it is not connected to a
controller. The design study's email batch is not a v1 capability, so the
demo's pending decision is a repository action.

## Layout

| Path | Contents |
| --- | --- |
| `lib/src/transport/` | Pure Dart. Strict JSON, envelopes, fault codes, endpoints, the controller client and submission identity. No Flutter imports. |
| `lib/src/api/` | Pure Dart. Typed wire resources, typed operation calls, and `acceptance.dart` (the manual-acceptance contract a "New task"/"New responsibility" dialog can honestly build — see "Organization, worker, group and task creation" below). |
| `lib/src/state/` | The workspace source interface, the snapshot, the view-state controller, the live source and the demo source. |
| `lib/src/ui/` | `WorkspaceShell`, `OrganizationConversationTree`, `ConversationView`, `MessageComposer`, `ActionReviewDialog`, `WorkerDetailsPanel`, `WorkspaceSettings`, `creation_dialogs.dart` (new organization/worker/group), `task_dialogs.dart` (new task/delegate, task results) and the theme tokens. |
| `lib/src/app/` | Startup plan, credential store and the application widget. |
| `live_test/` | The end-to-end proof against a real controller. Not part of `flutter test`; run it with `tool/live-proof.sh`. |
| `tool/` | `live-proof.sh`, which builds the controller and runs `live_test/`. |

## Client rules

- A mutation gets one submission key when you prepare it. A retry reuses the
  same key and the same request bytes.
- The client never resends a mutation by itself.
- `controller_unavailable` means no request byte was sent. You can submit the
  same submission again.
- An unknown acknowledgment means bytes may have been sent. The submission
  locks until `command.get` resolves it. Only a `not_found` lookup unlocks an
  explicit retry.
- At connect the client reads `capabilities.list` and confirms every
  operation it calls exists at the version and request shape it was written
  against. A mismatch is a named unsupported state, never a guessed call.
- Decoding is strict: duplicate keys, unknown fields, trailing data, integers
  outside int64 and malformed UTF-8 are refused. An envelope must agree with
  its HTTP status.
- Approval, pause and sent messages display only after the controller
  acknowledges them. Offline, decisions are disabled and messages stay visibly
  unsent. Reconnecting sends nothing.
- A decision names the review version and action digest the dialog rendered.
  When the review changes while the dialog is open, the decision is disabled
  until you read the new version.
- Review content is read through `artifact.read`. Content that is not text, is
  larger than 1 MiB, or does not match the digest the review binds to cannot
  be approved from this client.

## Organization, worker, group and task creation

New organization, new worker and new responsibility each drive the real
`configuration` compiler: `organization.create`/`worker.create`/
`responsibility.create` stage a draft (no `draft_id` given, so the controller
opens one against the current head itself), `configuration.plan` seals a
candidate, the dialog shows the plan's diagnostics as the review step, and
only an explicit "Create" calls `configuration.apply`. Nothing is active
before that. Applying an organization or worker also opens a real direct
conversation with the new chief/worker (`conversation.create`) using the
signed-in human's principal id, derived from an existing direct
conversation's own participants — never guessed, for the same reason
`live_source.dart`'s class doc gives for message attribution: the catalog has
no `identity.current`. New group is immediate (`conversation.create`
directly): group chats carry no org/grant/memory permission, so nothing is
staged through the compiler.

Bounded tasks and responsibilities this client creates are always decided
**manually**: `acceptance.mode: "manual"`, so success or failure is
established only by an eligible human calling `task.accept`, never an
automated verifier. This is a deliberate scope line, not an oversight:
automated (`mode: "independent"`) verification needs a capability-evidence
artifact whose bytes (a real SHA-256 digest and base64 payload) an operator
computes and uploads by hand — `internal/installation/firsttask.go`'s own
`FirstTaskSequence` spells this out as a multi-step CLI sequence with
placeholders a person fills in. A thin client with no business logic, no
filesystem access and no cryptographic-evidence-authoring role (this root's
AGENTS.md) has no honest way to do that, so `api/acceptance.dart` builds a
schema-valid but functionally inert `profile` for manual-mode tasks only
(confirmed by reading `internal/tasks/acceptance.go`'s `evaluateSuccess`:
manual-mode success never dereferences it) and this client never offers
`mode: "independent"`. Reported as a gap in the P43 handoff rather than
silently worked around further.

`installation.verifier.list` still names the real installed verifier
identity these forms reference (never invented), and `task.create`'s
`limits` defaults to zero-spend `XXX` — the wire's explicit "no currency
configured" sentinel — unless the worker already has a configured currency,
so a task or responsibility is always creatable even before a budget exists.

## Third-party packages

| Package | Reason |
| --- | --- |
| `flutter_secure_storage` | ADR 002 requires client credentials in operating-system secure storage through a Flutter plugin: Keychain on macOS, libsecret on Linux, Credential Manager on Windows. |

Everything else is the Flutter SDK. UUIDs, strict JSON and HTTP over a Unix
socket use `dart:io`, `dart:convert` and `dart:math`. `pubspec.lock` pins the
resolved versions.

## macOS sandbox and keychain: unqualified

The macOS App Sandbox blocks a connection to a Unix socket outside the app's
container. Two options exist:

1. Turn the sandbox off and distribute outside the Mac App Store.
2. Keep the sandbox and have the controller place its socket in an app-group
   container. That needs a signing team identifier and a controller-side
   change to the socket location.

This increment takes option 1, the minimal one: `com.apple.security.app-sandbox`
is `false` in `macos/Runner/DebugProfile.entitlements` and
`macos/Runner/Release.entitlements`. No other generated platform file changed.
Option 2 is a packaging decision for the platform qualification work.

The data-protection keychain needs a Keychain Sharing entitlement tied to a
signing team. This build is not signed with one, so the credential store uses
the login keychain (`usesDataProtectionKeychain: false`). Secure storage on
all three platforms is untested at run time in this increment.

## Verify

Run from `apps/desktop`:

```sh
dart format --output=none --set-exit-if-changed .
flutter analyze
flutter test
flutter build macos --debug
```

### Prove it against a real controller

`flutter test` proves local properties against a controlled fake. The
end-to-end proof against the product's own controller is a separate suite:

```sh
tool/live-proof.sh
```

The script builds `cmd/zatiti`, starts `zatiti serve` against a short-path
temporary state directory, runs `zatiti init`, and reads the owner credential
the controller writes to `<state-dir>/profiles/owner`.
`live_test/live_controller_test.dart` then drives this client's transport and
snapshot over the controller's private Unix socket, and
`live_test/live_app_test.dart` opens the shipped `ZatitiApp` widget tree
against the same controller, so what those tests assert is what a person sees
on screen. Nothing is mocked and nothing is skipped: an unreachable controller
fails every test in the suite with the controller's own log attached.

| Variable | Meaning |
| --- | --- |
| `ZATITI_CONTROLLER_BIN` | Use this `zatiti` binary instead of building one. |
| `ZATITI_LIVE_STATE_BASE` | Where state directories are made. Defaults to `/tmp`. The controller refuses a socket path of 104 bytes or more, so this must be short. |

Two things a live suite has to do that an ordinary test does not, both of
which fail quietly if forgotten:

- A widget test binding installs `HttpOverrides` that answer every request
  with an empty HTTP 400 and open no socket, which reaches the client as a
  malformed envelope. `live_test/live_app_test.dart` clears
  `HttpOverrides.global` before each test. Only that file does; every other
  suite keeps the binding's default.
- A widget test's own zone does not run the event loop that real socket
  futures complete on, so every call that reaches the controller is *started*
  inside `WidgetTester.runAsync`, not merely awaited there. Tapping a button
  that kicks off a request starts it in the test's zone instead, and the
  request never completes; `test/ui/` covers the buttons.

`test/transport/catalog_parity_test.dart` reads
`docs/implementation/operations.json`, so run the tests inside the repository.
`test/transport/mutual_tls_test.dart` mints certificates with the `openssl`
command into a temporary directory and skips the TLS exchange tests when
`openssl` is absent. No private key is committed.

## What is verified and what is not

Verified by tests in this repository:

- The transport against an in-process HTTP server on a Unix socket, replaying
  envelopes shaped like the Go transport tests.
- Mutual TLS against an in-process HTTPS server: the client certificate is
  presented, an unpinned server root is a permanent failure with nothing sent,
  and a client certificate from an unknown authority is never authenticated.
- The live source and view state end to end over a Unix socket with fixtures
  that follow the catalog's `$defs`.
- The UI behaviors listed in `test/ui/`.

Verified against a real controller by `live_test/` (`tool/live-proof.sh`), on
macOS over the private Unix socket:

- `zatiti serve` starts, `zatiti init` initializes the installation, and the
  owner profile it writes is a mode-0600 file holding the complete
  `Authorization` header value, used verbatim.
- Every operation in `Operations.all` is served at the version, submission-key
  and expected-version shape this client was written against.
- `principal.list` returns the owner and the controller's service identity.
- A keyed `principal.create` completes; the identical resend under the original
  key replays the original command id and data and creates nothing new; the
  same key with a changed input is refused `submission_conflict`, and nothing
  is created.
- A lost acknowledgment resolves through `command.get` to the original result
  envelope.
- A call with no credential is refused `verification_failed` at HTTP 422 with
  no data.
- `LiveWorkspaceSource.loadSnapshot` builds the real workspace: the personal
  organization, its chief and the pinned personal-chief conversation, with no
  invented work, decisions or files.
- A message sent from the composer is acknowledged, and `event.list` sees the
  event a mutation emits.
- The shipped application opens on the controller's personal chief with a
  ready composer, no offline banner and nothing claimed to need the person;
  settings name the owner and the controller's service identity and never
  render the credential; a typed message is sent and then shown.

Not verified:

- Mutual TLS against `internal/server`'s listener, including its
  certificate-fingerprint mapping and TLS version policy. The live proof drives
  the Unix socket only.
- Every surface that needs populated state: reviews, tasks, responsibilities,
  artifacts and spending are all empty on a fresh installation, so the live run
  proves they are honestly empty, not that they render real records.
- P43's organization/worker/group/task/responsibility creation flows: proven
  by `test/state/creation_flow_test.dart` and `test/state/
  prerequisites_and_unresolved_ops_test.dart` against the controlled fake
  (the real draft/plan/apply staging, the manual `task.accept` decision, and
  per-worker setup cards), but not yet run against a real controller through
  `tool/live-proof.sh`. That real-controller pass is explicit follow-up work
  for whoever lands this card next.
- Linux, where the live proof has not been run.
- Windows. Dart supports `AF_UNIX` addresses, but the transport choice for
  Windows is qualification work per ADR 002.
- Linux builds and secure storage on every platform.
- Screen readers, contrast and pointer-target sizes with real assistive
  technology. The tests check semantics labels, focus and traversal only.

## Catalog gaps

Where the public operation catalog has no operation for something the design
shows, the client renders the designed empty or prerequisite state and invents
nothing.

| Design element | Missing |
| --- | --- |
| Conversation history | No operation lists a conversation's messages. `mailbox.list` returns only messages delivered to the caller. The thread shows received messages when a principal is configured, messages sent from this window, and a notice. |
| Caller identity | No operation returns the authenticated caller's principal, which `mailbox.list` requires as `recipient_id`. It is startup configuration. Observed live: a mailbox is private to its own recipient, so any other principal is refused `permission_denied`. The client reports that as a prerequisite and keeps the rest of the workspace. |
| Sidebar preview line and unread state | `Conversation` carries only `last_meaningful_event`. No preview text or read marker is readable. Live rows show no preview. |
| Memory tab: list, source, freshness, remove from recall | `memory.inspect` needs a claim identity and no operation lists claims. The tab shows a notice. |
| Review title in plain language | `Review` and `Action` carry no human label. The client uses the tool's `name` from `tool.get`. |
| Files tab: name, provenance, sharing | `Artifact` has no name, producing task or sharing fields. Rows show media type, size and digest prefix. |
| Routines tab: schedule and time zone for a responsibility | `Responsibility` has triggers and `next_wake` only. `Schedule` carries the expression and time zone but has no link to a responsibility. |
| Task checks: observed failures | `Task` exposes `state` and `waiting_reason`, not the verification observations. A failed task shows "Needs changes" with the waiting reason. |
| Event tailing | `event.list` returns `next_cursor` only when more pages remain, so a drained client has no cursor to resume from and rereads the last page. |

Observed against the real controller, and not a gap:

- `conversation.message.send` keeps the `message_id` the client mints and
  returns it as the message's own id. The client uses that id for the message
  it displays, so a copy delivered back through a mailbox is recognized and
  not shown twice.

One controller defect, observed from this root, reproduced, and since fixed
by another lane (verified by reading `internal/tasks/ports.go`'s current
`callPeer`, which now passes a peer's named fault through unchanged instead
of stamping `internal_error` over it, and by `cmd/zatiti/firsttask_test.go`'s
`TestFirstTaskSequenceRunsAsPrinted`, which runs `task.create` with zero-spend
`XXX` on a fresh installation end to end and asserts it completes) — kept here
for history, not as a current gap:

- `task.create` on a freshly initialized installation used to fail
  `internal_error` "peer call `_accounting.reserve` failed" at HTTP 500 even
  at zero spend in the installation's own `XXX` currency, because a peer
  refusal that arrived as a Go error rather than inside the payload was
  overwritten with an unnamed internal error. P43 now calls `task.create`
  directly (see "Organization, worker, group and task creation" above); this
  note is why that call was safe to add.

Two contract observations for other lanes:

- `internal/messaging` list handlers put `next_cursor` inside `data`, which the
  catalog's output schemas do not declare. Other owners use the envelope
  field. The client accepts both.
- `internal/client` looks up `command.get` with `{"submission_key"}` only. The
  catalog and `internal/evidence` require `scope`, `submission_key`,
  `operation` and `operation_version`. This client follows the catalog.
