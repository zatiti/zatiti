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

// routeFor decides, from the immutable action, which owner is waiting on an
// operation. The frozen contract carries no owner field on an operation, so
// the controller relies on the only durable links that exist: a network job
// that names the operation, and the hosted loop's attempt identity in the
// action parameters. An operation nobody waits on has no callback.
func routeFor(operation contract.ID, action wireAction, jobs map[contract.ID]wireJob) *route {
	if job, ok := jobs[operation]; ok {
		switch job.Owner {
		case ownerMemory:
			return &route{Owner: ownerMemory, JobID: job.ID}
		case ownerConnections:
			return &route{Owner: ownerConnections, JobID: job.ID, Connection: action.Connection}
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

// deliver finishes a recorded effect: publish the staged outputs, then hand
// the normalized observation to the waiting owner. The raw observation is
// already durable with the effects owner, so nothing here can erase it and a
// failure here never authorizes another provider call.
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
	case ownerMemory:
		err = c.write(func() error {
			return c.call(ctx, sess, "_memory.record", memoryRecordInput{
				JobID: e.Route.JobID, OperationID: e.OperationID, Observation: normalized,
			}, nil)
		})
	case ownerConnections:
		err = c.write(func() error {
			return c.call(ctx, sess, "_connections.validation.record", validationRecordInput{
				ConnectionID:    e.Route.Connection.ID,
				ExpectedVersion: e.Route.Connection.Version,
				Observation:     normalized,
			}, nil)
		})
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
