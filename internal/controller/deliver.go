package controller

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// Callback owners.
const (
	ownerExecution   = "execution"
	ownerMemory      = "memory"
	ownerConnections = "connections"
	// ownerExecutionProposal routes a worker_turn-callback-routed effect's
	// outcome to _execution.proposal.record instead of _execution.
	// observation: the effect is a proposal's own external_tool dispatch
	// (P16 interpretExternalTool), not a fresh model step, and the owner's
	// turn/proposal record -- not an attempt -- is what advances.
	ownerExecutionProposal = "execution_proposal"
	ownerExecutionTurn     = "execution_turn"
	ownerQualification     = "configuration_qualification"
)

// stagedOutput mirrors the adapter StagedOutput handoff: bytes an adapter
// staged and hashed but did not publish.
type stagedOutput struct {
	StagingRef     string          `json:"staging_ref"`
	Digest         contract.Digest `json:"digest"`
	Size           int64           `json:"size"`
	MediaType      string          `json:"media_type"`
	Classification string          `json:"classification"`
	Purpose        string          `json:"purpose"`
}

// routeFor decides, from the admitted operation, which owner is waiting on
// it. The frozen contract's explicit callback_route (P00-006, revision 3)
// is authoritative and checked first; the legacy pre-turn hosted loop
// (controller_ops.go's prepareModelEffect, audit finding G05) predates
// callback_route and is never given one, so its attempt identity in the
// action's own parameters is kept as a fallback -- never the reverse, so a
// turn-linked effect is never misrouted by an attempt_id a strict provider
// parameter set happens to also carry. A network job naming the operation,
// and a probe's connection, are unrelated routing sources checked
// independently. An operation nobody waits on has no callback.
func (c *Controller) routeFor(operation contract.ID, op wireOperation, action wireAction, jobs map[contract.ID]wireJob) *route {
	if job, ok := jobs[operation]; ok {
		switch job.Owner {
		case ownerMemory:
			return &route{Owner: ownerMemory, JobID: job.ID}
		case ownerConnections:
			if job.Operation == "connection.validate" || job.Operation == "connection.discover" {
				return &route{Owner: ownerConnections, JobID: job.ID, Connection: action.Connection, ProbeKind: job.Operation}
			}
		case configurationOwner:
			if job.Operation == "execution_profile.qualify" && job.State == jobStatePending {
				var params qualificationProbeParameters
				if json.Unmarshal(action.Parameters, &params) == nil &&
					params.ProfileDigest != "" && params.Provider != "" {
					return &route{Owner: ownerQualification, JobID: job.ID, JobVersion: job.Version, ProfileDigest: params.ProfileDigest, Provider: params.Provider}
				}
			}
		}
	}
	if len(op.CallbackRoute) > 0 {
		var cb wireCallbackRoute
		if json.Unmarshal(op.CallbackRoute, &cb) == nil && cb.Kind == "worker_turn" && cb.TurnID != "" {
			if r := c.routeWorkerTurn(operation, cb); r != nil {
				return r
			}
		}
	}
	var params struct {
		AttemptID contract.ID `json:"attempt_id"`
	}
	if len(action.Parameters) > 0 && json.Unmarshal(action.Parameters, &params) == nil && params.AttemptID != "" {
		return &route{Owner: ownerExecution, AttemptID: params.AttemptID}
	}
	return &route{}
}

// routeWorkerTurn resolves a worker_turn callback route using the indexes
// the turn-work phase refreshed this tick: a model-step effect (this turn's
// own attempt is known) routes to _execution.observation, and a proposal's
// own external_tool effect (this exact operation was seen "prepared" by an
// earlier proposal.prepare discovery) routes to _execution.proposal.record.
// Neither index is a database of record -- they are exactly what the
// current tick's discovery already re-derived from live owner state, so a
// route that cannot be resolved this tick is retried next tick rather than
// guessed at.
func (c *Controller) routeWorkerTurn(operation contract.ID, cb wireCallbackRoute) *route {
	c.turnsMu.Lock()
	defer c.turnsMu.Unlock()
	// A proposal's own external_tool effect is keyed by this exact
	// operation id -- checked first, since such an operation is also
	// carried under its turn's attempt and must route to the proposal, not
	// re-enter model-step observation.
	if ref, ok := c.turnProposals[operation]; ok {
		return &route{Owner: ownerExecutionProposal, ProposalID: ref.ProposalID}
	}
	if attemptID, ok := c.turnAttempts[cb.TurnID]; ok {
		info := c.turnInfos[cb.TurnID]
		step := info.StepIndex
		if cb.StepIndex != nil {
			step = *cb.StepIndex
		}
		return &route{Owner: ownerExecution, AttemptID: attemptID, TurnID: cb.TurnID, StepIndex: step}
	}
	if _, ok := c.turnInfos[cb.TurnID]; ok && cb.StepIndex != nil {
		return &route{Owner: ownerExecutionTurn, TurnID: cb.TurnID, StepIndex: *cb.StepIndex}
	}
	return nil
}

// deliver finishes a recorded effect: publish the staged outputs — the
// adapter's staged request context among them, for every disposition
// including not_sent and unknown — then hand the normalized observation to
// the waiting owner. The raw observation is already durable with the effects
// owner and is never rewritten, so nothing here can erase it and a failure
// here never authorizes another provider call.
func (c *Controller) deliver(ctx context.Context, sess *session, e *entry) {
	normalized, ok := c.publish(ctx, sess, e)
	if !ok {
		return
	}
	if e.Route == nil || e.Route.Owner == "" {
		c.finish(sess, e)
		return
	}
	var err error
	switch e.Route.Owner {
	case ownerExecution:
		err = c.write(func() error {
			return c.call(ctx, sess, "_execution.observation", executionObservationInput{
				AttemptID: e.Route.AttemptID, OperationID: e.OperationID, Observation: normalized,
			}, nil)
		})
		if err == nil {
			c.observeTurnDelivery(ctx, sess, e, normalized)
		}
	case ownerExecutionTurn:
		err = c.write(func() error {
			return c.call(ctx, sess, "_execution.turn.observation", turnObservationInput{
				TurnID: e.Route.TurnID, StepIndex: e.Route.StepIndex,
				OperationID: e.OperationID, Observation: normalized,
			}, nil)
		})
		if err == nil {
			c.observeTurnDelivery(ctx, sess, e, normalized)
		}
	case ownerExecutionProposal:
		c.turnsMu.Lock()
		expected := c.turnProposals[e.OperationID].ExpectedVersion
		c.turnsMu.Unlock()
		artifact := resultArtifactOf(normalized)
		err = c.write(func() error {
			return c.call(ctx, sess, "_execution.proposal.record", proposalRecordInput{
				ProposalID: e.Route.ProposalID, ExpectedVersion: expected,
				EffectOperationID: e.OperationID, ResultArtifact: artifact,
			}, nil)
		})
	case ownerMemory:
		err = c.write(func() error {
			return c.call(ctx, sess, "_memory.record", memoryRecordInput{
				JobID: e.Route.JobID, OperationID: e.OperationID, Observation: normalized,
			}, nil)
		})
	case ownerConnections:
		err = c.write(func() error {
			input := connectionRecordInput{
				OperationID: e.OperationID, AttemptID: e.AttemptID,
				ConnectionID: e.Route.Connection.ID, ExpectedVersion: e.Route.Connection.Version,
				Observation: normalized,
			}
			probeKind := e.Route.ProbeKind
			if probeKind == "" {
				// Before probe_kind was journaled, every connections callback
				// was a validation callback. Keep those durable entries
				// deliverable across upgrades.
				probeKind = "connection.validate"
			}
			switch probeKind {
			case "connection.validate":
				return c.call(ctx, sess, "_connections.validation.record", validationRecordInput(input), nil)
			case "connection.discover":
				return c.call(ctx, sess, "_connections.discovery.record", discoveryRecordInput(input), nil)
			default:
				return internalFault("journal entry names unsupported connection callback %q", e.Route.ProbeKind)
			}
		})
	case ownerQualification:
		err = c.deliverQualification(ctx, sess, e, normalized)
	default:
		err = internalFault("journal entry names unknown callback owner %q", e.Route.Owner)
	}
	if err != nil {
		c.note(err)
		if !isFault(err) && !e.Unacked {
			// The callback may have committed. Remember that, so the owner's
			// duplicate refusal on redelivery reads as delivered.
			e.Unacked = true
			c.journal(sess, *e)
		}
		if transient(err) {
			return
		}
		if f := faultOf(err); e.Unacked && (f.Code == contract.CodeConflict || f.Code == contract.CodeStaleVersion) {
			c.finish(sess, e)
			return
		}
		// The owner durably refused the callback (a fenced attempt, a moved
		// version). Redelivery cannot change that.
		c.refuse(sess, e, obligationDelivery, faultOf(err))
		return
	}
	c.finish(sess, e)
}

func (c *Controller) finish(sess *session, e *entry) {
	e.Phase = phaseDone
	e.Fault = nil
	if c.journal(sess, *e) {
		c.resolve(obligationPublication, e.AttemptID)
	}
}

func (c *Controller) refuse(sess *session, e *entry, kind string, f *contract.Fault) {
	e.Phase = phaseRefused
	e.Fault = f
	if c.journal(sess, *e) {
		c.oblige(kind, e.AttemptID, f)
	}
}

// publish makes every staged output of the observation a real artifact and
// returns the observation with staged locators replaced. Bytes are published
// outside any transaction, then metadata is committed through the artifacts
// owner; progress is journaled per output so a retry never republishes
// metadata. An unpublishable output blocks the owner callback and stays a
// visible obligation.
func (c *Controller) publish(ctx context.Context, sess *session, e *entry) (contract.Observation, bool) {
	obs := *e.Observation
	staged, err := stagedOutputs(obs.Evidence)
	if err != nil {
		c.refuse(sess, e, obligationPublication, invalidInput("observation evidence declares malformed staged outputs"))
		return obs, false
	}
	if err := checkLocators(obs.Evidence, staged); err != nil {
		// The raw observation stands as recorded; evidence whose locators
		// cannot be resolved is never delivered as if it were published.
		c.refuse(sess, e, obligationPublication, err)
		return obs, false
	}
	if len(staged) == 0 {
		return obs, true
	}
	c.mu.Lock()
	blobs := c.deps.Blobs
	c.mu.Unlock()
	if blobs == nil {
		f := prerequisiteMissing(
			"no blob store is attached; %d staged output(s) of attempt %s cannot be published and the owner callback is withheld",
			len(staged), e.AttemptID)
		c.note(f)
		c.oblige(obligationPublication, e.AttemptID, f)
		return obs, false
	}
	scope := sess.scope
	if e.Scope != nil && e.Scope.InstallationID == sess.scope.InstallationID {
		scope = *e.Scope
	}
	done := make(map[string]contract.ID, len(e.Published))
	for _, p := range e.Published {
		done[p.StagingRef] = p.ArtifactID
	}
	for _, out := range staged {
		if _, ok := done[out.StagingRef]; ok {
			continue
		}
		var artifact artifactOutput
		err := c.write(func() error {
			if err := blobs.Publish(ctx, out.StagingRef, out.Digest); err != nil {
				return err
			}
			return c.call(ctx, sess, "_artifacts.publish", artifactsPublishInput{
				Scope:          scope,
				Digest:         out.Digest,
				Size:           out.Size,
				MediaType:      out.MediaType,
				Classification: out.Classification,
				Encrypted:      true,
			}, &artifact)
		})
		if err == nil && (artifact.Resource.ID == "" || artifact.Resource.Digest != out.Digest) {
			err = internalFault("artifacts owner published metadata that does not match the staged digest")
		}
		if err != nil {
			c.note(err)
			if transient(err) {
				return obs, false
			}
			c.refuse(sess, e, obligationPublication, faultOf(err))
			return obs, false
		}
		e.Published = append(e.Published, publishedOutput{
			StagingRef: out.StagingRef, Digest: out.Digest, ArtifactID: artifact.Resource.ID,
		})
		done[out.StagingRef] = artifact.Resource.ID
		if !c.journal(sess, *e) {
			return obs, false
		}
	}
	evidence, err := normalizeEvidence(obs.Evidence, e.Published)
	if err != nil {
		c.refuse(sess, e, obligationPublication, internalFault("published evidence cannot be normalized"))
		return obs, false
	}
	obs.Evidence = evidence
	return obs, true
}

// resultArtifactOf reads the first published output_artifacts entry of a
// normalized observation, the same field publish() populates from staged
// outputs -- a reasonable, inspectable choice of "the artifact this proposal
// produced" when the adapter published exactly one. An effect that
// published nothing (or several outputs, disambiguated only by the tool's
// own domain semantics execution -- not the controller -- owns) records no
// result_artifact rather than guessing.
func resultArtifactOf(obs contract.Observation) *wireArtifact {
	var probe struct {
		OutputArtifacts []wireArtifact `json:"output_artifacts"`
	}
	if len(obs.Evidence) == 0 || json.Unmarshal(obs.Evidence, &probe) != nil || len(probe.OutputArtifacts) != 1 {
		return nil
	}
	return &probe.OutputArtifacts[0]
}

// stagedOutputs reads the top-level staged_outputs every adapter evidence
// schema shares. Evidence without the field stages nothing.
func stagedOutputs(evidence json.RawMessage) ([]stagedOutput, error) {
	if len(evidence) == 0 {
		return nil, nil
	}
	var probe struct {
		Staged []stagedOutput `json:"staged_outputs"`
	}
	if err := json.Unmarshal(evidence, &probe); err != nil {
		return nil, err
	}
	for _, s := range probe.Staged {
		if s.StagingRef == "" || s.Digest == "" || s.MediaType == "" || s.Size < 0 {
			return nil, invalidInput("staged output is incomplete")
		}
		switch s.Classification {
		case "public", "internal", "restricted":
		default:
			return nil, invalidInput("staged output classification is not supported")
		}
	}
	return probe.Staged, nil
}

// checkLocators requires every staged ArtifactLocator in the evidence —
// physical_call.request_context and ModelOutput.request_context included —
// to name exactly one StagedOutput of the same observation, and no
// StagedOutput to be declared twice. Anything else cannot be published
// faithfully and is refused before any bytes move.
func checkLocators(evidence json.RawMessage, staged []stagedOutput) *contract.Fault {
	if len(evidence) == 0 {
		return nil
	}
	matches := make(map[string]int, len(staged))
	for _, s := range staged {
		key := s.StagingRef + "\x00" + string(s.Digest)
		matches[key]++
		if matches[key] > 1 {
			return invalidInput("staged output %q is declared more than once", s.StagingRef)
		}
	}
	dec := json.NewDecoder(bytes.NewReader(evidence))
	dec.UseNumber()
	var root map[string]any
	if err := dec.Decode(&root); err != nil {
		return invalidInput("observation evidence is not a JSON object")
	}
	for key, value := range root {
		if key == "staged_outputs" {
			continue
		}
		if f := walkLocators(value, matches); f != nil {
			return f
		}
	}
	return nil
}

func walkLocators(value any, matches map[string]int) *contract.Fault {
	switch v := value.(type) {
	case map[string]any:
		if kind, _ := v["kind"].(string); kind == "staged" {
			ref, _ := v["staging_ref"].(string)
			digest, _ := v["digest"].(string)
			if matches[ref+"\x00"+digest] != 1 {
				return invalidInput("staged locator %q matches no staged output of the observation", ref)
			}
			return nil
		}
		for _, inner := range v {
			if f := walkLocators(inner, matches); f != nil {
				return f
			}
		}
	case []any:
		for _, inner := range v {
			if f := walkLocators(inner, matches); f != nil {
				return f
			}
		}
	}
	return nil
}

// normalizeEvidence rewrites evidence for owners: every staged locator that
// names a published output becomes an artifact locator, output_artifacts
// gains the published references and staged_outputs empties, because the
// staging references no longer exist. Numbers keep their exact text.
func normalizeEvidence(evidence json.RawMessage, published []publishedOutput) (json.RawMessage, error) {
	refs := make(map[string]publishedOutput, len(published))
	for _, p := range published {
		refs[p.StagingRef] = p
	}
	dec := json.NewDecoder(bytes.NewReader(evidence))
	dec.UseNumber()
	var root map[string]any
	if err := dec.Decode(&root); err != nil {
		return nil, err
	}
	for key, value := range root {
		if key == "staged_outputs" {
			continue
		}
		root[key] = relocate(value, refs)
	}
	root["staged_outputs"] = []any{}
	artifacts, _ := root["output_artifacts"].([]any)
	for _, p := range published {
		artifacts = append(artifacts, map[string]any{"id": string(p.ArtifactID), "digest": string(p.Digest)})
	}
	root["output_artifacts"] = artifacts
	return json.Marshal(root)
}

// relocate replaces staged ArtifactLocator objects, recursively.
func relocate(value any, refs map[string]publishedOutput) any {
	switch v := value.(type) {
	case map[string]any:
		if kind, _ := v["kind"].(string); kind == "staged" {
			ref, _ := v["staging_ref"].(string)
			digest, _ := v["digest"].(string)
			if p, ok := refs[ref]; ok && string(p.Digest) == digest {
				return map[string]any{
					"kind":     "artifact",
					"artifact": map[string]any{"id": string(p.ArtifactID), "digest": string(p.Digest)},
				}
			}
		}
		for key, inner := range v {
			v[key] = relocate(inner, refs)
		}
		return v
	case []any:
		for i, inner := range v {
			v[i] = relocate(inner, refs)
		}
		return v
	default:
		return value
	}
}
