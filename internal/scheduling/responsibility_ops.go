package scheduling

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Public responsibility operations: staging (create/update/archive), exact
// and filtered reads, and the immediate pause/resume restrictive
// transitions. A responsibility is one ongoing reasoning cycle owned by a
// worker; its worker binding is the dependency the validate operation
// resolves, not a fact staging can invent.

// responsibilityDefinitionProblems returns the semantic problems of one
// responsibility definition beyond what the wire schema enforces. Cycle
// and aggregate limits must be mutually consistent: a per-cycle estimate
// above the aggregate would exhaust the aggregate on the first cycle, and
// a zero aggregate is a zero budget that admits no positive cycle spend.
func responsibilityDefinitionProblems(def responsibilityDefinitionInput) []string {
	var problems []string
	if def.WorkerID == "" {
		problems = append(problems, "worker_id must name the owning worker")
	}
	if def.Outcome == "" {
		problems = append(problems, "outcome must be stated")
	}
	if def.MinIntervalSeconds < 1 {
		problems = append(problems, "min_interval_seconds must be at least one second")
	}
	if def.CycleLimits.SpendMicroUnits < 0 || def.AggregateLimits.SpendMicroUnits < 0 {
		problems = append(problems, "spend limits must not be negative")
	}
	if def.CycleLimits.SpendMicroUnits > def.AggregateLimits.SpendMicroUnits {
		problems = append(problems, "cycle spend must not exceed the aggregate spend")
	}
	if def.CycleLimits.Currency != def.AggregateLimits.Currency {
		problems = append(problems, "cycle and aggregate spend limits must share one currency")
	}
	return problems
}

// responsibilityResource assembles the full responsibility definition
// carried by a staged change.
func responsibilityResource(id contract.ID, version contract.Version, def responsibilityDefinitionInput) wireResponsibility {
	return wireResponsibility{
		ID:                   id,
		Version:              version,
		Scope:                def.Scope,
		WorkerID:             def.WorkerID,
		Outcome:              def.Outcome,
		Signals:              def.Signals,
		Triggers:             def.Triggers,
		ReasoningPolicy:      def.ReasoningPolicy,
		MinIntervalSeconds:   def.MinIntervalSeconds,
		CycleLimits:          def.CycleLimits,
		AggregateLimits:      def.AggregateLimits,
		PauseConditions:      def.PauseConditions,
		EscalationConditions: def.EscalationConditions,
		Acceptance:           def.Acceptance,
		Paused:               def.Paused,
	}
}

// wire renders the stored row as the Responsibility wire shape.
func (r responsibilityRow) wire() (wireResponsibility, error) {
	var acceptance wireAcceptance
	if err := decodeJSON(string(r.Acceptance), &acceptance); err != nil {
		return wireResponsibility{}, err
	}
	return wireResponsibility{
		ID:                   r.ID,
		Version:              r.Version,
		Scope:                r.Scope,
		WorkerID:             r.WorkerID,
		Outcome:              r.Outcome,
		Signals:              r.Signals,
		Triggers:             r.Triggers,
		ReasoningPolicy:      r.ReasoningPolicy,
		MinIntervalSeconds:   r.MinIntervalSeconds,
		CycleLimits:          r.CycleLimits,
		AggregateLimits:      r.AggregateLimits,
		PauseConditions:      r.PauseConditions,
		EscalationConditions: r.EscalationConditions,
		Acceptance:           acceptance,
		Paused:               r.Paused,
		NextWake:             r.NextWake,
	}, nil
}

// responsibilityCreate stages a new responsibility. The resource identity is
// minted here so the draft references a concrete, immutable id.
func (s *Service) responsibilityCreate(ctx context.Context, unit contract.Unit, in responsibilityCreateInput) (contract.Payload, error) {
	if err := requireMatchingInstallation(unit, in.Definition.Scope); err != nil {
		return contract.Payload{}, err
	}
	if problems := responsibilityDefinitionProblems(in.Definition); len(problems) > 0 {
		return contract.Payload{}, invalidInput("responsibility definition rejected: %s", problems[0])
	}
	id := s.deps.IDs.New()
	resource := responsibilityResource(id, 1, in.Definition)
	defJSON, err := json.Marshal(resource)
	if err != nil {
		return contract.Payload{}, fmt.Errorf("scheduling: encode responsibility definition: %w", err)
	}
	draft, err := s.stageChange(ctx, unit, in.Scope, wireChange{
		Kind:            kindResponsibility,
		Action:          changeCreate,
		ID:              id,
		ExpectedVersion: 0,
		Definition:      defJSON,
	}, in.DraftID)
	if err != nil {
		return contract.Payload{}, err
	}
	return completed(stagedResponsibilityBody{Draft: draft, Resource: resource})
}

// responsibilityUpdate stages a new version of an existing responsibility.
func (s *Service) responsibilityUpdate(ctx context.Context, unit contract.Unit, in responsibilityUpdateInput) (contract.Payload, error) {
	row, found, err := loadResponsibility(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("responsibility %s is unknown in this installation", in.ID)
	}
	if row.Archived {
		return contract.Payload{}, conflict("responsibility %s is archived and cannot be updated", in.ID)
	}
	if row.Version != in.ExpectedVersion {
		return contract.Payload{}, staleVersion(
			"responsibility %s is at version %d, not the expected %d", in.ID, row.Version, in.ExpectedVersion)
	}
	if err := requireMatchingInstallation(unit, in.Definition.Scope); err != nil {
		return contract.Payload{}, err
	}
	if problems := responsibilityDefinitionProblems(in.Definition); len(problems) > 0 {
		return contract.Payload{}, invalidInput("responsibility definition rejected: %s", problems[0])
	}
	resource := responsibilityResource(in.ID, row.Version+1, in.Definition)
	defJSON, err := json.Marshal(resource)
	if err != nil {
		return contract.Payload{}, fmt.Errorf("scheduling: encode responsibility definition: %w", err)
	}
	draft, err := s.stageChange(ctx, unit, in.Scope, wireChange{
		Kind:            kindResponsibility,
		Action:          changeUpdate,
		ID:              in.ID,
		ExpectedVersion: int64(in.ExpectedVersion),
		Definition:      defJSON,
	}, in.DraftID)
	if err != nil {
		return contract.Payload{}, err
	}
	return completed(stagedResponsibilityBody{Draft: draft, Resource: resource})
}

// responsibilityArchive stages the archive of an existing responsibility,
// carrying the current definition so recorded cycles stay inspectable.
func (s *Service) responsibilityArchive(ctx context.Context, unit contract.Unit, in responsibilityArchiveInput) (contract.Payload, error) {
	row, found, err := loadResponsibility(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("responsibility %s is unknown in this installation", in.ID)
	}
	if row.Version != in.ExpectedVersion {
		return contract.Payload{}, staleVersion(
			"responsibility %s is at version %d, not the expected %d", in.ID, row.Version, in.ExpectedVersion)
	}
	resource, err := row.wire()
	if err != nil {
		return contract.Payload{}, err
	}
	resource.Version = row.Version + 1
	defJSON, err := json.Marshal(resource)
	if err != nil {
		return contract.Payload{}, fmt.Errorf("scheduling: encode responsibility definition: %w", err)
	}
	draft, err := s.stageChange(ctx, unit, in.Scope, wireChange{
		Kind:            kindResponsibility,
		Action:          changeArchive,
		ID:              in.ID,
		ExpectedVersion: int64(in.ExpectedVersion),
		Definition:      defJSON,
	}, in.DraftID)
	if err != nil {
		return contract.Payload{}, err
	}
	return completed(stagedResponsibilityBody{Draft: draft, Resource: resource})
}

// responsibilityGet resolves one responsibility by identity under the
// request scope.
func (s *Service) responsibilityGet(ctx context.Context, unit contract.Unit, in responsibilityGetInput) (contract.Payload, error) {
	if err := requireMatchingInstallation(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	row, found, err := loadResponsibility(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("responsibility %s is unknown in this installation", in.ID)
	}
	if !scopeContains(in.Scope, row.Scope) {
		return contract.Payload{}, permissionDenied("responsibility %s is outside the request scope", in.ID)
	}
	wire, err := row.wire()
	if err != nil {
		return contract.Payload{}, err
	}
	return completed(responsibilityBody{Resource: wire})
}

// responsibilityList pages responsibilities; the worker_id and
// organization_id filters apply to the resource.
func (s *Service) responsibilityList(ctx context.Context, unit contract.Unit, in listInput) (contract.Payload, error) {
	if err := requireMatchingInstallation(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	if unsupportedFilters(in.Filter, map[string]bool{"worker_id": true, "organization_id": true}) {
		return contract.Payload{}, invalidInput(
			"responsibility.list supports only the worker_id and organization_id filters")
	}
	worker, org := "", ""
	if in.Filter != nil {
		if in.Filter.WorkerID != nil {
			worker = string(*in.Filter.WorkerID)
		}
		if in.Filter.OrganizationID != nil {
			org = string(*in.Filter.OrganizationID)
		}
	}
	limit := defaultListLimit
	if in.Limit != nil {
		limit = *in.Limit
	}
	keyset, keysetArgs, err := s.keysetOf(opResponsibilityList, unit, in.Filter, in.Cursor)
	if err != nil {
		return contract.Payload{}, err
	}
	conds, args := listWhere(unit)
	if worker != "" {
		conds = append(conds, `worker_id = ?`)
		args = append(args, worker)
	}
	if org != "" {
		conds = append(conds, `organization_id = ?`)
		args = append(args, org)
	}
	if keyset != "" {
		conds = append(conds, keyset)
		args = append(args, keysetArgs...)
	}
	rows, err := listResponsibilities(ctx, unit, conds, args, limit+1)
	if err != nil {
		return contract.Payload{}, err
	}
	rows, next, err := s.pageResponsibilities(opResponsibilityList, unit, in.Filter, limit, rows, s.deps.Clock.Now())
	if err != nil {
		return contract.Payload{}, err
	}
	items := make([]wireResponsibility, 0, len(rows))
	for _, r := range rows {
		w, err := r.wire()
		if err != nil {
			return contract.Payload{}, err
		}
		items = append(items, w)
	}
	return listResult(itemsOut[wireResponsibility]{Items: items}, next)
}

// responsibilityPause disables future cycle admissions immediately. The
// pending wake is left in place so the durable next-wake identity survives;
// a later admission rechecks the paused flag transactionally.
func (s *Service) responsibilityPause(ctx context.Context, unit contract.Unit, in pauseResumeInput) (contract.Payload, error) {
	if err := requireMatchingInstallation(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	row, found, err := loadResponsibility(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("responsibility %s is unknown in this installation", in.ID)
	}
	if !scopeContains(in.Scope, row.Scope) {
		return contract.Payload{}, permissionDenied("responsibility %s is outside the request scope", in.ID)
	}
	if row.Archived {
		return contract.Payload{}, conflict("responsibility %s is archived and cannot be paused", in.ID)
	}
	row.Paused = true
	row.Version++
	row.UpdatedAt = s.deps.Clock.Now()
	if err := updateResponsibilityState(ctx, unit, row); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventResponsibilityPaused, row.ID, row.Version); err != nil {
		return contract.Payload{}, err
	}
	return completed(dispositionBody{Resource: wireDisposition{
		ID: row.ID, Version: row.Version, State: statePaused,
	}})
}

// responsibilityResume re-enables cycle admission. The pending wake survives
// the pause untouched; when none remains (the responsibility was created
// paused) resume arms the next wake one minimum interval from now.
func (s *Service) responsibilityResume(ctx context.Context, unit contract.Unit, in pauseResumeInput) (contract.Payload, error) {
	if err := requireMatchingInstallation(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	row, found, err := loadResponsibility(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("responsibility %s is unknown in this installation", in.ID)
	}
	if !scopeContains(in.Scope, row.Scope) {
		return contract.Payload{}, permissionDenied("responsibility %s is outside the request scope", in.ID)
	}
	if row.Archived {
		return contract.Payload{}, conflict("responsibility %s is archived and cannot be resumed", in.ID)
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
		next := now.Add(time.Duration(row.MinIntervalSeconds) * time.Second)
		row.NextWake = &next
		if err := s.insertSourceWake(ctx, unit, row.Scope, sourceResponsibility, row.ID,
			responsibilityOccurrenceKey(row.ID, row.Version, next), next, row.Version, now); err != nil {
			return contract.Payload{}, err
		}
	}
	if err := updateResponsibilityDefinition(ctx, unit, row); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventResponsibilityResumed, row.ID, row.Version); err != nil {
		return contract.Payload{}, err
	}
	return completed(dispositionBody{Resource: wireDisposition{
		ID: row.ID, Version: row.Version, State: stateActive,
	}})
}
