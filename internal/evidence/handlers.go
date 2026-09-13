package evidence

import (
	"context"
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// Operation handlers. Every handler runs inside the caller's transaction and
// returns a *contract.Fault on refusal so the transaction rolls back.

const defaultEventListLimit = int64(100)
const maxEventListLimit = int64(500)

// checkInstallation refuses an explicit input scope that names an
// installation other than the caller's own transaction scope.
func (s *Service) checkInstallation(unit contract.Unit, id contract.ID) error {
	if id == "" {
		return invalidInput("scope must name the installation")
	}
	if unit.Scope().InstallationID != id {
		return permissionDenied("scope installation %s does not match the caller's installation %s",
			id, unit.Scope().InstallationID)
	}
	return nil
}

// completedOutcome wraps a typed data body as a completed outcome.
func completedOutcome[O any](data O) (contract.Outcome[O], error) {
	return contract.Outcome[O]{Status: contract.StatusCompleted, Data: data}, nil
}

// isEmptyOrNullJSON reports whether raw is absent or the literal JSON null.
// json.RawMessage's UnmarshalJSON stores the four bytes "null" verbatim
// rather than an empty slice when the wire value is JSON null, so a plain
// len(raw) == 0 check alone misses that case.
func isEmptyOrNullJSON(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
}

// _evidence.command.begin atomically acquires a fresh command identity or
// returns the original disposition of an identical replay. Keys bind
// principal, operation, operation version and submission key; the same
// canonical request digest under that key replays the original disposition,
// a different digest conflicts. A fresh reservation is not durable on its
// own: if the enclosing transaction never reaches finish(), this insert
// rolls back with it and a later attempt reserves again from a clean slate.
func (s *Service) handleCommandBegin(ctx context.Context, unit contract.Unit, in commandBeginInput) (contract.Outcome[commandBeginOutput], error) {
	install := unit.Scope().InstallationID
	if install == "" {
		return contract.Outcome[commandBeginOutput]{}, internalFault("command begin requires an installation scope")
	}
	existing, err := loadCommandByIdentity(ctx, unit, install, in.PrincipalID, in.Operation, in.OperationVersion, in.SubmissionKey)
	if err != nil {
		return contract.Outcome[commandBeginOutput]{}, err
	}
	if existing != nil {
		if !existing.Finished {
			// An in-flight reservation is only ever visible inside its own
			// still-open transaction; command.begin never runs twice for the
			// same identity within one transaction in the real dispatch flow.
			return contract.Outcome[commandBeginOutput]{}, internalFault(
				"command %s identity is already reserved by an in-flight attempt", existing.ID)
		}
		if existing.RequestDigest != in.RequestDigest {
			return contract.Outcome[commandBeginOutput]{}, submissionConflict(
				"submission key %q is already bound to a different input for %s", in.SubmissionKey, in.Operation)
		}
		cmd, perr := projectCommand(existing)
		if perr != nil {
			return contract.Outcome[commandBeginOutput]{}, perr
		}
		return completedOutcome(commandBeginOutput{CommandID: existing.ID, Existing: &cmd})
	}
	id := s.deps.IDs.New()
	row := &commandRow{
		ID:               id,
		InstallationID:   install,
		PrincipalID:      in.PrincipalID,
		Operation:        in.Operation,
		OperationVersion: in.OperationVersion,
		SubmissionKey:    in.SubmissionKey,
		RequestDigest:    in.RequestDigest,
		DataJSON:         "{}",
		CreatedAt:        s.now(),
	}
	if err := insertCommand(ctx, unit, row); err != nil {
		return contract.Outcome[commandBeginOutput]{}, err
	}
	return completedOutcome(commandBeginOutput{CommandID: id})
}

// _evidence.command.finish persists the handler's complete disposition in
// the same transaction as its state changes and events, and emits a
// transition event for the command's own terminal state. It retains the
// exact original result envelope — fault message, details, retryability and
// cursor included — never redacted: only the command's own principal ever
// replays it back through command.get, so this record is exact evidence of
// what was actually decided, not a projection for a broader audience.
func (s *Service) handleCommandFinish(ctx context.Context, unit contract.Unit, in commandFinishInput) (contract.Outcome[commandResourceBody], error) {
	if in.Result.CommandID != in.CommandID {
		return contract.Outcome[commandResourceBody]{}, invalidInput(
			"result command id %s does not match command %s", in.Result.CommandID, in.CommandID)
	}
	if in.Result.Schema != contract.SchemaResult {
		return contract.Outcome[commandResourceBody]{}, invalidInput(
			"result schema %q is not %s", in.Result.Schema, contract.SchemaResult)
	}
	hasError := in.Result.Error != nil
	if hasError != (in.Result.Status == contract.StatusFailed) {
		return contract.Outcome[commandResourceBody]{}, invalidInput(
			"result status %q disagrees with its error presence", in.Result.Status)
	}
	row, err := loadCommandByID(ctx, unit, in.CommandID)
	if err != nil {
		return contract.Outcome[commandResourceBody]{}, err
	}
	if row == nil {
		return contract.Outcome[commandResourceBody]{}, notFound("command %s not found", in.CommandID)
	}
	if row.InstallationID != unit.Scope().InstallationID {
		return contract.Outcome[commandResourceBody]{}, permissionDenied(
			"command %s belongs to another installation", in.CommandID)
	}
	if row.Finished {
		return contract.Outcome[commandResourceBody]{}, internalFault("command %s is already finished", in.CommandID)
	}
	data := in.Result.Data
	if isEmptyOrNullJSON(data) {
		data = json.RawMessage(`{}`)
	}
	errorCode := ""
	if in.Result.Error != nil {
		errorCode = in.Result.Error.Code
	}
	resultJSON, merr := json.Marshal(in.Result)
	if merr != nil {
		return contract.Outcome[commandResourceBody]{}, internalFault("command %s result could not be encoded: %v", in.CommandID, merr)
	}
	now := s.now()
	if err := finishCommand(ctx, unit, row, in.Result.Status, string(data), errorCode, string(resultJSON), now); err != nil {
		return contract.Outcome[commandResourceBody]{}, err
	}
	if err := unit.Emit(ctx, contract.Event{
		Kind:            commandEventKind(in.Result.Status),
		ResourceID:      row.ID,
		ResourceVersion: 1,
	}); err != nil {
		return contract.Outcome[commandResourceBody]{}, internalFault("command %s: emit transition event: %v", in.CommandID, err)
	}
	cmd, perr := projectCommand(row)
	if perr != nil {
		return contract.Outcome[commandResourceBody]{}, perr
	}
	return completedOutcome(commandResourceBody{Resource: cmd})
}

// _evidence.snapshot returns the current event checkpoint visible to scope:
// a consistent read of the highest sequence number, so a caller can bind a
// fresh event.list cursor to it without inferring anything about events
// outside that scope from the position itself.
func (s *Service) handleSnapshot(ctx context.Context, unit contract.Unit, in snapshotInput) (contract.Outcome[snapshotOutput], error) {
	if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[snapshotOutput]{}, err
	}
	seq, err := maxEventSequence(ctx, unit, in.Scope)
	if err != nil {
		return contract.Outcome[snapshotOutput]{}, err
	}
	return completedOutcome(snapshotOutput{LastSequence: seq})
}

// command.get looks up the caller-bound durable disposition after a lost
// acknowledgement. The principal is part of the lookup itself, so a command
// belonging to another principal is indistinguishable from an absent one.
func (s *Service) handleCommandGet(ctx context.Context, unit contract.Unit, in commandGetInput) (contract.Outcome[commandResourceBody], error) {
	if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[commandResourceBody]{}, err
	}
	row, err := loadFinishedCommandByIdentity(ctx, unit, unit.Scope().InstallationID, unit.Actor().PrincipalID,
		in.Operation, in.OperationVersion, in.SubmissionKey)
	if err != nil {
		return contract.Outcome[commandResourceBody]{}, err
	}
	if row == nil {
		return contract.Outcome[commandResourceBody]{}, notFound("command not found")
	}
	cmd, perr := projectCommand(row)
	if perr != nil {
		return contract.Outcome[commandResourceBody]{}, perr
	}
	return completedOutcome(commandResourceBody{Resource: cmd})
}

// event.get reads one authorized, redacted immutable event. An event
// outside the caller's scope is indistinguishable from an absent one.
func (s *Service) handleEventGet(ctx context.Context, unit contract.Unit, in eventGetInput) (contract.Outcome[eventResourceBody], error) {
	if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[eventResourceBody]{}, err
	}
	ev, err := loadEventByID(ctx, unit, in.ID)
	if err != nil {
		return contract.Outcome[eventResourceBody]{}, err
	}
	if ev == nil || !scopeVisible(in.Scope, ev.Scope) {
		return contract.Outcome[eventResourceBody]{}, notFound("event %s not found", in.ID)
	}
	ev.Data = redactJSON(ev.Data)
	return completedOutcome(eventResourceBody{Resource: *ev})
}

// normalizeEventFilter validates and flattens the wire filter. Only
// organization_id, worker_id and task_id map onto an event's own columns;
// every other field is refused as unsupported for this resource.
func normalizeEventFilter(f *eventFilterInput) (eventFilter, error) {
	var out eventFilter
	if f == nil {
		return out, nil
	}
	if f.State != nil {
		return out, invalidInput("filter field %q is not supported for event.list", "state")
	}
	if f.Key != nil {
		return out, invalidInput("filter field %q is not supported for event.list", "key")
	}
	if f.ParentID != nil {
		return out, invalidInput("filter field %q is not supported for event.list", "parent_id")
	}
	if f.Descendants != nil {
		return out, invalidInput("filter field %q is not supported for event.list", "descendants")
	}
	if f.NeedsYou != nil {
		return out, invalidInput("filter field %q is not supported for event.list", "needs_you")
	}
	if f.OrganizationID != nil {
		out.OrganizationID = *f.OrganizationID
	}
	if f.WorkerID != nil {
		out.WorkerID = *f.WorkerID
	}
	if f.TaskID != nil {
		out.TaskID = *f.TaskID
	}
	return out, nil
}

// event.list replays authorized, redacted events after an opaque cursor.
// Filters are structured exact-match fields applied with AND semantics; an
// expired cursor demands a fresh snapshot rather than silently omitting a
// gap.
func (s *Service) handleEventList(ctx context.Context, unit contract.Unit, in eventListInput) (contract.Outcome[eventListOutput], error) {
	if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[eventListOutput]{}, err
	}
	filter, err := normalizeEventFilter(in.Filter)
	if err != nil {
		return contract.Outcome[eventListOutput]{}, err
	}
	limit := defaultEventListLimit
	if in.Limit != nil {
		limit = *in.Limit
		if limit < 1 || limit > maxEventListLimit {
			return contract.Outcome[eventListOutput]{}, invalidInput(
				"limit %d must be between 1 and %d", limit, maxEventListLimit)
		}
	}
	var after int64
	if in.Cursor != nil && *in.Cursor != "" {
		seq, cerr := s.readEventCursor(in.Scope, filter, *in.Cursor)
		if cerr != nil {
			return contract.Outcome[eventListOutput]{}, cerr
		}
		after = seq
	}
	events, hasMore, err := queryEventsPage(ctx, unit, in.Scope, filter, after, limit)
	if err != nil {
		return contract.Outcome[eventListOutput]{}, err
	}
	for i := range events {
		events[i].Data = redactJSON(events[i].Data)
	}
	outcome := contract.Outcome[eventListOutput]{
		Status: contract.StatusCompleted,
		Data:   eventListOutput{Items: events},
	}
	if hasMore && len(events) > 0 {
		last := events[len(events)-1].Sequence
		cursor, cerr := s.mintEventCursor(in.Scope, filter, last, s.now())
		if cerr != nil {
			return contract.Outcome[eventListOutput]{}, cerr
		}
		outcome.NextCursor = &cursor
	}
	return outcome, nil
}
