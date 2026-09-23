# Mac install-to-first-chat milestone

Status: proposed implementation plan, 2026-09-23. No release qualification,
publication, contract revision or implementation completion is claimed here.
This document coordinates future owner assignments; it does not authorize a
package to change sibling roots or weaken existing acceptance criteria.

Detailed dispatch instructions are in
[MAC_RELEASE_EXECUTION.md](MAC_RELEASE_EXECUTION.md): dependency graph,
contract freeze checklist, single-owner implementation tickets, setup state
machine, coding-model prompt template, fault tests and integration gates.
Use this document for product scope and the playbook for implementation order.

## Outcome

A person on a supported clean Mac can install Zatiti, open it, connect their
AI provider, confirm spending limits and receive a real reply from their
personal chief. They need no developer tools, environment variables, socket
paths, installation IDs, certificate files or manually started processes.

The same conversation survives closing the window, reopening, reboot/login
and a supported upgrade. Installation is one command or a graphical flow;
provider onboarding and necessary macOS consent are not bypassed.

## Proposed scope and defaults

- First release: Mac preview, local workspace, one qualified hosted provider,
  user-supplied API key. No Zatiti account or funded inference required.
- Apple Silicon (arm64) and Intel (x86_64/amd64) are both required launch
  targets, as requested by the user. Neither is deferred or optional. Freeze
  the supported macOS versions for each architecture against the pinned Flutter,
  native plugins, controller/runtime and service-registration versions in the
  foundation gate. Select a toolchain that supports both architectures; do not
  silently drop Intel support when upgrading dependencies.
- Produce native artifacts for both architectures under the same release
  version. Prefer separate architecture-specific installers initially, with
  automatic selection by the bootstrap command and Homebrew cask. A universal
  app is an alternative only after every bundled native component is verified
  for both architectures. Apple Silicon must not require Rosetta.
- One release version orchestrates separately verifiable desktop and controller
  distributions, plus any required qualified runtime. Keep business logic out
  of Flutter and preserve the independently managed controller lifecycle.
- Direct signed/notarized installer first; a project-controlled Homebrew cask
  wraps the same release. A hosted bootstrap command must also work without
  Homebrew, download prebuilt artifacts, verify them, install and open the app.
  Do not publish a placeholder installation URL or bypass Gatekeeper.
- Signed upgrade/reinstall with data preservation is required. Unattended
  background auto-update is deferred; a visible update action may launch the
  verified installer. Assign one update owner per installation channel.
- Linux, Windows, mobile, device pairing, cloud hosting, Mac App Store
  distribution and additional provider onboarding are follow-on milestones.
- Use the approved Zatiti mark in Finder, Dock, app switcher and installer.
  Normalize the chosen mark to a production master before exporting icon sizes;
  generated raster variants are not a consistent vector master.

## First-run flow

1. Install: verify OS/architecture, release signatures and component hashes;
   install a matched release without requiring Go, Flutter, Git or Xcode.
2. Open: present Welcome and a short explanation that workers continue when
   the window closes while this Mac is awake and the user service is running.
3. Prepare workspace: the packaged lifecycle helper registers/starts the
   controller; bootstrap creates the installation and owner once; provision
   credentials through OS secure storage and store protected nonsecret discovery
   metadata. Flutter discovers the connection without reading controller data.
4. Connect provider: enter key in a dedicated secure form, validate access and
   supported model, then show spending controls. Explain any paid validation
   before submitting it; never put credentials in chat, process arguments or logs.
5. Confirm limits: show the qualified model/pricing profile and distinguish
   enforceable Zatiti admission limits from external/advisory cost guarantees.
   Never substitute guessed prices or unsupported enforcement claims.
6. Start chatting: create/restore the pinned chief and direct conversation from
   acknowledged controller state. Show a ready composer only when prerequisites
   pass. Send a real prompt and display the persisted provider-backed reply.

Every step must resume after interruption. Bootstrap, chief creation and
submission retries must not duplicate durable objects or paid requests.
Offline launch, invalid key, locked Keychain, denied background permission,
unsupported model, exhausted limit and service startup failure get actionable
UI recovery. No silent demo fallback or plaintext credential fallback.

## Ordered work packages

| ID | Owner(s) | Deliverable and exit condition | Depends on |
| --- | --- | --- | --- |
| M0 | Integration/specification maintenance | Audit existing evidence; freeze preview capability matrix, OS/CPU targets, provider/BYOK scope and lifecycle design. Revise authored inputs to allow Mac-only qualification rather than requiring Linux release evidence too; regenerate prompts and check drift. Document all affected callers for new setup/discovery interfaces. | None |
| M1 | Integration foundation | Serialize toolchain/runtime/plugin/license/checksum lock work. Resolve live provider profile and Serenity blockers or land an explicit reviewed reduced-capability specification. A required unqualified dependency blocks release. | M0 |
| M2 | packaging, internal/platform, cmd/zatiti | End-to-end install/lifecycle helper, protected discovery and secure credential handoff. Start, inspect, repair and stop the user service with bounded results; retry install safely. Prove real Keychain custody and service behavior. | M0, M1 |
| M3 | internal/installation, identity, configuration, connections, accounting; controller/server as required | A resumable bootstrap-to-ready orchestration using public operations and existing compiler/acknowledgment rules. Close missing seams via coordinated contracts. Prove one owner, chief and conversation after interrupted setup. | M0, M1 |
| M4 | apps/desktop | Welcome, preparation progress/recovery, provider connection, limits and first chat. Load protected local profile by default; keep environment configuration as a development override. Replace default Flutter icon. Native accessibility and visual review of onboarding and chat. | M2, M3 |
| M5 | packaging, .github/workflows | Release-mode matched artifacts, signing/notarization/stapling, SBOM/notices, installer/bootstrap/cask, upgrade and uninstall. Stage artifacts for review, using protected release identity supplied outside the repository. | M1, M2, M4 |
| M6 | tests/integration, tests/qualification | Execute the clean-host matrix below against installed signed release candidates and a real provider. Record exact versions, observed results and failure evidence. | M3–M5 |
| M7 | Integration/release owner | Review evidence, confirm supported capability claims, prepare release notes and rollback instructions; publish only as a separate authorized release action. | M6 passes |

These broad milestones are integration gates, not instructions to implement
every row serially. After the contract/dependency baseline freezes, dispatch
independent ownership roots against the same conformance fixtures as described
in the execution playbook. Consumers may implement before producers land;
integration and qualification still require the real dependencies.

Do not dispatch root dependency writes alongside child-package writes. Use the
repository's isolated-worktree/one-landing-owner rules for parallel assignments,
with one active writer per root. This planning change does not start agents.

## Lifecycle and upgrade design decisions

- Prefer per-user ownership and no elevated controller. Evaluate Apple's
  SMAppService for the chosen deployment floor against the existing LaunchAgent
  packaging approach. Settle bundle/helper placement in M0: the current desktop
  manifest excludes controller/helper payloads, so embedding them requires a
  coordinated packaging/specification revision. A combined outer installer can
  retain separate component manifests without embedding a controller in Flutter.
- Quitting the UI leaves the controller running; explicit Stop controls require
  controller acknowledgment. Logout/sleep are not promised as continuous work.
  Reboot recovery is qualified after login and Keychain availability.
- Upgrades verify all components before activation, stop admission safely,
  preserve in-flight/unknown effect identity, then health-check the new version.
  Test interruption at each phase. Binary rollback is allowed only when data
  remains compatible; otherwise use a tested backup/restore recovery path.
- Uninstall removes service registration and binaries. Retain workspace data by
  default; deleting data and credentials is a separate explicit user choice.

## Release acceptance matrix

Run on every advertised architecture and the declared minimum/current supported
macOS versions, with no development toolchains installed. A debug launch or
synthetic installer fixture does not satisfy these gates.

Both native Intel and native Apple Silicon must pass before publishing the Mac
milestone. Build and test each architecture in CI, including native plugins,
controller and required runtime. Verify architecture selection and reject an
incompatible artifact before changing an installation. Sign and notarize both
release variants and exercise upgrades within each architecture. An Intel-only
or Apple-Silicon-only pass does not qualify the combined release.

The user's Intel laptop is a useful additional native smoke-test machine, but
its development environment cannot substitute for a clean Intel qualification
host. Also provide a clean Apple Silicon host. Select supported OS test versions
per architecture rather than assuming the newest macOS runs on both.

| Journey | Required observed result |
| --- | --- |
| Fresh install | Published-form command and graphical installer both install the same signed release and open the app; Gatekeeper verification works without overrides. |
| First chat | Provider/limits setup occurs wholly in UI; chief returns a real reply, backed by persisted controller state and recorded provider run evidence. |
| Interrupted setup | Relaunch resumes safely at each step, including after credential custody and bootstrap commit; no second installation, chief or conversation. |
| Provider failure | Bad credentials, unavailable model, no network and exhausted allowance show specific repair actions without losing the draft or claiming success. |
| Unknown send outcome | Disconnect after acceptance reconciles the original submission before retry; no duplicate message or uncontrolled paid replay. |
| Lifecycle | Close/reopen preserves chat; controller survives UI exit; service crash recovery, denied/disabled background item, reboot/login and sleep/wake recover honestly. |
| Secure storage | Locked/unavailable Keychain blocks safely; credentials never appear in logs, UI model context, diagnostics, process arguments or plain files. |
| Upgrade/reinstall | Prior supported installation preserves data and credentials, avoids duplicate services, negotiates compatible versions and recovers from interrupted activation/migration. |
| Uninstall/reinstall | Service and binaries are removed; retained workspace reopens on reinstall; explicit data deletion is separately tested. |
| Native UX | Correct Zatiti icons; readable dark/light onboarding and chat; keyboard, VoiceOver, text scaling and small supported window sizes work without hidden controls. |

Collect source revision, release/component hashes, exact toolchain/OS/hardware
and provider profile versions, signing/notarization results, redacted diagnostic
records, expected/observed outcomes and screenshots. Store only synthetic data
and nonsecret evidence. Set a first-run performance target after measuring the
candidate; do not promise a download or provider-latency SLA before measuring it.

## Known blockers and decisions

- Current startup reads connection information from environment variables;
  automatic discovery and protected provisioning need implementation.
- Packaging README status is not sufficient evidence: audit actual lock and
  qualification artifacts. The inspected dependency report pins Serenity source
  by inspection but records required capability failures; it also leaves the
  hosted provider's live model/pricing/profile qualification unresolved.
- Do not hide required Serenity capabilities behind an optional toggle. Either
  qualify the required runtime, or explicitly revise the release scope and all
  affected requirements through specification maintenance before shipping.
- Apple signing identity, secure CI credentials, release hosting/domain and
  clean qualification Macs must be supplied before M5/M6. No credentials belong
  in this plan or repository.
- BYOK and user-triggered updates are planning defaults,
  not previously approved product-contract changes. M0 settles them before
  dependent implementation. Support for both Apple Silicon and Intel is an
  explicit user requirement and a release gate.

## References

- Existing startup: `lib/src/app/startup.dart` and `lib/main.dart`.
- Existing distribution: `../../packaging/README.md` and `QUALIFICATION.md`.
- Ownership: `../../docs/implementation/README.md`.
- Dependencies: `../../docs/implementation/dependencies.lock.json`.
- [Flutter platform support](https://docs.flutter.dev/reference/supported-platforms)
  and [macOS release guidance](https://docs.flutter.dev/deployment/macos).
- [Apple service management](https://developer.apple.com/documentation/servicemanagement/smappservice)
  and [Developer ID distribution](https://developer.apple.com/developer-id/).

Completion means an evidenced Mac preview users can install and chat with,
not completed implementation tasks alone. Linux qualification follows this
milestone; Windows and mobile reuse the client/controller boundary later.
