# Reconnect and key rotation

**Status: draft (S6).** This page covers behavior verified in the source. It
hasn't been verified on an installed, signed build.

## Reconnecting a client

The desktop, CLI, and MCP clients reach the controller over a private Unix
socket. A remote desktop can instead use an explicitly configured mutual-TLS
listener. Closing a client doesn't stop the controller. Work you have
already authorized keeps running.

### What happens when a connection drops

- **Reads reconnect on their own.** The desktop reloads snapshots and live
  reply streams. It never resends a mutation or audio when it reconnects.
- **A mutation whose acknowledgment was lost isn't assumed to have
  failed.** The client looks up what happened by the mutation's submission
  key with `command.get`. If the controller recorded the command, you get
  that recorded result. If the lookup can't establish an outcome, the client
  reports the outcome as unknown instead of guessing.
- **The desktop never shows an approval, pause, or creation as done until
  the controller confirms it.** While disconnected, the desktop shows cached
  history marked as stale and keeps your unsent drafts.

### Check a command after a lost acknowledgment

Run `command.get` with the same submission key and operation:

```sh
zatiti command get --json --input '{"scope":<SCOPE>,"submission_key":"<KEY>","operation":"<OPERATION_ID>","operation_version":1}'
```

`command.get` returns only commands submitted by the same principal (the
user or client identity that sent them). It never returns another
principal's command.

### Retry safely

Resend a mutation with **the same submission key and identical input**. The
controller deduplicates by key, so an identical retry can't run twice. A
retry with the same key but different input is refused with
`submission_conflict`.

## Rotating a provider key

A provider connection holds only an opaque reference to a credential in the
macOS Keychain. The key itself never appears in operation input, model
context, logs, or diagnostics.

### Same-account rotation

**Not yet available:** `connection.rotate` accepts a rotation request and
creates a pending job, but nothing in the current source runs that job. The
controller doesn't dispatch a validation probe for it, and no code replaces
the stored credential or completes the job. The job stays `pending`. Until
that's implemented, use [Revoke a connection
immediately](#revoke-a-connection-immediately) for a compromised key, and
expect that setting up a replacement connection is also incomplete (see the
note in that section).

The designed behavior is as follows. `connection.rotate` replaces the
credential of an existing connection with another credential **for the same
provider account**. The controller first runs a validation probe with the
new credential and checks that it belongs to the same account. Only then
does it replace the old one. If the new key belongs to a different account,
the replacement doesn't happen. Changing accounts means creating a new
connection and getting it reviewed.

What the current source already enforces when it accepts the request:

- A generic MCP connection is refused with `capability_unsupported`, because
  it can't prove the account is the same.
- A revoked or inactive connection is refused.
- A request whose `expected_version` doesn't match the connection is refused
  with `stale_version`.

1. Create the new key in the provider's dashboard. Don't revoke the old key
   yet.
2. Store the new key through the signed local credential helper. In the
   desktop, open the provider connection and select **Set API key**. The
   desktop never accepts the key itself. The helper stores it in the Keychain
   and returns an opaque store reference.

   **Not yet available:** the signed credential helper isn't qualified.
   Signing, notarization and native Keychain behavior are still release work
   (P2 and S5), so this step works only in a development build.
3. Rotate the connection, passing its current version and the new store
   reference:

   ```sh
   zatiti connection rotate --submission-key <KEY> --json --input '{"scope":<SCOPE>,"id":"<CONNECTION_ID>","expected_version":<VERSION>,"store_ref":"<STORE_REF>"}'
   ```

   Rotation runs as a job. Follow it with `job.get` using the returned job
   ID. A job status never treats the provider accepting a request as
   confirmed success. In the current source the job stays `pending`; see the
   note at the start of this section.
4. After the job succeeds, revoke the old key in the provider's dashboard.
   Don't revoke the old key while the job is still pending, or the
   connection is left without a working credential.

### Revoke a connection immediately

If you think a key is compromised, revoke the connection first and replace
it afterwards:

```sh
zatiti connection revoke --submission-key <KEY> --json --input '{"scope":<SCOPE>,"id":"<CONNECTION_ID>","expected_version":<VERSION>}'
```

Revocation blocks any further use of the credential and any new dispatch
through the connection immediately. It keeps a record of effects whose
outcome is still unresolved, along with an obligation to clean up the stored
secret. Revoking the connection doesn't revoke the key at the provider; do
that in the provider's dashboard.

**Not yet available:** a working replacement isn't established yet. A new
connection must pass `connection.validate` before it can be used, and that
validation hasn't been shown to complete end to end against a live
provider. Plan for a revoked connection to stay unusable until a
replacement is validated.

## Rotate the exposed OpenRouter key before the qualification probe

Launch readiness workstream P1 requires rotating a previously exposed
provider key before any live qualification runs. The OpenRouter
qualification probe (`TestOpenRouterProfileQualificationProbe`) reads its
key from a named macOS Keychain item, never from an environment variable.

1. In the OpenRouter dashboard, revoke the exposed key and create a new one.
   Set a spend limit on the new key.
2. Store the new key in your login Keychain. When you omit the value after
   `-w`, `security` prompts for it, so the key stays out of your shell
   history:

   ```sh
   security add-generic-password -U -s <SERVICE> -a <ACCOUNT> -w
   ```

3. Confirm the item can be read. This command prints only whether it
   succeeded, never the key:

   ```sh
   security find-generic-password -s <SERVICE> -a <ACCOUNT> >/dev/null && echo readable
   ```

4. Run the probe as described in its test file and in the pull request that
   added it. The run is one billable request. Don't rerun it after an
   unknown outcome until you have established that the request didn't
   execute.

Passing the probe covers only its own step of P1. It doesn't qualify a first
chat through the installed desktop and controller.
