package scheduling

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Public schedule operations: staging (create/update/archive), exact and
// filtered reads, and the immediate pause/resume restrictive transitions.
// Staging never activates anything: it validates the owned definition and
// hands one sealed typed change to configuration's compiler. Activation
// happens only through _scheduling.activate inside an authorized apply.

// scheduleDefinitionProblems returns the semantic problems of one schedule
// definition beyond what the wire schema enforces. A schedule whose
// expression or timezone cannot resolve would admit nothing, so both are
// checked against the embedded IANA database and the cron evaluator here,
// before the definition can be staged.
func scheduleDefinitionProblems(def scheduleDefinitionInput) []string {
	var problems []string
	switch def.Misfire {
	case misfireCoalesce, misfireSkip:
	default:
		problems = append(problems, "misfire must be coalesce or skip")
	}
	if def.CatchUpSeconds < 0 {
		problems = append(problems, "catch_up_seconds must not be negative")
	}
	if _, err := loadLocation(def.Timezone); err != nil {
		problems = append(problems, "timezone is not a known IANA zone")
	}
	if _, err := parseCron(def.Expression); err != nil {
		problems = append(problems, "expression is not a valid five-field cron expression")
	}
	if def.TaskTemplate.WorkerID == "" {
		problems = append(problems, "task template must name a worker")
	}
	if def.TaskTemplate.Outcome == "" {
		problems = append(problems, "task template must state an outcome")
	}
	if def.TaskTemplate.State != "ready" {
		problems = append(problems, "task template state must be ready")
	}
	if def.TaskTemplate.Scope.InstallationID != def.Scope.InstallationID {
		problems = append(problems, "task template scope must live in the schedule's installation")
	}
	return problems
}

// scheduleResource assembles the full schedule definition carried by a
// staged change. The template is carried verbatim; admission rewrites its
// identity.
func scheduleResource(id contract.ID, version contract.Version, def scheduleDefinitionInput) wireSchedule {
	return wireSchedule{
		ID:             id,
		Version:        version,
		Scope:          def.Scope,
		TaskTemplate:   def.TaskTemplate,
		Timezone:       def.Timezone,
		Expression:     def.Expression,
		Misfire:        def.Misfire,
		CatchUpSeconds: def.CatchUpSeconds,
		Paused:         def.Paused,
	}
}

// wire renders the stored row as the Schedule wire shape.
func (r scheduleRow) wire() (wireSchedule, error) {
	var tpl wireTask
	if err := decodeJSON(string(r.TaskTemplate), &tpl); err != nil {
		return wireSchedule{}, err
	}
	return wireSchedule{
		ID:             r.ID,
		Version:        r.Version,
		Scope:          r.Scope,
		TaskTemplate:   tpl,
		Timezone:       r.Timezone,
		Expression:     r.Expression,
		Misfire:        r.Misfire,
		CatchUpSeconds: r.CatchUpSeconds,
		Paused:         r.Paused,
		NextWake:       r.NextWake,
	}, nil
}

// scheduleCreate stages a new schedule. The resource identity is minted here
// so the draft references a concrete, immutable id.
func (s *Service) scheduleCreate(ctx context.Context, unit contract.Unit, in scheduleCreateInput) (contract.Payload, error) {
	if err := requireMatchingInstallation(unit, in.Definition.Scope); err != nil {
		return contract.Payload{}, err
	}
	if problems := scheduleDefinitionProblems(in.Definition); len(problems) > 0 {
		return contract.Payload{}, invalidInput("schedule definition rejected: %s", problems[0])
	}
	id := s.deps.IDs.New()
	resource := scheduleResource(id, 1, in.Definition)
	defJSON, err := json.Marshal(resource)
	if err != nil {
		return contract.Payload{}, fmt.Errorf("scheduling: encode schedule definition: %w", err)
	}
	draft, err := s.stageChange(ctx, unit, in.Scope, wireChange{
		Kind:            kindSchedule,
		Action:          changeCreate,
		ID:              id,
		ExpectedVersion: 0,
		Definition:      defJSON,
	}, in.DraftID)
	if err != nil {
		return contract.Payload{}, err
	}
	return completed(stagedScheduleBody{Draft: draft, Resource: resource})
}

// scheduleUpdate stages a new version of an existing schedule. The staged
// change carries the post-apply version so the compiler can fence optimistic
// concurrency.
func (s *Service) scheduleUpdate(ctx context.Context, unit contract.Unit, in scheduleUpdateInput) (contract.Payload, error) {
	row, found, err := loadSchedule(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("schedule %s is unknown in this installation", in.ID)
	}
	if row.Archived {
		return contract.Payload{}, conflict("schedule %s is archived and cannot be updated", in.ID)
	}
	if row.Version != in.ExpectedVersion {
		return contract.Payload{}, staleVersion(
			"schedule %s is at version %d, not the expected %d", in.ID, row.Version, in.ExpectedVersion)
	}
	if err := requireMatchingInstallation(unit, in.Definition.Scope); err != nil {
		return contract.Payload{}, err
	}
	if problems := scheduleDefinitionProblems(in.Definition); len(problems) > 0 {
		return contract.Payload{}, invalidInput("schedule definition rejected: %s", problems[0])
	}
	resource := scheduleResource(in.ID, row.Version+1, in.Definition)
	defJSON, err := json.Marshal(resource)
	if err != nil {
		return contract.Payload{}, fmt.Errorf("scheduling: encode schedule definition: %w", err)
	}
	draft, err := s.stageChange(ctx, unit, in.Scope, wireChange{
		Kind:            kindSchedule,
		Action:          changeUpdate,
		ID:              in.ID,
		ExpectedVersion: int64(in.ExpectedVersion),
		Definition:      defJSON,
	}, in.DraftID)
	if err != nil {
		return contract.Payload{}, err
	}
	return completed(stagedScheduleBody{Draft: draft, Resource: resource})
}

// scheduleArchive stages the archive of an existing schedule, carrying the
// current definition so retained history and obligations stay inspectable.
func (s *Service) scheduleArchive(ctx context.Context, unit contract.Unit, in scheduleArchiveInput) (contract.Payload, error) {
	row, found, err := loadSchedule(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("schedule %s is unknown in this installation", in.ID)
	}
	if row.Version != in.ExpectedVersion {
		return contract.Payload{}, staleVersion(
			"schedule %s is at version %d, not the expected %d", in.ID, row.Version, in.ExpectedVersion)
	}
	resource, err := row.wire()
	if err != nil {
		return contract.Payload{}, err
	}
	resource.Version = row.Version + 1
	defJSON, err := json.Marshal(resource)
	if err != nil {
		return contract.Payload{}, fmt.Errorf("scheduling: encode schedule definition: %w", err)
	}
	draft, err := s.stageChange(ctx, unit, in.Scope, wireChange{
		Kind:            kindSchedule,
		Action:          changeArchive,
		ID:              in.ID,
		ExpectedVersion: int64(in.ExpectedVersion),
		Definition:      defJSON,
	}, in.DraftID)
	if err != nil {
		return contract.Payload{}, err
	}
	return completed(stagedScheduleBody{Draft: draft, Resource: resource})
}

// scheduleGet resolves one schedule by identity under the request scope.
func (s *Service) scheduleGet(ctx context.Context, unit contract.Unit, in scheduleGetInput) (contract.Payload, error) {
	if err := requireMatchingInstallation(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	row, found, err := loadSchedule(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("schedule %s is unknown in this installation", in.ID)
	}
	if !scopeContains(in.Scope, row.Scope) {
		return contract.Payload{}, permissionDenied("schedule %s is outside the request scope", in.ID)
	}
	wire, err := row.wire()
	if err != nil {
		return contract.Payload{}, err
	}
	return completed(scheduleBody{Resource: wire})
}

// scheduleList pages schedules; only the organization_id filter applies to
// the resource.
func (s *Service) scheduleList(ctx context.Context, unit contract.Unit, in listInput) (contract.Payload, error) {
	if err := requireMatchingInstallation(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	if unsupportedFilters(in.Filter, map[string]bool{"organization_id": true}) {
		return contract.Payload{}, invalidInput("schedule.list supports only the organization_id filter")
	}
	org := ""
	if in.Filter != nil && in.Filter.OrganizationID != nil {
		org = string(*in.Filter.OrganizationID)
	}
	limit := defaultListLimit
	if in.Limit != nil {
		limit = *in.Limit
	}
	keyset, keysetArgs, err := s.keysetOf(opScheduleList, unit, in.Filter, in.Cursor)
	if err != nil {
		return contract.Payload{}, err
	}
	conds, args := listWhere(unit)
	if org != "" {
		conds = append(conds, `organization_id = ?`)
		args = append(args, org)
	}
	if keyset != "" {
		conds = append(conds, keyset)
		args = append(args, keysetArgs...)
	}
	rows, err := listSchedules(ctx, unit, conds, args, limit+1)
	if err != nil {
		return contract.Payload{}, err
	}
	rows, next, err := s.pageSchedules(opScheduleList, unit, in.Filter, limit, rows, s.deps.Clock.Now())
	if err != nil {
		return contract.Payload{}, err
	}
	items := make([]wireSchedule, 0, len(rows))
	for _, r := range rows {
		w, err := r.wire()
		if err != nil {
			return contract.Payload{}, err
		}
		items = append(items, w)
	}
	return listResult(itemsOut[wireSchedule]{Items: items}, next)
}

// schedulePause disables future admissions immediately. The commit carries no
// compilation and no spending: the pending wake is left in place and a later
// admission rechecks the paused flag transactionally.
func (s *Service) schedulePause(ctx context.Context, unit contract.Unit, in pauseResumeInput) (contract.Payload, error) {
	if err := requireMatchingInstallation(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	row, found, err := loadSchedule(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("schedule %s is unknown in this installation", in.ID)
	}
	if !scopeContains(in.Scope, row.Scope) {
		return contract.Payload{}, permissionDenied("schedule %s is outside the request scope", in.ID)
	}
	if row.Archived {
		return contract.Payload{}, conflict("schedule %s is archived and cannot be paused", in.ID)
	}
	now := s.deps.Clock.Now()
	row.Paused = true
	row.Version++
	row.UpdatedAt = now
	if err := updateScheduleState(ctx, unit, row); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventSchedulePaused, row.ID, row.Version); err != nil {
		return contract.Payload{}, err
	}
	return completed(dispositionBody{Resource: wireDisposition{
		ID: row.ID, Version: row.Version, State: statePaused,
	}})
}

// scheduleResume re-enables admissions. The pending wake survives the pause
// untouched; when none remains (the schedule was created paused) resume arms
// the next wake so the schedule fires again from now.
func (s *Service) scheduleResume(ctx context.Context, unit contract.Unit, in pauseResumeInput) (contract.Payload, error) {
	if err := requireMatchingInstallation(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	row, found, err := loadSchedule(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("schedule %s is unknown in this installation", in.ID)
	}
	if !scopeContains(in.Scope, row.Scope) {
		return contract.Payload{}, permissionDenied("schedule %s is outside the request scope", in.ID)
	}
	if row.Archived {
		return contract.Payload{}, conflict("schedule %s is archived and cannot be resumed", in.ID)
	}
	now := s.deps.Clock.Now()
	row.Paused = false
	row.Version++
	row.UpdatedAt = now
	pending, err := pendingWakeExists(ctx, unit, row.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !pending {
		expr, err := parseCron(row.Expression)
		if err != nil {
			return contract.Payload{}, invalidInput("schedule %s expression is no longer a valid cron expression: %v", row.ID, err)
		}
		loc, err := loadLocation(row.Timezone)
		if err != nil {
			return contract.Payload{}, invalidInput("schedule %s timezone is no longer resolvable", row.ID)
		}
		next, ok := expr.nextAfter(now, loc)
		if !ok {
			return contract.Payload{}, invalidInput("schedule %s expression admits no occurrence within the scheduling horizon", row.ID)
		}
		row.NextWake = &next
		if err := s.insertSourceWake(ctx, unit, row.Scope, sourceSchedule, row.ID,
			scheduleOccurrenceKey(row.ID, row.Version, next), next, row.Version, now); err != nil {
			return contract.Payload{}, err
		}
	}
	if err := updateScheduleDefinition(ctx, unit, row); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventScheduleResumed, row.ID, row.Version); err != nil {
		return contract.Payload{}, err
	}
	return completed(dispositionBody{Resource: wireDisposition{
		ID: row.ID, Version: row.Version, State: stateActive,
	}})
}
