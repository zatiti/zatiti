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

// resolveExecutionProfile loads the exact immutable hosted model profile.
// It is deliberately separate from the current worker snapshot so an in-flight
// turn can never silently adopt a newer profile version.
func (s *Service) resolveExecutionProfile(ctx context.Context, unit contract.Unit, scope contract.Scope, ref wireRef) (wireExecutionProfile, error) {
	data, err := s.callPeer(ctx, unit, peerConfigProfileResolve, map[string]any{"scope": scope, "profile": ref})
	if err != nil {
		return wireExecutionProfile{}, err
	}
	return decodeResource[wireExecutionProfile]("execution profile", data)
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

func (s *Service) callMessagingHistory(ctx context.Context, unit contract.Unit, scope contract.Scope, conversationID, workerID contract.ID, limit int64) ([]wireMessage, bool, error) {
	data, err := s.callPeer(ctx, unit, peerMessagingHistory, map[string]any{
		"scope": scope, "conversation_id": conversationID, "worker_id": workerID, "limit": limit,
	})
	if err != nil {
		return nil, false, err
	}
	var body struct {
		Items    []wireMessage `json:"items"`
		Complete bool          `json:"complete"`
	}
	if err := contract.DecodeStrict(data, &body); err != nil {
		return nil, false, fmt.Errorf("execution: decode messaging history response: %w", err)
	}
	return body.Items, body.Complete, nil
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

// recordSchedulingCycle records one responsibility-triggered turn's bounded
// cycle_decision outcome through scheduling's own owner-backed ledger.
// cycle_id is the unique replay/conflict fence: an identical result for the
// same cycle_id replays, a differing one is submission_conflict -- scheduling
// enforces that, not execution.
func (s *Service) recordSchedulingCycle(ctx context.Context, unit contract.Unit, responsibilityID contract.ID, expectedVersion contract.Version, nextWake string, outputs []wireArtifactRef, taskIDs []contract.ID, cycleID, turnID contract.ID) error {
	input := map[string]any{
		"responsibility_id": responsibilityID,
		"expected_version":  expectedVersion,
		"next_wake":         nextWake,
		"outputs":           outputs,
		"task_ids":          taskIDs,
		"cycle_id":          cycleID,
	}
	if turnID != "" {
		input["turn_id"] = turnID
	}
	_, err := s.callPeer(ctx, unit, peerSchedulingCycleRecord, input)
	return err
}

// resolveOutputArtifacts resolves named output-slot bindings to published
// attempt artifacts through the artifacts metadata boundary, exactly as
// pinInputVersions resolves task inputs: a binding naming an artifact that
// is not registered, digest-mismatched or not yet available refuses the
// whole call rather than sealing a verification request around a claim
// nothing backs. Bindings with an empty name (outputs beyond the task's
// named required-output slots) are not part of the pinned acceptance
// contract and are skipped -- they stay in the attempt's own output record
// but never become a named VerifierOutputRequirement.
func (s *Service) resolveOutputArtifacts(ctx context.Context, unit contract.Unit, scope contract.Scope, bindings []reportBindingProposal) ([]wireVerifierOutputRequirement, error) {
	named := make([]reportBindingProposal, 0, len(bindings))
	for _, b := range bindings {
		if b.Name != "" {
			named = append(named, b)
		}
	}
	if len(named) == 0 {
		return []wireVerifierOutputRequirement{}, nil
	}
	refs := make([]wireArtifactRef, len(named))
	for i, b := range named {
		refs[i] = b.Artifact
	}
	data, err := s.callPeer(ctx, unit, peerArtifactsMeta, map[string]any{
		"scope":     scope,
		"artifacts": refs,
	})
	if err != nil {
		return nil, err
	}
	var body struct {
		Artifacts []wireArtifact `json:"artifacts"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, prerequisitesDecode(err)
	}
	byDigest := make(map[contract.Digest]wireArtifact, len(body.Artifacts))
	for _, a := range body.Artifacts {
		byDigest[a.Digest] = a
	}
	out := make([]wireVerifierOutputRequirement, 0, len(named))
	for _, b := range named {
		artifact, ok := byDigest[b.Artifact.Digest]
		if !ok || artifact.ID != b.Artifact.ID || artifact.State != "available" {
			return nil, prerequisiteMissing("required output %q names an artifact that is not a published attempt output", b.Name)
		}
		out = append(out, wireVerifierOutputRequirement{
			Name:      b.Name,
			Artifact:  wireArtifactRef{ID: artifact.ID, Digest: artifact.Digest},
			MediaType: artifact.MediaType,
		})
	}
	return out, nil
}

// publishArtifact registers domain metadata for bytes a trusted IO phase
// already staged and blob-published (never new bytes: publishing inside a
// Unit performs no blob IO). Used to promote the verifier's already
// blob-published sealed request into a real, inspectable Artifact before it
// is handed to tasks as evidence.
func (s *Service) publishArtifact(ctx context.Context, unit contract.Unit, scope contract.Scope, digest contract.Digest, size int64, mediaType, classification string, encrypted bool) (wireArtifact, error) {
	data, err := s.callPeer(ctx, unit, peerArtifactsPublish, map[string]any{
		"scope": scope, "digest": digest, "size": size,
		"media_type": mediaType, "classification": classification, "encrypted": encrypted,
	})
	if err != nil {
		return wireArtifact{}, err
	}
	return decodeResource[wireArtifact]("artifacts publish", data)
}

// recordTaskEvidence binds the trusted verifier's own evidence -- the
// published request artifact, the resolved named output bindings and the
// verdict -- to the task before any success transition depends on it. Only
// this recorded lineage, or eligible explicit manual acceptance, can ever
// establish succeeded (_tasks.evidence.record's own contract); this is
// never called with a worker-supplied verdict, only the one this package's
// own trusted verification.record boundary just independently established.
func (s *Service) recordTaskEvidence(ctx context.Context, unit contract.Unit, taskID, attemptID contract.ID, expectedVersion contract.Version, acceptanceDigest contract.Digest, verificationArtifact wireArtifactRef, outputBindings []reportBindingProposal, verdict string) (wireTask, error) {
	if outputBindings == nil {
		outputBindings = []reportBindingProposal{}
	}
	data, err := s.callPeer(ctx, unit, peerTasksEvidenceRecord, map[string]any{
		"task_id": taskID, "attempt_id": attemptID, "expected_version": expectedVersion,
		"acceptance_digest": acceptanceDigest, "verification_artifact": verificationArtifact,
		"output_bindings": outputBindings, "verdict": verdict,
	})
	if err != nil {
		return wireTask{}, err
	}
	return decodeResource[wireTask]("tasks evidence record", data)
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
