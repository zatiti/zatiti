package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"sort"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// P15: build and publish the actual model context. Prepare resolves every
// pinned configuration/task/history/tool/memory reference inside the
// transaction that admits the plan -- no bytes are read and no provider is
// contacted here. stageContext (perform) runs outside any Unit: it folds
// the plan's private recipe into the exact zatiti.context/v1 transcript,
// validates it against the frozen adapter schema, and stages+publishes the
// bytes through the blob store, mirroring the verifier's own
// stage-then-reference pattern (verifier.go) since neither document goes
// through the artifacts domain's own upload lifecycle. Commit then
// re-validates the plan's pins are still current and records lineage.

const (
	// defaultInboxLimit bounds how many pending inbox messages one context
	// build folds into the transcript.
	defaultInboxLimit = 20
	// defaultResolveVersion is the version execution pins when validating a
	// tool/connection binding it has no other source for the current
	// version of. _connections.resolve rejects a mismatch outright
	// (stale_version) rather than silently accepting a guess, so an
	// incorrect pin fails closed instead of dispatching against the wrong
	// identity. Closing this for good needs either a version carried on
	// Binding/ExecutionProfile or a discovery peer op -- both out of this
	// card's scope; see the P15 handoff notes.
	defaultResolveVersion contract.Version = 1
	// defaultMaxOutputTokens bounds a model step's requested output when no
	// tighter adapter/profile bound is available to execution. The
	// authoritative bound lives on the connections-owned adapter profile
	// (ResponsesProfile.max_output_tokens), which execution has no peer
	// surface to read; this is a conservative placeholder pending that
	// integration.
	defaultMaxOutputTokens = 4096
)

// contextRecipe is execution's own private plan of what belongs in the
// eventual zatiti.context/v1 document, in transcript order. It is never
// part of the wire ContextPlan (whose frozen schema carries only refs and
// byte/token bounds) -- it is the internal recipe stageContext consumes to
// deterministically assemble the document from exactly the data prepare
// already resolved and authorized, so perform makes no fresh authorization
// decision of its own.
type contextRecipe struct {
	Scope            contract.Scope     `json:"scope"`
	AttemptID        contract.ID        `json:"attempt_id"`
	Worker           wireRef            `json:"worker"`
	ExecutionProfile wireRef            `json:"execution_profile"`
	AdapterProfile   json.RawMessage    `json:"adapter_profile,omitempty"`
	SkillVersions    []wireRef          `json:"skill_versions"`
	Components       []contextComponent `json:"components"`
}

// contextComponent is one transcript entry or tool carried into the
// eventual context document. Kind selects which fields apply:
// instruction/task/inbox/tool_result build one ContextMessage each; tool
// builds one ContextToolDefinition.
type contextComponent struct {
	Kind           string              `json:"kind"`
	Role           string              `json:"role,omitempty"`
	Origin         string              `json:"origin,omitempty"`
	Text           string              `json:"text,omitempty"`
	Artifact       *wireArtifactRef    `json:"artifact,omitempty"`
	MediaType      string              `json:"media_type,omitempty"`
	Classification string              `json:"classification,omitempty"`
	MessageID      contract.ID         `json:"message_id,omitempty"`
	SenderID       contract.ID         `json:"sender_id,omitempty"`
	CreatedAt      string              `json:"created_at,omitempty"`
	Attachments    []contextAttachment `json:"attachments,omitempty"`
	ProposalID     string              `json:"proposal_id,omitempty"`
	OperationID    contract.ID         `json:"operation_id,omitempty"`

	// Tool component fields.
	ToolID            contract.ID      `json:"tool_id,omitempty"`
	ToolVersion       contract.Version `json:"tool_version,omitempty"`
	ConnectionID      contract.ID      `json:"connection_id,omitempty"`
	ConnectionVersion contract.Version `json:"connection_version,omitempty"`
	AccountIdentity   string           `json:"account_identity,omitempty"`
	Name              string           `json:"name,omitempty"`
	InputSchema       json.RawMessage  `json:"input_schema,omitempty"`
	OutputSchema      json.RawMessage  `json:"output_schema,omitempty"`
	Effect            string           `json:"effect,omitempty"`
	Destinations      []string         `json:"destinations,omitempty"`
	BindingID         contract.ID      `json:"binding_id,omitempty"`
	Adapter           string           `json:"adapter,omitempty"`
	IsModelTool       bool             `json:"is_model_tool,omitempty"`
}

type contextAttachment struct {
	Artifact       wireArtifactRef `json:"artifact"`
	MediaType      string          `json:"media_type"`
	Classification string          `json:"classification"`
}

// Wire shapes of the zatiti.context/v1 document (ContextArtifact) and the
// zatiti.responses.action/v1 model_step action (ResponsesModelStepParameters),
// mirroring the frozen $defs embedded in context_schema.go exactly.

type wireContextArtifact struct {
	Schema                string               `json:"schema"`
	AttemptID             contract.ID          `json:"attempt_id"`
	Scope                 contract.Scope       `json:"scope"`
	ConfigurationRevision contract.Version     `json:"configuration_revision"`
	Worker                wireRef              `json:"worker"`
	ExecutionProfile      wireRef              `json:"execution_profile"`
	SkillVersions         []wireRef            `json:"skill_versions"`
	Messages              []wireContextMessage `json:"messages"`
	Tools                 []wireContextTool    `json:"tools"`
	SourceArtifacts       []wireArtifactRef    `json:"source_artifacts"`
	Capture               string               `json:"capture"`
	CreatedAt             string               `json:"created_at"`
}

type wireContextMessage struct {
	ID              contract.ID       `json:"id"`
	MessageID       contract.ID       `json:"message_id,omitempty"`
	SenderID        contract.ID       `json:"sender_id,omitempty"`
	CreatedAt       string            `json:"created_at,omitempty"`
	Role            string            `json:"role"`
	Origin          string            `json:"origin"`
	Parts           []json.RawMessage `json:"parts"`
	SourceArtifacts []wireArtifactRef `json:"source_artifacts"`
}

type wireContextTool struct {
	Tool         wireRef         `json:"tool"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
	Effect       string          `json:"effect"`
	Destinations []string        `json:"destinations"`
	BindingID    contract.ID     `json:"binding_id"`
	SchemaDigest contract.Digest `json:"schema_digest"`
}

// wireContextText/wireContextArtifactPart/wireContextToolResult are the
// ContextPart oneOf branches this builder emits; each marshals to exactly
// its branch's closed field set.

type wireContextText struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type wireContextArtifactPart struct {
	Kind           string          `json:"kind"`
	Artifact       wireArtifactRef `json:"artifact"`
	MediaType      string          `json:"media_type"`
	Classification string          `json:"classification"`
}

type wireContextToolResultPart struct {
	Kind        string          `json:"kind"`
	ProposalID  string          `json:"proposal_id"`
	OperationID contract.ID     `json:"operation_id"`
	Status      string          `json:"status"`
	Artifact    wireArtifactRef `json:"artifact"`
}

// wireResponsesModelStep mirrors ResponsesModelStepParameters exactly.
type wireResponsesModelStep struct {
	Schema                string          `json:"schema"`
	Kind                  string          `json:"kind"`
	SessionHandle         string          `json:"session_handle"`
	ContextArtifact       wireArtifactRef `json:"context_artifact"`
	MaxOutputTokens       int64           `json:"max_output_tokens"`
	ToolContractVersions  []wireRef       `json:"tool_contract_versions"`
	ContinuationReference string          `json:"continuation_reference,omitempty"`
}

// wireResponsesPrepareSession mirrors ResponsesPrepareSessionParameters
// exactly: no model-visible content beyond what an empty provider
// conversation creation requires, and no context_artifact.
type wireResponsesPrepareSession struct {
	Schema string `json:"schema"`
	Kind   string `json:"kind"`
}

// handleContextPrepare is the _execution.context.prepare boundary: build a
// versioned immutable context plan naming exact authorized refs and
// byte/token bounds inside the transaction. No IO, no provider call and no
// blob bytes are read here -- refs name pinned artifacts by reference only,
// and every source (configuration, task, inbox, memory, prior turn
// history, tool bindings) is resolved through the same internal peer
// boundary every other handler uses.
func (s *Service) handleContextPrepare(ctx context.Context, unit contract.Unit, in contextPrepareInput) (contract.Outcome[contextPlanBody], error) {
	t, err := loadTurnForUpdate(ctx, unit, in.TurnID, in.ExpectedVersion)
	if err != nil {
		return contract.Outcome[contextPlanBody]{}, err
	}
	if t.InstallationID != installationOf(unit) {
		return contract.Outcome[contextPlanBody]{}, permissionDenied("turn %s belongs to another installation", t.ID)
	}
	if t.State != "claimed" && t.State != "context_pending" {
		return contract.Outcome[contextPlanBody]{}, conflict("turn %s is %s and cannot prepare context", t.ID, t.State)
	}
	if t.Generation != in.Generation {
		return contract.Outcome[contextPlanBody]{}, conflict(
			"turn %s generation %d does not match %d", t.ID, t.Generation, in.Generation)
	}
	now := s.now()

	// Configuration: resolve the current scope snapshot and refuse before
	// any further resolution if it no longer matches what the turn pinned
	// at admission, or if the worker carries no instructions to build a
	// context from at all.
	snapshot, err := s.callScopeSnapshot(ctx, unit, t.Scope)
	if err != nil {
		return contract.Outcome[contextPlanBody]{}, err
	}
	if snapshot.Revision != t.ConfigurationRevision {
		return contract.Outcome[contextPlanBody]{}, staleVersion(
			"turn %s pinned configuration revision %d; the current effective revision is %d",
			t.ID, t.ConfigurationRevision, snapshot.Revision)
	}
	if snapshot.Worker == nil {
		return contract.Outcome[contextPlanBody]{}, prerequisiteMissing(
			"worker %s is not present in the current configuration snapshot", t.WorkerID)
	}
	worker := snapshot.Worker
	if worker.Instructions == "" {
		return contract.Outcome[contextPlanBody]{}, prerequisiteMissing(
			"worker %s carries no instruction bytes to build a context from", worker.ID)
	}
	if worker.Profile == nil {
		return contract.Outcome[contextPlanBody]{}, prerequisiteMissing(
			"worker %s carries no execution profile to dispatch a model step under", worker.ID)
	}
	// Pin the immutable, fully resolved hosted profile now. Pending work must
	// not be reinterpreted with a later worker selection, and dispatch must not
	// infer a provider profile from the legacy display fields.
	if worker.Profile.Executor == "hosted" && worker.Profile.ConnectionVersion > 0 && len(worker.Profile.AdapterProfile) > 0 {
		resolved, err := s.resolveExecutionProfile(ctx, unit, t.Scope, wireRef{ID: worker.Profile.ID, Version: worker.Profile.Version})
		if err != nil {
			return contract.Outcome[contextPlanBody]{}, err
		}
		if len(resolved.AdapterProfile) == 0 || resolved.ConnectionVersion != worker.Profile.ConnectionVersion {
			return contract.Outcome[contextPlanBody]{}, capabilityUnsupported("resolved hosted profile is incomplete or differs from the pinned snapshot")
		}
		worker.Profile = &resolved
	}

	recipe := contextRecipe{
		Scope:            t.Scope,
		AttemptID:        t.AttemptID,
		Worker:           wireRef{ID: worker.ID, Version: worker.Version},
		ExecutionProfile: wireRef{ID: worker.Profile.ID, Version: worker.Profile.Version},
		AdapterProfile:   append(json.RawMessage(nil), worker.Profile.AdapterProfile...),
		SkillVersions:    worker.SkillVersions,
	}
	refs := []wireArtifactRef{}

	recipe.Components = append(recipe.Components, contextComponent{
		Kind: "instruction", Role: "developer", Origin: "effective_instruction", Text: worker.Instructions,
	})

	// Task: the accepted task's outcome description and pinned input
	// artifacts, each validated as currently resolvable before the plan
	// pins it.
	if t.TaskID != "" {
		task, err := s.callTaskSnapshot(ctx, unit, t.Scope, t.TaskID)
		if err != nil {
			return contract.Outcome[contextPlanBody]{}, err
		}
		recipe.Components = append(recipe.Components, contextComponent{
			Kind: "task", Role: "user", Origin: "effective_instruction", Text: task.Outcome,
		})
		if len(task.Inputs) > 0 {
			metas, err := s.callArtifactsMetadata(ctx, unit, t.Scope, task.Inputs)
			if err != nil {
				return contract.Outcome[contextPlanBody]{}, err
			}
			byDigest := make(map[contract.Digest]wireArtifact, len(metas))
			for _, m := range metas {
				byDigest[m.Digest] = m
			}
			for _, ref := range task.Inputs {
				meta, ok := byDigest[ref.Digest]
				if !ok {
					return contract.Outcome[contextPlanBody]{}, prerequisiteMissing(
						"task input artifact %s is not currently resolvable", ref.ID)
				}
				refs = append(refs, ref)
				recipe.Components = append(recipe.Components, contextComponent{
					Kind: "task", Role: "user", Origin: "effective_instruction",
					Artifact: &ref, MediaType: meta.MediaType, Classification: meta.Classification,
				})
			}
		}
	}

	// Chat history is resolved for the exact conversation and current turn
	// worker. Never mix arbitrary pending messages from other conversations
	// into this transcript.
	pending, err := s.callMessagingPending(ctx, unit, t.WorkerID, defaultInboxLimit)
	if err != nil {
		return contract.Outcome[contextPlanBody]{}, err
	}
	var messages []wireMessage
	if t.ConversationID != "" {
		var complete bool
		messages, complete, err = s.callMessagingHistory(ctx, unit, t.Scope, t.ConversationID, t.WorkerID, 200)
		if err != nil {
			return contract.Outcome[contextPlanBody]{}, err
		}
		if !complete {
			return contract.Outcome[contextPlanBody]{}, prerequisiteMissing(
				"authorized conversation history exceeds the 200-message context bound; provider dispatch is refused until history can be compacted safely",
			)
		}
		seen := make(map[contract.ID]bool, len(messages))
		for _, m := range messages {
			seen[m.ID] = true
		}
		for _, m := range pending {
			if m.ConversationID == t.ConversationID && !seen[m.ID] {
				messages = append(messages, m)
				seen[m.ID] = true
			}
		}
	} else if t.Source.Kind == "message" {
		for _, m := range pending {
			if m.ID == t.Source.SourceID {
				messages = append(messages, m)
				break
			}
		}
		if len(messages) == 0 {
			return contract.Outcome[contextPlanBody]{}, prerequisiteMissing(
				"the triggering message is no longer available from the authorized inbox; provider dispatch is refused",
			)
		}
	}
	sort.SliceStable(messages, func(i, j int) bool {
		if messages[i].CreatedAt == messages[j].CreatedAt {
			return messages[i].ID < messages[j].ID
		}
		return messages[i].CreatedAt < messages[j].CreatedAt
	})
	var messageAttachments []wireArtifactRef
	for _, m := range messages {
		messageAttachments = append(messageAttachments, m.Attachments...)
	}
	attachmentMeta := map[contract.Digest]wireArtifact{}
	if len(messageAttachments) > 0 {
		metas, err := s.callArtifactsMetadata(ctx, unit, t.Scope, messageAttachments)
		if err != nil {
			return contract.Outcome[contextPlanBody]{}, err
		}
		for _, meta := range metas {
			attachmentMeta[meta.Digest] = meta
		}
	}
	for _, m := range messages {
		attachments := make([]contextAttachment, 0, len(m.Attachments))
		for _, attachment := range m.Attachments {
			meta, ok := attachmentMeta[attachment.Digest]
			if !ok {
				return contract.Outcome[contextPlanBody]{}, prerequisiteMissing(
					"message attachment %s is not currently resolvable", attachment.ID)
			}
			refs = append(refs, attachment)
			attachments = append(attachments, contextAttachment{
				Artifact: attachment, MediaType: meta.MediaType, Classification: meta.Classification,
			})
		}
		recipe.Components = append(recipe.Components, contextComponent{
			Kind: "inbox", Role: "user", Origin: "user_message", Text: m.Body,
			MessageID: m.ID, SenderID: m.SenderID, CreatedAt: m.CreatedAt, Attachments: attachments,
		})
	}

	// Selected memory: filter the worker's configured memory bindings
	// through the current authorization/freshness bound. An unauthorized
	// or stale binding refuses the entire prepare before any further
	// resolution, let alone a provider dispatch.
	memoryBindingIDs := boundBindingIDs(snapshot.Bindings, worker.Bindings, "memory")
	if len(memoryBindingIDs) > 0 {
		selected, err := s.callMemorySelect(ctx, unit, t.Scope, memoryBindingIDs, "read", time.Time{})
		if err != nil {
			return contract.Outcome[contextPlanBody]{}, err
		}
		if len(selected) > 0 {
			// A selected binding is not itself model context. Do not mark the
			// eventual artifact complete or dispatch a provider request until a
			// durable, cost-bounded memory recall has completed and its exact
			// returned excerpts have been pinned into this recipe. The internal
			// recall/job/artifact-read contract is still an integration seam.
			return contract.Outcome[contextPlanBody]{}, prerequisiteMissing(
				"authorized memory bindings are configured, but completed hosted memory recall is not yet available for model context",
			)
		}
	}

	// Prior outputs / tool results: this turn's own recorded proposal
	// history, each result folded in exactly once -- a later prepare never
	// re-reads or re-derives an earlier step's committed context bytes, it
	// only appends what is new since.
	proposals, err := listProposalsForTurn(ctx, unit, t.ID)
	if err != nil {
		return contract.Outcome[contextPlanBody]{}, err
	}
	for _, p := range proposals {
		if p.ResultArtifact == nil || p.EffectOperationID == "" {
			continue
		}
		refs = append(refs, *p.ResultArtifact)
		recipe.Components = append(recipe.Components, contextComponent{
			Kind: "tool_result", ProposalID: p.ProposalID, OperationID: p.EffectOperationID,
			Artifact: p.ResultArtifact,
		})
	}

	// Tools: only the worker's currently bound, currently resolvable tools
	// are carried -- an empty binding set yields none, and an unresolvable
	// binding is skipped rather than fabricated. The one bound tool whose
	// resolved adapter is "responses" (the pinned hosted model adapter) is
	// also this turn's model-dispatch tool/connection identity.
	toolBindingIDs := boundTargetIDs(snapshot.Bindings, worker.Bindings, "tool")
	for _, targetID := range toolBindingIDs {
		binding := findSelectedBinding(snapshot.Bindings, worker.Bindings, targetID, "tool")
		if binding == nil {
			continue
		}
		destination := worker.Profile.ProviderDestination
		if len(binding.Destinations) > 0 {
			destination = binding.Destinations[0]
		}
		conn, tool, err := s.callConnectionsToolResolve(ctx, unit, t.Scope, binding.TargetID)
		if err != nil {
			var fault *contract.Fault
			if !errors.As(err, &fault) || fault.Code != contract.CodeNotFound {
				continue
			}
			if destination == "" {
				continue
			}
			conn, tool, err = s.callConnectionsResolve(ctx, unit, t.Scope,
				wireRef{ID: worker.Profile.ConnectionID, Version: profileConnectionVersion(worker.Profile)},
				wireRef{ID: binding.TargetID, Version: defaultResolveVersion}, destination)
			if err != nil || tool.Adapter == "mcp" {
				continue
			}
		} else {
			connectionBinding := findSelectedBinding(snapshot.Bindings, worker.Bindings, conn.ID, "connection")
			if tool.Adapter != "mcp" || conn.Provider != "mcp" || tool.ID != binding.TargetID || tool.Version < 1 || conn.Version < 1 || connectionBinding == nil || len(tool.Destinations) != 1 {
				continue
			}
			destination = tool.Destinations[0]
			if !slices.Contains(binding.Destinations, destination) || !slices.Contains(connectionBinding.Destinations, destination) || !slices.Contains(conn.Destinations, destination) {
				continue
			}
		}

		classification := ""
		if tool.Adapter == "mcp" {
			classification = "restricted"
		}
		recipe.Components = append(recipe.Components, contextComponent{
			Classification: classification,
			Kind:           "tool", ToolID: tool.ID, ToolVersion: tool.Version,
			ConnectionID: conn.ID, ConnectionVersion: conn.Version, AccountIdentity: conn.AccountIdentity,
			Name: tool.Name, InputSchema: tool.InputSchema, OutputSchema: tool.OutputSchema,
			Effect: tool.Effect, Destinations: tool.Destinations, BindingID: binding.ID,
			Adapter: tool.Adapter, IsModelTool: tool.Adapter == "responses",
		})
	}

	if t.State != "context_pending" {
		// The first prepare transitions the turn; a rebuild (the turn is
		// already context_pending from an earlier prepare whose commit was
		// discarded as stale) creates a fresh plan without moving the turn
		// again -- nothing about the turn itself changed, only the plan did.
		t.State = "context_pending"
		t.UpdatedAt = now
		if err := updateTurn(ctx, unit, t); err != nil {
			return contract.Outcome[contextPlanBody]{}, err
		}
	}

	plan := &contextPlanRow{
		ID:                    s.newID(),
		InstallationID:        t.InstallationID,
		TurnID:                t.ID,
		ExpectedVersion:       t.Version,
		Generation:            t.Generation,
		Refs:                  refs,
		ConfigurationRevision: t.ConfigurationRevision,
		ByteBound:             defaultContextByteBound,
		TokenBound:            defaultContextTokenBound,
		CreatedAt:             now,
		Recipe:                recipe,
	}
	if err := insertContextPlan(ctx, unit, plan); err != nil {
		return contract.Outcome[contextPlanBody]{}, err
	}
	if err := emitTransition(ctx, unit, eventContextPrepared, plan.ID, 1); err != nil {
		return contract.Outcome[contextPlanBody]{}, err
	}
	out, err := contextPlanOut(plan)
	if err != nil {
		return contract.Outcome[contextPlanBody]{}, fmt.Errorf("execution: encode context plan recipe: %w", err)
	}
	return completedOutcome(contextPlanBody{Resource: out})
}

// callArtifactsMetadata validates scope/digest/availability/classification
// of a batch of pinned artifacts before the plan pins them.
func (s *Service) callArtifactsMetadata(ctx context.Context, unit contract.Unit, scope contract.Scope, refs []wireArtifactRef) ([]wireArtifact, error) {
	data, err := s.callPeer(ctx, unit, peerArtifactsMeta, map[string]any{"scope": scope, "artifacts": refs})
	if err != nil {
		return nil, err
	}
	var body struct {
		Artifacts []wireArtifact `json:"artifacts"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("execution: decode artifacts metadata response: %w", err)
	}
	return body.Artifacts, nil
}

// boundTargetIDs returns the target IDs of the caller's authorized
// bindings matching kind, narrowed to exactly the binding IDs the worker's
// own Bindings selects -- parentage or group membership grants nothing.
func boundTargetIDs(bindings []wireBinding, workerBindingIDs []contract.ID, kind string) []contract.ID {
	authorized := make(map[contract.ID]bool, len(workerBindingIDs))
	for _, id := range workerBindingIDs {
		authorized[id] = true
	}
	var out []contract.ID
	for _, b := range bindings {
		if b.Kind == kind && authorized[b.ID] {
			out = append(out, b.TargetID)
		}
	}
	return out
}

// boundBindingIDs returns the selected grant identities for an owner operation
// that accepts binding IDs. Unlike adapter/tool resolution, memory.select
// authorizes a memory binding, not the brain target named by that binding.
func boundBindingIDs(bindings []wireBinding, workerBindingIDs []contract.ID, kind string) []contract.ID {
	selected := make(map[contract.ID]bool, len(workerBindingIDs))
	for _, id := range workerBindingIDs {
		selected[id] = true
	}
	var out []contract.ID
	for _, binding := range bindings {
		if binding.Kind == kind && selected[binding.ID] {
			out = append(out, binding.ID)
		}
	}
	return out
}

func profileConnectionVersion(profile *wireExecutionProfile) contract.Version {
	if profile != nil && profile.ConnectionVersion > 0 {
		return profile.ConnectionVersion
	}
	return defaultResolveVersion
}

// findBinding locates the authorized binding of kind naming targetID.
func findSelectedBinding(bindings []wireBinding, selected []contract.ID, targetID contract.ID, kind string) *wireBinding {
	for i := range bindings {
		b := &bindings[i]
		if b.Kind == kind && b.TargetID == targetID && slices.Contains(selected, b.ID) {
			return b
		}
	}
	return nil
}

func findBinding(bindings []wireBinding, targetID contract.ID, kind string) *wireBinding {
	for i := range bindings {
		if bindings[i].Kind == kind && bindings[i].TargetID == targetID {
			return &bindings[i]
		}
	}
	return nil
}

// localDecisionTools are the sealed, always-available non-provider decision
// tools (contract.LocalDecisionTool*), never routed through a
// connections.Tool/adapter and never given a provider operation mapping.
// Their IDs are minted deterministically from their sealed name so every
// build of the same turn names the identical stable identity.
func localDecisionTools() []wireContextTool {
	specs := []struct {
		name   string
		schema json.RawMessage
	}{
		{contract.LocalDecisionToolReply, contract.ReplyProposalSchema},
		{contract.LocalDecisionToolClarify, contract.ClarifyProposalSchema},
		{contract.LocalDecisionToolReportOutputs, contract.ReportOutputsProposalSchema},
		{contract.LocalDecisionToolCycleDecision, contract.CycleDecisionProposalSchema},
	}
	out := make([]wireContextTool, 0, len(specs))
	for _, spec := range specs {
		id := uuidFromDigest(sha256Hex([]byte("zatiti.local-decision-tool/" + spec.name)))
		digest := sha256Hex(spec.schema)
		out = append(out, wireContextTool{
			Tool:         wireRef{ID: id, Version: 1},
			Name:         spec.name,
			Description:  "sealed local decision tool: " + spec.name,
			InputSchema:  spec.schema,
			OutputSchema: json.RawMessage(`{}`),
			Effect:       "local",
			Destinations: []string{},
			BindingID:    id,
			SchemaDigest: digest,
		})
	}
	return out
}

// stageContext is the "perform" phase: it runs outside any Unit, folds the
// plan's private recipe into the exact zatiti.context/v1 transcript in
// order (worker instructions, immutable skill closure, accepted task,
// authorized inbox, selected memory, prior outputs, tool results),
// validates it against the frozen adapter schema, and stages+publishes the
// bounded document bytes through the blob store. It returns the published
// locator, the raw document bytes (so a caller can independently confirm
// the intended ordered messages) and the resolved model-dispatch tool
// components callers use to build the eventual dispatch action.
func (s *Service) stageContext(ctx context.Context, t *turnRow, plan *contextPlanRow) (wireArtifactLocator, []byte, error) {
	if s.deps.Blobs == nil {
		return wireArtifactLocator{}, nil, prerequisiteMissing("execution: staging a context requires a blob store")
	}
	refs := make([]contract.ArtifactRef, len(plan.Refs))
	for i, ref := range plan.Refs {
		refs[i] = contract.ArtifactRef{ID: ref.ID, Digest: ref.Digest}
	}
	attemptID := t.AttemptID
	if attemptID == "" {
		attemptID = t.ID
	}
	raw, err := s.buildContextArtifact(ctx, t.Scope, t.ID, attemptID, t.ConfigurationRevision, refs, plan.ByteBound, plan.TokenBound, plan.Recipe)
	if err != nil {
		return wireArtifactLocator{}, nil, err
	}

	digest := sha256Hex(raw)
	stagingRef, stagedDigest, _, err := s.deps.Blobs.Stage(ctx, bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return wireArtifactLocator{}, nil, fmt.Errorf("execution: stage context artifact: %w", err)
	}
	if err := s.deps.Blobs.Publish(ctx, stagingRef, stagedDigest); err != nil {
		return wireArtifactLocator{}, nil, fmt.Errorf("execution: publish context artifact: %w", err)
	}
	locator := wireArtifactLocator{
		Kind:     "artifact",
		Artifact: &wireArtifactRef{ID: uuidFromDigest(digest), Digest: digest},
	}
	return locator, raw, nil
}

// PerformContext implements the controller's trusted outside-Unit seam. The
// recipe is an internal-only snapshot returned by context.prepare; this method
// has no Unit and performs no owner-table or peer reads.
func (s *Service) PerformContext(ctx context.Context, plan contract.ContextPlan) (json.RawMessage, error) {
	if plan.ID == "" || plan.TurnID == "" || plan.Generation < 1 || plan.ConfigurationRevision < 1 || len(plan.Recipe) == 0 {
		return nil, invalidInput("context plan is incomplete")
	}
	var recipe contextRecipe
	if err := contract.DecodeStrict(plan.Recipe, &recipe); err != nil {
		return nil, invalidInput("context plan recipe is malformed: %v", err)
	}
	if recipe.Scope.InstallationID == "" || recipe.Scope.InstallationID != plan.Scope.InstallationID || recipe.Scope != plan.Scope {
		return nil, invalidInput("context plan recipe scope does not match its envelope")
	}
	if recipe.AttemptID != plan.AttemptID {
		return nil, invalidInput("context plan recipe attempt does not match its envelope")
	}
	refs := make([]contract.ArtifactRef, len(plan.Refs))
	for i, ref := range plan.Refs {
		refs[i] = contract.ArtifactRef{ID: ref.ID, Digest: ref.Digest}
	}
	return s.buildContextArtifact(ctx, plan.Scope, plan.TurnID, plan.AttemptID, plan.ConfigurationRevision, refs, plan.ByteBound, plan.TokenBound, recipe)
}

func (s *Service) buildContextArtifact(ctx context.Context, scope contract.Scope, turnID, attemptID contract.ID, configurationRevision contract.Version, refs []contract.ArtifactRef, byteBound, tokenBound int64, recipe contextRecipe) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if attemptID == "" {
		attemptID = turnID
	}
	sourceArtifacts := make([]wireArtifactRef, len(refs))
	for i, ref := range refs {
		sourceArtifacts[i] = wireArtifactRef{ID: ref.ID, Digest: ref.Digest}
	}
	doc := wireContextArtifact{
		Schema: "zatiti.context/v1", AttemptID: attemptID, Scope: scope,
		ConfigurationRevision: configurationRevision, Worker: recipe.Worker,
		ExecutionProfile: recipe.ExecutionProfile, SkillVersions: nonNilRefs(recipe.SkillVersions),
		SourceArtifacts: nonNilArtifactRefs(sourceArtifacts), Capture: "complete", CreatedAt: formatStamp(s.now()),
	}
	for _, c := range recipe.Components {
		msg, ok, err := s.componentMessage(c)
		if err != nil {
			return nil, err
		}
		if ok {
			doc.Messages = append(doc.Messages, msg)
		}
	}
	doc.Messages = nonNilMessages(doc.Messages)
	doc.Tools = localDecisionTools()
	for _, c := range recipe.Components {
		if c.Kind != "tool" {
			continue
		}
		doc.Tools = append(doc.Tools, wireContextTool{
			Tool: wireRef{ID: c.ToolID, Version: c.ToolVersion}, Name: c.Name,
			InputSchema: rawOrEmptyObject(c.InputSchema), OutputSchema: rawOrEmptyObject(c.OutputSchema),
			Effect: c.Effect, Destinations: nonNilStrings(c.Destinations), BindingID: c.BindingID,
			SchemaDigest: sha256Hex(c.InputSchema),
		})
	}
	raw, err := canonicalJSON(doc)
	if err != nil {
		return nil, err
	}
	if byteBound < 1 || int64(len(raw)) > byteBound {
		return nil, capabilityUnsupported("assembled context is %d bytes, exceeding the plan's %d byte bound", len(raw), byteBound)
	}
	// UTF-8 byte count is a conservative upper bound for byte-fallback model
	// tokenizers. Refuse rather than dispatch when the pinned token ceiling is
	// smaller; provider-specific tokenizers can only make this stricter.
	if tokenBound < 1 || int64(len(raw)) > tokenBound {
		return nil, capabilityUnsupported("assembled context is %d bytes, exceeding the plan's conservative %d-token bound", len(raw), tokenBound)
	}
	schema, err := contextArtifactSchema()
	if err != nil {
		return nil, fmt.Errorf("execution: load context artifact schema: %w", err)
	}
	if err := contract.ValidateSchema(schema, raw); err != nil {
		return nil, fmt.Errorf("execution: assembled context does not satisfy zatiti.context/v1: %w", err)
	}
	return raw, nil
}

// componentMessage renders one transcript component as its ContextMessage,
// or ok=false for a component kind (such as "tool") that contributes no
// message of its own.
func (s *Service) componentMessage(c contextComponent) (wireContextMessage, bool, error) {
	switch c.Kind {
	case "instruction":
		part, err := json.Marshal(wireContextText{Kind: "text", Text: c.Text})
		if err != nil {
			return wireContextMessage{}, false, err
		}
		return wireContextMessage{
			ID: s.newID(), Role: c.Role, Origin: c.Origin,
			Parts: []json.RawMessage{part}, SourceArtifacts: []wireArtifactRef{},
		}, true, nil
	case "inbox":
		parts := make([]json.RawMessage, 0, 1+len(c.Attachments))
		textPart, err := json.Marshal(wireContextText{Kind: "text", Text: c.Text})
		if err != nil {
			return wireContextMessage{}, false, err
		}
		parts = append(parts, textPart)
		sourceArtifacts := make([]wireArtifactRef, 0, len(c.Attachments))
		for _, attachment := range c.Attachments {
			part, err := json.Marshal(wireContextArtifactPart{
				Kind: "artifact", Artifact: attachment.Artifact,
				MediaType: attachment.MediaType, Classification: attachment.Classification,
			})
			if err != nil {
				return wireContextMessage{}, false, err
			}
			parts = append(parts, part)
			sourceArtifacts = append(sourceArtifacts, attachment.Artifact)
		}
		id := c.MessageID
		if id == "" {
			id = s.newID()
		}
		return wireContextMessage{
			ID: id, MessageID: c.MessageID, SenderID: c.SenderID, CreatedAt: c.CreatedAt,
			Role: c.Role, Origin: c.Origin, Parts: parts, SourceArtifacts: sourceArtifacts,
		}, true, nil
	case "task":
		var part json.RawMessage
		var err error
		sourceArtifacts := []wireArtifactRef{}
		if c.Artifact != nil {
			part, err = json.Marshal(wireContextArtifactPart{
				Kind: "artifact", Artifact: *c.Artifact, MediaType: c.MediaType, Classification: c.Classification,
			})
			sourceArtifacts = []wireArtifactRef{*c.Artifact}
		} else {
			part, err = json.Marshal(wireContextText{Kind: "text", Text: c.Text})
		}
		if err != nil {
			return wireContextMessage{}, false, err
		}
		return wireContextMessage{
			ID: s.newID(), Role: c.Role, Origin: c.Origin,
			Parts: []json.RawMessage{part}, SourceArtifacts: sourceArtifacts,
		}, true, nil
	case "tool_result":
		if c.Artifact == nil {
			return wireContextMessage{}, false, nil
		}
		part, err := json.Marshal(wireContextToolResultPart{
			Kind: "tool_result", ProposalID: c.ProposalID, OperationID: c.OperationID,
			Status: "completed", Artifact: *c.Artifact,
		})
		if err != nil {
			return wireContextMessage{}, false, err
		}
		return wireContextMessage{
			ID: s.newID(), Role: "tool", Origin: "tool_result",
			Parts: []json.RawMessage{part}, SourceArtifacts: []wireArtifactRef{*c.Artifact},
		}, true, nil
	default:
		return wireContextMessage{}, false, nil
	}
}

type resolvedResponsesProfile struct {
	Schema          string      `json:"schema"`
	Provider        string      `json:"provider"`
	Model           string      `json:"model"`
	ConnectionID    contract.ID `json:"connection_id"`
	MaxInputTokens  int64       `json:"max_input_tokens"`
	MaxOutputTokens int64       `json:"max_output_tokens"`
	Currency        string      `json:"currency"`
	InputRate       struct {
		Numerator   int64  `json:"numerator_micro_units"`
		Denominator int64  `json:"denominator_units"`
		Unit        string `json:"unit"`
	} `json:"input_rate"`
	OutputRate struct {
		Numerator   int64  `json:"numerator_micro_units"`
		Denominator int64  `json:"denominator_units"`
		Unit        string `json:"unit"`
	} `json:"output_rate"`
	SessionMode string `json:"session_mode"`
}

func modelDispatchProfile(plan *contextPlanRow) (mode, schema string, maxOutput int64, err error) {
	if len(plan.Recipe.AdapterProfile) == 0 {
		return "provider_conversation", "zatiti.responses/v1", defaultMaxOutputTokens, nil
	}
	var p resolvedResponsesProfile
	if err = json.Unmarshal(plan.Recipe.AdapterProfile, &p); err != nil {
		return "", "", 0, invalidInput("pinned responses adapter profile is malformed")
	}
	if p.Schema != "zatiti.responses/v2" || (p.Provider != "openai" && p.Provider != "openrouter" && p.Provider != "experiential") {
		return "", "", 0, capabilityUnsupported("pinned responses adapter profile has an unsupported schema or provider")
	}
	if p.SessionMode != "provider_conversation" && p.SessionMode != "stateless" {
		return "", "", 0, capabilityUnsupported("pinned responses adapter profile has no valid session mode")
	}
	if p.MaxInputTokens < 1 || p.MaxOutputTokens < 1 || p.MaxOutputTokens > 1_000_000 {
		return "", "", 0, capabilityUnsupported("pinned responses adapter profile has invalid token bounds")
	}
	if p.Provider == "openai" && p.SessionMode != "provider_conversation" || p.Provider != "openai" && p.SessionMode != "stateless" {
		return "", "", 0, capabilityUnsupported("provider and session mode in pinned profile disagree")
	}
	return p.SessionMode, p.Schema, p.MaxOutputTokens, nil
}

func buildResponsesModelStepActionV2(plan *contextPlanRow, contextArtifact wireArtifactRef, sessionHandle, mode string, maxOutput int64, sessionID contract.ID) (map[string]any, wireRef, wireRef, string, error) {
	modelTool := resolveModelToolComponent(plan)
	if modelTool == nil {
		return nil, wireRef{}, wireRef{}, "", prerequisiteMissing("no responses adapter tool is bound for this worker")
	}
	toolVersions := make([]wireRef, 0, len(plan.Recipe.Components))
	for _, component := range plan.Recipe.Components {
		if component.Kind == "tool" {
			toolVersions = append(toolVersions, wireRef{ID: component.ToolID, Version: component.ToolVersion})
		}
	}
	action := map[string]any{"schema": "zatiti.responses.action/v2", "kind": "model_step", "session_mode": mode, "context_artifact": contextArtifact, "max_output_tokens": maxOutput, "tool_contract_versions": toolVersions}
	if mode == "provider_conversation" {
		if sessionHandle == "" {
			return nil, wireRef{}, wireRef{}, "", prerequisiteMissing("provider conversation model step has no confirmed session handle")
		}
		action["session_handle"] = sessionHandle
	} else {
		// A local deterministic turn ID is grouping metadata only; it is not sent
		// as prior model context. The artifact already contains the full transcript.
		action["session_id"] = string(sessionID)
	}
	raw, err := json.Marshal(action)
	if err != nil {
		return nil, wireRef{}, wireRef{}, "", err
	}
	schema := json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.responses.action/v2"},"kind":{"const":"model_step"},"session_mode":{"enum":["provider_conversation","stateless"]},"session_id":{"type":"string","minLength":1,"maxLength":128},"context_artifact":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"digest":{"type":"string","pattern":"^[0-9a-f]{64}$"}},"required":["id","digest"]},"max_output_tokens":{"type":"integer","minimum":1,"maximum":1000000},"tool_contract_versions":{"type":"array","items":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1}},"required":["id","version"]}},"session_handle":{"type":"string","minLength":1,"maxLength":1024}},"required":["schema","kind","session_mode","context_artifact","max_output_tokens","tool_contract_versions"]}`)
	if err := contract.ValidateSchema(schema, raw); err != nil {
		return nil, wireRef{}, wireRef{}, "", fmt.Errorf("execution: assembled v2 model_step action is invalid: %w", err)
	}
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, wireRef{}, wireRef{}, "", err
	}
	return params, wireRef{ID: modelTool.ToolID, Version: modelTool.ToolVersion}, wireRef{ID: modelTool.ConnectionID, Version: modelTool.ConnectionVersion}, modelTool.AccountIdentity, nil
}

// modelStepMaximumCost computes the conservative bound for one request using
// exact integer arithmetic and rounds fractional micro-units upward.
func modelStepMaximumCost(plan *contextPlanRow) (*wireMoney, error) {
	if len(plan.Recipe.AdapterProfile) == 0 {
		return &wireMoney{Currency: "USD", MicroUnits: 0}, nil
	}
	var p resolvedResponsesProfile
	if err := json.Unmarshal(plan.Recipe.AdapterProfile, &p); err != nil {
		return nil, invalidInput("pinned responses profile is malformed")
	}
	if p.InputRate.Denominator < 1 || p.OutputRate.Denominator < 1 || p.InputRate.Unit != "input_token" || p.OutputRate.Unit != "output_token" || p.InputRate.Numerator < 0 || p.OutputRate.Numerator < 0 || p.Currency == "" {
		return nil, capabilityUnsupported("pinned model profile has no usable exact per-token cost rates")
	}
	inputTokens := p.MaxInputTokens
	total := new(big.Rat).SetFrac(big.NewInt(p.InputRate.Numerator), big.NewInt(p.InputRate.Denominator))
	total.Mul(total, new(big.Rat).SetInt64(inputTokens))
	out := new(big.Rat).SetFrac(big.NewInt(p.OutputRate.Numerator), big.NewInt(p.OutputRate.Denominator))
	total.Add(total, out.Mul(out, new(big.Rat).SetInt64(p.MaxOutputTokens)))
	n, d := total.Num(), total.Denom()
	n.Add(n, new(big.Int).Sub(d, big.NewInt(1)))
	n.Div(n, d)
	if !n.IsInt64() {
		return nil, capabilityUnsupported("model profile maximum cost exceeds the supported accounting range")
	}
	return &wireMoney{Currency: p.Currency, MicroUnits: n.Int64()}, nil
}

// buildResponsesModelStepAction constructs the exact zatiti.responses.action/v1
// model_step fields (schema, kind, context_artifact, max_output_tokens,
// tool_contract_versions, optional continuation_reference) once a session
// handle is known. It resolves real Tool/Connection refs/account from the
// plan's own resolved model-dispatch tool component -- never a hosted
// execution profile's own identity ("profile-as-tool") and never a
// hardcoded connection version -- and validates the assembled action
// against the frozen adapter schema before returning it. Dispatch sequencing
// (minting/reusing the session handle, calling _effects.prepare with the
// CallbackRoute) is the controller's own tick responsibility (contracts.md,
// "a controller-dispatched effect naming an explicit CallbackRoute"); this
// function proves the action shape a caller has to hand it is correct.
func buildResponsesModelStepAction(plan *contextPlanRow, contextArtifact wireArtifactRef, sessionHandle, continuationReference string) (wireResponsesModelStep, map[string]any, wireRef, wireRef, string, error) {
	var modelTool *contextComponent
	toolVersions := make([]wireRef, 0, len(plan.Recipe.Components))
	for i, c := range plan.Recipe.Components {
		if c.Kind != "tool" {
			continue
		}
		toolVersions = append(toolVersions, wireRef{ID: c.ToolID, Version: c.ToolVersion})
		if c.IsModelTool {
			modelTool = &plan.Recipe.Components[i]
		}
	}
	if modelTool == nil {
		return wireResponsesModelStep{}, nil, wireRef{}, wireRef{}, "",
			prerequisiteMissing("no responses-adapter tool is bound for this worker; cannot dispatch a model step")
	}
	if sessionHandle == "" {
		return wireResponsesModelStep{}, nil, wireRef{}, wireRef{}, "",
			prerequisiteMissing("model step requires an already-resolved session handle from a prior prepare_session effect")
	}
	action := wireResponsesModelStep{
		Schema:                "zatiti.responses.action/v1",
		Kind:                  "model_step",
		SessionHandle:         sessionHandle,
		ContextArtifact:       contextArtifact,
		MaxOutputTokens:       defaultMaxOutputTokens,
		ToolContractVersions:  toolVersions,
		ContinuationReference: continuationReference,
	}
	raw, err := json.Marshal(action)
	if err != nil {
		return wireResponsesModelStep{}, nil, wireRef{}, wireRef{}, "", err
	}
	schema, err := responsesModelStepSchema()
	if err != nil {
		return wireResponsesModelStep{}, nil, wireRef{}, wireRef{}, "", fmt.Errorf("execution: load responses model_step schema: %w", err)
	}
	if err := contract.ValidateSchema(schema, raw); err != nil {
		return wireResponsesModelStep{}, nil, wireRef{}, wireRef{}, "", fmt.Errorf(
			"execution: assembled model_step action does not satisfy zatiti.responses.action/v1: %w", err)
	}
	var parameters map[string]any
	if err := json.Unmarshal(raw, &parameters); err != nil {
		return wireResponsesModelStep{}, nil, wireRef{}, wireRef{}, "", err
	}
	toolRef := wireRef{ID: modelTool.ToolID, Version: modelTool.ToolVersion}
	connectionRef := wireRef{ID: modelTool.ConnectionID, Version: modelTool.ConnectionVersion}
	return action, parameters, toolRef, connectionRef, modelTool.AccountIdentity, nil
}

// resolveModelToolComponent locates the plan's one resolved model-dispatch
// tool component (the bound tool whose resolved adapter is "responses"), the
// same lookup buildResponsesModelStepAction performs inline. It is factored
// out here (execution-dispatch-model-step, the same-day P22 gap fix) so
// buildResponsesPrepareSessionAction and handleContextCommit's own dispatch
// helper resolve the identical real Tool/Connection identity -- never a
// second, independent lookup that could disagree, and never the worker's own
// execution profile identity (profile-as-tool). buildResponsesModelStepAction
// itself is left with its own original inline loop unchanged, since P15's
// existing test asserts its exact return shape.
func resolveModelToolComponent(plan *contextPlanRow) *contextComponent {
	for i, c := range plan.Recipe.Components {
		if c.Kind == "tool" && c.IsModelTool {
			return &plan.Recipe.Components[i]
		}
	}
	return nil
}

// buildResponsesPrepareSessionAction constructs the exact
// zatiti.responses.action/v1 prepare_session fields (schema, kind) -- no
// model-visible content, no context_artifact, no session_handle, since this
// action's own purpose is to obtain one (AGENTS.md's "OpenAI Responses
// session preparation": "the action carries no model-visible content beyond
// what an empty conversation creation requires"). It resolves the same real
// Tool/Connection identity buildResponsesModelStepAction resolves, so both
// dispatches for the same turn always name the identical account/connection.
func buildResponsesPrepareSessionAction(plan *contextPlanRow) (wireResponsesPrepareSession, map[string]any, wireRef, wireRef, string, error) {
	modelTool := resolveModelToolComponent(plan)
	if modelTool == nil {
		return wireResponsesPrepareSession{}, nil, wireRef{}, wireRef{}, "",
			prerequisiteMissing("no responses-adapter tool is bound for this worker; cannot dispatch prepare_session")
	}
	action := wireResponsesPrepareSession{
		Schema: "zatiti.responses.action/v1",
		Kind:   "prepare_session",
	}
	raw, err := json.Marshal(action)
	if err != nil {
		return wireResponsesPrepareSession{}, nil, wireRef{}, wireRef{}, "", err
	}
	schema, err := responsesPrepareSessionSchema()
	if err != nil {
		return wireResponsesPrepareSession{}, nil, wireRef{}, wireRef{}, "", fmt.Errorf("execution: load responses prepare_session schema: %w", err)
	}
	if err := contract.ValidateSchema(schema, raw); err != nil {
		return wireResponsesPrepareSession{}, nil, wireRef{}, wireRef{}, "", fmt.Errorf(
			"execution: assembled prepare_session action does not satisfy zatiti.responses.action/v1: %w", err)
	}
	var parameters map[string]any
	if err := json.Unmarshal(raw, &parameters); err != nil {
		return wireResponsesPrepareSession{}, nil, wireRef{}, wireRef{}, "", err
	}
	toolRef := wireRef{ID: modelTool.ToolID, Version: modelTool.ToolVersion}
	connectionRef := wireRef{ID: modelTool.ConnectionID, Version: modelTool.ConnectionVersion}
	return action, parameters, toolRef, connectionRef, modelTool.AccountIdentity, nil
}

// rawOrEmptyObject renders raw as itself, or an empty JSON object when raw
// carries no bytes: input/output schemas are optional per bound tool.
func rawOrEmptyObject(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}

func nonNilRefs(in []wireRef) []wireRef {
	if in == nil {
		return []wireRef{}
	}
	return in
}

func nonNilArtifactRefs(in []wireArtifactRef) []wireArtifactRef {
	if in == nil {
		return []wireArtifactRef{}
	}
	return in
}

func nonNilMessages(in []wireContextMessage) []wireContextMessage {
	if in == nil {
		return []wireContextMessage{}
	}
	return in
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
