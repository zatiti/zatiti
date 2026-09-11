package scheduling

import (
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Staging and read behavior: staging validates owned definitions and hands
// one sealed change to configuration's compiler without applying anything;
// reads fence scope; pause/resume are immediate restrictive commits whose
// pending wakes survive so a later admission rechecks state transactionally.

func TestScheduleCreateStagesWithoutApplying(t *testing.T) {
	e := newEnv(t)
	body := e.stageSchedule(scheduleCreateInput{Scope: e.scope, Definition: e.scheduleDef(e.scope, e.worker)})
	if body.Draft.ID == "" || body.Resource.ID == "" {
		t.Fatalf("staged body incomplete: %+v", body)
	}
	if body.Resource.Version != 1 || body.Resource.Expression != "* * * * *" {
		t.Fatalf("staged resource wrong: %+v", body.Resource)
	}
	if _, found := e.mustFindSchedule(body.Resource.ID); found {
		t.Fatalf("staging must not apply the schedule before activation")
	}
}

func TestScheduleCreateRejectsBrokenDefinitions(t *testing.T) {
	cases := map[string]func(e *testEnv, d *scheduleDefinitionInput){
		"misfire":            func(_ *testEnv, d *scheduleDefinitionInput) { d.Misfire = "whenever" },
		"negative_catch_up":  func(_ *testEnv, d *scheduleDefinitionInput) { d.CatchUpSeconds = -1 },
		"unknown_timezone":   func(_ *testEnv, d *scheduleDefinitionInput) { d.Timezone = "Mars/Olympus" },
		"local_timezone":     func(_ *testEnv, d *scheduleDefinitionInput) { d.Timezone = "Local" },
		"invalid_expression": func(_ *testEnv, d *scheduleDefinitionInput) { d.Expression = "not cron" },
		"missing_worker":     func(_ *testEnv, d *scheduleDefinitionInput) { d.TaskTemplate.WorkerID = "" },
		"missing_outcome":    func(_ *testEnv, d *scheduleDefinitionInput) { d.TaskTemplate.Outcome = "" },
		"template_state":     func(_ *testEnv, d *scheduleDefinitionInput) { d.TaskTemplate.State = "running" },
		"template_installation": func(e *testEnv, d *scheduleDefinitionInput) {
			d.TaskTemplate.Scope = contract.Scope{InstallationID: e.ids.New()}
		},
	}
	for name, breakDef := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			def := e.scheduleDef(e.scope, e.worker)
			breakDef(e, &def)
			_ = e.expectFault(opScheduleCreate, scheduleCreateInput{Scope: e.scope, Definition: def}, contract.CodeInvalidInput)
		})
	}
}

func TestScheduleCreateRejectsForeignDefinitionInstallation(t *testing.T) {
	e := newEnv(t)
	def := e.scheduleDef(e.scope, e.worker)
	def.Scope = contract.Scope{InstallationID: e.ids.New()}
	_ = e.expectFault(opScheduleCreate, scheduleCreateInput{Scope: e.scope, Definition: def}, contract.CodeInvalidInput)
}

func TestScheduleUpdateStaging(t *testing.T) {
	e := newEnv(t)
	row := e.createSchedule(e.scheduleDef(e.scope, e.worker))

	_ = e.expectFault(opScheduleUpdate, scheduleUpdateInput{
		Scope: e.scope, ID: e.ids.New(), ExpectedVersion: 1,
		Definition: e.scheduleDef(e.scope, e.worker),
	}, contract.CodeNotFound)
	_ = e.expectFault(opScheduleUpdate, scheduleUpdateInput{
		Scope: e.scope, ID: row.ID, ExpectedVersion: 99,
		Definition: e.scheduleDef(e.scope, e.worker),
	}, contract.CodeStaleVersion)

	payload := e.mustOK(opScheduleUpdate, scheduleUpdateInput{
		Scope: e.scope, ID: row.ID, ExpectedVersion: 1,
		Definition: e.scheduleDef(e.scope, e.worker),
	})
	var body stagedScheduleBody
	e.decode(payload.Data, &body)
	if body.Draft.ID == "" || body.Resource.Version != 2 {
		t.Fatalf("staged update wrong: draft %q version %d", body.Draft.ID, body.Resource.Version)
	}
	if live, _ := e.mustFindSchedule(row.ID); live.Version != 1 {
		t.Fatalf("staging must not bump the stored version, got %d", live.Version)
	}
}

func TestScheduleArchiveStaging(t *testing.T) {
	e := newEnv(t)
	row := e.createSchedule(e.scheduleDef(e.scope, e.worker))

	_ = e.expectFault(opScheduleArchive, scheduleArchiveInput{Scope: e.scope, ID: e.ids.New(), ExpectedVersion: 1}, contract.CodeNotFound)
	_ = e.expectFault(opScheduleArchive, scheduleArchiveInput{Scope: e.scope, ID: row.ID, ExpectedVersion: 99}, contract.CodeStaleVersion)

	payload := e.mustOK(opScheduleArchive, scheduleArchiveInput{Scope: e.scope, ID: row.ID, ExpectedVersion: 1})
	var body stagedScheduleBody
	e.decode(payload.Data, &body)
	if body.Resource.Version != 2 || body.Draft.ID == "" {
		t.Fatalf("staged archive wrong: %+v", body)
	}
	if live, _ := e.mustFindSchedule(row.ID); live.Archived {
		t.Fatalf("staging must not archive the schedule before activation")
	}
}

func TestScheduleGetScopeFences(t *testing.T) {
	e := newEnv(t)
	narrowed := e.scope
	narrowed.OrganizationID = e.org
	row := e.createSchedule(e.scheduleDef(narrowed, e.worker))

	got := e.getSchedule(narrowed, row.ID)
	if got.ID != row.ID || got.Version != 1 || got.NextWake == nil {
		t.Fatalf("schedule.get wrong: %+v", got)
	}
	_ = e.expectFault(opScheduleGet, scheduleGetInput{Scope: e.scope, ID: e.ids.New()}, contract.CodeNotFound)

	other := e.scope
	other.OrganizationID = e.ids.New()
	_ = e.expectFault(opScheduleGet, scheduleGetInput{Scope: other, ID: row.ID}, contract.CodePermissionDenied)

	foreign := contract.Scope{InstallationID: e.ids.New()}
	_ = e.expectFault(opScheduleGet, scheduleGetInput{Scope: foreign, ID: row.ID}, contract.CodeInvalidInput)
}

func TestScheduleListPaginationCursorAndFilters(t *testing.T) {
	e := newEnv(t)
	first := e.createSchedule(e.scheduleDef(e.scope, e.worker))
	e.createSchedule(e.scheduleDef(e.scope, e.worker))

	// Unbounded default page returns everything with no cursor.
	items, next := e.listSchedules(e.scope, nil, nil)
	if len(items) != 2 || next != nil {
		t.Fatalf("default page wrong: %d items next %v", len(items), next)
	}

	limit := int64(1)
	items, next = e.listSchedules(e.scope, nil, &limit)
	if len(items) != 1 || items[0].ID != first.ID || next == nil {
		t.Fatalf("first page wrong: %d items next %v", len(items), next)
	}
	items2, next2 := e.listSchedules(e.scope, next, &limit)
	if len(items2) != 1 || items2[0].ID == first.ID {
		t.Fatalf("cursor page returned wrong items: %+v", items2)
	}
	if next2 != nil {
		t.Fatalf("exhausted page must not mint a cursor")
	}

	// A cursor minted without a filter is bound to that shape.
	_, rebound := e.listSchedules(e.scope, nil, &limit)
	otherOrg := e.ids.New()
	_ = e.expectFault(opScheduleList, listInput{Scope: e.scope, Cursor: rebound, Limit: &limit,
		Filter: &listFilter{OrganizationID: &otherOrg}}, contract.CodeInvalidInput)

	// A tampered cursor is refused as invalid_input.
	tampered := []byte(*rebound)
	tampered[len(tampered)-2] = 'x'
	bad := string(tampered)
	_ = e.expectFault(opScheduleList, listInput{Scope: e.scope, Cursor: &bad, Limit: &limit}, contract.CodeInvalidInput)

	// Cursors expire with the snapshot window and demand a fresh snapshot.
	_, fresh := e.listSchedules(e.scope, nil, &limit)
	e.clock.Advance(16 * time.Minute)
	f := e.expectFault(opScheduleList, listInput{Scope: e.scope, Cursor: fresh, Limit: &limit}, contract.CodeCursorExpired)
	if f.Details == nil {
		t.Fatalf("cursor_expired must carry snapshot_required details")
	}

	// Filters the resource does not carry are refused outright.
	state := "ready"
	_ = e.expectFault(opScheduleList, listInput{Scope: e.scope, Filter: &listFilter{State: &state}}, contract.CodeInvalidInput)
}

func TestResponsibilityListFilterContract(t *testing.T) {
	e := newEnv(t)
	e.createResponsibility(responsibilityDef(e.scope, e.worker, e.clock.Now()))
	state := "ready"
	_ = e.expectFault(opResponsibilityList, listInput{Scope: e.scope, Filter: &listFilter{State: &state}}, contract.CodeInvalidInput)

	payload := e.mustOK(opResponsibilityList, listInput{Scope: e.scope})
	var out itemsOut[wireResponsibility]
	e.decode(payload.Data, &out)
	if len(out.Items) != 1 || payload.NextCursor != nil {
		t.Fatalf("responsibility list wrong: %d items", len(out.Items))
	}
}

func TestSchedulePauseResumeLifecycle(t *testing.T) {
	e := newEnv(t)
	narrowed := e.scope
	narrowed.OrganizationID = e.org
	row := e.createSchedule(e.scheduleDef(narrowed, e.worker))
	if len(e.pendingWakesFor(row.ID)) != 1 {
		t.Fatalf("activation must arm exactly one wake")
	}

	_ = e.expectFault(opSchedulePause, pauseResumeInput{Scope: narrowed, ID: e.ids.New(), ExpectedVersion: 1}, contract.CodeNotFound)
	other := narrowed
	other.OrganizationID = e.ids.New()
	_ = e.expectFault(opSchedulePause, pauseResumeInput{Scope: other, ID: row.ID, ExpectedVersion: 1}, contract.CodePermissionDenied)

	payload := e.mustOK(opSchedulePause, pauseResumeInput{Scope: narrowed, ID: row.ID, ExpectedVersion: 1})
	var disposition dispositionBody
	e.decode(payload.Data, &disposition)
	if disposition.Resource.State != statePaused || disposition.Resource.Version != 2 {
		t.Fatalf("pause disposition wrong: %+v", disposition.Resource)
	}

	// The pending wake survives the pause: admission rechecks the paused
	// flag, so the durable next-wake identity must remain.
	if wakes := e.pendingWakesFor(row.ID); len(wakes) != 1 {
		t.Fatalf("pause must keep the pending wake, got %d", len(wakes))
	}

	payload = e.mustOK(opScheduleResume, pauseResumeInput{Scope: narrowed, ID: row.ID, ExpectedVersion: 2})
	e.decode(payload.Data, &disposition)
	if disposition.Resource.State != stateActive || disposition.Resource.Version != 3 {
		t.Fatalf("resume disposition wrong: %+v", disposition.Resource)
	}
	if wakes := e.pendingWakesFor(row.ID); len(wakes) != 1 {
		t.Fatalf("resume with a pending wake must not arm another, got %d", len(wakes))
	}

	e.archiveSchedule(row.ID, 3)
	_ = e.expectFault(opSchedulePause, pauseResumeInput{Scope: narrowed, ID: row.ID, ExpectedVersion: 3}, contract.CodeConflict)
	_ = e.expectFault(opScheduleResume, pauseResumeInput{Scope: narrowed, ID: row.ID, ExpectedVersion: 3}, contract.CodeConflict)
}

func TestScheduleResumeArmsWhenCreatedPaused(t *testing.T) {
	e := newEnv(t)
	def := e.scheduleDef(e.scope, e.worker)
	def.Paused = true
	row := e.createSchedule(def)
	if len(e.pendingWakesFor(row.ID)) != 0 || row.NextWake != nil {
		t.Fatalf("paused activation must arm nothing: %+v", row.NextWake)
	}

	payload := e.mustOK(opScheduleResume, pauseResumeInput{Scope: e.scope, ID: row.ID, ExpectedVersion: 1})
	var disposition dispositionBody
	e.decode(payload.Data, &disposition)
	if disposition.Resource.State != stateActive || disposition.Resource.Version != 2 {
		t.Fatalf("resume disposition wrong: %+v", disposition.Resource)
	}

	wakes := e.pendingWakesFor(row.ID)
	if len(wakes) != 1 || !wakes[0].DueAt.Equal(e.clock.Now().Add(time.Minute)) {
		t.Fatalf("resume must arm one wake at the next instant, got %+v", wakes)
	}
	if want := scheduleOccurrenceKey(row.ID, 2, wakes[0].DueAt); wakes[0].OccurrenceKey != want {
		t.Fatalf("resume wake key %q, want %q", wakes[0].OccurrenceKey, want)
	}
	if live, _ := e.mustFindSchedule(row.ID); live.NextWake == nil || !live.NextWake.Equal(wakes[0].DueAt) {
		t.Fatalf("resume must persist the next wake")
	}

	// A second resume is a version bump without a second wake.
	e.mustOK(opScheduleResume, pauseResumeInput{Scope: e.scope, ID: row.ID, ExpectedVersion: 2})
	if wakes := e.pendingWakesFor(row.ID); len(wakes) != 1 {
		t.Fatalf("second resume must not arm another wake, got %d", len(wakes))
	}
}

func TestResponsibilityStagingAndPauseResume(t *testing.T) {
	e := newEnv(t)
	def := responsibilityDef(e.scope, e.worker, e.clock.Now())
	body := e.stageResponsibility(responsibilityCreateInput{Scope: e.scope, Definition: def})
	if body.Draft.ID == "" || body.Resource.WorkerID != e.worker {
		t.Fatalf("staged responsibility wrong: %+v", body.Resource)
	}
	if _, found := e.mustFindResponsibility(body.Resource.ID); found {
		t.Fatalf("staging must not apply the responsibility before activation")
	}

	// A zero aggregate is a zero budget that admits no positive cycle spend.
	def.AggregateLimits.SpendMicroUnits = 0
	_ = e.expectFault(opResponsibilityCreate, responsibilityCreateInput{Scope: e.scope, Definition: def}, contract.CodeInvalidInput)
	def.AggregateLimits.SpendMicroUnits = 100

	narrowed := e.scope
	narrowed.OrganizationID = e.org
	row := e.createResponsibility(responsibilityDef(narrowed, e.worker, e.clock.Now()))
	_ = e.expectFault(opResponsibilityUpdate, responsibilityUpdateInput{
		Scope: e.scope, ID: row.ID, ExpectedVersion: 42,
		Definition: responsibilityDef(e.scope, e.worker, e.clock.Now()),
	}, contract.CodeStaleVersion)

	if len(e.pendingWakesFor(row.ID)) != 1 {
		t.Fatalf("activation must arm one wake one interval out")
	}
	_ = e.expectFault(opResponsibilityPause, pauseResumeInput{Scope: narrowed, ID: e.ids.New(), ExpectedVersion: 1}, contract.CodeNotFound)
	other := narrowed
	other.OrganizationID = e.ids.New()
	_ = e.expectFault(opResponsibilityPause, pauseResumeInput{Scope: other, ID: row.ID, ExpectedVersion: 1}, contract.CodePermissionDenied)

	payload := e.mustOK(opResponsibilityPause, pauseResumeInput{Scope: narrowed, ID: row.ID, ExpectedVersion: 1})
	var disposition dispositionBody
	e.decode(payload.Data, &disposition)
	if disposition.Resource.State != statePaused || disposition.Resource.Version != 2 {
		t.Fatalf("pause disposition wrong: %+v", disposition.Resource)
	}
	if len(e.pendingWakesFor(row.ID)) != 1 {
		t.Fatalf("pause must keep the pending wake")
	}

	payload = e.mustOK(opResponsibilityResume, pauseResumeInput{Scope: narrowed, ID: row.ID, ExpectedVersion: 2})
	e.decode(payload.Data, &disposition)
	if disposition.Resource.State != stateActive || disposition.Resource.Version != 3 {
		t.Fatalf("resume disposition wrong: %+v", disposition.Resource)
	}
	if len(e.pendingWakesFor(row.ID)) != 1 {
		t.Fatalf("resume with a pending wake must not arm another")
	}

	e.archiveResponsibility(row.ID, 3)
	_ = e.expectFault(opResponsibilityPause, pauseResumeInput{Scope: narrowed, ID: row.ID, ExpectedVersion: 3}, contract.CodeConflict)
	_ = e.expectFault(opResponsibilityResume, pauseResumeInput{Scope: narrowed, ID: row.ID, ExpectedVersion: 3}, contract.CodeConflict)
}

func TestResponsibilityResumeArmsWhenCreatedPaused(t *testing.T) {
	e := newEnv(t)
	def := responsibilityDef(e.scope, e.worker, e.clock.Now())
	def.Paused = true
	row := e.createResponsibility(def)
	if len(e.pendingWakesFor(row.ID)) != 0 || row.NextWake != nil {
		t.Fatalf("paused activation must arm nothing")
	}

	e.mustOK(opResponsibilityResume, pauseResumeInput{Scope: e.scope, ID: row.ID, ExpectedVersion: 1})
	wakes := e.pendingWakesFor(row.ID)
	if len(wakes) != 1 || !wakes[0].DueAt.Equal(e.clock.Now().Add(time.Minute)) {
		t.Fatalf("resume must arm one wake one interval out, got %+v", wakes)
	}
	if want := responsibilityOccurrenceKey(row.ID, 2, wakes[0].DueAt); wakes[0].OccurrenceKey != want {
		t.Fatalf("resume wake key %q, want %q", wakes[0].OccurrenceKey, want)
	}
}

// hasCode reports whether one diagnostic code is present.
func hasCode(codes []string, want string) bool {
	for _, c := range codes {
		if c == want {
			return true
		}
	}
	return false
}

func TestValidateReportsWorkerBindingRequirement(t *testing.T) {
	e := newEnv(t)
	def := e.scheduleDef(e.scope, e.worker)
	id := e.ids.New()
	change := wireChange{
		Kind: kindSchedule, Action: changeCreate, ID: id, ExpectedVersion: 0,
		Definition: rawDef(scheduleChangeResource(id, 1, def)),
	}

	v := e.validateOp(change)
	if len(v.Diagnostics) != 0 {
		t.Fatalf("valid definition must carry no diagnostics: %v", diagCodes(v))
	}
	if len(v.Requirements) != 1 || v.Requirements[0].Code != reqWorkerBinding {
		t.Fatalf("unbound worker must require a binding: %+v", v.Requirements)
	}
	if v.Requirements[0].ResourceID != e.worker {
		t.Fatalf("requirement must name the worker: %+v", v.Requirements[0])
	}
	if len(v.Dependencies) != 0 {
		t.Fatalf("unbound worker must produce no dependency: %+v", v.Dependencies)
	}

	// A worker binding covering the scope satisfies the dependency and is
	// recorded as a versioned dependency on the binding itself.
	binding := peerBinding{
		ID: e.ids.New(), Version: 3, Scope: e.scope, Kind: "worker", TargetID: e.worker,
	}
	e.ports.setBindings([]peerBinding{binding})
	v2 := e.validateOp(change)
	if len(v2.Requirements) != 0 {
		t.Fatalf("binding must clear the requirement: %+v", v2.Requirements)
	}
	if len(v2.Dependencies) != 1 || v2.Dependencies[0].ID != binding.ID || v2.Dependencies[0].Version != binding.Version {
		t.Fatalf("binding must be the recorded dependency: %+v", v2.Dependencies)
	}
}

func TestValidateDiagnosticsOnSliceContent(t *testing.T) {
	e := newEnv(t)
	row := e.createSchedule(e.scheduleDef(e.scope, e.worker))
	def := e.scheduleDef(e.scope, e.worker)

	// Update against the wrong version reports stale.
	v := e.validateOp(wireChange{
		Kind: kindSchedule, Action: changeUpdate, ID: row.ID, ExpectedVersion: 42,
		Definition: rawDef(scheduleChangeResource(row.ID, 43, def)),
	})
	if !hasCode(diagCodes(v), "stale_version") {
		t.Fatalf("expected stale_version diagnostic, got %v", diagCodes(v))
	}

	// Update against an unknown identity.
	unknown := e.ids.New()
	v = e.validateOp(wireChange{
		Kind: kindSchedule, Action: changeUpdate, ID: unknown, ExpectedVersion: 1,
		Definition: rawDef(scheduleChangeResource(unknown, 2, def)),
	})
	if !hasCode(diagCodes(v), "unknown_reference") {
		t.Fatalf("expected unknown_reference diagnostic, got %v", diagCodes(v))
	}

	// The same resource changed twice in one candidate.
	id := e.ids.New()
	change := wireChange{
		Kind: kindSchedule, Action: changeCreate, ID: id, ExpectedVersion: 0,
		Definition: rawDef(scheduleChangeResource(id, 1, def)),
	}
	v = e.validateOp(change, change)
	if !hasCode(diagCodes(v), "duplicate_change") {
		t.Fatalf("expected duplicate_change diagnostic, got %v", diagCodes(v))
	}

	// An action outside create/update/archive — "delete" passes the wire
	// schema's action enum and is refused by the validator instead.
	v = e.validateOp(wireChange{
		Kind: kindSchedule, Action: "delete", ID: id, ExpectedVersion: 0,
		Definition: rawDef(scheduleChangeResource(id, 1, def)),
	})
	if !hasCode(diagCodes(v), "unsupported_action") {
		t.Fatalf("expected unsupported_action diagnostic, got %v", diagCodes(v))
	}

	// A definition whose identity does not match the change.
	v = e.validateOp(wireChange{
		Kind: kindSchedule, Action: changeCreate, ID: e.ids.New(), ExpectedVersion: 0,
		Definition: rawDef(scheduleChangeResource(id, 1, def)),
	})
	if !hasCode(diagCodes(v), "identity_mismatch") {
		t.Fatalf("expected identity_mismatch diagnostic, got %v", diagCodes(v))
	}

	// A definition homed in another installation.
	foreign := e.scheduleDef(e.scope, e.worker)
	foreign.Scope = contract.Scope{InstallationID: e.ids.New()}
	foreignID := e.ids.New()
	v = e.validateOp(wireChange{
		Kind: kindSchedule, Action: changeCreate, ID: foreignID, ExpectedVersion: 0,
		Definition: rawDef(scheduleChangeResource(foreignID, 1, foreign)),
	})
	if !hasCode(diagCodes(v), "installation_mismatch") {
		t.Fatalf("expected installation_mismatch diagnostic, got %v", diagCodes(v))
	}
}
