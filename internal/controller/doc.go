// Package controller owns one controller lifetime: the scheduler loop,
// adapter dispatch, durable background work and recovery coordination.
//
// Assembly acquires the installation lock, migrates the database and
// advances the generation, then hands the held ownership to New. The
// controller takes no lock and advances nothing. It stops admitting the
// moment ownership is lost, a newer generation appears, Stop is called or
// Run's context ends.
//
// # Effects
//
// Every effect crosses three owner transactions around one adapter call:
// _effects.admit, _effects.claim, the adapter outside any transaction, then
// _effects.record. The dispatch journal below the state directory is written
// ahead of each step, so a crash leaves a durable upper bound on what may
// have happened. Recovery records an attempt that was never claimed-and-
// dispatched as not sent, a claimed attempt without an observation as
// unknown, and a journaled observation exactly as observed. No recovery path
// reaches an adapter: a claimed effect is never resent, and a failed record
// write retries the record, never the call. Operations the owner lists as
// awaiting confirmation or outcome_unknown are left to a separately admitted
// reconciliation.
//
// After the record, staged adapter outputs are published outside any
// transaction and committed through _artifacts.publish, and the normalized
// observation is delivered to the waiting owner (_execution.observation,
// _memory.record, _connections.validation.record). The journal is the
// deduplicated outbox for those callbacks.
//
// # Shutdown
//
// Stop closes admission at once and lets work outside a transaction record
// its real outcome. At the caller's deadline the remaining work is
// abandoned, not cancelled on the provider's behalf: its claim is already
// journaled and the next generation records it as unknown.
//
// # Collaborators the frozen constructor cannot carry
//
// New has no parameter for the controller's service identity, a blob store
// or the executable body of an owner's durable job. Attach supplies them.
// Identity is required. Without a blob store, staged outputs stay an open
// publication obligation and the owner callback is withheld. A pending job
// with no attached JobRunner is never claimed and is reported as an
// obligation. Status lists every such obligation.
package controller
