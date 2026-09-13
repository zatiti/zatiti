package memory

import (
	"context"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Outgoing owner calls actually exercised by this package: policy admission
// for the four disclosure/mutation operations, a configuration snapshot to
// stamp the real configuration_revision an action was authorized under,
// configuration staging for the binding definition lifecycle, artifact
// validation and publication around Serenity evidence, and the execution
// job pair that tracks one governed dispatch from creation to completion.
//
// Three declared outgoing calls have no production path here and are not
// wrapped: `_tasks.snapshot` (this owner authorizes purely from its own
// binding rows, never from task state), `_connections.resolve` (effects
// resolves the connection itself at admission time; memory only ever names
// a tool/connection Ref on the action) and `_effects.admit` (the controller
// drives admission after `_effects.prepare`; this owner never claims a
// second transaction of another owner's state machine). This mirrors the
// effects package's own precedent of leaving declared-but-unused peers
// unwrapped.

// peerVersion is the contract version every peer descriptor serves.
const peerVersion int64 = 1

// callPeer invokes one peer operation and decodes its output body.
func callPeer[I, O any](ctx context.Context, s *Service, unit contract.Unit, op string, input I, output *O) error {
	if s.deps.Ports == nil {
		return prerequisiteMissing("peer operation %s is unavailable without a ports dependency", op)
	}
	raw, err := canonicalJSON(input)
	if err != nil {
		return err
	}
	payload, err := s.deps.Ports.Call(ctx, unit, contract.Invocation{Operation: op, Version: peerVersion, Input: raw})
	if err != nil {
		return fmt.Errorf("memory: call %s: %w", op, err)
	}
	if payload.Error != nil {
		return payload.Error
	}
	if payload.Status != contract.StatusCompleted {
		return &contract.Fault{Code: contract.CodeInternalError,
			Message: fmt.Sprintf("peer operation %s returned status %q", op, payload.Status)}
	}
	if err := contract.DecodeStrict(payload.Data, output); err != nil {
		return &contract.Fault{Code: contract.CodeInternalError,
			Message: fmt.Sprintf("peer operation %s returned malformed data: %v", op, err)}
	}
	return nil
}

// policyCheck evaluates current authorization for one capability and action.
func (s *Service) policyCheck(ctx context.Context, unit contract.Unit, in policyCheckInput) (policyResultBody, error) {
	var out policyResultBody
	if err := callPeer(ctx, s, unit, opPolicyCheck, in, &out); err != nil {
		return policyResultBody{}, err
	}
	return out, nil
}

// configurationSnapshot reads the current configuration revision for a
// scope, stamped onto every action this owner prepares.
func (s *Service) configurationSnapshot(ctx context.Context, unit contract.Unit, in configurationSnapshotInput) (snapshotBody, error) {
	var out snapshotBody
	if err := callPeer(ctx, s, unit, opConfigSnapshot, in, &out); err != nil {
		return snapshotBody{}, err
	}
	return out, nil
}

// configurationStage appends a draft change for later configuration.apply
// activation; it never mutates effective state itself.
func (s *Service) configurationStage(ctx context.Context, unit contract.Unit, in configurationStageInput) (draftResourceBody, error) {
	var out draftResourceBody
	if err := callPeer(ctx, s, unit, opConfigStage, in, &out); err != nil {
		return draftResourceBody{}, err
	}
	return out, nil
}

// artifactsMetadata validates scope, digests, availability and
// classification of pinned artifacts before disclosure or acceptance.
func (s *Service) artifactsMetadata(ctx context.Context, unit contract.Unit, in artifactsMetadataInput) (artifactsMetadataBody, error) {
	var out artifactsMetadataBody
	if err := callPeer(ctx, s, unit, opArtifactsMetadata, in, &out); err != nil {
		return artifactsMetadataBody{}, err
	}
	return out, nil
}

// artifactsPublish publishes metadata for bytes already staged and hashed by
// a trusted local IO phase.
func (s *Service) artifactsPublish(ctx context.Context, unit contract.Unit, in artifactsPublishInput) (artifactResourceBody, error) {
	var out artifactResourceBody
	if err := callPeer(ctx, s, unit, opArtifactsPublish, in, &out); err != nil {
		return artifactResourceBody{}, err
	}
	return out, nil
}

// effectsPrepare persists an immutable action and logical effect; required
// decisions surface as an awaiting_review operation state, never a physical
// call inside this transaction.
func (s *Service) effectsPrepare(ctx context.Context, unit contract.Unit, in effectsPrepareInput) (operationResourceBody, error) {
	var out operationResourceBody
	if err := callPeer(ctx, s, unit, opEffectsPrepare, in, &out); err != nil {
		return operationResourceBody{}, err
	}
	return out, nil
}

// executionJobCreate opens the durable, recoverable job the controller
// drives to invoke the adapter outside this transaction and report back
// through `_memory.record`.
func (s *Service) executionJobCreate(ctx context.Context, unit contract.Unit, in executionJobCreateInput) (jobResourceBody, error) {
	var out jobResourceBody
	if err := callPeer(ctx, s, unit, opExecutionJobCreate, in, &out); err != nil {
		return jobResourceBody{}, err
	}
	return out, nil
}

// executionJobRecord closes out the execution job this owner created, once
// `_memory.record` has finished applying the observation to its own claims,
// promotions and reconciliation obligations.
func (s *Service) executionJobRecord(ctx context.Context, unit contract.Unit, in executionJobRecordInput) (jobResourceBody, error) {
	var out jobResourceBody
	if err := callPeer(ctx, s, unit, opExecutionJobRecord, in, &out); err != nil {
		return jobResourceBody{}, err
	}
	return out, nil
}
