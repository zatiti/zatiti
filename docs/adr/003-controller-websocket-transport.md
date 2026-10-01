# ADR 003: Controller WebSocket transport

## Status
Accepted

## Date
2026-09-28

## Context
The user is planning Zatiti Cloud and decided to add WebSocket to the controller ('Let's build a Zatiti websocket server'). Today internal/server registers POST /v1/operations/{operation_id} (server.go:20, :93-98) and, since revision 20, the human-only SSE route POST /v1/replies/stream (server/stream.go) on a 0600 Unix socket and, when Config.RemoteAddress is set, on an mTLS listener that requires RequireAndVerifyClientCert (config.go:15-41).
v2 placed WebSocket in a separate gateway process. That process duplicated authentication and audit, needed a 0600 control socket with a capability bearer to reach the adapter, and gave remote clients no push channel.
Constraints:
- The frozen server brief (packages.py:81-83) says 'no unauthenticated public bind', 'Versioned POST operation endpoint only' and 'no alternate restore/admin endpoint'.
- Roots may not overlap (render.py:60-61).
- http.Server.Shutdown does not close or wait for hijacked connections.
- identity.Authenticate zeroes the credential slice it is given (authn.go:23-31).
- serve shuts down with srv.Close before ctl.Stop, sharing one grace period (serve.go:202-224).

## Decision
Placement:
- internal/server owns the WebSocket transport: all listeners, the GET /v1/ws upgrade, admission, frame I/O, limits, connection tracking and drain.
- A new root, internal/wshub, owns the hub: sessions, event hints, browser pairing and fence, command channel and ledger. It is named so that it does not overlap internal/server; internal/server/stream would violate render.py:60-61.
- internal/contract holds narrow seams (WSSession, WSHandler, BrowserChannel, AdapterDependencies.Browser), so server and stream never import each other and server imports stay 'contract application'.
- cmd/zatiti wires them together.

One endpoint, GET /v1/ws, with two subprotocols:
- zatiti.ws.v1 on the Unix and remote listeners: call, result, subscribe, events, resync, ping, pong, error.
- zatiti.browser.v1 only on the loopback browser listener.

Parity: a call frame carries the byte-identical POST body and returns the byte-identical response envelope and status. Both paths run one transport-neutral dispatch extracted from handler.go:69-102: decode with MaxBodyBytes, re-authenticate on every call with a fresh credential copy, application.Invoke, and the same slog fields with transport local-ws, remote-ws or browser-ws. installation.init is refused on every stream.

Listener model:
- Unix: the Authorization header on the upgrade.
- Remote mTLS: the verified certificate plus an optional bearer that must name the same principal (handler.go:130-141).
- Browser (new): literal 127.0.0.1 only; upgrades only; exact Host, exact extension Origin, subprotocol, then first-frame auth.
The Unix and remote upgrades refuse any Origin header, to defeat CSWSH through a browser-held client certificate.

Limits: a read limit, at most 8 in-flight calls, a 64-frame outbound queue with hint coalescing, a 10 s write timeout, a 60 s idle timeout, per-principal and per-listener caps, and server protocol pings every 30 s on the Unix and remote listeners.

Drain: the server tracks every stream. Close stops reading new calls, waits up to 5 s for in-flight calls, calls WSHandler.Drain, closes streams with 1012, and only then returns, so ctl.Stop records the outcomes.

Library: github.com/coder/websocket. It has a context-aware API, concurrent-write safety, read limits and Ping, and it claims zero dependencies under the ISC license; T2.4a verifies the version (v1.8.x expected), license and dependency claims before pinning, because no network verification was possible while planning. Alternatives considered:
- gorilla/websocket: BSD, mature, but allows only one concurrent writer and has a context-free API; its maintenance history is to be checked in T2.4a.
- golang.org/x/net/websocket: its own package documentation points users to more complete, actively maintained packages, and by default it only checks that Origin is a valid URL.
- A stdlib-only RFC 6455 implementation: the standard library has no WebSocket server, so this means several hundred lines of security-critical framing plus fuzzing.
Admission is always done by Zatiti's own exact checks before the library's Accept is called.

Zatiti Cloud: the remote mTLS listener already carries the stream, proven by an mTLS test listener. Deployment, desktop adoption, a Go stream client in internal/client and a storage commit-notification seam are planned in outline epic E13.

The Flutter desktop is unchanged: it keeps HTTP over the Unix socket.

SSE and WebSocket coexist. Naming uses ws, not stream, to avoid ReplyStreams/Config.Streams. Drain reuses streamsDone within the 10 s serveCloseGrace. SSE deprecation is deferred to E13.

## Consequences
- One authentication, audit and limit surface for every transport, with remote push available for Zatiti Cloud clients.
- The separate gateway process, its control socket and its capability bearer are deleted.
- internal/server and internal/contract briefs change in revision N, and T0.1 records owners for those roots, which no lane currently owns.
- A hub bug runs in the controller process, so per-session recover, bounded goroutines and -race tests are required.
- Event hints come from polling (750 ms tails) until E13 adds a commit-notification seam.
- The first browser setup needs one serve restart, because listener.json is read once per runServeOnce.
- coder/websocket becomes a direct dependency, scoped to internal/server and internal/wshub/wstest.
