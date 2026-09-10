// Package storage implements Zatiti's persistence owner: a single-tenant
// SQLite database accessed through one serialized write path, consistent
// read snapshots, per-transaction units, owner migrations and an atomic
// event outbox.
//
// Open returns the storage implementation of contract.Database. The caller
// must hold the platform installation lock before calling Open; storage
// never acquires locks itself. One controller writer is expected; reads run
// on WAL snapshots that stay consistent for the whole callback.
//
// Guarantees:
//
//   - Write serializes through a single in-process writer, opens a short
//     BEGIN IMMEDIATE transaction, invokes the callback exactly once and
//     commits state and emitted events together. A callback error, fault or
//     panic rolls the whole transaction back.
//   - Read opens a BEGIN DEFERRED snapshot on one connection; every query
//     inside the callback sees the same database state.
//   - ExecContext and Emit on a read snapshot unit are rejected.
//   - Emit appends to storage_events inside the same transaction with a
//     globally increasing sequence; Events is the trusted internal feed, not
//     a public surface.
//   - Migrate applies owner migrations under exclusive ownership, verifies
//     each migration body against its SHA-256 pin, enforces per-owner
//     monotonic versions and rejects a changed already-applied migration.
//   - Backup streams a consistent image produced by the SQLite online
//     backup API, never a copy of a live main database file.
//
// Lock waits are bounded by Config.BusyTimeout (default five seconds);
// busy exhaustion returns a retryable controller_unavailable fault.
package storage
