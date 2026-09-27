# Release evidence checklist (Mac)

**Status: draft (S6).** Use this checklist for the serialized release
review. Every item needs retained, secret-free evidence produced from the
exact release build. Passing controlled tests, compiling the code, or seeing
tests planned or skipped doesn't count as evidence. None of these items is
complete today.

## Build identity

- [ ] One release identity and source revision, bound to the Intel and Apple
      Silicon artifacts.
- [ ] Signed controller, desktop, credential helper, installer and bootstrap
      for each architecture, with the hash of each (P2).
- [ ] Developer ID signature, notarization and staple verified, and the Team
      ID pin checked (P2).
- [ ] A signed release descriptor, with its delivery bytes hashed.

## Services and provider

- [ ] Hosted Serenity public contract deployed, and its capability statement
      published (S1).
- [ ] Hosted OAuth binding works end to end: discovery, registration, PKCE,
      token rotation and revocation, and recovery after a crash (S2).
- [ ] The Serenity adapter's scoped operations pass, including
      reconciliation of lost acknowledgments (S3).
- [ ] Only completed, authorized, fresh recall enters the model context
      (S4).
- [ ] The exposed provider key is rotated before any live qualification
      (P1). Record the date only, never the key.
- [ ] One OpenRouter qualification probe result for the selected
      model and route, with the observed charge within the spend limit (P1).

## Clean-host journey (S5)

On a clean Intel Mac and a clean Apple Silicon Mac, with no developer tools
and no Rosetta:

- [ ] The literal one-line install succeeds, and the report binds the
      release identity to that architecture's artifact hashes.
- [ ] A first chat gets a real worker reply through the installed desktop and
      controller, and the reply reads back after the controller restarts.
- [ ] Native voice checks pass (V4): microphone permission, capture bounds,
      interruption, credential isolation, and disclosure that output is
      spoken.

## Recovery and support (S6)

- [ ] The desktop reconnects and recovers a lost acknowledgment through
      `command.get` against the release build.
- [ ] Provider key rotation (`connection.rotate`) and revocation
      (`connection.revoke`) work through the signed credential helper.
      Rotation first needs code: today a rotation job is admitted but never
      runs, so it stays pending.
- [ ] Backup, restore, and restart come back paused, then resume after
      reconciliation, on the release build.
- [ ] A decision on restoring onto a different Mac: either build and qualify
      key export and import, or state the limitation in user-facing docs.
- [ ] The [support](support.md) steps are verified, and diagnostics are
      confirmed free of secrets.
- [ ] Observability and schema-version findings are resolved or explicitly
      accepted.

## Approval

- [ ] Every item above links to its retained evidence.
- [ ] The README and install instructions are updated only after approval.
