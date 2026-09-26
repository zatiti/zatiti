# Implementation assignment: `internal/policy`

Generated specification revision 17; source digest `f77034332a96396a9f88395f71ff528f051f96f35b398629236dd00bc731c08f`. This file is committed implementation context. Do not independently edit it. Everything required from the product specification and adjacent interfaces is embedded below; no RFC copy is required.

## Mission and scope

Own deterministic authority intersection, standing/exact-review policy and earned autonomy.

Write scope: **`internal/policy/` only**, excluding this generated AGENTS.md. Go package name: `policy`. Ownership kind: domain; integration wave: 2.

Allowed production imports from this repository: `github.com/zatiti/zatiti/internal/contract`. Tests may use interfaces/fakes defined locally and, once available, storage-backed temporary fixtures; integration owns cross-package tests. No sibling raw SQL. No root dependency edits except the integration exception stated in its own brief.

## Implementation decisions and acceptance focus

Own policy_definitions/restrictions/promotion_rules/qualifications and immutable evaluation evidence links. Policy rules are interpreted bounded declarative conditions only. Supported condition keys: max_cost (Money), required_classification (enum), not_before/expires_at (UTC), required_tool_version (Ref), required_worker_id (ID), required_review (bool); reject unknown executable conditions. Intersect current principal grants, hierarchy ceilings, project/binding, worker/task envelope, restrictions and current resources. Explicit deny wins; missing required condition refuses admission. Default publication/outbound messages/merge/deploy/account substitution/permission expansion require eligible owner review absent narrower prior standing policy. Expansion always under old policy. Promotions bind capability/destination/worker/model/tool/skill versions/window/rule version and independently established evidence; workers cannot approve own evidence, change evaluator/criteria or widen ceiling. Permit automatic narrow grant only under previously approved rule. Relevant changes invalidate affected qualification before new admission; incidents immediately restrict/demote before continued work. Human-required classes persist until owner explicitly changes governing rule. Model summaries and memory judgments are evidence candidates, never policy authority.

Local proving focus: Deny intersection, missing condition, self-grant, old/new policy race, precise human-required class, qualification evidence independence/version binding, auto demotion before claim, parent ceiling.

## Incoming and outgoing boundaries

Incoming callers: accounting, application, configuration, connections, effects, execution, installation, memory, messaging, reviews, skills, tasks, application (authenticated public operations).

Outgoing owner calls: `_identity.authority`, `_identity.restrict`, `_identity.promote`, `_configuration.stage`, `_configuration.snapshot`, `_reviews.ensure`, `_reviews.check`, `_accounting.inspect`, `_tasks.snapshot`. Each exact input/output schema appears below. Calls retain current Unit/authority; owner allowlists are mandatory.

Expose `New(contract.Dependencies) (*Service,error)`; `*Service` implements `contract.Module` with Name `policy`, owner-prefixed migrations, all owned descriptors, and strict dispatch. No calls/goroutines during construction. Implement optional authentication/LocalIO interfaces where specified in the common contract. Tables are private under `policy_`; external callers rely only on methods and schemas.

### Imported package APIs and behavior

These briefs are embedded so you need not read a sibling prompt to discover its incoming API. Implement only your own package.

## Shared foundation contract

# Frozen implementation contract, revision 17

These decisions complete the product specification and bind every scope. Report contradictions with an affected-dependency list and proposed coordinated revision; do not change another owner's interface locally.

Revision 13 permits a Responses v2 provider-conversation evidence record to omit `session_handle` when its physical call was not authoritatively successful. A confirmed prepare-session still requires a nonempty handle; stateless evidence still forbids it. This preserves honest unknown/failure outcomes without inventing a provider session identifier. Execution persists the model-step index alongside each effects operation reference and accepts a callback only when that exact reference, route step, current turn step and `model_pending` state agree. Chat observations use the existing controller-only `_execution.turn.observation` operation and never fabricate a task or Attempt.

Revision 14 carries the transaction-pinned, secret-free context recipe in the internal-only ContextPlan so the trusted ContextPerformer can rebuild the exact context outside a Unit without opening owner tables or inventing artifact identities. The controller stages and publishes the bytes through BlobStore and `_artifacts.publish`; execution alone commits the returned owner-minted artifact after rechecking plan generation, authority and pinned references. The recipe is never a public client field or model-callable input.

Revision 15 adds `execution_profile.qualify` as the only path from a client-authored provider-profile draft to executable capability evidence. The strict `ExecutionProfileCandidate`/`ResponsesProfileDraft` schemas contain all selected provider, model, route, price and resource bounds but have no `capability_evidence` field. The operation creates one durable job and one separately admitted, explicitly cost-bounded qualification effect bound to the exact active connection version, candidate profile digest, destination, route and requested capability set. The qualification probe uses fixed non-sensitive input, one physical provider request, the normal credential resolver, current principal/scope/classification/disclosure checks, current cost reservation and the exact provider adapter. No fallback, retry, redirect, SDK retry or hidden auxiliary call is permitted. Before dispatch, persist the exact bounded request context through the ordinary artifact path. On authoritative response, the controller records the physical-call evidence and publishes a qualification artifact; only the trusted completion writer can construct a `QualifiedExecutionProfile` and its evidence bound to the canonical candidate digest. Provider refusal is a failed job. Lost acknowledgement after request bytes may have been sent is `outcome_unknown`, remains visible on the same job and cannot be silently resent or turned into a qualified profile. A caller can start a fresh qualification only after authoritative non-execution of the prior probe. Completion does not create or activate an execution profile: Desktop consumes the trusted result through the existing `execution_profile.create` → plan → explicit apply path. The existing context preparation/publication/commit generation and authority fences remain unchanged.

Revision 16 makes the qualification candidate resolution private and effect-bound. `_configuration.execution_profile.qualification.resolve` returns only the exact pending candidate and canonical digest to Effects. `_effects.prepare` may name that qualification ID only from the configuration owner; Effects verifies its connection, endpoint, model, price and requested bounds against the immutable action, then persists the candidate profile on the effect. Ordinary public effects still require an exact already-qualified execution profile, and cannot set the qualification ID or inject `Dispatch.adapter_profile`. The durable job links to the one effect operation so the controller can complete that same job from the exact physical observation.

Revision 11 selects **hosted Serenity as the primary Mac memory service**. The user's existing hosted personal brain is the default personal-chief brain, including when another client already uses it. Zatiti does not create or import a duplicate personal brain during setup. Serenity remains the canonical memory writer; Zatiti retains local execution, authorization, accounting, conversation and recovery state. Separate restricted worker or project brains, when required by the existing isolation contract, are separate projects within the same hosted Serenity account and require explicit grants. The free tier may be offered, but no paid entitlement, quota, extra brain, or successful memory call is assumed from sign-in alone. The local Serenity distribution path is optional future/self-hosted packaging; the Mac release descriptor, controller manifest, bootstrap, installer and LaunchAgent do not require or start a bundled Serenity binary or local read facade for the hosted mode. Preserve the rev10 installer and trust chain for Zatiti's controller, desktop and credential helper.

**Trusted browser setup.** `connection.setup.begin(method=browser)` is an intent boundary, not a preassembled URL from user-provided metadata. A signed installed helper owns exact OAuth protected-resource and authorization-server discovery from the fixed `https://serenity.sire.run/mcp` resource, dynamic public-client registration, an ephemeral numeric-loopback callback, S256 PKCE, state, nonce, deadline, system-browser launch, exact callback validation, code exchange and rotating token custody in the existing installation-local Keychain-backed SecretStore. Discovery endpoints and redirects must remain on the configured Serenity origin except the registered exact loopback callback; TLS, byte/time bounds and issuer/resource matching fail closed. No authorization code, verifier, access/refresh token or raw credential enters Dart, a public operation, argv, logs, model context or an environment variable. `connection.setup.status`, `command.get`, the durable challenge and a protected keyed helper intent reconcile browser denial, callback loss, token-response ambiguity, refresh loss, crash and retry without silently creating a second grant or replacing a different account. The helper returns only a redacted status. A revoked, expired or mismatched grant makes the connection unavailable and offers a fresh sign-in, never a plaintext token fallback.

**Verified brain binding.** OAuth consent selects exactly one existing hosted project/brain and read or read/write scope. The helper must call a public bearer-protected hosted binding endpoint after token exchange; that endpoint must return the issuing resource, stable account ID, selected hosted project ID, effective scopes, grant identity and current project state for this exact token. A browser callback or token response alone is insufficient to assert those values. `Connection.hosted_memory_grant` is server-observed, nonsecret metadata populated only by trusted setup/validation, excluded from user-authored connection definitions and model-supplied fields. `zatiti.serenity/v2` adds `SerenityHostedBrainMapping` with local `brain_id`, exact `hosted_project_id`, verified `connection_id`, endpoint and classification; the adapter checks it against the current connection grant. A local Zatiti UUID brain maps explicitly to one verified hosted project ID and one connection/grant; the hosted alphanumeric ID is never parsed as a UUID. The personal-chief mapping may target the user's already populated hosted brain. The account/project choice is displayed before activation; switching either requires a new explicit binding and current authority. No organization/worker automatically inherits the personal brain. A current connection/grant check precedes every memory dispatch, and revocation restricts use immediately.

**Hosted adapter and release gate.** The adapter uses the public hosted MCP Streamable HTTP endpoint, performs its session handshake as separately identified, bounded and accounted transport work, and never claims one MCP tool call is a sessionless physical request. It pins a verifiable server build/capability statement and fail-closes on missing or changed semantics. Serenity must expose public, authenticated, brain-scoped capabilities sufficient for the existing full `recall`, `remember`, `inspect`, `promote`, `retract`, `export_revision`, authoritative command-status, freshness, source/lineage, cost and disclosure guarantees; a hosted quota is not a Zatiti per-call hard spend bound. Zatiti's backup must pin and verify an immutable hosted brain revision and export/recovery path; an empty brain list or a web dashboard is not backup evidence. Until those upstream surfaces are implemented and qualified, the adapter stays unavailable and the Mac install-to-first-chat release remains blocked. Native Intel and Apple Silicon qualification must sign in through the real browser flow, verify the selected pre-existing brain identity and scoped read/write, perform and read back a real memory effect plus a real provider reply on the same clean-host release chain, and prove revocation/reconnect and backup revision behavior. This changes `internal/connections`, `internal/platform`, `internal/adapters/serenity`, `internal/memory`, `cmd/zatiti`, `apps/desktop`, `packaging`, CI and qualification; no synthetic OAuth fixture is live-service evidence.

Revision 10 coordinates the **post-package activation seam and linked clean-host evidence**. Revision 9's signed package, Keychain, helper and full Serenity gates remain unchanged. It adds no public operation or delivery field. A compiled binary, unsigned candidate, inert package inbox, independent case passes, or provider response without the same-host installation chain cannot establish a Mac install-to-first-chat release.

**Bootstrap phase contract.** `packaging.RunMacBootstrap` retains the signed delivery, package verifier, native architecture detector and protected watermark lock. Its production input additionally requires the current user's exact `Home` and fixed default Mac `StateDir`, a nonnil `MacInstalledInboxVerifier.VerifyInstalled(ctx, home, release, plan, binding) (MacStagedAssets, error)`, `MacMasterKeyProvisioner.Provision(ctx, stateDir) error`, and `MacReleaseActivator` with `Activate(ctx, release, plan, verifiedInbox) error` and `Audit(ctx, release, plan) (bool, error)`. These are packaging-owned interfaces; `packaging` remains standard-library-only. The entrypoint at `cmd/zatiti` is explicitly allowed to import `packaging` and injects `platform.ProvisionMacMasterKey` as the provisioner. `Home` and `StateDir` are checked against the current login user's real home and `$HOME/Library/Application Support/zatiti`; a caller cannot choose another package target or master-key account. The production package verifier proves final SHA, actual bounded package payload/BOM/Distribution/PackageInfo, out-of-band Developer ID Installer Team ID/certificate, stapled notary ticket and Gatekeeper install assessment before the fixed system-installer call. Only after installer success does `VerifyInstalled` re-open the exact three owner-only inbox files, compare binding and archive bytes against the signed plan, and return those verified archive paths. Under the same bootstrap lock, `Provision` then establishes the retained master item before `Activate` builds the ordinary `Plan` from the two verified archives and calls `Apply`. `Audit` checks both active signed trees, helper and fixed LaunchAgent against the descriptor before the accepted watermark advances. `MacInstallRunner.Installed` or a package receipt cannot substitute for `Audit`; an inert inbox is not an active release. On same-sequence rerun and after a crash between `Apply` and fence publication, re-provision without rotation, audit the exact active release and only then return or advance the fence. A different accepted digest, partial tree, uncertain audit or absent production capability fails closed. Existing injected-fake tests establish orchestration only; a production bootstrap cannot use a fake verifier/runner. This revision affects `packaging`, `cmd/zatiti`, `internal/platform`, `tests/integration`, `tests/qualification` and CI.

**Helper recovery record.** The owner-only bounded per-connection intent may retain the nonsecret installation/connection/challenge IDs, expected versions and expiry, account identity, a stable credential name, the opaque store reference and one submission key for each intended begin/complete call. It records an exact canonical request digest (or the bounded exact request bytes) before dispatch so a retry cannot silently change the receipt, expected version or submission key. The reader validates phase-dependent required fields and refuses malformed or partial records; no provider key, HMAC key or owner Authorization header enters the intent. This replaces revision 9's overly narrow phrase that listed only the reference and IDs; it does not authorize extra public fields. Terminal and AppKit entrypoints use the same recovery rule. This affects `cmd/zatiti`, `internal/connections` and qualification crash fixtures.

**Linked native release proof.** `QUALIFICATION.macos_install_to_first_chat` is one named live case per native architecture on a clean account without developer tools. Its redacted `zatiti.ci.mac_release_host/v1` evidence binds a unique host-run ID and OS build, exact signed release version/sequence, descriptor/delivery/script/universal-bootstrap hashes, out-of-band signer Team ID and certificate hash, and that architecture's installer/controller/desktop/helper hashes to the fixed package install, installed-inbox and active-tree audits, controller/Keychain readiness, and one real first provider reply read back from the authoritative conversation. The same run also proves the full Serenity writer, scoped memory, reconciliation, cost/disclosure and backup-revision gates. Intel and Apple Silicon reports must agree on shared signed release identity and bootstrap hashes, have distinct host-run IDs and matching native architectures, and each pass the linked case. Architecture-specific package/component hashes need not match each other. Missing fields, planned/skipped/not-run cases, synthetic markers, a report from Rosetta, or independent reports without this linkage block the one-line release. This affects `tests/qualification` and `.github/workflows`; no credential or raw model text is evidence.

Revision 9 freezes the **Mac installed package binding and local provider-key capture boundary**. It changes no public operation schema, release descriptor, delivery schema, or Serenity requirement; the component manifest gains one Mac-controller-only executable artifact kind `credential_helper` at exact relative path `bin/zatiti-credential-helper`. Both native amd64 and arm64 must satisfy the same full first-chat release gates; a reduced-capability preview is not implied. In particular the pinned Serenity public protocol, one canonical writer per brain, scoped recall/curation, command reconciliation, cost/disclosure bounds and backup revision behavior remain hard release prerequisites. A missing or unqualified guarantee blocks the release rather than becoming a silent fallback.

**Data-only per-user package.** For each architecture, the signed `zatiti-<version>-darwin-<arch>-installer.pkg` is a single-choice flat product package with a distribution domain allowing only `currentUserHome` (`enable_currentUserHome=true`, `enable_localSystem=false`, `enable_anywhere=false`, no admin authentication). The package contains exactly one component payload rooted at `Library/Application Support/zatiti-installer/inbox/<release_sequence>/<arch>/` relative to the selected home. That inbox contains exactly three owner-only regular files: `binding.json`, `controller.tar.gz`, and `desktop.tar.gz`; it has no install scripts, executable payload, extra component/choice, symlink, hard link or path outside that subtree. The final app, controller version tree, LaunchAgent and state are activated only by the trusted bootstrap's existing `packaging.Apply` path **after** package installation and re-verification; the package itself does not start services or modify the active distribution. A package opened directly in Installer may stage inert bytes but does not constitute an installed/runnable Zatiti release. Incomplete inbox data is quarantined or removed without changing the accepted release. This layout is intentionally separate from the revision-8 accepted watermark and controller state.

The strict canonical UTF-8 `zatiti.mac_pkg_binding/v1` `binding.json` is at most 4096 bytes and has exactly `schema`, positive `release_sequence`, `version`, `arch` (`amd64` or `arm64`), `release_descriptor_sha256`, `controller_sha256`, and `desktop_sha256`; all hashes are lowercase SHA-256. Its descriptor digest covers the **inner canonical signed MacReleaseDescriptor bytes** already verified under revision 7, while the component digests cover the exact archived bytes named by that descriptor's corresponding delivery assets. The bootstrap separately verifies the outer signed delivery record and its installer asset hash. The binding never contains the delivery digest, package digest, URL, signing key, or mutable path: the delivery record contains the package hash, so placing that hash or the delivery digest inside the package would create a circular dependency. The sequence/version/architecture and both component digests must match the already verified delivery plan. Duplicate JSON keys, unknown fields, noncanonical bytes, extra/missing payload entries or unsupported schema fail closed.

Before activation, `packaging` must inspect the actual bounded package payload bytes and script/component inventory, not infer them from `pkgutil --expand` metadata, a receipt, filenames or outer SHA alone. Verify its final download size/SHA against the signed delivery, Developer ID Installer signature and pinned **out-of-band** Team ID/certificate requirement, stapled notarization and local Gatekeeper install assessment; `pkgutil --check-signature`, `xcrun stapler validate` during producer qualification, and `/usr/sbin/spctl --assess --type install --verbose <pkg>` are diagnostic evidence, not substitutes for parsing payload bytes and comparing the exact trusted signer. The end-user bootstrap may use only system tools and native Security APIs; it must not require Xcode, `xcrun` or disabled Gatekeeper. Refuse a failed or unavailable trust check. Invoke only `/usr/sbin/installer -pkg <privately-staged-verified-pkg> -target CurrentUserHomeDirectory` with fixed arguments, no `sudo`, alternate target, script-provided command, or inherited secret. After installer returns, check the installed inbox owner/modes/no symlinks and hash every byte again against the binding and already downloaded components; only then invoke `Apply` to stage the two signed component trees and fixed LaunchAgent, audit both active trees and launcher against the descriptor, and advance the revision-8 watermark while holding its bootstrap lock. A crash between package staging, Apply and fence advancement is reconciled from exact signed bytes and an authoritative active-tree audit; never trust a package receipt or rerun an ambiguous activation blindly. The package verifier/runner must be a real production capability before `RunMacBootstrap` may activate; injected fakes prove only orchestration. Producer CI builds and signs nested code, package-signs the final bytes, notarizes/staples, verifies both architecture-specific packages, then computes delivery hashes and signs the delivery record. The actual Team ID, certificate identity, notarization account, hosting URL, trust-root rotation and native clean-host results are external release inputs/evidence, never invented source constants or completed claims. Apple documents the [per-user distribution domain](https://developer.apple.com/library/archive/documentation/DeveloperTools/Reference/DistributionDefinitionRef/Chapters/Distribution_XML_Ref.html), [package-signature diagnosis](https://developer.apple.com/documentation/security/resolving-common-notarization-issues), and [Gatekeeper package assessment](https://developer.apple.com/library/archive/documentation/Security/Conceptual/CodeSigningGuide/Procedures/Procedures.html); the exact fixed invocation and payload parser still require qualification on supported macOS versions.

**Activation-time master-key custody.** While holding the revision-8 bootstrap lock, after validating/staging the package inbox and before `Apply` may start the controller, the trusted local bootstrap composition injects `platform.ProvisionMacMasterKey(ctx, stateDir) error` into `packaging.RunMacBootstrap` through a narrow `MacMasterKeyProvisioner` capability and calls it for the fixed default state directory. `packaging` never imports `internal/platform` or derives a Keychain service itself. It creates or validates the owner-only 0700 state root and durable 0600 `instance.id` using the platform's existing instance-ID rules, derives the existing service name `com.zatiti.zatiti.v1.<first 16 hex instance characters>`, and addresses exactly the generic-password account `master` in the current user's **login Keychain**. It performs a create-only Security.framework `SecItemAdd` of 32 CSPRNG bytes encoded in the existing SecretStore base64 representation; it must never call the current `Put` replacement path for an existing master item. On `errSecDuplicateItem` or rerun, it reads and strictly decodes the existing item through the same backend, checks exactly 32 key bytes, and retains it unchanged. Lock, ACL refusal, malformed value, missing instance identity, race or uncertain add/read result fails closed; after a crash following a successful add, the next run reads the same item and continues. No master bytes enter stdout, log, argv, environment, database, inbox, package or backup, and temporary mutable copies are cleared where feasible. The provisioning call returns only readiness/typed error, never the bytes or a caller-selected account. It never rotates or deletes the item automatically; upgrade and ordinary uninstall retain both `instance.id` and the Keychain item. Explicit destructive data deletion requires a separate owner action and backup warning. The installed controller LaunchAgent uses `--credential-backend keychain --master-key secret:master` against the exact default state directory; these are fixed nonsecret selectors, not caller input. The GUI helper uses the same fixed selectors internally. Before claiming readiness, verify controller and helper reopen the *same* instance and key. The trusted command/bootstrap composition binds the platform implementation before the controller LaunchAgent starts; a nil production provisioner blocks activation. This is a coordinated `internal/platform` + `packaging` + `cmd/zatiti` capability; a data-only pkg cannot provision the key itself.

**One-line bootstrap entrypoint and trust distribution.** The published command is generated only from final signed/hosted artifacts and contains an immutable literal SHA-256 for a bounded `zatiti-bootstrap.sh` downloaded from one fixed HTTPS origin into an owner-only temporary directory. It downloads the script with redirects disabled, verifies the exact literal hash using system `/usr/bin/shasum`, then invokes `/bin/sh` on that verified local file; it never pipes network bytes to a shell. The short script contains only fixed nonsecret URL/SHA-256 literals for a universal amd64+arm64 `ZatitiBootstrap.app.zip` on that same origin, downloads with redirects disabled and a byte cap, verifies the archive hash, extracts into the private directory using system `/usr/bin/ditto`, rejects extra/symlinked/unsafe entries, verifies the exact signed `ZatitiBootstrap.app` designated identifier and externally pinned Team ID plus stapled notarization/Gatekeeper assessment, then launches only its `Contents/MacOS/zatiti-bootstrap` executable with no user-supplied arguments or inherited secret. The app is a universal, separately signed/notarized native wrapper around `RunMacBootstrap`; it embeds the trusted Ed25519 delivery public keys and fixed stable-channel metadata URL, with no fetched trust key or private key. It detects **hardware** architecture, including Rosetta, and selects only the matching three assets from the verified revision-7 delivery record. The script and app archive hashes are pinned in the release-generated command/script respectively; both must be regenerated when their bytes change. The README/release page is the trusted distribution point for the literal one-line command, and the signed release CI records its URL/hash/Team ID/entitlements/notary evidence, while key rotation ships an overlapping bootstrap before a new delivery signer is used. This bootstrap app is a seventh, independently pinned distribution artifact outside the six-asset delivery record, so it cannot derive its own trust from fetched delivery metadata. A user may also install via a reviewed cask with its own literal package SHA pin. No hostname, command hash, Team ID, signing identity or working public command is filled with a placeholder and advertised before actual hosting, signing, notarization and both clean-host runs. If the universal bootstrap wrapper or its script cannot pass those checks, the one-line install claim is blocked, even if `RunMacBootstrap` unit tests pass.

**Signed native credential helper.** The architecture-matched Mac controller distribution carries one separately signed AppKit executable as manifest artifact kind `credential_helper` at `bin/zatiti-credential-helper`; the desktop bundle continues to carry no secure helper. Its installed path is exactly `$HOME/Library/Application Support/zatiti-dist/current/bin/zatiti-credential-helper`, reached through packaging's owner-checked `current` link. Its signing identifier is `com.zatiti.credential-helper`, with the actual Team ID supplied by trusted release configuration. Verify the helper's declared SHA, architecture, code signature and pinned designated requirement before launch, and use a parent/responsible-process launch constraint requiring the signed Zatiti Flutter Runner. The native Runner alone resolves this fixed installed path from the protected default installation layout and spawns it directly; neither Dart nor discovery metadata supplies an executable path. A Flutter-to-Runner method channel request `zatiti.gui-credential-capture/v1` contains exactly `schema`, lowercase UUID `installation_id`, and lowercase UUID `connection_id`, at most 4096 bytes. It is allowed only for an installed local Mac profile after authenticated installation identity comparison; development/remote/headless modes report capability unavailable. The Runner serializes one capture, uses a bounded cancellation/deadline, and invokes the helper with fixed nonsecret arguments `capture --installation-id <UUID> --connection-id <UUID>` and a sanitized environment/closed unrelated descriptors. No raw provider key, owner header, receipt, store reference, master-key reference, state/socket path or arbitrary argument crosses the method channel or argv/environment. The helper's bounded, strict result to Runner is only `zatiti.gui-credential-capture-result/v1` with `schema`, `status` (`completed`, `cancelled`, `retryable`, `repair_required`), and a fixed nonsecret `reason_code`; it contains no free-form diagnostic, credential, reference or receipt. The Runner returns that redacted result to Dart, which reads authoritative `connection.get` and `connection.setup.status` before showing ready state.

The helper runs in the user's interactive GUI session and presents an `NSSecureTextField` with a locally fetched provider/account label and explicit consent. The field accepts a nonempty bounded UTF-8 provider credential (maximum 4096 bytes); cancel and expiry make no new connection authority. The helper itself resolves strict protected `desktop.json`, authenticates to the private socket using the existing installed owner Keychain item, verifies live installation ID, and uses the fixed installed default state directory and Keychain backend with the fixed nonsecret master key selector `secret:master`. Installer provisioning must establish and retain that master item in the same installation-local login Keychain service; absence, lock, ACL refusal or a mismatched launcher configuration is a named repair prerequisite, never a plaintext fallback. The currently used login-Keychain backend is not silently replaced by a data-protection access group; signed Runner/helper/controller access and continuity across upgrades need real Keychain/ACL qualification. The helper owns `connection.get` → `connection.setup.begin(method=store_reference)` → UI entry → `SecretStore.Put` → helper receipt → `connection.setup.complete` through the same trusted engine as the terminal helper. Both `setup.begin` and `setup.complete` are keyed mutation requests: mint and durably retain one submission key per intended call, pass it in the existing request envelope, and use `command.get` plus challenge/connection status to reconcile an ambiguous response before replaying the same bytes with that same key. A changed challenge/version/receipt requires a new intended call and key, never a blind retry. The existing terminal helper must obey the same rule; its current `callOperation` omits `SubmissionKey` and permissive fakes do not establish correctness. The receipt remains challenge/account/expiry bound and is verified by `internal/connections`; raw bytes never enter a public operation, MCP/model context, stdout/stderr, log, Dart heap, argv or environment. `NSSecureTextField` masks input, but no claim is made that Swift/Foundation/Go process heaps never hold transient copies. The concrete shared bridge is one **process**: build the Go trusted setup engine from `cmd/zatiti` as a Mac-specific `-buildmode=c-archive` target and statically link it into the Swift/AppKit helper. Keep Swift source/bridge headers in the `cmd/zatiti` ownership root and build architecture-specific signed helper binaries for the controller manifest. The narrow C ABI is `Prepare(installationID, connectionID) -> opaque in-process session handle plus bounded nonsecret provider/account label`, `Commit(handle, const uint8_t *credential, size_t length) -> fixed redacted status`, and `Cancel(handle) -> fixed redacted status`; the Go engine owns discovery, Keychain, authenticated operations, submission keys, pending intent, SecretStore and HMAC receipt. Swift obtains the field value only after consent, rejects >4096 UTF-8 bytes, passes a transient byte pointer directly to linked Go code, and clears mutable buffers where feasible. The pointer is never persisted, passed to another process, logged or returned; Go copies only as required for SecretStore.Put and clears its own temporary byte slice. Handles are process-local, unguessable and single-use. The terminal helper calls this same Go engine with its bounded terminal reader. This bridge adds no provider-key IPC and no second HMAC implementation; qualifying the Go c-archive/AppKit link and Swift memory behavior on both CPUs remains mandatory.

After `Put`, the helper records a protected per-challenge pending intent with the opaque store reference and nonsecret replay fields frozen by revision 10 before attempting completion; it is owner-only, bounded, atomic and removed after authoritative completion/cancel/expiry cleanup. An ambiguous `setup.complete` response or helper crash is reconciled by `connection.setup.status` and `connection.get` under the same installation identity before retrying. If completion committed, report completed and retain the referenced key; if pending and unexpired with the same version, reuse its stored reference and receipt; if cancelled/expired/stale, delete only the recorded orphan reference after confirming it is not the effective connection credential. Never silently begin a second challenge or rotate a key to repair a lost response. The helper does not need an XPC service; adding one would require another coordinated protocol and lifecycle. [Apple's secure field](https://developer.apple.com/documentation/appkit/nssecuretextfield), [launch constraints](https://developer.apple.com/documentation/security/constraining-a-tool%27s-launch-environment), and [legacy Keychain ACLs](https://developer.apple.com/documentation/security/access-control-lists) establish platform concepts, not Zatiti signing or live-provider qualification. This helper contract affects `cmd/zatiti`, `internal/platform`, `internal/connections`, `apps/desktop`, `packaging`, CI and qualification fixtures. No release readiness claim follows until signed native amd64/arm64 UI, locked Keychain, caller-rejection, cancellation, crash and genuine provider/Serenity first-chat tests pass.

Revision 8 freezes only the Mac bootstrap's local replay fence; it changes no public operation, release descriptor, delivery record, or product requirement. `packaging` owns `$HOME/Library/Application Support/zatiti-installer/accepted.json` on macOS, separate from controller state and both distribution trees. Its canonical strict UTF-8 JSON schema `zatiti.mac_accepted_release/v1` has exactly `schema`, positive `release_sequence`, and lowercase 64-digit `delivery_sha256`; the missing file means no release has been accepted. The record is at most 4096 bytes. Its directory is owner-only 0700, the regular file is owner-only 0600, and symlinks, wrong owner, permissive modes, malformed/unknown schema, partial records and corrupt bytes fail closed. Publication uses a same-directory 0600 temporary file, file fsync, atomic rename and directory fsync; it never truncates an accepted record in place. Uninstall retains the fence unless a separate explicit data-deletion action is authorized. A lower sequence or the same sequence with a different signed-delivery digest is rejected. An identical sequence/digest is a no-op only after both installed component trees and launchers audit as that signed release. If activation succeeded but publication failed, a higher-sequence rerun first audits the exact installed release, then advances the fence without invoking installation again. No cached file, package receipt or version string alone proves that audit.

The bootstrap holds a second owner-only advisory lock at `$HOME/Library/Application Support/zatiti-installer/bootstrap.lock` from before reading the fence through post-activation audit and fence publication. This lock serializes competing bootstrap processes; it is distinct from `packaging`'s existing `$HOME/.zatiti-install.lock` because `Apply` acquires that lock inside activation. The bootstrap never holds the existing Apply lock around an Apply call. The lock file is 0600 regular and owner-checked with no symlink traversal; cancellation, process death and failures release the kernel lock. A direct installer and a bootstrap using the same release channel must participate in the bootstrap lock before modifying that channel. Tests cover simultaneous installers, crash after activation before fence, truncated or symlinked record, wrong owner/mode, stale sequence, same-sequence alternate digest, and identical verified reinstall. This is a local packaging contract; `cmd/zatiti`, Flutter, CI and public wire callers gain no new operation or authority. The signed `.pkg` payload, Developer ID Team ID pin, native verifier and fixed activation entrypoint remain unresolved release interfaces; this revision does not authorize running a package or publishing an install command.

Revision 7 adds a downloadable Mac delivery record without changing the already staged `zatiti.mac_release/v1` descriptor or any product operation. A canonical, strict, <=64 KiB `zatiti.mac_delivery/v1` record contains: `schema`; the complete separately signed `MacReleaseDescriptor` and its `MacReleaseSignature`; `channel` (`stable`); positive monotonic `release_sequence`; UTC `published_at` and `expires_at` (at most 30 days apart); and exactly six sorted asset records, one `controller`, one `desktop`, and one `installer` for each of `amd64` and `arm64`. Each asset binds `arch`, `distribution`, exact basename `zatiti-<version>-darwin-<arch>-<distribution>.<extension>` (`tar.gz` for component trees, `pkg` for installer), HTTPS URL, byte size (1..2 GiB), and lowercase SHA-256. URLs have no userinfo, query, fragment, IP literal, traversal, or embedded credential; the basename must match the URL path's final segment and must be unique. Component archives contain the signed component manifest and the exact tree covered by the inner descriptor; the installer package must bind those same components and preserve state. The delivery record has its own Ed25519 signature under domain `zatiti.mac_delivery_signature/v1`, covering the canonical record hash. Its signer is selected only from public keys distributed with the installer/bootstrap or cask, never from a fetched record; key rotation ships overlapping trusted keys before a new signer is used. The signature, time window, sequence and exact assets are checked before any downloaded byte is executed; each download is bounded by signed size, keeps TLS validation, refuses cross-host redirects, verifies exact hash/length, and is staged privately before activation. A protected local sequence watermark refuses downgrade/replay without explicit reviewed recovery.

For a one-line direct installer, the published command must pin the bootstrap script's own SHA-256 (download to a temporary file, verify the literal digest in that command, then run it); a mutable `curl | sh` cannot establish the script's embedded trust key. The bootstrap uses native hardware detection so Apple Silicon under Rosetta selects arm64, chooses only signed delivery assets for that hardware, verifies Developer ID package signature and notarization evidence, and invokes the existing per-user installer without disabling Gatekeeper or installing developer tools. A project cask may instead pin each architecture's package URL and SHA-256 in its formula. Source contains no private signing key, hosting credential or placeholder public release claim. `packaging` owns schemas, offline validation, generation and installer tests; CI binds final signed/notarized bytes and evidence; qualification installs each final artifact on a clean native Intel and Apple Silicon host. No publication or qualified install command follows from synthetic fixtures alone.

Revision 6 freezes the Mac installed-client discovery and owner credential handoff. It adds no public operation or persisted row. The installed Mac release uses the controller's existing default state directory (`os.UserConfigDir()/zatiti`, exactly `$HOME/Library/Application Support/zatiti` on macOS); custom paths remain explicit development configuration. `internal/platform` owns an atomic, owner-only `desktop.json` record in that state directory. Its strict UTF-8 JSON schema is `zatiti.desktop-discovery/v1` and its fields are exactly `schema`, `protocol_version` (integer 1), `socket_path` (absolute local Unix socket path), and, only after committed bootstrap, `installation_id` (lowercase UUID), `keychain_service`, `keychain_account`. No credential, master key reference, provider key, helper executable path or caller-supplied endpoint appears. The record is at most 4096 bytes, has a 0700 parent and 0600 mode, is written by fsync plus atomic rename, and is refused on symlink, wrong owner, permissive mode, malformed/unknown schema, partial post-bootstrap locator or unsafe socket path. A missing record means the controller has not published its location, never that setup is complete. Stale metadata remains for repair after the service stops; the desktop confirms live identity through authenticated `installation.status` before showing user data.

`cmd/zatiti serve` publishes the pre-bootstrap record once its private socket is bound and republishes the initialized record after it reads authoritative `installation.OwnerCredential` from a database snapshot. It does this on every initialized startup and after an `installation.init` commit, including a restart after the commit but before publication; it never reruns init or mints a second owner token to repair publication. The existing CLI profile handoff may continue as a compatibility path, but it must use the same persisted StoreRef rather than an in-memory last-Put value. A missing, revoked or unreadable owner item fails closed with a named prerequisite; locked/unavailable/refused Keychain is distinct from absent metadata and never triggers bootstrap retry or token rotation. `internal/platform` exposes `(*Platform).KeychainLocator(ctx, opaqueRef) (service, account string, err error)` only for a stored, readable Keychain item. It validates the reference and never returns bytes; headless and non-Mac backends refuse capability. The locator corresponds to the exact Keychain generic-password item under the platform's installation-local service and account encoded by the opaque ref. The stored item is base64 of the complete Authorization header value, as current platform custody writes it.

The installed Flutter Mac client reads only this protected metadata by default; explicit environment/remote profiles remain development/advanced overrides. It reads the named login-Keychain item through the pinned `flutter_secure_storage` macOS backend using `MacOsOptions(accountName: keychain_service, usesDataProtectionKeychain: false)` and `read(key: keychain_account)`, strictly decodes one base64 value to a valid complete Authorization header, and supplies it only to `ControllerClient.credentials`. It does not copy the token to another storage item, reveal it in UI/logs, or load controller state files. Before bootstrap, it may call the already-public `installation.init` over the private socket with no credential and `credential_store: os`; it persists the intended owner name and any ambiguous attempt for reconciliation, and does not create a second chief. Once initialized, it waits for the republished locator, authenticates, compares `installation.status`'s installation ID with discovery, and only then opens the pinned chief conversation. Missing service, unreadable metadata, locked/refused Keychain, stale socket, and identity mismatch are distinct repair states. The app never declares chat-ready from socket existence, adapter registration, or successful compilation. This revision affects `internal/platform`, `cmd/zatiti`, `apps/desktop`, and integration/qualification fixtures; packaging's installed LaunchAgent must use the default state path, and qualification must prove signed Keychain access on both Mac architectures and across upgrade. The separate provider-key submission and downloadable release metadata contracts remain unresolved and must be frozen before their dependent tasks are dispatched.

Revision 5 adds `SecretStore.Lookup(ctx, key) (opaqueRef, error)` for trusted code that must recover the current reference for a stable, locally owned secret name. `Put` still returns an opaque reference, and `Get`/`Delete` still accept only such references. Lookup validates the same key bounds as Put, returns `not_found` for a missing key, fails closed when the store is unavailable, and never returns secret bytes or creates a credential. Its result is installation-local and must never appear in a public operation payload, receipt, log, or model context. The helper receipt signer stores its key under `connections/helper/receipt-key`; connection completion resolves that exact name through Lookup and then Get. It must never trust a reference supplied by the receipt or caller. This additive Go interface revision affects `internal/contract`, `internal/platform`, `internal/connections`, `cmd/zatiti`, and their SecretStore test doubles; it changes no wire schema or persisted row.

Revision 4 stages the first distribution on macOS for both native arm64 and amd64. Linux remains the next supported target after its own qualification; Windows and mobile remain later. A Mac release cannot claim Intel or Apple Silicon support from compilation alone: each requires an installed, signed, notarized artifact and clean-host qualification. The product and operation contracts of revision 3 remain in force; this revision changes release dispatch order and platform evidence, not the wire schemas or persisted rows. Required provider, Serenity, backup/restore and other first-release guarantees are not waived by phasing Linux later. Setup must let an installed Mac app reach the pinned personal-chief conversation without environment variables, manual socket paths or manual controller startup; any new helper/discovery or secret-custody interface requires its own coordinated contract before dependent implementation.

Revision 3 changes, each a coordinated revision that code written against revision 2 must follow. Every new field named below is additive and optional on an existing type: no existing required field changed type, name or was removed, so old persisted rows validate unchanged and remain inspectable with the new field simply absent. New tables (`WorkerTurn`, proposal records) and new operations have no prior callers to break.

- **Durable worker turn.** Execution owns a persisted `WorkerTurn` (see the embedded `WorkerTurn`/`TurnSource` schemas in `internal/execution`'s generated prompt) keyed by unique `(installation_id, source_kind, source_id, source_version, recipient_worker_id)`; re-admission through `_execution.turn.admit` returns the same turn. A pending inbox message and its turn admission/processed marker (`_messaging.processed`) commit in one shared transaction. A message arriving during an active turn is admitted as a safe-boundary injection durably linked to that turn, never silently dropped or processed twice. One active decision stream exists per worker/task lane; different eligible workers may run concurrently within aggregate limits. Each model step/proposal is stored separately as a `ProposalRecord` keyed by unique `(turn_id, step_index, proposal_id)`; the same key with different bytes is `submission_conflict`.
- **Worker turn pipeline operations.** `_execution.work.pending`/`.claim`, `_execution.context.prepare`/`.commit`, `_execution.proposal.prepare`/`.record`, `_execution.report`, `_execution.verification.pending`/`.claim`, `_tasks.evidence.record`, `_tasks.dependencies.wake`, `_messaging.ready`/`.processed` are new internal operations driving the durable worker loop end to end (admit → claim work → prepare/commit context → dispatch a model or tool effect → record the proposal → report → independently verify). Public `task.start` transitions an eligible draft/ready task to ready and enqueues its run in the same transaction; `task.create`/`task.assign` are unchanged. Exact schemas, callers and behavior are embedded in the generated prompts of `internal/execution`, `internal/tasks` and `internal/messaging`.
- **Worker operation executor.** A narrow `contract.WorkerOperator` capability (declared below) lets a worker-authored local proposal invoke an ordinary public operation under the worker's own authenticated actor, scope-intersected with the task/source authorization envelope. It resolves the actor from the persisted turn/worker mapping; `WorkerID` is an asserted match against that turn, never a way to select any principal. Local model-visible operations are an explicit allowlist (authorized configuration authoring/inspection, task/delegation/responsibility actions, approved memory/messaging operations, output publication); grant/policy changes, secrets, internal bookkeeping and human review never become available merely because they exist in the public catalog. The controller's administrative identity authorizes scheduling/bookkeeping only, never a model proposal. **Public/internal caller fix:** `task.assign` is a public operation and carries no caller allowlist (the registry rejects a public descriptor with callers — confirmed 2026-09-18 descriptor-drift finding in docs/roadmap.md); a chief-driven assignment is an ordinary authenticated `task.assign` call made through this executor under the requesting worker's own actor, not a domain-internal caller path.
- **Effects callback routing.** `Operation` gains optional `attempts` (`{attempt_id, generation}` pairs, additive alongside the unchanged `attempt_ids`) and optional `callback_route`; `Dispatch` gains the matching optional `callback_route`. `_effects.prepare` accepts an optional `callback_route` (`CallbackRoute`: `worker_turn`/`job`/`memory`/`skill`/`connection` plus the relevant turn/step/job id) persisted alongside the action and returned at claim. The controller resolves callback routing from this persisted route; it never infers routing by inserting an undeclared `attempt_id` into strict adapter parameters (fixes audit finding G05). `_effects.record` gains an optional `current_generation` for the successor-generation rule: a stray attempt is `not_sent` if never actually claimed under its recorded generation, `outcome_unknown` if claimed but unconfirmed — never silently dropped.
- **Effects reconciliation reaches `Adapter.Reconcile`.** New `_effects.reconciliation.prepare`/`.record` create a separately admitted, separately authorized and accounted bounded reconciliation read distinct from the original write, merging the qualified observation into the original operation's uncertainty without overwriting history or replaying the original action (fixes audit finding G13).
- **Job registry linkage.** `_execution.job.create` gains an optional `operation_id`, linking a network-backed job to its originating effects `Operation` at creation so reconciliation resolves it without scanning another owner's table; a job without `operation_id` is an ordinary local runner. New `_skills.evaluation.record` and `_configuration.export.prepare`/`.record` route skill evaluation and configuration export/import through this durable job ledger instead of handler-local ID minting with no owner-backed lookup (fixes audit findings G11, G12).
- **Registry lookup and schema normalization (already implemented; confirmed for revision 3).** `Lookup(id, 0)` resolves the highest registered version for that operation id. The registry accepts either a bare `$defs`-relative schema or a fully self-contained document at registration, and `Lookup` always returns a self-contained document pruned to reachable `$defs`. These two rules were implemented and tested against application's assumptions before this revision; this entry is their authored record (docs/roadmap.md, 2026-09-18).
- **CLI root tokens (clarification, no schema change).** `Descriptor.CLI` is the subcommand path only and excludes the binary name; `cmd/zatiti` prepends `zatiti` once at command-tree assembly. Generated `AGENTS.md` prose shows the full invocation (for example "CLI `zatiti artifact export`") for readability — that rendering convenience is not part of the wire contract, and an implementer must not treat the printed binary name as part of `Descriptor.CLI` (this ambiguity produced a wrong descriptor token shape in 11 modules; docs/roadmap.md, 2026-09-18).
- **Service-principal bootstrap.** `_identity.bootstrap` gains optional `service_credential_id`/`service_store_ref` so the bootstrap-created controller service principal can receive a credential in the same transaction, letting an out-of-process controller authenticate; omitted fields leave the service principal credential-less for the in-process explicit-`Actor` seam, unchanged from revision 2.
- **Review eligibility (ruling, confirmed for revision 3).** The eligible reviewer of an exact review-class request is the human principal whose current authority admitted that request; services, workers and agents are never eligible, and proposer separation stays mandatory for them. This settles "an eligible owner's decision" left undefined in revision 2's policy/reviews briefs (docs/roadmap.md, 2026-09-18 evening REVISION-3 RULING).
- **Internal authority checks (ruling, confirmed for revision 3).** `_identity.authority`, like every internal operation, is gated by its caller allowlist and by the calling actor being a registered, unrevoked principal in the transaction's installation — never by requiring the calling actor to independently hold the capability named in its own request. A standing grant of `_identity.authority` to work around a circular subject-capability check is unnecessary and must not be relied upon (docs/roadmap.md, 2026-09-19 REVISION-3 RULING). Whether an actor may read another principal's authority is an explicit code-level decision this ruling does not settle; downstream implementers must document it in place rather than infer it.
- **Artifact locator staging (confirmed unchanged).** `PhysicalCallEvidence.request_context` and `ModelOutput.request_context` remain the revision-2 `ArtifactLocator` retype; revision 3 makes no further change here and extends the same staged/artifact pattern to `_execution.context.commit`'s `staged_context`.
- **Responses `prepare_session`/`model_step` split.** See "OpenAI Responses session preparation" below.
- **Small query/result additions.** `conversation.get`/`.list` return the calling principal's optional `caller_unread_count`/`caller_last_read_marker`; new public `conversation.message.list` reads authorized message history for one conversation. New public `memory.list` lists authorized scoped claim refs (source/freshness/lineage/supported actions) without performing paid retrieval. `Artifact` gains optional `source_operation_id`/`purpose` (provenance). `Status` gains optional `runtime_ready`. `Responsibility` gains optional `last_cycle_id`, and `_scheduling.cycle.record` gains a required `cycle_id` replay/conflict fence plus optional `turn_id` linkage. New public `installation.verifier.list` enumerates installed trusted verifier profiles (secret-free) so a task's acceptance contract can name one. `event.list`'s drained-cursor/filter/scope/principal/retention behavior is unchanged; this is its explicit revision-3 confirmation, not a schema change. Public task/turn status projection and installed-capability desktop surfacing are explicitly deferred to `internal/evidence` (P34) and `apps/desktop` (P42) rather than invented here.
- **Local decision tools pinned separately from provider tools.** `ReplyProposal`, `ClarifyProposal`, `ReportOutputsProposal` and `CycleDecisionProposal` (schemas below) are the sealed names/inputs for the context builder's non-provider decision tools (`reply`, `clarify`, `report_outputs`, `cycle_decision`). They are execution-local proposals evaluated by the worker operation executor, never routed through a `connections.Tool`/adapter and never given a provider operation mapping; a final text answer alone may complete a chat turn but never implicitly fulfills a task's required outputs.
- Desktop client (ADR 002): the desktop is a Flutter application at `apps/desktop` that is a direct wire client of internal/server. The Go roots `internal/desktop` and `cmd/zatiti-desktop` are retired; no Go package may import or stand in for the desktop.
- Adapter request context: `PhysicalCallEvidence.request_context` and `ModelOutput.request_context` are an `ArtifactLocator`, not a bare `ArtifactRef`. Adapters return the staged variant; the controller publishes it and substitutes the artifact variant in normalized evidence.
- Installation database seam: `contract.DatabaseBackup` is a one-method capability that entrypoint assembly supplies only to internal/installation through `installation.WithDatabaseBackup`. `contract.Dependencies` is unchanged and no domain gains general database access.

## Build and dependency decisions

Module `github.com/zatiti/zatiti`; language `go 1.26.0`; initial toolchain `go1.26.2`. Integration owns go.mod/go.sum. The Flutter desktop pins its own Flutter/Dart SDK and plugins in `apps/desktop/pubspec.lock`; integration records those pins and licenses in the same lock report and the Go module never depends on them. Domains import standard library and internal/contract, not sibling domains. Application composes owner methods through a checked in-process dispatcher: ordinary Go calls, not another server or scheduler.

Library families: Cobra CLI; official MCP Go SDK, handwritten adapter for MCP `2025-11-25`; Flutter (Dart) desktop application at `apps/desktop` with an operating-system secure-storage plugin, outside the Go module; modernc.org/sqlite. Mint is optional build-time tooling and cannot weaken contracts. Hosted provider v1: a specifically qualified OpenAI Responses endpoint/profile, with configured model and prices. Other compatible endpoints remain unavailable until qualified. GitHub uses documented REST over net/http, with mutation retries disabled. Serenity is a separate pinned service using public interfaces only. Integration must resolve exact dependency releases/commits, checksums, licenses and qualification commands in a lock report BEFORE dispatching dependent implementation. These are selected implementation families, not claims that upstream behavior has been qualified. No agent independently substitutes libraries, models, prices, accounts or Serenity APIs. Dependency resolution/qualification is an explicit foundation assignment.

## Shared Go declarations

The contract owner implements these exact declarations. Omitted function bodies are the implementation assignment, not production stubs. Public JSON uses snake_case. Schema-derived DTO names concatenate operation path segments in PascalCase plus Input/Output and live in the owning package. JSON integer => int64, UUID => contract.ID, timestamp => time.Time, optional scalar => pointer, array => slice, explicitly open JSON => json.RawMessage. Unknown fields are rejected.

```go
package contract
import (
    "context"
    "database/sql"
    "encoding/json"
    "io"
    "net/http"
    "time"
)
type ID string
type Digest string
type Version int64
type Clock interface { Now() time.Time }
type IDSource interface { New() ID }
type Scope struct {
    InstallationID ID `json:"installation_id"`
    OrganizationID ID `json:"organization_id,omitempty"`
    ProjectID ID `json:"project_id,omitempty"`
    WorkerID ID `json:"worker_id,omitempty"`
    TaskID ID `json:"task_id,omitempty"`
}
type Actor struct {
    PrincipalID ID `json:"principal_id"`
    Kind string `json:"kind"` // human | client_agent | worker | service
    CredentialID ID `json:"credential_id"`
}
type Request struct {
    Schema string `json:"schema"` // zatiti.request/v1
    SubmissionKey string `json:"submission_key,omitempty"`
    Input json.RawMessage `json:"input"`
}
type Fault struct {
    Code string `json:"code"`
    Message string `json:"message"`
    Retryable bool `json:"retryable"`
    Details json.RawMessage `json:"details,omitempty"`
}
func (*Fault) Error() string
type Outcome[T any] struct { Status string; Data T; NextCursor *string }
type Payload struct {
    Status string `json:"status"` // completed | accepted | failed
    Data json.RawMessage `json:"data"`
    Error *Fault `json:"error"`
    NextCursor *string `json:"next_cursor"`
}
type Result struct {
    Schema string `json:"schema"` // zatiti.result/v1
    CommandID ID `json:"command_id"`
    Payload
}
type Invocation struct { Operation string; Version int64; Input json.RawMessage }
type Event struct {
    ID ID `json:"id"`
    Sequence int64 `json:"sequence"`
    At time.Time `json:"at"`
    Scope Scope `json:"scope"`
    Kind string `json:"kind"` // owner.entity.transition
    ResourceID ID `json:"resource_id"`
    ResourceVersion Version `json:"resource_version"`
    Data json.RawMessage `json:"data"`
}
type Reader interface {
    QueryContext(context.Context, string, ...any) (*sql.Rows, error)
    QueryRowContext(context.Context, string, ...any) *sql.Row
}
type Unit interface {
    Reader
    ExecContext(context.Context, string, ...any) (sql.Result, error)
    Actor() Actor
    Scope() Scope
    Generation() int64
    ReadOnly() bool
    Emit(context.Context, Event) error
}
type Ownership interface { Held() bool; Lost() <-chan struct{}; Close() error }
type Database interface {
    StartGeneration(context.Context) (int64, error)
    Generation(context.Context) (int64, error)
    Read(context.Context, Actor, Scope, func(Unit) error) error
    Write(context.Context, Actor, Scope, func(Unit) error) error
    Migrate(context.Context, []Migration) error
    Events(context.Context, int64, int) ([]Event, error)
    Backup(context.Context, io.Writer) error
    Close() error
}
type Migration struct { Owner string; Version int64; SQL string; SHA256 Digest }
type Descriptor struct {
    ID string; Version int64; Owner string
    Visibility string // public | internal
    Mode string // query | mutation
    Effect string // local | disclosure | external_read | external_mutation
    InputSchema json.RawMessage; OutputSchema json.RawMessage; CompletionSchema json.RawMessage
    CLI []string; MCP string; ScopeRequired []string; Callers []string
    ExpectedVersion bool; SubmissionKey bool
}
type Handler func(context.Context, Unit, Invocation) (Payload, error)
type Module interface {
    Name() string
    Migrations() []Migration
    Descriptors() []Descriptor
    Handle(context.Context, Unit, Invocation) (Payload, error)
}
type Ports interface { Call(context.Context, Unit, Invocation) (Payload, error) }
type SecretStore interface {
    Put(context.Context, string, []byte) (string, error)
    Lookup(context.Context, string) (string, error)
    Get(context.Context, string) ([]byte, error)
    Delete(context.Context, string) error
}
type BlobStore interface {
    Stage(context.Context, io.Reader, int64) (string, Digest, int64, error)
    Publish(context.Context, string, Digest) error
    Open(context.Context, Digest, int64, int64) (io.ReadCloser, error)
    RemoveStaged(context.Context, string) error
}
type Dependencies struct {
    Clock Clock; IDs IDSource; Ports Ports
    Secrets SecretStore; Blobs BlobStore
}
// Every domain owner: New(Dependencies) (*Service, error).
// *Service implements Module. Only declared outgoing ports may be used.
// Sole exception: installation's New also takes options (see DatabaseBackup).
type Dispatch struct {
    OperationID ID `json:"operation_id"`
    AttemptID ID `json:"attempt_id"`
    Generation int64 `json:"generation"`
    Adapter string `json:"adapter"`
    Action json.RawMessage `json:"action"`
    CredentialRef string `json:"credential_ref"`
    ProviderKey string `json:"provider_key,omitempty"`
    Deadline time.Time `json:"deadline"`
}
type Observation struct {
    Disposition string `json:"disposition"` // succeeded | failed | accepted | unknown | not_sent
    ProviderReference string `json:"provider_reference,omitempty"`
    Evidence json.RawMessage `json:"evidence"`
    Usage json.RawMessage `json:"usage"`
    ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
}
type Adapter interface {
    Name() string
    Contract() json.RawMessage
    Invoke(context.Context, Dispatch) (Observation, error)
    Reconcile(context.Context, Dispatch) (Observation, error)
}
type AdapterDependencies struct {
    HTTP *http.Client; Secrets SecretStore; Clock Clock; Blobs BlobStore
}
// Every adapter: New(AdapterDependencies, json.RawMessage) (Adapter, error).
// Config is strictly validated, versioned and secret-free.
type Operator interface { Call(context.Context, string, Request) (Result, error) }
type CredentialSource interface { Credential(context.Context) ([]byte, error) }
```

The registry binds concrete input/output DTO types to descriptors and handlers using `Bind[I, O any](descriptor contract.Descriptor, fn func(context.Context, contract.Unit, I) (contract.Outcome[O], error)) (contract.Handler, error)`. JSON decoding is strict and schema validation precedes execution. Internal cross-owner calls use the same schema-checked Invocation boundary to avoid sibling package imports. They retain the same Unit, actor, scope, generation and transaction; cannot mint authority; and are not exposed as public operations.

## Transaction and ownership rules

Each domain owns tables prefixed with its package name and `_`. Schema and migration bodies inside that namespace are package-private; no other package depends on their layout. Storage owns installation generation, migration metadata and `storage_events`. Evidence owns commands/receipts. Migration ordering is explicit in application assembly. All migrations run under exclusive installation ownership before serving. Cross-owner reads and changes use Ports.Call and the embedded internal schemas. Internal operations have a caller allowlist. The dispatcher detects recursive calls. Queries cannot invoke mutation descriptors. Public clients cannot supply Actor or Unit or invoke internal operations.

One controller, one ordered writer, WAL, foreign keys on every connection, synchronous FULL, busy timeout 5 seconds, consistent read snapshots. Database.Write performs ONE transaction callback attempt with no automatic callback retry. Busy exhaustion returns controller_unavailable/retryable. Unit is valid only inside its callback; no goroutines or retained handles. Unit.Emit appends state-correlated evidence to the storage outbox in the same transaction. Delivery is at least once and consumers deduplicate event IDs. No network/model calls, secret-store access, filesystem streaming or subprocess work inside a Unit. Stage bytes or local helper actions outside transactions, then publish metadata or reconciliation intent through owner methods.

Handler errors roll back the entire transaction. Persist a command refusal in a separate short transaction after rollback where necessary, retaining the original request identity. Query command IDs need not be durable. All mutations return durable command IDs; accepted responses must identify inspectable durable work. Output schemas describe Payload.Data, not the result envelope. Never leak SQL, tokens, provider raw responses or private paths through Fault.

Definition creates/updates/archive stage drafts, including schedules, responsibilities, policies and memory bindings. Only configuration.apply activates definitions. It validates owners' candidate slices, seals base revision/dependencies/requirements, and calls internal activate in the same transaction under old authority. Internal activate is admitted only from the compiler during exact plan application, not by a public flag. Public restrictive pause/revoke/cancel commits immediately without compilation or spending. Archive staging may immediately disable new admissions as an explicitly reported restrictive side effect; final removal waits for obligations.

## Effect and execution rules

Admit: current authorization, exact review/preconditions, all budget reservations, attempt/dispatch intent and evidence atomically. Claim: second transaction rechecks generation/restrictions/expiry and consumes the one-use claim. Invoke the adapter outside transactions. Record: third transaction stores observations, costs or retained uncertainty and events. Each physical call, including reconciliation and retries, has a distinct attempt. Reconciliation is an admitted bounded read and links to the original effect; it does not overwrite history. Internal jobs use explicitly provisioned scoped service identities plus current worker/task restrictions.

Once claimed, crash/timeout/cancel/lease expiry/operator acknowledgement/weak not-found cannot prove nonexecution. Preserve outcome_unknown and its reservation until authoritative evidence. Earlier unknown attempts survive later failures. Mutation SDK/HTTP retries are disabled. Even qualified idempotent retries need fresh authorization and physical-attempt evidence. Trusted adapters resolve credential references outside transactions; no public raw secrets or inherited owner credential environment. Fencing external workers blocks Zatiti mutations but cannot prove their processes stopped.

Task success needs independently established acceptance against pinned verifier code/version, sealed inputs and observations. Worker report/exit zero cannot set succeeded. Defaults: one concurrent attempt/worker, four/installation, 30 minutes/attempt, 100 model steps, eight children, delegation depth three, root deadline 24 hours. Configure explicit currency and finite spend limits before paid work. Amounts are int64 micro-units with checked overflow; rational rates use integer numerator/denominator and round reservations upward. Unknown/advisory usage and missing prices are not zero. Children share root and ancestor budgets.

## Wire conventions and limits

UUIDv4 for new identities; stable UUID references thereafter. SHA-256 lowercase hex digests. Versioned canonical JSON sorts keys, rejects duplicate keys/unknown fields, preserves exact integer values and semantic array order; schema-declared sets sort by stable identity. Preserve exact skill bytes. UTC RFC3339Nano timestamps and int64 versions >=1. Patches and snapshots are distinct; explicit delete only. Inert `extensions` alone permits namespaced extra keys. Scope is explicit and every referenced object is revalidated; no implicit global organization.

Default limits: JSON request 1 MiB; chunk 1 MiB decoded; artifact 256 MiB; skill archive 16 MiB compressed, 64 MiB expanded, 4096 entries, depth 32; list 50/max 200; read 1 MiB; redacted log record 8 KiB; event page 100/max 500; upload expiry 24 hours. Narrowing is allowed, increases require authorized configuration. Reject overflow and unsupported bounds.

Mutations require submission_key (1..128 printable ASCII) except one-time init. Bind principal, operation/version and canonical input hash. Retain >=30 days and through unresolved obligations. Replay identical input returns original disposition BEFORE stale-version validation; changed input => submission_conflict. Concurrent duplicates serialize. Recover lost acknowledgements by command lookup, not a new key. Resource updates require expected_version; apply requires base_revision and plan digest. Creation has no existing version. Opaque authenticated cursors bind principal/scope/filter/order/snapshot and expire explicitly with cursor_expired and snapshot_required=true. Event replay cannot silently omit a gap.

HTTP POST `/v1/operations/{operation_id}` uses the common request envelope. Local HTTP runs over a private Unix socket. The Flutter desktop is a direct client of this endpoint: the private Unix socket locally and, for an explicitly configured remote desktop, mutual TLS mapped to current application principals over the same operation contract, never remote MCP. It shares the envelope, fault mapping, limits, submission-key replay and cursor rules with every other client, holds no business logic or database access, and consumes the generated operation catalog rather than any Go package. Completed => HTTP 200; accepted => 202. Failed mapping: invalid_input 400, permission_denied 403, not_found 404, stale_version/submission_conflict/conflict/review_required/outcome_unknown/artifact_fault 409, cursor_expired 410, prerequisite_missing/external_action_required/budget_unavailable/capability_unsupported/verification_failed 422, controller_unavailable 503, internal_error 500. Domain failures always include the result envelope.

CLI completed/accepted => exit 0; invalid_input 2; permission_denied/review_required 3; stale_version/submission_conflict/conflict 4; prerequisite_missing/external_action_required/budget_unavailable/capability_unsupported 5; controller_unavailable/outcome_unknown 6; other failures 1. CLI --json emits one envelope to stdout, diagnostics stderr, no implicit prompts. MCP structuredContent is the full envelope, text is equivalent JSON, failed => isError true, malformed protocol => protocol error. Dot-separated operation IDs map to CLI tokens and `zatiti_` MCP names. Exceptions: installation.init => `zatiti init` / `zatiti_installation_init`; capabilities.list => `zatiti capabilities` / `zatiti_capabilities`. Lifecycle steps are individual tools. Tool input never selects a credential profile or accepts raw secrets/unrestricted server paths.

## Completion policy

Write only within the assigned root. Do not edit this prompt, siblings, shared contracts, root dependencies or external acceptance inputs. Implement exact fake dependencies locally; do not return invented production success for unavailable prerequisites. Use controlled providers, deterministic clocks and synthetic fixtures. Preserve expected/observed results and failure evidence. Package tests prove local properties; actual subprocess parity, crash recovery, desktop and adapter qualification prove integration. Z01-Z21 and first-release journeys must pass on both Intel and Apple Silicon macOS before the Mac release is claimed; Linux requires its own later qualification before Linux support is advertised.

## Local IO, authentication, jobs and verification seams

These additional declarations are part of the SAME frozen contract package (using imports already shown). Implementing an interface does not permit calling it inside a Unit except where the signature explicitly takes Unit/Reader.

```go
type Authenticator interface {
    Authenticate(context.Context, Reader, []byte) (Actor, error)
    AuthenticateCertificate(context.Context, Reader, Digest) (Actor, error)
}
type IOPlan struct {
    ID ID
    Owner string
    Invocation Invocation
    Actor Actor
    Scope Scope
    Generation int64
    ExpectedVersions map[ID]Version
    Prepared json.RawMessage
}
type IOResult struct { Data json.RawMessage; Fault *Fault }
type LocalIO interface {
    Prepare(context.Context, Unit, Invocation) (IOPlan, error)
    Perform(context.Context, IOPlan) (IOResult, error)
    Finish(context.Context, Unit, IOPlan, IOResult) (Payload, error)
}
// Request is the exact VerificationRequest JSON schema embedded in the prompt.
type Verification struct { Request json.RawMessage }
// Document is the exact VerificationResult schema, including independently
// observed checks, pinned identity, task/attempt and staged artifact handoff.
type VerificationResult struct { Document json.RawMessage }
type Verifier interface {
    Verify(context.Context, Verification) (VerificationResult, error)
}
type VerifierDependencies struct { Clock Clock; Blobs BlobStore }
// DatabaseBackup is a narrow capability, not database access. Database satisfies
// it structurally. Entrypoint assembly supplies it to installation only.
type DatabaseBackup interface { Backup(ctx context.Context, w io.Writer) error }

// Revision 3: worker operation execution and durable job registry. Same
// package, same import set; no new dependency. (Revision 3 also introduces a
// restore capability, but as shipped it is `controller.RestoreLifecycle`,
// declared in internal/controller, not a contract-package type -- see
// "Restore protocol (revision 3)" below.)
type WorkerRequest struct {
    TurnID ID
    ProposalID string
    WorkerID ID
    Scope Scope
    Operation string
    Version Version
    SubmissionKey string
    Input json.RawMessage
}
// WorkerOperator resolves the actor from the persisted turn/worker mapping and
// re-enters ordinary application authorization; WorkerID is an asserted match
// against that turn, never a way to select any principal. Injected only into
// trusted runtime composition (application, given to execution/controller).
type WorkerOperator interface {
    ExecuteWorker(context.Context, WorkerRequest) (Result, error)
}
// JobWork/JobOutcome/LocalJobRunner are the minimal shared job types, kept in
// contract (not controller) to avoid a domain->controller import. Execution owns
// the durable ledger (execution_jobs); owners retain their domain rows and
// receive idempotent typed completion callbacks through JobOutcome.
type JobWork struct {
    ID ID
    Version Version
    Generation int64
    Owner, Operation string
    Scope Scope
    Input json.RawMessage
}
type JobOutcome struct {
    State string
    Result json.RawMessage
    EvidenceIDs []ID
    Requirements []Requirement
}
type LocalJobRunner interface {
    RunJob(context.Context, JobWork) (JobOutcome, error)
}
// Requirement mirrors the shared $defs/Requirement schema (code/message plus
// optional resource_id/challenge_id); embedded here only for the Go signature.
type Requirement struct {
    Code string `json:"code"`
    Message string `json:"message"`
    ResourceID *ID `json:"resource_id,omitempty"`
    ChallengeID *ID `json:"challenge_id,omitempty"`
}
```

### Restore protocol (revision 3)

The six-step offline restore protocol (contract-proposals.md section 7) is split across two owners as shipped, not implemented by a single entrypoint-owned capability supplied to `internal/installation` alone: `internal/installation`'s public `installation.restore` operation performs steps 1-3 without ever touching the live database file, and `internal/controller` performs steps 4-6. `installation.restore` runs the same synchronous LocalIO Prepare/Perform/Finish flow as `installation.backup` (`internal/installation/restore.go`): Prepare requires exclusive maintenance, validates the pinned backup artifact and creates the durable job; Perform (outside any transaction) decrypts and verifies the uploaded bundle's binding, integrity and framed image, then — before any rewind — exports a sealed, published `RecoveryOverlay` document of this installation's CURRENT pre-restore state (source database digest from the `DatabaseBackup` capability, plus the obligations `snapshotObligations` captured at Prepare time from pending effects and unresolved memory writes); Finish registers that overlay artifact and leaves the job `running` with an `external_action_required` requirement naming the verified backup image. `internal/installation` never swaps the SQLite file itself: a domain module cannot replace the live database out from under its own open handle mid-process, so the installation stays merely paused, not yet rewound, once `installation.restore` completes.

That handoff is driven forward by `RestoreLifecycle`, declared in `internal/controller` (`internal/controller/restore.go`) — not `contract.SnapshotInventory`/`contract.RestoreCoordinator` as this section previously described; no such contract-package types exist. `RestoreLifecycle` has two methods: `StageCandidate(ctx context.Context, restoreJobID contract.ID, dir string) (RestoreCandidate, error)` resolves the job's already-verified backup artifact into a locally staged, decrypted candidate database file, and `MergeOverlay(ctx context.Context, u contract.Unit, restoreJobID contract.ID) error` folds the already-captured `RecoveryOverlay` into every owner's own tables monotonically — never resurrecting a revoked credential, resending a consumed dispatch or erasing a liability absent from the older snapshot — inside one transaction. It is a field of `controller.Collaborators` (`RestoreLifecycle RestoreLifecycle`), attached through `Controller.Attach` exactly like `Verifier`/`Operator`/`Blobs`: only entrypoint assembly may supply an implementation, because only it has direct Go access to `internal/installation`'s private bundle/key/overlay code, and the controller itself never decrypts a backup bundle or resolves a secret reference. `cmd/zatiti/restore.go` defines the one production implementation (unexported `restoreLifecycle{}`); `cmd/zatiti/serve.go`'s `superviseController` wires it into `Collaborators.RestoreLifecycle` alongside every other trusted collaborator before `ctl.Attach(collab)`.

Once a job reaches `running`/`external_action_required`, the controller's own tick flow claims it and `runRestore` drives the remaining steps: quiesce admission and drain every other in-flight unit (`drainOrdinary`), stage the candidate image via `RestoreLifecycle.StageCandidate` and hand it to `storage.Restorable.PrepareRestore`/`CommitRestore` for the atomic file-level swap and generation advance (`performSwap`), fold the overlay via `RestoreLifecycle.MergeOverlay` inside the transaction `storage.Restorable.WriteRestoreOverlay` opens on the freshly reopened, still-paused database, then call `storage.Restorable.ResumeAfterRestore` to lift the write gate (`mergeAndResume`), and finally report the durable disposition back to `internal/installation` through its own internal `_installation.restore.record` operation (`failRestore`/`settleRestore`). A controller lifetime that itself performs the swap cannot continue afterward: its `*application.Application` was built over the now-closed pre-restore database handle and there is no seam to repoint it, so `Controller.Run` returns the `ErrRestoreHandoff` sentinel error — not a fault — the instant the swap, overlay merge and storage resume are durable. `cmd/zatiti`'s `runServe` is an outer loop around exactly this signal: on `ErrRestoreHandoff` it calls `reassembleAfterRestoreHandoff` (closes the stale `Application` and database, reopens storage at the same path — which already observes the swap — and calls `Database.StartGeneration` again to fence out the lifetime that just ended) under the SAME held installation lock, then runs again. A crash mid-protocol recovers the same way at the next startup: `recoverRestoreBeforeFence` runs before the ordinary generation fence and drives any journal entry left open forward from its last durable phase, re-checking `storage.Restorable.RestorePaused` rather than trusting the last written phase, so it never repeats an already-committed swap or loses track of one.

Current state, honestly: production's only `RestoreLifecycle` implementation, `cmd/zatiti`'s `restoreLifecycle{}`, fails both methods closed with `contract.CodePrerequisiteMissing` rather than guessing or fabricating success — exactly the fallback `Collaborators.RestoreLifecycle`'s own doc comment designs for. `StageCandidate` fails because no operation, public or internal, exposes a pending restore job's original backup-artifact reference to entrypoint assembly (public `job.get`'s wire projection never carries `Input`; only the controller's own internal `_execution.job.claim` call does, and that result stays in the controller's private journal entry). `MergeOverlay` fails because no owner-defined merge operation yet exists in `internal/effects`, `internal/identity` or `internal/memory` to fold a `RecoveryOverlay`'s obligations into their own tables, and because credential/grant revocation obligations are never captured into the overlay to begin with: `snapshotObligations` (`internal/installation/restore.go`) records only `claimed_effect`/`unknown_effect` and `memory_write` obligation kinds. This is a scoped, tracked gap — new merge operations spanning effects/identity/memory, revocation-obligation capture in `snapshotObligations`, a job-input lookup path for `StageCandidate` — not a defect in what shipped: a restore observed without a working merge/staging path is recorded failed with `prerequisite_missing`, and the database is never touched by an incomplete attempt. A clean destination additionally needs a secure key-transfer/provisioning route; an opaque source secret-store reference alone is not portability. Missing required Serenity export guarantees blocks a full-memory backup claim; an empty `Brains` array must never be reported complete.

Identity Service also implements Authenticator. Application receives this interface explicitly in New; it uses a dedicated read snapshot and never reads identity-owned tables itself. Only the byte-slice credential boundary carries secret authentication material, never Invocation JSON. Zero sensitive buffers after use where practical; no logging. Certificate authentication resolves a preprovisioned credential reference and follows the same current principal/revocation rules.

Artifacts, skills, connections and installation Services also implement LocalIO. The registry detects this interface at assembly and routes ONLY registered local IO operations through Prepare/Perform/Finish: artifact.upload.chunk/finish/cancel/read/export; skill.import; connection.setup.begin/complete/cancel; installation.init/backup/restore. LocalIO handles local bounded files, secure helpers and backup work, never unadmitted provider/model calls. Prepare strictly validates input, versions, identity and authority and records replayable local intent under IOPlan.ID; Perform receives that exact trusted in-memory plan outside transactions, resolves opaque staging/helper references, and returns metadata; Finish rechecks authority/versions/generation and commits result/evidence. IOPlan is never public, accepted from an agent or stored with secrets. Prepared/Data JSON must use the operation's declared schemas plus private owner-local metadata, which no other package reads. No cross-owner business protocol may hide in those private fields. Perform must support safe replay of local staging/publication by plan ID, or preserve an inspectable failed/unknown local obligation. A synchronous read does not return bytes to client until final authorization check. New raw provider writes always use effects, not this interface.

For synchronous local IO mutations, persist command identity plus accepted internal pending disposition at Prepare, then replace pending disposition once at Finish. Concurrent same-key calls join/inspect that same in-progress command and never run duplicate Perform. The controller can recover abandoned local intents using the execution job API. Async backup/export uses the same interface with an inspectable job returned immediately. The artifact.upload.chunk endpoint has a 2 MiB encoded request cap (all other ordinary JSON requests 1 MiB), allowing the specified 1 MiB decoded chunk plus envelope.

Execution owns shared durable jobs (execution_jobs), distinct from runs and physical effects. `_execution.job.create` stores the original owner/operation/input and idempotent source identity; public job.get inspects any authorized job. Controller lists pending jobs, claims generation-bound ownership, invokes the named LocalIO owner outside transactions through its exact plan, and records result/obligation. Network jobs first prepare effects and wait on their recorded outcomes. `_execution.job.record` stores result data matching the originating operation's explicit completion_schema and links actual artifact/operation evidence. Never mark a job succeeded merely because its physical request was accepted.

Tasks calls `_execution.enqueue` with ready pinned task inside the same transaction; execution creates one run for task/version and returns it. `_tasks.ready` also exposes a bounded recovery scan. Execution's verifier constructor is `NewVerifier(contract.VerifierDependencies) (contract.Verifier,error)`. Verify executes outside Unit and returns actual independently established observations under the pinned accepted profile; `_execution.verification.record` rechecks attempt/task/version before changing task state. A forged worker-provided VerificationResult is never accepted through public report. Concrete context/adapter/verification payload schemas are included in relevant prompts.

Administrative effects that have no task use their command/operation ID as an accountable admission root; no fictional worker/task or fabricated task foreign key is required. Accounting skips absent scope dimensions but always reserves installation and present organization ancestors/project limits. Task effects additionally share root_task_id. A profile without enforceable charge bounds cannot claim a hard cap.

Typed handler bindings return `contract.Outcome[O]` so accepted results and page cursors survive typed registration. The frozen Bind signature is `Bind[I, O any](contract.Descriptor, func(context.Context, contract.Unit, I) (contract.Outcome[O], error)) (contract.Handler, error)`. Errors carry Fault and roll back, while accepted/completed status and cursor come from Outcome.

Assembly order: application.NewPorts() returns an unbound *PortRouter; For(owner string) returns an owner-bound contract.Ports without domain calls; construct identity and all other modules with those ports, passing installation.WithDatabaseBackup(backup-only wrapper over the opened Database) to installation.New and to no other constructor; registry.New(modules); application.New(db, registry, identityAuthenticator, clock, ids); router.Bind(app) exactly once; start serving only after Bind. PortRouter exposes `For(string) contract.Ports` and `Bind(*Application) error`, rejects pre-bind invocation, and never allows a domain to change its bound caller identity. Registry also implements Module for its own capabilities methods. Constructor execution must not query peers or start goroutines.

All native BlobStore objects are encrypted at rest (including public-classification content), because Stage has no classification argument. Metadata publication sets encrypted=true; classification separately governs disclosure. Controller records the raw adapter observation and cost disposition durably first, then publishes StagedOutput bytes outside Unit, then commits artifact metadata and normalized owner callbacks through an outbox consumer. Publication failure is a visible artifact obligation and blocks task/job acceptance; it does not erase a confirmed provider observation or authorize resending. The adapter cannot mint artifact IDs: AdapterDependencies carries no IDSource and BlobStore.Stage returns only a staging reference, digest and size. The same handoff covers the request record. Each adapter stages its exact secret-free request before sending and names it in `physical_call.request_context` as a staged ArtifactLocator with a matching StagedOutput of purpose context; the controller publishes it with the other staged bytes, for every disposition including not_sent and unknown, and delivers normalized evidence whose request_context is the artifact variant. The raw recorded observation keeps the staged locator and is never rewritten.

Installation alone needs the bytes of a consistent SQLite backup: BackupManifest requires database_digest/database_size and RecoveryOverlay requires source_database_digest, while Dependencies {Clock, IDs, Ports, Secrets, Blobs} deliberately exposes no database. The seam is the DatabaseBackup capability above and these exact declarations in internal/installation: `type Option func(*Service)`; `func WithDatabaseBackup(contract.DatabaseBackup) Option`; `func New(contract.Dependencies, ...Option) (*Service, error)`. `New(deps)` without options stays valid, so the common domain constructor shape holds. Dependencies gains no field and no other module receives the capability. Entrypoint assembly passes a wrapper value whose method set is exactly Backup and which delegates to the opened contract.Database, never the Database value itself; installation must not type-assert the capability to any wider interface or retain it beyond the Service. Installation calls Backup only from LocalIO.Perform for installation.backup and for the pre-restore recovery overlay, never inside a Unit, streaming through a hashing writer into BlobStore.Stage so database_digest/database_size and source_database_digest describe the exact staged bytes without unbounded buffering or a caller-supplied path. The capability grants no query, write, migration, event-feed or file-path access and is not the restore rewind mechanism. When the option is absent, installation.backup and installation.restore fail prerequisite_missing naming the database backup capability; they never fabricate a digest, and every other installation operation is unaffected.

Entrypoint assembly owns platform.Acquire -> storage.Open -> Migrate -> Database.StartGeneration, exactly once before controller/server admission. Pass the held contract.Ownership into controller.New; controller never acquires a second lock or advances generation again. Ownership.Lost closes when ownership is lost/released; stop admission immediately. Generation is a persisted local-controller fence, not distributed locking. The installation lock is retained until controller shutdown and DB close.

Remote TLS additionally validates the certificate chain and maps the SHA-256 certificate SPKI fingerprint through application.AuthenticateCertificate(ctx,Digest), which delegates to identity Authenticator.AuthenticateCertificate on a read snapshot. Only a verified TLS peer can enter that API; operation input cannot supply the fingerprint. Store certificate fingerprints as identity-owned authentication metadata on an explicitly provisioned credential reference. If a bearer credential is also supplied, its principal must match the verified certificate principal. No self-asserted certificate name, profile name or fingerprint header grants authority.

Every async operation declares completion_schema separately from its immediate Job response. job.get returns that schema as Job.result only when established; pending jobs omit result. Local IO mutations that may be recovered asynchronously accept either their completed resource output or `{job: Job}` in the descriptor output schema. Bootstrap remains synchronous under its exclusive one-time lock. No unspecified job kind may execute: _execution.job.create requires a registered completion_schema or a specifically registered private local-intent recovery contract.

Evidence retains the complete original result envelope for command replay, including fault message/details/retryability and cursor; projections of status/data/error_code must agree with Command.result. `_evidence.command.finish` takes that exact result. A replay cannot reconstruct a different error or lose an accepted job reference. ScopeRequired lists installation_id where input scope is required; resource-specific organization/project/worker conditions are enforced by the owned operation schemas and current referenced-resource validation, never inferred from a profile name.

Descriptor.Callers carries the exact catalog internal caller allowlist into runtime registration; application enforces this metadata rather than inventing it from operation names. Internal metadata remains available to trusted assembly/registry while public capability output contains public operations only. An empty allowlist never authorizes an internal call.

## OpenAI Responses session preparation (revision 3)

The checked-in `internal/adapters/responses` adapter created a provider conversation and then called Responses inside one `Invoke`, which is two physical requests behind a contract that specifies exactly one physical call per claimed effect (audit finding G27). Revision 3 freezes the split into two explicit effects, each making one physical call, with a persisted session handle passed between them:

- `prepare_session` — `kind: "prepare_session"` — creates the provider conversation and returns its authoritative handle as `ResponsesEvidence.session_handle` (a stable provider-issued string, never fabricated locally). **Disclosure:** the action carries no model-visible content beyond what an empty conversation creation requires; no `context_artifact` is sent. **Timeout:** a timeout after the request was sent leaves the session unknown, not absent — the caller must not create a second session on an unconfirmed `prepare_session` outcome; it waits for recovery. **Cost:** session creation itself is not billed by the pinned protocol; usage is `unknown`/advisory only if the provider's behavior deviates from that pin. **Unknown outcome:** if session creation may have succeeded but its response was lost, retain the unknown outcome without resending — a fresh `prepare_session` could create a second, unlinked provider conversation.
- `model_step` — `kind: "model_step"` — names the persisted session handle (`ResponsesParameters.session_handle`, required) and sends the model input (`context_artifact`, `max_output_tokens`, `tool_contract_versions`). **Disclosure:** exactly the pinned `context_artifact`; no other model-visible bytes. **Timeout:** a timeout after bytes were sent is `outcome_unknown`; an empty item list is equally consistent with "never ran" and "still running" (the provider documents no conversation search), so nonexecution is never assumed. **Cost:** reserved against the profile's accepted token bound before dispatch; a lookup that finds output but no usage cannot release the worst-case billing liability, so it stays reserved. **Unknown outcome:** preserved exactly as any other adapter unknown — no automatic fresh call, no silent retry.

`ResponsesParameters` is versioned to require `kind` (`prepare_session` | `model_step`) as a discriminator; `model_step` additionally requires `session_handle`. `session_handle` is scoped to the same conversation/context lineage as the turn that created it and is never reused across turns. P13 implements both translations against the pinned protocol; P15 consumes the resolved handle when preparing a `model_step` action; P12/P23 journal and dispatch each effect separately through the callback-routed `_effects.prepare`/`.record` pipeline above, so a lost `prepare_session` acknowledgement and a lost `model_step` acknowledgement are two independently recoverable unknowns, never conflated into one. Exact `ResponsesParameters`/`ResponsesEvidence` field-level schemas are frozen in `docs/implementation/adapter-schemas.json` and embedded in `internal/adapters/responses`'s generated prompt; this section is the coordinated-revision record for why they carry a `kind` discriminator and a `session_handle`, not a description of a new adapter behavior beyond the split itself.

## Local decision tool schemas (revision 3)

`ReplyProposal { text }`, `ClarifyProposal { question }`, `ReportOutputsProposal { bindings: [{name, artifact}] }` and `CycleDecisionProposal { decision: continue|wait|escalate|done, reason, next_wake? }` are frozen in `docs/implementation/operations.json`'s `$defs` (via `tools/specgen/model.py`) as `LocalDecisionTool`, a `oneOf` over the four. They are the sealed names/inputs contract-proposals.md section 4 requires before P16 interprets model output: execution-local proposals the context builder registers as non-provider tool definitions, never routed through a `connections.Tool`/adapter and never given a provider operation mapping. A final `reply` alone may complete a chat turn; it never implicitly fulfills a task's required outputs, which only `report_outputs` (bound through `_tasks.evidence.record`) can do.


## Revision 12 — provider profile and stateless turn contracts

`zatiti.responses/v1`, `zatiti.responses.action/v1`, and `zatiti.responses.evidence/v1` remain immutable compatibility schemas. New profiles use `zatiti.responses/v2`; actions and evidence use `/v2`. The V2 profile requires `provider` (`openai`, `openrouter`, `experiential`) and `session_mode` (`provider_conversation`, `stateless`) and a strict provider-specific `routing` object, while retaining endpoint, model, connection, token/byte/time limits, currency, rates, enforcement and capability evidence. OpenAI uses `provider_conversation`; OpenRouter and Experiential use `stateless`. Provider and protocol revision must agree. Fixed provider endpoint presets cannot be overridden by model strings or arbitrary routing keys.

V2 action branches are discriminated by `session_mode`: conversation mode requires a nonempty provider-issued `session_handle`; stateless mode forbids both `session_handle` and `continuation_reference`. Optional `session_id` is a bounded controller-generated grouping label with no prompt or secret material. `prepare_session` exists only for conversation mode. V2 evidence carries the same mode distinction. Every adapter invocation remains exactly one physical request; unknown outcomes are never retried. V1 persisted profile/action/evidence remains decodable for replay and recovery.

Hosted `ExecutionProfile` gains optional-on-read `adapter_profile` and `connection_version`; legacy profiles remain readable but require explicit import/resolution before new hosted dispatch. Editable new profiles require both. The nested profile must agree with legacy model, connection, destination and cost fields. Immutable profile versions are retained for pending work; capability evidence binds the canonical digest of the complete adapter profile, so changing any model, route, endpoint, rate or bound invalidates it. `Action.execution_profile` is an optional exact VersionRef. Trusted `Dispatch.adapter_profile` is optional raw JSON populated only by Effects from `_configuration.execution_profile.resolve`; public callers cannot inject it. Effects persists the exact secret-free profile with the operation and returns it on claim/reconciliation, then rechecks current connection authority before send. Historical work never resolves a newer worker selection.

The controller assembly dependency struct gains a required `Context contract.ContextPerformer` field for hosted context work; it does not widen the common domain `contract.Dependencies`. The trusted context seam is `ContextPlan {ID, TurnID, ExpectedVersion, Generation, Refs []ArtifactRef, ConfigurationRevision, ByteBound, TokenBound}` and `ContextPerformer.PerformContext(ctx, plan) (json.RawMessage, error)`. Execution implements it by reading the persisted immutable recipe, building, validating and staging the complete context outside a write Unit. `_execution.context.commit` performs current-authority/generation/reference checks and publication bookkeeping. Controller receives this injected capability; there is no model-callable context IO operation.

`_execution.turn.observation` is a controller-only mutation with input `{turn_id, step_index, operation_id, observation}` and output `WorkerTurn`. It authenticates the persisted effects callback route, fences the turn generation/step and deduplicates by operation ID. Task-bound compatibility may share the logic, while task success continues to require its verifier.

`model.provider.list` is an authenticated scoped query owned by connections. It returns the fixed OpenAI/OpenRouter/Experiential IDs, display names, endpoint presets, supported session mode and API-key setup mode. It performs no catalog request. Saving uses the existing configuration draft/validate/plan/apply flow; provider credential validation is a separate bounded `connection.validate` job and does not qualify a model/route profile.

Normalized provider usage may include requested and served model IDs, serving provider, provider request ID, and exact source decimal cost evidence. Convert decimal USD to integer micro-units with checked integer/rational arithmetic and upward rounding, preserving the original decimal. No floating point is permitted. Missing, invalid or overflowing cost, disputed route, or unpriced BYOK upstream cost stays unknown/advisory; a gateway platform cost of zero is not evidence of zero upstream charge.

Revision 17 adds the internal query `_messaging.history` (caller: execution) for reconstructing a worker turn's complete chat transcript. Its worker identity is taken from the persisted turn, and Messaging verifies current conversation membership before reading sender and admitted-recipient rows. It returns up to 200 authorized rows chronologically and an explicit `complete` flag; older undisclosed or over-limit history is never silently dropped, and execution refuses provider dispatch when `complete` is false. The existing public `conversation.message.list` remains principal-scoped and unchanged. This closes the context-history gap without granting the controller or a client a history bypass.

## Owned product requirements

### R2.1-002 (source section 2.1; primary owner configuration)

One installation belongs to one operator or team and has one database, credential custody boundary, controller owner, and administrative trust root. It may contain multiple organizations. An organization is a grouping of teams, projects, workers, and configuration; it is not a hosted tenant.

### R2.1-003 (source section 2.1; primary owner configuration)

Organization and project scopes still restrict ordinary principals and workers. Cross-organization references require an explicit binding, and secrets are never globally available by default. These checks protect against accidental or unauthorized application access. They do not promise isolation from the installation administrator, database owner, or another process with unrestricted access to the same operating-system account.

### R2.1-004 (source section 2.1; primary owner configuration)

Organizations form an acyclic parent-child tree rooted in the personal organization. Bootstrap creates that root and its personal-chief identity; paid execution remains unavailable until the operator configures a provider and limits. Creating another organization creates its chief in the same configuration plan. Each active organization has exactly one designated chief, whose replacement preserves the organization's identity, memory, obligations, and history. Workers have one home organization; cross-organization assignments require explicit bindings. Creating an ordinary worker does not create an organization.

### R2.1-005 (source section 2.1; primary owner configuration)

Parent organizations impose permission ceilings and aggregate budget limits on descendants. Child policy can narrow those limits, never expand them. Parent chiefs receive authorized reports from children, not automatic access to every descendant's private memory, credentials, or project data. Moving an organization or worker is an explicit configuration plan that rechecks inherited limits and memory bindings; it cannot silently transfer old private memories or widen the authority of active work.

### R2.1-006 (source section 2.1; primary owner configuration)

There is no tenant routing, tenant signup, tenant billing, or hosted multitenant control plane in v1. Separate installations are the boundary for mutually untrusted operators. Installation IDs prevent accidental mixing of exports, credentials, and checkpoints; they are not tenant IDs in another form.

### R5-002 (source section 5; primary owner identity)

Bootstrap establishes a local owner under explicit OS-level installation access. `installation.init` is available through both interfaces before normal authentication exists, but only in a one-time local bootstrap mode. `zatiti init` and `zatiti mcp serve --bootstrap` use the same initializer. Bootstrap checks the destination is uninitialized, acquires exclusive ownership, creates owner credentials in the selected secure store, and returns metadata only. It refuses reinitialization; it never exports an owner token into model context. Bootstrap mode ends after successful initialization and cannot be used as an alternate administration session.

### R5-003 (source section 5; primary owner identity)

Normal CLI and MCP sessions select an explicitly provisioned local credential profile. The controller authenticates its credential and resolves a principal; a profile name, tool argument, MCP client name, socket access, or OS username alone does not establish application authority. MCP tool arguments cannot select a more privileged profile. Subprocesses do not receive the owner's credential environment by default. Same-user processes with access to the secure store remain within the OS trust boundary described in section 2.

### R5-004 (source section 5; primary owner identity)

An owner can delegate administration of named organizations, projects, workers, skills, and connections to a client-agent principal. This permits an agent to create and activate ordinary configuration within that existing envelope without a human confirming every edit. Requests outside the envelope produce a precise review or denial. Authority is evaluated using current grants and policy, never the proposed replacement policy.

### R5-005 (source section 5; primary owner identity)

Permissions intersect principal scope, project/organization binding, worker/task scope where applicable, policy, active restrictions, resource availability, and required review. Delegation only narrows tool access, destinations, deadlines, and shared budgets. Explicit denials win. Unknown required conditions refuse admission. Worker self-modification and imported skill text cannot install grants.

### R5-006 (source section 5; primary owner identity)

Policy supports standing authorized classes and exact-review classes. Default external publication, outbound messages, merges, deployment, credential-account substitution, and permission expansion require an eligible owner's decision unless the owner has explicitly established a narrower standing policy for that class. New policy is applied under old authority. Enabling automation, including an earned-autonomy promotion rule, is an explicit administrative act, not a competence score or model assertion.

### R5-007 (source section 5; primary owner identity)

Workers and chiefs start with minimum permissions. The product supports increasing autonomy up to the operator's explicitly authorized ceilings. Chiefs propose promotions using recorded outcomes; deterministic rules evaluate independently established evidence against operator-approved requirements before activating a narrowly scoped grant. Without an applicable promotion rule, expansion requires the eligible owner's decision. No worker can approve its own evidence, rewrite its qualification criteria, or enlarge the ceiling that permits promotion.

### R5-008 (source section 5; primary owner identity)

Qualifications bind capability, destination/scope, worker identity, relevant model/tool/skill versions, evidence window, and the promotion-rule version. Success in one capability does not grant unrelated powers. Relevant configuration changes trigger requalification; specified failures or incidents automatically restrict or demote the affected grant before further admission. Promotion and demotion produce durable events and user-visible explanations. Mandatory human-review classes remain mandatory unless the owner explicitly changes their governing policy under existing authority.

### R5-009 (source section 5; primary owner identity)

Reviews declare whether a human is required. The same `review.decide` operation exists in CLI and MCP; both enforce principal kind, eligibility, current version, action digest, expiry, and optional separation of proposer and reviewer. An agent credential cannot satisfy a human-required review, even if it sends `approved_by_human: true` or runs the CLI instead. A human session can use either transport. V1 does not claim a model invocation through a human-authorized process proves physical human presence; installation owners are responsible for credential delegation.

### R5-010 (source section 5; primary owner identity)

Pause, revoke, and cancel commit restrictive state immediately under authorized access, without a model call, configuration compilation, or spend reservation. Resume and expansion use normal authorization. A pause prevents future admissions; it cannot retract a request already transmitted or terminate an uncooperative external process by assertion.

### R7.1-002 (source section 7.1; primary owner skills)

A skill package contains instructions, optional bounded supporting files, input/output schemas, declared tool/data requirements, and optional evaluation fixtures. Support `SKILL.md` packages using the published Agent Skills format through a validating import adapter; store executable meaning in Zatiti's versioned metadata rather than inventing authority from frontmatter. Skills are reusable across organizations only through explicit bindings.

### R7.1-003 (source section 7.1; primary owner skills)

Import creates an immutable draft version with content hashes, source and license provenance, dependencies, and diagnostics. Reject path traversal, symlink escapes, device files, duplicate/case-colliding paths, oversized extraction, and dependency cycles before publication. Imported scripts do not execute during discovery, import, or archive extraction. Imported text and tool output remain untrusted data.

### R7.1-004 (source section 7.1; primary owner skills)

Evaluation can run against sealed fixture inputs before a worker activates. Pin the candidate, evaluator, inputs, model/profile, limits, and expected observations. Agent-generated tests are development evidence; they do not replace an independently accepted completion contract. A changed skill, evaluator, dependency, or model invalidates only the qualifications that depended on it. Evaluation does not automatically expand authority.

### R15-002 (source section 15; primary owner memory)

Serenity supplies accumulated knowledge; Zatiti owns execution state, authorization, acceptance, budgets, and recovery obligations. Use its public interfaces through a pinned adapter, without importing its internal packages or creating a competing canonical memory writer. The integration must qualify the pinned implementation rather than treating upstream documentation as proof of supported behavior.

### R15-003 (source section 15; primary owner memory)

Each worker has an individual brain, each organization has a shared brain, and the installation has a separate brain for deliberately shared cross-organizational knowledge. The personal chief's worker memory, the root organization's shared memory, and installation-wide memory remain distinct scopes even when one chief curates them. Separate brains are logical access boundaries enforced by Zatiti's bindings; they do not isolate data from the installation administrator or unrestricted same-user filesystem access.

### R15-004 (source section 15; primary owner memory)

Memory bindings explicitly name read, write, curate/promote, and retract permissions. Zatiti resolves the caller's current worker, task, project, organization ancestry, and grants before selecting brains to query. Filter before retrieval or model composition, not after unauthorized data has already reached a model. An organization brain contains only knowledge suitable for its authorized readers; restricted project information remains in a narrower bound brain or governed artifact. Parentage alone never grants read access to a child's private knowledge.

### R15-005 (source section 15; primary owner memory)

Organization chiefs automatically curate shared memory within their standing authority: reconcile evidence, retain useful lessons, identify contradictions, and promote suitable knowledge from permitted worker or child-organization sources. The personal chief curates installation-wide memory. Automatic curation is scoped work with budgets and evidence, not permission to read everything. Promotion is a new destination claim linked to source brain, claim/version, supporting artifact references, curator identity, and any redaction. It requires both source disclosure authority and destination write authority. Shared-memory writes that exceed that envelope wait for the appropriate decision.

### R15-006 (source section 15; primary owner memory)

Corrections and retractions propagate through recorded promotion lineage as durable reconciliation obligations. A promoted statement is not independent corroboration of its own source. Revocation blocks subsequent retrieval immediately; it cannot erase already disclosed context. Forget/retract operations distinguish removal from active recall from historical erasure, including copies in Git history, backups, and prior run artifacts. The UI must state that distinction when relevant.

### R15-007 (source section 15; primary owner memory)

The adapter provides memory recall, remember, inspect, promotion, and retraction operations through the shared registry and exposes their supported prerequisites and outcomes equally through CLI and MCP. Desktop memory controls and conversational requests call those same operations. Recalled results retain scope, source references, confidence, relevant versions, and freshness; each run stores the actual selected context as an artifact. Reads across brains are not presumed to be one atomic snapshot. A task requiring unavailable freshness waits or reports a prerequisite failure rather than silently treating stale context as current.

### R15-008 (source section 15; primary owner memory)

Serenity's exported Go read facade may serve compatible reads. Canonical writes go to exactly one writer owner per brain through the supported protocol. Its Recall path may invoke models and record spend: "read" does not imply no disclosure or no charge. Composition, embedding, extraction, and curation must obey Zatiti's provider disclosure rules and reservations; until the adapter can enforce a required bound, that execution mode is unavailable or explicitly advisory where policy permits. Serenity's remembered judgments and plan checks inform work but cannot override Zatiti policy or constitute task acceptance on their own.

### R15-009 (source section 15; primary owner memory)

Zatiti's SQLite transaction cannot atomically commit a Serenity write. Persist a submission intent and adapter command identity before dispatch, record the actual disposition afterward, and reconcile a lost acknowledgment without blindly repeating the write. Backup manifests pin the required brain revisions and key-recovery prerequisites alongside Zatiti state. Restore starts paused and reconciles pending memory writes and promotions before resuming curation. Availability failures remain visible; no failed memory operation is presented as remembered knowledge.

### P00-014 (source section P00; primary owner identity)

_identity.bootstrap accepts optional service_credential_id/service_store_ref so the bootstrap-created controller service principal can receive a credential in the same transaction, letting an out-of-process controller authenticate; an in-process explicit-Actor seam may omit them and remains credential-less as in revision 2. _identity.authority, like every internal operation, is gated by its caller allowlist and by the calling actor being a registered, unrevoked principal in the transaction's installation -- never by requiring that actor to already hold the capability named in its own request; no standing grant of _identity.authority is needed to work around a circular subject-capability check.

### P00-015 (source section P00; primary owner reviews)

The eligible reviewer of an exact review-class request is the human principal whose current authority admitted that request; services, workers and agents are never eligible reviewers, and proposer separation stays mandatory for them regardless of any narrower standing policy. This settles "an eligible owner's decision", left undefined in the revision 2 policy/reviews briefs.
## Exact operation and dependency schemas

### `_accounting.inspect` v1 — accounting / internal / query / local

Allowed internal callers: policy, tasks, execution, effects, scheduling, installation. Submission key: not required at this internal/query/bootstrap boundary.

Return current intersected limits and honest usage to admission/doctor.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"limits":{"$ref":"#/$defs/Limits"},"usage":{"$ref":"#/$defs/Usage"}},"required":["limits","usage"]}
```

### `_configuration.snapshot` v1 — configuration / internal / query / local

Allowed internal callers: application, policy, tasks, execution, effects, memory, reviews, accounting, scheduling, messaging, connections, installation. Submission key: not required at this internal/query/bootstrap boundary.

Read current ancestry, effective bindings, worker/project and revision; no automatic descendant private data access.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/ScopeSnapshot"}},"required":["resource"]}
```

### `_configuration.stage` v1 — configuration / internal / mutation / local

Allowed internal callers: configuration, skills, connections, policy, accounting, scheduling, memory. Submission key: not required at this internal/query/bootstrap boundary.

Strictly validate typed definition schema then append draft change. No effective mutation. For creates allocate identity once using submission replay.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"change":{"$ref":"#/$defs/Change"},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","change"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Draft"}},"required":["resource"]}
```

### `_identity.authority` v1 — identity / internal / query / local

Allowed internal callers: application, policy, reviews, configuration, execution, effects. Submission key: not required at this internal/query/bootstrap boundary.

Read current principal, grants, expiry/revocations and restrictions; no grants from claimed profile/name or proposed policy.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"principal_id":{"type":"string","format":"uuid"},"scope":{"$ref":"#/$defs/Scope"}},"required":["principal_id","scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Authority"}},"required":["resource"]}
```

### `_identity.promote` v1 — identity / internal / mutation / local

Allowed internal callers: policy. Submission key: not required at this internal/query/bootstrap boundary.

Activate exact evidence-qualified narrow grant after old-policy rule check; verify ceiling/current rule version and immutable qualification.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"principal_id":{"type":"string","format":"uuid"},"qualification":{"$ref":"#/$defs/Qualification"},"ceiling_grant_id":{"type":"string","format":"uuid"}},"required":["principal_id","qualification","ceiling_grant_id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Grant"}},"required":["resource"]}
```

### `_identity.restrict` v1 — identity / internal / mutation / local

Allowed internal callers: policy, installation. Submission key: not required at this internal/query/bootstrap boundary.

Atomically narrow/revoke effective grant before future admission, never expand.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"principal_id":{"type":"string","format":"uuid"},"capability":{"type":"string","maxLength":8192},"reason":{"type":"string","maxLength":8192}},"required":["principal_id","capability","reason"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Disposition"}},"required":["resource"]}
```

### `_policy.activate` v1 — policy / internal / mutation / local

Allowed internal callers: configuration, application. Submission key: not required at this internal/query/bootstrap boundary.

Apply owned exact sealed candidate slice inside compiler transaction; caller must hold configuration-apply context established by application. No public activation flag or second compiler.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"candidate":{"$ref":"#/$defs/Candidate"}},"required":["candidate"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"versions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096}},"required":["versions"]}
```

### `_policy.check` v1 — policy / internal / query / local

Allowed internal callers: application, configuration, tasks, execution, effects, memory, messaging, connections, installation, reviews, accounting. Submission key: not required at this internal/query/bootstrap boundary.

Intersect authenticated current grants, ancestry/bindings, task/worker scope, policy, restrictions and required conditions. Explicit deny wins; unknown required conditions fail closed. Check exact human review requirements without accepting user assertions.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"capability":{"type":"string","maxLength":8192},"action":{"$ref":"#/$defs/Action"},"candidate_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"}},"required":["scope","capability"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/PolicyResult"}},"required":["resource"]}
```

### `_policy.invalidate` v1 — policy / internal / mutation / local

Allowed internal callers: configuration, skills, connections, execution, tasks. Submission key: not required at this internal/query/bootstrap boundary.

Invalidate only dependent qualifications, immediately restrict affected grants and emit explanations before further admission.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"changed_dependencies":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"reason":{"type":"string","maxLength":8192}},"required":["changed_dependencies","reason"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"qualification_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096}},"required":["qualification_ids"]}
```

### `_policy.validate` v1 — policy / internal / query / local

Allowed internal callers: configuration, application. Submission key: not required at this internal/query/bootstrap boundary.

Validate only owned candidate slice against current snapshot, collect dependency identities/requirements; no live changes or network. Candidate changes must match their registered concrete definition schemas. Expected-version zero is create-only. Authorization comes from old effective state.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"candidate":{"$ref":"#/$defs/Candidate"}},"required":["candidate"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Validation"}},"required":["resource"]}
```

### `_reviews.check` v1 — reviews / internal / query / local

Allowed internal callers: effects, configuration, policy, tasks. Submission key: not required at this internal/query/bootstrap boundary.

Recheck current eligible reviewer/grants, principal kind, expiry, version, proposer separation and exact action digest; false is not permission.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"action_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"}},"required":["scope","action_digest"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"eligible":{"type":"boolean"},"decision":{"$ref":"#/$defs/Decision"}},"required":["eligible"]}
```

### `_reviews.ensure` v1 — reviews / internal / mutation / local

Allowed internal callers: effects, configuration, policy. Submission key: not required at this internal/query/bootstrap boundary.

Create or inspect exact digest-bound review, retaining immutable history and eligibility constraints.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"action":{"$ref":"#/$defs/Action"},"requirement":{"$ref":"#/$defs/DecisionRequirement"}},"required":["scope","action","requirement"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Review"}},"required":["resource"]}
```

### `_tasks.snapshot` v1 — tasks / internal / query / local

Allowed internal callers: execution, effects, scheduling, memory, reviews, policy. Submission key: not required at this internal/query/bootstrap boundary.

Return current pinned contract and task scope, parent/root/dependency state; caller still obeys authority.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Task"}},"required":["resource"]}
```

### `autonomy.demote` v1 — policy / public / mutation / local

CLI `zatiti autonomy demote`; MCP `zatiti_autonomy_demote`. Submission key: required.

Commit immediate capability-specific restriction before any further admission, append explanatory event, retain evidence and grant history.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"reason":{"type":"string","maxLength":8192},"evidence_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096}},"required":["scope","id","expected_version","reason","evidence_ids"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Qualification"}},"required":["resource"]}
```

### `autonomy.evaluate` v1 — policy / public / mutation / local

CLI `zatiti autonomy evaluate`; MCP `zatiti_autonomy_evaluate`. Submission key: required.

Deterministically evaluate independently established evidence against exact prior rule/version and configuration. Apply narrowly eligible grant only within old ceiling; mandatory human classes remain. Otherwise create review/denial.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Qualification"}},"required":["resource"]}
```

### `autonomy.propose` v1 — policy / public / mutation / local

CLI `zatiti autonomy propose`; MCP `zatiti_autonomy_propose`. Submission key: required.

Record scoped proposal; proposer cannot approve own evidence or widen criteria/ceiling.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"worker_id":{"type":"string","format":"uuid"},"rule":{"$ref":"#/$defs/Ref"},"evidence_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096}},"required":["scope","worker_id","rule","evidence_ids"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Qualification"}},"required":["resource"]}
```

### `autonomy.qualification.get` v1 — policy / public / query / local

CLI `zatiti autonomy qualification get`; MCP `zatiti_autonomy_qualification_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Qualification"}},"required":["resource"]}
```

### `autonomy.qualification.list` v1 — policy / public / query / local

CLI `zatiti autonomy qualification list`; MCP `zatiti_autonomy_qualification_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Qualification"},"maxItems":500}},"required":["items"]}
```

### `autonomy.restrict` v1 — policy / public / mutation / local

CLI `zatiti autonomy restrict`; MCP `zatiti_autonomy_restrict`. Submission key: required.

Commit immediate capability-specific restriction before any further admission, append explanatory event, retain evidence and grant history.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"reason":{"type":"string","maxLength":8192},"evidence_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096}},"required":["scope","id","expected_version","reason","evidence_ids"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Qualification"}},"required":["resource"]}
```

### `autonomy.rule.archive` v1 — policy / public / mutation / local

CLI `zatiti autonomy rule archive`; MCP `zatiti_autonomy_rule_archive`. Submission key: required.

Stage a typed archive in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/PromotionRule"}},"required":["draft","resource"]}
```

### `autonomy.rule.create` v1 — policy / public / mutation / local

CLI `zatiti autonomy rule create`; MCP `zatiti_autonomy_rule_create`. Submission key: required.

Stage a typed create in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"definition":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"capability":{"type":"string","maxLength":8192},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"required_evidence":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"minimum_successes":{"type":"integer","minimum":1,"maximum":9223372036854775807},"evidence_window_seconds":{"type":"integer","minimum":1,"maximum":9223372036854775807},"disqualifying_events":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"ceiling_grant_id":{"type":"string","format":"uuid"},"human_required_preserved":{"type":"boolean"}},"required":["scope","capability","destinations","required_evidence","minimum_successes","evidence_window_seconds","disqualifying_events","ceiling_grant_id","human_required_preserved"]},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/PromotionRule"}},"required":["draft","resource"]}
```

### `autonomy.rule.get` v1 — policy / public / query / local

CLI `zatiti autonomy rule get`; MCP `zatiti_autonomy_rule_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/PromotionRule"}},"required":["resource"]}
```

### `autonomy.rule.list` v1 — policy / public / query / local

CLI `zatiti autonomy rule list`; MCP `zatiti_autonomy_rule_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/PromotionRule"},"maxItems":500}},"required":["items"]}
```

### `autonomy.rule.update` v1 — policy / public / mutation / local

CLI `zatiti autonomy rule update`; MCP `zatiti_autonomy_rule_update`. Submission key: required.

Stage a typed update in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"definition":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"capability":{"type":"string","maxLength":8192},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"required_evidence":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"minimum_successes":{"type":"integer","minimum":1,"maximum":9223372036854775807},"evidence_window_seconds":{"type":"integer","minimum":1,"maximum":9223372036854775807},"disqualifying_events":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"ceiling_grant_id":{"type":"string","format":"uuid"},"human_required_preserved":{"type":"boolean"}},"required":["scope","capability","destinations","required_evidence","minimum_successes","evidence_window_seconds","disqualifying_events","ceiling_grant_id","human_required_preserved"]},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/PromotionRule"}},"required":["draft","resource"]}
```

### `policy.archive` v1 — policy / public / mutation / local

CLI `zatiti policy archive`; MCP `zatiti_policy_archive`. Submission key: required.

Stage a typed archive in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Policy"}},"required":["draft","resource"]}
```

### `policy.create` v1 — policy / public / mutation / local

CLI `zatiti policy create`; MCP `zatiti_policy_create`. Submission key: required.

Stage a typed create in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"definition":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"rules":{"type":"array","items":{"$ref":"#/$defs/Rule"},"maxItems":4096},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["scope","rules"]},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Policy"}},"required":["draft","resource"]}
```

### `policy.explain` v1 — policy / public / query / local

CLI `zatiti policy explain`; MCP `zatiti_policy_explain`. Submission key: not required at this internal/query/bootstrap boundary.

Evaluate current intersected authority and explain exact action without granting or dispatching it.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"action":{"$ref":"#/$defs/Action"}},"required":["scope","action"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"decision":{"type":"string","enum":["allow","deny","review","prerequisite_missing"]},"reasons":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"requirements":{"type":"array","items":{"$ref":"#/$defs/DecisionRequirement"},"maxItems":4096}},"required":["decision","reasons","requirements"]}
```

### `policy.get` v1 — policy / public / query / local

CLI `zatiti policy get`; MCP `zatiti_policy_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Policy"}},"required":["resource"]}
```

### `policy.list` v1 — policy / public / query / local

CLI `zatiti policy list`; MCP `zatiti_policy_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Policy"},"maxItems":500}},"required":["items"]}
```

### `policy.update` v1 — policy / public / mutation / local

CLI `zatiti policy update`; MCP `zatiti_policy_update`. Submission key: required.

Stage a typed update in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"definition":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"rules":{"type":"array","items":{"$ref":"#/$defs/Rule"},"maxItems":4096},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["scope","rules"]},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Policy"}},"required":["draft","resource"]}
```

### Local schema definitions

The schemas above resolve exclusively against this embedded `$defs` object. Input objects reject additional properties except explicitly open schema/data fields. Field semantics are completed by the owned requirements and operation descriptions. Output `resource`, `items`, `draft`, `job`, etc. are literal keys. Pagination cursor lives in the common envelope.

```json
{"$defs":{"Acceptance":{"type":"object","additionalProperties":false,"properties":{"verifier_id":{"type":"string","maxLength":8192},"verifier_version":{"type":"string","maxLength":8192},"sealed_inputs":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"expected_observations":{"type":"array","items":{"$ref":"#/$defs/Adapter_ExpectedVerificationObservation"},"maxItems":512},"mode":{"type":"string","enum":["independent","manual"]},"required_child_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"profile":{"$ref":"#/$defs/Adapter_VerificationProfile"}},"required":["verifier_id","verifier_version","sealed_inputs","expected_observations","mode","required_child_ids","profile"]},"Action":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"tool":{"$ref":"#/$defs/Ref"},"connection":{"$ref":"#/$defs/Ref"},"account_identity":{"type":"string","maxLength":8192},"destination":{"type":"string","maxLength":8192},"content":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"not_before":{"type":"string","format":"date-time"},"expires_at":{"type":"string","format":"date-time"},"preconditions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"configuration_revision":{"type":"integer","minimum":1,"maximum":9223372036854775807},"parameters":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"cost_bound":{"$ref":"#/$defs/Money"},"execution_profile":{"$ref":"#/$defs/Ref"}},"required":["scope","tool","connection","account_identity","destination","content","not_before","expires_at","preconditions","configuration_revision","parameters","cost_bound"]},"Adapter_ArtifactRef":{"type":"object","additionalProperties":false,"properties":{"id":{"$ref":"#/$defs/Adapter_ID"},"digest":{"$ref":"#/$defs/Adapter_Digest"}},"required":["id","digest"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_ArtifactVerifierProfile":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.verifier-profile/v1","type":"string"},"kind":{"const":"artifact_contract","type":"string"},"id":{"type":"string","minLength":1,"maxLength":256},"version":{"type":"string","minLength":1,"maxLength":128},"code_digest":{"$ref":"#/$defs/Adapter_Digest"},"supported_checks":{"type":"array","items":{"type":"string","enum":["presence","digest","json_schema"]},"minItems":1,"maxItems":3},"max_bytes":{"type":"integer","minimum":1,"maximum":268435456},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"capability_evidence":{"$ref":"#/$defs/Adapter_CapabilityEvidence"}},"required":["schema","kind","id","version","code_digest","supported_checks","max_bytes","timeout_seconds","capability_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_CapabilityEvidence":{"type":"object","additionalProperties":false,"properties":{"artifact":{"$ref":"#/$defs/Adapter_ArtifactRef"},"adapter_version":{"type":"string","minLength":1,"maxLength":128},"source_revision":{"type":"string","minLength":1,"maxLength":128},"protocol_revision":{"type":"string","minLength":1,"maxLength":128},"profile_digest":{"$ref":"#/$defs/Adapter_Digest"},"qualified_at":{"$ref":"#/$defs/Adapter_UTC"},"capabilities":{"type":"array","items":{"type":"string","minLength":1,"maxLength":128},"minItems":0,"maxItems":128},"limitations":{"type":"array","items":{"type":"string","minLength":1,"maxLength":2048},"minItems":0,"maxItems":128}},"required":["artifact","adapter_version","source_revision","protocol_revision","profile_digest","qualified_at","capabilities","limitations"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_Digest":{"type":"string","pattern":"^[0-9a-f]{64}$","maxLength":64,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_ExpectedVerificationObservation":{"type":"object","additionalProperties":false,"properties":{"check_id":{"type":"string","minLength":1,"maxLength":256},"kind":{"type":"string","enum":["artifact_presence","artifact_digest","json_schema","repository_patch_applies","repository_command"]},"expected":{"type":"string","enum":["pass","fail"]},"artifact_name":{"type":"string","minLength":1,"maxLength":128},"expected_digest":{"$ref":"#/$defs/Adapter_Digest"},"schema":{"$ref":"#/$defs/Adapter_InertSchema"},"command_id":{"type":"string","minLength":1,"maxLength":256},"expected_exit_code":{"type":"integer","minimum":-2147483648,"maximum":2147483647}},"required":["check_id","kind","expected"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_ID":{"type":"string","format":"uuid","maxLength":36,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_InertSchema":{"type":"object","description":"A bounded JSON Schema 2020-12 document. Local references only; validators reject remote references, duplicate keys, excessive depth/node count and unsupported executable behavior. This is schema data, not authority.","maxProperties":256,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_RepositoryVerifierProfile":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.verifier-profile/v1","type":"string"},"kind":{"const":"repository_patch","type":"string"},"id":{"type":"string","minLength":1,"maxLength":256},"version":{"type":"string","minLength":1,"maxLength":128},"code_digest":{"$ref":"#/$defs/Adapter_Digest"},"runner_profile":{"type":"string","minLength":1,"maxLength":256},"command_id":{"type":"string","minLength":1,"maxLength":256},"command_digest":{"$ref":"#/$defs/Adapter_Digest"},"environment_profile":{"type":"string","minLength":1,"maxLength":256},"network":{"type":"string","enum":["disabled","qualified_allowlist"]},"max_output_bytes":{"type":"integer","minimum":1,"maximum":1048576},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"capability_evidence":{"$ref":"#/$defs/Adapter_CapabilityEvidence"}},"required":["schema","kind","id","version","code_digest","runner_profile","command_id","command_digest","environment_profile","network","max_output_bytes","timeout_seconds","capability_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_UTC":{"type":"string","format":"date-time","pattern":"Z$","maxLength":40,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_VerificationProfile":{"oneOf":[{"$ref":"#/$defs/Adapter_ArtifactVerifierProfile"},{"$ref":"#/$defs/Adapter_RepositoryVerifierProfile"}],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ArtifactRef":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"digest":{"type":"string","pattern":"^[0-9a-f]{64}$"}},"required":["id","digest"]},"Authority":{"type":"object","additionalProperties":false,"properties":{"principal":{"$ref":"#/$defs/Principal"},"grants":{"type":"array","items":{"$ref":"#/$defs/Grant"},"maxItems":4096},"restrictions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096}},"required":["principal","grants","restrictions"]},"Binding":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"kind":{"type":"string","enum":["tool","skill","connection","worker","repository","reporting","memory"]},"target_id":{"type":"string","format":"uuid"},"permissions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"source_scope":{"$ref":"#/$defs/Scope"},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096}},"required":["id","version","scope","kind","target_id","permissions"]},"CallbackRoute":{"type":"object","additionalProperties":false,"properties":{"kind":{"type":"string","enum":["worker_turn","job","memory","skill","connection"]},"turn_id":{"type":"string","format":"uuid"},"step_index":{"type":"integer","minimum":0,"maximum":9223372036854775807},"job_id":{"type":"string","format":"uuid"}},"required":["kind"]},"Candidate":{"type":"object","additionalProperties":false,"properties":{"plan_id":{"type":"string","format":"uuid"},"base_revision":{"type":"integer","minimum":1,"maximum":9223372036854775807},"candidate_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"changes":{"type":"array","items":{"$ref":"#/$defs/Change"},"maxItems":4096},"dependencies":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096}},"required":["plan_id","base_revision","candidate_digest","changes","dependencies"]},"Change":{"oneOf":[{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"organization"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Organization"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"team"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Team"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"project"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Project"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"worker"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Worker"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"binding"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Binding"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"execution_profile"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/ExecutionProfile"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"skill"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Skill"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"connection"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Connection"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"policy"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Policy"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"autonomy_rule"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/PromotionRule"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"schedule"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Schedule"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"responsibility"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Responsibility"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"memory_binding"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/MemoryBinding"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"budget"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Limits"}},"required":["kind","action","id","expected_version","definition"]}]},"Connection":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"provider":{"type":"string","maxLength":8192},"account_identity":{"type":"string","maxLength":8192},"credential_ref":{"type":"string","maxLength":8192},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"allowed_scopes":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"validation_state":{"type":"string","enum":["unverified","valid","invalid","expired","revoked"]},"validated_at":{"type":"string","format":"date-time"},"valid_until":{"type":"string","format":"date-time"},"hosted_memory_grant":{"$ref":"#/$defs/HostedMemoryGrant"}},"required":["id","version","scope","provider","account_identity","credential_ref","destinations","allowed_scopes","validation_state"]},"Decision":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"review_id":{"type":"string","format":"uuid"},"review_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"action_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"reviewer_id":{"type":"string","format":"uuid"},"decision":{"type":"string","enum":["approve","reject"]},"at":{"type":"string","format":"date-time"},"reason":{"type":"string","maxLength":8192}},"required":["id","review_id","review_version","action_digest","reviewer_id","decision","at","reason"]},"DecisionRequirement":{"type":"object","additionalProperties":false,"properties":{"action_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"human_required":{"type":"boolean"},"eligible_principals":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"expires_at":{"type":"string","format":"date-time"},"separate_proposer":{"type":"boolean"}},"required":["action_digest","human_required","eligible_principals","expires_at","separate_proposer"]},"Diagnostic":{"type":"object","additionalProperties":false,"properties":{"path":{"type":"string","maxLength":8192},"code":{"type":"string","maxLength":8192},"message":{"type":"string","maxLength":8192},"severity":{"type":"string","enum":["error","warning","info"]}},"required":["path","code","message","severity"]},"Disposition":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"state":{"type":"string","maxLength":8192},"job":{"$ref":"#/$defs/Job"},"operation":{"$ref":"#/$defs/Operation"},"requirements":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096}},"required":["id","version","state"]},"Draft":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"base_revision":{"type":"integer","minimum":1,"maximum":9223372036854775807},"changes":{"type":"array","items":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"maxItems":4096},"diagnostics":{"type":"array","items":{"$ref":"#/$defs/Diagnostic"},"maxItems":4096}},"required":["id","version","base_revision","changes","diagnostics"]},"ExecutionProfile":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"executor":{"type":"string","enum":["hosted","cooperative"]},"model":{"type":"string","maxLength":8192},"connection_id":{"type":"string","format":"uuid"},"provider_destination":{"type":"string","maxLength":8192},"capabilities":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"cost_bound":{"$ref":"#/$defs/Money"},"classification":{"type":"string","enum":["internal","public","restricted"]},"context_capture":{"type":"string","enum":["complete","partial","advisory"]},"adapter_profile":{"$ref":"#/$defs/ResponsesProfile"},"connection_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["id","version","executor","model","connection_id","provider_destination","capabilities","cost_bound","classification","context_capture"]},"Grant":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"principal_id":{"type":"string","format":"uuid"},"scope":{"$ref":"#/$defs/Scope"},"capabilities":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"denied":{"type":"boolean"},"expires_at":{"type":"string","format":"date-time"},"parent_grant_id":{"type":"string","format":"uuid"}},"required":["id","version","principal_id","scope","capabilities","destinations","denied"]},"HostedMemoryGrant":{"type":"object","additionalProperties":false,"properties":{"issuer":{"type":"string","maxLength":8192},"resource":{"type":"string","maxLength":8192},"account_id":{"type":"string","maxLength":8192},"project_id":{"type":"string","maxLength":8192},"scopes":{"type":"array","items":{"type":"string","enum":["memory:read","memory:write"]},"maxItems":2},"verified_at":{"type":"string","format":"date-time"}},"required":["issuer","resource","account_id","project_id","scopes","verified_at"]},"Job":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"kind":{"type":"string","maxLength":8192},"state":{"type":"string","enum":["pending","running","succeeded","failed","outcome_unknown","cancelled"]},"requirements":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096},"result_artifact":{"$ref":"#/$defs/ArtifactRef"},"operation_id":{"type":"string","format":"uuid"},"owner":{"type":"string","maxLength":8192},"operation":{"type":"string","maxLength":8192},"result":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","kind","state","requirements","owner","operation"]},"Limits":{"type":"object","additionalProperties":false,"properties":{"currency":{"type":"string","pattern":"^[A-Z]{3}$"},"spend_micro_units":{"type":"integer","minimum":0,"maximum":9223372036854775807},"concurrency":{"type":"integer","minimum":1,"maximum":9223372036854775807},"model_steps":{"type":"integer","minimum":1,"maximum":9223372036854775807},"child_count":{"type":"integer","minimum":0,"maximum":9223372036854775807},"delegation_depth":{"type":"integer","minimum":0,"maximum":9223372036854775807},"attempt_seconds":{"type":"integer","minimum":1,"maximum":9223372036854775807},"root_deadline":{"type":"string","format":"date-time"}},"required":["currency","spend_micro_units","concurrency","model_steps","child_count","delegation_depth","attempt_seconds","root_deadline"]},"MemoryBinding":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"brain_id":{"type":"string","format":"uuid"},"permissions":{"type":"array","items":{"type":"string","enum":["read","write","curate","promote","retract"]},"maxItems":4096},"classification":{"type":"string","enum":["internal","public","restricted"]}},"required":["id","version","scope","brain_id","permissions","classification"]},"Money":{"type":"object","additionalProperties":false,"properties":{"currency":{"type":"string","pattern":"^[A-Z]{3}$"},"micro_units":{"type":"integer","minimum":0,"maximum":9223372036854775807}},"required":["currency","micro_units"]},"Operation":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"action":{"$ref":"#/$defs/Action"},"action_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"state":{"type":"string","enum":["prepared","awaiting_review","ready","executing","awaiting_confirmation","outcome_unknown","succeeded","failed","denied","expired","cancelled"]},"attempt_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"linked_operation_id":{"type":"string","format":"uuid"},"relationship":{"type":"string","enum":["retry","reconciliation","compensation","replacement"]},"attempts":{"type":"array","items":{"$ref":"#/$defs/OperationAttempt"},"maxItems":4096},"callback_route":{"$ref":"#/$defs/CallbackRoute"}},"required":["id","version","action","action_digest","state","attempt_ids"]},"OperationAttempt":{"type":"object","additionalProperties":false,"properties":{"attempt_id":{"type":"string","format":"uuid"},"generation":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["attempt_id","generation"]},"Organization":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"chief_id":{"type":"string","format":"uuid"},"parent_id":{"type":"string","format":"uuid"},"limits":{"$ref":"#/$defs/Limits"},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","key","name","chief_id"]},"Policy":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"rules":{"type":"array","items":{"$ref":"#/$defs/Rule"},"maxItems":4096},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","scope","rules"]},"PolicyResult":{"type":"object","additionalProperties":false,"properties":{"decision":{"type":"string","enum":["allow","deny","review","prerequisite_missing"]},"reasons":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"requirements":{"type":"array","items":{"$ref":"#/$defs/DecisionRequirement"},"maxItems":4096}},"required":["decision","reasons","requirements"]},"Principal":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"kind":{"type":"string","enum":["human","client_agent","worker","service"]},"name":{"type":"string","maxLength":8192},"scope":{"$ref":"#/$defs/Scope"},"revoked":{"type":"boolean"}},"required":["id","version","kind","name","scope","revoked"]},"Project":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"repositories":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"bindings":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"classification":{"type":"string","enum":["internal","public","restricted"]},"limits":{"$ref":"#/$defs/Limits"},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","organization_id","key","name","repositories","bindings","classification"]},"PromotionRule":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"capability":{"type":"string","maxLength":8192},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"required_evidence":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"minimum_successes":{"type":"integer","minimum":1,"maximum":9223372036854775807},"evidence_window_seconds":{"type":"integer","minimum":1,"maximum":9223372036854775807},"disqualifying_events":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"ceiling_grant_id":{"type":"string","format":"uuid"},"human_required_preserved":{"type":"boolean"}},"required":["id","version","scope","capability","destinations","required_evidence","minimum_successes","evidence_window_seconds","disqualifying_events","ceiling_grant_id","human_required_preserved"]},"Qualification":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"worker_id":{"type":"string","format":"uuid"},"capability":{"type":"string","maxLength":8192},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"rule":{"$ref":"#/$defs/Ref"},"model":{"type":"string","maxLength":8192},"tool_versions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"skill_versions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"evidence_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"window_start":{"type":"string","format":"date-time"},"window_end":{"type":"string","format":"date-time"},"state":{"type":"string","enum":["proposed","qualified","rejected","restricted","expired"]},"explanation":{"type":"string","maxLength":8192}},"required":["id","version","worker_id","capability","destinations","rule","model","tool_versions","skill_versions","evidence_ids","window_start","window_end","state","explanation"]},"Ref":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["id","version"]},"Requirement":{"type":"object","additionalProperties":false,"properties":{"code":{"type":"string","maxLength":8192},"message":{"type":"string","maxLength":8192},"resource_id":{"type":"string","format":"uuid"},"challenge_id":{"type":"string","format":"uuid"}},"required":["code","message"]},"ResponsesCapabilityEvidence":{"type":"object","additionalProperties":false,"properties":{"artifact":{"$ref":"#/$defs/ArtifactRef"},"adapter_version":{"type":"string","minLength":1,"maxLength":128},"source_revision":{"type":"string","minLength":1,"maxLength":128},"protocol_revision":{"type":"string","minLength":1,"maxLength":128},"profile_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"qualified_at":{"type":"string","format":"date-time"},"capabilities":{"type":"array","items":{"type":"string","minLength":1,"maxLength":128},"minItems":0,"maxItems":128},"limitations":{"type":"array","items":{"type":"string","minLength":1,"maxLength":2048},"minItems":0,"maxItems":128}},"required":["artifact","adapter_version","source_revision","protocol_revision","profile_digest","qualified_at","capabilities","limitations"]},"ResponsesEnforcement":{"type":"object","additionalProperties":false,"properties":{"cost":{"type":"string","enum":["enforced","advisory","unsupported"]},"disclosure":{"type":"string","enum":["enforced","advisory","unsupported"]},"maximum_cost":{"$ref":"#/$defs/Money"},"provider_destinations":{"type":"array","items":{"type":"string","format":"uri","pattern":"^https://","maxLength":2048},"minItems":0,"maxItems":64}},"required":["cost","disclosure","maximum_cost","provider_destinations"]},"ResponsesProfile":{"oneOf":[{"$ref":"#/$defs/ResponsesProfileV1"},{"$ref":"#/$defs/ResponsesProfileV2"}]},"ResponsesProfileV1":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.responses/v1","type":"string"},"endpoint":{"type":"string","format":"uri","pattern":"^https://","maxLength":2048},"model":{"type":"string","minLength":1,"maxLength":256},"connection_id":{"type":"string","format":"uuid"},"max_input_tokens":{"type":"integer","minimum":1,"maximum":10000000},"max_output_tokens":{"type":"integer","minimum":1,"maximum":1000000},"max_response_bytes":{"type":"integer","minimum":1,"maximum":268435456},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"currency":{"type":"string","pattern":"^[A-Z]{3}$"},"input_rate":{"$ref":"#/$defs/ResponsesRate"},"output_rate":{"$ref":"#/$defs/ResponsesRate"},"enforcement":{"$ref":"#/$defs/ResponsesEnforcement"},"capability_evidence":{"$ref":"#/$defs/ResponsesCapabilityEvidence"}},"required":["schema","endpoint","model","connection_id","max_input_tokens","max_output_tokens","max_response_bytes","timeout_seconds","currency","input_rate","output_rate","enforcement","capability_evidence"]},"ResponsesProfileV2":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.responses/v2","type":"string"},"endpoint":{"type":"string","format":"uri","pattern":"^https://","maxLength":2048},"model":{"type":"string","minLength":1,"maxLength":256},"connection_id":{"type":"string","format":"uuid"},"max_input_tokens":{"type":"integer","minimum":1,"maximum":10000000},"max_output_tokens":{"type":"integer","minimum":1,"maximum":1000000},"max_response_bytes":{"type":"integer","minimum":1,"maximum":268435456},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"currency":{"type":"string","pattern":"^[A-Z]{3}$"},"input_rate":{"$ref":"#/$defs/ResponsesRate"},"output_rate":{"$ref":"#/$defs/ResponsesRate"},"enforcement":{"$ref":"#/$defs/ResponsesEnforcement"},"capability_evidence":{"$ref":"#/$defs/ResponsesCapabilityEvidence"},"provider":{"type":"string","enum":["openai","openrouter","experiential"]},"session_mode":{"type":"string","enum":["provider_conversation","stateless"]},"routing":{"oneOf":[{"type":"object","additionalProperties":false,"properties":{},"required":[]},{"$ref":"#/$defs/ResponsesRoutingOpenRouter"},{"$ref":"#/$defs/ResponsesRoutingExperiential"}]}},"required":["schema","endpoint","model","connection_id","max_input_tokens","max_output_tokens","max_response_bytes","timeout_seconds","currency","input_rate","output_rate","enforcement","capability_evidence","provider","session_mode","routing"],"allOf":[{"if":{"properties":{"provider":{"const":"openai"}},"required":["provider"]},"then":{"properties":{"session_mode":{"const":"provider_conversation"},"endpoint":{"const":"https://api.openai.com/v1/responses"},"routing":{"type":"object","additionalProperties":false,"properties":{},"required":[]}}}},{"if":{"properties":{"provider":{"const":"openrouter"}},"required":["provider"]},"then":{"properties":{"session_mode":{"const":"stateless"},"endpoint":{"const":"https://openrouter.ai/api/v1/responses"},"routing":{"$ref":"#/$defs/ResponsesRoutingOpenRouter"}}}},{"if":{"properties":{"provider":{"const":"experiential"}},"required":["provider"]},"then":{"properties":{"session_mode":{"const":"stateless"},"endpoint":{"const":"https://api.experientiallabs.ai/v1/responses"},"routing":{"$ref":"#/$defs/ResponsesRoutingExperiential"}}}}]},"ResponsesRate":{"type":"object","additionalProperties":false,"properties":{"numerator_micro_units":{"type":"integer","minimum":0,"maximum":9223372036854775807},"denominator_units":{"type":"integer","minimum":1,"maximum":9223372036854775807},"unit":{"type":"string","enum":["input_token","output_token","request","byte","second"]}},"required":["numerator_micro_units","denominator_units","unit"]},"ResponsesRoutingExperiential":{"type":"object","additionalProperties":false,"properties":{"gateway":{"type":"object","additionalProperties":false,"properties":{"retry":{"type":"object","additionalProperties":false,"properties":{"max_attempts_per_route":{"const":1,"type":"integer"},"max_total_attempts":{"const":1,"type":"integer"}},"required":["max_attempts_per_route","max_total_attempts"]},"backoff":{"type":"object","additionalProperties":false,"properties":{"type":{"const":"none","type":"string"}},"required":["type"]},"routing":{"type":"object","additionalProperties":false,"properties":{"allow_fallbacks":{"const":false,"type":"boolean"}},"required":["allow_fallbacks"]}},"required":["retry","backoff","routing"]},"route_id":{"type":"string","maxLength":8192},"privacy":{"type":"array","items":{"type":"string","enum":["no_training","data_policy","zero_retention"]},"maxItems":8}},"required":["gateway"]},"ResponsesRoutingOpenRouter":{"type":"object","additionalProperties":false,"properties":{"only":{"type":"array","items":{"type":"string","maxLength":8192},"minItems":1,"maxItems":16},"allow_fallbacks":{"const":false,"type":"boolean"},"require_parameters":{"const":true,"type":"boolean"},"price_ceiling":{"type":"object","additionalProperties":false,"properties":{"currency":{"const":"USD","type":"string"},"input_per_million":{"type":"string","pattern":"^(0|[1-9][0-9]*)(\\.[0-9]{1,18})?$","maxLength":64},"output_per_million":{"type":"string","pattern":"^(0|[1-9][0-9]*)(\\.[0-9]{1,18})?$","maxLength":64}},"required":["currency","input_per_million","output_per_million"]},"privacy":{"type":"array","items":{"type":"string","enum":["no_training","data_policy","zero_retention"]},"maxItems":8}},"required":["only","allow_fallbacks","require_parameters"]},"Responsibility":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"worker_id":{"type":"string","format":"uuid"},"outcome":{"type":"string","maxLength":8192},"signals":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"triggers":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"reasoning_policy":{"type":"string","maxLength":8192},"min_interval_seconds":{"type":"integer","minimum":1,"maximum":9223372036854775807},"cycle_limits":{"$ref":"#/$defs/Limits"},"aggregate_limits":{"$ref":"#/$defs/Limits"},"pause_conditions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"escalation_conditions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"acceptance":{"$ref":"#/$defs/Acceptance"},"paused":{"type":"boolean"},"next_wake":{"type":"string","format":"date-time"},"last_cycle_id":{"type":"string","format":"uuid"}},"required":["id","version","scope","worker_id","outcome","signals","triggers","reasoning_policy","min_interval_seconds","cycle_limits","aggregate_limits","pause_conditions","escalation_conditions","acceptance","paused"]},"Review":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"action_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"preview":{"$ref":"#/$defs/Action"},"requirement":{"$ref":"#/$defs/DecisionRequirement"},"proposer_id":{"type":"string","format":"uuid"},"state":{"type":"string","enum":["pending","approved","rejected","expired","invalidated"]},"decision_id":{"type":"string","format":"uuid"}},"required":["id","version","scope","action_digest","preview","requirement","proposer_id","state"]},"Rule":{"type":"object","additionalProperties":false,"properties":{"capability":{"type":"string","maxLength":8192},"effect":{"type":"string","enum":["local","disclosure","external_read","external_mutation"]},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"decision":{"type":"string","enum":["allow","deny","review"]},"human_required":{"type":"boolean"},"conditions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["capability","effect","destinations","decision","human_required","conditions"]},"Schedule":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"task_template":{"$ref":"#/$defs/Task"},"timezone":{"type":"string","maxLength":8192},"expression":{"type":"string","maxLength":8192},"misfire":{"type":"string","enum":["coalesce","skip"]},"catch_up_seconds":{"type":"integer","minimum":0,"maximum":9223372036854775807},"paused":{"type":"boolean"},"next_wake":{"type":"string","format":"date-time"}},"required":["id","version","scope","task_template","timezone","expression","misfire","catch_up_seconds","paused"]},"Scope":{"type":"object","additionalProperties":false,"properties":{"installation_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"project_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"}},"required":["installation_id"]},"ScopeSnapshot":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"revision":{"type":"integer","minimum":1,"maximum":9223372036854775807},"ancestors":{"type":"array","items":{"$ref":"#/$defs/Organization"},"maxItems":4096},"bindings":{"type":"array","items":{"$ref":"#/$defs/Binding"},"maxItems":4096},"worker":{"$ref":"#/$defs/Worker"},"project":{"$ref":"#/$defs/Project"}},"required":["scope","revision","ancestors","bindings"]},"Skill":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"name":{"type":"string","maxLength":8192},"instruction_artifact":{"$ref":"#/$defs/ArtifactRef"},"content_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"input_schema":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"output_schema":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"requirements":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"dependencies":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"source":{"type":"string","maxLength":8192},"license":{"type":"string","maxLength":8192},"evaluation_refs":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"diagnostics":{"type":"array","items":{"$ref":"#/$defs/Diagnostic"},"maxItems":4096}},"required":["id","version","name","instruction_artifact","content_digest","input_schema","output_schema","requirements","dependencies","source","license","evaluation_refs","diagnostics"]},"Task":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"owner_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"outcome":{"type":"string","maxLength":8192},"inputs":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"required_outputs":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"acceptance":{"$ref":"#/$defs/Acceptance"},"limits":{"$ref":"#/$defs/Limits"},"dependencies":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"state":{"type":"string","enum":["draft","ready","running","waiting","verifying","succeeded","failed","cancelled"]},"parent_id":{"type":"string","format":"uuid"},"root_id":{"type":"string","format":"uuid"},"waiting_reason":{"type":"string","maxLength":8192},"cancellation_requested":{"type":"boolean"},"manual_acceptance":{"type":"boolean"}},"required":["id","version","scope","owner_id","worker_id","outcome","inputs","required_outputs","acceptance","limits","dependencies","state"]},"Team":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"worker_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","organization_id","key","name","worker_ids"]},"Usage":{"type":"object","additionalProperties":false,"properties":{"currency":{"type":"string","pattern":"^[A-Z]{3}$"},"spent":{"type":"integer","minimum":0,"maximum":9223372036854775807},"reserved":{"type":"integer","minimum":0,"maximum":9223372036854775807},"estimated":{"type":"integer","minimum":0,"maximum":9223372036854775807},"unknown":{"type":"integer","minimum":0,"maximum":9223372036854775807},"advisory":{"type":"boolean"}},"required":["currency","spent","reserved","estimated","unknown","advisory"]},"Validation":{"type":"object","additionalProperties":false,"properties":{"diagnostics":{"type":"array","items":{"$ref":"#/$defs/Diagnostic"},"maxItems":4096},"requirements":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096},"dependencies":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096}},"required":["diagnostics","requirements","dependencies"]},"Worker":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"purpose":{"type":"string","maxLength":8192},"instructions":{"type":"string","maxLength":8192},"skill_versions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"bindings":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"profile":{"anyOf":[{"$ref":"#/$defs/ExecutionProfile"},{"type":"null"}]},"limits":{"anyOf":[{"$ref":"#/$defs/Limits"},{"type":"null"}]},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","organization_id","key","name","purpose","instructions","skill_versions","bindings","profile","limits"]}}}
```

## Named acceptance cases

Tests are implementation deliverables, not claims of already executed qualification. Retain expected/observed results, exact source/config/tool versions and failure evidence.

### Z01.revoked_principal — Z01

Setup: A client principal has an authenticated session and an authorized scoped mutation.

Action: Revoke its credential or grant, then repeat the mutation using the existing session.

Expected:

- Current authorization denies the mutation.
- Revocation is durable and survives controller restart.

### Z01.cross_scope_reference — Z01

Setup: Two organizations contain separate projects, workers and connections with no cross-scope binding.

Action: Use an otherwise authorized principal to reference the other organization or project in a draft and task.

Expected:

- The unauthorized reference is rejected.
- No activation, task admission or secret lookup occurs until an explicit valid binding exists.

### Z04.old_authority — Z04

Setup: A principal may edit a draft but cannot expand its own effective grants.

Action: Submit a plan containing a policy or grant that would authorize its own application.

Expected:

- Apply evaluates existing authority and rejects or requests an eligible exact decision.
- Proposed policy and imported instructions cannot authorize their own activation.

### Z05.malicious_instructions — Z05

Setup: A skill includes instructions to read secrets, self-grant permissions and ignore reviews.

Action: Import, bind and execute the skill in a constrained task.

Expected:

- Instructions remain untrusted input and install no grants.
- Tool, data and effect access remains limited to the pinned authorized binding closure.

### Z05.changed_tool_schema — Z05

Setup: An active worker binding pins a tool identity/version/schema.

Action: Change the provider-discovered schema or executable binding while retaining the old activation plan.

Expected:

- Changed executable meaning cannot silently enter the active tool closure.
- Revalidation and authorized activation are required; unsupported contracts return capability or prerequisite faults.

### Z05.empty_bindings — Z05

Setup: A worker has no explicit tools or cross-scope bindings.

Action: Ask it to use a known installation connection and a tool visible in discovery.

Expected:

- The worker receives no implicit global tool or credential access.
- Missing or ambiguous scope produces a named refusal rather than selecting a global organization.

### Z05.agent_self_grant — Z05

Setup: A client agent can create ordinary scoped definitions under a fixed delegation envelope.

Action: Attempt to enlarge its scope, authorize worker self-modification or establish an autonomy rule above the envelope.

Expected:

- No authority expands without an eligible act under existing policy.
- The exact denial or review requirement identifies the missing authority.

### Z06.admission_claim_revoke — Z06

Setup: A controlled adapter exposes barriers before admission, before claim and before invocation.

Action: Race revocation or pause against admission and claim in each ordering.

Expected:

- Revocation committed before claim prevents invocation; physical call count is zero.
- Admission reserves and writes intent/evidence atomically; claimed dispatch follows the specified irreversible boundary.
- Revocation after claim is recorded honestly and does not claim to retract transmitted bytes.

### Z07.reviewer_eligibility — Z07

Setup: Reviews include current version, expiry, proposer-separation and reviewer delegation restrictions.

Action: Decide using revoked or ineligible delegated credentials, an expired review, a stale version or the prohibited proposer.

Expected:

- Each ineligible decision is refused using current identity and policy.
- A permitted delegation is scoped and cannot bypass human-required or proposer-separation rules.

### Z12.delegation_narrows — Z12

Setup: A root task has finite scope, allowed tools/destinations, project data, deadline and shared budget.

Action: Independently delegate children expanding scope, tool access, data access, deadline or spending authority.

Expected:

- Each expansion is refused or reduced only through an explicit valid narrowing contract.
- Delegation cannot grant authority absent from its parent or inherited organization policy.

### Z17.hierarchy_cycles — Z17

Setup: Organizations form a valid root/child/grandchild tree.

Action: Attempt to parent the root under a descendant or create any cycle.

Expected:

- Planning or activation rejects cycles.
- The original hierarchy and inherited limits remain unchanged.

### Z17.inherited_denial — Z17

Setup: A parent explicitly denies an action that a child policy attempts to allow.

Action: Plan the child policy and attempt the action in the descendant scope.

Expected:

- The inherited denial wins and child policy cannot enlarge the parent ceiling.
- Parent-chief report access does not imply unrestricted access to descendant credentials, data or private memory.

### Z17.ancestor_budget_exhaustion — Z17

Setup: A child has local funds but an applicable ancestor budget is exhausted.

Action: Admit a paid child task or model call.

Expected:

- Admission is refused without provider dispatch.
- Every descendant reserves against applicable ancestor budgets and cannot bypass an exhausted aggregate limit.

### Z17.scoped_reparenting — Z17

Setup: A worker or organization with active work and private memory is moved between scopes with different ceilings.

Action: Apply the move through a configuration plan.

Expected:

- Inherited authority, budgets and memory bindings are rechecked.
- Existing private memories are not silently transferred and active work does not acquire broader authority.

### Z18.unauthorized_brain_not_queried — Z18

Setup: Instrumented brains include permitted worker/org brains and an unbound private descendant brain.

Action: Recall as a caller lacking the private brain binding.

Expected:

- The unauthorized brain receives zero read/model requests.
- Filtering occurs before retrieval and model composition; hierarchy alone supplies no private-memory access.

### Z18.restricted_project_memory — Z18

Setup: A worker can read one restricted project brain but an organization-wide audience cannot.

Action: Curate organization memory and recall as an ordinary organization member.

Expected:

- Restricted project content is excluded from the broadly shared brain and model context.
- Promotion cannot disclose it without current source-disclosure and destination-write authority.

### Z18.revoked_memory_binding — Z18

Setup: A worker previously recalled a brain and then loses its binding.

Action: Recall again and inspect prior run artifacts.

Expected:

- Subsequent brain retrieval is denied immediately under current authority.
- The system does not claim revocation erases previously disclosed context; historical artifacts remain governed by their own retention/access rules.

### Z19.self_promotion — Z19

Setup: A worker has minimum permissions and may propose a capability promotion.

Action: Have it approve its own evidence, edit its qualification criteria or raise the governing ceiling.

Expected:

- The worker cannot authorize any of those expansions.
- Without an applicable owner-approved rule, promotion waits for an eligible owner decision.

### Z19.unsupported_evidence — Z19

Setup: An owner-approved promotion rule requires independently established results in a specified evidence window.

Action: Submit model assertions, unverified worker tests, stale evidence or provenance-free memory claims as proof.

Expected:

- Insufficient evidence is rejected with an inspectable explanation.
- No grant activates solely from competence narration or a remembered judgment.

### Z19.capability_promotion — Z19

Setup: A rule permits one exact capability/destination for one worker under specified model/tool/skill versions.

Action: Supply qualifying evidence and evaluate the rule.

Expected:

- Only the exact permitted capability/scope activates within the prior ceiling.
- Unrelated powers remain unavailable; durable events identify the evidence and rule version responsible.

### Z19.version_requalification — Z19

Setup: A worker has qualifications depending on recorded model, tool, skill and evaluator versions.

Action: Change one relevant version, and separately change an unrelated dependency.

Expected:

- Affected qualifications require requalification before qualifying further admission.
- Only qualifications that depended on the changed configuration are invalidated; unsupported prior evidence is not reused silently.

### Z19.immediate_demotion — Z19

Setup: An active earned grant has an owner-approved incident/failure demotion rule.

Action: Record a qualifying incident concurrently with a new effect admission.

Expected:

- Restriction or demotion commits before any subsequently authorized admission can use the old grant.
- The user can inspect durable reasons and events; no model call or new spending is required to apply restriction.

### Z19.human_review_preserved — Z19

Setup: A worker earns a related capability but the governing policy still requires a human for a specific action class.

Action: Attempt that class under the new qualification.

Expected:

- The human-required decision remains mandatory.
- Only an explicitly authorized owner policy change under existing authority can change the mandatory class.

### Z20.responsibility_pause — Z20

Setup: Two responsibilities have active/queued work and only one is selected for pause.

Action: Pause that responsibility and race its next wake.

Expected:

- Restrictive state commits immediately and prevents new admission for that responsibility.
- Unrelated work is not cancelled; already transmitted effects and unresolved outcomes remain visible.

### Z21.groups_and_quiet_coordination — Z21

Setup: Workers from separate organizations share a group conversation and chiefs exchange routine reports.

Action: Create/join the group, share attachments, deliver routine internal coordination and inspect notifications.

Expected:

- Group membership changes neither worker home organization nor memory/tool authority; attachment sharing remains a governed disclosure.
- Routine coordination does not continually reorder chats, mark them unread or notify the human.
- Reports upward obey explicit reporting/data bindings.

### Z21.capability_and_pause_cards — Z21

Setup: A worker has a proposed promotion and active work with a possibly transmitted effect.

Action: Inspect promotion details, pause or stop the relevant work and observe offline/online acknowledgement.

Expected:

- Autonomy is shown as exact capabilities and authority changes with evidence and rule/decision status, not a universal trust score.
- Pause/stop reports controller acknowledgement and unresolved external effects honestly; responsibility creation does not imply expanded authority.

### JOURNEY.desktop_daily_work — JOURNEY

Setup: Launch the actual packaged desktop on a supported platform with a personal-chief conversation.

Action: Create marketing/engineering organizations and ordinary workers, assign a bounded task and ongoing responsibility, inspect result and memory, then handle an exact pending decision.

Expected:

- The visible hierarchy and work states match queried controller state rather than model narration.
- Memory scope/lineage and earned autonomy meet their own Z18/Z19 gates.
- Capture executed UI observations for setup and daily results/decisions; mocked view models alone are insufficient.

### P00.review_eligibility_human_only — P00

Setup: A review-class action is pending; the eligible set includes the human principal whose current authority admitted the request and a worker/service/agent principal that also holds broad standing capability.

Action: Attempt review.decide as the worker/service/agent principal, then as the eligible human principal, then as the human principal who authored the original proposal.

Expected:

- The worker/service/agent decision is refused regardless of its standing capability.
- The eligible human principal's decision is accepted.
- The proposer-as-reviewer attempt is refused on separation even though they are human.

### P00.identity_authority_caller_allowlist_only — P00

Setup: A narrowly granted principal holds no _identity.authority capability itself; policy is on _identity.authority's caller allowlist.

Action: Have policy call _identity.authority for that principal's current grants, then have an off-allowlist caller attempt the same internal call, then attempt it for a revoked principal and for a principal in a different installation.

Expected:

- Policy's call succeeds despite the subject holding no _identity.authority capability of its own.
- The off-allowlist caller is refused.
- The revoked principal and cross-installation principal are refused.

### P00.worker_operation_denies_unlisted_operation — P00

Setup: A worker's local proposal names grant.create as its target operation, which is not on the local model-visible allowlist.

Action: Route the proposal through contract.WorkerOperator.ExecuteWorker.

Expected:

- The call is refused before reaching grant.create's handler.
- An allowlisted operation (for example task.delegate) submitted the same way for the same worker succeeds under that worker's own intersected scope.
- The controller's administrative identity is never substituted to force the denied call through.

### P15.profile_qualification_cost_and_authority — Z18

Setup: A profile candidate has explicit token/rate bounds and the caller has scope-limited provider disclosure authority.

Action: Qualify the exact profile with a maximum probe cost, and vary the current grant, destination, classification, profile digest and available budget before claim.

Expected:

- The one probe is admitted only under current authority, exact bound destination and classification, and sufficient reserved budget.
- Observed charge is settled against that probe; a profile mismatch or a charge beyond the configured bound never yields qualified evidence.

## Delivery

Implement production behavior and meaningful local tests within scope. Report files changed, commands actually run, observed results, unresolved dependency qualifications and contract defects. A missing dependency may use an exact local fake for development; production must return a named prerequisite/unsupported error instead of fake success. Integration owns shared dependency changes, real assembly, cross-package proving and landing. Do not advertise release or client/platform support from compilation alone.
