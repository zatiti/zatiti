package artifacts

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Outgoing owner call. Artifacts is allowlisted only for _execution.job.create
// (registering a durable, inspectable job record); it holds no allowlist
// entry for job.claim/job.pending/job.record, so it never drives a job to
// completion itself. Fulfilling a created job is the controller's local IO
// recovery concern, outside this package's scope. A peer fault is returned
// unchanged so the surrounding transaction rolls back with the peer's own
// code.

// peerExecutionJobCreate is the one outgoing call this package is
// allowlisted for.
const peerExecutionJobCreate = "_execution.job.create"

// peerVersion is the contract version every peer descriptor serves.
const peerVersion int64 = 1

// callPeer invokes one peer operation and decodes its output body.
func callPeer[O any](ctx context.Context, s *Service, unit contract.Unit, op string, input any, output *O) error {
	if s.deps.Ports == nil {
		return prerequisiteMissing("peer operation %s is unavailable without a ports dependency", op)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return internalError("encode %s input failed", op)
	}
	payload, err := s.deps.Ports.Call(ctx, unit, contract.Invocation{Operation: op, Version: peerVersion, Input: raw})
	if err != nil {
		return fmt.Errorf("artifacts: call %s: %w", op, err)
	}
	if payload.Error != nil {
		return payload.Error
	}
	if payload.Status != contract.StatusCompleted {
		return internalError("peer operation %s returned status %q", op, payload.Status)
	}
	if err := contract.DecodeStrict(payload.Data, output); err != nil {
		return internalError("peer operation %s returned malformed data: %v", op, err)
	}
	return nil
}

// executionJobCreateInput mirrors execution's _execution.job.create input.
type executionJobCreateInput struct {
	Scope     wireScope       `json:"scope"`
	Owner     string          `json:"owner"`
	Operation string          `json:"operation"`
	Input     json.RawMessage `json:"input"`
	SourceID  contract.ID     `json:"source_id"`
}

// executionJobCreate registers a durable job for one local IO intent.
func (s *Service) executionJobCreate(ctx context.Context, unit contract.Unit, in executionJobCreateInput) (wireJob, error) {
	var out jobOutput
	if err := callPeer(ctx, s, unit, peerExecutionJobCreate, in, &out); err != nil {
		return wireJob{}, err
	}
	return out.Resource, nil
}
