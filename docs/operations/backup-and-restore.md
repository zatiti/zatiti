# Backup and restore

**Status: draft (S6).** The backup and restore paths are implemented and
covered by controlled tests, including a backup-then-restore round trip. They
haven't been exercised on an installed, signed build.

## What a backup contains

`installation.backup` creates one encrypted, verified bundle. It contains:

- A consistent image of the SQLite database, taken with the database's own
  backup mechanism. Copying a live database file is not a supported backup.
- A pinned manifest of the installation's artifacts.
- The memory brain revisions the installation depends on.

The bundle is encrypted with a per-installation backup key. That key lives in
the installation's secret store (the macOS Keychain), and the bundle never
contains it. A backup job is reported as complete only after the bundle is
verified. If a backup fails partway through, no completed manifest is left
behind.

## Limits you must know about

- **A backup restores only into the same installation.** A restore refuses a
  bundle created by a different installation.
- **The backup key must still resolve in this Mac's Keychain.** Restore looks
  the key up by the reference recorded in the bundle. If that Keychain entry
  is gone, the backup can't be decrypted. If the reference stops resolving,
  the next backup silently mints a new key, so older bundles can no longer
  be decrypted either.
- **Not yet available: restoring onto a different Mac.** The source contains
  a routine that exports the master key material and a routine that imports
  blobs encrypted by another installation. No command or operation calls
  either of them yet, and no key-recovery artifact is produced for you. A
  backup therefore protects against a corrupted database or an unwanted
  change on the same Mac. It doesn't protect against losing the Mac or its
  Keychain.
- **A restore rewinds the database; it can't undo external effects.**
  Provider calls, messages and other external actions that happened after
  the backup still happened.

## Create a backup

1. Pause the installation. Backup requires a paused installation. Pausing
   stops new work from being admitted, and the backup records the pending
   obligations as a consistent snapshot.

   ```sh
   zatiti installation status --json --input '{"scope":<SCOPE>}'
   zatiti installation pause --submission-key <KEY> --json --input '{"scope":<SCOPE>,"expected_version":<VERSION>}'
   ```

2. Start the backup:

   ```sh
   zatiti installation backup --submission-key <KEY> --json --input '{"scope":<SCOPE>}'
   ```

   The result is a job. Follow it until it reports a verified result:

   ```sh
   zatiti installation job get --json --input '{"scope":<SCOPE>,"id":"<JOB_ID>"}'
   ```

3. Optional: copy the bundle out of the local store with `artifact.export`.
   Remember the limits above: an exported bundle is useful only to this
   installation, with its Keychain intact.
4. Resume the installation:

   ```sh
   zatiti installation resume --submission-key <KEY> --json --input '{"scope":<SCOPE>,"expected_version":<VERSION>}'
   ```

## Restore from a backup

A restore replaces the installation's database with the backup image. It
runs only inside an exclusive local maintenance session, so no other work can
run at the same time.

1. Enter maintenance. This blocks new work, drains and fences running work,
   and records effects whose outcome is still unresolved:

   ```sh
   zatiti installation maintenance enter --submission-key <KEY> --json --input '{"scope":<SCOPE>,"expected_version":<VERSION>}'
   ```

2. Start the restore with the backup artifact reference from the backup job:

   ```sh
   zatiti installation restore --submission-key <KEY> --json --input '{"scope":<SCOPE>,"backup_artifact":<ARTIFACT_REF>,"expected_version":<VERSION>}'
   ```

   The controller then:

   - Verifies the bundle's encryption and integrity, and checks that it
     belongs to this installation. A tampered bundle, or one from another
     installation, is refused.
   - Swaps in the restored database and rebuilds itself over it.
   - Merges a recovery overlay, so anything that happened after the backup
     isn't forgotten. That covers revoked credentials, command identities,
     reservations, and effects whose outcome is still unknown.

3. Follow the job with `installation.job.get`.
4. **The installation comes back paused.** Restarting the controller doesn't
   resume it. Review `installation.status` and resolve every unknown outcome
   it lists (see [Recovery](recovery.md)), then resume. Resume rechecks
   current authority and recovery prerequisites, and it's refused while
   anything from the restore is still unresolved.

**Not yet available:** the restore journey isn't qualified on the release
build (S6).
