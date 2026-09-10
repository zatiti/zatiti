package tasks

import (
	"context"
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// listFilter is the structured exact-match filter for task.list. Filter
// values bind as SQL parameters; no filter string ever interpolates.
type listFilter struct {
	State          string
	Key            string
	ParentID       contract.ID
	WorkerID       contract.ID
	TaskID         contract.ID
	OrganizationID contract.ID
	Descendants    bool
	DescendantOf   contract.ID
	NeedsYou       bool
	RootID         contract.ID
	Limit          int
	Offset         int64
}

// handleTaskCreate admits a bounded durable task with its pinned
// outcome, inputs, acceptance and limits.
func handleTaskCreate(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Scope      wireScope `json:"scope"`
		Definition *wireTask `json:"definition"`
	}](s, "task.create", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := checkInputScopeVsUnit(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	// The accountable principal is the caller: direct users pin themselves.
	if in.Definition.OwnerID != unit.Actor().PrincipalID {
		return contract.Payload{}, permissionDenied("task owner_id must be the authenticated principal")
	}
	if in.Definition.ParentID != "" {
		return contract.Payload{}, invalidInput("task.create does not attach a parent; use task.delegate")
	}
	row, err := s.admitTask(ctx, unit, &admissionRequest{Definition: in.Definition})
	if err != nil {
		return contract.Payload{}, err
	}
	wire, err := row.toWire()
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(map[string]any{"resource": wire})
}

// handleTaskDelegate creates a linked child under a narrowed contract.
func handleTaskDelegate(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Scope           wireScope   `json:"scope"`
		ID              contract.ID `json:"id"`
		ExpectedVersion int64       `json:"expected_version"`
		Child           *wireTask   `json:"child"`
	}](s, "task.delegate", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	row, err := getTask(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if row == nil {
		return contract.Payload{}, notFound("task %s does not exist", in.ID)
	}
	if err := s.checkInputScope(unit, in.Scope, row); err != nil {
		return contract.Payload{}, err
	}
	if row.Version != in.ExpectedVersion {
		return contract.Payload{}, staleVersion("task %s is at version %d, not the expected %d",
			row.ID, row.Version, in.ExpectedVersion)
	}
	if isTerminal(row.State) || row.State == stateWaiting || row.State == stateVerifying {
		return contract.Payload{}, conflictFault("task %s in state %s cannot delegate", row.ID, row.State)
	}
	if row.CancellationRequested {
		return contract.Payload{}, conflictFault("task %s has a recorded cancellation intent and cannot delegate", row.ID)
	}
	// The delegating principal must own the parent.
	if row.OwnerID != unit.Actor().PrincipalID {
		return contract.Payload{}, permissionDenied("only the task owner may delegate from task %s", row.ID)
	}
	child, err := s.admitDelegatedChild(ctx, unit, row, in.Child)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := s.emitTaskEvent(ctx, unit, row, "tasks.task.delegated", map[string]any{
		"task_id": row.ID, "child_id": child.ID,
	}); err != nil {
		return contract.Payload{}, err
	}
	wire, err := child.toWire()
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(map[string]any{"resource": wire})
}

// handleTaskAccept records an eligible explicitly manual decision.
func handleTaskAccept(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Scope           wireScope     `json:"scope"`
		ID              contract.ID   `json:"id"`
		ExpectedVersion int64         `json:"expected_version"`
		Decision        string        `json:"decision"`
		EvidenceIDs     []contract.ID `json:"evidence_ids"`
		Reason          string        `json:"reason"`
	}](s, "task.accept", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	row, err := getTask(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if row == nil {
		return contract.Payload{}, notFound("task %s does not exist", in.ID)
	}
	if err := s.checkInputScope(unit, in.Scope, row); err != nil {
		return contract.Payload{}, err
	}
	if row.Version != in.ExpectedVersion {
		return contract.Payload{}, staleVersion("task %s is at version %d, not the expected %d",
			row.ID, row.Version, in.ExpectedVersion)
	}
	row, err = s.manualAcceptance(ctx, unit, row, in.Decision, in.EvidenceIDs, in.Reason)
	if err != nil {
		return contract.Payload{}, err
	}
	wire, err := row.toWire()
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(map[string]any{"resource": wire})
}

// handleTaskAssign performs a versioned worker assignment.
func handleTaskAssign(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Scope           wireScope   `json:"scope"`
		ID              contract.ID `json:"id"`
		ExpectedVersion int64       `json:"expected_version"`
		WorkerID        contract.ID `json:"worker_id"`
	}](s, "task.assign", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	row, err := getTask(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if row == nil {
		return contract.Payload{}, notFound("task %s does not exist", in.ID)
	}
	if err := s.checkInputScope(unit, in.Scope, row); err != nil {
		return contract.Payload{}, err
	}
	if row.Version != in.ExpectedVersion {
		return contract.Payload{}, staleVersion("task %s is at version %d, not the expected %d",
			row.ID, row.Version, in.ExpectedVersion)
	}
	row, err = s.assignWorker(ctx, unit, row, in.WorkerID)
	if err != nil {
		return contract.Payload{}, err
	}
	wire, err := row.toWire()
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(map[string]any{"resource": wire})
}

// handleTaskCancel records cancellation intent and blocks new work.
func handleTaskCancel(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Scope           wireScope   `json:"scope"`
		ID              contract.ID `json:"id"`
		ExpectedVersion int64       `json:"expected_version"`
		Reason          string      `json:"reason"`
	}](s, "task.cancel", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	row, err := getTask(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if row == nil {
		return contract.Payload{}, notFound("task %s does not exist", in.ID)
	}
	if err := s.checkInputScope(unit, in.Scope, row); err != nil {
		return contract.Payload{}, err
	}
	if row.Version != in.ExpectedVersion {
		return contract.Payload{}, staleVersion("task %s is at version %d, not the expected %d",
			row.ID, row.Version, in.ExpectedVersion)
	}
	row, err = s.requestCancellation(ctx, unit, row, in.Reason)
	if err != nil {
		return contract.Payload{}, err
	}
	wire, err := row.toWire()
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(map[string]any{"resource": wire})
}

// handleTaskRetry starts a new run attempt under the unchanged accepted
// contract.
func handleTaskRetry(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Scope           wireScope   `json:"scope"`
		ID              contract.ID `json:"id"`
		ExpectedVersion int64       `json:"expected_version"`
		Reason          string      `json:"reason"`
	}](s, "task.retry", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	row, err := getTask(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if row == nil {
		return contract.Payload{}, notFound("task %s does not exist", in.ID)
	}
	if err := s.checkInputScope(unit, in.Scope, row); err != nil {
		return contract.Payload{}, err
	}
	if row.Version != in.ExpectedVersion {
		return contract.Payload{}, staleVersion("task %s is at version %d, not the expected %d",
			row.ID, row.Version, in.ExpectedVersion)
	}
	row, err = s.retryTask(ctx, unit, row, in.Reason)
	if err != nil {
		return contract.Payload{}, err
	}
	wire, err := row.toWire()
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(map[string]any{"resource": wire})
}

// handleTaskUpdate rewrites pending inputs under a version check. The
// accepted verifier never changes during execution.
func handleTaskUpdate(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Scope           wireScope         `json:"scope"`
		ID              contract.ID       `json:"id"`
		ExpectedVersion int64             `json:"expected_version"`
		Inputs          []wireArtifactRef `json:"inputs"`
		Outcome         json.RawMessage   `json:"outcome,omitempty"`
	}](s, "task.update", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	// The schema types outcome as a string and does not require it, so a
	// present raw member is a JSON string; presence, not the value, decides
	// whether the pinned outcome is rewritten.
	var outcome string
	hasOutcome := len(in.Outcome) > 0
	if hasOutcome {
		if err := json.Unmarshal(in.Outcome, &outcome); err != nil {
			return contract.Payload{}, invalidInput("update outcome decoding failed: %v", err)
		}
	}
	row, err := getTask(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if row == nil {
		return contract.Payload{}, notFound("task %s does not exist", in.ID)
	}
	if err := s.checkInputScope(unit, in.Scope, row); err != nil {
		return contract.Payload{}, err
	}
	if row.Version != in.ExpectedVersion {
		return contract.Payload{}, staleVersion("task %s is at version %d, not the expected %d",
			row.ID, row.Version, in.ExpectedVersion)
	}
	row, err = s.updatePendingContract(ctx, unit, row, in.Inputs, outcome, hasOutcome)
	if err != nil {
		return contract.Payload{}, err
	}
	wire, err := row.toWire()
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(map[string]any{"resource": wire})
}

// handleTaskGet resolves exact identity and version under current
// authorization; cross-scope callers get not_found or permission_denied
// without disclosure.
func handleTaskGet(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Scope wireScope   `json:"scope"`
		ID    contract.ID `json:"id"`
	}](s, "task.get", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	row, err := getTask(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if row == nil {
		return contract.Payload{}, notFound("task %s does not exist", in.ID)
	}
	if err := s.checkInputScope(unit, in.Scope, row); err != nil {
		return contract.Payload{}, err
	}
	wire, err := row.toWire()
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(map[string]any{"resource": wire})
}

// handleTaskDependencies inspects dependency states. Worker claims and
// failed verification are never success: only the durable state is shown.
func handleTaskDependencies(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Scope wireScope   `json:"scope"`
		ID    contract.ID `json:"id"`
	}](s, "task.dependencies", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	row, err := getTask(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if row == nil {
		return contract.Payload{}, notFound("task %s does not exist", in.ID)
	}
	if err := s.checkInputScope(unit, in.Scope, row); err != nil {
		return contract.Payload{}, err
	}
	deps, err := row.decodeDependencies()
	if err != nil {
		return contract.Payload{}, err
	}
	out := make([]*wireTask, 0, len(deps))
	for _, dep := range deps {
		depRow, err := getTask(ctx, unit, dep)
		if err != nil {
			return contract.Payload{}, err
		}
		if depRow == nil {
			return contract.Payload{}, prerequisiteMissing("dependency task %s no longer exists", dep)
		}
		if depRow.InstallationID != row.InstallationID {
			return contract.Payload{}, permissionDenied("dependency task %s is outside the installation scope", dep)
		}
		wire, err := depRow.toWire()
		if err != nil {
			return contract.Payload{}, err
		}
		out = append(out, wire)
	}
	return s.completed(map[string]any{"dependencies": out})
}

// handleTaskList reads an authorized, filtered, paginated snapshot. Scope
// and filter apply before pagination; the cursor binds principal and
// query. Fields without a grounding on tasks resources refuse
// invalid_input per the list contract.
func handleTaskList(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[struct {
		Scope  wireScope `json:"scope"`
		Cursor string    `json:"cursor"`
		Limit  int64     `json:"limit"`
		Filter *struct {
			State          string      `json:"state"`
			Key            string      `json:"key"`
			ParentID       contract.ID `json:"parent_id"`
			WorkerID       contract.ID `json:"worker_id"`
			TaskID         contract.ID `json:"task_id"`
			OrganizationID contract.ID `json:"organization_id"`
			Descendants    bool        `json:"descendants"`
			NeedsYou       bool        `json:"needs_you"`
		} `json:"filter"`
	}](s, "task.list", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := checkInputScopeVsUnit(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	f := listFilter{Limit: limitOf(in.Limit)}
	if in.Filter != nil {
		if in.Filter.Key != "" {
			return contract.Payload{}, invalidInput("filter key is unsupported for tasks resources")
		}
		if in.Filter.NeedsYou {
			return contract.Payload{}, invalidInput("filter needs_you is unsupported for tasks resources")
		}
		switch in.Filter.State {
		case "", stateDraft, stateReady, stateRunning, stateWaiting, stateVerifying,
			stateSucceeded, stateFailed, stateCancelled:
		default:
			return contract.Payload{}, invalidInput("filter state %q is not a task state", in.Filter.State)
		}
		f.State = in.Filter.State
		f.ParentID = in.Filter.ParentID
		f.WorkerID = in.Filter.WorkerID
		f.TaskID = in.Filter.TaskID
		f.OrganizationID = in.Filter.OrganizationID
		f.Descendants = in.Filter.Descendants
	}
	// Descendant expansion: anchor at the given parent or task; the store
	// resolves the whole subtree with a cycle-safe recursive CTE so
	// pagination stays deterministic in SQL.
	if f.Descendants {
		anchor := f.ParentID
		if anchor == "" {
			anchor = f.TaskID
		}
		if anchor == "" {
			return contract.Payload{}, invalidInput("descendants filter requires parent_id or task_id")
		}
		f.DescendantOf = anchor
		f.ParentID = ""
	}

	// Cursor binds principal, operation and exact query fingerprint.
	fingerprint, err := listFingerprint(in.Scope, f)
	if err != nil {
		return contract.Payload{}, err
	}
	offset, err := decodeCursor(unit, "task.list", fingerprint, in.Cursor)
	if err != nil {
		return contract.Payload{}, err
	}
	f.Offset = offset
	rows, err := listTasks(ctx, unit, in.Scope.InstallationID, f)
	if err != nil {
		return contract.Payload{}, err
	}
	items := make([]*wireTask, 0, len(rows))
	for _, row := range rows {
		wire, err := row.toWire()
		if err != nil {
			return contract.Payload{}, err
		}
		items = append(items, wire)
	}
	out := map[string]any{"items": items}
	if len(items) == f.Limit {
		next, err := encodeCursor(unit, "task.list", fingerprint, offset+int64(f.Limit))
		if err != nil {
			return contract.Payload{}, err
		}
		out["next_cursor"] = next
	}
	return s.completed(out)
}

// listFingerprint is the stable query identity bound into cursors.
func listFingerprint(scope wireScope, f listFilter) (string, error) {
	raw, err := json.Marshal(struct {
		Scope    wireScope   `json:"scope"`
		State    string      `json:"state,omitempty"`
		ParentID contract.ID `json:"parent_id,omitempty"`
		WorkerID contract.ID `json:"worker_id,omitempty"`
		TaskID   contract.ID `json:"task_id,omitempty"`
		OrgID    contract.ID `json:"organization_id,omitempty"`
		DescOf   contract.ID `json:"descendant_of,omitempty"`
		Limit    int         `json:"limit"`
	}{
		Scope:    scope,
		State:    f.State,
		ParentID: f.ParentID,
		WorkerID: f.WorkerID,
		TaskID:   f.TaskID,
		OrgID:    f.OrganizationID,
		DescOf:   f.DescendantOf,
		Limit:    f.Limit,
	})
	if err != nil {
		return "", internalError("list fingerprint encoding failed: %v", err)
	}
	return string(raw), nil
}

// checkInputScopeVsUnit verifies the asserted scope against the
// authenticated unit scope before any lookup, so a foreign installation
// never reaches the store at all.
func checkInputScopeVsUnit(unit contract.Unit, asserted wireScope) error {
	us := unit.Scope()
	if us.InstallationID != "" && asserted.InstallationID != us.InstallationID {
		return permissionDenied("requested scope installation %s does not match the authenticated installation", asserted.InstallationID)
	}
	if asserted.InstallationID == "" {
		return invalidInput("scope installation_id is required")
	}
	if us.OrganizationID != "" && asserted.OrganizationID != "" && asserted.OrganizationID != us.OrganizationID {
		return permissionDenied("requested scope organization does not match the authenticated scope")
	}
	return nil
}
