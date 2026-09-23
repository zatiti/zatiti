package installation

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Outgoing owner calls. Every peer response type below mirrors only the
// exact fields this package reads off the peer's frozen schema; fields this
// package has no use for (Action's nested Tool/Connection/cost detail,
// Organization/Worker's optional Limits/Extensions/Profile bodies) decode
// into json.RawMessage, which DecodeStrict never recurses into for unknown
// fields -- so the wrapper stays exact against the peer's required set
// without reproducing its entire nested schema.

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
		return fmt.Errorf("installation: call %s: %w", op, err)
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

// ---------- _identity.bootstrap ----------

const peerIdentityBootstrap = "_identity.bootstrap"

type peerPrincipal struct {
	ID      contract.ID `json:"id"`
	Version int64       `json:"version"`
	Kind    string      `json:"kind"`
	Name    string      `json:"name"`
	Scope   wireScope   `json:"scope"`
	Revoked bool        `json:"revoked"`
}

type identityBootstrapInput struct {
	OwnerID        contract.ID `json:"owner_id"`
	CredentialID   contract.ID `json:"credential_id"`
	StoreRef       string      `json:"store_ref"`
	Name           string      `json:"name"`
	InstallationID contract.ID `json:"installation_id"`
}

func (s *Service) identityBootstrap(ctx context.Context, unit contract.Unit, in identityBootstrapInput) (peerPrincipal, error) {
	var out resourceOut[peerPrincipal]
	if err := callPeer(ctx, s, unit, peerIdentityBootstrap, in, &out); err != nil {
		return peerPrincipal{}, err
	}
	return out.Resource, nil
}

// ---------- _configuration.bootstrap ----------

const peerConfigurationBootstrap = "_configuration.bootstrap"

type peerOrganization struct {
	ID        contract.ID      `json:"id"`
	Version   int64            `json:"version"`
	Key       string           `json:"key"`
	Name      string           `json:"name"`
	ChiefID   contract.ID      `json:"chief_id"`
	ParentID  *contract.ID     `json:"parent_id,omitempty"`
	Limits    *json.RawMessage `json:"limits,omitempty"`
	Extension *json.RawMessage `json:"extensions,omitempty"`
}

type peerWorker struct {
	ID             contract.ID     `json:"id"`
	Version        int64           `json:"version"`
	OrganizationID contract.ID     `json:"organization_id"`
	Key            string          `json:"key"`
	Name           string          `json:"name"`
	Purpose        string          `json:"purpose"`
	Instructions   string          `json:"instructions"`
	SkillVersions  []wireRef       `json:"skill_versions"`
	Bindings       []contract.ID   `json:"bindings"`
	Profile        json.RawMessage `json:"profile"`
	Limits         json.RawMessage `json:"limits"`
	Extensions     json.RawMessage `json:"extensions,omitempty"`
}

type configurationBootstrapInput struct {
	InstallationID contract.ID `json:"installation_id"`
	OwnerID        contract.ID `json:"owner_id"`
	OrganizationID contract.ID `json:"organization_id"`
	ChiefID        contract.ID `json:"chief_id"`
}

type configurationBootstrapOutput struct {
	Organization peerOrganization `json:"organization"`
	Chief        peerWorker       `json:"chief"`
}

func (s *Service) configurationBootstrap(ctx context.Context, unit contract.Unit, in configurationBootstrapInput) (configurationBootstrapOutput, error) {
	var out configurationBootstrapOutput
	if err := callPeer(ctx, s, unit, peerConfigurationBootstrap, in, &out); err != nil {
		return configurationBootstrapOutput{}, err
	}
	return out, nil
}

// ---------- _memory.bootstrap ----------

const peerMemoryBootstrap = "_memory.bootstrap"

type peerMemoryBinding struct {
	ID             contract.ID `json:"id"`
	Version        int64       `json:"version"`
	Scope          wireScope   `json:"scope"`
	BrainID        contract.ID `json:"brain_id"`
	Permissions    []string    `json:"permissions"`
	Classification string      `json:"classification"`
}

type memoryBootstrapInput struct {
	InstallationID contract.ID `json:"installation_id"`
	OrganizationID contract.ID `json:"organization_id"`
	ChiefID        contract.ID `json:"chief_id"`
}

type memoryBootstrapOutput struct {
	Bindings []peerMemoryBinding `json:"bindings"`
}

func (s *Service) memoryBootstrap(ctx context.Context, unit contract.Unit, in memoryBootstrapInput) ([]peerMemoryBinding, error) {
	var out memoryBootstrapOutput
	if err := callPeer(ctx, s, unit, peerMemoryBootstrap, in, &out); err != nil {
		return nil, err
	}
	return out.Bindings, nil
}

// ---------- _memory.manifest ----------

const peerMemoryManifest = "_memory.manifest"

type memoryManifestOutput struct {
	BrainRevisions []wireRef         `json:"brain_revisions"`
	Obligations    []wireRequirement `json:"obligations"`
}

func (s *Service) memoryManifest(ctx context.Context, unit contract.Unit, scope wireScope) (memoryManifestOutput, error) {
	var out memoryManifestOutput
	if err := callPeer(ctx, s, unit, peerMemoryManifest, scopeInput{Scope: scope}, &out); err != nil {
		return memoryManifestOutput{}, err
	}
	if out.BrainRevisions == nil {
		out.BrainRevisions = []wireRef{}
	}
	if out.Obligations == nil {
		out.Obligations = []wireRequirement{}
	}
	return out, nil
}

// ---------- _messaging.bootstrap ----------

const peerMessagingBootstrap = "_messaging.bootstrap"

type peerConversation struct {
	ID                  contract.ID   `json:"id"`
	Version             int64         `json:"version"`
	Scope               wireScope     `json:"scope"`
	Kind                string        `json:"kind"`
	ParticipantIDs      []contract.ID `json:"participant_ids"`
	Title               string        `json:"title"`
	Pinned              bool          `json:"pinned"`
	LastMeaningfulEvent *string       `json:"last_meaningful_event,omitempty"`
}

type messagingBootstrapInput struct {
	Scope   wireScope   `json:"scope"`
	OwnerID contract.ID `json:"owner_id"`
	ChiefID contract.ID `json:"chief_id"`
}

func (s *Service) messagingBootstrap(ctx context.Context, unit contract.Unit, in messagingBootstrapInput) (peerConversation, error) {
	var out resourceOut[peerConversation]
	if err := callPeer(ctx, s, unit, peerMessagingBootstrap, in, &out); err != nil {
		return peerConversation{}, err
	}
	return out.Resource, nil
}

// ---------- _effects.pending ----------

const peerEffectsPending = "_effects.pending"

type peerOperation struct {
	ID                contract.ID        `json:"id"`
	Version           int64              `json:"version"`
	Action            json.RawMessage    `json:"action"`
	ActionDigest      string             `json:"action_digest"`
	State             string             `json:"state"`
	AttemptIDs        []contract.ID      `json:"attempt_ids"`
	LinkedOperationID *contract.ID       `json:"linked_operation_id,omitempty"`
	Relationship      *string            `json:"relationship,omitempty"`
	CallbackRoute     *peerCallbackRoute `json:"callback_route,omitempty"`
}

// peerCallbackRoute is the exact CallbackRoute $def shape: which durable
// subsystem an outstanding Operation reports back to. DecodeStrict rejects
// unknown fields even in a nested struct, so every optional field must be
// declared even though installation only reads Kind, to classify a pending
// effect as a stalled worker turn or an unclaimed job for doctor/status.
type peerCallbackRoute struct {
	Kind      string       `json:"kind"`
	TurnID    *contract.ID `json:"turn_id,omitempty"`
	StepIndex *int64       `json:"step_index,omitempty"`
	JobID     *contract.ID `json:"job_id,omitempty"`
}

type effectsPendingInput struct {
	Limit int64 `json:"limit"`
}

type effectsPendingOutput struct {
	Operations []peerOperation `json:"operations"`
}

func (s *Service) effectsPending(ctx context.Context, unit contract.Unit, limit int64) ([]peerOperation, error) {
	var out effectsPendingOutput
	if err := callPeer(ctx, s, unit, peerEffectsPending, effectsPendingInput{Limit: limit}, &out); err != nil {
		return nil, err
	}
	return out.Operations, nil
}

// ---------- _identity.revocations ----------

const peerIdentityRevocations = "_identity.revocations"

// peerRevocation is the exact Revocation $def shape: one credential or
// grant this installation currently holds revoked. Listing it IS the
// assertion that it is revoked; this package never lifts one and never
// learns any secret material from it.
type peerRevocation struct {
	Kind        string           `json:"kind"`
	ID          contract.ID      `json:"id"`
	Version     contract.Version `json:"version"`
	PrincipalID contract.ID      `json:"principal_id"`
}

type identityRevocationsOutput struct {
	Revocations []peerRevocation `json:"revocations"`
}

// identityRevocations reads this installation's current credential and grant
// revocations, so a paused backup's manifest and a restore's recovery
// overlay can carry them across a rewind. Before this peer operation
// existed, revocations were simply absent from both -- the gap
// restore_test.go's TestRestoreOverlayPreservesPostBackupUnknownEffectAcrossRewind
// doc comment named.
func (s *Service) identityRevocations(ctx context.Context, unit contract.Unit, scope wireScope) ([]peerRevocation, error) {
	var out identityRevocationsOutput
	if err := callPeer(ctx, s, unit, peerIdentityRevocations, scopeInput{Scope: scope}, &out); err != nil {
		return nil, err
	}
	return out.Revocations, nil
}

// ---------- _accounting.inspect ----------

const peerAccountingInspect = "_accounting.inspect"

type peerLimits struct {
	Currency        string `json:"currency"`
	SpendMicroUnits int64  `json:"spend_micro_units"`
	Concurrency     int64  `json:"concurrency"`
	ModelSteps      int64  `json:"model_steps"`
	ChildCount      int64  `json:"child_count"`
	DelegationDepth int64  `json:"delegation_depth"`
	AttemptSeconds  int64  `json:"attempt_seconds"`
	RootDeadline    string `json:"root_deadline"`
}

type peerUsage struct {
	Currency  string `json:"currency"`
	Spent     int64  `json:"spent"`
	Reserved  int64  `json:"reserved"`
	Estimated int64  `json:"estimated"`
	Unknown   int64  `json:"unknown"`
	Advisory  bool   `json:"advisory"`
}

type accountingInspectOutput struct {
	Limits peerLimits `json:"limits"`
	Usage  peerUsage  `json:"usage"`
}

func (s *Service) accountingInspect(ctx context.Context, unit contract.Unit, scope wireScope) (accountingInspectOutput, error) {
	var out accountingInspectOutput
	if err := callPeer(ctx, s, unit, peerAccountingInspect, scopeInput{Scope: scope}, &out); err != nil {
		return accountingInspectOutput{}, err
	}
	return out, nil
}

// ---------- _artifacts.metadata ----------

const peerArtifactsMetadata = "_artifacts.metadata"

type peerArtifact struct {
	ID             contract.ID     `json:"id"`
	Version        int64           `json:"version"`
	Scope          wireScope       `json:"scope"`
	Digest         contract.Digest `json:"digest"`
	Size           int64           `json:"size"`
	MediaType      string          `json:"media_type"`
	Classification string          `json:"classification"`
	Encrypted      bool            `json:"encrypted"`
	State          string          `json:"state"`
	CreatedAt      string          `json:"created_at"`
}

type artifactsMetadataInput struct {
	Scope     wireScope         `json:"scope"`
	Artifacts []wireArtifactRef `json:"artifacts"`
}

type artifactsMetadataOutput struct {
	Artifacts []peerArtifact `json:"artifacts"`
}

func (s *Service) artifactsMetadata(ctx context.Context, unit contract.Unit, scope wireScope, refs []wireArtifactRef) ([]peerArtifact, error) {
	var out artifactsMetadataOutput
	if err := callPeer(ctx, s, unit, peerArtifactsMetadata, artifactsMetadataInput{Scope: scope, Artifacts: refs}, &out); err != nil {
		return nil, err
	}
	return out.Artifacts, nil
}

// ---------- _artifacts.publish ----------

const peerArtifactsPublish = "_artifacts.publish"

type artifactsPublishInput struct {
	Scope          wireScope       `json:"scope"`
	Digest         contract.Digest `json:"digest"`
	Size           int64           `json:"size"`
	MediaType      string          `json:"media_type"`
	Classification string          `json:"classification"`
	Encrypted      bool            `json:"encrypted"`
}

// artifactsPublish registers already-published bundle bytes as a restricted,
// encrypted artifact of the installation and returns its reference.
func (s *Service) artifactsPublish(ctx context.Context, unit contract.Unit, scope wireScope, digest contract.Digest, size int64, mediaType string) (wireArtifactRef, error) {
	var out resourceOut[peerArtifact]
	if err := callPeer(ctx, s, unit, peerArtifactsPublish, artifactsPublishInput{
		Scope: scope, Digest: digest, Size: size, MediaType: mediaType, Classification: backupClass, Encrypted: true,
	}, &out); err != nil {
		return wireArtifactRef{}, err
	}
	if out.Resource.ID == "" || out.Resource.Digest != digest {
		return wireArtifactRef{}, internalError("artifacts owner published %s as %s/%s", digest, out.Resource.ID, out.Resource.Digest)
	}
	return wireArtifactRef{ID: out.Resource.ID, Digest: out.Resource.Digest}, nil
}

// ---------- _execution.job.create / _execution.job.record ----------

const (
	peerExecutionJobCreate = "_execution.job.create"
	peerExecutionJobRecord = "_execution.job.record"
)

type executionJobCreateInput struct {
	Scope     wireScope       `json:"scope"`
	Owner     string          `json:"owner"`
	Operation string          `json:"operation"`
	Input     json.RawMessage `json:"input"`
	SourceID  contract.ID     `json:"source_id"`
}

func (s *Service) executionJobCreate(ctx context.Context, unit contract.Unit, in executionJobCreateInput) (wireJob, error) {
	var out resourceOut[wireJob]
	if err := callPeer(ctx, s, unit, peerExecutionJobCreate, in, &out); err != nil {
		return wireJob{}, err
	}
	return out.Resource, nil
}

type executionJobRecordInput struct {
	JobID           contract.ID     `json:"job_id"`
	ExpectedVersion int64           `json:"expected_version"`
	Generation      int64           `json:"generation"`
	State           string          `json:"state"`
	Result          json.RawMessage `json:"result"`
	EvidenceIDs     []contract.ID   `json:"evidence_ids"`
}

func (s *Service) executionJobRecord(ctx context.Context, unit contract.Unit, in executionJobRecordInput) (wireJob, error) {
	if in.Result == nil {
		in.Result = json.RawMessage(`{}`)
	}
	if in.EvidenceIDs == nil {
		in.EvidenceIDs = []contract.ID{}
	}
	var out resourceOut[wireJob]
	if err := callPeer(ctx, s, unit, peerExecutionJobRecord, in, &out); err != nil {
		return wireJob{}, err
	}
	return out.Resource, nil
}

// ---------- _execution.fence ----------

const peerExecutionFence = "_execution.fence"

type executionFenceInput struct {
	Generation int64  `json:"generation"`
	Reason     string `json:"reason"`
}

type executionFenceOutput struct {
	AttemptIDs []contract.ID `json:"attempt_ids"`
}

func (s *Service) executionFence(ctx context.Context, unit contract.Unit, generation int64, reason string) ([]contract.ID, error) {
	var out executionFenceOutput
	if err := callPeer(ctx, s, unit, peerExecutionFence, executionFenceInput{Generation: generation, Reason: reason}, &out); err != nil {
		return nil, err
	}
	if out.AttemptIDs == nil {
		out.AttemptIDs = []contract.ID{}
	}
	return out.AttemptIDs, nil
}
