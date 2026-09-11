// Package execution owns the lifecycle of governed work in a zatiti
// installation: runs, attempts, leases, checkpoints, context lineage,
// verification jobs and the shared durable execution_jobs queue.
//
// A run pins one ready task/version with its effective configuration; a
// claim creates exactly one current attempt per run under one lease and one
// generation, reserving the spend envelope through accounting in the same
// transaction. A lost acknowledgement replays the original claim — the same
// attempt and lease, never a second owner. Heartbeats extend the current
// lease only for the bound identity; an expired lease fences Zatiti
// mutations and records recovery obligations but never asserts that any
// external process stopped. Replacement waits for the explicit recovery
// disposition, and uncertain physical effects stay retained.
//
// The hosted loop persists every model-visible message, instruction, tool
// result and memory recall as lineage before dispatch, parses model tool
// proposals as untrusted typed requests authorized against the pinned tool
// closure, and applies limits at every step. Reports bind the exact
// worker/attempt/lease/generation, settle the reservation through
// accounting and enter independent verification; the trusted controller
// verifier path alone transitions the task. Worker assertions are recorded
// as observations, never proof.
package execution
