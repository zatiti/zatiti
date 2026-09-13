// Package evidence owns durable command replay, authorized event reads and
// receipts projected from actual evidence.
//
// Every public mutation the application dispatcher runs is wrapped by
// _evidence.command.begin and _evidence.command.finish inside the handler's
// own write transaction: begin reserves (or replays) the durable command
// identity bound to the caller's principal, operation, operation version and
// submission key; finish persists the handler's complete result envelope,
// verbatim, in the same transaction as the state changes and events it
// correlates to. A rolled-back attempt leaves no trace — the reserved
// identity and its row vanish with the transaction — so a fresh attempt (the
// application's own durable-refusal recording, or an identical client retry)
// reserves again from a clean slate; a durably committed command, by
// contrast, always replays its exact original disposition.
//
// event.get and event.list expose the storage owner's global append-only
// event feed under installation- and scope-based authorization; storage
// persists whatever bytes a handler emits and leaves redaction and exposure
// authorization to this package (see storage's own Events doc), so payload
// redaction happens here, at read time, not at write time — a command's
// retained result is never redacted, since only the command's own principal
// ever replays it, but an event may be listed by any authorized reader
// within its scope.
package evidence
