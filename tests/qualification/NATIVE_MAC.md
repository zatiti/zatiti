# Native Mac release qualification

Run the qualification package independently on a clean native Intel Mac and a clean native Apple Silicon Mac. A Rosetta process does not satisfy the Apple Silicon case. Set `ZATITI_QUALIFICATION_EVIDENCE` to a private output directory. The read-only signed-artifact case accepts the final package, universal bootstrap app, architecture-specific credential helper, and the externally verified Developer ID Team ID:

```sh
ZATITI_QUALIFICATION_EVIDENCE="$EVIDENCE_DIR" \
ZATITI_MAC_TEAM_ID="$TEAM_ID" \
ZATITI_MAC_PKG="$FINAL_PKG" \
ZATITI_MAC_BOOTSTRAP_APP="$FINAL_BOOTSTRAP_APP" \
ZATITI_MAC_HELPER="$FINAL_HELPER" \
go test ./tests/qualification -run '^TestNativeMacRelease$' -count=1 -v
```

The test records artifact hashes, OS/source/toolchain versions, native hardware and process architecture, and executes `pkgutil`, `spctl`, `xcrun stapler`, `codesign`, and `lipo` against the supplied final artifacts. Missing inputs are `not_run`; a supplied artifact that fails a native check is `failed`. Each host emits cases only for its own native architecture. A non-Mac or Rosetta run emits a blocking host-prerequisite case instead. Review both hosts' private JSON case records and `release-report.json`; the absence of the other architecture in one report is never its passing evidence.

The installed package/bootstrap, signed helper/Keychain, and real Serenity/provider first-chat cases currently remain `not_run` by design. They need a controlled clean-host driver that actually invokes the verified bootstrap, checks the installed inbox and active trees, exercises upgrade/crash recovery and signed caller restrictions, and observes a committed first chief reply with Serenity's required protocol, writer, cost, reconciliation and backup behavior. Artifact checks, a screenshot, a fixture transcript or a supplied success JSON do not establish those observations. The overall release report remains unclaimable until those drivers and both native runs exist.

Revision 10 adds `QUALIFICATION.macos_install_to_first_chat` and the `macHostJourneyDriver` seam. The driver must observe four stages in one native run: verify the final signed delivery/script/bootstrap/pkg and pinned certificates; invoke the bootstrap and audit the installed inbox, active trees, helper, launcher and Keychain against those bytes; capture a disposable provider credential in the installed helper and compare the displayed chief reply with authenticated controller readback; and prove the pinned Serenity protocol, one writer, scoped memory, reconciliation, cost/disclosure and backup revision. It returns only redacted IDs, hashes and booleans derived from those real checks. Every stage carries one generated host-run ID; mismatched bytes, IDs or missing guarantees fail. A named missing prerequisite records `not_run`; an observed mismatch records `failed`. The `release-report.json` contains `mac_release` only after all four stages pass. No driver is currently registered, so no environment variable or supplied JSON can cause this case to pass or populate that object. A production driver requires coordinated native bootstrap/installed-UI control and final signed artifact inputs; this source tree cannot qualify the release without them.
