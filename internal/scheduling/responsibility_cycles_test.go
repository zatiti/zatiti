package scheduling

import (
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// P17: responsibility reasoning cycles and event wakes. These tests prove
// the three required behaviors named in the card: (1) a responsibility's
// first cycle creates bounded work and, once the driven caller (execution)
// records it through the real internal pipeline, automatically arms the
// next wake with no test-only backdoor; (2) an idle "no work found" cycle
// still consumes bounded accounted spend, and a duplicated cycle_id or a
// redelivered event/restart never repeats a cycle; (3) a pause racing a due
// wake, and a reply racing a wake, both admit at most the permitted work.
//
// Two supplementary tests cover the wake-admission recheck this card also
// requires (item 4 of the assignment): an event wake authenticates its
// occurrence key against a genuine pending message rather than trusting the
// caller's string, and a paused responsibility leaves an unauthenticated-
// but-otherwise-valid event unconsumed so a later, legitimate delivery is
// not silently lost.

// TestFirstCycleAdmitsBoundedWorkAndCycleRecordArmsSecondWakeAutomatically
// drives the full loop using only the two real internal operations a
// controller/execution pair would call (_scheduling.wake.admit and
// _scheduling.cycle.record) — no insertWakeRow or other test-only storage
// backdoor ever fabricates a wake. The first cycle's admission alone
// creates bounded work (one draft task, landed through tasks and enqueued);
// recording that cycle through the ordinary driven call is what then arms
// the second wake, automatically, as a direct consequence of the one
// legitimate cycle.record call — never a second, separate or manual step.
func TestFirstCycleAdmitsBoundedWorkAndCycleRecordArmsSecondWakeAutomatically(t *testing.T) {
	e := newEnv(t)
	row := e.createResponsibility(responsibilityDef(e.scope, e.worker, e.clock.Now()))

	// The responsibility's own creation already armed its first wake
	// (activateResponsibilityChange); nothing here manually inserts one.
	initial := e.pendingWakesFor(row.ID)
	if len(initial) != 1 {
		t.Fatalf("responsibility creation must arm exactly one initial wake, got %d", len(initial))
	}

	e.clock.Advance(90 * time.Second) // 12:01:30, wake due 12:01:00
	wake := e.mustFindWake(initial[0].ID)
	admitted := e.admitWakeOp(wake)
	if admitted.Skipped || admitted.Task == nil {
		t.Fatalf("first cycle admission must create bounded work: %+v", admitted)
	}
	if got := len(e.tasksCreateCalls()); got != 1 {
		t.Fatalf("first cycle must land exactly one bounded task, got %d", got)
	}
	// Admission itself never arms a wake: the durable next-wake decision
	// belongs to the recorded cycle outcome, not to admission.
	if got := len(e.pendingWakesFor(row.ID)); got != 0 {
		t.Fatalf("admission must not itself arm a wake, got %d pending", got)
	}

	next := e.clock.Now().Add(45 * time.Second)
	recorded := e.recordCycleOp(row.ID, 1, next,
		[]wireArtifactRef{{ID: e.ids.New(), Digest: candidateDigest()}}, []contract.ID{admitted.Task.ID})
	if recorded.Version != 2 {
		t.Fatalf("recording the cycle must bump the version, got %d", recorded.Version)
	}
	if recorded.NextWake == nil || !recorded.NextWake.Equal(next) {
		t.Fatalf("recording the cycle must report the durable next wake: %+v", recorded.NextWake)
	}

	second := e.pendingWakesFor(row.ID)
	if len(second) != 1 || !second[0].DueAt.Equal(next) {
		t.Fatalf("cycle.record must automatically arm the second wake, got %+v", second)
	}
}

// TestNoWorkCycleConsumesBoundedAccountedSpend proves an idle reasoning
// pass — a cycle that discovers nothing to do and reports no outputs and no
// task_ids — still counts against the responsibility's per-cycle and
// aggregate spend limits exactly like a working cycle: an idle loop cannot
// reconsider or spend indefinitely.
func TestNoWorkCycleConsumesBoundedAccountedSpend(t *testing.T) {
	e := newEnv(t)
	def := responsibilityDef(e.scope, e.worker, e.clock.Now())
	def.AggregateLimits.SpendMicroUnits = 25 // cycle spend is 10: two no-work cycles fit, a third does not
	row := e.createResponsibility(def)

	for i := 0; i < 2; i++ {
		e.clock.Advance(90 * time.Second)
		wake := e.mustFindWake(e.pendingWakesFor(row.ID)[0].ID)
		admitted := e.admitWakeOp(wake)
		if admitted.Skipped || admitted.Task == nil {
			t.Fatalf("no-work cycle %d must still admit inside the aggregate: %+v", i, admitted)
		}
		// A genuine no-work decision: neither outputs nor task_ids.
		recorded := e.recordCycleOp(row.ID, contract.Version(i+1), e.clock.Now().Add(90*time.Second), nil, nil)
		if recorded.Version != contract.Version(i+2) {
			t.Fatalf("no-work cycle %d must still bump the version, got %d", i, recorded.Version)
		}
	}
	if got := len(e.cyclesOf(row.ID)); got != 2 {
		t.Fatalf("expected two recorded no-work cycles, got %d", got)
	}

	// A third admission would exceed the aggregate: the two no-work cycles
	// already spent the same per-cycle amount a working cycle would.
	e.clock.Advance(90 * time.Second)
	next := e.mustFindWake(e.pendingWakesFor(row.ID)[0].ID)
	f := e.expectFault(opWakeAdmit, wakeAdmitInput{Wake: wakeWire(next)}, contract.CodeBudgetUnavailable)
	if !f.Retryable {
		t.Fatalf("budget_unavailable must be retryable")
	}
	if got := len(e.tasksCreateCalls()); got != 2 {
		t.Fatalf("the exhausted aggregate must land no further task, got %d", got)
	}
}

// TestDuplicateCycleRecordDoesNotRepeatACycle proves cycle_id is the
// idempotency fence the frozen schema documents: a restart or a
// lost-acknowledgement retry resubmitting the identical cycle_id replays
// the original disposition without inserting a second cycle row or arming
// a second wake, and the same cycle_id with different content is
// submission_conflict, never a silent overwrite.
func TestDuplicateCycleRecordDoesNotRepeatACycle(t *testing.T) {
	e := newEnv(t)
	row := e.createResponsibility(responsibilityDef(e.scope, e.worker, e.clock.Now()))
	e.clock.Advance(90 * time.Second)
	admitted := e.admitWakeOp(e.mustFindWake(e.pendingWakesFor(row.ID)[0].ID))

	cycleID := e.ids.New()
	next := e.clock.Now().Add(30 * time.Second)
	outputs := []wireArtifactRef{{ID: e.ids.New(), Digest: candidateDigest()}}
	taskIDs := []contract.ID{admitted.Task.ID}

	first := e.recordCycleOpWithID(row.ID, cycleID, 1, next, outputs, taskIDs)
	if first.Version != 2 {
		t.Fatalf("first record must bump the version, got %d", first.Version)
	}

	// A restart resubmits the identical cycle_id and content.
	replay := e.recordCycleOpWithID(row.ID, cycleID, 1, next, outputs, taskIDs)
	if replay.Version != 2 {
		t.Fatalf("a replay must report the current version, got %d", replay.Version)
	}
	if got := len(e.cyclesOf(row.ID)); got != 1 {
		t.Fatalf("a replay must not insert a second cycle row, got %d", got)
	}
	if got := len(e.pendingWakesFor(row.ID)); got != 1 {
		t.Fatalf("a replay must not arm a second wake, got %d", got)
	}

	// The same cycle_id resubmitted with different content is a genuine
	// conflict, checked before (and regardless of) expected_version.
	f := e.expectFault(opCycleRecord,
		e.cycleRecordInput(row.ID, cycleID, 1, next.Add(time.Second), outputs, taskIDs),
		contract.CodeSubmissionConflict)
	if f.Message == "" {
		t.Fatalf("submission_conflict must carry a message")
	}
	if got := len(e.cyclesOf(row.ID)); got != 1 {
		t.Fatalf("a conflicting resubmission must not mutate the recorded cycle, got %d rows", got)
	}
}

// TestDuplicateEventWakeRestartDoesNotRepeatACycle proves the event-wake
// admission path is idempotent by wake identity exactly like the timer
// path: a controller restart that redelivers the identical Wake object
// (the same wake id, minted once per durable event) replays skipped rather
// than landing a second task.
func TestDuplicateEventWakeRestartDoesNotRepeatACycle(t *testing.T) {
	e := newEnv(t)
	def := responsibilityDef(e.scope, e.worker, e.clock.Now())
	def.Triggers = []string{"reply"}
	row := e.createResponsibility(def)

	msgID := e.ids.New()
	e.ports.setPendingMessages(row.WorkerID, msgID)
	wake := wireWake{
		ID: e.ids.New(), Scope: e.scope, SourceID: row.ID,
		OccurrenceKey: string(msgID), DueAt: e.clock.Now(), ConditionVersion: row.Version,
	}

	first := e.admitWakeWireOp(wake)
	if first.Skipped || first.Task == nil {
		t.Fatalf("the first authenticated event delivery must admit: %+v", first)
	}
	if got := len(e.tasksCreateCalls()); got != 1 {
		t.Fatalf("expected exactly one landed task, got %d", got)
	}

	// A restart (or a lost-acknowledgement retry) redelivers the identical
	// wake object.
	second := e.admitWakeWireOp(wake)
	if !second.Skipped || second.Task != nil {
		t.Fatalf("a redelivered event wake must not repeat a cycle: %+v", second)
	}
	if got := len(e.tasksCreateCalls()); got != 1 {
		t.Fatalf("the redelivery must not land a second task, got %d", got)
	}
	if got := len(e.occurrencesOf(row.ID)); got != 1 {
		t.Fatalf("the redelivery must not record a second occurrence, got %d", got)
	}
}

// TestPauseVersusDueWakeAdmitsAtMostPermittedWork races a responsibility's
// pause against its own due timer wake: pause commits first, and admission
// of the now-due wake must land no work at all, not one bounded cycle.
func TestPauseVersusDueWakeAdmitsAtMostPermittedWork(t *testing.T) {
	e := newEnv(t)
	row := e.createResponsibility(responsibilityDef(e.scope, e.worker, e.clock.Now()))
	wake := e.mustFindWake(e.pendingWakesFor(row.ID)[0].ID)

	e.mustOK(opResponsibilityPause, pauseResumeInput{Scope: e.scope, ID: row.ID, ExpectedVersion: 1})
	e.clock.Advance(90 * time.Second)

	body := e.admitWakeOp(wake)
	if !body.Skipped || body.Task != nil {
		t.Fatalf("a pause that committed before the due wake must admit no work: %+v", body)
	}
	if got := len(e.tasksCreateCalls()); got != 0 {
		t.Fatalf("the pause-versus-due-wake race must land no task, got %d", got)
	}
}

// TestReplyVersusWakeRaceAdmitsAtMostPermittedWork proves the reply/event
// trigger channel and the timer channel share one pacing fence: once the
// timer-driven cycle has been recorded, a reply that arrives inside the
// same minimum reconsideration interval is refused — the two channels
// together admit at most the one permitted cycle, not two.
func TestReplyVersusWakeRaceAdmitsAtMostPermittedWork(t *testing.T) {
	e := newEnv(t)
	def := responsibilityDef(e.scope, e.worker, e.clock.Now())
	def.Triggers = []string{"reply"}
	row := e.createResponsibility(def)

	e.clock.Advance(90 * time.Second)
	admitted := e.admitWakeOp(e.mustFindWake(e.pendingWakesFor(row.ID)[0].ID))
	if admitted.Skipped || admitted.Task == nil {
		t.Fatalf("the due timer wake must admit: %+v", admitted)
	}
	e.recordCycleOp(row.ID, 1, e.clock.Now().Add(90*time.Second), nil, []contract.ID{admitted.Task.ID})

	// A reply arrives immediately after, well inside the minimum
	// reconsideration interval the just-recorded cycle started.
	msgID := e.ids.New()
	e.ports.setPendingMessages(row.WorkerID, msgID)
	reply := e.admitWakeWireOp(wireWake{
		ID: e.ids.New(), Scope: e.scope, SourceID: row.ID,
		OccurrenceKey: string(msgID), DueAt: e.clock.Now(), ConditionVersion: 2,
	})
	if !reply.Skipped || reply.Task != nil {
		t.Fatalf("a reply inside the minimum reconsideration interval must admit no work: %+v", reply)
	}
	if got := len(e.tasksCreateCalls()); got != 1 {
		t.Fatalf("the race must admit at most the one permitted cycle, got %d tasks", got)
	}
}

// TestEventWakeRefusesUnauthenticatedOccurrenceKey proves an event wake is
// authenticated against a genuine pending message, never against the
// caller's occurrence key string alone: an occurrence key naming nothing
// _messaging.pending actually admitted for the worker is refused, and lands
// no task.
func TestEventWakeRefusesUnauthenticatedOccurrenceKey(t *testing.T) {
	e := newEnv(t)
	row := e.createResponsibility(responsibilityDef(e.scope, e.worker, e.clock.Now()))
	// No pending messages are seeded for the worker.

	f := e.expectFault(opWakeAdmit, wakeAdmitInput{Wake: wireWake{
		ID: e.ids.New(), Scope: e.scope, SourceID: row.ID,
		OccurrenceKey: "forged-event-id", DueAt: e.clock.Now(), ConditionVersion: row.Version,
	}}, contract.CodeInvalidInput)
	if f.Message == "" {
		t.Fatalf("the authentication failure must carry a message")
	}
	if got := len(e.tasksCreateCalls()); got != 0 {
		t.Fatalf("an unauthenticated event wake must land no task, got %d", got)
	}
}

// TestEventWakePausedResponsibilityLeavesEventUnconsumed proves pause is
// rechecked at event-wake admission exactly as it is at timer-wake
// admission, and that refusing a paused admission persists nothing: the
// identical event admits once the responsibility resumes, rather than
// being silently lost.
func TestEventWakePausedResponsibilityLeavesEventUnconsumed(t *testing.T) {
	e := newEnv(t)
	row := e.createResponsibility(responsibilityDef(e.scope, e.worker, e.clock.Now()))
	e.mustOK(opResponsibilityPause, pauseResumeInput{Scope: e.scope, ID: row.ID, ExpectedVersion: 1})

	msgID := e.ids.New()
	e.ports.setPendingMessages(row.WorkerID, msgID)
	body := e.admitWakeWireOp(wireWake{
		ID: e.ids.New(), Scope: e.scope, SourceID: row.ID,
		OccurrenceKey: string(msgID), DueAt: e.clock.Now(), ConditionVersion: 2,
	})
	if !body.Skipped || body.Task != nil {
		t.Fatalf("a paused responsibility must admit no event-triggered work: %+v", body)
	}
	if got := len(e.tasksCreateCalls()); got != 0 {
		t.Fatalf("the paused attempt must land no task, got %d", got)
	}

	e.mustOK(opResponsibilityResume, pauseResumeInput{Scope: e.scope, ID: row.ID, ExpectedVersion: 2})
	resumed := e.admitWakeWireOp(wireWake{
		ID: e.ids.New(), Scope: e.scope, SourceID: row.ID, // a fresh delivery: nothing persisted from the paused attempt
		OccurrenceKey: string(msgID), DueAt: e.clock.Now(), ConditionVersion: 3,
	})
	if resumed.Skipped || resumed.Task == nil {
		t.Fatalf("the unconsumed event must admit once resumed: %+v", resumed)
	}
}
