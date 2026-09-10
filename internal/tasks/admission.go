package tasks

import (
	"context"
	"encoding/json"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Admission pipeline. Every task enters the durable store through one
// fenced path: scope containment, finite envelope, sealed acceptance,
// pinned artifact inputs, worker home validation, budget reservation,
// dependency existence and acyclicity. The state engine then governs every
// later change of state.

// admissionRequest carries the definition for one new task.
type admissionRequest struct {
	Definition    *wireTask
	SourceID      string
	OccurrenceKey string
}

// normalizeDefinition stabilizes the wire definition in place: empty
// slices become present-and-empty so the content digest and stored bytes
// are identical across replays of the same logical input.
func normalizeDefinition(def *wireTask) {
	normalizeAcceptance(&def.Acceptance)
	def.Inputs = nonNilRefs(def.Inputs)
	def.RequiredOutputs = nonNilStrings(def.RequiredOutputs)
	def.Dependencies = nonNilIDs(def.Dependencies)
}

// admitTask runs the full admission fence and persists the new task as
// draft. It runs strictly inside the caller's unit so a later failure
// rolls the reservation, edges and row back together.
func (s *Service) admitTask(ctx context.Context, unit contract.Unit, req *admissionRequest) (*taskRow, error) {
	def := req.Definition
	normalizeDefinition(def)
	now := s.clock.Now().UTC()

	// Initial state: tasks are born draft; readiness is a fenced
	// transition that validates prerequisites.
	if def.State != "" && def.State != stateDraft {
		return nil, invalidInput("new tasks must enter as draft, not %q", def.State)
	}
	if def.Version != 0 && def.Version != 1 {
		return nil, invalidInput("new task version must be 1, not %d", def.Version)
	}

	// Scope containment: the authenticated unit scope must cover the task
	// scope. The installation is mandatory; optional dimensions present on
	// the unit must not be contradicted by the task.
	if err := checkUnitScope(unit, def.Scope); err != nil {
		return nil, err
	}
	if def.Scope.InstallationID == "" {
		def.Scope.InstallationID = unit.Scope().InstallationID
	}
	if def.OwnerID == "" {
		return nil, invalidInput("task owner_id must be pinned")
	}
	if def.WorkerID == "" {
		return nil, invalidInput("task worker_id must be pinned")
	}

	// Finite envelope: explicit currency before spend, deadline in the
	// future, every dimension present.
	if err := validateLimits(def.Limits, now); err != nil {
		return nil, err
	}

	// Acceptance seal: semantic validation plus the canonical digest that
	// fences every later success evaluation.
	digest, err := sealAcceptance(&def.Acceptance)
	if err != nil {
		return nil, err
	}
	if err := bindRequiredOutputs(&def.Acceptance, def.RequiredOutputs); err != nil {
		return nil, err
	}

	// Worker home and bindings: the worker must exist in the current
	// configuration snapshot for the requested scope.
	if err := s.validateWorker(ctx, unit, def.Scope, def.WorkerID); err != nil {
		return nil, err
	}

	// Pinned inputs: every artifact reference must resolve, match its
	// digest, be in scope and be available.
	if _, err := s.validateArtifacts(ctx, unit, def.Scope.toContract(), def.Inputs); err != nil {
		return nil, err
	}

	// Dependencies: referenced tasks must exist and stay inside the
	// installation. Cycles are checked after the edges are recorded.
	for _, dep := range def.Dependencies {
		if dep == "" {
			return nil, invalidInput("dependency ids must be UUIDs")
		}
		existing, err := getTask(ctx, unit, dep)
		if err != nil {
			return nil, err
		}
		if existing == nil {
			return nil, notFound("dependency task %s does not exist", dep)
		}
		if existing.InstallationID != def.Scope.InstallationID {
			return nil, permissionDenied("dependency task %s is outside the installation scope", dep)
		}
	}

	// Identity: honor a pinned id (the wake identity computes it), mint
	// otherwise. The root for accounting is the declared root or self.
	taskID := def.ID
	if taskID == "" {
		taskID = s.ids.New()
	}
	rootID := def.RootID
	if rootID == "" {
		rootID = taskID
	}

	// Budget: precheck the current intersected limits and usage, then
	// reserve atomically inside this transaction. Children share the root
	// budget through root_task_id.
	if err := s.reserveBudget(ctx, unit, def, rootID); err != nil {
		return nil, err
	}

	row := &taskRow{
		ID:                  taskID,
		Version:             1,
		InstallationID:      def.Scope.InstallationID,
		OrganizationID:      def.Scope.OrganizationID,
		ScopeJSON:           string(mustJSON(def.Scope)),
		OwnerID:             def.OwnerID,
		WorkerID:            def.WorkerID,
		ParentID:            def.ParentID,
		RootID:              rootID,
		Outcome:             def.Outcome,
		InputsJSON:          string(mustJSON(nonNilRefs(def.Inputs))),
		RequiredOutputsJSON: string(mustJSON(nonNilStrings(def.RequiredOutputs))),
		AcceptanceJSON:      string(mustJSON(normalizeAcceptance(&def.Acceptance))),
		AcceptanceDigest:    string(digest),
		LimitsJSON:          string(mustJSON(def.Limits)),
		DependenciesJSON:    string(mustJSON(nonNilIDs(def.Dependencies))),
		State:               stateDraft,
		ManualAcceptance:    def.ManualAcceptance,
		SourceID:            req.SourceID,
		OccurrenceKey:       req.OccurrenceKey,
		ContentDigest:       string(contract.Hash(mustJSON(def))),
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := insertTask(ctx, unit, row); err != nil {
		return nil, conflictFault("task identity %s already exists", row.ID)
	}
	for _, dep := range def.Dependencies {
		if err := insertDependency(ctx, unit, row.ID, dep, row.InstallationID, now); err != nil {
			return nil, err
		}
	}
	if err := s.checkDependencyCycle(ctx, unit, row.ID); err != nil {
		return nil, err
	}
	if err := s.emitTaskEvent(ctx, unit, row, "tasks.task.created", map[string]any{
		"task_id": row.ID, "state": row.State, "parent_id": row.ParentID, "root_id": row.RootID,
	}); err != nil {
		return nil, err
	}
	return row, nil
}

// admitDelegatedChild admits a child under a delegating parent: scope and
// limits are narrowed, depth and child count are enforced, the dependency
// edge parent->child is recorded, and the root budgets stay shared. The
// parent must be alive and version-checked by the caller.
func (s *Service) admitDelegatedChild(ctx context.Context, unit contract.Unit, parent *taskRow, childDef *wireTask) (*taskRow, error) {
	parentScope, err := parent.decodeScope()
	if err != nil {
		return nil, err
	}
	if err := narrowScope(parentScope, childDef.Scope); err != nil {
		return nil, err
	}
	if err := checkDepthAndCount(ctx, unit, parent); err != nil {
		return nil, err
	}
	narrowed, err := narrowedChildLimits(parent, childDef.Limits)
	if err != nil {
		return nil, err
	}
	childDef.Limits = narrowed
	childDef.ParentID = parent.ID
	childDef.RootID = parent.RootID
	if childDef.RootID == "" {
		childDef.RootID = parent.ID
	}
	row, err := s.admitTask(ctx, unit, &admissionRequest{Definition: childDef})
	if err != nil {
		return nil, err
	}
	// The parent depends on the child for its own completion criteria.
	if err := insertDependency(ctx, unit, parent.ID, row.ID, row.InstallationID, row.CreatedAt); err != nil {
		return nil, err
	}
	// Every edge insertion ends with a cycle check over the inserting
	// task; a cycle closing through the new edge is refused here.
	if err := s.checkDependencyCycle(ctx, unit, parent.ID); err != nil {
		return nil, err
	}
	return row, nil
}

// checkDependencyCycle verifies that the dependency graph reachable from
// taskID never returns to it.
func (s *Service) checkDependencyCycle(ctx context.Context, unit contract.Unit, taskID contract.ID) error {
	deps, err := dependencyIDs(ctx, unit, taskID)
	if err != nil {
		return err
	}
	for _, dep := range deps {
		reaches, err := dependencyReaches(ctx, unit, dep, taskID)
		if err != nil {
			return err
		}
		if reaches {
			return invalidInput("dependency cycle detected through task %s", dep)
		}
	}
	return nil
}

// validateLimits enforces the finite envelope required before any paid or
// unbounded work: explicit currency, finite spend, future deadline.
func validateLimits(l wireLimits, now time.Time) error {
	if l.SpendMicroUnits < 0 {
		return invalidInput("spend_micro_units must not be negative")
	}
	if l.SpendMicroUnits > 0 && l.Currency == "" {
		return invalidInput("currency must be configured before spend limits")
	}
	if l.Currency != "" && len(l.Currency) != 3 {
		return invalidInput("currency %q must be a three-letter code", l.Currency)
	}
	if l.Concurrency < 1 || l.ModelSteps < 1 || l.AttemptSeconds < 1 {
		return invalidInput("concurrency, model_steps and attempt_seconds must be positive")
	}
	if l.ChildCount < 0 || l.DelegationDepth < 0 {
		return invalidInput("child_count and delegation_depth must not be negative")
	}
	deadline, err := parseDeadline(l.RootDeadline)
	if err != nil {
		return err
	}
	if !deadline.After(now) {
		return invalidInput("root_deadline must be in the future")
	}
	return nil
}

// bindRequiredOutputs requires every declared output name to be covered by
// a passing artifact_presence observation pinning that name. Without the
// binding, output presence could not be fenced independently.
func bindRequiredOutputs(a *wireAcceptance, requiredOutputs []string) error {
	covered := map[string]bool{}
	for _, o := range a.ExpectedObservations {
		if o.Kind == obsArtifactPresence && o.Expected == observationExpectedPass {
			covered[o.ArtifactName] = true
		}
	}
	for _, name := range requiredOutputs {
		if !covered[name] {
			return invalidInput("required output %q is not covered by a passing artifact_presence observation", name)
		}
	}
	return nil
}

// validateWorker resolves the worker home through configuration. The
// worker must exist, resolve to the pinned identity and not be archived.
func (s *Service) validateWorker(ctx context.Context, unit contract.Unit, scope wireScope, workerID contract.ID) error {
	workerScope := scope
	if workerScope.WorkerID == "" {
		workerScope.WorkerID = workerID
	}
	var snap peerScopeSnapshot
	if err := s.callPeer(ctx, unit, "_configuration.snapshot", peerSnapshotIn{Scope: workerScope}, &snap); err != nil {
		return err
	}
	worker := snap.Resource.Worker
	if worker == nil || worker.ID == "" {
		return notFound("worker %s does not exist in the current configuration", workerID)
	}
	if contract.ID(worker.ID) != workerID {
		return notFound("worker %s does not resolve to the pinned identity", workerID)
	}
	if worker.State != "" && worker.State != "active" {
		return permissionDenied("worker %s is not active", workerID)
	}
	return nil
}

// reserveBudget prechecks the intersected envelope and reserves inside the
// caller's transaction. The reservation shares the root budget.
func (s *Service) reserveBudget(ctx context.Context, unit contract.Unit, def *wireTask, rootID contract.ID) error {
	var inspect peerInspectOut
	if err := s.callPeer(ctx, unit, "_accounting.inspect", peerInspectIn{Scope: def.Scope}, &inspect); err != nil {
		return err
	}
	if inspect.Usage.Currency != "" && def.Limits.Currency != "" && inspect.Usage.Currency != def.Limits.Currency {
		return budgetUnavailable("accounting currency %s does not match the task currency %s",
			inspect.Usage.Currency, def.Limits.Currency)
	}
	if def.Limits.SpendMicroUnits > 0 && inspect.Limits.SpendMicroUnits > 0 &&
		inspect.Usage.Reserved+def.Limits.SpendMicroUnits > inspect.Limits.SpendMicroUnits {
		return budgetUnavailable("spend reservation %d would exceed the intersected limit %d",
			inspect.Usage.Reserved+def.Limits.SpendMicroUnits, inspect.Limits.SpendMicroUnits)
	}
	reservationID := s.ids.New()
	var reserved peerReserveOut
	if err := s.callPeer(ctx, unit, "_accounting.reserve", peerReserveIn{
		Scope:       def.Scope,
		RootTaskID:  rootID,
		OperationID: reservationID,
		Amount:      wireMoney{Currency: def.Limits.Currency, MicroUnits: def.Limits.SpendMicroUnits},
		Limits:      def.Limits,
	}, &reserved); err != nil {
		return err
	}
	return nil
}

// checkUnitScope verifies the authenticated unit scope covers the asserted
// task scope: same installation, no contradiction on optional dimensions.
func checkUnitScope(unit contract.Unit, scope wireScope) error {
	us := unit.Scope()
	if us.InstallationID != "" && scope.InstallationID != "" && scope.InstallationID != us.InstallationID {
		return permissionDenied("task scope installation %s does not match the authenticated installation", scope.InstallationID)
	}
	if us.OrganizationID != "" && scope.OrganizationID != "" && scope.OrganizationID != us.OrganizationID {
		return permissionDenied("task scope organization does not match the authenticated scope")
	}
	if us.ProjectID != "" && scope.ProjectID != "" && scope.ProjectID != us.ProjectID {
		return permissionDenied("task scope project does not match the authenticated scope")
	}
	return nil
}

// checkInputScope verifies a public caller's asserted scope against both
// the unit scope and the stored task scope. Mismatches are permission
// faults without cross-scope disclosure.
func (s *Service) checkInputScope(unit contract.Unit, asserted wireScope, row *taskRow) error {
	us := unit.Scope()
	if us.InstallationID != "" && asserted.InstallationID != "" && asserted.InstallationID != us.InstallationID {
		return permissionDenied("requested scope installation %s does not match the authenticated installation", asserted.InstallationID)
	}
	taskScope, err := row.decodeScope()
	if err != nil {
		return err
	}
	if asserted.InstallationID != "" && asserted.InstallationID != taskScope.InstallationID {
		return permissionDenied("task is outside the requested scope")
	}
	if us.InstallationID != "" && taskScope.InstallationID != us.InstallationID {
		return permissionDenied("task is outside the authenticated installation")
	}
	if asserted.OrganizationID != "" && asserted.OrganizationID != taskScope.OrganizationID {
		return permissionDenied("task is outside the requested organization")
	}
	if asserted.ProjectID != "" && asserted.ProjectID != taskScope.ProjectID {
		return permissionDenied("task is outside the requested project")
	}
	if asserted.WorkerID != "" && asserted.WorkerID != taskScope.WorkerID {
		return permissionDenied("task is outside the requested worker scope")
	}
	return nil
}

// normalizeAcceptance returns the acceptance with empty slices normalized
// so the stored contract bytes are stable across re-encodings.
func normalizeAcceptance(a *wireAcceptance) *wireAcceptance {
	if a.SealedInputs == nil {
		a.SealedInputs = []wireArtifactRef{}
	}
	if a.ExpectedObservations == nil {
		a.ExpectedObservations = []wireExpectedObservation{}
	}
	if a.RequiredChildIDs == nil {
		a.RequiredChildIDs = []contract.ID{}
	}
	return a
}

func nonNilRefs(in []wireArtifactRef) []wireArtifactRef {
	if in == nil {
		return []wireArtifactRef{}
	}
	return in
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func nonNilIDs(in []contract.ID) []contract.ID {
	if in == nil {
		return []contract.ID{}
	}
	return in
}

// mustJSON marshals v, panicking only on unreachable encoding failures of
// wire-validated values.
func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic("tasks: wire value encoding failed: " + err.Error())
	}
	return raw
}
