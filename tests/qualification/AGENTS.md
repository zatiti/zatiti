# Implementation assignment: `tests/qualification`

Generated specification revision 17; source digest `f77034332a96396a9f88395f71ff528f051f96f35b398629236dd00bc731c08f`. This file is committed implementation context. Do not independently edit it. Everything required from the product specification and adjacent interfaces is embedded below; no RFC copy is required.

## Mission and scope

Own executable external adapter, real Flutter desktop/platform/client qualification and release evidence.

Write scope: **`tests/qualification/` only**, excluding this generated AGENTS.md. Go package name: `qualification_test`. Ownership kind: verification; integration wave: 4.

Allowed production imports from this repository: `github.com/zatiti/zatiti/internal/contract`, `github.com/zatiti/zatiti/internal/client`, `github.com/zatiti/zatiti/internal/adapters/responses`, `github.com/zatiti/zatiti/internal/adapters/github`, `github.com/zatiti/zatiti/internal/adapters/httpread`, `github.com/zatiti/zatiti/internal/adapters/serenity`. Tests may use interfaces/fakes defined locally and, once available, storage-backed temporary fixtures; integration owns cross-package tests. No sibling raw SQL. No root dependency edits except the integration exception stated in its own brief.

## Implementation decisions and acceptance focus

Implement package qualification_test and controlled subprocess/GUI drivers in this root. Qualifications bind exact source/config/dependency/tool/profile/client/platform versions and expected/observed evidence. The desktop is the Flutter application at apps/desktop, not an importable Go package: qualify it by building the real application from a recorded source revision with the pinned Flutter toolchain and driving that built application (Flutter integration_test and/or OS accessibility automation launched as a controlled subprocess from this root) against an actual controller over the real Unix socket, plus the explicit mutual-TLS remote path. Never substitute a Go stand-in, a widget test, the design study's browser rendering or a WebView for the built client. Run first-release desktop onboarding/daily result/decision paths; verify UI against committed controller state read back through the Go client, not narration or screenshots alone. Record Flutter/Dart/plugin versions, build mode and platform with each result. A missing Flutter toolchain or display prerequisite reports not-run and blocks desktop release claims. Qualify the first Mac release on clean native arm64 and amd64 hosts (controller always-on, desktop closing, Serenity one writer/brain/key setup, secure helper, backup restore, first real chief reply); qualify Linux later before advertising Linux support. Test actual named agent-client versions before advertising compatibility; MCP baseline with optional features disabled. Adapters qualify public APIs with explicit test accounts/operator-controlled safe destinations; scripts must not embed credentials or silently perform irreversible real publication. Unit fakes cannot stand in for real protocol capabilities. Missing credentials/platform prerequisites report not-run and block relevant release claims. Additional remote MCP, contained executor, Windows and providers are outside v1 scope. Revision 9: clean native Intel and Apple Silicon hosts must prove per-user pkg target, actual installed payload and active-tree binding, Gatekeeper/Developer ID/notarization, signed helper caller restriction, Keychain ACL across upgrade, crash/rerun repairs and first real provider reply with all required Serenity guarantees. Record blocked/not-run for any missing external input; no reduced preview. Revision 10: one real native clean-host run per architecture must link the exact signed bootstrap, delivery and architecture-specific package/component/helper digests through install, active-tree audit and first real provider/Serenity reply in QUALIFICATION.macos_install_to_first_chat. Populate the mac_release evidence object from verified artifacts and authoritative readbacks only, with unique host-run identity and no secrets. Revision 11: Mac release is blocked until a real browser sign-in selects and verifies the pre-existing hosted personal brain and the full Serenity semantic/backup gates pass with the genuine provider reply on both native architectures. An HTTP 200, OAuth login, empty recall or synthetic fixture is not first-chat proof.

Local proving focus: Real versioned platform/client/GUI/provider evidence and Z01-Z21 release report; never compile-only compatibility. Rev15: controlled live provider run proves one qualification probe becomes exact profile evidence and a subsequent real first chat uses that same profile/connection/model/route under enforced cost limits; timeout/unknown and mismatched-profile cases remain unqualified.

## Incoming and outgoing boundaries

Incoming callers: entrypoint/assembly or tests via the explicit Go API.

Outgoing owner calls: none; use only declared Go dependency interfaces. Each exact input/output schema appears below. Calls retain current Unit/authority; owner allowlists are mandatory.

### Imported package APIs and behavior

These briefs are embedded so you need not read a sibling prompt to discover its incoming API. Implement only your own package.

**`internal/client`** — Own shared controller client, secure credential plumbing, reconnect and explicit submission-key retry.

Expose Config{SocketPath string; RemoteURL string; TLSConfig *tls.Config; Timeout time.Duration}; New(Config,contract.CredentialSource) (*Client,error); (*Client).Call(context.Context,string,contract.Request) (contract.Result,error), implementing contract.Operator. Local Unix HTTP transport; explicit remote TLS only where configured. Go callers (CLI, MCP, qualification drivers) use this package; the Flutter desktop at apps/desktop cannot import it and reimplements these exact semantics in Dart against the same wire contract. CredentialSource provides header bytes securely per request, never JSON/tool arguments. Strict envelope validation and bounded body reads. Never replace a mutation key after timeout. Auto retries only safe transport connection establishment before any request bytes, or explicit identical submission with same key; unknown ack resolved command.get. Cursor expiry surfaces snapshot_required so caller refreshes snapshot before replay; never fill gap silently. No server file opens, scheduler, model defaults or hidden paid fallback. Cancellation of local wait is not cancellation of accepted command.

**`internal/adapters/responses`** — Qualified hosted Responses model adapter.

Adapter name responses; profile schema zatiti.responses/v1 has endpoint, model, connection_id, input/output token bounds, max_response_bytes, timeout_seconds, currency, input/output rational rates, capability evidence artifact. Endpoint/provider/model are installation inputs, no default account/model/price. Pin documented Responses wire fields during qualification and preserve full model-visible request artifact. Input Action.parameters includes context_artifact, max_output_tokens, tool_contract_versions; output Observation.evidence includes response_id, output artifact refs, typed tool proposals and finish reason. Never execute model tool proposals in adapter. Enforce explicit disclosure destination/classification and bounded token charges; refuse hard cap if selected API cannot bound required spend. One physical HTTP invocation per Invoke, no mutation retry. Timeout after bytes sent => unknown; provider response acceptance not invented success. Sanitize provider bodies/errors; usage exact observed tokens/prices or unknown. Reconciliation only documented authoritative lookup under qualified retention, otherwise preserve unknown. No hidden provider/model fallback. Revision 3 freezes ResponsesParameters/ResponsesEvidence as a kind-discriminated oneOf: prepare_session creates the provider conversation and returns session_handle with no model-visible content and no context_artifact; model_step names that persisted session_handle and sends context_artifact/max_output_tokens/tool_contract_versions. Each Invoke performs exactly one of the two, never both; a prepare_session whose response is lost stays unknown and is never retried into a second session, and a model_step timeout after bytes were sent is outcome_unknown with no automatic fresh call. Revision 15: support the fixed qualification_probe action only for an effects operation carrying the controller-persisted qualification-job route and a strict evidence-free ResponsesProfileDraft. The probe has a fixed non-sensitive prompt, bounded input/output/response bytes and one physical request; it cannot invoke tools, continue, follow redirects or retry. Emit only observed protocol/model/usage evidence; do not assert capabilities not exercised or documented. Ordinary prepare_session/model_step still require a complete evidence-bound profile.

**`internal/adapters/github`** — Qualified GitHub repository artifact/publication adapter.

Adapter name github; profile schema zatiti.github/v1 has api_base, allowed_repositories, allowed_actions, max_response_bytes, timeout_seconds, idempotency_profile and automation_constraints. Action.parameters discriminated kind read_repository/create_branch/push_commit/open_pull_request/merge_pull_request; exact repository owner/name, branch/head SHA, base SHA, patch/content artifact hashes, PR title/body artifact and relevant workflow/automation bounds. Restrict v1 publication to qualified forms, unsupported action named capability_unsupported. Review binds exact repo/head/content/timing/account and downstream automation effects; opening PR is consequential. Fetch/check current preconditions immediately before governed call where contract supports, but no unrecorded additional HTTP calls: preflight is its own admitted read. No automatic hidden write retry. Provider accepted/confirmed/unknown distinguished; GitHub eventual not-found cannot prove nonexecution. Repository outputs are governed immutable artifacts, never unbounded controller working-directory shell execution. HTTP redirects cannot widen host/account scope.

**`internal/adapters/httpread`** — Qualified bounded public HTTP reads for research.

Adapter name httpread; profile schema zatiti.httpread/v1 has allowed_origins, max_bytes, timeout_seconds, max_redirects, allowed_media_types. Action.parameters url, method fixed GET, permitted headers excluding raw secret input, expected_media_type. Current authorization/classification/disclosure and cost bound apply even to reads. Enforce response size/time bounds before consuming unlimited bytes, restrict redirect origins and resolve destination at each hop. Refuse local/private/link-local/metadata addresses unless separately explicit installation-authorized destination binding; prevent DNS rebinding by binding validated dial addresses. Each actual physical request/redirect is an accounted effect attempt; simplest v1 qualified profile sets max_redirects=0 and returns prerequisite for an explicit redirected read. Record requested/resolved URL, status, bounded content digest/artifact and freshness for cited brief. No arbitrary shell/browser/script execution.

**`internal/adapters/serenity`** — Qualified public Serenity protocol/read-facade adapter and writer capability report.

Adapter name serenity; profile schema zatiti.serenity/v1 has exact version/commit, public endpoint/brain root mapping, writer ownership, supported operations, cost/disclosure enforcement profile, command status lookup semantics, freshness/index capability, timeout_seconds, max_bytes and backup revision protocol. During foundation integration inspect public upstream API and pin exact source/protocol; never invent a public method from name or import internal packages. Adapter action kind recall/remember/inspect/promote/retract/export_revision, brain_id and stable adapter_command_id plus bounded payload. Brain selected by memory owner authorization, adapter cannot widen query. Canonical writes only one writer per brain; exported Go facade only for compatible reads. Recall may call models and incur charges, so required bound/disclosure enforcement must be qualified before enabling. If actual API cannot provide safe command lookup after lost ack, retain unknown and require explicit reconciliation; do not claim idempotency. Preserve provenance/source/version/freshness in observations. Backup pins real brain revisions, handles Git/history and keys, restore paused pending writer/promotion obligations. Report unsupported guarantees honestly and gate release until required first-release modes qualified. Revision 11: hosted Streamable HTTP MCP is the Mac primary. Map local UUID brains to verified hosted alphanumeric project IDs via the connection grant, never parse one as the other. Account the MCP handshake separately. Require public authenticated binding identity, authoritative command status, full claim/lineage/freshness and immutable revision/export evidence, enforced per-call cost/disclosure and verifiable build capabilities before advertising a supported operation. A hosted quota alone is not a hard cost bound; keep unsupported actions fail-closed.

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

### R2.3-002 (source section 2.3; primary owner integration)

1. An operator initializes an installation and configures scoped access for an agent.
2. That agent creates an organization, imports a skill, binds a connection, creates workers and a project, validates the resulting configuration, and activates it under current policy.
3. The agent assigns a task with pinned inputs, a resource envelope, and an acceptance contract; it follows runs, artifacts, reviews, costs, and results.
4. A worker delegates bounded child tasks, waits durably, and resumes after controller restart without duplicating protected effects.
5. A human or appropriately authorized principal reviews exact actions; a separate client can inspect or continue the workflow.
6. The installation can pause, export, back up, restore, and reconcile unresolved work through the same operation model.

### R2.3-003 (source section 2.3; primary owner integration)

The first release includes the chat-first desktop application, CLI and local stdio MCP, a complete shared operation catalog, hierarchical organization authoring, personal and organization chiefs, Serenity-backed memory, versioned skills, connection setup, durable execution, scoped policy, earned autonomy, approvals, evidence, and recovery. It includes one qualified hosted model-provider adapter, the cooperative external-worker protocol, bounded HTTP reads, repository artifact workflows, and one qualified GitHub adapter for explicitly authorized repository actions. Provider/model selections are installation inputs, not hardcoded personal accounts or model aliases.

### R2.3-004 (source section 2.3; primary owner integration)

Distribution is phased: the first release targets macOS on both native Apple Silicon (arm64) and Intel (amd64). Both architectures must pass installed-artifact qualification before the Mac release is claimed. Linux follows after its own qualification; Windows is later. No advertised platform or agent-client compatibility is established solely by code compiling.

### R2.4-002 (source section 2.4; primary owner controller)

V1 does not include a standalone web client, a mobile client, hosted multitenancy, remote MCP over HTTP, high availability, multiple active controllers, automatic controller migration, cloud fleet provisioning, a marketplace, or a universal guarantee of exactly-once effects. Social publishing, external chat ingestion, arbitrary third-party server execution, and additional provider adapters can follow the same contracts later; they do not gate the first release. The desktop framework remains an implementation decision; a browser-based renderer inside the desktop application does not imply a separately supported web product.

### R2.4-003 (source section 2.4; primary owner controller)

An external worker is advisory in v1. A contained runner is a separately qualified extension, not an implied property of MCP, a subprocess, or a container. Zatiti does not require a particular coding planner or fleet scheduler.

### R8.3-002 (source section 8.3; primary owner mcp)

V1 uses stdio with the generated Mint adapter (or an equivalent pinned adapter) and a pinned supported protocol revision. `zatiti mcp serve` connects to the configured local controller as the selected scoped principal. MCP stdout carries protocol frames only; diagnostic logging goes to stderr. Reconnection does not create another controller or restart tasks. A missing controller returns a named availability error; process-start convenience must not hide a second scheduler.

### R8.3-003 (source section 8.3; primary owner mcp)

The server advertises typed tools with input and output schemas. It returns the common result envelope as `structuredContent`, with equivalent serialized JSON in text content for clients that need it. Domain failures use a tool result with `isError: true`; malformed protocol messages use protocol errors. Zatiti specifies this mapping against the [MCP tools contract](https://modelcontextprotocol.io/specification/2025-11-25/server/tools).

### R8.3-004 (source section 8.3; primary owner mcp)

Do not require optional client features such as sampling, elicitation, resources, prompts, or protocol-level task extensions for baseline product access. Long work uses Zatiti task/command IDs and polling. Local stdio is the first-release transport; remote HTTP requires a separate authentication and exposure design before support. See the [MCP transport specification](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports) and [official Go SDK](https://github.com/modelcontextprotocol/go-sdk).

### R12-002 (source section 12; primary owner integration)

These gates must produce named executed cases, source/config/tool versions, expected and observed results, and retained failure evidence. Planned tests, skipped suites and a passing process exit are not release evidence.

### R12-003 (source section 12; primary owner integration)

| ID | Required property | Required proving cases |
|---|---|---|
| Z01 | One installation owner and scoped identities | Duplicate controller, revoked principal, cross-organization/project reference, unauthorized credential access |
| Z02 | CLI/MCP feature parity | Every registry operation exposed in both; equivalent results, state, errors, pagination and authorization on isolated matching fixtures |
| Z03 | Complete agent administration | Agent creates organization/skills/workers/connections, resolves prerequisites, plans/applies and starts work through MCP; identical CLI journey; no dashboard or source-code knowledge required |
| Z04 | One compiler and exact atomic activation | Concurrent apply, stale dependency, explicit removal, authority expansion under old policy, repeated submission key |
| Z05 | Instructions and discovery grant no authority | Malicious skill text, archive escape, changed tool schema, callback request, empty bindings and agent self-grant |
| Z06 | Current authorization for every physical effect | Admission/claim/revoke/crash races; exact adapter call counts and no hidden mutation retries |
| Z07 | Exact eligible review | Content/destination/time/head changes; agent posing as human; revoked/delegated reviewer; CLI cannot bypass MCP denial |
| Z08 | Unknown outcomes remain honest | Lost response after provider success, failed retry after unknown, expired provider key, delayed confirmation and cancellation |
| Z09 | Atomic bounded accounting | Concurrent children and model calls, unknown external costs, missing price, reservation settlement and recovery |
| Z10 | Durable task and execution identity | Duplicate schedules/claims, restart, stale heartbeat, lost claim acknowledgement, cancelled wake and conflicting replacement |
| Z11 | Independently established completion | Exit zero with missing output, tampered verifier, worker assertion, failed child acceptance, manual acceptance distinctly labeled |
| Z12 | Narrowing delegation | Child scope/tool/deadline/budget expansion refused; root limits remain shared |
| Z13 | Credentials and execution claims are honest | No secret in CLI/MCP/log/export; advisory worker limitation visible; no client compatibility inferred as containment |
| Z14 | Safe evidence and restore | Atomic state/event failure, missing artifact, paused clean restore, unresolved outbox and revocation preserved |
| Z15 | Portable definitions without live authority leakage | Canonical export/import round trip, stable IDs, cross-installation rebinding, no secret or runtime history in organization export |
| Z16 | Transport-independent recovery | Start through CLI, continue through MCP and reverse; disconnect after mutation, command lookup, cursor expiry, interrupted upload and maintenance restore |
| Z17 | Hierarchical organization ownership | Atomic organization/chief creation, cycle rejection, chief replacement, inherited denial and ancestor budget exhaustion, scoped reparenting |
| Z18 | Scoped memory and traceable curation | Unauthorized brain never queried, restricted project data excluded, chief promotion with provenance, correction across promoted copies, stale index reported, recalled context retained |
| Z19 | Earned autonomy within prior authority | Self-promotion refused, unsupported evidence rejected, capability-specific promotion, version-triggered requalification, immediate demotion, human-review class preserved |
| Z20 | Durable ongoing responsibility | Scheduled and reasoning-driven cycles, bounded idle spend, restart at wake, mailbox redelivery, duplicate assignment conflict, responsibility pause |
| Z21 | Desktop personal-chief journey | First conversation, create two child chiefs, delegate work, inspect organization breadcrumb, handle exact approval, reconnect without duplicate effects, close client while controller continues |

### R12-004 (source section 12; primary owner integration)

Parity compares fixtures with the same principal and state, normalizing only generated IDs/timestamps with an explicit mapping. It must not normalize away permissions, outcome uncertainty, resource versions or business events. Golden files alone cannot prove lifecycle equivalence; exercise the handlers through real CLI subprocesses and an MCP client against controlled controllers.

### R12-005 (source section 12; primary owner integration)

Three release journeys cover the product:

### R12-006 (source section 12; primary owner integration)

1. **Organization setup:** discover capabilities, create an organization and two workers, import/evaluate a skill, establish a provider connection without exposing credentials, activate a plan, and produce a first verified artifact.
2. **Engineering:** a cooperative worker claims a repository task, checkpoints, submits a patch and independent check evidence, and proposes an exact repository publication action under configured policy. Failed checks block completion; a stale repository head invalidates applicable review.
3. **Research and drafting:** a hosted worker gathers bounded public sources and produces a cited brief and draft artifact. This proves generality without requiring a social publishing integration. Scope and disclosure controls apply to reads and model calls.

### R12-007 (source section 12; primary owner integration)

Run each journey entirely through CLI and entirely through MCP, plus interruption/cross-interface cases. Test actual client sessions before naming a supported Claude Code, Codex or Cursor version in release documentation. Also test baseline MCP behavior with optional resources, prompts, sampling, elicitation and task extensions unavailable.

### R12-008 (source section 12; primary owner integration)

Run the organization setup and daily results/decisions journeys through the real desktop client as well. Begin with the personal chief, create marketing and engineering child organizations, add ordinary workers, assign both a bounded task and an ongoing responsibility, inspect a result, and handle an exact pending decision. Verify the displayed hierarchy and work state against the controller; a chat message claiming creation or completion is insufficient. Memory and earned-autonomy qualification are first-release gates, not implied by Serenity's or another harness's test results.

### R13-002 (source section 13; primary owner integration)

| Stage | Deliverable | Exit gate |
|---|---|---|
| 1 | Go binary, SQLite/controller ownership, bootstrap, identity, operation registry, CLI and MCP adapters | One real operation round-trips through both; bootstrap and denial parity; clean restart |
| 2 | Hierarchical organizations/chiefs/projects/workers, skills, connections, compiler/revisions, first desktop conversation | Personal-chief setup, child creation, exact plans, secret-free provisioning, concurrent apply and portability |
| 3 | Tasks/responsibilities, hosted/cooperative execution, schedules and reasoning cycles, mailboxes, reviews, effects, accounting and Serenity bindings | Governed work and scoped recall survive failure; verified completion and physical-call fault tests |
| 4 | Earned autonomy, desktop daily workflows, chief memory curation, artifact/evidence/recovery/backup completion and qualified adapters | CLI/MCP and desktop journeys, Z01-Z21, restore, packaging and named client compatibility |

### R13-003 (source section 13; primary owner integration)

CLI and MCP ship together at every stage; MCP is not a later wrapper around a finished CLI. Desktop workflows use the same operations and authorization, while presenting only what the human needs for the task. Do not advertise the first release until the full first-release gates pass. Build implementation plans from the current frontier, using actual schemas and capability evidence. Additional remote transports, contained runners, channels and clients require separate RFC amendments and qualification.

### R16-002 (source section 16; primary owner desktop)

The desktop is the human's daily workspace. CLI and MCP are primarily interfaces for coding agents; no human journey requires a terminal, operation identifier, or schema knowledge. The visual and interaction direction is a simple conversation list and selected conversation, informed by the supplied GrokBot screenshots and Rakazo's chat-first workflows. Organization structure becomes visible when useful without requiring an organization chart or dashboard at entry.

### R16-003 (source section 16; primary owner desktop)

The two primary journeys are giving a chief or worker a task/responsibility, and returning to inspect results and handle decisions. First launch, after necessary connection/bootstrap prerequisites, opens the personal-chief conversation with a ready composer. A returning launch restores the current conversation. The personal chief is pinned and is the default place to coordinate multiple organizations; users may contact any worker directly.

### R16-004 (source section 16; primary owner desktop)

The user can say "Create a marketing chief and an engineering chief and have them build their teams." Creating a chief for a new responsibility stages a child organization and its designated worker together, applies permitted changes through the compiler, and shows the resulting organization card from committed state. The card distinguishes proposed, awaiting decision, and created states. A chief can add ordinary workers without creating more organizations. A New menu also offers New worker, New organization, and Group chat; organization creation asks for a name and an optional parent defaulted from the current context. Conversational and direct controls operate the same objects.

### R16-005 (source section 16; primary owner desktop)

The sidebar defaults to All chats. A small organization selector reveals an expandable hierarchy; selecting an organization filters conversations to it and its descendants without navigating away to an administrative home. A muted organization label beside a worker's name gives local context, and the conversation header shows a clickable full path such as `Studio > Engineering > Quality`. Ambiguous search results include enough ancestry to distinguish workers. Membership is never conveyed by color alone. Worker identities persist across tasks and conversations.

### R16-006 (source section 16; primary owner desktop)

An organization owns responsibilities, memory, budgets, workers, and reporting relationships. A group chat is a conversation among selected participants. Creating or joining a group chat does not change home organization, grant new memory access, or expand tool authority. Messages and attachments shared with participants remain governed disclosures. Direct user requests and chief assignments refer to the same durable tasks, with conflicting updates surfaced rather than independently scheduled twice.

### R16-007 (source section 16; primary owner desktop)

Conversation carries requests, answers, meaningful updates, exact approval cards, files, and results. One optional details panel exposes current responsibilities, active work, pending decisions, files, and memory with sources and sharing scope. Durable work remains discoverable outside the message scroll. An action card renders the actual controller disposition and links to evidence; model narration never serves as its state authority.

### R16-008 (source section 16; primary owner desktop)

Chiefs summarize useful outcomes, exceptions, and decisions upward within reporting bindings. Routine internal coordination and repeated reasoning do not continually reorder human chats, mark them unread, or generate notifications. A Needs you filter gathers unresolved human decisions across organizations and links directly to their exact action cards. Users can inspect detailed activity on demand. Closing the desktop leaves authorized server work running, with that behavior made clear during setup; offline cached views explicitly distinguish unsent input and stale state.

### R16-009 (source section 16; primary owner desktop)

Autonomy is expressed as concrete capabilities such as "Can publish documentation updates" and "Asks before spending," not a universal trust score. Promotion cards show the exact authority change, relevant evidence, and whether an existing owner-approved rule activated it or a decision is still needed. Responsibility creation and authority expansion remain distinct: a newly created chief starts with minimum permissions under its parent's ceiling. Pause and stop controls are directly available and report controller acknowledgment and unresolved external effects honestly.

### R16-010 (source section 16; primary owner desktop)

The desktop framework, final visual tokens, and detailed interaction designs remain to be selected. The default personal-chief onboarding, chat-first navigation, optional hierarchy, subtle organization identity, and progressive access to durable work are product requirements.

### R17-002 (source section 17; primary owner integration)

- [MCP tools](https://modelcontextprotocol.io/specification/2025-11-25/server/tools) and [transports](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports): protocol references for the proposed adapter, not proof of Zatiti conformance.
- [Mint](https://github.com/sirerun/mint): proposed build-time OpenAPI-to-MCP generator; pin its version/commit and qualify generated output before release.
- [Official MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk): protocol implementation used by the generated adapter or a fallback adapter; pin and test a release.
- [Agent Skills specification](https://agentskills.io/specification): proposed instructional-package import format; Zatiti permissions remain separate.
- [SQLite WAL](https://sqlite.org/wal.html) and [online backup API](https://sqlite.org/backup.html): storage implementation references.
- [Apache License 2.0](https://www.apache.org/licenses/LICENSE-2.0): distribution license.
- [Serenity](https://github.com/sirerun/serenity): proposed scoped memory integration; pin and qualify its public read and write interfaces.
- [Rakazo](https://github.com/elie222/rakazo): reference for persistent teammates, chat-first interaction, provider boundaries, and execution recovery; not evidence of Zatiti conformance.
- [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness): reference for capability-declared executors, durable agent messaging, reconstructable model context, and pre-effect checkpoints; not a required runtime dependency.

### R-MAC9-001 (source section MAC9; primary owner distribution)

The first Mac release uses architecture-specific signed, notarized, data-only per-user packages. Each package has one exact inbox payload with canonical acyclic binding to the separately signed inner descriptor and controller/desktop archive bytes; no package script, extra choice, unexpected path or executable payload activates. The trusted bootstrap verifies actual payload bytes, pinned Developer ID identity and Gatekeeper decision before fixed current-user installation, re-verifies installed bytes, activates only through packaging.Apply, audits active trees and launcher, then advances the protected sequence fence. Both native Mac architectures and real supported OS versions must pass clean-host installation, upgrade and crash-recovery qualification without Xcode on the destination.

### R-MAC9-002 (source section MAC9; primary owner zatiti)

An installed signed Mac GUI credential helper in the controller distribution captures a bounded provider key through AppKit without passing raw bytes through Dart, argv, environment or any public operation. The signed Runner sends only installation and connection IDs and receives a redacted status; the helper owns the protected Keychain and trusted challenge/store-reference/receipt flow. Setup mutations use durable submission keys and command/status reconciliation; a protected pending intent prevents silent duplicate challenges or orphaned credential refs after a lost acknowledgement or crash. Missing master-key/owner access and locked or refused Keychain fail closed. A real provider first reply and all required Serenity protocol, writer, memory, accounting, reconciliation and backup guarantees remain mandatory release gates.

### R-MAC9-003 (source section MAC9; primary owner distribution)

The one-line Mac command is generated only after the hosted bootstrap script has a literal SHA-256 in the command. That script verifies a literal hash of the signed/notarized universal bootstrap app archive before extracting and launching its fixed executable. The bootstrap app embeds the trusted delivery-signature public keys and hardware-architecture detection, then consumes the six-asset signed delivery record through RunMacBootstrap. Neither an unsigned curl-pipe-shell stage nor a fetched trust key establishes trust. Real Team ID, hosting URL, notarization and clean-host evidence are external gates; no public command is claimed from fixture tests.

### R-MAC10-001 (source section MAC10; primary owner distribution)

Mac bootstrap activation is a locked ordered chain: prove signed package bytes and Apple trust, run the fixed per-user installer, verify the installed three-file inbox against the signed plan, create or validate the fixed login-Keychain master item without rotation, activate through Apply, audit the active controller and desktop trees, helper and LaunchAgent, then advance the protected release fence. Existing-release and crash recovery require the same authoritative active-tree audit; package receipts and inert inbox bytes are insufficient. Packaging owns narrow injected interfaces and imports no platform code; cmd/zatiti composes the platform provisioner.

### R-MAC10-002 (source section MAC10; primary owner zatiti)

A protected credential-helper recovery intent retains only bounded nonsecret replay identities, account and credential name, opaque store reference, challenge version/expiry, stable begin and complete submission keys, and an exact request digest or request bytes persisted before each mutation call. A retry reconstructs byte-identical input under the same key or refuses; phase-dependent fields are validated and provider/owner/HMAC secret bytes are absent.

### R-MAC10-003 (source section MAC10; primary owner qualification)

Each native Mac release report must prove one linked clean-host install-to-first-chat journey on its architecture, binding the same signed delivery, literal script and universal bootstrap hashes to its architecture-specific package, component and helper hashes, installed-inbox and active-tree audits, fixed launcher and Keychain readiness, genuine provider response readback and full Serenity capability evidence. The Intel and Apple Silicon reports use distinct host-run IDs, agree on shared release identity, and both pass the linked named case; absent, skipped, synthetic or independently assembled evidence blocks release.

### R-MAC11-001 (source section MAC11; primary owner connections)

Mac setup signs the personal chief into the existing hosted Serenity personal brain through system-browser OAuth with explicit project and read/write consent. A trusted installed helper owns PKCE, exact loopback callback, token exchange and secure token custody; a public authenticated binding read verifies issuer, resource, account, selected project, scopes and grant before connection activation. Zatiti maps its local UUID brain to the verified hosted project ID without duplicating memory or granting other workers inherited access. Lost responses, denial, refresh/revocation and account/project changes reconcile or fail closed; tokens and codes never enter Dart, model context, public operations, argv or logs.

### R-MAC11-002 (source section MAC11; primary owner serenity)

The hosted Serenity mode is the first Mac release memory target and needs no bundled Serenity process/read facade. Its public authenticated interface must expose verifiable build/capabilities, selected grant/brain identity, full scoped claim and lineage semantics, authoritative write disposition, enforceable per-call cost/disclosure bounds, and immutable brain revision/export/recovery evidence. The free tier and account quota do not substitute for these guarantees. Both native clean-host runs must use the selected pre-existing personal brain and observe a real memory effect and provider reply through the linked signed release; any unavailable capability blocks release.

### R12-PROVIDER-CONTRACTS (source section provider model revision 12; primary owner configuration)

Revision 12 freezes strict Responses v2 provider/session schemas for OpenAI conversation mode and stateless OpenRouter/Experiential gateway modes; immutable v1 schemas remain readable. Hosted execution profiles pin a complete secret-free adapter profile and exact connection version. Effects persists that exact profile with every operation and returns it during claim/reconciliation. Controller injects execution-owned context performance and records turn observations without task fabrication. Provider metadata is a fixed local query. Provider cost evidence preserves exact source decimal and converts to integer micro-units with checked arithmetic and upward rounding; unknown/unpriced BYOK cost remains advisory.
## Exact operation and dependency schemas

### `artifact.export` v1 — artifacts / public / mutation / local

CLI `zatiti artifact export`; MCP `zatiti_artifact_export`. Submission key: required.

Produce authorized export artifact; client chooses local output path, no server path input.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Job"}},"required":["resource"]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Artifact"}},"required":["resource"]}
```

### `artifact.get` v1 — artifacts / public / query / local

CLI `zatiti artifact get`; MCP `zatiti_artifact_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Artifact"}},"required":["resource"]}
```

### `artifact.list` v1 — artifacts / public / query / local

CLI `zatiti artifact list`; MCP `zatiti_artifact_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Artifact"},"maxItems":500}},"required":["items"]}
```

### `artifact.read` v1 — artifacts / public / query / local

CLI `zatiti artifact read`; MCP `zatiti_artifact_read`. Submission key: not required at this internal/query/bootstrap boundary.

Read authorized bounded range outside transaction after metadata access; integrity/auth checks precede disclosure. Max encoded output follows 1 MiB decoded.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"offset":{"type":"integer","minimum":0,"maximum":9223372036854775807},"length":{"type":"integer","minimum":1,"maximum":1048576}},"required":["scope","id","offset","length"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"bytes_base64":{"type":"string","maxLength":1398104},"digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"offset":{"type":"integer","minimum":0,"maximum":9223372036854775807},"total_size":{"type":"integer","minimum":0,"maximum":9223372036854775807}},"required":["bytes_base64","digest","offset","total_size"]}
```

### `artifact.upload.begin` v1 — artifacts / public / mutation / local

CLI `zatiti artifact upload begin`; MCP `zatiti_artifact_upload_begin`. Submission key: required.

Allocate bounded staged upload tied to caller/scope/digest/size; no published artifact yet.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"size":{"type":"integer","minimum":0,"maximum":9223372036854775807},"digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"media_type":{"type":"string","maxLength":8192},"classification":{"type":"string","enum":["internal","public","restricted"]}},"required":["scope","size","digest","media_type","classification"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Upload"}},"required":["resource"]}
```

### `artifact.upload.cancel` v1 — artifacts / public / mutation / local

CLI `zatiti artifact upload cancel`; MCP `zatiti_artifact_upload_cancel`. Submission key: required.

Cancel unpublished upload and queue safe staged-content cleanup; do not delete referenced content.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"upload_id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","upload_id","expected_version"]}
```
Output data schema:
```json
{"oneOf":[{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Disposition"}},"required":["resource"]},{"type":"object","additionalProperties":false,"properties":{"job":{"$ref":"#/$defs/Job"}},"required":["job"]}]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Disposition"}},"required":["resource"]}
```

### `artifact.upload.chunk` v1 — artifacts / public / mutation / local

CLI `zatiti artifact upload chunk`; MCP `zatiti_artifact_upload_chunk`. Submission key: required.

Write staged bounded chunk outside DB transaction via prepare/record phases. Same offset and bytes replay; different bytes conflict. No arbitrary server path.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"upload_id":{"type":"string","format":"uuid"},"offset":{"type":"integer","minimum":0,"maximum":9223372036854775807},"bytes_base64":{"type":"string","maxLength":1398104},"chunk_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"}},"required":["scope","upload_id","offset","bytes_base64","chunk_digest"]}
```
Output data schema:
```json
{"oneOf":[{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Upload"}},"required":["resource"]},{"type":"object","additionalProperties":false,"properties":{"job":{"$ref":"#/$defs/Job"}},"required":["job"]}]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Upload"}},"required":["resource"]}
```

### `artifact.upload.finish` v1 — artifacts / public / mutation / local

CLI `zatiti artifact upload finish`; MCP `zatiti_artifact_upload_finish`. Submission key: required.

Hash/size verify complete staged bytes, publish content-addressed file, then commit metadata/event. Missing committed bytes produce artifact_fault; orphan bytes are reclaimed later.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"upload_id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","upload_id","expected_version"]}
```
Output data schema:
```json
{"oneOf":[{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Artifact"}},"required":["resource"]},{"type":"object","additionalProperties":false,"properties":{"job":{"$ref":"#/$defs/Job"}},"required":["job"]}]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Artifact"}},"required":["resource"]}
```

### `attempt.cancel` v1 — execution / public / mutation / local

CLI `zatiti attempt cancel`; MCP `zatiti_attempt_cancel`. Submission key: required.

Commit cancellation intent and fence governed work as applicable; do not assert external process stopped. Preserve uncertain effects.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"reason":{"type":"string","maxLength":8192}},"required":["scope","id","expected_version","reason"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Disposition"}},"required":["resource"]}
```

### `attempt.checkpoint` v1 — execution / public / mutation / local

CLI `zatiti attempt checkpoint`; MCP `zatiti_attempt_checkpoint`. Submission key: required.

Persist reconstructable context and output disposition at safe boundary with pinned versions; declare external capture limits.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"attempt_id":{"type":"string","format":"uuid"},"lease_id":{"type":"string","format":"uuid"},"generation":{"type":"integer","minimum":1,"maximum":9223372036854775807},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"context":{"$ref":"#/$defs/ArtifactRef"},"outputs":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096}},"required":["scope","attempt_id","lease_id","generation","expected_version","context","outputs"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Attempt"}},"required":["resource"]}
```

### `attempt.get` v1 — execution / public / query / local

CLI `zatiti attempt get`; MCP `zatiti_attempt_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Attempt"}},"required":["resource"]}
```

### `attempt.heartbeat` v1 — execution / public / mutation / local

CLI `zatiti attempt heartbeat`; MCP `zatiti_attempt_heartbeat`. Submission key: required.

Extend current valid lease only for bound identity/generation; stale heartbeats cannot revive fenced attempts.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"attempt_id":{"type":"string","format":"uuid"},"lease_id":{"type":"string","format":"uuid"},"generation":{"type":"integer","minimum":1,"maximum":9223372036854775807},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","attempt_id","lease_id","generation","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Attempt"}},"required":["resource"]}
```

### `attempt.list` v1 — execution / public / query / local

CLI `zatiti attempt list`; MCP `zatiti_attempt_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Attempt"},"maxItems":500}},"required":["items"]}
```

### `attempt.recovery` v1 — execution / public / query / local

CLI `zatiti attempt recovery`; MCP `zatiti_attempt_recovery`. Submission key: not required at this internal/query/bootstrap boundary.

Inspect generation, leases, conflicting resources and effect obligations before replacement.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Attempt"},"obligations":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096}},"required":["resource","obligations"]}
```

### `attempt.report` v1 — execution / public / mutation / local

CLI `zatiti attempt report`; MCP `zatiti_attempt_report`. Submission key: required.

Persist observations and enter independent verification; process exit/report never directly succeeds task. Reject stale/wrong-attempt reports.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"attempt_id":{"type":"string","format":"uuid"},"lease_id":{"type":"string","format":"uuid"},"generation":{"type":"integer","minimum":1,"maximum":9223372036854775807},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"outputs":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"observations":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"usage":{"$ref":"#/$defs/Usage"}},"required":["scope","attempt_id","lease_id","generation","expected_version","outputs","observations","usage"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Attempt"}},"required":["resource"]}
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

### `binding.archive` v1 — configuration / public / mutation / local

CLI `zatiti binding archive`; MCP `zatiti_binding_archive`. Submission key: required.

Stage a typed archive in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Binding"}},"required":["draft","resource"]}
```

### `binding.create` v1 — configuration / public / mutation / local

CLI `zatiti binding create`; MCP `zatiti_binding_create`. Submission key: required.

Stage a typed create in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"definition":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"kind":{"type":"string","enum":["tool","skill","connection","worker","repository","reporting","memory"]},"target_id":{"type":"string","format":"uuid"},"permissions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"source_scope":{"$ref":"#/$defs/Scope"},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096}},"required":["scope","kind","target_id","permissions"]},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Binding"}},"required":["draft","resource"]}
```

### `binding.get` v1 — configuration / public / query / local

CLI `zatiti binding get`; MCP `zatiti_binding_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Binding"}},"required":["resource"]}
```

### `binding.list` v1 — configuration / public / query / local

CLI `zatiti binding list`; MCP `zatiti_binding_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Binding"},"maxItems":500}},"required":["items"]}
```

### `binding.update` v1 — configuration / public / mutation / local

CLI `zatiti binding update`; MCP `zatiti_binding_update`. Submission key: required.

Stage a typed update in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"definition":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"kind":{"type":"string","enum":["tool","skill","connection","worker","repository","reporting","memory"]},"target_id":{"type":"string","format":"uuid"},"permissions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"source_scope":{"$ref":"#/$defs/Scope"},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096}},"required":["scope","kind","target_id","permissions"]},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Binding"}},"required":["draft","resource"]}
```

### `budget.get` v1 — accounting / public / query / local

CLI `zatiti budget get`; MCP `zatiti_budget_get`. Submission key: not required at this internal/query/bootstrap boundary.

Inspect effective intersected installation/ancestor/project/worker/root limits.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"limits":{"$ref":"#/$defs/Limits"}},"required":["limits"]}
```

### `budget.propose` v1 — accounting / public / mutation / local

CLI `zatiti budget propose`; MCP `zatiti_budget_propose`. Submission key: required.

Stage exact budget change under old authority; no paid execution until explicit currency/finite spend.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"limits":{"$ref":"#/$defs/Limits"},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","expected_version","limits"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Draft"}},"required":["resource"]}
```

### `capabilities.list` v1 — registry / public / query / local

CLI `zatiti capabilities`; MCP `zatiti_capabilities`. Submission key: not required at this internal/query/bootstrap boundary.

Enumerate complete public operation catalog/support versions without granting authority.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"}},"required":[]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Descriptor"},"maxItems":500}},"required":["items"]}
```

### `capabilities.schema` v1 — registry / public / query / local

CLI `zatiti capabilities schema`; MCP `zatiti_capabilities_schema`. Submission key: not required at this internal/query/bootstrap boundary.

Return exact schemas, names, semantics and availability prerequisites; no hidden product operations.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"operation":{"type":"string","maxLength":8192},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["operation","version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Descriptor"}},"required":["resource"]}
```

### `command.get` v1 — evidence / public / query / local

CLI `zatiti command get`; MCP `zatiti_command_get`. Submission key: not required at this internal/query/bootstrap boundary.

Look up caller-bound durable disposition after lost acknowledgement; never return another principal command.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"submission_key":{"type":"string","maxLength":8192},"operation":{"type":"string","maxLength":8192},"operation_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","submission_key","operation","operation_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Command"}},"required":["resource"]}
```

### `configuration.apply` v1 — configuration / public / mutation / local

CLI `zatiti configuration apply`; MCP `zatiti_configuration_apply`. Submission key: required.

Atomically recheck current head, old authority, restrictions, dependency/prerequisite validity and exact decisions, activate complete bundle and append event. Stale plans fail, identical key replay returns original result.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"plan_id":{"type":"string","format":"uuid"},"base_revision":{"type":"integer","minimum":1,"maximum":9223372036854775807},"candidate_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"}},"required":["scope","plan_id","base_revision","candidate_digest"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Revision"}},"required":["resource"]}
```

### `configuration.draft.create` v1 — configuration / public / mutation / local

CLI `zatiti configuration draft create`; MCP `zatiti_configuration_draft_create`. Submission key: required.

Create empty draft against current revision.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"base_revision":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","base_revision"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Draft"}},"required":["resource"]}
```

### `configuration.draft.discard` v1 — configuration / public / mutation / local

CLI `zatiti configuration draft discard`; MCP `zatiti_configuration_draft_discard`. Submission key: required.

Discard only draft; preserve plan lineage and evidence.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Disposition"}},"required":["resource"]}
```

### `configuration.draft.get` v1 — configuration / public / query / local

CLI `zatiti configuration draft get`; MCP `zatiti_configuration_draft_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Draft"}},"required":["resource"]}
```

### `configuration.draft.list` v1 — configuration / public / query / local

CLI `zatiti configuration draft list`; MCP `zatiti_configuration_draft_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Draft"},"maxItems":500}},"required":["items"]}
```

### `configuration.draft.update` v1 — configuration / public / mutation / local

CLI `zatiti configuration draft update`; MCP `zatiti_configuration_draft_update`. Submission key: required.

Validate each change against its concrete kind schema. Complete definitions, no implicit deletion. Unknown kind or fields fail. Changes remain inactive.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"changes":{"type":"array","items":{"$ref":"#/$defs/Change"},"maxItems":4096}},"required":["scope","id","expected_version","changes"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Draft"}},"required":["resource"]}
```

### `configuration.plan` v1 — configuration / public / mutation / local

CLI `zatiti configuration plan`; MCP `zatiti_configuration_plan`. Submission key: required.

Seal canonical candidate, base revision, changed objects, dependency identities, compiler/schema versions, authority delta and prerequisites. Return exact preview and missing requirements; no live activation.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"draft_id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","draft_id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Plan"}},"required":["resource"]}
```

### `configuration.plan.get` v1 — configuration / public / query / local

CLI `zatiti configuration plan get`; MCP `zatiti_configuration_plan_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Plan"}},"required":["resource"]}
```

### `configuration.plan.list` v1 — configuration / public / query / local

CLI `zatiti configuration plan list`; MCP `zatiti_configuration_plan_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Plan"},"maxItems":500}},"required":["items"]}
```

### `configuration.revision.get` v1 — configuration / public / query / local

CLI `zatiti configuration revision get`; MCP `zatiti_configuration_revision_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Revision"}},"required":["resource"]}
```

### `configuration.revision.list` v1 — configuration / public / query / local

CLI `zatiti configuration revision list`; MCP `zatiti_configuration_revision_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Revision"},"maxItems":500}},"required":["items"]}
```

### `configuration.rollback.plan` v1 — configuration / public / mutation / local

CLI `zatiti configuration rollback plan`; MCP `zatiti_configuration_rollback_plan`. Submission key: required.

Build a new plan against current head restoring eligible definitions; no database rewind or erased obligations.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"revision_id":{"type":"string","format":"uuid"},"base_revision":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","revision_id","base_revision"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Plan"}},"required":["resource"]}
```

### `connection.archive` v1 — connections / public / mutation / local

CLI `zatiti connection archive`; MCP `zatiti_connection_archive`. Submission key: required.

Stage a typed archive in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Connection"}},"required":["draft","resource"]}
```

### `connection.create` v1 — connections / public / mutation / local

CLI `zatiti connection create`; MCP `zatiti_connection_create`. Submission key: required.

Stage a typed create in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"definition":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"provider":{"type":"string","maxLength":8192},"account_identity":{"type":"string","maxLength":8192},"credential_ref":{"type":"string","maxLength":8192},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"allowed_scopes":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096}},"required":["scope","provider","account_identity","credential_ref","destinations","allowed_scopes"]},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Connection"}},"required":["draft","resource"]}
```

### `connection.get` v1 — connections / public / query / local

CLI `zatiti connection get`; MCP `zatiti_connection_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Connection"}},"required":["resource"]}
```

### `connection.list` v1 — connections / public / query / local

CLI `zatiti connection list`; MCP `zatiti_connection_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Connection"},"maxItems":500}},"required":["items"]}
```

### `connection.revoke` v1 — connections / public / mutation / local

CLI `zatiti connection revoke`; MCP `zatiti_connection_revoke`. Submission key: required.

Immediately block credential use and future dispatch; retain unresolved effects and secret cleanup obligation.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Disposition"}},"required":["resource"]}
```

### `connection.rotate` v1 — connections / public / mutation / external_read

CLI `zatiti connection rotate`; MCP `zatiti_connection_rotate`. Submission key: required.

Same-account rotation validates account identity before replacement. Account substitution requires a new exact configuration/review under old policy.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"store_ref":{"type":"string","maxLength":8192}},"required":["scope","id","expected_version","store_ref"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Job"}},"required":["resource"]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Connection"}},"required":["resource"]}
```

### `connection.setup.begin` v1 — connections / public / mutation / local

CLI `zatiti connection setup begin`; MCP `zatiti_connection_setup_begin`. Submission key: required.

Typed credential challenge: begin. Trusted local helper handles all codes/tokens outside model-visible data. Begin may return external_action_required with challenge; complete validates bound helper result/account/current authority. Expired/cancelled challenges cannot complete.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"connection_id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"method":{"type":"string","enum":["browser","store_reference"]}},"required":["scope","connection_id","expected_version","method"]}
```
Output data schema:
```json
{"oneOf":[{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Challenge"}},"required":["resource"]},{"type":"object","additionalProperties":false,"properties":{"job":{"$ref":"#/$defs/Job"}},"required":["job"]}]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Challenge"}},"required":["resource"]}
```

### `connection.setup.cancel` v1 — connections / public / mutation / local

CLI `zatiti connection setup cancel`; MCP `zatiti_connection_setup_cancel`. Submission key: required.

Typed credential challenge: cancel. Trusted local helper handles all codes/tokens outside model-visible data. Begin may return external_action_required with challenge; complete validates bound helper result/account/current authority. Expired/cancelled challenges cannot complete.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"challenge_id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","challenge_id","expected_version"]}
```
Output data schema:
```json
{"oneOf":[{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Challenge"}},"required":["resource"]},{"type":"object","additionalProperties":false,"properties":{"job":{"$ref":"#/$defs/Job"}},"required":["job"]}]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Challenge"}},"required":["resource"]}
```

### `connection.setup.complete` v1 — connections / public / mutation / local

CLI `zatiti connection setup complete`; MCP `zatiti_connection_setup_complete`. Submission key: required.

Typed credential challenge: complete. Trusted local helper handles all codes/tokens outside model-visible data. Begin may return external_action_required with challenge; complete validates bound helper result/account/current authority. Expired/cancelled challenges cannot complete.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"challenge_id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"helper_ref":{"type":"string","maxLength":8192}},"required":["scope","challenge_id","expected_version","helper_ref"]}
```
Output data schema:
```json
{"oneOf":[{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Challenge"}},"required":["resource"]},{"type":"object","additionalProperties":false,"properties":{"job":{"$ref":"#/$defs/Job"}},"required":["job"]}]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Challenge"}},"required":["resource"]}
```

### `connection.setup.status` v1 — connections / public / query / local

CLI `zatiti connection setup status`; MCP `zatiti_connection_setup_status`. Submission key: not required at this internal/query/bootstrap boundary.

Typed credential challenge: status. Trusted local helper handles all codes/tokens outside model-visible data. Begin may return external_action_required with challenge; complete validates bound helper result/account/current authority. Expired/cancelled challenges cannot complete.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"challenge_id":{"type":"string","format":"uuid"}},"required":["scope","challenge_id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Challenge"}},"required":["resource"]}
```

### `connection.update` v1 — connections / public / mutation / local

CLI `zatiti connection update`; MCP `zatiti_connection_update`. Submission key: required.

Stage a typed update in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"definition":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"provider":{"type":"string","maxLength":8192},"account_identity":{"type":"string","maxLength":8192},"credential_ref":{"type":"string","maxLength":8192},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"allowed_scopes":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096}},"required":["scope","provider","account_identity","credential_ref","destinations","allowed_scopes"]},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Connection"}},"required":["draft","resource"]}
```

### `connection.validate` v1 — connections / public / mutation / external_read

CLI `zatiti connection validate`; MCP `zatiti_connection_validate`. Submission key: required.

Admit a bounded separately authorized provider probe. Record observed account/scopes, timestamp and freshness. Mismatch cannot silently substitute accounts.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Job"}},"required":["resource"]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Connection"}},"required":["resource"]}
```

### `conversation.create` v1 — messaging / public / mutation / local

CLI `zatiti conversation create`; MCP `zatiti_conversation_create`. Submission key: required.

Create conversation without changing home organization, memory access or tool grants; validate participant disclosure bindings.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"kind":{"type":"string","enum":["direct","group"]},"participant_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"title":{"type":"string","maxLength":8192}},"required":["scope","kind","participant_ids","title"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Conversation"}},"required":["resource"]}
```

### `conversation.get` v1 — messaging / public / query / local

CLI `zatiti conversation get`; MCP `zatiti_conversation_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Conversation"}},"required":["resource"]}
```

### `conversation.list` v1 — messaging / public / query / local

CLI `zatiti conversation list`; MCP `zatiti_conversation_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Conversation"},"maxItems":500}},"required":["items"]}
```

### `conversation.message.list` v1 — messaging / public / query / local

CLI `zatiti conversation message list`; MCP `zatiti_conversation_message_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read authorized sent/received message history for a conversation, limited to disclosed membership intervals; joining a group discloses no retroactive restricted history.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"conversation_id":{"type":"string","format":"uuid"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200}},"required":["scope","conversation_id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Message"},"maxItems":500}},"required":["items"]}
```

### `conversation.message.send` v1 — messaging / public / mutation / disclosure

CLI `zatiti conversation message send`; MCP `zatiti_conversation_message_send`. Submission key: required.

Durably admit authenticated user message; attachments/participants are governed disclosures. Task creation/assignment uses existing versioned task operations, not duplicate scheduling.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"conversation_id":{"type":"string","format":"uuid"},"message_id":{"type":"string","format":"uuid"},"body":{"type":"string","maxLength":8192},"attachments":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"task_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096}},"required":["scope","conversation_id","message_id","body","attachments","task_ids"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Message"}},"required":["resource"]}
```

### `conversation.update` v1 — messaging / public / mutation / local

CLI `zatiti conversation update`; MCP `zatiti_conversation_update`. Submission key: required.

Versioned membership/title updates require governed disclosure; joining grants no historical restricted material automatically.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"participant_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"title":{"type":"string","maxLength":8192},"pinned":{"type":"boolean"}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Conversation"}},"required":["resource"]}
```

### `credential.provision` v1 — identity / public / mutation / local

CLI `zatiti credential provision`; MCP `zatiti_credential_provision`. Submission key: required.

Attach a helper-provisioned opaque local reference; authenticate its binding. Never accept or return secret bytes.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"principal_id":{"type":"string","format":"uuid"},"store_ref":{"type":"string","maxLength":8192},"expires_at":{"type":"string","format":"date-time"}},"required":["scope","principal_id","store_ref"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Credential"}},"required":["resource"]}
```

### `credential.revoke` v1 — identity / public / mutation / local

CLI `zatiti credential revoke`; MCP `zatiti_credential_revoke`. Submission key: required.

Commit revocation immediately; future authentication and claims fail. Retain revocation through restore.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Credential"}},"required":["resource"]}
```

### `event.get` v1 — evidence / public / query / local

CLI `zatiti event get`; MCP `zatiti_event_get`. Submission key: not required at this internal/query/bootstrap boundary.

Read authorized immutable event.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Event"}},"required":["resource"]}
```

### `event.list` v1 — evidence / public / query / local

CLI `zatiti event list`; MCP `zatiti_event_list`. Submission key: not required at this internal/query/bootstrap boundary.

Replay authorized events after opaque cursor with explicit gap/expiry handling; fetch snapshot when expired. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":500},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Event"},"maxItems":500}},"required":["items"]}
```

### `execution_profile.archive` v1 — configuration / public / mutation / local

CLI `zatiti execution_profile archive`; MCP `zatiti_execution_profile_archive`. Submission key: required.

Stage a typed archive in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/ExecutionProfile"}},"required":["draft","resource"]}
```

### `execution_profile.create` v1 — configuration / public / mutation / local

CLI `zatiti execution_profile create`; MCP `zatiti_execution_profile_create`. Submission key: required.

Stage a typed create in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"definition":{"type":"object","additionalProperties":false,"properties":{"executor":{"type":"string","enum":["hosted","cooperative"]},"model":{"type":"string","maxLength":8192},"connection_id":{"type":"string","format":"uuid"},"provider_destination":{"type":"string","maxLength":8192},"capabilities":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"cost_bound":{"$ref":"#/$defs/Money"},"classification":{"type":"string","enum":["internal","public","restricted"]},"context_capture":{"type":"string","enum":["complete","partial","advisory"]},"adapter_profile":{"$ref":"#/$defs/ResponsesProfile"},"connection_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["executor","model","connection_id","provider_destination","capabilities","cost_bound","classification","context_capture"]},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/ExecutionProfile"}},"required":["draft","resource"]}
```

### `execution_profile.get` v1 — configuration / public / query / local

CLI `zatiti execution_profile get`; MCP `zatiti_execution_profile_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/ExecutionProfile"}},"required":["resource"]}
```

### `execution_profile.list` v1 — configuration / public / query / local

CLI `zatiti execution_profile list`; MCP `zatiti_execution_profile_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/ExecutionProfile"},"maxItems":500}},"required":["items"]}
```

### `execution_profile.qualify` v1 — configuration / public / mutation / external_read

CLI `zatiti execution_profile qualify`; MCP `zatiti_execution_profile_qualify`. Submission key: required.

Run one explicit, bounded provider qualification probe for this exact connection version, model, endpoint, route and pricing profile. The probe is separately authorized and accounted, accepts no capability-evidence input, and produces a trusted qualified profile only from the recorded physical-call observation. A lost acknowledgement remains outcome_unknown and is never silently resent; the caller retrieves the same job and may only start a new probe after authoritative non-execution.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"definition":{"$ref":"#/$defs/ExecutionProfileCandidate"},"qualification_cost_bound":{"$ref":"#/$defs/Money"}},"required":["scope","definition","qualification_cost_bound"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"job":{"$ref":"#/$defs/Job"}},"required":["job"]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/QualifiedExecutionProfile"}},"required":["resource"]}
```

### `execution_profile.update` v1 — configuration / public / mutation / local

CLI `zatiti execution_profile update`; MCP `zatiti_execution_profile_update`. Submission key: required.

Stage a typed update in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"definition":{"type":"object","additionalProperties":false,"properties":{"executor":{"type":"string","enum":["hosted","cooperative"]},"model":{"type":"string","maxLength":8192},"connection_id":{"type":"string","format":"uuid"},"provider_destination":{"type":"string","maxLength":8192},"capabilities":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"cost_bound":{"$ref":"#/$defs/Money"},"classification":{"type":"string","enum":["internal","public","restricted"]},"context_capture":{"type":"string","enum":["complete","partial","advisory"]},"adapter_profile":{"$ref":"#/$defs/ResponsesProfile"},"connection_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["executor","model","connection_id","provider_destination","capabilities","cost_bound","classification","context_capture"]},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/ExecutionProfile"}},"required":["draft","resource"]}
```

### `grant.create` v1 — identity / public / mutation / local

CLI `zatiti grant create`; MCP `zatiti_grant_create`. Submission key: required.

Apply create with current authorization, expected version where existing, durable command replay and atomic evidence. Revocation is immediate restriction.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"definition":{"type":"object","additionalProperties":false,"properties":{"principal_id":{"type":"string","format":"uuid"},"scope":{"$ref":"#/$defs/Scope"},"capabilities":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"denied":{"type":"boolean"},"expires_at":{"type":"string","format":"date-time"},"parent_grant_id":{"type":"string","format":"uuid"}},"required":["principal_id","scope","capabilities","destinations","denied"]}},"required":["scope","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Grant"}},"required":["resource"]}
```

### `grant.get` v1 — identity / public / query / local

CLI `zatiti grant get`; MCP `zatiti_grant_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Grant"}},"required":["resource"]}
```

### `grant.list` v1 — identity / public / query / local

CLI `zatiti grant list`; MCP `zatiti_grant_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Grant"},"maxItems":500}},"required":["items"]}
```

### `grant.revoke` v1 — identity / public / mutation / local

CLI `zatiti grant revoke`; MCP `zatiti_grant_revoke`. Submission key: required.

Apply revoke with current authorization, expected version where existing, durable command replay and atomic evidence. Revocation is immediate restriction.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Grant"}},"required":["resource"]}
```

### `grant.update` v1 — identity / public / mutation / local

CLI `zatiti grant update`; MCP `zatiti_grant_update`. Submission key: required.

Apply update with current authorization, expected version where existing, durable command replay and atomic evidence. Revocation is immediate restriction.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"definition":{"type":"object","additionalProperties":false,"properties":{"principal_id":{"type":"string","format":"uuid"},"scope":{"$ref":"#/$defs/Scope"},"capabilities":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"denied":{"type":"boolean"},"expires_at":{"type":"string","format":"date-time"},"parent_grant_id":{"type":"string","format":"uuid"}},"required":["principal_id","scope","capabilities","destinations","denied"]}},"required":["scope","id","expected_version","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Grant"}},"required":["resource"]}
```

### `installation.backup` v1 — installation / public / mutation / local

CLI `zatiti installation backup`; MCP `zatiti_installation_backup`. Submission key: required.

Create encrypted verified consistent SQLite+artifact+brain manifest backup. Return opaque backup artifact; preserve separate key-recovery prerequisites. Never copy a live SQLite file casually.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Job"}},"required":["resource"]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Backup"}},"required":["resource"]}
```

### `installation.doctor` v1 — installation / public / query / local

CLI `zatiti installation doctor`; MCP `zatiti_installation_doctor`. Submission key: not required at this internal/query/bootstrap boundary.

Inspect acknowledged owner/generation/paused/maintenance state and named prerequisites/limitations with secret-free diagnostics.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Status"}},"required":["resource"]}
```

### `installation.init` v1 — installation / public / mutation / local

CLI `zatiti init`; MCP `zatiti_installation_init`. Submission key: not required at this internal/query/bootstrap boundary.

One-time OS-authorized local bootstrap acquires exclusive lock and creates installation, owner credential and personal organization/chief together. Return metadata only. Refuse initialized destination. End bootstrap session; paid work remains unavailable.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"credential_store":{"type":"string","enum":["os","headless"]},"owner_name":{"type":"string","maxLength":8192},"headless_key_ref":{"type":"string","maxLength":8192}},"required":["credential_store","owner_name"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Status"}},"required":["resource"]}
```

### `installation.job.get` v1 — installation / public / query / local

CLI `zatiti installation job get`; MCP `zatiti_installation_job_get`. Submission key: not required at this internal/query/bootstrap boundary.

Inspect durable backup/restore progress and verified results.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Job"}},"required":["resource"]}
```

### `installation.maintenance.enter` v1 — installation / public / mutation / local

CLI `zatiti installation maintenance enter`; MCP `zatiti_installation_maintenance_enter`. Submission key: required.

Pause/maintenance enter immediately blocks new admissions; maintenance drains/fences work and records unresolved effects. Resume rechecks current authority and recovery prerequisites; never claims in-flight bytes were retracted.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Status"}},"required":["resource"]}
```

### `installation.pause` v1 — installation / public / mutation / local

CLI `zatiti installation pause`; MCP `zatiti_installation_pause`. Submission key: required.

Pause/maintenance enter immediately blocks new admissions; maintenance drains/fences work and records unresolved effects. Resume rechecks current authority and recovery prerequisites; never claims in-flight bytes were retracted.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Status"}},"required":["resource"]}
```

### `installation.restore` v1 — installation / public / mutation / local

CLI `zatiti installation restore`; MCP `zatiti_installation_restore`. Submission key: required.

Require exclusive quiesced local-owner maintenance session. Verify encrypted backup/installation bindings/keys. Restore paused, preserve revocation/command identities/reservations and reconcile provider/memory intents before resume.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"backup_artifact":{"$ref":"#/$defs/ArtifactRef"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","backup_artifact","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Job"}},"required":["resource"]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Status"}},"required":["resource"]}
```

### `installation.resume` v1 — installation / public / mutation / local

CLI `zatiti installation resume`; MCP `zatiti_installation_resume`. Submission key: required.

Pause/maintenance enter immediately blocks new admissions; maintenance drains/fences work and records unresolved effects. Resume rechecks current authority and recovery prerequisites; never claims in-flight bytes were retracted.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Status"}},"required":["resource"]}
```

### `installation.status` v1 — installation / public / query / local

CLI `zatiti installation status`; MCP `zatiti_installation_status`. Submission key: not required at this internal/query/bootstrap boundary.

Inspect acknowledged owner/generation/paused/maintenance state and named prerequisites/limitations with secret-free diagnostics.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Status"}},"required":["resource"]}
```

### `installation.verifier.list` v1 — installation / public / query / local

CLI `zatiti installation verifier list`; MCP `zatiti_installation_verifier_list`. Submission key: not required at this internal/query/bootstrap boundary.

Enumerate installed trusted verifier profiles available to name in a task acceptance contract; secret-free, no executable path or argv disclosed.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/VerifierDescriptor"},"maxItems":500}},"required":["items"]}
```

### `job.get` v1 — execution / public / query / local

CLI `zatiti job get`; MCP `zatiti_job_get`. Submission key: not required at this internal/query/bootstrap boundary.

Inspect authorized durable job state/result/evidence for any owner. Status never equates provider acceptance to confirmed success.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Job"}},"required":["resource"]}
```

### `mailbox.ack` v1 — messaging / public / mutation / local

CLI `zatiti mailbox ack`; MCP `zatiti_mailbox_ack`. Submission key: required.

Acknowledge durable target admission at safe worker boundary; enforce recipient/current attempt where worker-bound.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"message_id":{"type":"string","format":"uuid"},"recipient_id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","message_id","recipient_id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Message"}},"required":["resource"]}
```

### `mailbox.list` v1 — messaging / public / query / local

CLI `zatiti mailbox list`; MCP `zatiti_mailbox_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read authorized admitted messages; no authority from body text. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]},"recipient_id":{"type":"string","format":"uuid"}},"required":["scope","recipient_id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Message"},"maxItems":500}},"required":["items"]}
```

### `mailbox.send` v1 — messaging / public / mutation / disclosure

CLI `zatiti mailbox send`; MCP `zatiti_mailbox_send`. Submission key: required.

Record authenticated sender, scope and stable identity; acknowledge receipt only after durable target admission. Redelivery deduplicates identity and checks changed content conflict.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"message_id":{"type":"string","format":"uuid"},"recipient_id":{"type":"string","format":"uuid"},"body":{"type":"string","maxLength":8192},"attachments":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"task_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096}},"required":["scope","message_id","recipient_id","body","attachments","task_ids"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Message"}},"required":["resource"]}
```

### `memory.binding.archive` v1 — memory / public / mutation / local

CLI `zatiti memory binding archive`; MCP `zatiti_memory_binding_archive`. Submission key: required.

Stage a typed archive in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/MemoryBinding"}},"required":["draft","resource"]}
```

### `memory.binding.create` v1 — memory / public / mutation / local

CLI `zatiti memory binding create`; MCP `zatiti_memory_binding_create`. Submission key: required.

Stage a typed create in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"definition":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"brain_id":{"type":"string","format":"uuid"},"permissions":{"type":"array","items":{"type":"string","enum":["read","write","curate","promote","retract"]},"maxItems":4096},"classification":{"type":"string","enum":["internal","public","restricted"]}},"required":["scope","brain_id","permissions","classification"]},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/MemoryBinding"}},"required":["draft","resource"]}
```

### `memory.binding.get` v1 — memory / public / query / local

CLI `zatiti memory binding get`; MCP `zatiti_memory_binding_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/MemoryBinding"}},"required":["resource"]}
```

### `memory.binding.list` v1 — memory / public / query / local

CLI `zatiti memory binding list`; MCP `zatiti_memory_binding_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/MemoryBinding"},"maxItems":500}},"required":["items"]}
```

### `memory.binding.update` v1 — memory / public / mutation / local

CLI `zatiti memory binding update`; MCP `zatiti_memory_binding_update`. Submission key: required.

Stage a typed update in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"definition":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"brain_id":{"type":"string","format":"uuid"},"permissions":{"type":"array","items":{"type":"string","enum":["read","write","curate","promote","retract"]},"maxItems":4096},"classification":{"type":"string","enum":["internal","public","restricted"]}},"required":["scope","brain_id","permissions","classification"]},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/MemoryBinding"}},"required":["draft","resource"]}
```

### `memory.inspect` v1 — memory / public / query / local

CLI `zatiti memory inspect`; MCP `zatiti_memory_inspect`. Submission key: not required at this internal/query/bootstrap boundary.

Inspect authorized scoped claim through public adapter facade; no unbound brain access. Potential provider work still requires separately admitted job.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"brain_id":{"type":"string","format":"uuid"},"claim_id":{"type":"string","format":"uuid"}},"required":["scope","brain_id","claim_id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Claim"}},"required":["resource"]}
```

### `memory.job.get` v1 — memory / public / query / local

CLI `zatiti memory job get`; MCP `zatiti_memory_job_get`. Submission key: not required at this internal/query/bootstrap boundary.

Inspect actual memory disposition, prerequisites, context artifact and unresolved writer obligations.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Job"}},"required":["resource"]}
```

### `memory.list` v1 — memory / public / query / local

CLI `zatiti memory list`; MCP `zatiti_memory_list`. Submission key: not required at this internal/query/bootstrap boundary.

List authorized scoped claim refs with source, freshness, lineage and supported actions for brains the caller can access; never lists an unauthorized brain and never performs paid retrieval.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"binding_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200}},"required":["scope","binding_ids"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Claim"},"maxItems":500}},"required":["items"]}
```

### `memory.promote` v1 — memory / public / mutation / external_mutation

CLI `zatiti memory promote`; MCP `zatiti_memory_promote`. Submission key: required.

Require source disclosure and destination write/curate authority. New destination claim retains source/version/evidence/curator/redaction lineage; copies are not independent corroboration.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"source_brain_id":{"type":"string","format":"uuid"},"source_claim":{"$ref":"#/$defs/Ref"},"destination_binding_id":{"type":"string","format":"uuid"},"redaction":{"type":"string","maxLength":8192},"limits":{"$ref":"#/$defs/Limits"}},"required":["scope","source_brain_id","source_claim","destination_binding_id","limits"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Job"}},"required":["resource"]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"claims":{"type":"array","items":{"$ref":"#/$defs/Claim"},"maxItems":4096},"obligations":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096}},"required":["claims","obligations"]}
```

### `memory.recall` v1 — memory / public / mutation / disclosure

CLI `zatiti memory recall`; MCP `zatiti_memory_recall`. Submission key: required.

Authorize and select brains BEFORE retrieval/model composition. Recall may charge/disclose and uses governed effects. Store actual context artifact with scope/sources/confidence/versions/freshness; unavailable bound => wait/refusal.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"query":{"type":"string","maxLength":8192},"binding_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"minimum_freshness":{"type":"string","format":"date-time"},"limits":{"$ref":"#/$defs/Limits"}},"required":["scope","query","binding_ids","minimum_freshness","limits"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Job"}},"required":["resource"]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"context_artifact":{"$ref":"#/$defs/ArtifactRef"},"claims":{"type":"array","items":{"$ref":"#/$defs/Claim"},"maxItems":4096},"brain_versions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"freshness":{"type":"string","format":"date-time"},"requirements":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096}},"required":["context_artifact","claims","brain_versions","freshness","requirements"]}
```

### `memory.remember` v1 — memory / public / mutation / external_mutation

CLI `zatiti memory remember`; MCP `zatiti_memory_remember`. Submission key: required.

Persist canonical writer intent/command ID before dispatch to exactly one Serenity writer for brain; lost acknowledgement requires reconciliation, not blind retry.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"binding_id":{"type":"string","format":"uuid"},"text":{"type":"string","maxLength":8192},"sources":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"limits":{"$ref":"#/$defs/Limits"}},"required":["scope","binding_id","text","sources","limits"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Job"}},"required":["resource"]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"claims":{"type":"array","items":{"$ref":"#/$defs/Claim"},"maxItems":4096},"obligations":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096}},"required":["claims","obligations"]}
```

### `memory.retract` v1 — memory / public / mutation / external_mutation

CLI `zatiti memory retract`; MCP `zatiti_memory_retract`. Submission key: required.

Immediately exclude revoked claim from future recall, persist downstream promotion reconciliation obligations, explain active-recall removal versus historical erasure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"brain_id":{"type":"string","format":"uuid"},"claim":{"$ref":"#/$defs/Ref"},"reason":{"type":"string","maxLength":8192}},"required":["scope","brain_id","claim","reason"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Job"}},"required":["resource"]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"claims":{"type":"array","items":{"$ref":"#/$defs/Claim"},"maxItems":4096},"obligations":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096},"historical_erasure":{"const":false}},"required":["claims","obligations","historical_erasure"]}
```

### `model.provider.list` v1 — connections / public / query / local

CLI `zatiti model provider list`; MCP `zatiti_model_provider_list`. Submission key: not required at this internal/query/bootstrap boundary.

Return the fixed supported provider presets and endpoints; no network/catalog request, credentials, or account identity. Provider validation is a separate bounded connection.validate job.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/ProviderDescriptor"},"maxItems":3}},"required":["items"]}
```

### `operation.compensation.propose` v1 — effects / public / mutation / local

CLI `zatiti operation compensation propose`; MCP `zatiti_operation_compensation_propose`. Submission key: required.

New linked compensation operation with own review/budget and consequences; original history remains unchanged.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"action":{"$ref":"#/$defs/Action"}},"required":["scope","id","expected_version","action"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Operation"}},"required":["resource"]}
```

### `operation.get` v1 — effects / public / query / local

CLI `zatiti operation get`; MCP `zatiti_operation_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Operation"}},"required":["resource"]}
```

### `operation.list` v1 — effects / public / query / local

CLI `zatiti operation list`; MCP `zatiti_operation_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Operation"},"maxItems":500}},"required":["items"]}
```

### `operation.propose` v1 — effects / public / mutation / local

CLI `zatiti operation propose`; MCP `zatiti_operation_propose`. Submission key: required.

Canonicalize immutable exact action, resolve current prerequisites/policy and create logical operation. No provider call in handler.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"action":{"$ref":"#/$defs/Action"}},"required":["scope","action"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Operation"}},"required":["resource"]}
```

### `operation.reconcile` v1 — effects / public / mutation / external_read

CLI `zatiti operation reconcile`; MCP `zatiti_operation_reconcile`. Submission key: required.

Create separately admitted bounded reconciliation read; retain original unknown outcome and reservation until supported evidence resolves it.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Job"}},"required":["resource"]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Operation"}},"required":["resource"]}
```

### `operation.replacement.propose` v1 — effects / public / mutation / local

CLI `zatiti operation replacement propose`; MCP `zatiti_operation_replacement_propose`. Submission key: required.

New linked replacement operation with own review/budget and consequences; original history remains unchanged.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"action":{"$ref":"#/$defs/Action"}},"required":["scope","id","expected_version","action"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Operation"}},"required":["resource"]}
```

### `organization.archive` v1 — configuration / public / mutation / local

CLI `zatiti organization archive`; MCP `zatiti_organization_archive`. Submission key: required.

Stage a typed archive in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Organization"}},"required":["draft","resource"]}
```

### `organization.chief.replace` v1 — configuration / public / mutation / local

CLI `zatiti organization chief replace`; MCP `zatiti_organization_chief_replace`. Submission key: required.

Stage explicit atomic change. Reject ancestry cycles, recheck inherited authority/budgets and memory bindings. Preserve organization identity, memory, obligations and history. Do not transfer private memory or widen active-work authority.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"chief_id":{"type":"string","format":"uuid"},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","chief_id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"}},"required":["draft"]}
```

### `organization.create` v1 — configuration / public / mutation / local

CLI `zatiti organization create`; MCP `zatiti_organization_create`. Submission key: required.

Stage a typed create in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion. Allocate organization and designated chief IDs together; bootstrap can create a non-executable minimum-permission chief before provider/limits exist. Ordinary worker creation creates no organization.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"definition":{"type":"object","additionalProperties":false,"properties":{"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"limits":{"$ref":"#/$defs/Limits"},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["key","name"]},"draft_id":{"type":"string","format":"uuid"},"chief":{"type":"object","additionalProperties":false,"properties":{"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"purpose":{"type":"string","maxLength":8192},"instructions":{"type":"string","maxLength":8192},"skill_versions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"bindings":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"profile":{"anyOf":[{"$ref":"#/$defs/ExecutionProfile"},{"type":"null"}]},"limits":{"anyOf":[{"$ref":"#/$defs/Limits"},{"type":"null"}]},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["key","name","purpose","instructions","skill_versions","bindings","profile","limits"]}},"required":["scope","definition","chief"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Organization"}},"required":["draft","resource"]}
```

### `organization.export` v1 — configuration / public / mutation / local

CLI `zatiti organization export`; MCP `zatiti_organization_export`. Submission key: required.

Create a bounded artifact job containing zatiti.organization/v1 portable definitions scoped to this resource. Exclude secrets, credentials and runtime history. Opaque credential refs require explicit destination rebinding.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"job":{"$ref":"#/$defs/Job"}},"required":["job"]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Artifact"}},"required":["resource"]}
```

### `organization.get` v1 — configuration / public / query / local

CLI `zatiti organization get`; MCP `zatiti_organization_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Organization"}},"required":["resource"]}
```

### `organization.import` v1 — configuration / public / mutation / local

CLI `zatiti organization import`; MCP `zatiti_organization_import`. Submission key: required.

Validate schema, IDs, references, rebinding and canonical digest, then stage a draft. Cross-installation import never transfers live authority.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"artifact":{"$ref":"#/$defs/ArtifactRef"},"rebindings":{"type":"array","items":{"type":"object","additionalProperties":false,"properties":{"source_ref":{"type":"string","maxLength":8192},"destination_ref":{"type":"string","maxLength":8192}},"required":["source_ref","destination_ref"]},"maxItems":4096},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","artifact","rebindings"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"diagnostics":{"type":"array","items":{"$ref":"#/$defs/Diagnostic"},"maxItems":4096}},"required":["draft","diagnostics"]}
```

### `organization.list` v1 — configuration / public / query / local

CLI `zatiti organization list`; MCP `zatiti_organization_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Organization"},"maxItems":500}},"required":["items"]}
```

### `organization.move` v1 — configuration / public / mutation / local

CLI `zatiti organization move`; MCP `zatiti_organization_move`. Submission key: required.

Stage explicit atomic change. Reject ancestry cycles, recheck inherited authority/budgets and memory bindings. Preserve organization identity, memory, obligations and history. Do not transfer private memory or widen active-work authority.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"parent_id":{"type":"string","format":"uuid"},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","parent_id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"}},"required":["draft"]}
```

### `organization.update` v1 — configuration / public / mutation / local

CLI `zatiti organization update`; MCP `zatiti_organization_update`. Submission key: required.

Stage a typed update in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"definition":{"type":"object","additionalProperties":false,"properties":{"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"chief_id":{"type":"string","format":"uuid"},"parent_id":{"type":"string","format":"uuid"},"limits":{"$ref":"#/$defs/Limits"},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["key","name","chief_id"]},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Organization"}},"required":["draft","resource"]}
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

### `principal.create` v1 — identity / public / mutation / local

CLI `zatiti principal create`; MCP `zatiti_principal_create`. Submission key: required.

Apply create with current authorization, expected version where existing, durable command replay and atomic evidence. Revocation is immediate restriction.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"definition":{"type":"object","additionalProperties":false,"properties":{"kind":{"type":"string","enum":["human","client_agent","worker","service"]},"name":{"type":"string","maxLength":8192},"scope":{"$ref":"#/$defs/Scope"},"revoked":{"type":"boolean"}},"required":["kind","name","scope","revoked"]}},"required":["scope","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Principal"}},"required":["resource"]}
```

### `principal.get` v1 — identity / public / query / local

CLI `zatiti principal get`; MCP `zatiti_principal_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Principal"}},"required":["resource"]}
```

### `principal.list` v1 — identity / public / query / local

CLI `zatiti principal list`; MCP `zatiti_principal_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Principal"},"maxItems":500}},"required":["items"]}
```

### `principal.revoke` v1 — identity / public / mutation / local

CLI `zatiti principal revoke`; MCP `zatiti_principal_revoke`. Submission key: required.

Apply revoke with current authorization, expected version where existing, durable command replay and atomic evidence. Revocation is immediate restriction.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Principal"}},"required":["resource"]}
```

### `principal.update` v1 — identity / public / mutation / local

CLI `zatiti principal update`; MCP `zatiti_principal_update`. Submission key: required.

Apply update with current authorization, expected version where existing, durable command replay and atomic evidence. Revocation is immediate restriction.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"definition":{"type":"object","additionalProperties":false,"properties":{"kind":{"type":"string","enum":["human","client_agent","worker","service"]},"name":{"type":"string","maxLength":8192},"scope":{"$ref":"#/$defs/Scope"},"revoked":{"type":"boolean"}},"required":["kind","name","scope","revoked"]}},"required":["scope","id","expected_version","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Principal"}},"required":["resource"]}
```

### `project.archive` v1 — configuration / public / mutation / local

CLI `zatiti project archive`; MCP `zatiti_project_archive`. Submission key: required.

Stage a typed archive in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Project"}},"required":["draft","resource"]}
```

### `project.create` v1 — configuration / public / mutation / local

CLI `zatiti project create`; MCP `zatiti_project_create`. Submission key: required.

Stage a typed create in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"definition":{"type":"object","additionalProperties":false,"properties":{"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"repositories":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"bindings":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"classification":{"type":"string","enum":["internal","public","restricted"]},"limits":{"$ref":"#/$defs/Limits"},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["organization_id","key","name","repositories","bindings","classification"]},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Project"}},"required":["draft","resource"]}
```

### `project.export` v1 — configuration / public / mutation / local

CLI `zatiti project export`; MCP `zatiti_project_export`. Submission key: required.

Create a bounded artifact job containing zatiti.organization/v1 portable definitions scoped to this resource. Exclude secrets, credentials and runtime history. Opaque credential refs require explicit destination rebinding.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"job":{"$ref":"#/$defs/Job"}},"required":["job"]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Artifact"}},"required":["resource"]}
```

### `project.get` v1 — configuration / public / query / local

CLI `zatiti project get`; MCP `zatiti_project_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Project"}},"required":["resource"]}
```

### `project.import` v1 — configuration / public / mutation / local

CLI `zatiti project import`; MCP `zatiti_project_import`. Submission key: required.

Validate schema, IDs, references, rebinding and canonical digest, then stage a draft. Cross-installation import never transfers live authority.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"artifact":{"$ref":"#/$defs/ArtifactRef"},"rebindings":{"type":"array","items":{"type":"object","additionalProperties":false,"properties":{"source_ref":{"type":"string","maxLength":8192},"destination_ref":{"type":"string","maxLength":8192}},"required":["source_ref","destination_ref"]},"maxItems":4096},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","artifact","rebindings"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"diagnostics":{"type":"array","items":{"$ref":"#/$defs/Diagnostic"},"maxItems":4096}},"required":["draft","diagnostics"]}
```

### `project.list` v1 — configuration / public / query / local

CLI `zatiti project list`; MCP `zatiti_project_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Project"},"maxItems":500}},"required":["items"]}
```

### `project.update` v1 — configuration / public / mutation / local

CLI `zatiti project update`; MCP `zatiti_project_update`. Submission key: required.

Stage a typed update in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"definition":{"type":"object","additionalProperties":false,"properties":{"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"repositories":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"bindings":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"classification":{"type":"string","enum":["internal","public","restricted"]},"limits":{"$ref":"#/$defs/Limits"},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["organization_id","key","name","repositories","bindings","classification"]},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Project"}},"required":["draft","resource"]}
```

### `responsibility.archive` v1 — scheduling / public / mutation / local

CLI `zatiti responsibility archive`; MCP `zatiti_responsibility_archive`. Submission key: required.

Stage a typed archive in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Responsibility"}},"required":["draft","resource"]}
```

### `responsibility.create` v1 — scheduling / public / mutation / local

CLI `zatiti responsibility create`; MCP `zatiti_responsibility_create`. Submission key: required.

Stage a typed create in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"definition":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"worker_id":{"type":"string","format":"uuid"},"outcome":{"type":"string","maxLength":8192},"signals":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"triggers":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"reasoning_policy":{"type":"string","maxLength":8192},"min_interval_seconds":{"type":"integer","minimum":1,"maximum":9223372036854775807},"cycle_limits":{"$ref":"#/$defs/Limits"},"aggregate_limits":{"$ref":"#/$defs/Limits"},"pause_conditions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"escalation_conditions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"acceptance":{"$ref":"#/$defs/Acceptance"},"paused":{"type":"boolean"}},"required":["scope","worker_id","outcome","signals","triggers","reasoning_policy","min_interval_seconds","cycle_limits","aggregate_limits","pause_conditions","escalation_conditions","acceptance","paused"]},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Responsibility"}},"required":["draft","resource"]}
```

### `responsibility.get` v1 — scheduling / public / query / local

CLI `zatiti responsibility get`; MCP `zatiti_responsibility_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Responsibility"}},"required":["resource"]}
```

### `responsibility.list` v1 — scheduling / public / query / local

CLI `zatiti responsibility list`; MCP `zatiti_responsibility_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Responsibility"},"maxItems":500}},"required":["items"]}
```

### `responsibility.pause` v1 — scheduling / public / mutation / local

CLI `zatiti responsibility pause`; MCP `zatiti_responsibility_pause`. Submission key: required.

Pause disables future admissions immediately; resume requires current authority. Responsibility pause does not cancel unrelated tasks. Preserve durable next-wake/occurrence identity.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Disposition"}},"required":["resource"]}
```

### `responsibility.resume` v1 — scheduling / public / mutation / local

CLI `zatiti responsibility resume`; MCP `zatiti_responsibility_resume`. Submission key: required.

Pause disables future admissions immediately; resume requires current authority. Responsibility pause does not cancel unrelated tasks. Preserve durable next-wake/occurrence identity.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Disposition"}},"required":["resource"]}
```

### `responsibility.update` v1 — scheduling / public / mutation / local

CLI `zatiti responsibility update`; MCP `zatiti_responsibility_update`. Submission key: required.

Stage a typed update in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"definition":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"worker_id":{"type":"string","format":"uuid"},"outcome":{"type":"string","maxLength":8192},"signals":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"triggers":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"reasoning_policy":{"type":"string","maxLength":8192},"min_interval_seconds":{"type":"integer","minimum":1,"maximum":9223372036854775807},"cycle_limits":{"$ref":"#/$defs/Limits"},"aggregate_limits":{"$ref":"#/$defs/Limits"},"pause_conditions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"escalation_conditions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"acceptance":{"$ref":"#/$defs/Acceptance"},"paused":{"type":"boolean"}},"required":["scope","worker_id","outcome","signals","triggers","reasoning_policy","min_interval_seconds","cycle_limits","aggregate_limits","pause_conditions","escalation_conditions","acceptance","paused"]},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Responsibility"}},"required":["draft","resource"]}
```

### `review.decide` v1 — reviews / public / mutation / local

CLI `zatiti review decide`; MCP `zatiti_review_decide`. Submission key: required.

Check current principal kind/eligibility, separation, expiry, version and exact digest; immutable decision. Agent claims of human approval are invalid input, not authority.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"action_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"decision":{"type":"string","enum":["approve","reject"]},"reason":{"type":"string","maxLength":8192}},"required":["scope","id","expected_version","action_digest","decision","reason"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Decision"}},"required":["resource"]}
```

### `review.delegate` v1 — reviews / public / mutation / local

CLI `zatiti review delegate`; MCP `zatiti_review_delegate`. Submission key: required.

Delegate only inside existing reviewer authority and eligible kind; human-required and proposer separation remain enforced.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"principal_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","principal_id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Review"}},"required":["resource"]}
```

### `review.get` v1 — reviews / public / query / local

CLI `zatiti review get`; MCP `zatiti_review_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Review"}},"required":["resource"]}
```

### `review.list` v1 — reviews / public / query / local

CLI `zatiti review list`; MCP `zatiti_review_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Review"},"maxItems":500}},"required":["items"]}
```

### `run.cancel` v1 — execution / public / mutation / local

CLI `zatiti run cancel`; MCP `zatiti_run_cancel`. Submission key: required.

Commit cancellation intent and fence governed work as applicable; do not assert external process stopped. Preserve uncertain effects.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"reason":{"type":"string","maxLength":8192}},"required":["scope","id","expected_version","reason"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Disposition"}},"required":["resource"]}
```

### `run.claim` v1 — execution / public / mutation / local

CLI `zatiti run claim`; MCP `zatiti_run_claim`. Submission key: required.

Atomically claim exactly one current attempt and reserve envelope; bind scoped caller, worker, lease and generation. Lost acknowledgement replays original claim. Reject unsupported required executor guarantees.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"run_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"capabilities":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096}},"required":["scope","run_id","worker_id","expected_version","capabilities"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"attempt":{"$ref":"#/$defs/Attempt"},"task":{"$ref":"#/$defs/Task"},"context":{"$ref":"#/$defs/ArtifactRef"}},"required":["attempt","task","context"]}
```

### `run.export` v1 — execution / public / mutation / local

CLI `zatiti run export`; MCP `zatiti_run_export`. Submission key: required.

Export authorized bounded run history artifact separately from portable definitions and encrypted backups.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Job"}},"required":["resource"]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Artifact"}},"required":["resource"]}
```

### `run.get` v1 — execution / public / query / local

CLI `zatiti run get`; MCP `zatiti_run_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Run"}},"required":["resource"]}
```

### `run.list` v1 — execution / public / query / local

CLI `zatiti run list`; MCP `zatiti_run_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Run"},"maxItems":500}},"required":["items"]}
```

### `run.recovery` v1 — execution / public / query / local

CLI `zatiti run recovery`; MCP `zatiti_run_recovery`. Submission key: not required at this internal/query/bootstrap boundary.

Inspect generation, leases, conflicting resources and effect obligations before replacement.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Run"},"obligations":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096}},"required":["resource","obligations"]}
```

### `schedule.archive` v1 — scheduling / public / mutation / local

CLI `zatiti schedule archive`; MCP `zatiti_schedule_archive`. Submission key: required.

Stage a typed archive in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Schedule"}},"required":["draft","resource"]}
```

### `schedule.create` v1 — scheduling / public / mutation / local

CLI `zatiti schedule create`; MCP `zatiti_schedule_create`. Submission key: required.

Stage a typed create in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"definition":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"task_template":{"$ref":"#/$defs/Task"},"timezone":{"type":"string","maxLength":8192},"expression":{"type":"string","maxLength":8192},"misfire":{"type":"string","enum":["coalesce","skip"]},"catch_up_seconds":{"type":"integer","minimum":0,"maximum":9223372036854775807},"paused":{"type":"boolean"}},"required":["scope","task_template","timezone","expression","misfire","catch_up_seconds","paused"]},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Schedule"}},"required":["draft","resource"]}
```

### `schedule.get` v1 — scheduling / public / query / local

CLI `zatiti schedule get`; MCP `zatiti_schedule_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Schedule"}},"required":["resource"]}
```

### `schedule.list` v1 — scheduling / public / query / local

CLI `zatiti schedule list`; MCP `zatiti_schedule_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Schedule"},"maxItems":500}},"required":["items"]}
```

### `schedule.pause` v1 — scheduling / public / mutation / local

CLI `zatiti schedule pause`; MCP `zatiti_schedule_pause`. Submission key: required.

Pause disables future admissions immediately; resume requires current authority. Responsibility pause does not cancel unrelated tasks. Preserve durable next-wake/occurrence identity.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Disposition"}},"required":["resource"]}
```

### `schedule.resume` v1 — scheduling / public / mutation / local

CLI `zatiti schedule resume`; MCP `zatiti_schedule_resume`. Submission key: required.

Pause disables future admissions immediately; resume requires current authority. Responsibility pause does not cancel unrelated tasks. Preserve durable next-wake/occurrence identity.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Disposition"}},"required":["resource"]}
```

### `schedule.update` v1 — scheduling / public / mutation / local

CLI `zatiti schedule update`; MCP `zatiti_schedule_update`. Submission key: required.

Stage a typed update in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"definition":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"task_template":{"$ref":"#/$defs/Task"},"timezone":{"type":"string","maxLength":8192},"expression":{"type":"string","maxLength":8192},"misfire":{"type":"string","enum":["coalesce","skip"]},"catch_up_seconds":{"type":"integer","minimum":0,"maximum":9223372036854775807},"paused":{"type":"boolean"}},"required":["scope","task_template","timezone","expression","misfire","catch_up_seconds","paused"]},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Schedule"}},"required":["draft","resource"]}
```

### `skill.archive` v1 — skills / public / mutation / local

CLI `zatiti skill archive`; MCP `zatiti_skill_archive`. Submission key: required.

Stage a typed archive in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Skill"}},"required":["draft","resource"]}
```

### `skill.evaluate` v1 — skills / public / mutation / local

CLI `zatiti skill evaluate`; MCP `zatiti_skill_evaluate`. Submission key: required.

Create admitted evaluation with sealed fixtures/evaluator/profile versions. Skill or evaluator changes invalidate only dependent qualifications; evaluation grants no authority.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"skill":{"$ref":"#/$defs/Ref"},"acceptance":{"$ref":"#/$defs/Acceptance"},"profile":{"$ref":"#/$defs/ExecutionProfile"},"limits":{"$ref":"#/$defs/Limits"}},"required":["scope","skill","acceptance","profile","limits"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Job"}},"required":["resource"]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"evaluation_id":{"type":"string","format":"uuid"},"passed":{"type":"boolean"},"evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096}},"required":["evaluation_id","passed","evidence"]}
```

### `skill.evaluation.status` v1 — skills / public / query / local

CLI `zatiti skill evaluation status`; MCP `zatiti_skill_evaluation_status`. Submission key: not required at this internal/query/bootstrap boundary.

Inspect actual evaluation disposition and retained evidence.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Job"}},"required":["resource"]}
```

### `skill.get` v1 — skills / public / query / local

CLI `zatiti skill get`; MCP `zatiti_skill_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Skill"}},"required":["resource"]}
```

### `skill.import` v1 — skills / public / mutation / local

CLI `zatiti skill import`; MCP `zatiti_skill_import`. Submission key: required.

Stage immutable content-hashed skill after bounded archive validation. Never execute imported scripts; reject traversal, escaping symlinks, devices, collisions and dependency cycles.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"artifact":{"$ref":"#/$defs/ArtifactRef"},"source":{"type":"string","maxLength":8192},"license":{"type":"string","maxLength":8192},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","artifact","source","license"]}
```
Output data schema:
```json
{"oneOf":[{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Skill"}},"required":["resource"]},{"type":"object","additionalProperties":false,"properties":{"job":{"$ref":"#/$defs/Job"}},"required":["job"]}]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Skill"}},"required":["resource"]}
```

### `skill.list` v1 — skills / public / query / local

CLI `zatiti skill list`; MCP `zatiti_skill_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Skill"},"maxItems":500}},"required":["items"]}
```

### `task.accept` v1 — tasks / public / mutation / local

CLI `zatiti task accept`; MCP `zatiti_task_accept`. Submission key: required.

Eligible manual acceptance only for a manual contract; label manual distinctly. Worker cannot self-accept by report. Independent contracts need verifier observations.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"decision":{"type":"string","enum":["accept","reject"]},"evidence_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"reason":{"type":"string","maxLength":8192}},"required":["scope","id","expected_version","decision","evidence_ids","reason"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Task"}},"required":["resource"]}
```

### `task.assign` v1 — tasks / public / mutation / local

CLI `zatiti task assign`; MCP `zatiti_task_assign`. Submission key: required.

Versioned assignment shared by direct user and chief requests; conflicting concurrent assignment fails. A chief-driven assignment is an ordinary authenticated call under the requesting worker's own scoped actor through the worker operation executor (revision 3), never a domain-internal caller path: this public operation carries no caller allowlist.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"worker_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","worker_id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Task"}},"required":["resource"]}
```

### `task.cancel` v1 — tasks / public / mutation / local

CLI `zatiti task cancel`; MCP `zatiti_task_cancel`. Submission key: required.

Cancel first records intent and blocks new work; terminal only after owned execution stops or is conclusively fenced, retaining unresolved effects. Retry creates new run attempt under unchanged acceptance and requires safe replacement disposition.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"reason":{"type":"string","maxLength":8192}},"required":["scope","id","expected_version","reason"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Task"}},"required":["resource"]}
```

### `task.create` v1 — tasks / public / mutation / local

CLI `zatiti task create`; MCP `zatiti_task_create`. Submission key: required.

Create bounded durable task with pinned outcome/inputs/acceptance/limits. Validate references, finite envelopes and worker home/bindings; ready only if prerequisites hold.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"definition":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"owner_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"outcome":{"type":"string","maxLength":8192},"inputs":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"required_outputs":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"acceptance":{"$ref":"#/$defs/Acceptance"},"limits":{"$ref":"#/$defs/Limits"},"dependencies":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"parent_id":{"type":"string","format":"uuid"},"root_id":{"type":"string","format":"uuid"},"waiting_reason":{"type":"string","maxLength":8192},"cancellation_requested":{"type":"boolean"},"manual_acceptance":{"type":"boolean"}},"required":["scope","owner_id","worker_id","outcome","inputs","required_outputs","acceptance","limits","dependencies"]}},"required":["scope","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Task"}},"required":["resource"]}
```

### `task.delegate` v1 — tasks / public / mutation / local

CLI `zatiti task delegate`; MCP `zatiti_task_delegate`. Submission key: required.

Create linked child with intersected permissions/data/deadline, shared root budgets and finite depth/count/concurrency. Expansion is denied.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"child":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"owner_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"outcome":{"type":"string","maxLength":8192},"inputs":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"required_outputs":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"acceptance":{"$ref":"#/$defs/Acceptance"},"limits":{"$ref":"#/$defs/Limits"},"dependencies":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"parent_id":{"type":"string","format":"uuid"},"root_id":{"type":"string","format":"uuid"},"waiting_reason":{"type":"string","maxLength":8192},"cancellation_requested":{"type":"boolean"},"manual_acceptance":{"type":"boolean"}},"required":["scope","owner_id","worker_id","outcome","inputs","required_outputs","acceptance","limits","dependencies"]}},"required":["scope","id","expected_version","child"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Task"}},"required":["resource"]}
```

### `task.dependencies` v1 — tasks / public / query / local

CLI `zatiti task dependencies`; MCP `zatiti_task_dependencies`. Submission key: not required at this internal/query/bootstrap boundary.

Inspect dependency states without treating worker claims or failed verification as success.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"dependencies":{"type":"array","items":{"$ref":"#/$defs/Task"},"maxItems":4096}},"required":["dependencies"]}
```

### `task.get` v1 — tasks / public / query / local

CLI `zatiti task get`; MCP `zatiti_task_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Task"}},"required":["resource"]}
```

### `task.list` v1 — tasks / public / query / local

CLI `zatiti task list`; MCP `zatiti_task_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Task"},"maxItems":500}},"required":["items"]}
```

### `task.retry` v1 — tasks / public / mutation / local

CLI `zatiti task retry`; MCP `zatiti_task_retry`. Submission key: required.

Cancel first records intent and blocks new work; terminal only after owned execution stops or is conclusively fenced, retaining unresolved effects. Retry creates new run attempt under unchanged acceptance and requires safe replacement disposition.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"reason":{"type":"string","maxLength":8192}},"required":["scope","id","expected_version","reason"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Task"}},"required":["resource"]}
```

### `task.start` v1 — tasks / public / mutation / local

CLI `zatiti task start`; MCP `zatiti_task_start`. Submission key: required.

Transition an eligible draft/ready task to ready and enqueue its run in the same transaction; existing draft-only task.create stays compatible and task.assign continues to only change worker/version without readying. Reject a task with unresolved required dependencies or missing acceptance/verifier prerequisites; replay of the same submission key returns the original disposition.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"task":{"$ref":"#/$defs/Task"},"run":{"$ref":"#/$defs/Run"}},"required":["task","run"]}
```

### `task.update` v1 — tasks / public / mutation / local

CLI `zatiti task update`; MCP `zatiti_task_update`. Submission key: required.

Update only pending draft/ready input under version check; accepted verifier cannot be changed during execution.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"inputs":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"outcome":{"type":"string","maxLength":8192}},"required":["scope","id","expected_version","inputs"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Task"}},"required":["resource"]}
```

### `team.archive` v1 — configuration / public / mutation / local

CLI `zatiti team archive`; MCP `zatiti_team_archive`. Submission key: required.

Stage a typed archive in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Team"}},"required":["draft","resource"]}
```

### `team.create` v1 — configuration / public / mutation / local

CLI `zatiti team create`; MCP `zatiti_team_create`. Submission key: required.

Stage a typed create in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"definition":{"type":"object","additionalProperties":false,"properties":{"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"worker_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["organization_id","key","name","worker_ids"]},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Team"}},"required":["draft","resource"]}
```

### `team.export` v1 — configuration / public / mutation / local

CLI `zatiti team export`; MCP `zatiti_team_export`. Submission key: required.

Create a bounded artifact job containing zatiti.organization/v1 portable definitions scoped to this resource. Exclude secrets, credentials and runtime history. Opaque credential refs require explicit destination rebinding.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"job":{"$ref":"#/$defs/Job"}},"required":["job"]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Artifact"}},"required":["resource"]}
```

### `team.get` v1 — configuration / public / query / local

CLI `zatiti team get`; MCP `zatiti_team_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Team"}},"required":["resource"]}
```

### `team.import` v1 — configuration / public / mutation / local

CLI `zatiti team import`; MCP `zatiti_team_import`. Submission key: required.

Validate schema, IDs, references, rebinding and canonical digest, then stage a draft. Cross-installation import never transfers live authority.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"artifact":{"$ref":"#/$defs/ArtifactRef"},"rebindings":{"type":"array","items":{"type":"object","additionalProperties":false,"properties":{"source_ref":{"type":"string","maxLength":8192},"destination_ref":{"type":"string","maxLength":8192}},"required":["source_ref","destination_ref"]},"maxItems":4096},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","artifact","rebindings"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"diagnostics":{"type":"array","items":{"$ref":"#/$defs/Diagnostic"},"maxItems":4096}},"required":["draft","diagnostics"]}
```

### `team.list` v1 — configuration / public / query / local

CLI `zatiti team list`; MCP `zatiti_team_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Team"},"maxItems":500}},"required":["items"]}
```

### `team.update` v1 — configuration / public / mutation / local

CLI `zatiti team update`; MCP `zatiti_team_update`. Submission key: required.

Stage a typed update in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"definition":{"type":"object","additionalProperties":false,"properties":{"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"worker_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["organization_id","key","name","worker_ids"]},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Team"}},"required":["draft","resource"]}
```

### `tool.bind` v1 — connections / public / mutation / local

CLI `zatiti tool bind`; MCP `zatiti_tool_bind`. Submission key: required.

Stage explicit tool binding change through compiler; reject unknown adapter or unqualified executable binding.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"binding":{"$ref":"#/$defs/Binding"},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","binding"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Draft"}},"required":["resource"]}
```

### `tool.get` v1 — connections / public / query / local

CLI `zatiti tool get`; MCP `zatiti_tool_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Tool"}},"required":["resource"]}
```

### `tool.list` v1 — connections / public / query / local

CLI `zatiti tool list`; MCP `zatiti_tool_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Tool"},"maxItems":500}},"required":["items"]}
```

### `tool.schema` v1 — connections / public / query / local

CLI `zatiti tool schema`; MCP `zatiti_tool_schema`. Submission key: not required at this internal/query/bootstrap boundary.

Inspect exact pinned contract; discovery installs no authority.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"input_schema":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"output_schema":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["input_schema","output_schema"]}
```

### `tool.unbind` v1 — connections / public / mutation / local

CLI `zatiti tool unbind`; MCP `zatiti_tool_unbind`. Submission key: required.

Stage explicit tool binding change through compiler; reject unknown adapter or unqualified executable binding.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"binding":{"$ref":"#/$defs/Binding"},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","binding"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Draft"}},"required":["resource"]}
```

### `usage.get` v1 — accounting / public / query / local

CLI `zatiti usage get`; MCP `zatiti_usage_get`. Submission key: not required at this internal/query/bootstrap boundary.

Return spent/reserved/estimated/unknown separately; advisory/missing price is not zero.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Usage"}},"required":["resource"]}
```

### `worker.archive` v1 — configuration / public / mutation / local

CLI `zatiti worker archive`; MCP `zatiti_worker_archive`. Submission key: required.

Stage a typed archive in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Worker"}},"required":["draft","resource"]}
```

### `worker.create` v1 — configuration / public / mutation / local

CLI `zatiti worker create`; MCP `zatiti_worker_create`. Submission key: required.

Stage a typed create in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"definition":{"type":"object","additionalProperties":false,"properties":{"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"purpose":{"type":"string","maxLength":8192},"instructions":{"type":"string","maxLength":8192},"skill_versions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"bindings":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"profile":{"anyOf":[{"$ref":"#/$defs/ExecutionProfile"},{"type":"null"}]},"limits":{"anyOf":[{"$ref":"#/$defs/Limits"},{"type":"null"}]},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["organization_id","key","name","purpose","instructions","skill_versions","bindings","profile","limits"]},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Worker"}},"required":["draft","resource"]}
```

### `worker.get` v1 — configuration / public / query / local

CLI `zatiti worker get`; MCP `zatiti_worker_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Worker"}},"required":["resource"]}
```

### `worker.list` v1 — configuration / public / query / local

CLI `zatiti worker list`; MCP `zatiti_worker_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Worker"},"maxItems":500}},"required":["items"]}
```

### `worker.move` v1 — configuration / public / mutation / local

CLI `zatiti worker move`; MCP `zatiti_worker_move`. Submission key: required.

Stage explicit atomic change. Reject ancestry cycles, recheck inherited authority/budgets and memory bindings. Preserve organization identity, memory, obligations and history. Do not transfer private memory or widen active-work authority.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"organization_id":{"type":"string","format":"uuid"},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","organization_id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"}},"required":["draft"]}
```

### `worker.pause` v1 — execution / public / mutation / local

CLI `zatiti worker pause`; MCP `zatiti_worker_pause`. Submission key: required.

Pause immediately blocks new worker admissions without inference or spending; resume requires current normal authority. Existing external effects remain visible.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Disposition"}},"required":["resource"]}
```

### `worker.resume` v1 — execution / public / mutation / local

CLI `zatiti worker resume`; MCP `zatiti_worker_resume`. Submission key: required.

Pause immediately blocks new worker admissions without inference or spending; resume requires current normal authority. Existing external effects remain visible.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Disposition"}},"required":["resource"]}
```

### `worker.update` v1 — configuration / public / mutation / local

CLI `zatiti worker update`; MCP `zatiti_worker_update`. Submission key: required.

Stage a typed update in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"definition":{"type":"object","additionalProperties":false,"properties":{"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"purpose":{"type":"string","maxLength":8192},"instructions":{"type":"string","maxLength":8192},"skill_versions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"bindings":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"profile":{"anyOf":[{"$ref":"#/$defs/ExecutionProfile"},{"type":"null"}]},"limits":{"anyOf":[{"$ref":"#/$defs/Limits"},{"type":"null"}]},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["organization_id","key","name","purpose","instructions","skill_versions","bindings","profile","limits"]},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Worker"}},"required":["draft","resource"]}
```

### Local schema definitions

The schemas above resolve exclusively against this embedded `$defs` object. Input objects reject additional properties except explicitly open schema/data fields. Field semantics are completed by the owned requirements and operation descriptions. Output `resource`, `items`, `draft`, `job`, etc. are literal keys. Pagination cursor lives in the common envelope.

```json
{"$defs":{"Acceptance":{"type":"object","additionalProperties":false,"properties":{"verifier_id":{"type":"string","maxLength":8192},"verifier_version":{"type":"string","maxLength":8192},"sealed_inputs":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"expected_observations":{"type":"array","items":{"$ref":"#/$defs/Adapter_ExpectedVerificationObservation"},"maxItems":512},"mode":{"type":"string","enum":["independent","manual"]},"required_child_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"profile":{"$ref":"#/$defs/Adapter_VerificationProfile"}},"required":["verifier_id","verifier_version","sealed_inputs","expected_observations","mode","required_child_ids","profile"]},"Action":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"tool":{"$ref":"#/$defs/Ref"},"connection":{"$ref":"#/$defs/Ref"},"account_identity":{"type":"string","maxLength":8192},"destination":{"type":"string","maxLength":8192},"content":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"not_before":{"type":"string","format":"date-time"},"expires_at":{"type":"string","format":"date-time"},"preconditions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"configuration_revision":{"type":"integer","minimum":1,"maximum":9223372036854775807},"parameters":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"cost_bound":{"$ref":"#/$defs/Money"},"execution_profile":{"$ref":"#/$defs/Ref"}},"required":["scope","tool","connection","account_identity","destination","content","not_before","expires_at","preconditions","configuration_revision","parameters","cost_bound"]},"Adapter_ArtifactRef":{"type":"object","additionalProperties":false,"properties":{"id":{"$ref":"#/$defs/Adapter_ID"},"digest":{"$ref":"#/$defs/Adapter_Digest"}},"required":["id","digest"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_ArtifactVerifierProfile":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.verifier-profile/v1","type":"string"},"kind":{"const":"artifact_contract","type":"string"},"id":{"type":"string","minLength":1,"maxLength":256},"version":{"type":"string","minLength":1,"maxLength":128},"code_digest":{"$ref":"#/$defs/Adapter_Digest"},"supported_checks":{"type":"array","items":{"type":"string","enum":["presence","digest","json_schema"]},"minItems":1,"maxItems":3},"max_bytes":{"type":"integer","minimum":1,"maximum":268435456},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"capability_evidence":{"$ref":"#/$defs/Adapter_CapabilityEvidence"}},"required":["schema","kind","id","version","code_digest","supported_checks","max_bytes","timeout_seconds","capability_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_CapabilityEvidence":{"type":"object","additionalProperties":false,"properties":{"artifact":{"$ref":"#/$defs/Adapter_ArtifactRef"},"adapter_version":{"type":"string","minLength":1,"maxLength":128},"source_revision":{"type":"string","minLength":1,"maxLength":128},"protocol_revision":{"type":"string","minLength":1,"maxLength":128},"profile_digest":{"$ref":"#/$defs/Adapter_Digest"},"qualified_at":{"$ref":"#/$defs/Adapter_UTC"},"capabilities":{"type":"array","items":{"type":"string","minLength":1,"maxLength":128},"minItems":0,"maxItems":128},"limitations":{"type":"array","items":{"type":"string","minLength":1,"maxLength":2048},"minItems":0,"maxItems":128}},"required":["artifact","adapter_version","source_revision","protocol_revision","profile_digest","qualified_at","capabilities","limitations"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_Digest":{"type":"string","pattern":"^[0-9a-f]{64}$","maxLength":64,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_ExpectedVerificationObservation":{"type":"object","additionalProperties":false,"properties":{"check_id":{"type":"string","minLength":1,"maxLength":256},"kind":{"type":"string","enum":["artifact_presence","artifact_digest","json_schema","repository_patch_applies","repository_command"]},"expected":{"type":"string","enum":["pass","fail"]},"artifact_name":{"type":"string","minLength":1,"maxLength":128},"expected_digest":{"$ref":"#/$defs/Adapter_Digest"},"schema":{"$ref":"#/$defs/Adapter_InertSchema"},"command_id":{"type":"string","minLength":1,"maxLength":256},"expected_exit_code":{"type":"integer","minimum":-2147483648,"maximum":2147483647}},"required":["check_id","kind","expected"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_ID":{"type":"string","format":"uuid","maxLength":36,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_InertSchema":{"type":"object","description":"A bounded JSON Schema 2020-12 document. Local references only; validators reject remote references, duplicate keys, excessive depth/node count and unsupported executable behavior. This is schema data, not authority.","maxProperties":256,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_RepositoryVerifierProfile":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.verifier-profile/v1","type":"string"},"kind":{"const":"repository_patch","type":"string"},"id":{"type":"string","minLength":1,"maxLength":256},"version":{"type":"string","minLength":1,"maxLength":128},"code_digest":{"$ref":"#/$defs/Adapter_Digest"},"runner_profile":{"type":"string","minLength":1,"maxLength":256},"command_id":{"type":"string","minLength":1,"maxLength":256},"command_digest":{"$ref":"#/$defs/Adapter_Digest"},"environment_profile":{"type":"string","minLength":1,"maxLength":256},"network":{"type":"string","enum":["disabled","qualified_allowlist"]},"max_output_bytes":{"type":"integer","minimum":1,"maximum":1048576},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"capability_evidence":{"$ref":"#/$defs/Adapter_CapabilityEvidence"}},"required":["schema","kind","id","version","code_digest","runner_profile","command_id","command_digest","environment_profile","network","max_output_bytes","timeout_seconds","capability_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_UTC":{"type":"string","format":"date-time","pattern":"Z$","maxLength":40,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_VerificationProfile":{"oneOf":[{"$ref":"#/$defs/Adapter_ArtifactVerifierProfile"},{"$ref":"#/$defs/Adapter_RepositoryVerifierProfile"}],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Artifact":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"size":{"type":"integer","minimum":0,"maximum":9223372036854775807},"media_type":{"type":"string","maxLength":8192},"classification":{"type":"string","enum":["internal","public","restricted"]},"encrypted":{"type":"boolean"},"state":{"type":"string","enum":["available","fault"]},"created_at":{"type":"string","format":"date-time"},"source_operation_id":{"type":"string","format":"uuid"},"purpose":{"type":"string","maxLength":8192}},"required":["id","version","scope","digest","size","media_type","classification","encrypted","state","created_at"]},"ArtifactRef":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"digest":{"type":"string","pattern":"^[0-9a-f]{64}$"}},"required":["id","digest"]},"Attempt":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"run_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"executor":{"type":"string","enum":["hosted","cooperative"]},"generation":{"type":"integer","minimum":1,"maximum":9223372036854775807},"lease_id":{"type":"string","format":"uuid"},"lease_expires_at":{"type":"string","format":"date-time"},"last_heartbeat":{"type":"string","format":"date-time"},"reservation_id":{"type":"string","format":"uuid"},"state":{"type":"string","enum":["claimed","running","waiting","reported","fenced","stopped","failed"]},"capabilities":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"context_artifact":{"$ref":"#/$defs/ArtifactRef"},"recovery_reason":{"type":"string","maxLength":8192}},"required":["id","version","run_id","worker_id","executor","generation","lease_id","lease_expires_at","last_heartbeat","reservation_id","state","capabilities"]},"Backup":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"artifact":{"$ref":"#/$defs/ArtifactRef"},"installation_id":{"type":"string","format":"uuid"},"created_at":{"type":"string","format":"date-time"},"brain_revisions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"key_prerequisites":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"verified":{"type":"boolean"}},"required":["id","version","artifact","installation_id","created_at","brain_revisions","key_prerequisites","verified"]},"Binding":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"kind":{"type":"string","enum":["tool","skill","connection","worker","repository","reporting","memory"]},"target_id":{"type":"string","format":"uuid"},"permissions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"source_scope":{"$ref":"#/$defs/Scope"},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096}},"required":["id","version","scope","kind","target_id","permissions"]},"CallbackRoute":{"type":"object","additionalProperties":false,"properties":{"kind":{"type":"string","enum":["worker_turn","job","memory","skill","connection"]},"turn_id":{"type":"string","format":"uuid"},"step_index":{"type":"integer","minimum":0,"maximum":9223372036854775807},"job_id":{"type":"string","format":"uuid"}},"required":["kind"]},"Challenge":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"connection_id":{"type":"string","format":"uuid"},"state":{"type":"string","enum":["pending","external_action_required","completed","cancelled","expired","failed"]},"expires_at":{"type":"string","format":"date-time"},"consent_url":{"type":"string","maxLength":8192},"helper_ref":{"type":"string","maxLength":8192},"requirements":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096}},"required":["id","version","connection_id","state","expires_at"]},"Change":{"oneOf":[{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"organization"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Organization"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"team"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Team"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"project"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Project"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"worker"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Worker"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"binding"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Binding"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"execution_profile"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/ExecutionProfile"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"skill"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Skill"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"connection"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Connection"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"policy"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Policy"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"autonomy_rule"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/PromotionRule"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"schedule"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Schedule"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"responsibility"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Responsibility"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"memory_binding"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/MemoryBinding"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"budget"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Limits"}},"required":["kind","action","id","expected_version","definition"]}]},"Claim":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"brain_id":{"type":"string","format":"uuid"},"text":{"type":"string","maxLength":8192},"sources":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"confidence":{"type":"integer","minimum":0,"maximum":1000000},"freshness":{"type":"string","format":"date-time"},"active":{"type":"boolean"},"source_brain_id":{"type":"string","format":"uuid"},"source_claim":{"$ref":"#/$defs/Ref"},"curator_id":{"type":"string","format":"uuid"},"redaction":{"type":"string","maxLength":8192}},"required":["id","version","brain_id","text","sources","confidence","freshness","active"]},"Command":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"principal_id":{"type":"string","format":"uuid"},"operation":{"type":"string","maxLength":8192},"operation_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"submission_key":{"type":"string","maxLength":8192},"request_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"status":{"type":"string","enum":["completed","accepted","failed"]},"data":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"error_code":{"type":"string","maxLength":8192},"result":{"$ref":"#/$defs/Result"}},"required":["id","principal_id","operation","operation_version","submission_key","request_digest","status","data","result"]},"Connection":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"provider":{"type":"string","maxLength":8192},"account_identity":{"type":"string","maxLength":8192},"credential_ref":{"type":"string","maxLength":8192},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"allowed_scopes":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"validation_state":{"type":"string","enum":["unverified","valid","invalid","expired","revoked"]},"validated_at":{"type":"string","format":"date-time"},"valid_until":{"type":"string","format":"date-time"},"hosted_memory_grant":{"$ref":"#/$defs/HostedMemoryGrant"}},"required":["id","version","scope","provider","account_identity","credential_ref","destinations","allowed_scopes","validation_state"]},"Conversation":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"kind":{"type":"string","enum":["direct","group"]},"participant_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"title":{"type":"string","maxLength":8192},"pinned":{"type":"boolean"},"last_meaningful_event":{"type":"string","format":"date-time"},"caller_unread_count":{"type":"integer","minimum":0,"maximum":9223372036854775807},"caller_last_read_marker":{"type":"string","format":"date-time"}},"required":["id","version","scope","kind","participant_ids","title","pinned"]},"Credential":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"principal_id":{"type":"string","format":"uuid"},"store_ref":{"type":"string","maxLength":8192},"revoked":{"type":"boolean"},"expires_at":{"type":"string","format":"date-time"}},"required":["id","version","principal_id","store_ref","revoked"]},"Decision":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"review_id":{"type":"string","format":"uuid"},"review_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"action_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"reviewer_id":{"type":"string","format":"uuid"},"decision":{"type":"string","enum":["approve","reject"]},"at":{"type":"string","format":"date-time"},"reason":{"type":"string","maxLength":8192}},"required":["id","review_id","review_version","action_digest","reviewer_id","decision","at","reason"]},"DecisionRequirement":{"type":"object","additionalProperties":false,"properties":{"action_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"human_required":{"type":"boolean"},"eligible_principals":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"expires_at":{"type":"string","format":"date-time"},"separate_proposer":{"type":"boolean"}},"required":["action_digest","human_required","eligible_principals","expires_at","separate_proposer"]},"Descriptor":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","maxLength":8192},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"owner":{"type":"string","maxLength":8192},"input_schema":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"output_schema":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"effect":{"type":"string","maxLength":8192},"scope_requirements":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"cli":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"mcp":{"type":"string","maxLength":8192},"submission_key":{"type":"boolean"},"expected_version":{"type":"boolean"},"completion_schema":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","owner","input_schema","output_schema","effect","scope_requirements","cli","mcp","submission_key","expected_version"]},"Diagnostic":{"type":"object","additionalProperties":false,"properties":{"path":{"type":"string","maxLength":8192},"code":{"type":"string","maxLength":8192},"message":{"type":"string","maxLength":8192},"severity":{"type":"string","enum":["error","warning","info"]}},"required":["path","code","message","severity"]},"Disposition":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"state":{"type":"string","maxLength":8192},"job":{"$ref":"#/$defs/Job"},"operation":{"$ref":"#/$defs/Operation"},"requirements":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096}},"required":["id","version","state"]},"Draft":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"base_revision":{"type":"integer","minimum":1,"maximum":9223372036854775807},"changes":{"type":"array","items":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"maxItems":4096},"diagnostics":{"type":"array","items":{"$ref":"#/$defs/Diagnostic"},"maxItems":4096}},"required":["id","version","base_revision","changes","diagnostics"]},"Event":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"sequence":{"type":"integer","minimum":1,"maximum":9223372036854775807},"at":{"type":"string","format":"date-time"},"scope":{"$ref":"#/$defs/Scope"},"kind":{"type":"string","maxLength":8192},"resource_id":{"type":"string","format":"uuid"},"resource_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"data":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","sequence","at","scope","kind","resource_id","resource_version","data"]},"ExecutionProfile":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"executor":{"type":"string","enum":["hosted","cooperative"]},"model":{"type":"string","maxLength":8192},"connection_id":{"type":"string","format":"uuid"},"provider_destination":{"type":"string","maxLength":8192},"capabilities":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"cost_bound":{"$ref":"#/$defs/Money"},"classification":{"type":"string","enum":["internal","public","restricted"]},"context_capture":{"type":"string","enum":["complete","partial","advisory"]},"adapter_profile":{"$ref":"#/$defs/ResponsesProfile"},"connection_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["id","version","executor","model","connection_id","provider_destination","capabilities","cost_bound","classification","context_capture"]},"ExecutionProfileCandidate":{"type":"object","additionalProperties":false,"properties":{"executor":{"type":"string","enum":["hosted","cooperative"]},"model":{"type":"string","maxLength":8192},"connection_id":{"type":"string","format":"uuid"},"provider_destination":{"type":"string","maxLength":8192},"capabilities":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"cost_bound":{"$ref":"#/$defs/Money"},"classification":{"type":"string","enum":["internal","public","restricted"]},"context_capture":{"type":"string","enum":["complete","partial","advisory"]},"adapter_profile":{"$ref":"#/$defs/ResponsesProfileDraft"},"connection_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["executor","model","connection_id","provider_destination","capabilities","cost_bound","classification","context_capture","adapter_profile","connection_version"]},"Fault":{"type":"object","additionalProperties":false,"properties":{"code":{"type":"string","maxLength":8192},"message":{"type":"string","maxLength":8192},"retryable":{"type":"boolean"},"details":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["code","message","retryable"]},"Grant":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"principal_id":{"type":"string","format":"uuid"},"scope":{"$ref":"#/$defs/Scope"},"capabilities":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"denied":{"type":"boolean"},"expires_at":{"type":"string","format":"date-time"},"parent_grant_id":{"type":"string","format":"uuid"}},"required":["id","version","principal_id","scope","capabilities","destinations","denied"]},"HostedMemoryGrant":{"type":"object","additionalProperties":false,"properties":{"issuer":{"type":"string","maxLength":8192},"resource":{"type":"string","maxLength":8192},"account_id":{"type":"string","maxLength":8192},"project_id":{"type":"string","maxLength":8192},"scopes":{"type":"array","items":{"type":"string","enum":["memory:read","memory:write"]},"maxItems":2},"verified_at":{"type":"string","format":"date-time"}},"required":["issuer","resource","account_id","project_id","scopes","verified_at"]},"Job":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"kind":{"type":"string","maxLength":8192},"state":{"type":"string","enum":["pending","running","succeeded","failed","outcome_unknown","cancelled"]},"requirements":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096},"result_artifact":{"$ref":"#/$defs/ArtifactRef"},"operation_id":{"type":"string","format":"uuid"},"owner":{"type":"string","maxLength":8192},"operation":{"type":"string","maxLength":8192},"result":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","kind","state","requirements","owner","operation"]},"Limits":{"type":"object","additionalProperties":false,"properties":{"currency":{"type":"string","pattern":"^[A-Z]{3}$"},"spend_micro_units":{"type":"integer","minimum":0,"maximum":9223372036854775807},"concurrency":{"type":"integer","minimum":1,"maximum":9223372036854775807},"model_steps":{"type":"integer","minimum":1,"maximum":9223372036854775807},"child_count":{"type":"integer","minimum":0,"maximum":9223372036854775807},"delegation_depth":{"type":"integer","minimum":0,"maximum":9223372036854775807},"attempt_seconds":{"type":"integer","minimum":1,"maximum":9223372036854775807},"root_deadline":{"type":"string","format":"date-time"}},"required":["currency","spend_micro_units","concurrency","model_steps","child_count","delegation_depth","attempt_seconds","root_deadline"]},"MemoryBinding":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"brain_id":{"type":"string","format":"uuid"},"permissions":{"type":"array","items":{"type":"string","enum":["read","write","curate","promote","retract"]},"maxItems":4096},"classification":{"type":"string","enum":["internal","public","restricted"]}},"required":["id","version","scope","brain_id","permissions","classification"]},"Message":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"sender_id":{"type":"string","format":"uuid"},"recipient_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"scope":{"$ref":"#/$defs/Scope"},"task_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"body":{"type":"string","maxLength":8192},"attachments":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"state":{"type":"string","enum":["submitted","admitted","acknowledged"]},"created_at":{"type":"string","format":"date-time"},"conversation_id":{"type":"string","format":"uuid"}},"required":["id","version","sender_id","recipient_ids","scope","task_ids","body","attachments","state","created_at"]},"Money":{"type":"object","additionalProperties":false,"properties":{"currency":{"type":"string","pattern":"^[A-Z]{3}$"},"micro_units":{"type":"integer","minimum":0,"maximum":9223372036854775807}},"required":["currency","micro_units"]},"Operation":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"action":{"$ref":"#/$defs/Action"},"action_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"state":{"type":"string","enum":["prepared","awaiting_review","ready","executing","awaiting_confirmation","outcome_unknown","succeeded","failed","denied","expired","cancelled"]},"attempt_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"linked_operation_id":{"type":"string","format":"uuid"},"relationship":{"type":"string","enum":["retry","reconciliation","compensation","replacement"]},"attempts":{"type":"array","items":{"$ref":"#/$defs/OperationAttempt"},"maxItems":4096},"callback_route":{"$ref":"#/$defs/CallbackRoute"}},"required":["id","version","action","action_digest","state","attempt_ids"]},"OperationAttempt":{"type":"object","additionalProperties":false,"properties":{"attempt_id":{"type":"string","format":"uuid"},"generation":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["attempt_id","generation"]},"Organization":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"chief_id":{"type":"string","format":"uuid"},"parent_id":{"type":"string","format":"uuid"},"limits":{"$ref":"#/$defs/Limits"},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","key","name","chief_id"]},"Plan":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"draft_id":{"type":"string","format":"uuid"},"base_revision":{"type":"integer","minimum":1,"maximum":9223372036854775807},"candidate_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"changes":{"type":"array","items":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"maxItems":4096},"dependencies":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"compiler_version":{"type":"string","maxLength":8192},"schema_version":{"type":"string","maxLength":8192},"authority_requirements":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096},"decisions":{"type":"array","items":{"$ref":"#/$defs/DecisionRequirement"},"maxItems":4096},"diagnostics":{"type":"array","items":{"$ref":"#/$defs/Diagnostic"},"maxItems":4096},"requirements":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096}},"required":["id","version","draft_id","base_revision","candidate_digest","changes","dependencies","compiler_version","schema_version","authority_requirements","decisions","diagnostics","requirements"]},"Policy":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"rules":{"type":"array","items":{"$ref":"#/$defs/Rule"},"maxItems":4096},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","scope","rules"]},"Principal":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"kind":{"type":"string","enum":["human","client_agent","worker","service"]},"name":{"type":"string","maxLength":8192},"scope":{"$ref":"#/$defs/Scope"},"revoked":{"type":"boolean"}},"required":["id","version","kind","name","scope","revoked"]},"Project":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"repositories":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"bindings":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"classification":{"type":"string","enum":["internal","public","restricted"]},"limits":{"$ref":"#/$defs/Limits"},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","organization_id","key","name","repositories","bindings","classification"]},"PromotionRule":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"capability":{"type":"string","maxLength":8192},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"required_evidence":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"minimum_successes":{"type":"integer","minimum":1,"maximum":9223372036854775807},"evidence_window_seconds":{"type":"integer","minimum":1,"maximum":9223372036854775807},"disqualifying_events":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"ceiling_grant_id":{"type":"string","format":"uuid"},"human_required_preserved":{"type":"boolean"}},"required":["id","version","scope","capability","destinations","required_evidence","minimum_successes","evidence_window_seconds","disqualifying_events","ceiling_grant_id","human_required_preserved"]},"ProviderDescriptor":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","enum":["openai","openrouter","experiential"]},"display_name":{"type":"string","maxLength":8192},"default_endpoint":{"type":"string","maxLength":8192},"session_mode":{"type":"string","enum":["provider_conversation","stateless"]},"credential_setup":{"type":"string","enum":["api_key"]}},"required":["id","display_name","default_endpoint","session_mode","credential_setup"],"allOf":[{"if":{"properties":{"id":{"const":"openai"}},"required":["id"]},"then":{"properties":{"default_endpoint":{"const":"https://api.openai.com/v1/responses"},"session_mode":{"const":"provider_conversation"}}}},{"if":{"properties":{"id":{"const":"openrouter"}},"required":["id"]},"then":{"properties":{"default_endpoint":{"const":"https://openrouter.ai/api/v1/responses"},"session_mode":{"const":"stateless"}}}},{"if":{"properties":{"id":{"const":"experiential"}},"required":["id"]},"then":{"properties":{"default_endpoint":{"const":"https://api.experientiallabs.ai/v1/responses"},"session_mode":{"const":"stateless"}}}}]},"Qualification":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"worker_id":{"type":"string","format":"uuid"},"capability":{"type":"string","maxLength":8192},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"rule":{"$ref":"#/$defs/Ref"},"model":{"type":"string","maxLength":8192},"tool_versions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"skill_versions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"evidence_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"window_start":{"type":"string","format":"date-time"},"window_end":{"type":"string","format":"date-time"},"state":{"type":"string","enum":["proposed","qualified","rejected","restricted","expired"]},"explanation":{"type":"string","maxLength":8192}},"required":["id","version","worker_id","capability","destinations","rule","model","tool_versions","skill_versions","evidence_ids","window_start","window_end","state","explanation"]},"QualifiedExecutionProfile":{"type":"object","additionalProperties":false,"properties":{"executor":{"type":"string","enum":["hosted","cooperative"]},"model":{"type":"string","maxLength":8192},"connection_id":{"type":"string","format":"uuid"},"provider_destination":{"type":"string","maxLength":8192},"capabilities":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"cost_bound":{"$ref":"#/$defs/Money"},"classification":{"type":"string","enum":["internal","public","restricted"]},"context_capture":{"type":"string","enum":["complete","partial","advisory"]},"adapter_profile":{"$ref":"#/$defs/ResponsesProfile"},"connection_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["executor","model","connection_id","provider_destination","capabilities","cost_bound","classification","context_capture","adapter_profile","connection_version"]},"Ref":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["id","version"]},"Requirement":{"type":"object","additionalProperties":false,"properties":{"code":{"type":"string","maxLength":8192},"message":{"type":"string","maxLength":8192},"resource_id":{"type":"string","format":"uuid"},"challenge_id":{"type":"string","format":"uuid"}},"required":["code","message"]},"ResponsesCapabilityEvidence":{"type":"object","additionalProperties":false,"properties":{"artifact":{"$ref":"#/$defs/ArtifactRef"},"adapter_version":{"type":"string","minLength":1,"maxLength":128},"source_revision":{"type":"string","minLength":1,"maxLength":128},"protocol_revision":{"type":"string","minLength":1,"maxLength":128},"profile_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"qualified_at":{"type":"string","format":"date-time"},"capabilities":{"type":"array","items":{"type":"string","minLength":1,"maxLength":128},"minItems":0,"maxItems":128},"limitations":{"type":"array","items":{"type":"string","minLength":1,"maxLength":2048},"minItems":0,"maxItems":128}},"required":["artifact","adapter_version","source_revision","protocol_revision","profile_digest","qualified_at","capabilities","limitations"]},"ResponsesEnforcement":{"type":"object","additionalProperties":false,"properties":{"cost":{"type":"string","enum":["enforced","advisory","unsupported"]},"disclosure":{"type":"string","enum":["enforced","advisory","unsupported"]},"maximum_cost":{"$ref":"#/$defs/Money"},"provider_destinations":{"type":"array","items":{"type":"string","format":"uri","pattern":"^https://","maxLength":2048},"minItems":0,"maxItems":64}},"required":["cost","disclosure","maximum_cost","provider_destinations"]},"ResponsesProfile":{"oneOf":[{"$ref":"#/$defs/ResponsesProfileV1"},{"$ref":"#/$defs/ResponsesProfileV2"}]},"ResponsesProfileDraft":{"oneOf":[{"$ref":"#/$defs/ResponsesProfileDraftV1"},{"$ref":"#/$defs/ResponsesProfileDraftV2"}]},"ResponsesProfileDraftV1":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.responses/v1","type":"string"},"endpoint":{"type":"string","format":"uri","pattern":"^https://","maxLength":2048},"model":{"type":"string","minLength":1,"maxLength":256},"connection_id":{"type":"string","format":"uuid"},"max_input_tokens":{"type":"integer","minimum":1,"maximum":10000000},"max_output_tokens":{"type":"integer","minimum":1,"maximum":1000000},"max_response_bytes":{"type":"integer","minimum":1,"maximum":268435456},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"currency":{"type":"string","pattern":"^[A-Z]{3}$"},"input_rate":{"$ref":"#/$defs/ResponsesRate"},"output_rate":{"$ref":"#/$defs/ResponsesRate"},"enforcement":{"$ref":"#/$defs/ResponsesEnforcement"}},"required":["schema","endpoint","model","connection_id","max_input_tokens","max_output_tokens","max_response_bytes","timeout_seconds","currency","input_rate","output_rate","enforcement"]},"ResponsesProfileDraftV2":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.responses/v2","type":"string"},"endpoint":{"type":"string","format":"uri","pattern":"^https://","maxLength":2048},"model":{"type":"string","minLength":1,"maxLength":256},"connection_id":{"type":"string","format":"uuid"},"max_input_tokens":{"type":"integer","minimum":1,"maximum":10000000},"max_output_tokens":{"type":"integer","minimum":1,"maximum":1000000},"max_response_bytes":{"type":"integer","minimum":1,"maximum":268435456},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"currency":{"type":"string","pattern":"^[A-Z]{3}$"},"input_rate":{"$ref":"#/$defs/ResponsesRate"},"output_rate":{"$ref":"#/$defs/ResponsesRate"},"enforcement":{"$ref":"#/$defs/ResponsesEnforcement"},"provider":{"type":"string","enum":["openai","openrouter","experiential"]},"session_mode":{"type":"string","enum":["provider_conversation","stateless"]},"routing":{"oneOf":[{"type":"object","additionalProperties":false,"properties":{},"required":[]},{"$ref":"#/$defs/ResponsesRoutingOpenRouter"},{"$ref":"#/$defs/ResponsesRoutingExperiential"}]}},"required":["schema","endpoint","model","connection_id","max_input_tokens","max_output_tokens","max_response_bytes","timeout_seconds","currency","input_rate","output_rate","enforcement","provider","session_mode","routing"],"allOf":[{"if":{"properties":{"provider":{"const":"openai"}},"required":["provider"]},"then":{"properties":{"session_mode":{"const":"provider_conversation"},"endpoint":{"const":"https://api.openai.com/v1/responses"},"routing":{"type":"object","additionalProperties":false,"properties":{},"required":[]}}}},{"if":{"properties":{"provider":{"const":"openrouter"}},"required":["provider"]},"then":{"properties":{"session_mode":{"const":"stateless"},"endpoint":{"const":"https://openrouter.ai/api/v1/responses"},"routing":{"$ref":"#/$defs/ResponsesRoutingOpenRouter"}}}},{"if":{"properties":{"provider":{"const":"experiential"}},"required":["provider"]},"then":{"properties":{"session_mode":{"const":"stateless"},"endpoint":{"const":"https://api.experientiallabs.ai/v1/responses"},"routing":{"$ref":"#/$defs/ResponsesRoutingExperiential"}}}}]},"ResponsesProfileV1":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.responses/v1","type":"string"},"endpoint":{"type":"string","format":"uri","pattern":"^https://","maxLength":2048},"model":{"type":"string","minLength":1,"maxLength":256},"connection_id":{"type":"string","format":"uuid"},"max_input_tokens":{"type":"integer","minimum":1,"maximum":10000000},"max_output_tokens":{"type":"integer","minimum":1,"maximum":1000000},"max_response_bytes":{"type":"integer","minimum":1,"maximum":268435456},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"currency":{"type":"string","pattern":"^[A-Z]{3}$"},"input_rate":{"$ref":"#/$defs/ResponsesRate"},"output_rate":{"$ref":"#/$defs/ResponsesRate"},"enforcement":{"$ref":"#/$defs/ResponsesEnforcement"},"capability_evidence":{"$ref":"#/$defs/ResponsesCapabilityEvidence"}},"required":["schema","endpoint","model","connection_id","max_input_tokens","max_output_tokens","max_response_bytes","timeout_seconds","currency","input_rate","output_rate","enforcement","capability_evidence"]},"ResponsesProfileV2":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.responses/v2","type":"string"},"endpoint":{"type":"string","format":"uri","pattern":"^https://","maxLength":2048},"model":{"type":"string","minLength":1,"maxLength":256},"connection_id":{"type":"string","format":"uuid"},"max_input_tokens":{"type":"integer","minimum":1,"maximum":10000000},"max_output_tokens":{"type":"integer","minimum":1,"maximum":1000000},"max_response_bytes":{"type":"integer","minimum":1,"maximum":268435456},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"currency":{"type":"string","pattern":"^[A-Z]{3}$"},"input_rate":{"$ref":"#/$defs/ResponsesRate"},"output_rate":{"$ref":"#/$defs/ResponsesRate"},"enforcement":{"$ref":"#/$defs/ResponsesEnforcement"},"capability_evidence":{"$ref":"#/$defs/ResponsesCapabilityEvidence"},"provider":{"type":"string","enum":["openai","openrouter","experiential"]},"session_mode":{"type":"string","enum":["provider_conversation","stateless"]},"routing":{"oneOf":[{"type":"object","additionalProperties":false,"properties":{},"required":[]},{"$ref":"#/$defs/ResponsesRoutingOpenRouter"},{"$ref":"#/$defs/ResponsesRoutingExperiential"}]}},"required":["schema","endpoint","model","connection_id","max_input_tokens","max_output_tokens","max_response_bytes","timeout_seconds","currency","input_rate","output_rate","enforcement","capability_evidence","provider","session_mode","routing"],"allOf":[{"if":{"properties":{"provider":{"const":"openai"}},"required":["provider"]},"then":{"properties":{"session_mode":{"const":"provider_conversation"},"endpoint":{"const":"https://api.openai.com/v1/responses"},"routing":{"type":"object","additionalProperties":false,"properties":{},"required":[]}}}},{"if":{"properties":{"provider":{"const":"openrouter"}},"required":["provider"]},"then":{"properties":{"session_mode":{"const":"stateless"},"endpoint":{"const":"https://openrouter.ai/api/v1/responses"},"routing":{"$ref":"#/$defs/ResponsesRoutingOpenRouter"}}}},{"if":{"properties":{"provider":{"const":"experiential"}},"required":["provider"]},"then":{"properties":{"session_mode":{"const":"stateless"},"endpoint":{"const":"https://api.experientiallabs.ai/v1/responses"},"routing":{"$ref":"#/$defs/ResponsesRoutingExperiential"}}}}]},"ResponsesRate":{"type":"object","additionalProperties":false,"properties":{"numerator_micro_units":{"type":"integer","minimum":0,"maximum":9223372036854775807},"denominator_units":{"type":"integer","minimum":1,"maximum":9223372036854775807},"unit":{"type":"string","enum":["input_token","output_token","request","byte","second"]}},"required":["numerator_micro_units","denominator_units","unit"]},"ResponsesRoutingExperiential":{"type":"object","additionalProperties":false,"properties":{"gateway":{"type":"object","additionalProperties":false,"properties":{"retry":{"type":"object","additionalProperties":false,"properties":{"max_attempts_per_route":{"const":1,"type":"integer"},"max_total_attempts":{"const":1,"type":"integer"}},"required":["max_attempts_per_route","max_total_attempts"]},"backoff":{"type":"object","additionalProperties":false,"properties":{"type":{"const":"none","type":"string"}},"required":["type"]},"routing":{"type":"object","additionalProperties":false,"properties":{"allow_fallbacks":{"const":false,"type":"boolean"}},"required":["allow_fallbacks"]}},"required":["retry","backoff","routing"]},"route_id":{"type":"string","maxLength":8192},"privacy":{"type":"array","items":{"type":"string","enum":["no_training","data_policy","zero_retention"]},"maxItems":8}},"required":["gateway"]},"ResponsesRoutingOpenRouter":{"type":"object","additionalProperties":false,"properties":{"only":{"type":"array","items":{"type":"string","maxLength":8192},"minItems":1,"maxItems":16},"allow_fallbacks":{"const":false,"type":"boolean"},"require_parameters":{"const":true,"type":"boolean"},"price_ceiling":{"type":"object","additionalProperties":false,"properties":{"currency":{"const":"USD","type":"string"},"input_per_million":{"type":"string","pattern":"^(0|[1-9][0-9]*)(\\.[0-9]{1,18})?$","maxLength":64},"output_per_million":{"type":"string","pattern":"^(0|[1-9][0-9]*)(\\.[0-9]{1,18})?$","maxLength":64}},"required":["currency","input_per_million","output_per_million"]},"privacy":{"type":"array","items":{"type":"string","enum":["no_training","data_policy","zero_retention"]},"maxItems":8}},"required":["only","allow_fallbacks","require_parameters"]},"Responsibility":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"worker_id":{"type":"string","format":"uuid"},"outcome":{"type":"string","maxLength":8192},"signals":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"triggers":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"reasoning_policy":{"type":"string","maxLength":8192},"min_interval_seconds":{"type":"integer","minimum":1,"maximum":9223372036854775807},"cycle_limits":{"$ref":"#/$defs/Limits"},"aggregate_limits":{"$ref":"#/$defs/Limits"},"pause_conditions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"escalation_conditions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"acceptance":{"$ref":"#/$defs/Acceptance"},"paused":{"type":"boolean"},"next_wake":{"type":"string","format":"date-time"},"last_cycle_id":{"type":"string","format":"uuid"}},"required":["id","version","scope","worker_id","outcome","signals","triggers","reasoning_policy","min_interval_seconds","cycle_limits","aggregate_limits","pause_conditions","escalation_conditions","acceptance","paused"]},"Result":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.result/v1"},"command_id":{"type":"string","format":"uuid"},"status":{"type":"string","enum":["completed","accepted","failed"]},"data":{"anyOf":[{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},{"type":"null"}]},"error":{"anyOf":[{"$ref":"#/$defs/Fault"},{"type":"null"}]},"next_cursor":{"anyOf":[{"type":"string","maxLength":8192},{"type":"null"}]}},"required":["schema","command_id","status","data","error","next_cursor"]},"Review":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"action_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"preview":{"$ref":"#/$defs/Action"},"requirement":{"$ref":"#/$defs/DecisionRequirement"},"proposer_id":{"type":"string","format":"uuid"},"state":{"type":"string","enum":["pending","approved","rejected","expired","invalidated"]},"decision_id":{"type":"string","format":"uuid"}},"required":["id","version","scope","action_digest","preview","requirement","proposer_id","state"]},"Revision":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"plan_id":{"type":"string","format":"uuid"},"candidate_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"activated_at":{"type":"string","format":"date-time"}},"required":["id","version","plan_id","candidate_digest","activated_at"]},"Rule":{"type":"object","additionalProperties":false,"properties":{"capability":{"type":"string","maxLength":8192},"effect":{"type":"string","enum":["local","disclosure","external_read","external_mutation"]},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"decision":{"type":"string","enum":["allow","deny","review"]},"human_required":{"type":"boolean"},"conditions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["capability","effect","destinations","decision","human_required","conditions"]},"Run":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"task_id":{"type":"string","format":"uuid"},"configuration_revision":{"type":"integer","minimum":1,"maximum":9223372036854775807},"input_versions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"state":{"type":"string","enum":["ready","running","waiting","verifying","succeeded","failed","cancelled"]},"attempt_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096}},"required":["id","version","task_id","configuration_revision","input_versions","state","attempt_ids"]},"Schedule":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"task_template":{"$ref":"#/$defs/Task"},"timezone":{"type":"string","maxLength":8192},"expression":{"type":"string","maxLength":8192},"misfire":{"type":"string","enum":["coalesce","skip"]},"catch_up_seconds":{"type":"integer","minimum":0,"maximum":9223372036854775807},"paused":{"type":"boolean"},"next_wake":{"type":"string","format":"date-time"}},"required":["id","version","scope","task_template","timezone","expression","misfire","catch_up_seconds","paused"]},"Scope":{"type":"object","additionalProperties":false,"properties":{"installation_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"project_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"}},"required":["installation_id"]},"Skill":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"name":{"type":"string","maxLength":8192},"instruction_artifact":{"$ref":"#/$defs/ArtifactRef"},"content_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"input_schema":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"output_schema":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"requirements":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"dependencies":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"source":{"type":"string","maxLength":8192},"license":{"type":"string","maxLength":8192},"evaluation_refs":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"diagnostics":{"type":"array","items":{"$ref":"#/$defs/Diagnostic"},"maxItems":4096}},"required":["id","version","name","instruction_artifact","content_digest","input_schema","output_schema","requirements","dependencies","source","license","evaluation_refs","diagnostics"]},"Status":{"type":"object","additionalProperties":false,"properties":{"installation_id":{"type":"string","format":"uuid"},"generation":{"type":"integer","minimum":1,"maximum":9223372036854775807},"paused":{"type":"boolean"},"maintenance":{"type":"boolean"},"initialized":{"type":"boolean"},"requirements":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"runtime_ready":{"type":"boolean"}},"required":["installation_id","generation","paused","maintenance","initialized","requirements","version"]},"Task":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"owner_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"outcome":{"type":"string","maxLength":8192},"inputs":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"required_outputs":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"acceptance":{"$ref":"#/$defs/Acceptance"},"limits":{"$ref":"#/$defs/Limits"},"dependencies":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"state":{"type":"string","enum":["draft","ready","running","waiting","verifying","succeeded","failed","cancelled"]},"parent_id":{"type":"string","format":"uuid"},"root_id":{"type":"string","format":"uuid"},"waiting_reason":{"type":"string","maxLength":8192},"cancellation_requested":{"type":"boolean"},"manual_acceptance":{"type":"boolean"}},"required":["id","version","scope","owner_id","worker_id","outcome","inputs","required_outputs","acceptance","limits","dependencies","state"]},"Team":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"worker_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","organization_id","key","name","worker_ids"]},"Tool":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"name":{"type":"string","maxLength":8192},"input_schema":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"output_schema":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"effect":{"type":"string","enum":["local","disclosure","external_read","external_mutation"]},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"credential_kind":{"type":"string","maxLength":8192},"cost_bound":{"$ref":"#/$defs/Money"},"timeout_seconds":{"type":"integer","minimum":1,"maximum":9223372036854775807},"idempotency":{"type":"string","enum":["none","qualified_key","authoritative_nonexecution"]},"key_retention_seconds":{"type":"integer","minimum":0,"maximum":9223372036854775807},"confirmation":{"type":"string","enum":["synchronous","asynchronous","advisory"]},"reconciliation":{"type":"string","maxLength":8192},"adapter":{"type":"string","maxLength":8192}},"required":["id","version","name","input_schema","output_schema","effect","destinations","credential_kind","cost_bound","timeout_seconds","idempotency","key_retention_seconds","confirmation","reconciliation","adapter"]},"Upload":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"expected_size":{"type":"integer","minimum":0,"maximum":9223372036854775807},"expected_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"received_size":{"type":"integer","minimum":0,"maximum":9223372036854775807},"expires_at":{"type":"string","format":"date-time"},"state":{"type":"string","enum":["open","finished","cancelled","expired"]}},"required":["id","version","scope","expected_size","expected_digest","received_size","expires_at","state"]},"Usage":{"type":"object","additionalProperties":false,"properties":{"currency":{"type":"string","pattern":"^[A-Z]{3}$"},"spent":{"type":"integer","minimum":0,"maximum":9223372036854775807},"reserved":{"type":"integer","minimum":0,"maximum":9223372036854775807},"estimated":{"type":"integer","minimum":0,"maximum":9223372036854775807},"unknown":{"type":"integer","minimum":0,"maximum":9223372036854775807},"advisory":{"type":"boolean"}},"required":["currency","spent","reserved","estimated","unknown","advisory"]},"VerifierDescriptor":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","maxLength":8192},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"kind":{"type":"string","enum":["artifact","repository"]},"classification":{"type":"string","enum":["internal","public","restricted"]}},"required":["id","version","kind","classification"]},"Worker":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"purpose":{"type":"string","maxLength":8192},"instructions":{"type":"string","maxLength":8192},"skill_versions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"bindings":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"profile":{"anyOf":[{"$ref":"#/$defs/ExecutionProfile"},{"type":"null"}]},"limits":{"anyOf":[{"$ref":"#/$defs/Limits"},{"type":"null"}]},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","organization_id","key","name","purpose","instructions","skill_versions","bindings","profile","limits"]}}}
```

## Local adapter, context, verifier and backup payloads

These local schemas freeze the handoff between execution, adapters and artifact publication. They do not assert upstream compatibility.

These are exact Zatiti-side contracts, not representations of upstream APIs. Adapter implementation translates to a pinned, separately qualified public upstream protocol; unsupported semantics fail capability_unsupported or preserve outcome_unknown.
To validate a named schema, place definitions under the root $defs and reference the selected name. The catalog container uses definitions for packaging only; every embedded #/$defs/Name resolves against that assembled validation document. JSON Schema dialect is 2020-12.
Validate strict objects, formats, explicit field limits, duplicate-key rejection and exact integer bounds. Runtime must also enforce total JSON/artifact byte limits, at most 64 JSON levels and 100000 schema/input nodes; schema documents and dynamic tool arguments cannot escape the qualified local validation dialect.
Opaque references, URLs, headers and diagnostics must never contain secret bytes. Profile configuration is trusted local configuration; public operation arguments cannot install profiles, select a more privileged credential, open an arbitrary server path or replace capability evidence.
Before dispatch, profile capability evidence must match the canonical profile digest computed with the capability_evidence field omitted; self-referential capability evidence and qualification records do not grant authority. The binding/effect owner independently checks current account, scope, destinations, classification, prices and qualified capability.
PhysicalCallEvidence records exactly one physical network request per claimed attempt. request_context is an ArtifactLocator that identifies the pre-dispatch persisted request. An adapter receives no IDSource and BlobStore mints no artifact identity, so an adapter never fabricates an ArtifactRef and never reuses capability_evidence or any other unrelated artifact as a stand-in. Before writing any request bytes, the adapter stages the exact bounded request record (method, destination, permitted headers and body as sent, with credentials and every secret excluded) through BlobStore.Stage and returns request_context as kind staged with that staging_ref and digest, plus exactly one matching StagedOutput with purpose context in the same observation. If staging fails, the adapter sends nothing. Kind artifact is allowed at adapter return only to pass through, unchanged, a published ArtifactRef received in Dispatch.Action whose bytes already are the complete request record, for example ResponsesParameters.context_artifact when the translated provider request adds nothing model-visible beyond the persisted Action. ModelOutput.request_context equals physical_call.request_context. Reconcile is its own physical call with its own request_context. A not_sent, failed or unknown attempt keeps its staged request context. No adapter adds preflight, redirect, polling, SDK retry or model tool execution inside that request; each such external action requires a separate governed attempt.
StagedOutput is an IO handoff, not a published ArtifactRef. Adapter stages bytes and returns staging_ref/digest metadata; controller uses the artifacts owner to publish metadata and replace staged locators with real ArtifactRefs before delivering normalized observations to execution/memory/other domains. output_artifacts may be empty at adapter return and contains the resulting published refs in recorded domain evidence. ArtifactLocator staged references, including physical_call.request_context and ModelOutput.request_context, must match exactly one StagedOutput in the same observation. The controller is responsible for publishing the staged request context with every other StagedOutput and for replacing the staged locator with kind artifact and the published ArtifactRef in the normalized evidence it delivers; the raw recorded adapter observation is not rewritten. Owners receiving normalized evidence accept only kind artifact for request_context and treat a staged locator there as an unpublished obligation, not success. HTTPReadEvidence therefore allows two staged outputs and two output artifacts: the request context and at most one response body. Failed publication remains a visible obligation/fault.
ContextArtifact.messages and each parts array preserve the exact model-visible semantic order. Tool definitions, memory excerpts and injected messages must all appear in the persisted request context before dispatch. Context source artifacts and compaction lineage do not replace retaining the actual selected text. Complete capture is required for owned hosted loops; external capture limitations remain explicit.
Model tool proposals are observations. tool ID/version must match the pinned context tool closure; operation ID/version must be the qualified mapping for that tool. Validate input against that exact schema and apply current authority/review/budgets before a separate operation. Never execute a model proposal directly in an adapter.
Responses input_rate.unit must equal input_token and output_rate.unit output_token; profile currencies and all reported/reserved usage must agree. Round admission reservations upward using checked integer rational arithmetic. Missing or unbounded required charges refuse hard-cap mode. A null/absent upstream usage report maps to unknown/advisory billing, never free success.
GitHubParameters is discriminated by kind. read_repository requires branch for ref, sha for commit/tree/blob, and pull_request_number for pull_request; irrelevant resource selectors are rejected. Relative repository paths and branch syntax receive Git-aware validation. Profile allowed_actions must be separately qualified against real API preconditions and downstream automation.
GitHub push_commit publishes an already prepared exact commit SHA in one qualified physical request. Creating remote blobs, trees or commits is not hidden inside Invoke: it needs an explicit separately admitted decomposition and qualification before enabling that route. Preflight evidence cannot substitute for atomic provider preconditions where the action requires protection against a race; refuse unsupported guarantees.
GitHub open_pull_request and merge_pull_request are consequential effects bound to exact account/repository/head/base/content/timing/automation constraints. A successful HTTP response or accepted request is not independently assumed confirmed completion; response interpretation follows the qualified contract. Eventual not-found never proves nonexecution.
HTTPRead v1 max_redirects is exactly zero. Return redirect_location as evidence/prerequisite and require a separately authorized read for the new URL. Validate DNS results and bind validated dial addresses; reject local/private/link-local/metadata targets unless explicitly permitted by an installation-authorized destination binding. Header values reject controls and duplicate header names; Authorization, Cookie and arbitrary raw credential input are excluded.
Serenity profile endpoints/root_ref/writer_owner are opaque locally provisioned mappings, not model-selectable arbitrary filesystem locations. They identify real pinned public protocol/read-facade support. Zatiti never imports upstream internal packages or invents public endpoints from these local operation names.
Serenity recall/remember/promote may incur disclosure or charges. All nested upstream provider destinations and spend must fit enforcement; unsupported enforceable bounds disable the mode or require explicitly permitted advisory use. Promote resolves and authorizes source content before preparing the destination write and retains the source evidence; it does not perform an unaccounted extra source read inside Invoke.
Serenity adapter_command_id is the durable pre-dispatch command identity. Its existence does not prove upstream idempotency: reconcile through qualified status semantics, retaining unknown after lost acknowledgement if authoritative lookup is unavailable. One writer owner per brain. Retract removes active recall and leaves historical_erasure false; promotion correction lineage remains a durable memory-owner obligation.
Verification profiles are installation-owned and pinned before task execution. command_id, runner_profile and environment_profile resolve only to prequalified local verifiers; no request supplies executable paths or argv. ArtifactVerifierProfile runs presence/digest/schema checks; RepositoryVerifierProfile runs the pinned controlled patch/check profile outside Unit. A worker cannot alter profile bytes, accepted inputs or result authority.
VerificationResult.independent=true is necessary structure, not trust evidence: only the trusted runner may submit that result through its internal admission identity. Match task/attempt, acceptance digest, verifier identity/version/digest and request artifact before accepting. Passed requires all sealed expected checks observed as required; unavailable, interrupted, worker assertion and process exit zero alone cannot succeed a task. Manual acceptance uses the separate task operation and never fabricates an independent result.
BackupManifest is inside an authenticated encrypted bundle; it never includes decrypting master keys. archive_entry values are normalized relative entries with no traversal, absolute paths, symlinks, duplicate/case collisions or devices. Verify database/artifact/brain digests and qualified revision exports before marking backup verified. Large manifests stream through bounded artifact IO, not public JSON request bodies.
Restore starts paused under exclusive maintenance, validates installation binding and key prerequisites, and retains current recovery overlay before rewinding. Merge revocations, durable command identities, claimed/unknown effects, reservations and memory obligations monotonically via their owners; fail closed on conflict and retain the overlay. Overlay record artifacts must be included/pinned and encrypted. No database/brain rewind is represented as undoing an external effect.
Revision 3 splits ResponsesParameters/ResponsesEvidence by the kind discriminator into prepare_session (creates the provider conversation, returns session_handle, no model-visible content and no context_artifact) and model_step (names the persisted session_handle and sends context_artifact/max_output_tokens/tool_contract_versions). Each is exactly one physical call per Invoke; the adapter never performs both requests inside one Invoke. A prepare_session timeout after the request was sent leaves the session unknown, not absent, and must not be resent; a model_step timeout after bytes were sent is outcome_unknown and an empty item list is equally consistent with never ran and still running, so nonexecution is never assumed and no automatic fresh call follows.

```json
{"$defs":{"ID":{"type":"string","format":"uuid","maxLength":36,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Digest":{"type":"string","pattern":"^[0-9a-f]{64}$","maxLength":64,"$schema":"https://json-schema.org/draft/2020-12/schema"},"UTC":{"type":"string","format":"date-time","pattern":"Z$","maxLength":40,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Version":{"type":"integer","minimum":1,"maximum":9223372036854775807,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Currency":{"type":"string","pattern":"^[A-Z]{3}$","maxLength":3,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Classification":{"type":"string","enum":["public","internal","restricted"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"HTTPSURL":{"type":"string","format":"uri","pattern":"^https://","maxLength":4096,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Scope":{"type":"object","additionalProperties":false,"properties":{"installation_id":{"$ref":"#/$defs/ID"},"organization_id":{"$ref":"#/$defs/ID"},"project_id":{"$ref":"#/$defs/ID"},"worker_id":{"$ref":"#/$defs/ID"},"task_id":{"$ref":"#/$defs/ID"}},"required":["installation_id"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"VersionRef":{"type":"object","additionalProperties":false,"properties":{"id":{"$ref":"#/$defs/ID"},"version":{"$ref":"#/$defs/Version"}},"required":["id","version"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ArtifactRef":{"type":"object","additionalProperties":false,"properties":{"id":{"$ref":"#/$defs/ID"},"digest":{"$ref":"#/$defs/Digest"}},"required":["id","digest"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Money":{"type":"object","additionalProperties":false,"properties":{"currency":{"$ref":"#/$defs/Currency"},"micro_units":{"type":"integer","minimum":0,"maximum":9223372036854775807}},"required":["currency","micro_units"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Usage":{"type":"object","additionalProperties":false,"properties":{"currency":{"$ref":"#/$defs/Currency"},"spent":{"type":"integer","minimum":0,"maximum":9223372036854775807},"reserved":{"type":"integer","minimum":0,"maximum":9223372036854775807},"estimated":{"type":"integer","minimum":0,"maximum":9223372036854775807},"unknown":{"type":"integer","minimum":0,"maximum":9223372036854775807},"advisory":{"type":"boolean"}},"required":["currency","spent","reserved","estimated","unknown","advisory"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"RationalRate":{"type":"object","additionalProperties":false,"properties":{"numerator_micro_units":{"type":"integer","minimum":0,"maximum":9223372036854775807},"denominator_units":{"type":"integer","minimum":1,"maximum":9223372036854775807},"unit":{"type":"string","enum":["input_token","output_token","request","byte","second"]}},"required":["numerator_micro_units","denominator_units","unit"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"CapabilityEvidence":{"type":"object","additionalProperties":false,"properties":{"artifact":{"$ref":"#/$defs/ArtifactRef"},"adapter_version":{"type":"string","minLength":1,"maxLength":128},"source_revision":{"type":"string","minLength":1,"maxLength":128},"protocol_revision":{"type":"string","minLength":1,"maxLength":128},"profile_digest":{"$ref":"#/$defs/Digest"},"qualified_at":{"$ref":"#/$defs/UTC"},"capabilities":{"type":"array","items":{"type":"string","minLength":1,"maxLength":128},"minItems":0,"maxItems":128},"limitations":{"type":"array","items":{"type":"string","minLength":1,"maxLength":2048},"minItems":0,"maxItems":128}},"required":["artifact","adapter_version","source_revision","protocol_revision","profile_digest","qualified_at","capabilities","limitations"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"BoundEnforcement":{"type":"object","additionalProperties":false,"properties":{"cost":{"type":"string","enum":["enforced","advisory","unsupported"]},"disclosure":{"type":"string","enum":["enforced","advisory","unsupported"]},"maximum_cost":{"$ref":"#/$defs/Money"},"provider_destinations":{"type":"array","items":{"$ref":"#/$defs/HTTPSURL"},"minItems":0,"maxItems":64},"classifications":{"type":"array","items":{"$ref":"#/$defs/Classification"},"minItems":1,"maxItems":3},"evidence":{"$ref":"#/$defs/CapabilityEvidence"}},"required":["cost","disclosure","maximum_cost","provider_destinations","classifications","evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"StagedOutput":{"type":"object","additionalProperties":false,"properties":{"staging_ref":{"type":"string","minLength":1,"maxLength":256},"digest":{"$ref":"#/$defs/Digest"},"size":{"type":"integer","minimum":0,"maximum":268435456},"media_type":{"type":"string","minLength":1,"maxLength":256},"classification":{"$ref":"#/$defs/Classification"},"purpose":{"type":"string","enum":["provider_response","model_text","context","tool_result","repository","public_source","memory","verification","backup","export"]}},"required":["staging_ref","digest","size","media_type","classification","purpose"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ArtifactLocator":{"oneOf":[{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"artifact","type":"string"},"artifact":{"$ref":"#/$defs/ArtifactRef"}},"required":["kind","artifact"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"staged","type":"string"},"staging_ref":{"type":"string","minLength":1,"maxLength":256},"digest":{"$ref":"#/$defs/Digest"}},"required":["kind","staging_ref","digest"]}],"$schema":"https://json-schema.org/draft/2020-12/schema"},"PhysicalCallEvidence":{"type":"object","additionalProperties":false,"properties":{"operation_id":{"$ref":"#/$defs/ID"},"attempt_id":{"$ref":"#/$defs/ID"},"account_identity":{"type":"string","minLength":1,"maxLength":512},"requested_destination":{"type":"string","minLength":1,"maxLength":4096},"resolved_destination":{"type":"string","minLength":1,"maxLength":4096},"profile_digest":{"$ref":"#/$defs/Digest"},"capability_evidence":{"$ref":"#/$defs/ArtifactRef"},"started_at":{"$ref":"#/$defs/UTC"},"finished_at":{"$ref":"#/$defs/UTC"},"request_context":{"$ref":"#/$defs/ArtifactLocator"},"request_sent":{"type":"string","enum":["no","yes","unknown"]},"confirmation":{"type":"string","enum":["authoritative_success","authoritative_failure","provider_accepted","authoritative_nonexecution","unknown"]},"http_status":{"type":"integer","minimum":100,"maximum":599},"provider_reference":{"type":"string","minLength":0,"maxLength":1024},"error_code":{"type":"string","minLength":0,"maxLength":128},"error_message":{"type":"string","minLength":0,"maxLength":2048}},"required":["operation_id","attempt_id","account_identity","requested_destination","resolved_destination","profile_digest","started_at","finished_at","request_context","request_sent","confirmation"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ProviderUsage":{"type":"object","additionalProperties":false,"properties":{"accounting":{"$ref":"#/$defs/Usage"},"billing":{"type":"string","enum":["observed","bounded_estimate","unknown","advisory","no_charge"]},"input_tokens":{"type":"integer","minimum":0,"maximum":9223372036854775807},"output_tokens":{"type":"integer","minimum":0,"maximum":9223372036854775807},"input_rate":{"$ref":"#/$defs/RationalRate"},"output_rate":{"$ref":"#/$defs/RationalRate"},"provider_usage_reference":{"type":"string","minLength":0,"maxLength":1024},"requested_model":{"type":"string","minLength":1,"maxLength":256},"served_model":{"type":"string","minLength":1,"maxLength":256},"serving_provider":{"type":"string","enum":["openai","openrouter","experiential"]},"provider_request_id":{"type":"string","minLength":1,"maxLength":256},"source_cost_decimal":{"type":"string","pattern":"^(0|[1-9][0-9]*)(\\.[0-9]{1,18})?$","maxLength":64},"source_cost_currency":{"$ref":"#/$defs/Currency"},"source_cost_kind":{"type":"string","enum":["provider_billed","gateway_platform","upstream_estimate"]}},"required":["accounting","billing"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"InertSchema":{"type":"object","description":"A bounded JSON Schema 2020-12 document. Local references only; validators reject remote references, duplicate keys, excessive depth/node count and unsupported executable behavior. This is schema data, not authority.","maxProperties":256,"$schema":"https://json-schema.org/draft/2020-12/schema"},"ToolArguments":{"type":"object","description":"Strictly validate against the exact pinned tool input schema before use. Unknown tool fields fail. This open container is never an executable grant.","maxProperties":256,"$schema":"https://json-schema.org/draft/2020-12/schema"},"ModelToolProposal":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","minLength":1,"maxLength":256},"tool":{"$ref":"#/$defs/VersionRef"},"operation_id":{"type":"string","minLength":1,"maxLength":128},"operation_version":{"$ref":"#/$defs/Version"},"input":{"$ref":"#/$defs/ToolArguments"},"source_context":{"$ref":"#/$defs/ArtifactRef"},"explanation":{"type":"string","minLength":0,"maxLength":8192}},"required":["id","tool","operation_id","operation_version","input","source_context"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ContextText":{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"text","type":"string"},"text":{"type":"string","minLength":0,"maxLength":262144}},"required":["kind","text"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ContextArtifactPart":{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"artifact","type":"string"},"artifact":{"$ref":"#/$defs/ArtifactRef"},"media_type":{"type":"string","minLength":1,"maxLength":256},"classification":{"$ref":"#/$defs/Classification"},"label":{"type":"string","minLength":0,"maxLength":256}},"required":["kind","artifact","media_type","classification"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ContextToolCall":{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"tool_call","type":"string"},"proposal":{"$ref":"#/$defs/ModelToolProposal"}},"required":["kind","proposal"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ContextToolResult":{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"tool_result","type":"string"},"proposal_id":{"type":"string","minLength":1,"maxLength":256},"operation_id":{"$ref":"#/$defs/ID"},"status":{"type":"string","enum":["completed","accepted","failed"]},"artifact":{"$ref":"#/$defs/ArtifactRef"},"error_code":{"type":"string","minLength":0,"maxLength":128}},"required":["kind","proposal_id","operation_id","status","artifact"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ContextMemoryExcerpt":{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"memory_excerpt","type":"string"},"brain_id":{"$ref":"#/$defs/ID"},"claim":{"$ref":"#/$defs/VersionRef"},"text":{"type":"string","minLength":0,"maxLength":262144},"sources":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":256},"confidence":{"type":"integer","minimum":0,"maximum":1000000},"freshness":{"$ref":"#/$defs/UTC"},"scope":{"$ref":"#/$defs/Scope"},"selected_context":{"$ref":"#/$defs/ArtifactRef"}},"required":["kind","brain_id","claim","text","sources","confidence","freshness","scope","selected_context"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ContextPart":{"oneOf":[{"$ref":"#/$defs/ContextText"},{"$ref":"#/$defs/ContextArtifactPart"},{"$ref":"#/$defs/ContextToolCall"},{"$ref":"#/$defs/ContextToolResult"},{"$ref":"#/$defs/ContextMemoryExcerpt"}],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ContextMessage":{"type":"object","additionalProperties":false,"properties":{"id":{"$ref":"#/$defs/ID"},"role":{"type":"string","enum":["system","developer","user","assistant","tool"]},"origin":{"type":"string","enum":["effective_instruction","user_message","model_output","tool_result","memory_recall","agent_message","compaction"]},"parts":{"type":"array","items":{"$ref":"#/$defs/ContextPart"},"minItems":1,"maxItems":512},"source_artifacts":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":512},"sender_id":{"$ref":"#/$defs/ID"},"message_id":{"$ref":"#/$defs/ID"},"created_at":{"$ref":"#/$defs/UTC"}},"required":["id","role","origin","parts","source_artifacts"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ContextToolDefinition":{"type":"object","additionalProperties":false,"properties":{"tool":{"$ref":"#/$defs/VersionRef"},"name":{"type":"string","minLength":1,"maxLength":128},"description":{"type":"string","minLength":0,"maxLength":16384},"input_schema":{"$ref":"#/$defs/InertSchema"},"output_schema":{"$ref":"#/$defs/InertSchema"},"effect":{"type":"string","enum":["local","disclosure","external_read","external_mutation"]},"destinations":{"type":"array","items":{"type":"string","minLength":1,"maxLength":4096},"minItems":0,"maxItems":64},"binding_id":{"$ref":"#/$defs/ID"},"schema_digest":{"$ref":"#/$defs/Digest"}},"required":["tool","name","description","input_schema","output_schema","effect","destinations","binding_id","schema_digest"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ContextArtifact":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.context/v1","type":"string"},"attempt_id":{"$ref":"#/$defs/ID"},"scope":{"$ref":"#/$defs/Scope"},"configuration_revision":{"$ref":"#/$defs/Version"},"worker":{"$ref":"#/$defs/VersionRef"},"execution_profile":{"$ref":"#/$defs/VersionRef"},"skill_versions":{"type":"array","items":{"$ref":"#/$defs/VersionRef"},"minItems":0,"maxItems":256},"messages":{"type":"array","items":{"$ref":"#/$defs/ContextMessage"},"minItems":0,"maxItems":4096},"tools":{"type":"array","items":{"$ref":"#/$defs/ContextToolDefinition"},"minItems":0,"maxItems":256},"source_artifacts":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":4096},"capture":{"type":"string","enum":["complete","partial","advisory"]},"created_at":{"$ref":"#/$defs/UTC"},"compaction":{"type":"object","additionalProperties":false,"properties":{"source_contexts":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":1,"maxItems":256},"compactor_profile":{"$ref":"#/$defs/VersionRef"},"summary_artifact":{"$ref":"#/$defs/ArtifactRef"}},"required":["source_contexts","compactor_profile","summary_artifact"]}},"required":["schema","attempt_id","scope","configuration_revision","worker","execution_profile","skill_versions","messages","tools","source_artifacts","capture","created_at"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ModelOutput":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.model-output/v1","type":"string"},"response_id":{"type":"string","minLength":0,"maxLength":1024},"request_context":{"$ref":"#/$defs/ArtifactLocator"},"finish_reason":{"type":"string","enum":["completed","tool_calls","length_limit","refused","interrupted","failed","unknown"]},"text_outputs":{"type":"array","items":{"$ref":"#/$defs/ArtifactLocator"},"minItems":0,"maxItems":256},"tool_proposals":{"type":"array","items":{"$ref":"#/$defs/ModelToolProposal"},"minItems":0,"maxItems":256},"usage":{"$ref":"#/$defs/ProviderUsage"},"refusal":{"type":"string","minLength":0,"maxLength":8192},"continuation_reference":{"type":"string","minLength":0,"maxLength":1024}},"required":["schema","response_id","request_context","finish_reason","text_outputs","tool_proposals","usage"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ResponsesProfile":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.responses/v1","type":"string"},"endpoint":{"$ref":"#/$defs/HTTPSURL"},"model":{"type":"string","minLength":1,"maxLength":256},"connection_id":{"$ref":"#/$defs/ID"},"max_input_tokens":{"type":"integer","minimum":1,"maximum":10000000},"max_output_tokens":{"type":"integer","minimum":1,"maximum":1000000},"max_response_bytes":{"type":"integer","minimum":1,"maximum":268435456},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"currency":{"$ref":"#/$defs/Currency"},"input_rate":{"$ref":"#/$defs/RationalRate"},"output_rate":{"$ref":"#/$defs/RationalRate"},"enforcement":{"$ref":"#/$defs/BoundEnforcement"},"capability_evidence":{"$ref":"#/$defs/CapabilityEvidence"}},"required":["schema","endpoint","model","connection_id","max_input_tokens","max_output_tokens","max_response_bytes","timeout_seconds","currency","input_rate","output_rate","enforcement","capability_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ResponsesParameters":{"oneOf":[{"$ref":"#/$defs/ResponsesPrepareSessionParameters"},{"$ref":"#/$defs/ResponsesModelStepParameters"}],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ResponsesEvidence":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.responses.evidence/v1","type":"string"},"physical_call":{"$ref":"#/$defs/PhysicalCallEvidence"},"session_handle":{"type":"string","minLength":1,"maxLength":1024},"response_id":{"type":"string","minLength":0,"maxLength":1024},"output":{"$ref":"#/$defs/ModelOutput"},"staged_outputs":{"type":"array","items":{"$ref":"#/$defs/StagedOutput"},"minItems":0,"maxItems":256},"output_artifacts":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":256}},"required":["schema","physical_call","session_handle","staged_outputs"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Repository":{"type":"object","additionalProperties":false,"properties":{"owner":{"type":"string","pattern":"^[A-Za-z0-9][A-Za-z0-9-]{0,99}$"},"name":{"type":"string","pattern":"^[A-Za-z0-9_.-]{1,100}$"}},"required":["owner","name"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"GitSHA":{"type":"string","pattern":"^([0-9a-f]{40}|[0-9a-f]{64})$","maxLength":64,"$schema":"https://json-schema.org/draft/2020-12/schema"},"GitBranch":{"type":"string","minLength":1,"maxLength":1024,"description":"Validate as a safe Git branch/ref name; reject traversal, control characters and invalid Git ref syntax.","$schema":"https://json-schema.org/draft/2020-12/schema"},"RepositoryPath":{"type":"string","minLength":1,"maxLength":4096,"description":"Normalized relative repository path; reject absolute paths, dot/dot-dot components, NUL, backslash ambiguity and path escapes.","$schema":"https://json-schema.org/draft/2020-12/schema"},"AutomationConstraints":{"type":"object","additionalProperties":false,"properties":{"allowed_workflows":{"type":"array","items":{"type":"string","minLength":1,"maxLength":512},"minItems":0,"maxItems":256},"allowed_deployment_environments":{"type":"array","items":{"type":"string","minLength":1,"maxLength":256},"minItems":0,"maxItems":128},"allow_external_notifications":{"type":"boolean"},"allow_automatic_merge":{"type":"boolean"},"unknown_automation":{"type":"string","enum":["deny","review"]},"evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":256}},"required":["allowed_workflows","allowed_deployment_environments","allow_external_notifications","allow_automatic_merge","unknown_automation","evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"GitHubIdempotencyProfile":{"type":"object","additionalProperties":false,"properties":{"mode":{"type":"string","enum":["none","qualified_key","authoritative_nonexecution"]},"retention_seconds":{"type":"integer","minimum":0,"maximum":9223372036854775807},"equivalence_fields":{"type":"array","items":{"type":"string","minLength":1,"maxLength":128},"minItems":0,"maxItems":64},"evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":64}},"required":["mode","retention_seconds","equivalence_fields","evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"GitHubProfile":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.github/v1","type":"string"},"api_base":{"$ref":"#/$defs/HTTPSURL"},"allowed_repositories":{"type":"array","items":{"$ref":"#/$defs/Repository"},"minItems":1,"maxItems":256},"allowed_actions":{"type":"array","items":{"type":"string","enum":["read_repository","create_branch","push_commit","open_pull_request","merge_pull_request"]},"minItems":1,"maxItems":5},"max_response_bytes":{"type":"integer","minimum":1,"maximum":268435456},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"idempotency_profile":{"$ref":"#/$defs/GitHubIdempotencyProfile"},"automation_constraints":{"$ref":"#/$defs/AutomationConstraints"},"capability_evidence":{"$ref":"#/$defs/CapabilityEvidence"}},"required":["schema","api_base","allowed_repositories","allowed_actions","max_response_bytes","timeout_seconds","idempotency_profile","automation_constraints","capability_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"GitHubReadRepository":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.github.action/v1","type":"string"},"repository":{"$ref":"#/$defs/Repository"},"kind":{"const":"read_repository","type":"string"},"resource":{"type":"string","enum":["metadata","ref","commit","tree","blob","pull_request"]},"branch":{"$ref":"#/$defs/GitBranch"},"sha":{"$ref":"#/$defs/GitSHA"},"path":{"$ref":"#/$defs/RepositoryPath"},"pull_request_number":{"type":"integer","minimum":1,"maximum":9223372036854775807},"preflight_for_operation_id":{"$ref":"#/$defs/ID"}},"required":["schema","repository","kind","resource"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"GitHubCreateBranch":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.github.action/v1","type":"string"},"repository":{"$ref":"#/$defs/Repository"},"kind":{"const":"create_branch","type":"string"},"branch":{"$ref":"#/$defs/GitBranch"},"base_sha":{"$ref":"#/$defs/GitSHA"},"expected_absent":{"const":"required","type":"string"},"automation_constraints":{"$ref":"#/$defs/AutomationConstraints"},"preflight_evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":1,"maxItems":64}},"required":["schema","repository","kind","branch","base_sha","expected_absent","automation_constraints","preflight_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"GitHubPushCommit":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.github.action/v1","type":"string"},"repository":{"$ref":"#/$defs/Repository"},"kind":{"const":"push_commit","type":"string"},"branch":{"$ref":"#/$defs/GitBranch"},"expected_head_sha":{"$ref":"#/$defs/GitSHA"},"prepared_commit_sha":{"$ref":"#/$defs/GitSHA"},"base_sha":{"$ref":"#/$defs/GitSHA"},"patch":{"$ref":"#/$defs/ArtifactRef"},"content_artifacts":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":4096},"force":{"const":false,"type":"boolean"},"automation_constraints":{"$ref":"#/$defs/AutomationConstraints"},"preflight_evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":1,"maxItems":64}},"required":["schema","repository","kind","branch","expected_head_sha","prepared_commit_sha","base_sha","patch","content_artifacts","force","automation_constraints","preflight_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"GitHubOpenPullRequest":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.github.action/v1","type":"string"},"repository":{"$ref":"#/$defs/Repository"},"kind":{"const":"open_pull_request","type":"string"},"head_branch":{"$ref":"#/$defs/GitBranch"},"head_sha":{"$ref":"#/$defs/GitSHA"},"base_branch":{"$ref":"#/$defs/GitBranch"},"base_sha":{"$ref":"#/$defs/GitSHA"},"title":{"$ref":"#/$defs/ArtifactRef"},"body":{"$ref":"#/$defs/ArtifactRef"},"patch":{"$ref":"#/$defs/ArtifactRef"},"draft":{"type":"boolean"},"automation_constraints":{"$ref":"#/$defs/AutomationConstraints"},"preflight_evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":1,"maxItems":64}},"required":["schema","repository","kind","head_branch","head_sha","base_branch","base_sha","title","body","patch","draft","automation_constraints","preflight_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"GitHubMergePullRequest":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.github.action/v1","type":"string"},"repository":{"$ref":"#/$defs/Repository"},"kind":{"const":"merge_pull_request","type":"string"},"pull_request_number":{"type":"integer","minimum":1,"maximum":9223372036854775807},"expected_head_sha":{"$ref":"#/$defs/GitSHA"},"expected_base_sha":{"$ref":"#/$defs/GitSHA"},"merge_method":{"type":"string","enum":["merge","squash","rebase"]},"commit_title":{"$ref":"#/$defs/ArtifactRef"},"commit_body":{"$ref":"#/$defs/ArtifactRef"},"automation_constraints":{"$ref":"#/$defs/AutomationConstraints"},"preflight_evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":1,"maxItems":64}},"required":["schema","repository","kind","pull_request_number","expected_head_sha","expected_base_sha","merge_method","commit_title","commit_body","automation_constraints","preflight_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"GitHubParameters":{"oneOf":[{"$ref":"#/$defs/GitHubReadRepository"},{"$ref":"#/$defs/GitHubCreateBranch"},{"$ref":"#/$defs/GitHubPushCommit"},{"$ref":"#/$defs/GitHubOpenPullRequest"},{"$ref":"#/$defs/GitHubMergePullRequest"}],"$schema":"https://json-schema.org/draft/2020-12/schema"},"GitHubEvidence":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.github.evidence/v1","type":"string"},"physical_call":{"$ref":"#/$defs/PhysicalCallEvidence"},"kind":{"type":"string","enum":["read_repository","create_branch","push_commit","open_pull_request","merge_pull_request"]},"repository":{"$ref":"#/$defs/Repository"},"usage":{"$ref":"#/$defs/ProviderUsage"},"staged_outputs":{"type":"array","items":{"$ref":"#/$defs/StagedOutput"},"minItems":0,"maxItems":256},"output_artifacts":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":256},"branch":{"$ref":"#/$defs/GitBranch"},"observed_head_sha":{"$ref":"#/$defs/GitSHA"},"observed_base_sha":{"$ref":"#/$defs/GitSHA"},"commit_sha":{"$ref":"#/$defs/GitSHA"},"pull_request_number":{"type":"integer","minimum":1,"maximum":9223372036854775807},"pull_request_url":{"$ref":"#/$defs/HTTPSURL"},"automation_evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":256}},"required":["schema","physical_call","kind","repository","usage","staged_outputs","output_artifacts"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"HTTPOrigin":{"type":"string","format":"uri","pattern":"^https?://[^/?#]+$","maxLength":2048,"$schema":"https://json-schema.org/draft/2020-12/schema"},"HTTPURL":{"type":"string","format":"uri","pattern":"^https?://","maxLength":4096,"$schema":"https://json-schema.org/draft/2020-12/schema"},"HTTPReadProfile":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.httpread/v1","type":"string"},"allowed_origins":{"type":"array","items":{"$ref":"#/$defs/HTTPOrigin"},"minItems":1,"maxItems":256},"max_bytes":{"type":"integer","minimum":1,"maximum":268435456},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"max_redirects":{"type":"integer","const":0},"allowed_media_types":{"type":"array","items":{"type":"string","minLength":1,"maxLength":256},"minItems":1,"maxItems":128},"capability_evidence":{"$ref":"#/$defs/CapabilityEvidence"}},"required":["schema","allowed_origins","max_bytes","timeout_seconds","max_redirects","allowed_media_types","capability_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"HTTPReadHeader":{"type":"object","additionalProperties":false,"properties":{"name":{"type":"string","enum":["Accept","Accept-Language","If-None-Match","If-Modified-Since","User-Agent"]},"value":{"type":"string","maxLength":1024,"pattern":"^[^\r\n]*$"}},"required":["name","value"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"HTTPReadParameters":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.httpread.action/v1","type":"string"},"kind":{"const":"read","type":"string"},"url":{"$ref":"#/$defs/HTTPURL"},"method":{"const":"GET","type":"string"},"headers":{"type":"array","items":{"$ref":"#/$defs/HTTPReadHeader"},"minItems":0,"maxItems":16},"expected_media_type":{"type":"string","minLength":1,"maxLength":256}},"required":["schema","kind","url","method","headers","expected_media_type"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"HTTPReadEvidence":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.httpread.evidence/v1","type":"string"},"physical_call":{"$ref":"#/$defs/PhysicalCallEvidence"},"requested_url":{"$ref":"#/$defs/HTTPURL"},"resolved_url":{"$ref":"#/$defs/HTTPURL"},"validated_dial_addresses":{"type":"array","items":{"type":"string","minLength":1,"maxLength":128},"minItems":0,"maxItems":32},"status":{"type":"integer","minimum":100,"maximum":599},"media_type":{"type":"string","minLength":0,"maxLength":256},"freshness":{"$ref":"#/$defs/UTC"},"usage":{"$ref":"#/$defs/ProviderUsage"},"staged_outputs":{"type":"array","items":{"$ref":"#/$defs/StagedOutput"},"minItems":0,"maxItems":2},"output_artifacts":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":2},"content_digest":{"$ref":"#/$defs/Digest"},"content_size":{"type":"integer","minimum":0,"maximum":268435456},"etag":{"type":"string","minLength":0,"maxLength":1024},"last_modified":{"type":"string","minLength":0,"maxLength":128},"redirect_location":{"$ref":"#/$defs/HTTPURL"}},"required":["schema","physical_call","requested_url","resolved_url","validated_dial_addresses","status","media_type","freshness","usage","staged_outputs","output_artifacts"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityBrainMapping":{"type":"object","additionalProperties":false,"properties":{"brain_id":{"$ref":"#/$defs/ID"},"endpoint":{"type":"string","minLength":1,"maxLength":4096},"root_ref":{"type":"string","minLength":1,"maxLength":512},"writer_owner":{"type":"string","minLength":1,"maxLength":512},"classification":{"$ref":"#/$defs/Classification"}},"required":["brain_id","endpoint","root_ref","writer_owner","classification"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityLookupSemantics":{"type":"object","additionalProperties":false,"properties":{"mode":{"type":"string","enum":["authoritative","non_authoritative","unsupported"]},"retention_seconds":{"type":"integer","minimum":0,"maximum":9223372036854775807},"command_identity_supported":{"type":"boolean"},"evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":64}},"required":["mode","retention_seconds","command_identity_supported","evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityFreshnessCapability":{"type":"object","additionalProperties":false,"properties":{"source_revision_supported":{"type":"boolean"},"index_revision_supported":{"type":"boolean"},"minimum_freshness_enforceable":{"type":"boolean"},"read_facade":{"type":"string","enum":["qualified_local","unsupported"]},"evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":64}},"required":["source_revision_supported","index_revision_supported","minimum_freshness_enforceable","read_facade","evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityBackupProtocol":{"type":"object","additionalProperties":false,"properties":{"mode":{"type":"string","enum":["qualified_pinned_revision","unsupported"]},"protocol_profile":{"type":"string","minLength":1,"maxLength":256},"immutable_revision_export":{"type":"boolean"},"restore_supported":{"type":"boolean"},"evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":64}},"required":["mode","protocol_profile","immutable_revision_export","restore_supported","evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityProfile":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.serenity/v2","type":"string"},"version":{"type":"string","minLength":1,"maxLength":128},"commit":{"type":"string","minLength":1,"maxLength":128},"brain_mappings":{"type":"array","items":{"oneOf":[{"$ref":"#/$defs/SerenityBrainMapping"},{"$ref":"#/$defs/SerenityHostedBrainMapping"}]},"minItems":0,"maxItems":4096},"supported_operations":{"type":"array","items":{"type":"string","enum":["recall","remember","inspect","promote","retract","export_revision"]},"minItems":0,"maxItems":6},"enforcement":{"$ref":"#/$defs/BoundEnforcement"},"command_status_lookup":{"$ref":"#/$defs/SerenityLookupSemantics"},"freshness":{"$ref":"#/$defs/SerenityFreshnessCapability"},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"max_bytes":{"type":"integer","minimum":1,"maximum":268435456},"backup_revision_protocol":{"$ref":"#/$defs/SerenityBackupProtocol"},"capability_evidence":{"$ref":"#/$defs/CapabilityEvidence"}},"required":["schema","version","commit","brain_mappings","supported_operations","enforcement","command_status_lookup","freshness","timeout_seconds","max_bytes","backup_revision_protocol","capability_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"MemoryClaim":{"type":"object","additionalProperties":false,"properties":{"id":{"$ref":"#/$defs/ID"},"version":{"$ref":"#/$defs/Version"},"brain_id":{"$ref":"#/$defs/ID"},"text":{"type":"string","minLength":0,"maxLength":262144},"sources":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":256},"confidence":{"type":"integer","minimum":0,"maximum":1000000},"freshness":{"$ref":"#/$defs/UTC"},"active":{"type":"boolean"},"source_brain_id":{"$ref":"#/$defs/ID"},"source_claim":{"$ref":"#/$defs/VersionRef"},"curator_id":{"$ref":"#/$defs/ID"},"redaction":{"type":"string","minLength":0,"maxLength":8192}},"required":["id","version","brain_id","text","sources","confidence","freshness","active"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"BrainRevision":{"type":"object","additionalProperties":false,"properties":{"brain_id":{"$ref":"#/$defs/ID"},"revision":{"type":"string","minLength":1,"maxLength":256},"digest":{"$ref":"#/$defs/Digest"},"observed_at":{"$ref":"#/$defs/UTC"},"index_revision":{"type":"string","minLength":1,"maxLength":256}},"required":["brain_id","revision","digest","observed_at"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityRecall":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.serenity.action/v1","type":"string"},"brain_id":{"$ref":"#/$defs/ID"},"adapter_command_id":{"$ref":"#/$defs/ID"},"kind":{"const":"recall","type":"string"},"query":{"type":"string","minLength":1,"maxLength":8192},"minimum_freshness":{"$ref":"#/$defs/UTC"},"max_claims":{"type":"integer","minimum":1,"maximum":200},"maximum_cost":{"$ref":"#/$defs/Money"},"allowed_provider_destinations":{"type":"array","items":{"$ref":"#/$defs/HTTPSURL"},"minItems":0,"maxItems":64},"classification":{"$ref":"#/$defs/Classification"}},"required":["schema","brain_id","adapter_command_id","kind","query","minimum_freshness","max_claims","maximum_cost","allowed_provider_destinations","classification"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityRemember":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.serenity.action/v1","type":"string"},"brain_id":{"$ref":"#/$defs/ID"},"adapter_command_id":{"$ref":"#/$defs/ID"},"kind":{"const":"remember","type":"string"},"text":{"type":"string","minLength":1,"maxLength":8192},"sources":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":256},"writer_owner":{"type":"string","minLength":1,"maxLength":512},"maximum_cost":{"$ref":"#/$defs/Money"},"allowed_provider_destinations":{"type":"array","items":{"$ref":"#/$defs/HTTPSURL"},"minItems":0,"maxItems":64}},"required":["schema","brain_id","adapter_command_id","kind","text","sources","writer_owner","maximum_cost","allowed_provider_destinations"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityInspect":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.serenity.action/v1","type":"string"},"brain_id":{"$ref":"#/$defs/ID"},"adapter_command_id":{"$ref":"#/$defs/ID"},"kind":{"const":"inspect","type":"string"},"claim":{"$ref":"#/$defs/VersionRef"},"minimum_freshness":{"$ref":"#/$defs/UTC"}},"required":["schema","brain_id","adapter_command_id","kind","claim","minimum_freshness"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityPromote":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.serenity.action/v1","type":"string"},"brain_id":{"$ref":"#/$defs/ID"},"adapter_command_id":{"$ref":"#/$defs/ID"},"kind":{"const":"promote","type":"string"},"source_brain_id":{"$ref":"#/$defs/ID"},"source_claim":{"$ref":"#/$defs/VersionRef"},"source_disclosure_evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":1,"maxItems":64},"text":{"type":"string","minLength":1,"maxLength":8192},"sources":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":256},"curator_id":{"$ref":"#/$defs/ID"},"writer_owner":{"type":"string","minLength":1,"maxLength":512},"maximum_cost":{"$ref":"#/$defs/Money"},"allowed_provider_destinations":{"type":"array","items":{"$ref":"#/$defs/HTTPSURL"},"minItems":0,"maxItems":64},"redaction":{"type":"string","minLength":0,"maxLength":8192}},"required":["schema","brain_id","adapter_command_id","kind","source_brain_id","source_claim","source_disclosure_evidence","text","sources","curator_id","writer_owner","maximum_cost","allowed_provider_destinations"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityRetract":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.serenity.action/v1","type":"string"},"brain_id":{"$ref":"#/$defs/ID"},"adapter_command_id":{"$ref":"#/$defs/ID"},"kind":{"const":"retract","type":"string"},"claim":{"$ref":"#/$defs/VersionRef"},"reason":{"type":"string","minLength":1,"maxLength":8192},"writer_owner":{"type":"string","minLength":1,"maxLength":512},"removal":{"const":"active_recall","type":"string"}},"required":["schema","brain_id","adapter_command_id","kind","claim","reason","writer_owner","removal"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityExportRevision":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.serenity.action/v1","type":"string"},"brain_id":{"$ref":"#/$defs/ID"},"adapter_command_id":{"$ref":"#/$defs/ID"},"kind":{"const":"export_revision","type":"string"},"revision":{"$ref":"#/$defs/BrainRevision"}},"required":["schema","brain_id","adapter_command_id","kind","revision"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityParameters":{"oneOf":[{"$ref":"#/$defs/SerenityRecall"},{"$ref":"#/$defs/SerenityRemember"},{"$ref":"#/$defs/SerenityInspect"},{"$ref":"#/$defs/SerenityPromote"},{"$ref":"#/$defs/SerenityRetract"},{"$ref":"#/$defs/SerenityExportRevision"}],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityEvidence":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.serenity.evidence/v1","type":"string"},"physical_call":{"$ref":"#/$defs/PhysicalCallEvidence"},"kind":{"type":"string","enum":["recall","remember","inspect","promote","retract","export_revision"]},"brain_id":{"$ref":"#/$defs/ID"},"adapter_command_id":{"$ref":"#/$defs/ID"},"command_status":{"type":"string","enum":["not_admitted","accepted","completed","failed","unknown"]},"claims":{"type":"array","items":{"$ref":"#/$defs/MemoryClaim"},"minItems":0,"maxItems":200},"brain_revisions":{"type":"array","items":{"$ref":"#/$defs/BrainRevision"},"minItems":0,"maxItems":64},"usage":{"$ref":"#/$defs/ProviderUsage"},"staged_outputs":{"type":"array","items":{"$ref":"#/$defs/StagedOutput"},"minItems":0,"maxItems":256},"output_artifacts":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":256},"writer_owner":{"type":"string","minLength":1,"maxLength":512},"active_recall_removed":{"type":"boolean"},"historical_erasure":{"type":"boolean","const":false},"selected_context":{"$ref":"#/$defs/ArtifactLocator"},"lookup_authoritative":{"type":"boolean"}},"required":["schema","physical_call","kind","brain_id","adapter_command_id","command_status","claims","brain_revisions","usage","staged_outputs","output_artifacts"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"VerifierOutputRequirement":{"type":"object","additionalProperties":false,"properties":{"name":{"type":"string","minLength":1,"maxLength":128},"artifact":{"$ref":"#/$defs/ArtifactRef"},"media_type":{"type":"string","minLength":1,"maxLength":256},"json_schema":{"$ref":"#/$defs/InertSchema"}},"required":["name","artifact","media_type"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ArtifactVerifierProfile":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.verifier-profile/v1","type":"string"},"kind":{"const":"artifact_contract","type":"string"},"id":{"type":"string","minLength":1,"maxLength":256},"version":{"type":"string","minLength":1,"maxLength":128},"code_digest":{"$ref":"#/$defs/Digest"},"supported_checks":{"type":"array","items":{"type":"string","enum":["presence","digest","json_schema"]},"minItems":1,"maxItems":3},"max_bytes":{"type":"integer","minimum":1,"maximum":268435456},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"capability_evidence":{"$ref":"#/$defs/CapabilityEvidence"}},"required":["schema","kind","id","version","code_digest","supported_checks","max_bytes","timeout_seconds","capability_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"RepositoryVerifierProfile":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.verifier-profile/v1","type":"string"},"kind":{"const":"repository_patch","type":"string"},"id":{"type":"string","minLength":1,"maxLength":256},"version":{"type":"string","minLength":1,"maxLength":128},"code_digest":{"$ref":"#/$defs/Digest"},"runner_profile":{"type":"string","minLength":1,"maxLength":256},"command_id":{"type":"string","minLength":1,"maxLength":256},"command_digest":{"$ref":"#/$defs/Digest"},"environment_profile":{"type":"string","minLength":1,"maxLength":256},"network":{"type":"string","enum":["disabled","qualified_allowlist"]},"max_output_bytes":{"type":"integer","minimum":1,"maximum":1048576},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"capability_evidence":{"$ref":"#/$defs/CapabilityEvidence"}},"required":["schema","kind","id","version","code_digest","runner_profile","command_id","command_digest","environment_profile","network","max_output_bytes","timeout_seconds","capability_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"VerificationProfile":{"oneOf":[{"$ref":"#/$defs/ArtifactVerifierProfile"},{"$ref":"#/$defs/RepositoryVerifierProfile"}],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ExpectedVerificationObservation":{"type":"object","additionalProperties":false,"properties":{"check_id":{"type":"string","minLength":1,"maxLength":256},"kind":{"type":"string","enum":["artifact_presence","artifact_digest","json_schema","repository_patch_applies","repository_command"]},"expected":{"type":"string","enum":["pass","fail"]},"artifact_name":{"type":"string","minLength":1,"maxLength":128},"expected_digest":{"$ref":"#/$defs/Digest"},"schema":{"$ref":"#/$defs/InertSchema"},"command_id":{"type":"string","minLength":1,"maxLength":256},"expected_exit_code":{"type":"integer","minimum":-2147483648,"maximum":2147483647}},"required":["check_id","kind","expected"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ObservedVerificationCheck":{"type":"object","additionalProperties":false,"properties":{"check_id":{"type":"string","minLength":1,"maxLength":256},"kind":{"type":"string","enum":["artifact_presence","artifact_digest","json_schema","repository_patch_applies","repository_command"]},"status":{"type":"string","enum":["passed","failed","unavailable","tampered"]},"evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactLocator"},"minItems":0,"maxItems":128},"explanation":{"type":"string","minLength":0,"maxLength":8192},"observed_digest":{"$ref":"#/$defs/Digest"},"observed_exit_code":{"type":"integer","minimum":-2147483648,"maximum":2147483647}},"required":["check_id","kind","status","evidence","explanation"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"VerificationRequest":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.verification-request/v1","type":"string"},"job_id":{"$ref":"#/$defs/ID"},"task_id":{"$ref":"#/$defs/ID"},"attempt_id":{"$ref":"#/$defs/ID"},"scope":{"$ref":"#/$defs/Scope"},"acceptance_digest":{"$ref":"#/$defs/Digest"},"profile":{"$ref":"#/$defs/VerificationProfile"},"sealed_inputs":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":4096},"outputs":{"type":"array","items":{"$ref":"#/$defs/VerifierOutputRequirement"},"minItems":0,"maxItems":4096},"expected_observations":{"type":"array","items":{"$ref":"#/$defs/ExpectedVerificationObservation"},"minItems":1,"maxItems":512},"deadline":{"$ref":"#/$defs/UTC"},"repository":{"$ref":"#/$defs/Repository"},"base_sha":{"$ref":"#/$defs/GitSHA"},"patch":{"$ref":"#/$defs/ArtifactRef"}},"required":["schema","job_id","task_id","attempt_id","scope","acceptance_digest","profile","sealed_inputs","outputs","expected_observations","deadline"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"VerificationResult":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.verification-result/v1","type":"string"},"job_id":{"$ref":"#/$defs/ID"},"task_id":{"$ref":"#/$defs/ID"},"attempt_id":{"$ref":"#/$defs/ID"},"acceptance_digest":{"$ref":"#/$defs/Digest"},"verifier_id":{"type":"string","minLength":1,"maxLength":256},"verifier_version":{"type":"string","minLength":1,"maxLength":128},"verifier_code_digest":{"$ref":"#/$defs/Digest"},"request_artifact":{"$ref":"#/$defs/ArtifactRef"},"status":{"type":"string","enum":["passed","failed","prerequisite_missing","tampered","interrupted"]},"observations":{"type":"array","items":{"$ref":"#/$defs/ObservedVerificationCheck"},"minItems":0,"maxItems":512},"started_at":{"$ref":"#/$defs/UTC"},"finished_at":{"$ref":"#/$defs/UTC"},"staged_outputs":{"type":"array","items":{"$ref":"#/$defs/StagedOutput"},"minItems":0,"maxItems":256},"output_artifacts":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":256},"independent":{"type":"boolean","const":true}},"required":["schema","job_id","task_id","attempt_id","acceptance_digest","verifier_id","verifier_version","verifier_code_digest","request_artifact","status","observations","started_at","finished_at","staged_outputs","output_artifacts","independent"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"BackupArtifactEntry":{"type":"object","additionalProperties":false,"properties":{"artifact":{"$ref":"#/$defs/ArtifactRef"},"size":{"type":"integer","minimum":0,"maximum":268435456},"media_type":{"type":"string","minLength":1,"maxLength":256},"classification":{"$ref":"#/$defs/Classification"},"encrypted":{"type":"boolean"},"archive_entry":{"type":"string","minLength":1,"maxLength":512},"pins":{"type":"array","items":{"$ref":"#/$defs/ID"},"minItems":0,"maxItems":4096}},"required":["artifact","size","media_type","classification","encrypted","archive_entry","pins"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"BackupBrainEntry":{"type":"object","additionalProperties":false,"properties":{"revision":{"$ref":"#/$defs/BrainRevision"},"export_artifact":{"$ref":"#/$defs/ArtifactRef"},"writer_owner":{"type":"string","minLength":1,"maxLength":512},"adapter_profile_digest":{"$ref":"#/$defs/Digest"},"key_prerequisites":{"type":"array","items":{"type":"string","minLength":1,"maxLength":512},"minItems":0,"maxItems":64}},"required":["revision","export_artifact","writer_owner","adapter_profile_digest","key_prerequisites"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"RecoveryObligation":{"type":"object","additionalProperties":false,"properties":{"id":{"$ref":"#/$defs/ID"},"owner":{"type":"string","enum":["identity","evidence","effects","accounting","execution","memory","installation"]},"kind":{"type":"string","enum":["credential_revocation","principal_revocation","command_identity","claimed_effect","unknown_effect","reservation","lease_conflict","memory_write","memory_promotion","memory_retraction"]},"resource_id":{"$ref":"#/$defs/ID"},"resource_version":{"$ref":"#/$defs/Version"},"record_artifact":{"$ref":"#/$defs/ArtifactRef"},"record_digest":{"$ref":"#/$defs/Digest"},"state":{"type":"string","minLength":1,"maxLength":128},"recorded_at":{"$ref":"#/$defs/UTC"}},"required":["id","owner","kind","resource_id","resource_version","record_artifact","record_digest","state","recorded_at"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"BackupManifest":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.backup/v1","type":"string"},"installation_id":{"$ref":"#/$defs/ID"},"backup_id":{"$ref":"#/$defs/ID"},"generation":{"$ref":"#/$defs/Version"},"created_at":{"$ref":"#/$defs/UTC"},"database_digest":{"$ref":"#/$defs/Digest"},"database_size":{"type":"integer","minimum":1,"maximum":9223372036854775807},"database_archive_entry":{"const":"state.sqlite","type":"string"},"database_schema_versions":{"type":"array","items":{"type":"object","additionalProperties":false,"properties":{"owner":{"type":"string","minLength":1,"maxLength":128},"version":{"$ref":"#/$defs/Version"},"migration_digest":{"$ref":"#/$defs/Digest"}},"required":["owner","version","migration_digest"]},"minItems":0,"maxItems":256},"artifacts":{"type":"array","items":{"$ref":"#/$defs/BackupArtifactEntry"},"minItems":0,"maxItems":1000000},"brains":{"type":"array","items":{"$ref":"#/$defs/BackupBrainEntry"},"minItems":0,"maxItems":100000},"retained_obligations":{"type":"array","items":{"$ref":"#/$defs/RecoveryObligation"},"minItems":0,"maxItems":1000000},"key_prerequisites":{"type":"array","items":{"type":"string","minLength":1,"maxLength":512},"minItems":0,"maxItems":256},"source_revision":{"type":"string","minLength":1,"maxLength":128},"controller_version":{"type":"string","minLength":1,"maxLength":128},"required_protocol_profiles":{"type":"array","items":{"type":"string","minLength":1,"maxLength":256},"minItems":0,"maxItems":256},"paused":{"type":"boolean","const":true},"recovery_overlay":{"$ref":"#/$defs/ArtifactRef"}},"required":["schema","installation_id","backup_id","generation","created_at","database_digest","database_size","database_archive_entry","database_schema_versions","artifacts","brains","retained_obligations","key_prerequisites","source_revision","controller_version","required_protocol_profiles","paused"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"RecoveryOverlay":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.recovery-overlay/v1","type":"string"},"installation_id":{"$ref":"#/$defs/ID"},"captured_at":{"$ref":"#/$defs/UTC"},"source_generation":{"$ref":"#/$defs/Version"},"source_database_digest":{"$ref":"#/$defs/Digest"},"obligations":{"type":"array","items":{"$ref":"#/$defs/RecoveryObligation"},"minItems":0,"maxItems":1000000},"artifact_entries":{"type":"array","items":{"$ref":"#/$defs/BackupArtifactEntry"},"minItems":0,"maxItems":1000000},"key_prerequisites":{"type":"array","items":{"type":"string","minLength":1,"maxLength":512},"minItems":0,"maxItems":256}},"required":["schema","installation_id","captured_at","source_generation","source_database_digest","obligations","artifact_entries","key_prerequisites"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ResponsesPrepareSessionParameters":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.responses.action/v1","type":"string"},"kind":{"const":"prepare_session","type":"string"}},"required":["schema","kind"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ResponsesModelStepParameters":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.responses.action/v1","type":"string"},"kind":{"const":"model_step","type":"string"},"session_handle":{"type":"string","minLength":1,"maxLength":1024},"context_artifact":{"$ref":"#/$defs/ArtifactRef"},"max_output_tokens":{"type":"integer","minimum":1,"maximum":1000000},"tool_contract_versions":{"type":"array","items":{"$ref":"#/$defs/VersionRef"},"minItems":0,"maxItems":256},"continuation_reference":{"type":"string","minLength":0,"maxLength":1024}},"required":["schema","kind","session_handle","context_artifact","max_output_tokens","tool_contract_versions"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityHostedBrainMapping":{"type":"object","additionalProperties":false,"properties":{"brain_id":{"$ref":"#/$defs/ID"},"hosted_project_id":{"type":"string","pattern":"^[A-Za-z0-9]{16,64}$"},"connection_id":{"$ref":"#/$defs/ID"},"endpoint":{"const":"https://serenity.sire.run/mcp","type":"string"},"classification":{"$ref":"#/$defs/Classification"}},"required":["brain_id","hosted_project_id","connection_id","endpoint","classification"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ResponsesProfileV2":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.responses/v2","type":"string"},"endpoint":{"$ref":"#/$defs/HTTPSURL"},"model":{"type":"string","minLength":1,"maxLength":256},"connection_id":{"$ref":"#/$defs/ID"},"max_input_tokens":{"type":"integer","minimum":1,"maximum":10000000},"max_output_tokens":{"type":"integer","minimum":1,"maximum":1000000},"max_response_bytes":{"type":"integer","minimum":1,"maximum":268435456},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"currency":{"$ref":"#/$defs/Currency"},"input_rate":{"$ref":"#/$defs/RationalRate"},"output_rate":{"$ref":"#/$defs/RationalRate"},"enforcement":{"$ref":"#/$defs/BoundEnforcement"},"capability_evidence":{"$ref":"#/$defs/CapabilityEvidence"},"provider":{"type":"string","enum":["openai","openrouter","experiential"]},"session_mode":{"type":"string","enum":["provider_conversation","stateless"]},"routing":{"type":"object"}},"required":["schema","endpoint","model","connection_id","max_input_tokens","max_output_tokens","max_response_bytes","timeout_seconds","currency","input_rate","output_rate","enforcement","capability_evidence","provider","session_mode","routing"],"$schema":"https://json-schema.org/draft/2020-12/schema","allOf":[{"if":{"properties":{"provider":{"const":"openai"}},"required":["provider"]},"then":{"properties":{"session_mode":{"const":"provider_conversation"},"routing":{"maxProperties":0},"endpoint":{"const":"https://api.openai.com/v1/responses","type":"string"}}}},{"if":{"properties":{"provider":{"const":"openrouter"}},"required":["provider"]},"then":{"properties":{"session_mode":{"const":"stateless"},"routing":{"type":"object","additionalProperties":false,"properties":{"only":{"type":"array","items":{"type":"string","minLength":1,"maxLength":128},"minItems":1,"maxItems":16},"allow_fallbacks":{"const":false,"type":"boolean"},"require_parameters":{"const":true,"type":"boolean"},"privacy":{"type":"array","items":{"type":"string","enum":["no_training","data_policy","zero_retention"]},"minItems":0,"maxItems":8},"price_ceiling":{"type":"object","additionalProperties":false,"properties":{"currency":{"const":"USD","type":"string"},"input_per_million":{"type":"string","pattern":"^(0|[1-9][0-9]*)(\\.[0-9]{1,18})?$","maxLength":64},"output_per_million":{"type":"string","pattern":"^(0|[1-9][0-9]*)(\\.[0-9]{1,18})?$","maxLength":64}},"required":["currency","input_per_million","output_per_million"]}},"required":["only","allow_fallbacks","require_parameters"]},"endpoint":{"const":"https://openrouter.ai/api/v1/responses","type":"string"}}}},{"if":{"properties":{"provider":{"const":"experiential"}},"required":["provider"]},"then":{"properties":{"session_mode":{"const":"stateless"},"routing":{"type":"object","additionalProperties":false,"properties":{"gateway":{"type":"object","additionalProperties":false,"properties":{"retry":{"type":"object","additionalProperties":false,"properties":{"max_attempts_per_route":{"const":1,"type":"integer"},"max_total_attempts":{"const":1,"type":"integer"}},"required":["max_attempts_per_route","max_total_attempts"]},"backoff":{"type":"object","additionalProperties":false,"properties":{"type":{"const":"none","type":"string"}},"required":["type"]},"routing":{"type":"object","additionalProperties":false,"properties":{"allow_fallbacks":{"const":false,"type":"boolean"}},"required":["allow_fallbacks"]}},"required":["retry","backoff","routing"]},"route_id":{"type":"string","minLength":1,"maxLength":256},"privacy":{"type":"array","items":{"type":"string","enum":["no_training","data_policy","zero_retention"]},"minItems":0,"maxItems":8}},"required":["gateway"]},"endpoint":{"const":"https://api.experientiallabs.ai/v1/responses","type":"string"}}}}]},"ResponsesPrepareSessionParametersV2":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.responses.action/v2","type":"string"},"kind":{"const":"prepare_session","type":"string"},"session_mode":{"type":"string","enum":["provider_conversation","stateless"],"const":"provider_conversation"},"session_id":{"type":"string","minLength":1,"maxLength":128}},"required":["schema","kind","session_mode"]},"ResponsesModelStepParametersV2":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.responses.action/v2","type":"string"},"kind":{"const":"model_step","type":"string"},"session_mode":{"type":"string","enum":["provider_conversation","stateless"]},"session_id":{"type":"string","minLength":1,"maxLength":128},"context_artifact":{"$ref":"#/$defs/ArtifactRef"},"max_output_tokens":{"type":"integer","minimum":1,"maximum":1000000},"tool_contract_versions":{"type":"array","items":{"$ref":"#/$defs/VersionRef"},"minItems":0,"maxItems":256},"session_handle":{"type":"string","minLength":1,"maxLength":1024},"continuation_reference":{"type":"string","minLength":1,"maxLength":1024}},"required":["schema","kind","session_mode","context_artifact","max_output_tokens","tool_contract_versions"],"allOf":[{"if":{"properties":{"session_mode":{"const":"provider_conversation"}},"required":["session_mode"]},"then":{"required":["session_handle"]}},{"if":{"properties":{"session_mode":{"const":"stateless"}},"required":["session_mode"]},"then":{"not":{"anyOf":[{"required":["session_handle"]},{"required":["continuation_reference"]}]}}}]},"ResponsesParametersV2":{"oneOf":[{"$ref":"#/$defs/ResponsesPrepareSessionParametersV2"},{"$ref":"#/$defs/ResponsesModelStepParametersV2"},{"$ref":"#/$defs/ResponsesQualificationProbeParametersV2"}]},"ResponsesEvidenceV2":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.responses.evidence/v2","type":"string"},"physical_call":{"$ref":"#/$defs/PhysicalCallEvidence"},"session_handle":{"type":"string","minLength":1,"maxLength":1024},"response_id":{"type":"string","minLength":0,"maxLength":1024},"output":{"$ref":"#/$defs/ModelOutput"},"staged_outputs":{"type":"array","items":{"$ref":"#/$defs/StagedOutput"},"minItems":0,"maxItems":256},"output_artifacts":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":256},"session_mode":{"type":"string","enum":["provider_conversation","stateless"]},"session_id":{"type":"string","minLength":1,"maxLength":128},"kind":{"type":"string","const":"qualification_probe"},"qualification":{"$ref":"#/$defs/ResponsesQualificationMetadataV2"}},"required":["schema","physical_call","staged_outputs","session_mode"],"$schema":"https://json-schema.org/draft/2020-12/schema","allOf":[{"if":{"properties":{"session_mode":{"const":"provider_conversation"},"physical_call":{"properties":{"confirmation":{"const":"authoritative_success"}},"required":["confirmation"]}},"required":["session_mode","physical_call"],"not":{"required":["kind"]}},"then":{"required":["session_handle"]}},{"if":{"properties":{"session_mode":{"const":"stateless"}},"required":["session_mode"]},"then":{"not":{"required":["session_handle"]}}},{"if":{"required":["kind"],"properties":{"kind":{"const":"qualification_probe"}}},"then":{"not":{"required":["session_handle"]}}},{"if":{"required":["kind"],"properties":{"kind":{"const":"qualification_probe"}}},"then":{"required":["qualification"]}}]},"ProviderDescriptor":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","enum":["openai","openrouter","experiential"]},"display_name":{"type":"string","minLength":1,"maxLength":128},"default_endpoint":{"$ref":"#/$defs/HTTPSURL"},"session_mode":{"type":"string","enum":["provider_conversation","stateless"]},"credential_setup":{"const":"api_key","type":"string"}},"required":["id","display_name","default_endpoint","session_mode","credential_setup"],"allOf":[{"if":{"properties":{"id":{"const":"openai"}},"required":["id"]},"then":{"properties":{"default_endpoint":{"const":"https://api.openai.com/v1/responses","type":"string"},"session_mode":{"const":"provider_conversation"}}}},{"if":{"properties":{"id":{"const":"openrouter"}},"required":["id"]},"then":{"properties":{"default_endpoint":{"const":"https://openrouter.ai/api/v1/responses","type":"string"},"session_mode":{"const":"stateless"}}}},{"if":{"properties":{"id":{"const":"experiential"}},"required":["id"]},"then":{"properties":{"default_endpoint":{"const":"https://api.experientiallabs.ai/v1/responses","type":"string"},"session_mode":{"const":"stateless"}}}}]},"ResponsesQualificationProbeParametersV2":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.responses.action/v2","type":"string"},"kind":{"const":"qualification_probe","type":"string"},"session_mode":{"type":"string","enum":["provider_conversation","stateless"]},"model":{"type":"string","minLength":1,"maxLength":256},"probe_text":{"const":"Reply with exactly: ZATITI_MODEL_QUALIFIED","type":"string"},"max_output_tokens":{"type":"integer","minimum":1,"maximum":64},"profile_digest":{"$ref":"#/$defs/Digest"},"qualification_cost_bound":{"$ref":"#/$defs/Money"},"provider":{"type":"string","enum":["openai","openrouter","experiential"]}},"required":["schema","kind","session_mode","model","probe_text","max_output_tokens","profile_digest","qualification_cost_bound","provider"]},"ResponsesQualificationMetadataV2":{"type":"object","additionalProperties":false,"properties":{"adapter_version":{"type":"string","minLength":1,"maxLength":128},"source_revision":{"type":"string","minLength":1,"maxLength":256},"protocol_revision":{"type":"string","minLength":1,"maxLength":128},"capabilities":{"type":"array","items":{"type":"string","minLength":1,"maxLength":128},"maxItems":32},"limitations":{"type":"array","items":{"type":"string","minLength":1,"maxLength":2048},"maxItems":32}},"required":["adapter_version","source_revision","protocol_revision","capabilities","limitations"]}},"adapter_mapping":{"responses":{"profile":"ResponsesProfile","parameters":"ResponsesParameters","evidence":"ResponsesEvidence","profile_v2":"ResponsesProfileV2","parameters_v2":"ResponsesParametersV2","evidence_v2":"ResponsesEvidenceV2"},"github":{"profile":"GitHubProfile","parameters":"GitHubParameters","evidence":"GitHubEvidence"},"httpread":{"profile":"HTTPReadProfile","parameters":"HTTPReadParameters","evidence":"HTTPReadEvidence"},"serenity":{"profile":"SerenityProfile","parameters":"SerenityParameters","evidence":"SerenityEvidence"}}}
```

## Named acceptance cases

Tests are implementation deliverables, not claims of already executed qualification. Retain expected/observed results, exact source/config/tool versions and failure evidence.

### Z01.duplicate_controller — Z01

Setup: An initialized installation is already served by one controller.

Action: Start a second controller against the same state directory.

Expected:

- Second controller refuses exclusive ownership and serves no requests.
- Only the original controller admits work; no extra scheduler or writer appears.

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

### Z01.credential_custody — Z01

Setup: A scoped agent can inspect connection metadata but has no access to owner or provider secret material.

Action: Inspect credentials through capabilities, CLI, MCP, connection inspection and malformed credential/profile arguments.

Expected:

- Only opaque references and permitted metadata are returned.
- Profile names, MCP client names and socket access do not independently confer authority; owner profile cannot be selected through tool arguments.

### Z02.registry_surface — Z02

Setup: A generated registry enumerates every concrete operation/version, including bootstrap and maintenance.

Action: Discover CLI mappings and MCP tools and compare them to the registry.

Expected:

- Every registry operation has both mappings and equivalent input/output schemas.
- Generation rejects mapping collisions; capabilities has its declared special mapping.
- Optional MCP resources or prompts are never the sole route to a capability.

### Z02.lifecycle_parity — Z02

Setup: Create isolated matching controllers with equivalent state and principals plus controlled provider fixtures.

Action: Execute each registry operation through real CLI subprocesses and an MCP client, including lifecycle sequences.

Expected:

- Equivalent results, resource versions, state changes, errors, pagination, authorization and business events are observed.
- Only generated IDs and timestamps are normalized through an explicit mapping; permissions and uncertainty are never normalized away.

### Z02.wire_errors — Z02

Setup: Fixtures include completed, accepted, invalid, denied, review-required, stale, unavailable and unknown operations.

Action: Call equivalent operations through CLI and MCP.

Expected:

- CLI JSON mode emits exactly one versioned result on stdout and diagnostics on stderr; exit mappings match the specification.
- MCP emits protocol frames only on stdout, returns equivalent structuredContent and JSON text, maps domain faults to isError, and uses protocol errors for malformed protocol messages.

### Z03.agent_setup_mcp — Z03

Setup: A clean installation and scoped client-agent profile exist; paid execution is initially unavailable.

Action: Using MCP discovery alone, create an organization and workers, import/evaluate a skill, set up and validate a connection, resolve prerequisites, plan/apply and start work.

Expected:

- The complete authorized setup succeeds without dashboard or repository-source knowledge.
- A verified output artifact is inspectable; paid work remains refused until provider, currency and limits are configured.
- Credential prerequisites return actionable challenges without secret bytes in model-visible input or output.

### Z03.agent_setup_cli — Z03

Setup: Create a fixture equivalent to the MCP setup case with the same authority.

Action: Perform the complete discovery, organization/skill/worker/connection, prerequisite, activation and first-work journey using CLI JSON mode.

Expected:

- The CLI completes the same lifecycle and produces equivalent authorized state and verified output.
- No TTY or silent prompt is required; incomplete input returns a structured actionable result.

### Z03.bootstrap_parity — Z03

Setup: Two matching empty installation destinations and secure credential stores are available.

Action: Initialize one with zatiti init and the other with zatiti mcp serve --bootstrap; attempt initialization again.

Expected:

- Both use the one-time local initializer and create owner, personal organization and personal chief consistently.
- Results contain metadata only; reinitialization is refused and bootstrap cannot become a normal administration session.

### Z04.concurrent_apply — Z04

Setup: Two plans share one active base revision and conflict on at least one object.

Action: Apply both concurrently.

Expected:

- Exactly one revision activates; the other returns stale/conflicting state.
- Every object in the winning bundle and its event commit atomically; no partial activation occurs.

### Z04.stale_dependencies — Z04

Setup: A sealed plan pins dependencies, connection identity/validation, compiler/schema versions and a required decision.

Action: Change a pinned dependency or expire validation before applying; regenerate a changed plan and reuse the old decision.

Expected:

- The original plan cannot activate.
- Regeneration repins dependencies and requires renewal of any decision whose action digest changed.

### Z04.explicit_removal — Z04

Setup: An active configuration contains workers, projects and retained obligations.

Action: Apply a patch omitting an object, then explicitly remove/archive an object that owns active work or unknown effects.

Expected:

- Omission does not delete live objects.
- Explicit removal first disables new work and requires archival disposition or refuses destructive removal while obligations remain.
- Artifacts, operation uncertainty and accounting obligations are not discarded.

### Z04.old_authority — Z04

Setup: A principal may edit a draft but cannot expand its own effective grants.

Action: Submit a plan containing a policy or grant that would authorize its own application.

Expected:

- Apply evaluates existing authority and rejects or requests an eligible exact decision.
- Proposed policy and imported instructions cannot authorize their own activation.

### Z04.submission_replay — Z04

Setup: An apply has committed but its acknowledgement is lost.

Action: Retry with the same principal, operation version, submission key and canonical input; then reuse that key with different input.

Expected:

- Identical retry returns the original durable command disposition without another revision or event.
- Changed input is refused; command identity survives restart and remains retained through unresolved obligations and at least 30 days.

### Z05.malicious_instructions — Z05

Setup: A skill includes instructions to read secrets, self-grant permissions and ignore reviews.

Action: Import, bind and execute the skill in a constrained task.

Expected:

- Instructions remain untrusted input and install no grants.
- Tool, data and effect access remains limited to the pinned authorized binding closure.

### Z05.archive_safety — Z05

Setup: Prepared archives contain traversal, absolute paths, symlink escapes, device entries, duplicate/case-colliding paths, oversized expansion and dependency cycles.

Action: Import every malicious archive plus a bounded valid control archive.

Expected:

- Every unsafe archive is rejected before publication and performs no executable side effects.
- Valid import produces immutable hashed draft content and source/license provenance; discovery and extraction execute no scripts.

### Z05.changed_tool_schema — Z05

Setup: An active worker binding pins a tool identity/version/schema.

Action: Change the provider-discovered schema or executable binding while retaining the old activation plan.

Expected:

- Changed executable meaning cannot silently enter the active tool closure.
- Revalidation and authorized activation are required; unsupported contracts return capability or prerequisite faults.

### Z05.callback_discovery — Z05

Setup: A discovered tool or HTTP response advertises a callback, redirect, external server or subprocess.

Action: Attempt to follow that behavior outside the registered reviewed destination/adapter contract.

Expected:

- Discovery grants no execution or disclosure authority.
- Unreviewed callbacks, destination changes and arbitrary external MCP/subprocess execution are refused.

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

### Z06.crash_call_counts — Z06

Setup: A controlled provider counts physical invocations and fault injection surrounds admit, claim, network return and record.

Action: Crash and restart at each boundary, then inspect and reconcile the operation.

Expected:

- No crash recovery blindly repeats a potentially sent effect.
- Attempt records and provider call counts correspond exactly; failed record persistence does not authorize another send.

### Z06.no_hidden_retries — Z06

Setup: Qualified adapters use a provider simulator with transient errors, timeouts and connection resets.

Action: Invoke each adapter once under a contract that has not established safe retry.

Expected:

- The adapter performs exactly one physical request per claimed attempt.
- SDK or transport mutation retries are disabled; any permitted retry receives a new authorized recorded attempt.

### Z07.exact_action_changes — Z07

Setup: An approved action binds account, destination, content/media hashes, timing, preconditions, repository head and relevant versions.

Action: Independently change content, destination, timing, repository head or account before dispatch.

Expected:

- Every material change invalidates the original digest-bound approval where review is required.
- Dispatch waits for a new eligible decision over the exact changed preview.

### Z07.agent_impersonates_human — Z07

Setup: A pending review requires a human principal and an agent can call review.decide.

Action: Submit an approval with an approved_by_human assertion using both transports.

Expected:

- The agent credential cannot satisfy the review regardless of its payload or transport.
- No approval or protected dispatch is committed.

### Z07.reviewer_eligibility — Z07

Setup: Reviews include current version, expiry, proposer-separation and reviewer delegation restrictions.

Action: Decide using revoked or ineligible delegated credentials, an expired review, a stale version or the prohibited proposer.

Expected:

- Each ineligible decision is refused using current identity and policy.
- A permitted delegation is scoped and cannot bypass human-required or proposer-separation rules.

### Z07.transport_denial — Z07

Setup: Matching controller fixtures contain an action denied to an agent through MCP.

Action: Submit the identical decision using CLI with the equivalent agent principal; then use an eligible human principal through each interface.

Expected:

- CLI cannot bypass the MCP denial.
- An eligible human session can decide through either transport without any claim of proving physical human presence.

### Z08.lost_success_response — Z08

Setup: A provider commits a mutation but drops the response before Zatiti records its outcome.

Action: Restart or timeout the sender and inspect the logical operation.

Expected:

- The operation remains outcome_unknown with retained reservation and its original attempt.
- Neither the task result nor the command reports the external write as confirmed successful or safely unsent.

### Z08.failed_retry_after_unknown — Z08

Setup: An earlier attempt has an unresolved potentially successful outcome; a qualified contract permits a later attempt.

Action: Run the later attempt and return a definitive failure for that attempt.

Expected:

- Earlier uncertainty remains attached to the logical operation.
- Failed retry does not erase the earlier attempt, release its unknown reservation or prove no external effect occurred.

### Z08.expired_provider_key — Z08

Setup: A provider idempotency key has exceeded its qualified retention window for an unresolved operation.

Action: Attempt automatic redispatch using the old key.

Expected:

- The expired key is not treated as proof of safe idempotency.
- Dispatch is blocked unless authoritative nonexecution or another qualified safe contract is established.

### Z08.delayed_confirmation — Z08

Setup: An operation awaits confirmation or is outcome_unknown and the provider initially returns eventually consistent not-found.

Action: Reconcile, then deliver delayed authoritative success; separately deliver contradictory late evidence.

Expected:

- Non-authoritative not-found does not establish nonexecution.
- Authoritative success settles according to evidence; contradictory evidence creates a visible correction or dispute instead of rewriting history.

### Z08.cancel_unknown — Z08

Setup: An effect is already claimed and may have reached its provider.

Action: Cancel the task or operation and acknowledge the uncertainty as an operator.

Expected:

- Cancellation records restrictive intent but does not reclassify the possibly sent attempt as unsent.
- Unknown outcomes and reservations remain inspectable until supported evidence resolves them.

### Z09.concurrent_reservations — Z09

Setup: Installation, ancestor organizations, project, worker and root-task budgets are nearly exhausted.

Action: Concurrently admit child tasks, evaluation and model calls competing for the remaining funds.

Expected:

- Atomic reservations across all applicable scopes prevent overcommit and use deterministic lock/update order.
- Children and retries consume shared root/ancestor limits rather than independent copies.

### Z09.unknown_external_cost — Z09

Setup: A cooperative external worker or provider cannot establish a reliable charge bound.

Action: Admit work requiring a hard spending cap and report unknown/advisory external usage.

Expected:

- Unenforceable hard-cap mode is refused or explicitly advisory only where policy permits.
- Unknown and advisory usage is reported separately and never treated as zero.

### Z09.missing_price — Z09

Setup: A model profile lacks a required price bound, currency or configured spending limit.

Action: Attempt a paid model step or memory/evaluation operation requiring that provider.

Expected:

- Admission returns a named prerequisite or budget refusal without a provider call.
- There is no hardcoded account, provider, billing or model fallback.

### Z09.settlement_recovery — Z09

Setup: Reservations include confirmed spend, refundable unused bounds and unknown attempts using exact micro-units and rational rates.

Action: Record each disposition with a crash before or after transaction commit and recover.

Expected:

- Spent, reserved, estimated and unknown values remain distinct and exact without floating-point drift.
- Settlement is atomic and idempotent; unresolved reservations survive restart and are not released twice.

### Z10.duplicate_schedule_claim — Z10

Setup: One scheduled occurrence is due and multiple polls and cooperative claimants race.

Action: Poll repeatedly across timezone/DST boundaries and concurrently claim the same task.

Expected:

- The occurrence key admits at most one occurrence according to the configured misfire rule.
- Only one current attempt owner, lease and generation are committed.

### Z10.restart_identity — Z10

Setup: A task has a pinned run, active attempt, checkpoint and resource reservation.

Action: Restart the controller and continue through the supported recovery path.

Expected:

- Persisted generation advances and recovery uses durable state.
- Task/run history, accepted contract, pinned configuration and checkpoint lineage remain intact; retry creates a distinct attempt.

### Z10.stale_heartbeat — Z10

Setup: An attempt lease/generation is stale after expiry or controller restart.

Action: Send heartbeat, checkpoint and completion report from that stale owner or a different worker.

Expected:

- Stale or mismatched worker/attempt/generation requests cannot mutate current execution or complete the task.
- Lease expiry alone does not assert that the old external process stopped.

### Z10.lost_claim_ack — Z10

Setup: A cooperative claim commits and its network acknowledgement is dropped.

Action: Repeat the claim with the same submission identity and inspect the command.

Expected:

- The claimant recovers the original attempt/lease disposition.
- No duplicate current owner or replacement attempt is created merely because the acknowledgement was lost.

### Z10.cancelled_wake — Z10

Setup: A task is durably waiting for a timer or authenticated reply event.

Action: Commit cancellation or a qualifying reply concurrently with wake evaluation.

Expected:

- Wake conditions are checked transactionally.
- Cancelled work is not readmitted; a reply already recorded does not cause duplicate or obsolete wake work.

### Z10.conflicting_replacement — Z10

Setup: An expired attempt may still control a repository resource or have an unresolved provider effect.

Action: Request a replacement attempt.

Expected:

- Replacement waits for explicit recovery disposition of conflicting resources and possible effects.
- No unsupported containment or successful stop is inferred from lease expiry.

### Z11.exit_zero_missing_output — Z11

Setup: A task requires independently verified artifacts.

Action: Have its worker exit successfully or report success while omitting required output bytes.

Expected:

- Task does not enter succeeded.
- Missing artifacts produce a visible fault and dependents requiring verified success remain blocked.

### Z11.tampered_verifier — Z11

Setup: A task pins verifier code/version, sealed inputs and expected observations.

Action: Modify verifier bytes/version or attempt to replace acceptance during the run or retry.

Expected:

- Verification rejects a mismatched or unapproved verifier/contract.
- A worker cannot weaken the accepted contract during execution; retries retain it.

### Z11.worker_assertion — Z11

Setup: A worker submits plausible narrative success and self-generated tests.

Action: Report completion without independently accepted check evidence.

Expected:

- Worker assertions are recorded as observations rather than proof.
- Independent verification or an authorized explicitly manual acceptance is still required.

### Z11.failed_child_acceptance — Z11

Setup: A parent acceptance contract requires a designated child to pass independent checks.

Action: Have that child finish execution but fail verification.

Expected:

- The parent and dependent tasks do not satisfy verified-success conditions.
- Failed child acceptance and its evidence remain visible.

### Z11.manual_acceptance_label — Z11

Setup: A task contract permits eligible manual acceptance and has no passing automated result.

Action: Accept using the eligible principal, then inspect CLI/MCP results and evidence.

Expected:

- Acceptance remains explicitly labeled manual.
- The product never presents manual approval as successful independent automated verification.

### Z12.delegation_narrows — Z12

Setup: A root task has finite scope, allowed tools/destinations, project data, deadline and shared budget.

Action: Independently delegate children expanding scope, tool access, data access, deadline or spending authority.

Expected:

- Each expansion is refused or reduced only through an explicit valid narrowing contract.
- Delegation cannot grant authority absent from its parent or inherited organization policy.

### Z12.root_limits_shared — Z12

Setup: A root task is configured with finite child count, depth, concurrency, planner steps and wall-time limits.

Action: Delegate concurrently and recursively until each limit is reached.

Expected:

- All branches share root budgets and finite limits; limits cannot be reset by making another child.
- Default limits match one worker attempt, four installation attempts, 30 minutes per attempt, 100 steps, eight children, depth three and a 24-hour root deadline where applicable.

### Z13.secret_surfaces — Z13

Setup: Synthetic uniquely marked secrets exist in owner/provider secure stores and setup helpers.

Action: Exercise setup, rotation, failure logging, CLI/MCP output, organization export, artifact metadata and diagnostics.

Expected:

- No planted raw credential is exposed in tool arguments, ordinary flags, CLI/MCP results, logs or organization exports.
- Setup exposes challenge references and metadata; subprocesses do not inherit owner credential environment by default.

### Z13.advisory_worker — Z13

Setup: A cooperative worker has an unrestricted external shell/network and no qualified containment adapter.

Action: Inspect capabilities, claim context, usage reports and desktop execution details.

Expected:

- External shell, network and billing limitations are explicitly advisory.
- Lease fencing is described as restricting Zatiti mutations and does not claim the external process is contained or terminated.

### Z13.compatibility_not_containment — Z13

Setup: An MCP session succeeds with a named external agent client.

Action: Evaluate execution capability declarations and release claims.

Expected:

- Client interoperability grants no implicit launch, sandbox, interruption, cost-enforcement or exact-context-replay guarantees.
- Unsupported executor guarantees fail explicitly instead of being inferred from a client, harness, container or subprocess name.

### Z14.atomic_state_event — Z14

Setup: Fault injection can fail event/outbox insertion or state persistence inside a shared transaction.

Action: Perform representative configuration, task and effect transitions with each failure injected.

Expected:

- Neither state without its required event nor an event without its state becomes committed.
- Recovery reads durable records and does not infer success from log text.

### Z14.missing_artifact — Z14

Setup: Metadata references a committed content hash whose bytes are missing or corrupt.

Action: Read the artifact, verify its task and create or inspect a backup.

Expected:

- A visible integrity/artifact fault is returned.
- The missing output is never a successful task result or silently accepted backup; unreferenced staging bytes can be reclaimed separately.

### Z14.paused_restore — Z14

Setup: A verified encrypted backup contains a consistent database snapshot and pinned artifact manifest with separately available key prerequisites.

Action: Enter exclusive maintenance and restore into a clean destination.

Expected:

- Restore verifies contents and starts paused with exclusive ownership.
- No pending paid work or effect dispatch resumes automatically; a live-database file copy alone is not accepted as the backup contract.

### Z14.outbox_revocation_restore — Z14

Setup: The backup contains unresolved dispatch/outbox intents, unknown reservations, revoked credentials and pending memory writes/promotions.

Action: Restore, inspect state, attempt use of revoked credentials and reconcile before resuming.

Expected:

- Unresolved obligations and revocation states are preserved and inspectable.
- Pending effect and memory outcomes require reconciliation; restoring an older database does not claim to undo provider effects.

### Z15.canonical_roundtrip — Z15

Setup: A definition contains ordered semantic lists, unordered maps/sets, exact skill bytes, extensions and explicit removals.

Action: Export, import, plan, activate and export again within the same installation.

Expected:

- Canonical definition meaning and stable identities round-trip without changing semantic order or skill bytes.
- Duplicate keys, invalid references, unsafe paths, unknown executable fields and cycles are rejected; omission is not deletion.

### Z15.cross_installation_rebind — Z15

Setup: An organization export contains stable IDs and opaque connection references from installation A.

Action: Import into installation B and attempt activation before and after explicit local credential/account rebinding.

Expected:

- Foreign credential references do not become live authority automatically.
- Required rebinding is surfaced; authorized activation uses B-specific bindings while preserving portable definition identity semantics.

### Z15.export_exclusions — Z15

Setup: An organization owns credentials, completed tasks, unknown operations and run history.

Action: Create an organization export and separately request run history export and encrypted backup.

Expected:

- Organization export includes versioned definitions but excludes secret values, principal credentials, task history and mutable operation state.
- History export and backup have distinct contracts; required runtime obligations remain in state and backup.

### Z16.cli_to_mcp — Z16

Setup: A scoped principal creates and starts durable work through CLI.

Action: Disconnect the CLI and inspect, decide or continue that same work through MCP.

Expected:

- The second transport uses the same task, command, review, state and authorization.
- No duplicate controller, scheduler or task is created.

### Z16.mcp_to_cli — Z16

Setup: A scoped principal creates and starts durable work through MCP.

Action: Disconnect MCP and continue that work using CLI.

Expected:

- Equivalent authorized state and durable identities are preserved across transports.
- No transport-specific prerequisite is required to continue the workflow.

### Z16.disconnect_command_lookup — Z16

Setup: A mutation commits immediately before its transport connection drops.

Action: Reconnect, look up the command by its original submission key and retry only with that key.

Expected:

- The original disposition is recovered without another business mutation or event.
- JSON-RPC request IDs are not treated as durable submission keys.

### Z16.cursor_expiry — Z16

Setup: An event cursor falls outside the retained replay window while scoped state continues changing.

Action: Resume event reading with that cursor.

Expected:

- A named cursor-expiry result requires a fresh authorized snapshot and new replay position.
- The client never presents an unseen event gap as complete history; both transports preserve equivalent scope and freshness.

### Z16.interrupted_upload — Z16

Setup: A bounded chunk upload is partially accepted before a client disconnects.

Action: Resume or inspect using the stable upload identity, retry an accepted chunk, finish and cancel a separate partial upload.

Expected:

- Chunk retries do not corrupt content or publish duplicate artifacts; finish verifies the full digest and bounds.
- Partial bytes are not published as completed artifacts; cancellation leaves only reclaimable staging data.
- Neither interface accepts an unrestricted controller filesystem path.

### Z16.maintenance_restore_parity — Z16

Setup: A local owner has uploaded a valid encrypted backup artifact.

Action: Enter quiesced maintenance and restore through CLI on one fixture and MCP on another.

Expected:

- Both invoke the same authorized owner operation using opaque backup references.
- No transport-specific admin endpoint is necessary; both restore paused with equivalent preserved obligations.

### Z17.atomic_chief_creation — Z17

Setup: A root organization exists and a permitted author proposes a child organization with its chief.

Action: Apply successfully, then inject a transaction failure during a second child creation.

Expected:

- Each active organization has exactly one designated chief.
- Organization and chief activate together or neither activates; creating an ordinary worker does not create an organization.

### Z17.hierarchy_cycles — Z17

Setup: Organizations form a valid root/child/grandchild tree.

Action: Attempt to parent the root under a descendant or create any cycle.

Expected:

- Planning or activation rejects cycles.
- The original hierarchy and inherited limits remain unchanged.

### Z17.chief_replacement — Z17

Setup: An organization has a designated chief, shared memory, responsibilities, pending decisions and history.

Action: Replace its chief using an authorized exact configuration plan.

Expected:

- The organization keeps its identity, memory, obligations and history.
- Exactly one active chief remains; replacement does not silently grant private memories or excess authority.

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

### Z18.chief_promotion_provenance — Z18

Setup: A chief has explicit source read/disclosure and destination curate/write permissions under finite budgets.

Action: Promote a suitable claim from worker or child-organization memory into shared memory.

Expected:

- The new destination claim links source brain, claim/version, supporting artifacts, curator identity and redaction.
- Promotion is scoped admitted work; the copied claim is not independent corroboration of its source.

### Z18.promotion_correction — Z18

Setup: A source claim has several recorded promoted copies and one destination writer is unavailable.

Action: Correct or retract the source, crash/restart and resume reconciliation.

Expected:

- Durable lineage obligations cover every affected promoted copy and survive restart.
- Unavailable reconciliation remains visible; forget/retract distinguishes active recall removal from erasure of Git history, backups and previous run artifacts.

### Z18.stale_index — Z18

Setup: A permitted brain has an index behind the required source revision or a failed writer.

Action: Recall for a task requiring current freshness and inspect memory status.

Expected:

- Results report source/scope/version/confidence/freshness where available.
- The task waits or receives a named prerequisite failure rather than silently treating stale content as current; failed writes are not presented as remembered knowledge.

### Z18.retained_context — Z18

Setup: A run recalls several authorized brains with distinct revisions.

Action: Dispatch a model step, later update the brains and inspect the original run context.

Expected:

- The run retains the actual selected memory excerpts and provenance as artifacts.
- Inspection reconstructs what the model received; reads across brains are not falsely described as one atomic snapshot.

### Z18.memory_write_unknown — Z18

Setup: A Serenity writer commits a command but its acknowledgement is lost; recall may also incur model charges.

Action: Recover the command and run recall under a hard disclosure/spend bound.

Expected:

- The prior persisted intent and adapter command identity are reconciled without blind repeat writing.
- Exactly one canonical writer owns each brain; unsupported enforceable recall charge/disclosure bounds make that mode unavailable or explicitly advisory where policy permits.

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

### Z20.scheduled_cycles — Z20

Setup: A responsibility specifies scheduled/event triggers, finite cycle/root limits, timezone and catch-up window.

Action: Trigger normal, missed and duplicated occurrences and an authenticated event.

Expected:

- Each admitted cycle is bounded and linked to the responsibility; repeated polls/events do not duplicate task admission.
- Default missed occurrences coalesce once within the configured window and pauses disable new admission.

### Z20.reasoning_cycles — Z20

Setup: An active responsibility permits repeated reasoning without new external events and has a minimum reconsideration interval.

Action: Run a reasoning cycle that proposes useful work, then inspect its next wake and outputs.

Expected:

- The cycle is an admitted bounded run with accountable outputs and a persisted next-wake decision.
- Proposed work converges on versioned tasks and stays within task-count, scope and spending limits.

### Z20.bounded_idle_spend — Z20

Setup: A responsibility repeatedly finds no useful work and has finite per-cycle/aggregate funds and a minimum interval.

Action: Advance the deterministic clock through many potential cycles.

Expected:

- The minimum interval prevents a tight reasoning loop.
- Aggregate exhaustion or pause conditions stop new spend; idle reasoning cannot create unlimited tasks or spend indefinitely.

### Z20.restart_at_wake — Z20

Setup: A durable responsibility wake is due and admission has a controllable transaction boundary.

Action: Crash immediately before and after wake admission, then restart.

Expected:

- Recovery preserves the correct next-wake decision and occurrence identity.
- Exactly the intended bounded cycle is admitted without loss or duplication.

### Z20.mailbox_redelivery — Z20

Setup: A scoped sender submits a stable message ID to a running worker and an idle worker.

Action: Lose delivery acknowledgement, redeliver and restart at target admission.

Expected:

- Acknowledgement occurs only after durable target admission and message identity deduplicates redelivery.
- Running workers receive the message at a safe step boundary; idle workers can resume.
- Message content is an untrusted observation and cannot replace authority.

### Z20.duplicate_assignment_conflict — Z20

Setup: Direct user and chief conversations reference the same durable task/version.

Action: Submit conflicting assignment or pending-input updates concurrently.

Expected:

- One accepted version is authoritative and the conflicting update is surfaced.
- The system does not independently schedule duplicate work from the competing conversations.

### Z20.responsibility_pause — Z20

Setup: Two responsibilities have active/queued work and only one is selected for pause.

Action: Pause that responsibility and race its next wake.

Expected:

- Restrictive state commits immediately and prevents new admission for that responsibility.
- Unrelated work is not cancelled; already transmitted effects and unresolved outcomes remain visible.

### Z21.first_conversation — Z21

Setup: Start the real desktop on a clean supported installation with bootstrap/provider prerequisites handled through its UI.

Action: Complete first launch and relaunch after selecting a different conversation.

Expected:

- First launch opens the pinned personal-chief conversation with a ready composer.
- Returning launch restores the current conversation; no terminal, operation ID or schema knowledge is required.

### Z21.two_child_chiefs — Z21

Setup: The personal chief has permitted organization-authoring authority.

Action: Ask it to create marketing and engineering chiefs and build their teams; separately use New organization and New worker.

Expected:

- Each chief creation stages and activates its child organization and chief atomically.
- Cards distinguish proposed, awaiting decision and created using committed controller state; ordinary workers do not create organizations.
- Direct and conversational controls operate the same durable objects.

### Z21.delegate_work — Z21

Setup: A real desktop session has personal, marketing and engineering chiefs plus ordinary workers.

Action: Assign a bounded task and an ongoing responsibility, delegate work, then inspect a produced result.

Expected:

- The UI exposes durable task/responsibility state, required outputs and acceptance evidence outside message narration.
- Failed checks cannot be represented as successful completion; the responsibility remains independently pausable.

### Z21.hierarchy_navigation — Z21

Setup: Multiple branches contain workers with duplicate display names and at least one nested organization.

Action: Use All chats, organization hierarchy filtering, worker search and clickable breadcrumb navigation.

Expected:

- Organization filtering includes its descendants without forcing an administrative landing page.
- Worker labels and full ancestry disambiguate identities; membership is not conveyed by color alone.
- Displayed hierarchy agrees with controller state.

### Z21.exact_decision — Z21

Setup: Several organizations contain unresolved human-required actions and one stale reviewed action.

Action: Open Needs you, inspect an exact action card, approve one eligible action and try the stale action.

Expected:

- The card shows the actual action/destination/content/preconditions and acknowledged disposition with evidence links.
- The stale action cannot reuse approval; Needs you links to unresolved exact decisions.
- A chat assertion does not serve as approval or completion authority.

### Z21.reconnect_no_duplicate — Z21

Setup: The desktop sends a mutation whose response is lost before disconnect.

Action: Show cached history, enter an offline draft, reconnect and retry with original submission identity.

Expected:

- Unsent input and stale cached state remain distinguishable until controller acknowledgement.
- Reconnection restores snapshots/replay and command disposition without duplicate tasks or external effects.

### Z21.close_client — Z21

Setup: Authorized work is running on an available controller and its continuation behavior was shown during setup.

Action: Close the desktop process, allow work to continue, then reopen it.

Expected:

- Closing the client does not stop the controller or scheduler.
- Reopened state accurately shows controller progress/results and unresolved effects.

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

### JOURNEY.organization_setup_cli — JOURNEY

Setup: Use a fresh controlled installation and scoped external agent principal.

Action: Discover capabilities, create an organization and two workers, import/evaluate a skill, establish a provider connection, plan/apply and complete first work entirely through CLI.

Expected:

- The first artifact is independently verified and inspectable.
- Provider secrets never enter agent-visible arguments/results; prerequisite resolution requires no dashboard or source knowledge.
- Retain operation traces, state/event observations and verification artifacts.

### JOURNEY.organization_setup_mcp — JOURNEY

Setup: Use a matching fresh installation with a scoped agent MCP client.

Action: Perform the full organization, two-worker, skill evaluation, provider setup, activation and first verified artifact journey entirely through MCP.

Expected:

- The same authorized product lifecycle and evidence is obtained as through CLI.
- Every prerequisite and long-running operation remains discoverable and inspectable without optional MCP features.

### JOURNEY.engineering_cli — JOURNEY

Setup: A controlled repository has an explicit base head, policy, cooperative worker and independent sealed verifier.

Action: Through CLI, claim work, checkpoint, submit a patch and independent check evidence, then propose exact repository publication; also run failed-check and stale-head variants.

Expected:

- Successful acceptance is independently established and publication is governed by exact repository/branch/head/change/automation constraints.
- Failed checks block completion and stale head invalidates applicable review.
- Opening a pull request is not automatically treated as harmless when downstream automation broadens consequences.

### JOURNEY.engineering_mcp — JOURNEY

Setup: Use matching repository, worker, policy and sealed verification fixtures.

Action: Perform claim, checkpoint, patch/evidence submission and exact publication proposal entirely through MCP, including failed checks and stale head.

Expected:

- The same verified acceptance and protected-effect lifecycle is observed as through CLI.
- No arbitrary shell invocation exposed as an MCP substitute counts toward operation parity.

### JOURNEY.research_drafting_cli — JOURNEY

Setup: A hosted worker has an authorized model profile, bounded public HTTP destinations and finite cost/disclosure limits.

Action: Through CLI assign research, gather controlled public sources, produce a cited brief and draft artifact and inspect verification.

Expected:

- The hosted loop uses governed bounded reads/model calls and retains reconstructable context and source references.
- Artifacts satisfy acceptance and no social publishing adapter is required.

### JOURNEY.research_drafting_mcp — JOURNEY

Setup: Use matching hosted worker, public source fixtures and policy limits.

Action: Perform the research and cited-brief/draft workflow entirely through MCP.

Expected:

- The same scope, disclosure, accounting, context and artifact guarantees are observed as through CLI.
- Long-running work is followed through durable task/command IDs and polling.

### JOURNEY.cross_interface_fault_matrix — JOURNEY

Setup: Prepare each organization, engineering and research journey at mutation acknowledgement, claim, checkpoint, review, upload, effect return and restart boundaries.

Action: Interrupt at each boundary and continue through the opposite interface in both directions.

Expected:

- Submission identity, pinned contracts, events and authorization remain equivalent.
- No duplicate protected effect occurs; unknown outcomes remain unknown until qualified reconciliation.
- The evidence names each fault boundary and both starting/continuing transports.

### JOURNEY.desktop_daily_work — JOURNEY

Setup: Launch the actual packaged desktop on a supported platform with a personal-chief conversation.

Action: Create marketing/engineering organizations and ordinary workers, assign a bounded task and ongoing responsibility, inspect result and memory, then handle an exact pending decision.

Expected:

- The visible hierarchy and work states match queried controller state rather than model narration.
- Memory scope/lineage and earned autonomy meet their own Z18/Z19 gates.
- Capture executed UI observations for setup and daily results/decisions; mocked view models alone are insufficient.

### QUALIFICATION.macos_distribution — QUALIFICATION

Setup: Build signed and notarized macOS release artifacts for native arm64 and amd64 with pinned Go/dependency/protocol/service versions and licenses.

Action: Install on clean native Apple Silicon and Intel macOS hosts without developer tools; bootstrap, complete provider and limits setup in the desktop, receive a real first reply, run CLI/MCP/desktop journeys, stop/restart controller, back up/restore and uninstall according to documentation.

Expected:

- Documented installation commands work from actual artifacts; private socket, secure-store and local-lock behavior are exercised.
- Hosted Serenity OAuth, verified existing-brain binding, scoped memory and reconnection work with the controller/desktop; no local Serenity process is required in hosted mode.
- Both native architectures pass with signed/notarized installed artifacts; release evidence identifies exact OS/architecture/artifact/source/tool versions and observed results. One architecture or compile success alone is not Mac release qualification.

### QUALIFICATION.linux_distribution — QUALIFICATION

Setup: After the Mac release, build the documented Linux release artifacts with pinned dependencies and an explicitly provisioned supported secret source. Linux support remains unadvertised until this separate qualification passes.

Action: Install on a clean supported Linux host and run the same controller, CLI/MCP, desktop, restart and backup/restore journeys.

Expected:

- Documented artifact installation and headless/desktop credential prerequisites work without secret exposure.
- Controller ownership, Serenity lifecycle and desktop continuation are exercised on Linux itself.
- Advertised Linux targets match executed OS/architecture evidence; Windows support is not inferred.

### QUALIFICATION.named_agent_clients — QUALIFICATION

Setup: Select exact Claude Code, Codex or Cursor versions for each client compatibility claim intended for release.

Action: Use actual sessions of every claimed version to discover tools, perform setup, execute long work, encounter denial and recover a disconnected mutation.

Expected:

- Each named client/version has retained executed interoperability evidence.
- Unsupported or untested clients are not advertised as qualified and client compatibility is not an executor-containment claim.

### QUALIFICATION.baseline_mcp — QUALIFICATION

Setup: A pinned protocol client disables resources, prompts, sampling, elicitation and protocol task extensions.

Action: Discover and operate all required product capabilities, including setup challenges, long work and recovery.

Expected:

- Baseline stdio tools and durable polling provide complete authorized functionality without optional features.
- Input/output schemas, result/error mapping and negotiated protocol behavior match the pinned supported revision.

### QUALIFICATION.adapter_bounds — QUALIFICATION

Setup: Pin public provider, GitHub, bounded HTTP and Serenity adapters with controlled fault simulators and authorized live qualification fixtures where required.

Action: Exercise cost bounds, disclosure destinations, timeouts, retries, confirmations, repository-head checks, memory freshness and lost acknowledgements.

Expected:

- Record exact adapter/source/protocol/profile versions and observed physical calls.
- Unsupported hard caps, safe-idempotency windows, continuation/context guarantees or memory operations remain unavailable or explicitly advisory as permitted, never inferred from upstream claims.

### QUALIFICATION.release_evidence — QUALIFICATION

Setup: All named acceptance cases have executable harness identities and retained case results.

Action: Assemble the release report and audit skipped, planned, failed and passing runs against Z01-Z21 and required journeys.

Expected:

- Each required passing case records source/config/tool versions, expected and observed results, and retained failure evidence from fault tests.
- Planned tests, skipped suites, golden files alone and a zero process exit do not satisfy release gates.
- README and installation/client/platform claims match executed evidence; public fixtures use synthetic identities and contain no private operational data.

### P00.worker_turn_admission_idempotent — P00

Setup: A worker is bound and a message names it as recipient; no WorkerTurn yet exists for that (source_kind, source_id, source_version, recipient_worker_id).

Action: Call _execution.turn.admit twice with the identical source identity, once as the original admission and once after a simulated lost-acknowledgement retry.

Expected:

- Both calls return the same WorkerTurn id and version.
- No second turn or duplicate execution_turns row is created.
- A message admitted mid-turn is linked to the active turn as a safe-boundary injection rather than dropped or processed as a second turn.

### P00.proposal_duplicate_and_conflict — P00

Setup: A WorkerTurn is in proposal_pending state with one recorded ProposalRecord at (turn_id, step_index=1, proposal_id=P1).

Action: Call _execution.proposal.record again with the identical proposal_id and identical result, then again with the same proposal_id and a different result.

Expected:

- The identical repeat replays the original recorded disposition without creating a second record.
- The differing repeat is refused submission_conflict and the original record is unchanged.

### P00.effects_callback_route_not_in_adapter_params — P00

Setup: Execution prepares a model_step effect for a WorkerTurn step and passes callback_route naming that turn/step.

Action: Claim and dispatch the effect through the controller, inspect the exact adapter Dispatch.Action bytes sent to the Responses adapter, then resolve the callback using only Dispatch.callback_route after the observation returns.

Expected:

- Dispatch.Action contains no attempt_id, run_id or any field beyond ResponsesModelStepParameters' declared schema; an inserted attempt_id is rejected by the adapter's strict decode.
- The controller correctly routes the recorded proposal back to the originating turn/step using only callback_route.

### P00.effects_successor_generation_resolves_stray_attempt — P00

Setup: An effect attempt is claimed under generation N; the controller restarts and advances to generation N+1 before any record is written for that attempt.

Action: Call _effects.record for the stray attempt with current_generation=N+1, once for an attempt that was never actually claimed under N and once for one that was claimed but unconfirmed.

Expected:

- The never-claimed attempt records not_sent.
- The claimed-but-unconfirmed attempt records outcome_unknown, never succeeded or failed.
- Neither call silently drops the attempt or loses its reservation.

### P00.job_registry_operation_id_linkage — P00

Setup: Memory prepares a network-backed remember effect and calls _execution.job.create with operation_id naming that effect's Operation.

Action: Read the job back through public job.get, then trigger reconciliation for the underlying effect.

Expected:

- job.get's Job.operation_id matches the effects Operation id supplied at creation.
- Reconciliation resolves the job's originating effect directly from operation_id without an owner-table scan.
- A job created without operation_id behaves as an ordinary ownerless local runner and is unaffected.

### P00.responses_prepare_session_then_model_step — P00

Setup: A hosted worker turn requires its first model call against a profile with no existing session_handle.

Action: Dispatch prepare_session, record its evidence, then dispatch model_step naming the returned session_handle; separately simulate a prepare_session whose HTTP response is lost after the request was sent.

Expected:

- Exactly two physical calls occur, one per Invoke, never both inside one Invoke.
- model_step's persisted session_handle equals prepare_session's returned session_handle.
- The lost-response prepare_session stays outcome_unknown and no second session is created or retried automatically.

### P00.task_start_readies_and_enqueues — P00

Setup: A draft task exists with satisfied dependencies, a pinned acceptance contract and no run.

Action: Call task.start with the task's expected_version.

Expected:

- The task transitions to ready and exactly one Run is created for it in the same transaction.
- task.create and task.assign remain unchanged: create still yields draft, assign still only changes worker/version.
- A task with an unresolved required dependency is refused rather than readied.

### P00.output_slot_binding_and_verification_request — P00

Setup: A task's acceptance contract names one required output slot (name, classification, media_type, max_bytes, artifact_digest check).

Action: Report the attempt with a published artifact bound to that slot, then inspect the sealed VerificationRequest and attempt a byte change after binding.

Expected:

- VerificationRequest.Outputs contains the bound slot with its resolved artifact, not an empty array.
- The bound digest cannot be changed after binding; a differing report is refused.
- A report omitting the required slot never reaches succeeded.

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

### P00.restore_protocol_paused_and_resume_rechecks — P00

Setup: A verified encrypted backup bundle exists for a distinct destination installation with at least one revoked credential and one unresolved effect recorded after the backup was taken.

Action: Run RestoreCoordinator.Prepare then Commit against the destination, inspect the paused result, then call the explicit authorized resume.

Expected:

- Restore completes paused, not resumed, and names its recovery overlay.
- The revocation recorded after the backup is preserved after restore; the credential is not resurrected.
- Resume rechecks prerequisites again rather than assuming the paused state was already validated.

### P00.old_persisted_rows_remain_inspectable — P00

Setup: An Operation, Job, Status and Conversation row persisted before this revision lack attempts, callback_route, operation_id, runtime_ready and caller_unread_count/caller_last_read_marker respectively.

Action: Read each row back through its existing public/internal query operation after the revision-3 schema lands.

Expected:

- Every row validates against its updated schema with the new fields simply absent.
- No read fails, migrates destructively or fabricates a value for an absent optional field.
- Writing any of the rows again populates the new fields going forward without altering the unrelated persisted history.

### P00.worker_operation_denies_unlisted_operation — P00

Setup: A worker's local proposal names grant.create as its target operation, which is not on the local model-visible allowlist.

Action: Route the proposal through contract.WorkerOperator.ExecuteWorker.

Expected:

- The call is refused before reaching grant.create's handler.
- An allowlisted operation (for example task.delegate) submitted the same way for the same worker succeeds under that worker's own intersected scope.
- The controller's administrative identity is never substituted to force the denied call through.

### P00.render_check_clean_offline — P00

Setup: A clean checkout of tools/specgen and docs/implementation with no docs/rfc.md present and no network access.

Action: Run `python3 tools/specgen/render.py` then `python3 tools/specgen/render.py --check`.

Expected:

- Both commands exit 0 with no RFC file read and no network call.
- render --check reports zero stale/missing generated files.
- All prior Z01-Z21 and JOURNEY/QUALIFICATION cases remain present and unweakened in the regenerated coverage.json.

### QUALIFICATION.macos_pkg_binding — QUALIFICATION

Setup: Build one final signed/notarized pkg for each native Mac architecture, with exact component archives, signed release descriptor/delivery record, external Team ID pin and a clean user account. Include synthetic tampered fixtures.

Action: Inspect the real package payload and signer, run fixed current-user installer on clean hosts, rehash installed inbox, activate and audit both trees/launcher, then inject crash after package staging and after Apply before watermark publication.

Expected:

- Exact three-file package inbox and acyclic binding match the signed inner descriptor and both downloaded archives; scripts, extra choices/paths, wrong signer/arch and tampered payload are rejected before activation.
- Only audited active trees advance the protected fence; rerun after each crash preserves state and neither replays an ambiguous activation blindly nor treats a package receipt as evidence.
- Signed/notarized and Gatekeeper evidence is recorded for Intel and Apple Silicon with actual OS/tool/artifact versions; Xcode is not needed on the destination, and inability to use the fixed per-user target blocks release.

### QUALIFICATION.macos_gui_secret_helper — QUALIFICATION

Setup: Install the signed architecture-matched controller helper and Flutter Runner with a provisioned default login Keychain on clean Intel and Apple Silicon accounts; use synthetic credentials until explicit live-provider qualification.

Action: Capture, cancel, lock/refuse Keychain, invoke helper from an unsigned/other-team parent, lose setup.begin and setup.complete responses, crash after SecretStore.Put, then upgrade and reopen the app. Inspect argv/environment/channel/log/public-operation bytes.

Expected:

- Dart, argv, environment, output, logs and public operation inputs contain no raw provider key; native request/result fields are strictly bounded and redacted.
- Only the signed Runner launches the declared helper; wrong caller/signature, missing master item, locked Keychain and stale installation identity fail closed without plaintext fallback.
- Durable submission keys and challenge/connection status reconcile lost acknowledgements; the effective credential is retained, a stale orphan is removed only after authoritative check, and no second challenge/key is silently minted.
- Real signed GUI and Keychain ACL continuity pass separately on both native architectures; fake UI/store tests alone are not qualification.

### QUALIFICATION.macos_serenity_hard_gate — QUALIFICATION

Setup: Pin and verify the public hosted Serenity build/protocol and obtain an OAuth grant to an existing personal brain on clean native Intel and Apple Silicon hosts.

Action: Prove bearer-authenticated grant/brain identity, scoped recall and write/curation, canonical hosted writer, authoritative command disposition after lost acknowledgement, bounded spend/disclosure behavior, immutable exportable backup revision and paused restore reconciliation through public interfaces.

Expected:

- Each required guarantee has observed versioned evidence, including physical call counts and crash/unknown cases; a missing upstream method or missing qualification blocks the first-chat release.
- No reduced-capability preview, fake success, private Serenity API or empty-brain backup substitutes for the full requirement.

### QUALIFICATION.macos_bootstrap_entrypoint — QUALIFICATION

Setup: Build and host final script plus signed/notarized universal bootstrap app using external trusted signing inputs, then publish a literal command pinned to the exact script SHA on a reviewed release page.

Action: On clean native Intel and Apple Silicon hosts without developer tools, run the literal command, tamper each script/app archive hash and signature, attempt redirect and Rosetta execution, and inspect the selected signed delivery assets.

Expected:

- The command never executes network script bytes before verifying its literal SHA; the script verifies a separately pinned bootstrap app archive and exact code-signing/notarization identity before launch.
- The bootstrap app embeds trusted Ed25519 roots, never adopts a fetched key, detects native hardware under Rosetta and downloads only the matching controller/desktop/pkg assets.
- Actual hosted bytes, URL, command hash, Team ID, OS/arch and installer evidence are recorded; any missing artifact or failed clean-host run blocks the advertised one-line installation claim.

### QUALIFICATION.macos_install_to_first_chat — QUALIFICATION

Setup: Prepare the final signed release delivery, literal bootstrap script and universal app, both architecture-specific signed packages and a clean native Intel or Apple Silicon account without developer tools; provide qualified live provider and full public Serenity capabilities.

Action: On each native host run the exact one-line command, verify installed inbox and active tree/launcher/master Keychain against the same signed bytes, sign in to hosted Serenity through the system browser and verify the selected existing personal brain, capture a provider credential in the signed helper, send the first chief chat, and read the response and Serenity effects back from authoritative controller state. Repeat on the other native architecture and aggregate the two redacted host reports.

Expected:

- Each host records a unique host-run ID, native OS build, fixed installer invocation, actual signer Team ID/certificate, descriptor/delivery/script/universal-bootstrap digests and that host's package/controller/desktop/helper digests, with no credential or raw model text.
- The same run links package payload and installed inbox checks, retained master key, active trees/LaunchAgent, verified hosted OAuth account/project/scopes, real provider response readback and every full Serenity writer, memory, reconciliation, cost/disclosure and backup-revision guarantee; a synthetic or independent case cannot substitute.
- Both native reports agree on shared release identity and hashes, differ by host-run ID, and pass the linked case. Missing fields, Rosetta, skipped/not-run cases, no installed artifact, or unavailable upstream guarantee blocks the first-chat release and public one-line command.

### QUALIFICATION.macos_hosted_serenity_oauth — QUALIFICATION

Setup: A clean installed Mac account has access to an already populated hosted Serenity personal project and an installed signed Zatiti helper.

Action: Sign in through the system browser, select that project and read/write consent, complete the exact loopback callback, verify bearer-bound account/project/scopes, recall an existing fact, write and read back a new fact, then revoke/reconnect and exercise denied, expired, wrong-project and ambiguous-refresh cases.

Expected:

- No code/token/verifier reaches Dart, argv, public operations, logs or model context; the trusted connection records only observed grant metadata and a protected credential reference.
- The personal chief uses the selected pre-existing hosted brain, while unrelated scopes cannot read it; revocation and account/project substitution fail closed.
- Real hosted capability, command-status, spend/disclosure and immutable backup-revision evidence is required before the first-chat gate passes.

### P12.responses_v1_recovery — Z18

Setup: A persisted v1 profile/action/evidence fixture is replayed under revision 12.

Action: Exercise the revision 12 contract with synthetic immutable fixtures.

Expected:

- Strict v1 decoding succeeds unchanged and preserves its original evidence.

### P12.stateless_action — Z18

Setup: A stateless OpenRouter or Experiential action includes a continuation/session handle or omits required context.

Action: Exercise the revision 12 contract with synthetic immutable fixtures.

Expected:

- Strict schema rejects session_handle and continuation_reference in stateless mode, and rejects missing context or output bounds.

### P12.pinned_profile_recovery — Z18

Setup: A worker profile changes after an operation was prepared.

Action: Exercise the revision 12 contract with synthetic immutable fixtures.

Expected:

- Claim and reconciliation return the exact historical adapter profile and connection version; no latest profile is substituted.

### P12.turn_without_task — Z18

Setup: A chief chat model response is observed with no task or Attempt.

Action: Exercise the revision 12 contract with synthetic immutable fixtures.

Expected:

- The controller-only callback records the fenced WorkerTurn observation idempotently by operation ID.

### P12.provider_metadata — Z18

Setup: An authenticated client queries supported model providers.

Action: Exercise the revision 12 contract with synthetic immutable fixtures.

Expected:

- The fixed three provider presets and their session modes are returned without network access or credential/account claims.

### P12.exact_cost — Z18

Setup: Provider cost is absent, malformed, overflowing, fractional at micro-unit precision, or from BYOK upstream.

Action: Exercise the revision 12 contract with synthetic immutable fixtures.

Expected:

- Checked decimal arithmetic rounds upward; invalid/overflow/unpriced costs remain unknown/advisory and source decimal evidence is retained.

### P15.profile_qualification_requires_evidence_free_candidate — Z18

Setup: An authenticated human has an active provider connection and submits a candidate model profile for qualification.

Action: Attempt to include capability_evidence in the qualification candidate, then submit the strict evidence-free candidate bound to the exact connection version.

Expected:

- Client-authored capability evidence is rejected by schema validation.
- The accepted candidate is pinned to the exact connection, model, endpoint, route and profile digest; no credential is returned or accepted in the public operation.

### P15.profile_qualification_cost_and_authority — Z18

Setup: A profile candidate has explicit token/rate bounds and the caller has scope-limited provider disclosure authority.

Action: Qualify the exact profile with a maximum probe cost, and vary the current grant, destination, classification, profile digest and available budget before claim.

Expected:

- The one probe is admitted only under current authority, exact bound destination and classification, and sufficient reserved budget.
- Observed charge is settled against that probe; a profile mismatch or a charge beyond the configured bound never yields qualified evidence.

### P15.profile_qualification_unknown_never_replays — Z18

Setup: A qualification request may lose its provider response or controller acknowledgement after request bytes are sent.

Action: Crash or drop the response at each provider boundary, restart the controller and inspect/retry the same qualification job.

Expected:

- Possible-send outcomes remain outcome_unknown on the same job and are not automatically resent.
- Unknown, failed, stale-connection and stale-profile outcomes never produce a QualifiedExecutionProfile; a new probe requires authoritative proof the previous request was not sent.

### P15.desktop_model_profile_setup_and_first_chat — Z21

Setup: A live controller and Desktop have a usable secure credential helper, an active provider connection and sufficient explicit cost budget.

Action: In Desktop, configure a model/rates/resource bounds, approve one qualification spend bound, wait for the trusted result, review and apply the profile, then send a first chat.

Expected:

- Desktop submits no capability evidence and reconciles a lost acknowledgement through the same job/submission identity.
- The user reviews and applies the qualified profile through the ordinary compiler path; the first chat uses that exact qualified profile and complete persisted context.
- The reply is read back from authoritative controller conversation state, and provider request/response cost and unknown outcome behavior remain visible and bounded.

### P15.complete_authorized_chat_history — Z21

Setup: A worker is a current participant in a conversation with more than one previously admitted message, including a group conversation with historical membership changes.

Action: Send another chat turn and inspect the persisted context artifact and provider dispatch boundary.

Expected:

- The context contains the worker-visible sent and admitted messages in chronological order, deduplicated with the triggering inbox message.
- Messages outside the worker current membership interval are not disclosed.
- If authorized history exceeds the bound or cannot be fully read, no provider effect is dispatched and context is not labeled complete.

## Delivery

Implement production behavior and meaningful local tests within scope. Report files changed, commands actually run, observed results, unresolved dependency qualifications and contract defects. A missing dependency may use an exact local fake for development; production must return a named prerequisite/unsupported error instead of fake success. Integration owns shared dependency changes, real assembly, cross-package proving and landing. Do not advertise release or client/platform support from compilation alone.
