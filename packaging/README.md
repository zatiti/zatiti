# Packaging

This root owns the Zatiti distribution lifecycle: release manifests, service
launchers, the installation-time secure helper, and the record of the pinned
Serenity distribution. It imports the Go standard library only.

## Status

No release exists. The controller binary (`cmd/zatiti`) is landed but no
release build of it has been packaged; the Flutter desktop client
(`apps/desktop`) has not been built for release; the Serenity pin and the
Flutter, Dart, and plugin pins are unresolved in
`docs/implementation/dependencies.lock.json`. Nothing in this directory is
an installation command, and nothing here claims that an artifact is signed,
notarized, or published.

Everything in this root is proven against synthetic trees in temporary
directories. Behavior against a real binary, a real Flutter build, a real
`launchd` or `systemd`, and a real keychain is qualification work. See
[QUALIFICATION.md](QUALIFICATION.md).

Installing the desktop bundle is not in this revision. `PlanInstallation`
refuses a desktop manifest with `capability_unsupported`; the controller
lifecycle below is complete.

## What the package provides

| File | Responsibility |
|---|---|
| `manifest.go` | The `zatiti.release_manifest/v1` schema, strict decoding, canonical encoding, and validation |
| `tree.go` | `Build` measures a staged tree into a manifest; `VerifyTree` checks a tree against one |
| `licenses.go` | License notice audit: the license entries and the SBOM must describe the same components |
| `signature.go` | Detached Ed25519 manifest signature, verified against caller-supplied trusted keys |
| `service.go`, `templates/` | The macOS LaunchAgent and the Linux user `systemd` unit |
| `layout.go`, `plan.go`, `apply.go` | Controller install, upgrade, and uninstall planning and execution |
| `servicemanager.go` | `launchctl` and `systemctl --user` drivers behind the `ServiceManager` interface |
| `masterkey.go` | Headless master key provisioning |
| `audit.go` | Installed permission, symlink, and integrity audit |
| `desktop.go` | Everything specific to the Flutter desktop client |

## Release manifest

A manifest describes one distribution of one release for one target
(`darwin` or `linux`, `amd64` or `arm64`). The two distributions are packaged
separately, so a headless host installs the controller alone:

- `controller`: the `zatiti` binary (`serve`, `init`, the generated CLI, and
  `mcp serve`), the pinned Serenity runtime and read facade, and the secure
  helper record.
- `desktop`: the Flutter application bundle from `flutter build macos` or
  `flutter build linux` (release mode), as one `tar.gz` artifact because a
  macOS `.app` holds symlinks that a flat tree refuses, plus the Flutter and
  Dart SDK versions, every plugin at its pinned version with its license
  notice, and the native libraries the runner loads from the host: GTK and
  the Secret Service client library on Linux, system frameworks on macOS.

Every manifest carries:

- the version, source revision, and Go toolchain;
- every file in the tree with its SHA-256 digest, size, and permissions;
- one license entry per shipped component, each pointing at a notice file in
  the tree, and an SPDX or CycloneDX SBOM;
- the wire contracts of contract revision 2: operation API `v1`,
  `zatiti.request/v1`, `zatiti.result/v1`, and MCP `2025-11-25`;
- the adapter profiles the release supports, each with qualification
  evidence in the tree;
- attestations, each pointing at an evidence file in the tree.

The controller distribution adds the Serenity pin (source, version, revision,
license, interface version, runtime, read facade, and qualification evidence)
and the secure helper for the target. The desktop distribution adds the
desktop section and carries no controller binary, Serenity pin, secure
helper, controller state, database driver, or credential: file names that
look like state or key material are refused, and a named list of SQLite
drivers is refused in the SBOM.

The manifest has no timestamp, so the same tree always encodes to the same
bytes. `manifest.json` and `manifest.sig.json` are the only files in a tree
that the manifest does not list.

Validation enforces these rules:

- `LICENSE` must be the unmodified Apache License 2.0 text. The package pins
  its digest, and a test compares the pin with the repository's `LICENSE`.
- A profile, a Serenity pin, or a code signature, notarization, or
  qualification attestation without an evidence file in the tree is
  rejected. A claim cannot be made without its evidence.
- An absent Serenity pin returns `prerequisite_missing`. The package invents
  no Serenity version, interface, or launch command.
- No file is group writable or world writable, and no file carries `setuid`,
  `setgid`, or sticky bits.

## Signatures

`Sign` takes a `crypto.Signer` that holds an Ed25519 key, so the key can live
in hardware. `VerifySignature` takes the trusted public keys from the caller.
An empty trust set returns `prerequisite_missing`; it never passes. This
repository holds no key, and the tests generate throwaway keys.

Platform code signing and notarization are outside this package. A release
workflow that performs them records the result as an attestation with its
evidence file.

## Service launchers

The controller runs as a per-user service that does not depend on the
desktop client:

- macOS: a LaunchAgent with `RunAtLoad`, `KeepAlive`, a `077` umask, and a
  30-second exit timeout.
- Linux: a user `systemd` unit with `Restart=always`, `UMask=0077`,
  `NoNewPrivileges=yes`, and `WantedBy=default.target`.

Neither launcher names another unit, so closing the desktop client cannot
stop the controller. On Linux, a user service stops at logout unless the
operator enables lingering for the account; the install plan says so in its
notices.

The controller launcher runs `<controller> serve`. Any further argument comes
from the caller, because the entrypoint's flags are not part of the frozen
contract. A Serenity launcher takes its executable and arguments from the
qualified pin.

The controller listens on `<state dir>/zatiti.sock` unless `--socket` or
`ZATITI_SOCKET` says otherwise, and `cmd/zatiti` refuses a socket path of 104
bytes or more before it assembles anything. `Render` resolves the same path
and refuses a controller launcher that would run into that refusal, naming
the remedy.

Launchers never carry secrets. `Render` rejects an argument or environment
name that looks like a credential, such as `--owner-token` or
`OPENAI_API_KEY`. Names that end in `-ref`, `-file`, `-path`, or `-dir` are
references and are allowed. This is a guard against an obvious mistake, not
secret detection.

`ValidateServices` refuses two controllers, a repeated label, and two
services that would write the same directory tree. That is the packaging
half of "one writer owner per brain and one controller state lock". The
running processes enforce the other half with their own locks.

## Install, upgrade, and uninstall

A layout separates three disjoint trees: the distribution directory, the
launcher directory, and the state directory. Packaging never writes inside
the state directory.

```text
<distribution>/versions/<version>/   one directory per release, with its manifest
<distribution>/current               symlink to the active release
<launchers>/<label>.plist|.service   one launcher per service
```

Launchers reference binaries through `current`, so an upgrade does not
change the path they run.

- **Install** verifies the source tree against the manifest, copies each
  file while hashing it again, publishes the release directory with one
  rename, points `current` at it, writes the launchers, and loads the
  services.
- **Upgrade** publishes the new release while the services still run, then
  unloads them, moves `current`, rewrites the launchers, and loads them. The
  previous release stays on disk. If a step fails after the unload and before
  `current` moves, `Apply` loads the services again on the old release.
- **Downgrade** is refused. State written by a later release cannot be read
  by an earlier one; restore a backup instead.
- **Uninstall** unloads the services and removes the launchers and the
  distribution directory. State is preserved. State is removed only when the
  operator requests it and repeats the exact state directory path. Keychain
  entries and a master key file live outside the state directory and are
  never removed.

`Apply` treats a plan as untrusted data. It re-validates the layout, refuses
any step outside the layout, refuses to write through a symlinked directory,
and refuses a plan built against a different active release.

## Secret and key provisioning

Secrets reach the controller through the platform secret store, never
through flags, chat, or an environment that a worker inherits.

- macOS uses the OS keychain through the OS-provided helper. The manifest
  records the helper's system path; the helper is not packaged.
- A headless host uses encrypted files under a master key.
  `ProvisionMasterKey` draws 32 bytes from the OS random source and writes
  them with `0600` permissions in a `0700` directory, in the raw form that
  the platform `file:` master key reference reads. The function returns only
  the reference. It never overwrites an existing key, because every blob and
  headless secret is encrypted under it.

## Backup and restoration key prerequisites

A backup of the state directory is useless without the key material that
encrypts it, and a backup that carries its own key protects nothing. Keep
these separate from the backup and from each other's failure domain:

- **Headless hosts:** the master key file. The key sits outside the state
  directory by construction, so a state backup does not contain it. Store a
  copy where the backup is not stored. Restore the key file with `0600`
  permissions in a `0700` directory before starting a restored controller.
- **macOS hosts:** the keychain entries for the installation, including a
  master key held as a `secret:` reference. Keychain items do not travel with
  a copy of the state directory. Plan their recovery with the operating
  system's keychain backup mechanism.
- **Every host:** the backup encryption key, if the backup is encrypted under
  a key other than the master key.

A restored controller starts paused. Removing the state directory during an
uninstall cannot be undone without a verified backup and its keys.
