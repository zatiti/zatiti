// Package tasks owns durable bounded work: tasks pin an accountable
// principal, scope, assigned worker, outcome, input artifacts, required
// outputs, an immutable accepted verifier contract and a finite resource
// envelope. Admission seals the acceptance contract (verifier identity,
// version and code digest, sealed inputs, expected observations, mode) and
// stores its canonical digest beside the exact contract bytes. Success is
// never claimed into existence: a transition to succeeded recomputes the
// seal, evaluates every expected observation against verifier-held evidence
// and checks required children; a worker report or process exit alone
// cannot establish success, and an explicitly manual acceptance stays
// labeled manual forever. The dependency graph and delegation tree refuse
// cycles and expansion: children inherit intersected authority, project
// data, deadlines and root budgets under finite depth, child count,
// concurrency, planner-step and wall-time limits.
package tasks
