package scheduling

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Internal compiler operations. _scheduling.validate and _scheduling.activate
// are the two ends of the owned-slice protocol configuration drives: validate
// reads the owned slice against current state and never faults on slice
// content (diagnostics carry the problems), activate applies the exact sealed
// slice inside the caller's transaction and faults on anything unappliable.

// Input aliases: validate and activate both take the shared Candidate
// wrapper configuration sends to every owner.
type (
	validateInput = candidateEnvelope
	activateInput = candidateEnvelope
)

// reqWorkerBinding is the requirement code validate reports when a definition
// names a worker no scope-level worker or active worker binding covers.
const reqWorkerBinding = "worker_binding_required"

// diag returns an error-severity diagnostic; warnDiag an advisory one.
func diag(path, code, message string) wireDiagnostic {
	return wireDiagnostic{Path: path, Code: code, Message: message, Severity: "error"}
}

func warnDiag(path, code, message string) wireDiagnostic {
	return wireDiagnostic{Path: path, Code: code, Message: message, Severity: "warning"}
}

// validationOut accumulates one validate run: diagnostics, prerequisites and
// versioned dependency identities.
type validationOut struct {
	out  wireValidation
	deps map[wireRef]bool
}

func newValidationOut() *validationOut {
	return &validationOut{
		out: wireValidation{
			Diagnostics:  []wireDiagnostic{},
			Requirements: []wireRequirement{},
			Dependencies: []wireRef{},
		},
		deps: make(map[wireRef]bool),
	}
}

func (v *validationOut) diag(path, code, message string) {
	v.out.Diagnostics = append(v.out.Diagnostics, diag(path, code, message))
}

func (v *validationOut) warn(path, code, message string) {
	v.out.Diagnostics = append(v.out.Diagnostics, warnDiag(path, code, message))
}

// validateChangeIdentity decodes one owned change's definition strictly for
// its kind and checks that it carries the change's identity.
func validateChangeIdentity(c wireChange) (*wireSchedule, *wireResponsibility, *wireDiagnostic) {
	problem := func(path, code, message string) *wireDiagnostic {
		d := diag(path, code, message)
		return &d
	}
	switch c.Kind {
	case kindSchedule:
		var def wireSchedule
		if err := contract.DecodeStrict(c.Definition, &def); err != nil {
			return nil, nil, problem("definition", "decode", "schedule definition is not a decodable Schedule: "+err.Error())
		}
		if def.ID != c.ID {
			return nil, nil, problem("definition.id", "identity_mismatch", "definition identity does not match the change")
		}
		return &def, nil, nil
	case kindResponsibility:
		var def wireResponsibility
		if err := contract.DecodeStrict(c.Definition, &def); err != nil {
			return nil, nil, problem("definition", "decode", "responsibility definition is not a decodable Responsibility: "+err.Error())
		}
		if def.ID != c.ID {
			return nil, nil, problem("definition.id", "identity_mismatch", "definition identity does not match the change")
		}
		return nil, &def, nil
	}
	return nil, nil, problem("definition", "decode", "unknown owned kind "+c.Kind)
}

// validateExistingChange mirrors configuration's owned-change fencing: the
// target must exist and sit at the expected version; update definitions
// carry the post-apply version.
func (s *Service) validateExistingChange(ctx context.Context, unit contract.Unit, path string, c wireChange, resource string, lookup func() (int64, bool, error), v *validationOut) {
	version, found, err := lookup()
	if err != nil {
		v.diag(path, "storage", err.Error())
		return
	}
	if !found {
		v.diag(path+".id", "unknown_reference", resource+" "+string(c.ID)+" does not exist")
		return
	}
	if c.ExpectedVersion != version {
		v.diag(path+".expected_version", "stale_version",
			resource+" "+string(c.ID)+" is at version "+strconv.FormatInt(version, 10))
		return
	}
	if c.Action == changeUpdate {
		var def struct {
			Version int64 `json:"version"`
		}
		if err := json.Unmarshal(c.Definition, &def); err != nil || def.Version != c.ExpectedVersion+1 {
			v.diag(path+".definition.version", "version",
				resource+" update definition must carry the post-apply version "+
					strconv.FormatInt(c.ExpectedVersion+1, 10))
		}
	}
	if c.Action == changeArchive {
		v.warn(path, "restrictive_side_effect",
			"archive disables future admissions when applied; definitions and recorded history are retained")
	}
}

// resolveWorkerDependency records the versioned identity of the configuration
// object that binds one worker to the definition's scope: the scope-level
// worker when the scope names it, else the active worker binding whose target
// it is. When nothing binds the worker, validate reports a requirement the
// apply flow must satisfy before activation.
func (s *Service) resolveWorkerDependency(ctx context.Context, unit contract.Unit, path string, workerID contract.ID, scope contract.Scope, v *validationOut) {
	if workerID == "" {
		return
	}
	snap, err := s.callSnapshot(ctx, unit, scope)
	if err != nil {
		v.diag(path+".definition", "storage", err.Error())
		return
	}
	if snap.Worker != nil && snap.Worker.ID == workerID {
		v.deps[wireRef{ID: workerID, Version: snap.Worker.Version}] = true
		return
	}
	for _, b := range snap.Bindings {
		if b.Kind == "worker" && b.TargetID == workerID {
			v.deps[wireRef{ID: b.ID, Version: b.Version}] = true
			return
		}
	}
	v.out.Requirements = append(v.out.Requirements, wireRequirement{
		Code: reqWorkerBinding,
		Message: "scheduling definition " + string(path) + " requires a current worker binding for worker " +
			string(workerID) + " covering its scope before activation",
		ResourceID: workerID,
	})
}

// validateScheduleChange validates one schedule change against current state.
func (s *Service) validateScheduleChange(ctx context.Context, unit contract.Unit, path string, c wireChange, def wireSchedule, install contract.ID, v *validationOut) {
	if def.Scope.InstallationID != install {
		v.diag(path+".definition.scope", "installation_mismatch",
			"schedule definition is homed in another installation")
		return
	}
	for _, p := range scheduleDefinitionProblems(scheduleDefinitionInput{
		Scope: def.Scope, TaskTemplate: def.TaskTemplate, Timezone: def.Timezone,
		Expression: def.Expression, Misfire: def.Misfire,
		CatchUpSeconds: def.CatchUpSeconds, Paused: def.Paused,
	}) {
		v.diag(path+".definition", "definition", p)
	}
	s.resolveWorkerDependency(ctx, unit, path, def.TaskTemplate.WorkerID, def.Scope, v)
	switch c.Action {
	case changeCreate:
		if c.ExpectedVersion != 0 {
			v.diag(path+".expected_version", "expected_version", "schedule create must carry expected_version 0")
			return
		}
		if def.Version != 1 {
			v.diag(path+".definition.version", "identity_mismatch", "schedule create definition must carry version 1")
			return
		}
		if _, found, err := loadSchedule(ctx, unit, def.ID); err != nil {
			v.diag(path, "storage", err.Error())
		} else if found {
			v.diag(path+".id", "conflict", "schedule "+string(def.ID)+" already exists")
		}
	case changeUpdate:
		s.validateExistingChange(ctx, unit, path, c, "schedule", func() (int64, bool, error) {
			row, found, err := loadSchedule(ctx, unit, def.ID)
			return int64(row.Version), found, err
		}, v)
		s.warnNoOccurrences(path, def.Expression, def.Timezone, v)
	case changeArchive:
		s.validateExistingChange(ctx, unit, path, c, "schedule", func() (int64, bool, error) {
			row, found, err := loadSchedule(ctx, unit, def.ID)
			return int64(row.Version), found, err
		}, v)
	}
}

// warnNoOccurrences reports an advisory when a schedule's expression admits
// no occurrence within the scheduling horizon; the definition is still valid,
// but it would fire nothing.
func (s *Service) warnNoOccurrences(path, expression, timezone string, v *validationOut) {
	expr, err := parseCron(expression)
	if err != nil {
		return // the definition problems diagnostic already covers the parse
	}
	loc, err := loadLocation(timezone)
	if err != nil {
		return
	}
	if _, ok := expr.nextAfter(s.deps.Clock.Now(), loc); !ok {
		v.warn(path+".definition.expression", "no_occurrences",
			"expression admits no occurrence within the scheduling horizon")
	}
}

// validateResponsibilityChange validates one responsibility change against
// current state. The owning worker is a prerequisite: validate resolves it
// the same way a schedule's task template is resolved.
func (s *Service) validateResponsibilityChange(ctx context.Context, unit contract.Unit, path string, c wireChange, def wireResponsibility, install contract.ID, v *validationOut) {
	if def.Scope.InstallationID != install {
		v.diag(path+".definition.scope", "installation_mismatch",
			"responsibility definition is homed in another installation")
		return
	}
	for _, p := range responsibilityDefinitionProblems(responsibilityDefinitionInput{
		Scope: def.Scope, WorkerID: def.WorkerID, Outcome: def.Outcome,
		Signals: def.Signals, Triggers: def.Triggers, ReasoningPolicy: def.ReasoningPolicy,
		MinIntervalSeconds: def.MinIntervalSeconds, CycleLimits: def.CycleLimits,
		AggregateLimits: def.AggregateLimits, PauseConditions: def.PauseConditions,
		EscalationConditions: def.EscalationConditions, Acceptance: def.Acceptance,
		Paused: def.Paused,
	}) {
		v.diag(path+".definition", "definition", p)
	}
	s.resolveWorkerDependency(ctx, unit, path, def.WorkerID, def.Scope, v)
	switch c.Action {
	case changeCreate:
		if c.ExpectedVersion != 0 {
			v.diag(path+".expected_version", "expected_version",
				"responsibility create must carry expected_version 0")
			return
		}
		if def.Version != 1 {
			v.diag(path+".definition.version", "identity_mismatch",
				"responsibility create definition must carry version 1")
			return
		}
		if _, found, err := loadResponsibility(ctx, unit, def.ID); err != nil {
			v.diag(path, "storage", err.Error())
		} else if found {
			v.diag(path+".id", "conflict", "responsibility "+string(def.ID)+" already exists")
		}
	case changeUpdate, changeArchive:
		s.validateExistingChange(ctx, unit, path, c, "responsibility", func() (int64, bool, error) {
			row, found, err := loadResponsibility(ctx, unit, def.ID)
			return int64(row.Version), found, err
		}, v)
	}
}

// validate implements _scheduling.validate: validate only the owned candidate
// slice against the current snapshot, collect dependency identities and
// requirements; no live changes and no network. Slice content problems are
// diagnostics, never faults; only a malformed envelope faults (which strict
// dispatch already rejects).
func (s *Service) validate(ctx context.Context, unit contract.Unit, in validateInput) (contract.Payload, error) {
	v := newValidationOut()
	install := unit.Scope().InstallationID
	seen := make(map[string]bool, len(in.Candidate.Changes))
	for i, c := range in.Candidate.Changes {
		if c.Kind != kindSchedule && c.Kind != kindResponsibility {
			continue // other owners validate their own slices
		}
		dup := c.Kind + "|" + string(c.ID)
		if seen[dup] {
			v.diag("changes["+strconv.Itoa(i)+"]", "duplicate_change",
				c.Kind+" "+string(c.ID)+" is changed more than once in this candidate")
			continue
		}
		seen[dup] = true
		path := "changes[" + strconv.Itoa(i) + "]"
		if c.Action != changeCreate && c.Action != changeUpdate && c.Action != changeArchive {
			v.diag(path+".action", "unsupported_action",
				"scheduling-owned resources support create, update and archive only")
			continue
		}
		scheduleDef, responsibilityDef, problem := validateChangeIdentity(c)
		if problem != nil {
			v.out.Diagnostics = append(v.out.Diagnostics, *problem)
			continue
		}
		switch c.Kind {
		case kindSchedule:
			s.validateScheduleChange(ctx, unit, path, c, *scheduleDef, install, v)
		case kindResponsibility:
			s.validateResponsibilityChange(ctx, unit, path, c, *responsibilityDef, install, v)
		}
	}
	for ref := range v.deps {
		v.out.Dependencies = append(v.out.Dependencies, ref)
	}
	sort.Slice(v.out.Dependencies, func(a, b int) bool {
		if v.out.Dependencies[a].ID != v.out.Dependencies[b].ID {
			return v.out.Dependencies[a].ID < v.out.Dependencies[b].ID
		}
		return v.out.Dependencies[a].Version < v.out.Dependencies[b].Version
	})
	return completed(validationBody{Resource: v.out})
}

// activate implements _scheduling.activate: apply the owned exact sealed
// candidate slice inside the caller's compiler transaction. Everything the
// slice needs must already hold: any unappliable change faults and rolls the
// whole bundle back.
func (s *Service) activate(ctx context.Context, unit contract.Unit, in activateInput) (contract.Payload, error) {
	now := s.deps.Clock.Now()
	versions := []wireRef{}
	for _, c := range in.Candidate.Changes {
		if c.Kind != kindSchedule && c.Kind != kindResponsibility {
			continue
		}
		scheduleDef, responsibilityDef, problem := validateChangeIdentity(c)
		if problem != nil {
			return contract.Payload{}, invalidInput("candidate slice rejected: %s", problem.Message)
		}
		switch c.Kind {
		case kindSchedule:
			out, err := s.activateScheduleChange(ctx, unit, c, *scheduleDef, now)
			if err != nil {
				return contract.Payload{}, err
			}
			versions = append(versions, out)
		case kindResponsibility:
			out, err := s.activateResponsibilityChange(ctx, unit, c, *responsibilityDef, now)
			if err != nil {
				return contract.Payload{}, err
			}
			versions = append(versions, out)
		}
	}
	return completed(versionsBody{Versions: versions})
}

// armScheduleWake arms the first pending wake for a schedule at the first
// cron instant after now, when the schedule is not paused and its expression
// admits an occurrence within the horizon. It returns the wake time, if any.
func (s *Service) armScheduleWake(ctx context.Context, unit contract.Unit, def wireSchedule, version contract.Version, now time.Time) (*time.Time, error) {
	if def.Paused {
		return nil, nil
	}
	expr, err := parseCron(def.Expression)
	if err != nil {
		return nil, invalidInput("schedule %s expression is not a valid cron expression: %v", def.ID, err)
	}
	loc, err := loadLocation(def.Timezone)
	if err != nil {
		return nil, invalidInput("schedule %s timezone is not a known IANA zone", def.ID)
	}
	next, ok := expr.nextAfter(now, loc)
	if !ok {
		return nil, nil
	}
	if err := s.insertSourceWake(ctx, unit, def.Scope, sourceSchedule, def.ID,
		scheduleOccurrenceKey(def.ID, version, next), next, version, now); err != nil {
		return nil, err
	}
	return &next, nil
}

// activateScheduleChange applies one schedule change with optimistic version
// fencing. Update always rebuilds the wake set from the new definition so a
// paused or re-expressed schedule never keeps a stale pending wake.
func (s *Service) activateScheduleChange(ctx context.Context, unit contract.Unit, c wireChange, def wireSchedule, now time.Time) (wireRef, error) {
	if def.Scope.InstallationID != unit.Scope().InstallationID {
		return wireRef{}, invalidInput("schedule %s is homed in another installation", def.ID)
	}
	installation, organization, project, worker, task := scopeDims(def.Scope)
	switch c.Action {
	case changeCreate:
		if c.ExpectedVersion != 0 || def.Version != 1 {
			return wireRef{}, invalidInput("schedule create %s must carry expected_version 0 and definition version 1", def.ID)
		}
		template, err := encodeOwned(def.TaskTemplate, "schedule task template")
		if err != nil {
			return wireRef{}, err
		}
		next, err := s.armScheduleWake(ctx, unit, def, 1, now)
		if err != nil {
			return wireRef{}, err
		}
		row := scheduleRow{
			ID: def.ID, Version: 1,
			InstallationID: installation, OrganizationID: organization, ProjectID: project,
			WorkerID: worker, TaskID: task,
			Scope:        def.Scope,
			TaskTemplate: template,
			Timezone:     def.Timezone, Expression: def.Expression,
			Misfire: def.Misfire, CatchUpSeconds: def.CatchUpSeconds,
			Paused: def.Paused, NextWake: next, Archived: false,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := insertSchedule(ctx, unit, row); err != nil {
			return wireRef{}, err
		}
		if err := emitTransition(ctx, unit, eventScheduleActivated, def.ID, 1); err != nil {
			return wireRef{}, err
		}
		return wireRef{ID: def.ID, Version: 1}, nil
	case changeUpdate:
		row, found, err := loadSchedule(ctx, unit, def.ID)
		if err != nil {
			return wireRef{}, err
		}
		if !found {
			return wireRef{}, notFound("schedule %s is unknown in this installation", def.ID)
		}
		if row.Archived {
			return wireRef{}, conflict("schedule %s is archived and refuses changes", def.ID)
		}
		if int64(row.Version) != c.ExpectedVersion {
			return wireRef{}, staleVersion(
				"schedule %s is at version %d, not the expected %d", def.ID, row.Version, c.ExpectedVersion)
		}
		if int64(def.Version) != c.ExpectedVersion+1 {
			return wireRef{}, invalidInput(
				"schedule update %s definition must carry the post-apply version %d", def.ID, c.ExpectedVersion+1)
		}
		template, err := encodeOwned(def.TaskTemplate, "schedule task template")
		if err != nil {
			return wireRef{}, err
		}
		if err := deletePendingWakes(ctx, unit, def.ID); err != nil {
			return wireRef{}, err
		}
		next, err := s.armScheduleWake(ctx, unit, def, def.Version, now)
		if err != nil {
			return wireRef{}, err
		}
		row.Scope = def.Scope
		row.InstallationID, row.OrganizationID = installation, organization
		row.ProjectID, row.WorkerID, row.TaskID = project, worker, task
		row.TaskTemplate = template
		row.Timezone, row.Expression = def.Timezone, def.Expression
		row.Misfire, row.CatchUpSeconds = def.Misfire, def.CatchUpSeconds
		row.Paused, row.NextWake = def.Paused, next
		row.Version = def.Version
		row.UpdatedAt = now
		if err := updateScheduleDefinition(ctx, unit, row); err != nil {
			return wireRef{}, err
		}
		if err := emitTransition(ctx, unit, eventScheduleActivated, def.ID, contract.Version(row.Version)); err != nil {
			return wireRef{}, err
		}
		return wireRef{ID: def.ID, Version: contract.Version(row.Version)}, nil
	case changeArchive:
		row, found, err := loadSchedule(ctx, unit, def.ID)
		if err != nil {
			return wireRef{}, err
		}
		if !found {
			return wireRef{}, notFound("schedule %s is unknown in this installation", def.ID)
		}
		if int64(row.Version) != c.ExpectedVersion {
			return wireRef{}, staleVersion(
				"schedule %s is at version %d, not the expected %d", def.ID, row.Version, c.ExpectedVersion)
		}
		row.Archived = true
		row.Version = row.Version + 1
		row.UpdatedAt = now
		if err := archiveScheduleRow(ctx, unit, row); err != nil {
			return wireRef{}, err
		}
		if err := emitTransition(ctx, unit, eventScheduleArchived, def.ID, contract.Version(row.Version)); err != nil {
			return wireRef{}, err
		}
		return wireRef{ID: def.ID, Version: contract.Version(row.Version)}, nil
	default:
		return wireRef{}, invalidInput("scheduling-owned resources support create, update and archive only")
	}
}

// armResponsibilityWake arms the first pending wake for a responsibility one
// minimum interval from now, when the responsibility is not paused.
func (s *Service) armResponsibilityWake(ctx context.Context, unit contract.Unit, def wireResponsibility, version contract.Version, now time.Time) (*time.Time, error) {
	if def.Paused {
		return nil, nil
	}
	next := now.Add(time.Duration(def.MinIntervalSeconds) * time.Second)
	if err := s.insertSourceWake(ctx, unit, def.Scope, sourceResponsibility, def.ID,
		responsibilityOccurrenceKey(def.ID, version, next), next, version, now); err != nil {
		return nil, err
	}
	return &next, nil
}

// activateResponsibilityChange applies one responsibility change with
// optimistic version fencing.
func (s *Service) activateResponsibilityChange(ctx context.Context, unit contract.Unit, c wireChange, def wireResponsibility, now time.Time) (wireRef, error) {
	if def.Scope.InstallationID != unit.Scope().InstallationID {
		return wireRef{}, invalidInput("responsibility %s is homed in another installation", def.ID)
	}
	installation, organization, project, _, task := scopeDims(def.Scope)
	switch c.Action {
	case changeCreate:
		if c.ExpectedVersion != 0 || def.Version != 1 {
			return wireRef{}, invalidInput("responsibility create %s must carry expected_version 0 and definition version 1", def.ID)
		}
		next, err := s.armResponsibilityWake(ctx, unit, def, 1, now)
		if err != nil {
			return wireRef{}, err
		}
		acceptance, err := encodeOwned(def.Acceptance, "responsibility acceptance")
		if err != nil {
			return wireRef{}, err
		}
		row := responsibilityRow{
			ID: def.ID, Version: 1,
			InstallationID: installation, OrganizationID: organization, ProjectID: project,
			WorkerID: def.WorkerID, TaskID: task,
			Scope:                def.Scope,
			Outcome:              def.Outcome,
			Signals:              def.Signals,
			Triggers:             def.Triggers,
			ReasoningPolicy:      def.ReasoningPolicy,
			MinIntervalSeconds:   def.MinIntervalSeconds,
			CycleLimits:          def.CycleLimits,
			AggregateLimits:      def.AggregateLimits,
			PauseConditions:      def.PauseConditions,
			EscalationConditions: def.EscalationConditions,
			Acceptance:           acceptance,
			Paused:               def.Paused, NextWake: next, Archived: false,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := insertResponsibility(ctx, unit, row); err != nil {
			return wireRef{}, err
		}
		if err := emitTransition(ctx, unit, eventResponsibilityActivated, def.ID, 1); err != nil {
			return wireRef{}, err
		}
		return wireRef{ID: def.ID, Version: 1}, nil
	case changeUpdate:
		row, found, err := loadResponsibility(ctx, unit, def.ID)
		if err != nil {
			return wireRef{}, err
		}
		if !found {
			return wireRef{}, notFound("responsibility %s is unknown in this installation", def.ID)
		}
		if row.Archived {
			return wireRef{}, conflict("responsibility %s is archived and refuses changes", def.ID)
		}
		if int64(row.Version) != c.ExpectedVersion {
			return wireRef{}, staleVersion(
				"responsibility %s is at version %d, not the expected %d", def.ID, row.Version, c.ExpectedVersion)
		}
		if int64(def.Version) != c.ExpectedVersion+1 {
			return wireRef{}, invalidInput(
				"responsibility update %s definition must carry the post-apply version %d", def.ID, c.ExpectedVersion+1)
		}
		acceptance, err := encodeOwned(def.Acceptance, "responsibility acceptance")
		if err != nil {
			return wireRef{}, err
		}
		if err := deletePendingWakes(ctx, unit, def.ID); err != nil {
			return wireRef{}, err
		}
		next, err := s.armResponsibilityWake(ctx, unit, def, def.Version, now)
		if err != nil {
			return wireRef{}, err
		}
		row.Scope = def.Scope
		row.InstallationID, row.OrganizationID = installation, organization
		row.ProjectID, row.TaskID = project, task
		row.WorkerID = def.WorkerID
		row.Outcome = def.Outcome
		row.Signals, row.Triggers = def.Signals, def.Triggers
		row.ReasoningPolicy = def.ReasoningPolicy
		row.MinIntervalSeconds = def.MinIntervalSeconds
		row.CycleLimits, row.AggregateLimits = def.CycleLimits, def.AggregateLimits
		row.PauseConditions, row.EscalationConditions = def.PauseConditions, def.EscalationConditions
		row.Acceptance = acceptance
		row.Paused, row.NextWake = def.Paused, next
		row.Version = def.Version
		row.UpdatedAt = now
		if err := updateResponsibilityDefinition(ctx, unit, row); err != nil {
			return wireRef{}, err
		}
		if err := emitTransition(ctx, unit, eventResponsibilityActivated, def.ID, contract.Version(row.Version)); err != nil {
			return wireRef{}, err
		}
		return wireRef{ID: def.ID, Version: contract.Version(row.Version)}, nil
	case changeArchive:
		row, found, err := loadResponsibility(ctx, unit, def.ID)
		if err != nil {
			return wireRef{}, err
		}
		if !found {
			return wireRef{}, notFound("responsibility %s is unknown in this installation", def.ID)
		}
		if int64(row.Version) != c.ExpectedVersion {
			return wireRef{}, staleVersion(
				"responsibility %s is at version %d, not the expected %d", def.ID, row.Version, c.ExpectedVersion)
		}
		row.Archived = true
		row.Version = row.Version + 1
		row.UpdatedAt = now
		if err := archiveResponsibilityRow(ctx, unit, row); err != nil {
			return wireRef{}, err
		}
		if err := emitTransition(ctx, unit, eventResponsibilityArchived, def.ID, contract.Version(row.Version)); err != nil {
			return wireRef{}, err
		}
		return wireRef{ID: def.ID, Version: contract.Version(row.Version)}, nil
	default:
		return wireRef{}, invalidInput("scheduling-owned resources support create, update and archive only")
	}
}

// encodeOwned marshals an owned definition assembled in-process. A failure is
// a wrapped error; the value carries no channels or functions, so this is a
// defensive guard rather than an expected path.
func encodeOwned(v any, what string) (json.RawMessage, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("scheduling: encode %s: %w", what, err)
	}
	return raw, nil
}
