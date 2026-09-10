// Package client owns Zatiti's shared controller client: the Go transport
// every local surface (CLI, desktop, MCP adapter) uses to call controller
// operations. It implements contract.Operator.
//
// Transport. Call posts the versioned request envelope to
// POST /v1/operations/{operation_id}. Config selects exactly one endpoint:
// a private Unix-domain socket for local controllers, or an explicitly
// configured remote desktop endpoint over TLS (https:// only). Local traffic
// is never wrapped in TLS; remote traffic always is. HTTP redirects are
// refused so the credential header can never follow to another host.
//
// Credentials. A contract.CredentialSource supplies the credential header
// bytes freshly for every Call; the bytes become the value of AuthHeader
// (Authorization) and are never placed in the JSON request body, tool
// arguments, or diagnostics. The client zeroes the buffer it received once
// the exchange ends, so a source must hand out a private buffer per call.
//
// Strictness. Responses are read with a bounded reader and decoded with
// contract.DecodeStrict (duplicate keys, unknown fields, trailing data and
// out-of-range integers are rejected). The result envelope's schema, status
// and error fields are validated, and the HTTP status must agree with the
// frozen mapping: 200 completed, 202 accepted, domain faults on their mapped
// failure statuses. A domain failure surfaces its fault as the error value
// while the full envelope stays in the returned Result.
//
// Retry and reconnect. Every Call dials fresh, so a restarted controller is
// reconnected automatically. Auto retries never invent or replace a
// submission key. They are limited to:
//
//   - connection establishment that fails before any request byte reaches
//     the controller (bounded attempts; the call fails with
//     ErrControllerUnavailable and may simply be reissued with the same key),
//   - an explicit identical submission: when the request carries a
//     SubmissionKey and the exchange fails after the bytes were sent, the
//     exact same bytes are replayed under the same key, which the controller
//     deduplicates,
//   - an unresolvable unknown acknowledgement of a keyed submission is
//     resolved through CommandGetOperation (command.get) looked up by the
//     original submission key; a not_found lookup proves the command never
//     committed and licenses one final identical replay.
//
// A cancellation or timeout of the local wait is not a cancellation of an
// accepted command: once bytes were sent the disposition is unknown, and the
// client stops network work on the dead context and returns UnknownAckError
// carrying the original operation and submission key.
//
// Cursors. An expired cursor is never papered over: the cursor_expired fault
// surfaces as *CursorExpiredError with its snapshot_required detail parsed,
// and the caller refreshes its snapshot and replay position.
package client
