# Support

**Status: draft (S6).** Zatiti is pre-release and has no supported release.
There's no support service, so report problems through the public issue
tracker.

## Collect diagnostics

Both commands return secret-free diagnostics: the installation's owner,
generation, paused and maintenance state, and any named missing
prerequisites or limitations.

```sh
zatiti installation status --json --input '{"scope":<SCOPE>}'
zatiti installation doctor --json --input '{"scope":<SCOPE>}'
```

The controller writes its log records to standard error. The logger redacts
sensitive attributes, such as credentials, owner secrets and `Authorization`
values, before writing them.

## Before you share anything

Check every file or snippet before you attach it to a public issue.
Redaction covers only attributes the logger recognizes, so remove anything
that looks like:

- API keys or tokens
- Keychain service or account names you consider private
- Home directory paths, host names or private IP addresses
- Conversation content, task text or customer information

Never attach a backup bundle, a database file or the contents of the state
directory.

## Report a problem

Open an issue at https://github.com/zatiti/zatiti/issues and include:

- The commit or version you built from, and whether you run the desktop,
  the CLI or MCP.
- The operation that failed, its error `code`, and the redacted `--json`
  result envelope.
- The redacted output of `installation.status` and `installation.doctor`.
- Whether the outcome was reported as unknown, and what `command.get`
  returned for that submission key.

**Not yet available:** a supported release with its own support channel, a
diagnostics bundle command, and support procedures verified against the
release build (S6).
