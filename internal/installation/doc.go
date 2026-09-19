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
// work; Finish commits the disposition). Both need a consistent SQLite image
// of the installation's own database, which contract.Dependencies
// deliberately does not carry: entrypoint assembly binds the one-method
// contract.DatabaseBackup capability through WithDatabaseBackup, and this
// package calls it only from Perform, streaming through a hashing writer so
// the manifest's digest and size describe exactly the bytes it framed and
// sealed (bundle.go). Without the option every path that needs the image
// fails prerequisite_missing naming the capability and never fabricates a
// digest. Restore verifies the bundle and publishes the encrypted recovery
// overlay of the current installation; the file-level rewind itself belongs
// to the controller, which reports its disposition through
// _installation.restore.record.
package installation
