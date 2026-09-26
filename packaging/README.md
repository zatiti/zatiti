# Packaging

This root owns the Zatiti distribution lifecycle: release manifests, service
launchers, the installation-time secure helper, and hosted or optional
self-hosted Serenity metadata. It imports the Go standard library only.

## Status

No release exists. The controller and Flutter client have not been assembled
into a qualified, signed release. The source Serenity pin and Flutter, Dart,
and plugin pins are recorded in `docs/implementation/dependencies.lock.json`,
but the hosted Serenity interface is not qualified for Zatiti's full memory
requirements. The `zatiti-pack` executable below is a packaging/install
driver, not an installation command for a release. Nothing here claims that
an artifact is signed, notarized, or published.

Everything in this root is proven against synthetic trees in temporary
directories, including `zatiti-pack`'s own tests, which run the real
executable's code (in-process, against a recording stand-in for `launchctl`
and `systemctl`, never the host's real ones). Behavior against a real
binary, a real Flutter build, a real `launchd` or `systemd`, and a real
keychain is qualification work. See [QUALIFICATION.md](QUALIFICATION.md).

## What the package provides

| File | Responsibility |
|---|---|
| `manifest.go` | The `zatiti.release_manifest/v1` schema, strict decoding, canonical encoding, and validation |
| `tree.go` | `Build` measures a staged tree into a manifest; `VerifyTree` checks a tree against one |
| `licenses.go` | License notice audit: the license entries and the SBOM must describe the same components |
| `signature.go` | Detached Ed25519 manifest signature, verified against caller-supplied trusted keys |
| `mac_release.go` | Matched Intel/Apple Silicon Mac descriptor, component and bundle verification, and a separate detached metadata signature |
| `templates/macos/select_arch.sh` | Sourceable native Mac CPU selector using `hw.optional.arm64`, including a translated shell; no download or install command |
| `service.go`, `templates/` | The macOS LaunchAgent and the Linux user `systemd` unit |
| `layout.go`, `plan.go`, `apply.go` | Install, upgrade, and uninstall planning and execution |
| `servicemanager.go` | `launchctl` and `systemctl --user` drivers behind the `ServiceManager` interface |
| `masterkey.go` | Headless master key provisioning |
| `audit.go` | Installed permission, symlink, and integrity audit |
| `desktop.go` | The desktop distribution's manifest rules |
| `desktop_bundle.go` | Deterministic bundle archive assembly, extraction, and verification |
| `desktop_install.go` | Desktop layout, install, upgrade, uninstall, audit, and the Linux launcher entry |
| `cli.go`, `cli_*.go` | The packaging/install driver's implementation (`RunCLI`), built entirely on the exported functions above |
| `cmd/zatiti-pack` | The driver's five-line executable entry point; `go build ./packaging/cmd/zatiti-pack` |

## The packaging/install driver

`zatiti-pack` is an executable command-line driver over this package's
library: it assembles a manifest from a staged tree, signs and verifies it,
and plans and applies install, upgrade, and uninstall for either
distribution. It adds no capability the library does not already have -
`RunCLI` (`cli.go`) parses flags and calls the same exported functions this
package's own tests call, which is also why `go test ./packaging` exercises
the driver's exact code, not a stand-in for it.

```
go build -o zatiti-pack ./packaging/cmd/zatiti-pack
zatiti-pack help
```

Every subcommand prints one JSON document to stdout on success, or one JSON
error document to stderr (`{"error": {"code", "message", "findings"}}`) on
failure, and exits 0 on success or a small nonzero code modeled on the
frozen CLI convention (`invalid_input`=2, `conflict`=4,
`prerequisite_missing`/`capability_unsupported`=5, otherwise 1).

- `assemble --root <tree> --descriptor <file.json> [--write]` builds a
  manifest from a staged tree and a descriptor file (the same fields
  `BuildInput` takes, as JSON) and, with `--write`, writes it into the tree
  as `manifest.json`. `assemble-bundle --dir <built bundle> --archive <out>`
  does the same for the desktop application bundle.
- `keygen --private-out <file> --public-out <file>` generates a local
  Ed25519 key pair for development and testing signatures only. This
  repository holds no release identity, and this command must never be used
  to mint one for an actual release: `sign` and `verify` never claim, and
  this driver never performs, a real release signing, notarization,
  publication, or deployment.
- `sign --manifest <file> --key <PEM PKCS8 file>` and
  `verify --manifest <file> --sig <file> --trusted <PEM PKIX file>...
  [--tree <dir>]` wrap `Sign` and `VerifySignature` (and, with `--tree`,
  `VerifyTree`).
- `install` and `uninstall` (each `--distribution controller|desktop --os
  darwin|linux --home <dir> --state-dir <dir> ...`) plan an install,
  upgrade, or uninstall and, with `--apply`, execute it; omitted, they are a
  dry run that touches nothing on disk. `install` requires a verified
  signature (`--trusted`, and `--sig` if it is not next to the manifest)
  unless the caller explicitly accepts `--allow-unsigned`, so a tampered
  manifest, artifact, secure helper declaration, or profile claim is
  refused before any service is touched. A controller `install` additionally
  takes `--services <file.json>`, an array of service specifications in the
  same shape `ServiceSpec` takes.
- `service --verb load|unload|restart --label <label> --unit <file> --os
  <os>` drives one launcher directly, outside of a plan - `restart` is
  `unload` followed by `load` through the same manager.
- `audit` and `inspect` wrap `AuditInstalled`/`AuditDesktopInstalled` and
  `Inspect`.
- `master-key provision --key-path <file> ...` and
  `master-key check --key-path <file>` wrap `ProvisionMasterKey` and
  `CheckMasterKey`.

`install`, `uninstall`, and `service` take `--service-manager
launchd|systemd|none|auto` (`auto`, the default, follows `--os`), and
`--launchctl`/`--systemctl` to override the default absolute tool path
(`DefaultLaunchctl`/`DefaultSystemctl`) - most useful for pointing the
driver at a recording stand-in while testing or auditing this tool, the same
substitution its own tests make.

## Release manifest

A manifest describes one distribution of one release for one target
(`darwin` or `linux`, `amd64` or `arm64`). The two distributions are packaged
separately, so a headless host installs the controller alone:

- `controller`: the `zatiti` binary (`serve`, `init`, the generated CLI, and
  `mcp serve`) and the secure helper record. The Mac hosted mode uses remote
  Serenity over OAuth and MCP and does not bundle a Serenity runtime or read
  facade. An optional self-hosted mode may declare its own qualified runtime.
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

For hosted Mac mode, the controller distribution records the hosted endpoint,
OAuth profile contract, and only qualified capability evidence; it does not
install or launch Serenity. A self-hosted mode must record its source,
version, revision, license, interface, runtime, read facade, and qualification
evidence. The desktop distribution adds the
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
- A hosted Mac manifest may omit a local Serenity pin. Any claimed hosted or
  self-hosted capability still requires evidence; the package invents no
  Serenity version, interface, or launch command.
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

`AssembleMacRelease` accepts exactly four staged, separately signed macOS
trees: controller and desktop for both `amd64` and `arm64`. It verifies the
component signatures, tree digests, desktop archives and declared executable,
including a thin Mach-O executable whose CPU matches the manifest target.
Universal and malformed executables are refused. Desktop executable inspection
uses a bounded temporary file and removes it after the check. Assembly also
rejects mismatched version, source revision, protocol, SDK/plugin pin,
Serenity pin, profile or license records. The canonical descriptor contains
the four manifest digests and signatures. `SignMacRelease` signs that metadata
under a distinct schema; a consumer must verify the descriptor signature
**and** call `VerifyMacRelease` with the four downloaded trees. These functions
use caller-supplied trust keys and make no claim about code signing,
notarization or a qualified downloadable release.

The Mac architecture selector consults `/usr/sbin/sysctl -n hw.optional.arm64`
before `uname`. A value of `1` selects `arm64` even when a translated shell
reports `x86_64`; an Intel host can report an unknown OID and selects `amd64`
only when that exact error and `x86_64` agree. Other probe failures stop.
The descriptor currently has no artifact URLs, maximum download sizes,
hosting layout or trust-key rotation path, so it cannot generate a secure
bootstrap downloader yet. Those fields need a coordinated release descriptor
revision before a public install command exists.

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
contract. A local Serenity launcher takes its executable and arguments from
the qualified self-hosted pin. Hosted Mac mode has no Serenity launcher.

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
  previous release stays on disk. If a controller service step or start fails,
  `Apply` attempts to restore the old link and launchers and restart the old
  service. A failed recovery is reported explicitly for operator repair.
- **Repeat install** of the same version verifies the installed manifest,
  files, bundle, and launchers against the requested release, then changes
  nothing. A damaged or different same-version release is refused.
- **Downgrade** is refused. State written by a later release cannot be read
  by an earlier one; restore a backup instead.
- **Uninstall** unloads the services and removes the launchers and the
  distribution directory. State is preserved. State is removed only when the
  operator requests it and repeats the exact state directory path. Keychain
  entries and a master key file live outside the state directory and are
  never removed.

`Apply` treats a plan as untrusted data. It re-validates the layout, refuses
any step outside the layout, refuses to write through a symlinked directory,
and refuses a plan built against a different active release. An owner-only
advisory lock in the user's home serializes controller and desktop changes;
waiting is cancelled with the caller's context and otherwise stops after ten
seconds. The lock file stays in place so a second process always locks the
same inode.

### Desktop distribution

The desktop client installs into its own layout, disjoint from the
controller's and from the state directory:

```text
<desktop distribution>/versions/<version>/   the release tree with its manifest
<desktop distribution>/current               symlink to the active release
<desktop distribution>/bundles/<version>/    the unpacked application bundle
<desktop distribution>/current-bundle        symlink to the active bundle
~/.local/share/applications/dev.zatiti.zatiti_desktop.desktop   Linux launcher entry
~/Applications/<Name>.app                    macOS link into the active bundle
```

`AssembleBundle` turns a `flutter build` output directory into one `tar.gz`
with sorted entries, zero timestamps and ownership, and only the owner
execute bit preserved, so the same build always produces the same bytes.
Relative symlinks that stay inside the bundle are kept (a macOS `.app` needs
them); absolute or escaping links, hard links, special files, and names that
look like state or key material are refused. Extraction applies the same
rules, checks the archive against the manifest digest while reading it,
writes links last so nothing is written through one, and confirms the
declared executable is a regular executable file. `VerifyBundle` compares an
unpacked bundle with its archive entry by entry.

Install, upgrade, and uninstall follow the controller's shape without any
service action. Uninstall removes the launcher and the distribution;
credentials and drafts in operating-system secure storage are not touched.
The Linux launcher entry is written unescaped, so a path with a character
the `Exec` key would interpret is refused rather than escaped.

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
