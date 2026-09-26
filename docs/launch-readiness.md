# Zatiti launch readiness

Status as of 2026-09-26: **pre-release; Mac install-to-first-chat is not qualified.** The P00–P50 implementation dispatch is closed. The open work below is the current product/release roadmap; old cards are historical records, not active branches.

## Ordered launch gates

1. **Hosted Serenity setup and memory.** Implement the local helper's hosted OAuth discovery, public client registration, PKCE loopback callback, token custody in Keychain, reconnect/revocation, and account/project/scope confirmation. Then qualify the deployed hosted service's real bounded recall, provenance, freshness, and write/reconciliation guarantees. Wire completed governed recall into the immutable model context. Current code fails closed if an authorized memory binding requires recall that has not completed. Do not enable a capability until its real service evidence is recorded. This is blocked on service capabilities and a completed Zatiti OAuth/recall path, not on an abandoned implementation branch.
2. **Provider-backed first chat.** Qualify the selected provider/model through a real chat turn and read its controller-derived response back from durable conversation history. The OpenAI, OpenRouter, and Experiential Labs adapters and desktop provider/model settings have implementation and controlled tests, but no current live first-chat evidence is recorded. Rotate the Experiential Labs key that was exposed in tool output before using it; do not reuse the exposed value.
3. **Mac distribution.** Build controller and Flutter desktop artifacts for Intel and Apple Silicon, sign with Developer ID, notarize, produce verifiable release manifests, and test install, first setup/chat, update, rollback, uninstall, and state retention on clean native Macs. The packaging code assembles artifacts but does not itself prove a supported one-line installer or a qualified release.
4. **Recovery and support.** Preserve backup/restore qualification and close the remaining observability/schema-version findings noted in the historical roadmap. Verify user-facing recovery and key rotation/reconnect paths against the release build.
5. **Publish a single reviewed release.** Only after gates 1–4 pass, update the supported install command and platform matrix, publish signed artifacts/evidence, and announce the qualified scope. iOS, Android, web, Linux, and Windows remain later qualification tracks.

## Evidence available now

- The integrated full Go suite passed: `go test -p 2 ./... -count=1 -timeout=20m`.
- `go vet ./...` passed.
- The spec renderer check passed for 41 files, 37 scopes, 305 operations, 153 source blocks, and 147 cases. The generated MCP runtime mirror check and its synchronization tests passed.
- Flutter analyze and all 243 desktop tests passed from `apps/desktop`.
- The opt-in OpenAI live qualification test compiles and its default gated path passes; it did not make a live request.
- No real hosted Serenity recall, live provider first-chat, signed/notarized Mac installation, or clean Intel/Apple Silicon host run has been recorded in this work. The Go macOS distribution harness is controlled packaging evidence, not native signed-install evidence.

These results demonstrate source-level and controlled-test progress only. They do not close the launch gates.

## Repository integration status

Local `main` includes the reconciled `origin/main` history plus the integrated implementation commits; it is 82 commits ahead with no remote-only commits. The integration changes in this update are tested and ready for a draft PR. There are no side worktrees or local implementation branches. The GitHub description and marketing README describe the current pre-release status. No local implementation branch or worktree for the closed P00–P50 dispatch has been abandoned. Remote historical/topic refs remain for repository history; none are active local work items. The draft PR is the review/publishing step, not a release qualification claim.
