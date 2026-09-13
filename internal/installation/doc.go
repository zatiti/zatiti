// Package installation owns bootstrap, installation restriction/maintenance
// state, health diagnostics, encrypted backup and paused restore jobs for
// one installation.
//
// installation.init is the one-time local bootstrap: it runs before any
// principal can authenticate, mints no durable state until a trusted helper
// (Perform, outside any transaction) has custodied an owner secret, and then
// commits identity, root organization/chief, distinct memory brains and the
// pinned personal-chief conversation in one transaction (Finish). A crash
// between the helper and that transaction leaves an opaque, tokenless
// "pending" bootstrap intent row that the next init attempt detects and
// retires before starting its own attempt; the destination is never
// reinitialized once a completed installation_state row exists.
//
// Maintenance, pause and resume are immediate, acknowledged restrictions:
// they never wait on a model call, a configuration compile or a spend
// reservation. Entering maintenance fences current execution attempts at the
// installation's generation and records unresolved effects as inspectable
// requirements; it never claims an uncooperative external process stopped.
//
// Backup and restore are local IO operations (Prepare validates and records
// intent inside the caller's transaction; Perform performs the untransacted
// work; Finish commits the disposition). Both need a consistent point-in-time
// digest of the installation's own SQLite database, which this package
// cannot obtain: contract.Dependencies (internal/contract/module.go) carries
// no contract.Database handle and this package's frozen outgoing call list
// carries no operation that produces one. Every code path that needs that
// digest returns a named prerequisite_missing fault instead of fabricating
// one; see backup.go and restore.go for the exact seam and doc.go's sibling
// AGENTS.md for the acceptance language this satisfies.
package installation
