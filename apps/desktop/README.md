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
| `lib/src/api/` | Pure Dart. Typed wire resources and typed operation calls. |
| `lib/src/state/` | The workspace source interface, the snapshot, the view-state controller, the live source and the demo source. |
| `lib/src/ui/` | `WorkspaceShell`, `OrganizationConversationTree`, `ConversationView`, `MessageComposer`, `ActionReviewDialog`, `WorkerDetailsPanel`, `WorkspaceSettings` and the theme tokens. |
| `lib/src/app/` | Startup plan, credential store and the application widget. |

## Client rules

- A mutation gets one submission key when you prepare it. A retry reuses the
  same key and the same request bytes.
- The client never resends a mutation by itself.
- `controller_unavailable` means no request byte was sent. You can submit the
  same submission again.
- An unknown acknowledgment means bytes may have been sent. The submission
  locks until `command.get` resolves it. Only a `not_found` lookup unlocks an
  explicit retry.
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

Not verified:

- Any exchange with the real Go controller. The fixtures are hand-written from
  the catalog.
- Mutual TLS against `internal/server`'s listener, including its
  certificate-fingerprint mapping and TLS version policy.
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
| Caller identity | No operation returns the authenticated caller's principal, which `mailbox.list` requires as `recipient_id`. It is startup configuration. |
| Sidebar preview line and unread state | `Conversation` carries only `last_meaningful_event`. No preview text or read marker is readable. Live rows show no preview. |
| Memory tab: list, source, freshness, remove from recall | `memory.inspect` needs a claim identity and no operation lists claims. The tab shows a notice. |
| Review title in plain language | `Review` and `Action` carry no human label. The client uses the tool's `name` from `tool.get`. |
| Files tab: name, provenance, sharing | `Artifact` has no name, producing task or sharing fields. Rows show media type, size and digest prefix. |
| Routines tab: schedule and time zone for a responsibility | `Responsibility` has triggers and `next_wake` only. `Schedule` carries the expression and time zone but has no link to a responsibility. |
| Task checks: observed failures | `Task` exposes `state` and `waiting_reason`, not the verification observations. A failed task shows "Needs changes" with the waiting reason. |
| Event tailing | `event.list` returns `next_cursor` only when more pages remain, so a drained client has no cursor to resume from and rereads the last page. |

Two contract observations for other lanes:

- `internal/messaging` list handlers put `next_cursor` inside `data`, which the
  catalog's output schemas do not declare. Other owners use the envelope
  field. The client accepts both.
- `internal/client` looks up `command.get` with `{"submission_key"}` only. The
  catalog and `internal/evidence` require `scope`, `submission_key`,
  `operation` and `operation_version`. This client follows the catalog.
