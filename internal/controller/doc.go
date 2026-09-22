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
// After the record, every staged adapter output — the staged request context
// named by physical_call.request_context included, whatever the disposition
// — is published outside any transaction and committed through
// _artifacts.publish; each staged locator becomes the published artifact
// reference in the normalized observation delivered to the waiting owner
// (_execution.observation, _memory.record, _connections.validation.record).
// A staged locator that names no staged output, or more than one, refuses
// publication as an obligation; the raw recorded observation is never
// rewritten. The journal is the deduplicated outbox for those callbacks.
//
// # Database
//
// The controller never hands its contract.Database, or any capability over
// it, to a module. A backup job is only the invocation of the installation
// owner's own plan through an attached JobRunner; a plan that reports
// prerequisite_missing for the database backup capability is recorded as a
// failed job with that requirement, never substituted by a controller-side
// file copy.
//
// A restore job is the one exception: installation.restore's own Prepare/
// Perform/Finish already ran synchronously (verifying the backup, capturing
// the paused RecoveryOverlay of this installation's current state) and left
// its job "running" with an external_action_required requirement -- a
// domain module cannot swap the live SQLite file out from under its own
// open database mid-process. restore.go drives that handoff directly: it is
// never an ordinary JobRunner claim (one is invoked while this same
// application/database handle stays open, which database replacement
// cannot tolerate). It closes admission, drains every other in-flight unit,
// hands the already-verified backup to storage.Restorable for the atomic
// file swap and generation advance, merges the RecoveryOverlay through an
// entrypoint-supplied RestoreLifecycle, and lifts the storage write gate.
// The lifetime that itself performs the swap ends deliberately
// (ErrRestoreHandoff, never a fault) the moment that is durable, because its
// own Application was built over the now-closed pre-restore database
// handle; the caller reassembles Application/Controller over the freshly
// reopened database and runs again, and that resumed lifetime's own start()
// finishes recording the disposition under its own, current generation --
// so no old-generation controller or worker can ever commit after reopen.
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
