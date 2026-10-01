# Recovery

**Status: draft (S6).** The operations described here are implemented and
covered by controlled tests. They aren't qualified on an installed, signed
build.

## Pause, maintenance and resume

| Operation | Effect |
|---|---|
| `installation.pause` | Stops new work from being admitted immediately. Work already running continues. |
| `installation.maintenance.enter` | Stops new work from being admitted, drains and fences running work, and records effects whose outcome is still unresolved. Restore requires it. |
| `installation.resume` | Rechecks current authority and recovery prerequisites before admitting new work. |

Pausing or entering maintenance never claims that a request already sent to a
provider was withdrawn. Bytes that left the machine may still have had an
effect.

## Unknown outcomes

An external action can end with an **unknown** outcome, for example when a
connection drops after a request was sent. Zatiti keeps the outcome unknown
and keeps its cost reservation until evidence resolves it. It never blindly
repeats the action.

- To check a command whose acknowledgment was lost, use `command.get` with
  its submission key. See [Reconnect and key
  rotation](reconnect-and-key-rotation.md).
- To resolve an unknown provider or effect outcome, use
  `operation.reconcile`. It runs one bounded, separately admitted lookup.
  The original unknown outcome and its reservation stay in place until the
  lookup finds supporting evidence. A lookup can confirm that something
  happened, but it can't prove that something didn't happen.

## Inspect before replacing a run or attempt

Before you replace a stuck run or attempt, inspect it:

```sh
zatiti run recovery --json --input '{"scope":<SCOPE>,"id":"<RUN_ID>"}'
zatiti attempt recovery --json --input '{"scope":<SCOPE>,"id":"<ATTEMPT_ID>"}'
```

Both commands show the run's generation (which incarnation of it is current),
its leases, any conflicting resources, and outstanding effect obligations.
Replace a run only when that output shows no live lease and no unresolved
effect that a replacement could duplicate.

## Controller restart

The controller keeps durable records of commands, turns and effect intents.
After a restart it picks up pending work from those records. Some things
don't survive a restart:

- Live reply previews are lost, because they're ephemeral by design.
- A pending restore keeps the installation paused, and no new work is
  admitted until you resume it explicitly.

**Not yet available:** crash-and-restart recovery for hosted Serenity OAuth
and memory depends on workstreams S1–S4, which aren't finished.
