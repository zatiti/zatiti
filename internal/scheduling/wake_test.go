package scheduling

import (
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Wake behavior: wake.due reads without admitting; wake.admit is the
// transactional gate — restrictive source recheck, occurrence dedup,
// exactly-once fence, bounded task landing and the next-wake decision.

func TestWakeDueListsDueWakesWithoutAdmitting(t *testing.T) {
	e := newEnv(t)
	row := e.createSchedule(e.scheduleDef(e.scope, e.worker))
	resp := e.createResponsibility(responsibilityDef(e.scope, e.worker, e.clock.Now()))

	if wakes := e.wakeDueOp(e.clock.Now(), 100); len(wakes) != 0 {
		t.Fatalf("nothing is due before the first instants: %d wakes", len(wakes))
	}
	due := e.clock.Now().Add(61 * time.Second)
	wakes := e.wakeDueOp(due, 100)
	if len(wakes) != 2 {
		t.Fatalf("expected both sources due, got %d", len(wakes))
	}
	limited := e.wakeDueOp(due, 1)
	if len(limited) != 1 {
		t.Fatalf("limit must bound the page, got %d", len(limited))
	}

	// Reading admits nothing.
	if len(e.pendingWakesFor(row.ID)) != 1 || len(e.pendingWakesFor(resp.ID)) != 1 {
		t.Fatalf("wake.due must not consume wakes")
	}
	if len(e.tasksCreateCalls()) != 0 {
		t.Fatalf("wake.due must not land tasks")
	}
}

func TestWakeAdmitRefusesNotYetDue(t *testing.T) {
	e := newEnv(t)
	row := e.createSchedule(e.scheduleDef(e.scope, e.worker))
	wake := e.mustFindWake(e.pendingWakesFor(row.ID)[0].ID)
	f := e.expectFault(opWakeAdmit, wakeAdmitInput{Wake: wakeWire(wake)}, contract.CodeInvalidInput)
	if f.Message == "" {
		t.Fatalf("not-due fault carries no message")
	}
}

func TestWakeAdmitSchedulePinnedInstant(t *testing.T) {
	e := newEnv(t)
	row := e.createSchedule(e.scheduleDef(e.scope, e.worker))
	wire, err := row.wire()
	if err != nil {
		t.Fatalf("render schedule: %v", err)
	}
	wake := e.mustFindWake(e.pendingWakesFor(row.ID)[0].ID)
	e.clock.Advance(90 * time.Second) // 12:01:30, wake due 12:01

	body := e.admitWakeOp(wake)
	if body.Skipped || body.Task == nil {
		t.Fatalf("pinned instant must admit a task: %+v", body)
	}

	creates := e.tasksCreateCalls()
	if len(creates) != 1 {
		t.Fatalf("expected one tasks.create, got %d", len(creates))
	}
	born := creates[0].Task
	if born.ID == wire.TaskTemplate.ID {
		t.Fatalf("admission must rewrite the template identity")
	}
	if born.Version != 1 || born.State != "draft" {
		t.Fatalf("born task must be a fresh draft: version %d state %q", born.Version, born.State)
	}
	if born.WorkerID != wire.TaskTemplate.WorkerID || born.Scope.InstallationID != row.Scope.InstallationID {
		t.Fatalf("born task must carry the template identity: %+v", born)
	}
	if born.Limits.RootDeadline != formatStamp(e.clock.Now().Add(600*time.Second)) {
		t.Fatalf("born task must be bounded from admission, root deadline %q", born.Limits.RootDeadline)
	}
	if creates[0].SourceID != row.ID || creates[0].OccurrenceKey != wake.OccurrenceKey {
		t.Fatalf("tasks.create must carry the occurrence identity: %+v", creates[0])
	}

	transitions := e.tasksTransitionCalls()
	if len(transitions) != 1 || transitions[0].TaskID != born.ID ||
		transitions[0].ExpectedVersion != 1 || transitions[0].State != "ready" {
		t.Fatalf("draft must fence straight to ready: %+v", transitions)
	}
	enqueues := e.enqueueCalls()
	if len(enqueues) != 1 || enqueues[0].Task.ID != born.ID || enqueues[0].Task.State != "ready" {
		t.Fatalf("ready task must be enqueued: %+v", enqueues)
	}
	if body.Task.ID != born.ID || body.Task.Version != 2 || body.Task.State != "ready" {
		t.Fatalf("admit body must report the ready task: %+v", body.Task)
	}

	occurrences := e.occurrencesOf(row.ID)
	if len(occurrences) != 1 || occurrences[0].State != occurrenceAdmitted ||
		occurrences[0].OccurrenceKey != wake.OccurrenceKey || occurrences[0].TaskRef != string(born.ID) {
		t.Fatalf("admitted occurrence wrong: %+v", occurrences)
	}

	// Exactly one next wake armed at the next cron instant, no version bump.
	wakes := e.pendingWakesFor(row.ID)
	next := e.clock.Now().Add(30 * time.Second) // 12:02
	if len(wakes) != 1 || !wakes[0].DueAt.Equal(next) {
		t.Fatalf("next wake must be armed at 12:02, got %+v", wakes)
	}
	if want := scheduleOccurrenceKey(row.ID, row.Version, next); wakes[0].OccurrenceKey != want {
		t.Fatalf("next wake key %q, want %q", wakes[0].OccurrenceKey, want)
	}
	live, _ := e.mustFindSchedule(row.ID)
	if live.Version != 1 || live.NextWake == nil || !live.NextWake.Equal(next) {
		t.Fatalf("next wake must persist without a version bump: %+v", live)
	}
}

func TestWakeAdmitCoalescesNewestMissedInstant(t *testing.T) {
	e := newEnv(t)
	row := e.createSchedule(e.scheduleDef(e.scope, e.worker)) // coalesce, 120s window
	wake := e.mustFindWake(e.pendingWakesFor(row.ID)[0].ID)
	e.clock.Advance(5*time.Minute + 30*time.Second) // 12:05:30, wake due 12:01

	body := e.admitWakeOp(wake)
	if body.Skipped || body.Task == nil {
		t.Fatalf("pinned instant must admit: %+v", body)
	}

	// Two tasks: the pinned instant and the newest instant inside the
	// catch-up window (12:05, admitted 30s late).
	creates := e.tasksCreateCalls()
	if len(creates) != 2 {
		t.Fatalf("coalesce must admit two tasks, got %d", len(creates))
	}
	if creates[0].OccurrenceKey != wake.OccurrenceKey {
		t.Fatalf("first task must carry the pinned key: %+v", creates[0])
	}
	coalesced := scheduleOccurrenceKey(row.ID, row.Version, e.clock.Now().Add(-30*time.Second))
	if creates[1].OccurrenceKey != coalesced {
		t.Fatalf("second task must carry the coalesced key %q, got %q", coalesced, creates[1].OccurrenceKey)
	}

	occurrences := e.occurrencesOf(row.ID)
	if len(occurrences) != 5 {
		t.Fatalf("expected pinned + coalesced + three misfires, got %d", len(occurrences))
	}
	admitted := 0
	skipped := 0
	for _, o := range occurrences {
		switch o.State {
		case occurrenceAdmitted:
			admitted++
		case occurrenceSkipped:
			skipped++
			if o.Reason != "misfire" {
				t.Fatalf("skipped occurrence reason %q", o.Reason)
			}
		}
	}
	if admitted != 2 || skipped != 3 {
		t.Fatalf("admitted %d skipped %d, want 2 and 3", admitted, skipped)
	}

	// The next wake is armed from now, not from the coalesced instant.
	next := e.clock.Now().Add(30 * time.Second) // 12:06
	wakes := e.pendingWakesFor(row.ID)
	if len(wakes) != 1 || !wakes[0].DueAt.Equal(next) {
		t.Fatalf("next wake must be armed from the admission instant, got %+v", wakes)
	}
}

func TestWakeAdmitSkipPolicyRecordsMissedAsSkipped(t *testing.T) {
	e := newEnv(t)
	def := e.scheduleDef(e.scope, e.worker)
	def.Misfire = misfireSkip
	row := e.createSchedule(def)
	wake := e.mustFindWake(e.pendingWakesFor(row.ID)[0].ID)
	e.clock.Advance(5*time.Minute + 30*time.Second) // 12:05:30, wake due 12:01

	body := e.admitWakeOp(wake)
	if body.Skipped || body.Task == nil {
		t.Fatalf("skip policy still admits the pinned instant: %+v", body)
	}
	if got := len(e.tasksCreateCalls()); got != 1 {
		t.Fatalf("skip policy must admit only the pinned task, got %d", got)
	}
	occurrences := e.occurrencesOf(row.ID)
	skipped := 0
	for _, o := range occurrences {
		if o.State == occurrenceSkipped {
			skipped++
			if o.Reason != "misfire" {
				t.Fatalf("skipped occurrence reason %q", o.Reason)
			}
		}
	}
	if len(occurrences) != 5 || skipped != 4 {
		t.Fatalf("expected pinned admitted and four misfires, got %d rows %d skipped", len(occurrences), skipped)
	}
}

func TestWakeAdmitFenceIsIdempotent(t *testing.T) {
	e := newEnv(t)
	row := e.createSchedule(e.scheduleDef(e.scope, e.worker))
	wake := e.mustFindWake(e.pendingWakesFor(row.ID)[0].ID)
	e.clock.Advance(90 * time.Second)

	first := e.admitWakeOp(wake)
	if first.Skipped {
		t.Fatalf("first delivery must admit")
	}
	second := e.admitWakeOp(wake)
	if !second.Skipped || second.Task != nil {
		t.Fatalf("duplicate delivery must report skipped: %+v", second)
	}
	if got := len(e.tasksCreateCalls()); got != 1 {
		t.Fatalf("duplicate delivery must not land a second task, got %d", got)
	}
	if len(e.occurrencesOf(row.ID)) != 1 {
		t.Fatalf("duplicate delivery must not record another occurrence")
	}
}

// The occurrence-dedup fence (occurrenceExists) is defensive only and has
// no reachable persisted flow: scheduling_wakes UNIQUE-constrains
// (source_id, occurrence_key), consumed wakes keep their row, and the
// delete paths drop only not-yet-admitted wakes — so a wake whose
// occurrence was already recorded can never be re-inserted for a fresh
// delivery. The reachable exactly-once guarantee is the admitted-at fence
// above (TestWakeAdmitFenceIsIdempotent).

func TestWakeAdmitPausedKeepsWakePendingThenReadmits(t *testing.T) {
	e := newEnv(t)
	row := e.createSchedule(e.scheduleDef(e.scope, e.worker))
	wake := e.mustFindWake(e.pendingWakesFor(row.ID)[0].ID)

	e.mustOK(opSchedulePause, pauseResumeInput{Scope: e.scope, ID: row.ID, ExpectedVersion: 1})
	e.clock.Advance(90 * time.Second)

	skipped := e.admitWakeOp(wake)
	if !skipped.Skipped || skipped.Task != nil {
		t.Fatalf("paused admission must skip: %+v", skipped)
	}
	if wakes := e.pendingWakesFor(row.ID); len(wakes) != 1 {
		t.Fatalf("paused admission must not consume the wake, got %d pending", len(wakes))
	}
	if len(e.occurrencesOf(row.ID)) != 0 {
		t.Fatalf("paused admission must not record an occurrence")
	}

	// Resume leaves the durable wake in place; the same delivery then
	// admits through the misfire path.
	e.mustOK(opScheduleResume, pauseResumeInput{Scope: e.scope, ID: row.ID, ExpectedVersion: 2})
	readmitted := e.admitWakeOp(wake)
	if readmitted.Skipped || readmitted.Task == nil {
		t.Fatalf("resume must let the pending wake admit: %+v", readmitted)
	}
	if got := len(e.tasksCreateCalls()); got != 1 {
		t.Fatalf("readmission must land exactly one task, got %d", got)
	}
	// The wake was due 12:01 and admission happens at 12:01:30: no missed
	// instants, so the next wake arms at 12:02 under the post-resume version.
	next := e.clock.Now().Add(30 * time.Second)
	wakes := e.pendingWakesFor(row.ID)
	if len(wakes) != 1 || !wakes[0].DueAt.Equal(next) {
		t.Fatalf("next wake must arm at 12:02, got %+v", wakes)
	}
	if want := scheduleOccurrenceKey(row.ID, 3, next); wakes[0].OccurrenceKey != want {
		t.Fatalf("next wake key must pin the current version %d: %q", 3, wakes[0].OccurrenceKey)
	}
}

func TestWakeAdmitArchivedScheduleRecordsSkippedOccurrence(t *testing.T) {
	e := newEnv(t)
	row := e.createSchedule(e.scheduleDef(e.scope, e.worker))
	e.archiveSchedule(row.ID, 1)

	// Archiving drops the source's own pending wakes; the fence still owes
	// a skipped occurrence to any delivery that arrives after the archive,
	// which is exactly the post-archive wake a controller could carry.
	wake := e.insertWakeRow(e.scope, sourceSchedule, row.ID,
		scheduleOccurrenceKey(row.ID, 2, e.clock.Now().Add(time.Minute)),
		e.clock.Now().Add(30*time.Second), 2)
	e.clock.Advance(time.Minute)

	body := e.admitWakeOp(wake)
	if !body.Skipped || body.Task != nil {
		t.Fatalf("archived admission must skip: %+v", body)
	}
	occurrences := e.occurrencesOf(row.ID)
	if len(occurrences) != 1 || occurrences[0].State != occurrenceSkipped || occurrences[0].Reason != "archived" {
		t.Fatalf("archived admission must record the skipped occurrence: %+v", occurrences)
	}
	if len(e.pendingWakesFor(row.ID)) != 0 {
		t.Fatalf("archived admission must consume the wake")
	}
	if len(e.tasksCreateCalls()) != 0 {
		t.Fatalf("archived admission must not land a task")
	}
}

func TestWakeAdmitArchivedResponsibilityRecordsSkippedOccurrence(t *testing.T) {
	e := newEnv(t)
	row := e.createResponsibility(responsibilityDef(e.scope, e.worker, e.clock.Now()))
	e.archiveResponsibility(row.ID, 1)

	wake := e.insertWakeRow(e.scope, sourceResponsibility, row.ID,
		responsibilityOccurrenceKey(row.ID, 2, e.clock.Now().Add(time.Minute)),
		e.clock.Now().Add(30*time.Second), 2)
	e.clock.Advance(time.Minute)

	body := e.admitWakeOp(wake)
	if !body.Skipped || body.Task != nil {
		t.Fatalf("archived admission must skip: %+v", body)
	}
	occurrences := e.occurrencesOf(row.ID)
	if len(occurrences) != 1 || occurrences[0].State != occurrenceSkipped || occurrences[0].Reason != "archived" {
		t.Fatalf("archived admission must record the skipped occurrence: %+v", occurrences)
	}
	if len(e.pendingWakesFor(row.ID)) != 0 {
		t.Fatalf("archived admission must consume the wake")
	}
}

func TestWakeAdmitMissingSourceConsumesQuietly(t *testing.T) {
	e := newEnv(t)
	ghost := e.ids.New()
	wake := e.insertWakeRow(e.scope, sourceSchedule, ghost,
		scheduleOccurrenceKey(ghost, 1, e.clock.Now().Add(time.Minute)),
		e.clock.Now().Add(30*time.Second), 1)
	e.clock.Advance(time.Minute)

	body := e.admitWakeOp(wake)
	if !body.Skipped || body.Task != nil {
		t.Fatalf("missing source must skip: %+v", body)
	}
	if len(e.occurrencesOf(ghost)) != 0 {
		t.Fatalf("no occurrence row is invented for a source this transaction never saw")
	}
	if len(e.pendingWakes()) != 0 {
		t.Fatalf("missing source must consume the wake")
	}
	if len(e.tasksCreateCalls()) != 0 {
		t.Fatalf("missing source must not land a task")
	}
}

// The storage layer CHECK-constrains wakes to the schedule and
// responsibility source kinds, so the unknown-source branch in wake.admit
// is defensive only and unreachable through persisted rows; there is no
// behavioral test for it.

func TestWakeAdmitSkipsArmingWhenAnotherWakeIsPending(t *testing.T) {
	e := newEnv(t)
	row := e.createSchedule(e.scheduleDef(e.scope, e.worker))
	wake := e.mustFindWake(e.pendingWakesFor(row.ID)[0].ID)

	// A second pending wake for the same source: a concurrent arming must
	// not produce two live wakes, so admission defers to it.
	e.insertWakeRow(e.scope, sourceSchedule, row.ID,
		scheduleOccurrenceKey(row.ID, 1, e.clock.Now().Add(30*time.Second)),
		e.clock.Now().Add(30*time.Second), 1)
	e.clock.Advance(90 * time.Second)

	body := e.admitWakeOp(wake)
	if body.Skipped || body.Task == nil {
		t.Fatalf("pinned instant must admit: %+v", body)
	}
	if got := len(e.tasksCreateCalls()); got != 1 {
		t.Fatalf("admission must land one task, got %d", got)
	}
	wakes := e.pendingWakesFor(row.ID)
	if len(wakes) != 1 {
		t.Fatalf("admission must not arm while another wake is pending, got %d", len(wakes))
	}
	live, _ := e.mustFindSchedule(row.ID)
	if live.NextWake == nil || !live.NextWake.Equal(time.Date(2026, 9, 10, 12, 1, 0, 0, time.UTC)) {
		t.Fatalf("next wake decision must stay with the pending wake: %+v", live.NextWake)
	}
}

func TestWakeAdmitResponsibilityLandsBoundedCycleTask(t *testing.T) {
	e := newEnv(t)
	def := responsibilityDef(e.scope, e.worker, e.clock.Now())
	row := e.createResponsibility(def)
	wake := e.mustFindWake(e.pendingWakesFor(row.ID)[0].ID)
	e.clock.Advance(90 * time.Second)

	body := e.admitWakeOp(wake)
	if body.Skipped || body.Task == nil {
		t.Fatalf("responsibility wake must admit a cycle task: %+v", body)
	}

	creates := e.tasksCreateCalls()
	if len(creates) != 1 {
		t.Fatalf("expected one tasks.create, got %d", len(creates))
	}
	task := creates[0].Task
	if task.OwnerID != row.WorkerID || task.WorkerID != row.WorkerID {
		t.Fatalf("cycle task must be owned by the responsible worker: owner %q worker %q", task.OwnerID, task.WorkerID)
	}
	if task.State != "draft" || task.Version != 1 {
		t.Fatalf("cycle task must be born a draft: %+v", task)
	}
	if task.Outcome != row.Outcome {
		t.Fatalf("cycle task must carry the responsibility outcome: %q", task.Outcome)
	}
	if task.Acceptance.VerifierID != "verifier" {
		t.Fatalf("cycle task must carry the stored acceptance: %+v", task.Acceptance)
	}
	if task.Limits.SpendMicroUnits != def.CycleLimits.SpendMicroUnits {
		t.Fatalf("cycle task must carry the per-cycle spend, got %d", task.Limits.SpendMicroUnits)
	}
	if task.Limits.RootDeadline != formatStamp(e.clock.Now().Add(600*time.Second)) {
		t.Fatalf("cycle task must be bounded from admission, root deadline %q", task.Limits.RootDeadline)
	}
	if creates[0].SourceID != row.ID || creates[0].OccurrenceKey != wake.OccurrenceKey {
		t.Fatalf("tasks.create must carry the occurrence identity: %+v", creates[0])
	}
	if len(e.tasksTransitionCalls()) != 1 || e.tasksTransitionCalls()[0].State != "ready" {
		t.Fatalf("cycle task must transition to ready")
	}
	if len(e.enqueueCalls()) != 1 {
		t.Fatalf("cycle task must be enqueued")
	}

	// Admission never arms a responsibility wake: the next wake is the
	// cycle outcome's durable decision, recorded by cycle.record.
	if wakes := e.pendingWakesFor(row.ID); len(wakes) != 0 {
		t.Fatalf("admission must not arm a responsibility wake, got %d", len(wakes))
	}
	live, _ := e.mustFindResponsibility(row.ID)
	if live.NextWake == nil || !live.NextWake.Equal(time.Date(2026, 9, 10, 12, 1, 0, 0, time.UTC)) {
		t.Fatalf("admission must not move the responsibility next wake: %+v", live.NextWake)
	}
	occurrences := e.occurrencesOf(row.ID)
	if len(occurrences) != 1 || occurrences[0].State != occurrenceAdmitted ||
		occurrences[0].TaskRef != string(task.ID) {
		t.Fatalf("admitted occurrence wrong: %+v", occurrences)
	}
}

func TestWakeAdmitResponsibilityEnforcesAggregateFence(t *testing.T) {
	e := newEnv(t)
	def := responsibilityDef(e.scope, e.worker, e.clock.Now())
	def.AggregateLimits.SpendMicroUnits = 10 // cycle spend is also 10
	row := e.createResponsibility(def)

	e.clock.Advance(90 * time.Second)
	wake := e.mustFindWake(e.pendingWakesFor(row.ID)[0].ID)
	first := e.admitWakeOp(wake)
	if first.Skipped || first.Task == nil {
		t.Fatalf("first cycle must admit inside the aggregate: %+v", first)
	}

	// Recording the cycle makes the aggregate exactly exhausted, so the
	// next admission is refused as budget_unavailable.
	e.recordCycleOp(row.ID, 1, e.clock.Now().Add(30*time.Second), nil, []contract.ID{first.Task.ID})
	e.clock.Advance(time.Minute)
	next := e.mustFindWake(e.pendingWakesFor(row.ID)[0].ID)
	f := e.expectFault(opWakeAdmit, wakeAdmitInput{Wake: wakeWire(next)}, contract.CodeBudgetUnavailable)
	if f.Retryable != true {
		t.Fatalf("budget_unavailable must be retryable")
	}
	if got := len(e.tasksCreateCalls()); got != 1 {
		t.Fatalf("exhausted aggregate must not land a task, got %d", got)
	}
}

// A peer fault during task landing unwinds the whole admission: the fence,
// the occurrence rows and the next-wake arm all live in the caller's
// transaction, so the failed delivery leaves the wake pending and a retry
// after the peer recovers admits cleanly.
func TestWakeAdmitPeerFaultRollsBackTransaction(t *testing.T) {
	e := newEnv(t)
	row := e.createSchedule(e.scheduleDef(e.scope, e.worker))
	wake := e.mustFindWake(e.pendingWakesFor(row.ID)[0].ID)
	e.clock.Advance(90 * time.Second) // 12:01:30, wake due 12:01

	e.ports.failOp("_tasks.create", &contract.Fault{Code: contract.CodeConflict, Message: "fake peer failure"})
	f := e.expectFault(opWakeAdmit, wakeAdmitInput{Wake: wakeWire(wake)}, contract.CodeConflict)
	if f.Message != "fake peer failure" {
		t.Fatalf("peer fault must pass through unchanged, got %q", f.Message)
	}

	// The rolled-back fence is the exactly-once guarantee: the wake reads as
	// never admitted and no occurrence row survived.
	live := e.mustFindWake(wake.ID)
	if live.AdmittedAt != nil {
		t.Fatalf("failed admission must roll the fence back: %+v", live.AdmittedAt)
	}
	if got := len(e.occurrencesOf(row.ID)); got != 0 {
		t.Fatalf("failed admission must record no occurrences, got %d", got)
	}
	if got := len(e.pendingWakesFor(row.ID)); got != 1 {
		t.Fatalf("failed admission must keep the wake pending, got %d", got)
	}

	e.ports.failOp("_tasks.create", nil)
	retry := e.admitWakeOp(wake)
	if retry.Skipped || retry.Task == nil {
		t.Fatalf("retry after the peer recovers must admit: %+v", retry)
	}
	occurrences := e.occurrencesOf(row.ID)
	if len(occurrences) != 1 || occurrences[0].State != occurrenceAdmitted {
		t.Fatalf("retry must record exactly one admitted occurrence: %+v", occurrences)
	}
}
