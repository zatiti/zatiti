# Packaging qualification

These are instructions for the qualification owner (`tests/qualification`).
They list what the package tests cannot prove and how to prove it against
built artifacts on real hosts. Nothing in this file has been run. Record the
expected and observed result of every step, with the exact source revision,
manifest digest, operating system version, and tool versions.

Do not publish an installation command, and do not claim a release, until
every section passes on both macOS and Linux.

## Boundary

The package tests prove these properties against synthetic trees in
temporary directories:

- manifest generation, canonical encoding, strict decoding, and validation;
- tree verification, including tampering, extra files, and symlinks;
- Ed25519 manifest signature verification;
- launcher rendering and escaping, checked with an XML parser and with a
  test-local implementation of the `systemd` quoting rules;
- install, upgrade, and uninstall file operations, state preservation,
  refusal of plans that escape the layout, and refusal of symlinked
  directories;
- the exact `launchctl` and `systemctl` argument lists, against a recording
  runner;
- master key provisioning permissions and the no-overwrite rule.

Qualification must prove the rest:

| Unproven here | Why |
|---|---|
| `launchd` accepts the LaunchAgent and keeps the controller alive | No real `launchd` in package tests |
| `systemd` parses the unit, including quoted arguments with `%` and `$` | The quoting check is self-consistency, not `systemd` |
| `launchctl print`, `bootstrap`, and `bootout` exit codes make load and unload idempotent | Exit codes are observed, not assumed |
| `launchd` resolves the `current` symlink at each start | Depends on the OS |
| The controller binary accepts `serve` and the arguments the launcher passes | No release build of `cmd/zatiti` has been packaged |
| The Serenity runtime starts from its launcher and holds one writer per brain | The Serenity pin is unresolved |
| A real `flutter build` output archives and unpacks under the bundle rules, starts from the unpacked location, and fails clearly without the declared native dependencies | `apps/desktop` has no release build; the SDK and plugin pins are not in the lock report; the bundle tests use synthetic trees shaped like a build |
| The desktop launcher entry is picked up by the desktop environment, and Finder and Launchpad open the linked `.app` | Needs a real desktop session |
| The desktop bundle holds no state, driver, or credential | The manifest rules check names; only the built bundle can be inspected |
| The keychain helper at the manifest's path stores and returns secrets | Needs a real keychain |
| Code signature and notarization evidence | Needs a real developer identity, which this repository never holds |

## Preconditions

1. Build the controller from a recorded source revision with the pinned
   toolchain. Build the desktop client with the pinned Flutter SDK
   (`flutter build macos` or `flutter build linux`, release mode) and archive
   the resulting bundle with `AssembleBundle`. Expect two builds of the same
   revision to produce the same archive digest only if the Flutter build
   itself is reproducible; record the observed digests either way.
2. Resolve the Serenity pin in the dependency lock report. Without it,
   `Build` returns `prerequisite_missing` and qualification cannot start.
3. Generate the SBOM for each distribution and collect a notice file for
   every component in it. For the desktop, take the Flutter, Dart, and plugin
   versions from `apps/desktop/pubspec.lock` as recorded in the lock report.
4. Stage each tree, call `Build`, write `manifest.json`, and sign it with a
   qualification key generated for the run. Discard the key afterward. The
   `zatiti-pack` executable (`go build ./packaging/cmd/zatiti-pack`; see the
   README's "The packaging/install driver") wraps this exact sequence
   (`assemble --write`, `keygen`, `sign`) and the install/upgrade/uninstall
   steps below (`install`, `service`, `uninstall`, `audit`); qualification
   may run it directly instead of writing an ad hoc program against the
   library. It still signs with a qualification key discarded after the run,
   never a real release identity, and performs no publication step.

## 1. Clean install and launch

On a host with no prior installation:

1. Verify the signature, then plan and apply an install.
2. Expect `AuditInstalled` to pass, and expect the state directory to be
   absent or untouched.
3. Expect the service manager to report the controller as running.
4. Run a CLI query against the private socket and expect a completed result.
5. Expect the state directory root to have `0700` permissions and sensitive
   files `0600`, and expect the launcher log file, if configured, to have
   `0600` permissions.

## 2. Controller survives desktop close

1. Plan and apply a desktop install, expect `AuditDesktopInstalled` to pass,
   start the desktop client from the launcher entry or application link, and
   confirm it is connected over the private socket.
2. Quit the desktop client. Then end the desktop client process with a
   signal.
3. Expect the controller process ID to be unchanged in both cases, and expect
   a CLI query to complete.
4. Linux only: log out of the graphical session with lingering enabled and
   expect the controller to keep running. Record the behavior with lingering
   disabled; the install plan warns about it.

## 3. Restart

1. End the controller process with a signal and expect the service manager
   to start it again.
2. Expect the controller generation to advance by exactly one, and expect
   state created before the restart to be readable.
3. Reboot the host, log in, and expect the controller to be running.
4. Start a second controller by hand against the same state directory and
   expect it to fail with `controller_unavailable`.

## 4. Upgrade

1. Create state: an organization, a task, and a stored secret.
2. Apply an upgrade to a later release.
3. Expect the service to be unloaded before `current` moves and loaded after,
   the previous release directory to remain, and every item of state to be
   readable, including the secret.
4. Interrupt an upgrade during the copy phase and expect the running
   controller to be unaffected. Apply the same plan again and expect it to
   succeed.
5. Attempt a downgrade and expect `capability_unsupported`.

## 5. Uninstall

1. Apply an uninstall without a state request. Expect the service to be
   unloaded, the launchers and distribution directory to be removed, and the
   state directory to be byte-identical to a listing taken beforehand.
2. Reinstall and expect the earlier state to be served.
3. Apply an uninstall with a confirmed state removal on a disposable host.
   Expect the state directory to be removed and the master key file and
   keychain entries to remain.

## 6. Serenity lifecycle

1. Expect the installed Serenity runtime and read facade digests to match the
   manifest and the pin.
2. Start the Serenity service from its launcher. Attempt to start a second
   writer for the same brain and expect the second to be refused.
3. Upgrade Zatiti and expect the Serenity service to restart on the new
   release with its brain intact.
4. Expect `ValidateServices` to refuse two launchers that own the same brain
   directory.

## 7. Secure helper

1. macOS: store and read a secret through the controller, and expect the
   keychain to hold an item under the installation's service name. Expect no
   secret in the launcher, the process arguments, or the process environment
   of the controller and of a worker.
2. Headless Linux: call `ProvisionMasterKey`, configure the returned
   reference, and start the controller. Expect a second call for the same
   path to return `conflict` and leave the key unchanged.

## 8. License notice audit

1. Expect `VerifyTree` to pass on the release tree.
2. Expect `LICENSE` to match the pinned Apache License 2.0 digest.
3. Compare the SBOM with `go version -m` output for each shipped Go binary
   and expect every linked module to appear in the SBOM, and therefore in the
   license entries.
4. Remove one notice file and expect `VerifyTree` to report it.

## 9. Signing and notarization

Run this section only in a release workflow that holds a real identity.
Record the verification tool's output as an evidence file in the tree, and
add the matching attestation. A manifest without that evidence makes no
signing or notarization claim, and no document may make one for it.
