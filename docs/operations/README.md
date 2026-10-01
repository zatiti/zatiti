# Operating Zatiti: recovery and support

**Status: draft for release review (S6). Zatiti is pre-release.** These
pages describe what the current source does. They aren't release evidence.
No step here has been run on a signed, installed build. Any step that
depends on unfinished release workstreams is marked **Not yet available**.

| Page | Covers |
|---|---|
| [Reconnect and key rotation](reconnect-and-key-rotation.md) | Client reconnects, unknown acknowledgments, rotating provider keys, and the OpenRouter key rotation that must happen before the provider qualification probe runs |
| [Backup and restore](backup-and-restore.md) | Encrypted backups, restoring an installation, and what a backup doesn't protect against |
| [Recovery](recovery.md) | Pausing, maintenance, unknown outcomes, and inspecting runs before you replace them |
| [Support](support.md) | Collecting diagnostics without secrets, and reporting a problem |
| [Release evidence checklist](release-evidence-checklist.md) | What the release reviewer must see before approving a Mac release |

## Conventions

- Every CLI command in these pages is generated from the operation catalog.
  Commands take structured input with `--input` (inline JSON, `@file`, or `-`
  for standard input). Add `--json` for one machine-readable result envelope.
- Every mutation needs `--submission-key`. This caller-chosen key makes a
  retry safe: if the controller already recorded that key, it returns the
  original result instead of running the command again.
- `<SCOPE>` stands for the scope object that identifies your installation,
  for example `{"installation_id":"<INSTALLATION_ID>"}`.
- Mutations that change a versioned resource also need `expected_version`,
  the version you last read. A stale version fails with `stale_version`
  instead of overwriting a newer change.
