// Package packaging owns the Zatiti distribution lifecycle: release
// manifests, service launchers, the installation-time secure helper and the
// pinned Serenity distribution record.
//
// It imports the standard library only. Nothing here imports a product
// package, so the fault codes in errors.go repeat the frozen contract
// vocabulary as plain strings.
//
// What this package does:
//
//   - Build, encode, decode and validate a release manifest
//     (zatiti.release_manifest/v1) for either distribution: the controller
//     (one Go binary, the Serenity pin, the secure helper) or the desktop
//     (the Flutter application bundle, SDK and plugin pins, native runner
//     dependencies). Both carry version, target, per-file SHA-256 checksums,
//     license notices, an SBOM reference, and the supported protocols and
//     profiles.
//   - Verify a distribution tree against its manifest and verify a detached
//     Ed25519 manifest signature against caller-supplied trusted keys.
//   - Render the macOS LaunchAgent and the Linux user systemd unit that keep
//     the controller (and the Serenity service) alive independently of the
//     desktop client.
//   - Plan and apply install, upgrade and uninstall of the controller
//     distribution over a per-user layout. State is never touched unless the
//     operator requests removal and confirms the exact state directory.
//   - Provision the headless master key file without the key ever passing
//     through flags, chat or an inherited environment.
//   - Audit installed permissions and license notices.
//
// What this package does not claim:
//
//   - No artifact is signed, notarized or published by this package. An
//     attestation in a manifest must reference an evidence file that is part
//     of the verified tree; a claim without evidence fails validation.
//   - The Serenity pin is unresolved in the dependency lock report. Build and
//     Validate return prerequisite_missing until a complete pin is supplied;
//     no Serenity interface, version or launch command is invented here.
//   - Behavior against a real launchd or systemd, a real built binary, a
//     real Flutter build, and a real keychain is qualification work. See
//     QUALIFICATION.md.
//   - Installing the desktop bundle (unpacking the archive, placing the
//     application, the freedesktop launcher entry) is not in this revision;
//     PlanInstallation refuses a desktop manifest with capability_unsupported.
//
// Everything specific to the desktop client lives in desktop.go and
// desktop_test.go so that a change of desktop framework replaces those files
// without touching the controller, service or helper lifecycle.
package packaging
