# Zatiti launch readiness

Status as of 2026-09-25: **pre-release; Mac install-to-first-chat is not qualified.** The P00–P50 implementation dispatch is closed. The open work below is the current product/release roadmap; old cards are historical records, not active branches.

## Ordered launch gates

1. **Hosted Serenity setup and memory.** Implement the local helper's hosted OAuth discovery, public client registration, PKCE loopback callback, token custody in Keychain, reconnect/revocation, and account/project/scope confirmation. Then qualify the deployed hosted service's real bounded recall, provenance, freshness, and write/reconciliation guarantees. Wire completed governed recall into the immutable model context. Current code fails closed if an authorized memory binding requires recall that has not completed. Do not enable a capability until its real service evidence is recorded.
2. **Provider-backed first chat.** Qualify the selected provider/model through a real chat turn and read its controller-derived response back from durable conversation history. The OpenAI, OpenRouter, and Experiential Labs adapters and desktop provider/model settings have implementation and controlled tests, but no current live first-chat evidence is recorded. Rotate the Experiential Labs key that was exposed in tool output before using it; do not reuse the exposed value.
3. **Mac distribution.** Build controller and Flutter desktop artifacts for Intel and Apple Silicon, sign with Developer ID, notarize, produce verifiable release manifests, and test install, first setup/chat, update, rollback, uninstall, and state retention on clean native Macs. The packaging code assembles artifacts but does not itself prove a supported one-line installer or a qualified release.
4. **Recovery and support.** Preserve backup/restore qualification and close the remaining observability/schema-version findings noted in the historical roadmap. Verify user-facing recovery and key rotation/reconnect paths against the release build.
5. **Publish a single reviewed release.** Only after gates 1–4 pass, update the supported install command and platform matrix, publish signed artifacts/evidence, and announce the qualified scope. iOS, Android, web, Linux, and Windows remain later qualification tracks.

## Evidence available now

- The latest full Go test run passed: `go test -p 2 ./... -count=1 -timeout=12m`.
- The spec renderer check passed for 40 files, 36 scopes, 300 operations, 153 source blocks, and 147 cases.
- Flutter tests/analyze had passed before the final provider GUI edits; they must be rerun against the current tree.
- The opt-in OpenAI live qualification test compiles and its default gated path passes; it did not make a live request.
- No real hosted Serenity recall, live provider first-chat, signed/notarized Mac installation, or clean Intel/Apple Silicon host run has been recorded in this work.

These results demonstrate source-level and controlled-test progress only. They do not close the launch gates.

## Repository integration status

There are no side worktrees or local implementation branches left. Unique code rescued from the old `wave3/responses` branch is present in the pending main worktree. The GitHub description is updated; the README and current launch ledger are local changes and still need integration/publication. Local `main` and `origin/main` have diverged (74 local commits ahead, 29 remote commits ahead at the last check). Reconcile by a normal merge/rebase after the full current verification; do not force-push. Remote topic refs are not local worktrees and are not deleted by this plan.
