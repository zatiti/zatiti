// Package accounting owns budget definitions, atomic reservations and the
// exact cost/uncertainty ledgers of a zatiti installation.
//
// Money is int64 micro-units of one explicit ISO currency; rates are rational
// (integer numerator/denominator) and round reservations upward with checked
// arithmetic — floating point never touches money. A reservation commits
// enforceable cost and one concurrency slot across installation, ancestor
// organizations (root down), project, worker and root task in one stable
// order inside the caller's transaction; every level shares its aggregate
// position with all other children. Usage is recorded separately as spent,
// reserved, estimated and unknown; unknown physical effects and advisory
// external spend are never presented as zero, and only proven-unused bounds
// are released. Paid work is refused until the installation explicitly
// selects a currency and a finite spend ceiling.
package accounting
