# ADR 004: Browser extension security and pairing

## Status
Accepted

## Date
2026-09-28

## Context
The MV3 extension must reach the local controller over ws://127.0.0.1 without exposing controller operations to web pages, other extensions, DNS-rebinding attackers or same-user processes squatting the port.
The extension receives a credential that can relay chat, so it must not become a bypass of the relay fence, and revocation must be prompt.
MV3 service workers idle after about 30 s without activity, and the JS WebSocket API cannot observe protocol pings.

## Decision
Browser listener:
- literal 127.0.0.1:<port>; WebSocket upgrades only;
- exact Host 127.0.0.1:<port> (v2's localhost alternative is dropped because the extension always dials the literal);
- exact Origin chrome-extension://<pinned id or ids from listener.json>;
- subprotocol zatiti.browser.v1; 403 otherwise.
CSWSH is defeated because browsers attach a script-unforgeable Origin to every WebSocket handshake, so web pages and other extensions fail. DNS rebinding is defeated because a rebound name produces Host evil:<port> and Origin http://evil, and both fail. Non-browser local processes can forge both headers, so authentication is first-frame and server-proof-before-secret.

Pairing:
- `zatiti browser pair` (owner) creates the client_agent principal, binds it in the MAC'd label file, grants the exact relay allowlist plus event.list with no '*' narrow rules, writes an offer {code, principal_id, link_secret, expires_at} as a 0600 file under <state>/browser/offers/<id> (O_EXCL, consumed by rename), and prints ztp1.<port>.<128-bit code> carrying the link secret. The controller credential is provisioned into the SecretStore (browser/<label>/credential, transport=browser_listener) only when the hub consumes the offer at Bind; pair blocks until consumption or TTL, then revokes the unused principal.
- Handshake: hello{code_mac = HMAC(code, 'client'||nonce)} -> challenge{server_mac} -> auth{} -> welcome{link_secret once}.
- Codes are single use with a 120 s TTL. After 5 consecutive failures all pending offers are burned. A global limit of 10 failures per minute closes with 4429, and at most 8 offers may be pending.
- No pairing endpoint exists. This preserves the frozen 'no alternate admin endpoint' rule, and the CLI already reaches the SecretStore (helper_bridge_factory_darwin.go:61).

Resume: the hub proves HMAC(K_s, ...) with K_s = SHA-256('zatiti-gw-proof'||link_secret), stored in the paired record, before the extension sends its link secret.

Listener binding: identity marks browser credentials transport=browser_listener and the Unix, remote and HTTP handlers refuse such rows fail closed; MAC'd label files refuse every browser principal when missing, malformed or MAC-failing.

Relay fence:
- an exact allowlist: conversation.create, conversation.list, conversation.get, conversation.message.send, conversation.message.list, command.get, review.get and operation.get; everything else refused, and the grant is exactly the allowlist plus event.list with no '*' narrow rules;
- conversations with exactly {self, owner, worker} as participants;
- review.get and operation.get filtered to c7-c9 on the label's connection;
- data-free hints, with message hints fenced on conversation_id.

Re-authentication happens on every frame, including a 20 s JSON ping, so unpair (credential.revoke plus principal.revoke) closes the session with 4401 within 20 s.

Keepalive: a JSON ping every 20 s, a chrome.alarms reconnect, and a 60 s server idle timeout; Chrome 116 or later.

Close codes: 4401, 4403, 4408, 4409, 4429 and 1012.

Qualification results are recorded in apps/extension/test/QUALIFICATION.md.

The extension chats as a client_agent (not a delegated owner credential), with evidence from voice/service.go:150, server/stream.go:42, turns.go:92 and delivery.go:337; decide.go:135 denies non-human principals only when the review is HumanRequired, so browser-originated c8 and c9 reviews are created human_required by worker-scope policy and T10.2 additionally asserts a client_agent cannot decide a default-class review. Consequently it receives previews only for its own turns (CC19).

## Consequences
- The extension never holds a controller credential; the link secret alone is refused off the browser listener (transport=browser_listener rows fail closed).
- Pairing needs no running service besides serve.
- A port squatter learns nothing and cannot command the extension.
- Revocation is bounded to 20 s.
- Residual risks: a same-user process that reads the extension's IndexedDB holds only the link secret, which the identity layer refuses off the browser listener; a process that can read the Keychain can still reach the credential, bounded by the fence and review. Manual pairing is required, and the credential expires in 30 d (rotation is planned in E11). Loopback TCP remains reachable by every local OS user.
