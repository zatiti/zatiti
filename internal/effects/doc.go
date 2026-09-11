// Package effects owns immutable actions, logical effects, physical attempts,
// dispatch claims and reconciliation history of a zatiti installation.
//
// Every protected effect separates three records: the action (exact intent,
// bound by a SHA-256 digest that reviews reference), the operation (one
// logical effect moving through the frozen state machine) and the attempt
// (one physical provider invocation). Execution runs as three short local
// transactions around a network call — admit, claim, record — and the
// controller owns the actual adapter invocation; this package never performs
// network work. Once an attempt is claimed, a lost response preserves
// outcome_unknown and its budget reservation until authoritative evidence
// resolves it: timeouts, cancellations and eventually consistent not-found
// never prove non-execution. Earlier unknown attempts survive later failed
// retries, immutable observations are never rewritten, and contradictory
// late evidence appends an explicit correction or dispute instead.
package effects
