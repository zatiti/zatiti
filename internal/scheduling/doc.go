// Package scheduling owns durable schedules, responsibility reasoning
// cycles, occurrence identities and wake conditions.
//
// Domain boundaries: public schedule and responsibility operations stage
// typed definitions through configuration's compiler and never activate a
// definition directly; restrictive pause/resume commits immediately without
// compilation or spending. Internal operations serve the compiler
// (_scheduling.validate, _scheduling.activate) and the controller
// (_scheduling.wake.due, _scheduling.wake.admit) and execution
// (_scheduling.cycle.record). The scheduler itself is driven only by the
// controller: no goroutine scheduler runs in the service constructor, and
// every tick reads due wakes under an injected clock.
//
// Occurrence identity is the deduplication fence: a schedule occurrence key
// is the schedule ID plus the pinned template version plus the UTC instant;
// an event wake's key is the durable event ID. Admission rechecks pause,
// archive and reply conditions transactionally, admits each distinct instant
// once, persists task creation, the occurrence key and the next wake in one
// transaction, and advances the durable next wake atomically with the
// admitted work.
package scheduling

import "github.com/zatiti/zatiti/internal/contract"

var _ contract.Module = (*Service)(nil)
