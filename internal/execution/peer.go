package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Peer calls. Execution never guesses peer state: budget arrives through
// accounting, pinned task contracts through tasks, worker and revision
// through configuration. A peer fault is returned unchanged so the
// surrounding transaction rolls back with the peer's own code.

// descriptorVersion (service.go) is the version this module pins on every
// outgoing call.

// callPeer invokes one peer operation and returns its completed payload
// data. Ports are optional at construction: a runtime assembled without
// peers reports prerequisite_missing at call time instead of panicking. A
// payload fault is returned directly; a transport failure wraps the
// underlying error.
func (s *Service) callPeer(ctx context.Context, unit contract.Unit, operation string, input any) (json.RawMessage, error) {
	if s.deps.Ports == nil {
		return nil, prerequisiteMissing(
			"peer operation %s is unavailable in this runtime", operation)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("execution: encode %s input: %w", operation, err)
	}
	payload, err := s.deps.Ports.Call(ctx, unit, contract.Invocation{
		Operation: operation,
		Version:   1,
		Input:     raw,
	})
	if err != nil {
		return nil, err
	}
	if payload.Error != nil {
		return nil, payload.Error
	}
	return payload.Data, nil
}

// decodeResource unwraps a peer body shaped {"resource": ...} into T.
func decodeResource[T any](domain string, data json.RawMessage) (T, error) {
	var body struct {
		Resource T `json:"resource"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		var zero T
		return zero, fmt.Errorf("execution: decode %s response: %w", domain, err)
	}
	return body.Resource, nil
}

// peerScopeSnapshot is the part of configuration's scope snapshot execution
// consumes: the scope-level worker, if any, the configuration revision, and
// the current bindings a worker's own Bindings IDs select into -- the
// authorized tool/connection/memory capability list P15's context builder
// resolves against.
type peerScopeSnapshot struct {
	Scope    contract.Scope   `json:"scope"`
	Revision contract.Version `json:"revision"`
	Worker   *wireWorker      `json:"worker"`
	Bindings []wireBinding    `json:"bindings"`
}

// callScopeSnapshot reads configuration's scope snapshot.
func (s *Service) callScopeSnapshot(ctx context.Context, unit contract.Unit, scope contract.Scope) (peerScopeSnapshot, error) {
	data, err := s.callPeer(ctx, unit, peerConfigSnapshot, map[string]any{"scope": scope})
	if err != nil {
		return peerScopeSnapshot{}, err
	}
	return decodeResource[peerScopeSnapshot]("configuration scope snapshot", data)
}

// peerReservation is the part of an accounting reservation execution pins on
// the attempt.
type peerReservation struct {
	ID      contract.ID      `json:"id"`
	Version contract.Version `json:"version"`
	State   string           `json:"state"`
}

// reserveBudget reserves enforceable cost and concurrency for one attempt.
func (s *Service) reserveBudget(ctx context.Context, unit contract.Unit, scope contract.Scope, rootTaskID contract.ID, operationID contract.ID, amount wireMoney, limits wireLimits) (peerReservation, error) {
	input := map[string]any{
		"scope":        scope,
		"operation_id": operationID,
		"amount":       amount,
		"limits":       limits,
	}
	if rootTaskID != "" {
		input["root_task_id"] = rootTaskID
	}
	data, err := s.callPeer(ctx, unit, peerAccountReserve, input)
	if err != nil {
		return peerReservation{}, err
	}
	return decodeResource[peerReservation]("accounting reservation", data)
}

// settleBudget settles observed usage against one reservation.
func (s *Service) settleBudget(ctx context.Context, unit contract.Unit, reservationID contract.ID, expectedVersion contract.Version, usage wireUsage, authoritativeNonexecution bool) error {
	_, err := s.callPeer(ctx, unit, peerAccountSettle, map[string]any{
		"reservation_id":             reservationID,
		"expected_version":           expectedVersion,
		"usage":                      usage,
		"authoritative_nonexecution": authoritativeNonexecution,
	})
	return err
}

// callTaskSnapshot reads the current pinned contract of one task.
func (s *Service) callTaskSnapshot(ctx context.Context, unit contract.Unit, scope contract.Scope, id contract.ID) (wireTask, error) {
	data, err := s.callPeer(ctx, unit, peerTasksSnapshot, map[string]any{"scope": scope, "id": id})
	if err != nil {
		return wireTask{}, err
	}
	return decodeResource[wireTask]("tasks snapshot", data)
}

// callMessagingPending reads the worker's authorized inbox for
// safe-boundary injection or context assembly, without trusting message
// bodies as grants: the caller renders each returned message as
// user-originated, untrusted transcript content.
func (s *Service) callMessagingPending(ctx context.Context, unit contract.Unit, workerID contract.ID, limit int64) ([]wireMessage, error) {
	data, err := s.callPeer(ctx, unit, peerMessagingPending, map[string]any{
		"worker_id": workerID, "limit": limit,
	})
	if err != nil {
		return nil, err
	}
	var body struct {
		Items []wireMessage `json:"items"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("execution: decode messaging pending response: %w", err)
	}
	return body.Items, nil
}

// callMemorySelect filters the current authorized bindings before any
// retrieval: a binding outside the caller's permission or freshness bound
// refuses here, before any context bytes are built or staged.
func (s *Service) callMemorySelect(ctx context.Context, unit contract.Unit, scope contract.Scope, bindingIDs []contract.ID, permission string, minimumFreshness time.Time) ([]wireMemoryBinding, error) {
	data, err := s.callPeer(ctx, unit, peerMemorySelect, map[string]any{
		"scope": scope, "binding_ids": bindingIDs, "permission": permission,
		"minimum_freshness": formatStamp(minimumFreshness),
	})
	if err != nil {
		return nil, err
	}
	var body struct {
		Bindings []wireMemoryBinding `json:"bindings"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("execution: decode memory select response: %w", err)
	}
	return body.Bindings, nil
}

// callConnectionsResolve validates one exact connection/tool/destination
// triple: lifecycle, freshness, revocation and destination bindings on
// both sides. It never discovers an unknown tool -- the caller must already
// name the exact version it pins; a mismatch refuses with stale_version
// rather than silently dispatching against a guessed identity.
func (s *Service) callConnectionsResolve(ctx context.Context, unit contract.Unit, scope contract.Scope, connection, tool wireRef, destination string) (wireConnection, wireTool, error) {
	data, err := s.callPeer(ctx, unit, peerConnectionsResolve, map[string]any{
		"scope": scope, "connection": connection, "tool": tool, "destination": destination,
	})
	if err != nil {
		return wireConnection{}, wireTool{}, err
	}
	var body struct {
		Connection wireConnection `json:"connection"`
		Tool       wireTool       `json:"tool"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return wireConnection{}, wireTool{}, fmt.Errorf("execution: decode connections resolve response: %w", err)
	}
	return body.Connection, body.Tool, nil
}

// transitionTask moves one task to a new state with evidence.
func (s *Service) transitionTask(ctx context.Context, unit contract.Unit, taskID contract.ID, expectedVersion contract.Version, state string, evidenceIDs []contract.ID, waitingReason string, manual bool) (wireTask, error) {
	input := map[string]any{
		"task_id":          taskID,
		"expected_version": expectedVersion,
		"state":            state,
		"evidence_ids":     evidenceIDs,
	}
	if waitingReason != "" {
		input["waiting_reason"] = waitingReason
	}
	if manual {
		input["manual"] = true
	}
	data, err := s.callPeer(ctx, unit, peerTasksTransit, input)
	if err != nil {
		return wireTask{}, err
	}
	return decodeResource[wireTask]("tasks transition", data)
}
