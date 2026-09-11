package scheduling

import (
	"math"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// The cycle record operation: execution reports one completed bounded cycle
// behind the responsibility's version fence; recording counts the spend,
// persists the durable next-wake decision and arms the next cycle.

func TestCycleRecordPersistsCycleAndArmsNextWake(t *testing.T) {
	e := newEnv(t)
	row := e.createResponsibility(responsibilityDef(e.scope, e.worker, e.clock.Now()))
	e.clock.Advance(90 * time.Second) // 12:01:30, wake due 12:01

	admitted := e.admitWakeOp(e.mustFindWake(e.pendingWakesFor(row.ID)[0].ID))
	if admitted.Task == nil {
		t.Fatalf("admission must land the cycle task")
	}

	output := wireArtifactRef{ID: e.ids.New(), Digest: candidateDigest()}
	recorded := e.recordCycleOp(row.ID, 1, e.clock.Now().Add(30*time.Second),
		[]wireArtifactRef{output}, []contract.ID{admitted.Task.ID})
	if recorded.Version != 2 {
		t.Fatalf("record must bump the version, got %d", recorded.Version)
	}
	if recorded.NextWake == nil || !recorded.NextWake.Equal(e.clock.Now().Add(30*time.Second)) {
		t.Fatalf("record must report the durable next wake: %+v", recorded.NextWake)
	}

	cycles := e.cyclesOf(row.ID)
	if len(cycles) != 1 {
		t.Fatalf("expected one recorded cycle, got %d", len(cycles))
	}
	cycle := cycles[0]
	if cycle.ResponsibilityVersion != 1 {
		t.Fatalf("cycle must pin the pre-bump version, got %d", cycle.ResponsibilityVersion)
	}
	if !cycle.NextWake.Equal(e.clock.Now().Add(30 * time.Second)) {
		t.Fatalf("cycle next wake wrong: %v", cycle.NextWake)
	}
	if len(cycle.TaskIDs) != 1 || cycle.TaskIDs[0] != admitted.Task.ID {
		t.Fatalf("cycle must carry the produced task ids: %+v", cycle.TaskIDs)
	}
	if len(cycle.Outputs) != 1 || cycle.Outputs[0].ID != output.ID {
		t.Fatalf("cycle must carry the accountable outputs: %+v", cycle.Outputs)
	}
	if !cycle.RecordedAt.Equal(e.clock.Now()) {
		t.Fatalf("cycle recorded at %v, want the pinned instant", cycle.RecordedAt)
	}

	live, _ := e.mustFindResponsibility(row.ID)
	if live.Version != 2 || live.NextWake == nil || !live.NextWake.Equal(cycle.NextWake) {
		t.Fatalf("responsibility must advance to the recorded next wake: %+v", live)
	}
	if live.LastCycleAt == nil || !live.LastCycleAt.Equal(cycle.RecordedAt) {
		t.Fatalf("last cycle instant wrong: %+v", live.LastCycleAt)
	}

	// The next wake arms under the post-bump version after leftovers drop.
	wakes := e.pendingWakesFor(row.ID)
	if len(wakes) != 1 || !wakes[0].DueAt.Equal(cycle.NextWake) {
		t.Fatalf("next cycle wake must be armed, got %+v", wakes)
	}
	if want := responsibilityOccurrenceKey(row.ID, 2, cycle.NextWake); wakes[0].OccurrenceKey != want {
		t.Fatalf("next wake key %q, want %q", wakes[0].OccurrenceKey, want)
	}
	if wakes[0].ConditionVersion != 2 {
		t.Fatalf("next wake must pin the post-bump version, got %d", wakes[0].ConditionVersion)
	}
}

func TestCycleRecordFaults(t *testing.T) {
	e := newEnv(t)
	row := e.createResponsibility(responsibilityDef(e.scope, e.worker, e.clock.Now()))
	outputs := []wireArtifactRef{}
	taskIDs := []contract.ID{}

	_ = e.expectFault(opCycleRecord, cycleRecordInput{
		ResponsibilityID: e.ids.New(), ExpectedVersion: 1,
		NextWake: e.clock.Now().Add(time.Minute), Outputs: outputs, TaskIDs: taskIDs,
	}, contract.CodeNotFound)

	// The minimum reconsideration interval fences consecutive cycle starts.
	_ = e.expectFault(opCycleRecord, cycleRecordInput{
		ResponsibilityID: row.ID, ExpectedVersion: 1,
		NextWake: e.clock.Now().Add(-30 * time.Second), Outputs: outputs, TaskIDs: taskIDs,
	}, contract.CodeInvalidInput)

	_ = e.expectFault(opCycleRecord, cycleRecordInput{
		ResponsibilityID: row.ID, ExpectedVersion: 99,
		NextWake: e.clock.Now().Add(time.Minute), Outputs: outputs, TaskIDs: taskIDs,
	}, contract.CodeStaleVersion)

	e.archiveResponsibility(row.ID, 1)
	_ = e.expectFault(opCycleRecord, cycleRecordInput{
		ResponsibilityID: row.ID, ExpectedVersion: 2,
		NextWake: e.clock.Now().Add(time.Minute), Outputs: outputs, TaskIDs: taskIDs,
	}, contract.CodeConflict)
}

func TestCycleRecordAggregateFenceRejectsExhaustedBudget(t *testing.T) {
	e := newEnv(t)
	def := responsibilityDef(e.scope, e.worker, e.clock.Now())
	def.AggregateLimits.SpendMicroUnits = 10 // cycle spend is also 10
	row := e.createResponsibility(def)

	e.clock.Advance(90 * time.Second)
	admitted := e.admitWakeOp(e.mustFindWake(e.pendingWakesFor(row.ID)[0].ID))
	e.recordCycleOp(row.ID, 1, e.clock.Now().Add(30*time.Second), nil, []contract.ID{admitted.Task.ID})

	_ = e.expectFault(opCycleRecord, cycleRecordInput{
		ResponsibilityID: row.ID, ExpectedVersion: 2,
		NextWake: e.clock.Now().Add(90 * time.Second),
		Outputs:  []wireArtifactRef{}, TaskIDs: []contract.ID{},
	}, contract.CodeBudgetUnavailable)
	if got := len(e.cyclesOf(row.ID)); got != 1 {
		t.Fatalf("exhausted aggregate must not record, got %d cycles", got)
	}
}

// TestCycleRecordOverflowFailsClosed drives the checked product past its
// representable range: a per-cycle and aggregate limit at the int64 maximum
// admits the first cycle (one cycle spends exactly the aggregate), and the
// second cycle's product overflows, which the fence treats as exceeded.
func TestCycleRecordOverflowFailsClosed(t *testing.T) {
	e := newEnv(t)
	def := responsibilityDef(e.scope, e.worker, e.clock.Now())
	def.CycleLimits.SpendMicroUnits = math.MaxInt64
	def.AggregateLimits.SpendMicroUnits = math.MaxInt64
	row := e.createResponsibility(def)

	e.clock.Advance(90 * time.Second)
	admitted := e.admitWakeOp(e.mustFindWake(e.pendingWakesFor(row.ID)[0].ID))
	if admitted.Skipped || admitted.Task == nil {
		t.Fatalf("first cycle must admit: %+v", admitted)
	}
	e.recordCycleOp(row.ID, 1, e.clock.Now().Add(30*time.Second), nil, []contract.ID{admitted.Task.ID})

	_ = e.expectFault(opCycleRecord, cycleRecordInput{
		ResponsibilityID: row.ID, ExpectedVersion: 2,
		NextWake: e.clock.Now().Add(90 * time.Second),
		Outputs:  []wireArtifactRef{}, TaskIDs: []contract.ID{},
	}, contract.CodeBudgetUnavailable)
	if got := len(e.cyclesOf(row.ID)); got != 1 {
		t.Fatalf("overflow must not record, got %d cycles", got)
	}
}
