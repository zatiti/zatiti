package messaging

import (
	"context"
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// Peer call plumbing. Messaging calls exactly the four declared outgoing
// operations (_configuration.snapshot, _policy.check, _artifacts.metadata,
// _evidence.snapshot). Peer responses are decoded into typed structs; the
// peers own the strictness of their own wire documents, and lenient field
// extraction is safe here. Assignment and task creation stay in the tasks
// owner: messages reference tasks by id and never call task operations.

// callPeer marshals input, invokes the named peer operation at version 1
// inside the caller's unit, and decodes a completed payload into out.
func (s *Service) callPeer(ctx context.Context, unit contract.Unit, op string, input any, out any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return internalError("peer input encoding failed for %s: %v", op, err)
	}
	payload, err := s.ports.Call(ctx, unit, contract.Invocation{Operation: op, Version: 1, Input: raw})
	if err != nil {
		return faultWrap(&contract.Fault{Code: contract.CodeInternalError, Message: "peer call " + op + " failed"}, err)
	}
	if payload.Error != nil {
		return &faultError{fault: payload.Error}
	}
	if payload.Status == contract.StatusFailed {
		return internalError("peer call %s returned failed status without a fault", op)
	}
	if out == nil || len(payload.Data) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload.Data, out); err != nil {
		return internalError("peer %s response decoding failed: %v", op, err)
	}
	return nil
}

// peerInvocation wraps the exact input shapes of the outgoing operations.

type peerSnapshotIn struct {
	Scope wireScope `json:"scope"`
}

// peerScopeSnapshot extracts the fields messaging uses from ScopeSnapshot.
// Only bindings matter here: they carry the reporting relationships that
// decide whether an admitted agent message is a meaningful human-facing
// event or quiet coordination.
type peerScopeSnapshot struct {
	Resource struct {
		Scope    wireScope `json:"scope"`
		Revision int64     `json:"revision"`
		Bindings []struct {
			ID           string   `json:"id"`
			Version      int64    `json:"version"`
			Kind         string   `json:"kind"`
			TargetID     string   `json:"target_id"`
			Permissions  []string `json:"permissions"`
			Destinations []string `json:"destinations"`
		} `json:"bindings"`
	} `json:"resource"`
}

// peerReportingBinding is one decoded binding of the reporting kind.
type peerReportingBinding struct {
	ID           string
	TargetID     string
	Destinations []string
}

// peerConfigurationSnapshot reads the current ancestry/bindings snapshot.
func (s *Service) peerConfigurationSnapshot(ctx context.Context, unit contract.Unit, scope contract.Scope) (*peerScopeSnapshot, error) {
	var out peerScopeSnapshot
	if err := s.callPeer(ctx, unit, "_configuration.snapshot",
		peerSnapshotIn{Scope: scopeFromContract(scope)}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// reportingBindingsFor returns the reporting bindings granted to one worker
// identity inside the snapshot.
func reportingBindings(snapshot *peerScopeSnapshot, workerID contract.ID) []peerReportingBinding {
	var out []peerReportingBinding
	for _, b := range snapshot.Resource.Bindings {
		if b.Kind != bindingKindReporting || b.TargetID != string(workerID) {
			continue
		}
		out = append(out, peerReportingBinding{ID: b.ID, Destinations: b.Destinations})
	}
	return out
}

type peerMetadataIn struct {
	Scope     wireScope         `json:"scope"`
	Artifacts []wireArtifactRef `json:"artifacts"`
}

// peerArtifact is the Artifact def fields messaging consumes.
type peerArtifact struct {
	ID             contract.ID `json:"id"`
	Version        int64       `json:"version"`
	Scope          wireScope   `json:"scope"`
	Digest         string      `json:"digest"`
	Size           int64       `json:"size"`
	MediaType      string      `json:"media_type"`
	State          string      `json:"state"`
	Encrypted      bool        `json:"encrypted"`
	CreatedAt      string      `json:"created_at"`
	Classification string      `json:"classification"`
}

type peerMetadataOut struct {
	Artifacts []peerArtifact `json:"artifacts"`
}

// peerArtifactMetadata resolves a batch of artifact references. The peer
// validates scope, digests, availability and classification; this wrapper
// additionally verifies the response covers every requested reference so a
// silent peer omission cannot pass as presence.
func (s *Service) peerArtifactMetadata(ctx context.Context, unit contract.Unit, scope contract.Scope, refs []wireArtifactRef) ([]peerArtifact, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	var out peerMetadataOut
	err := s.callPeer(ctx, unit, "_artifacts.metadata",
		peerMetadataIn{Scope: scopeFromContract(scope), Artifacts: refs}, &out)
	if err != nil {
		return nil, err
	}
	byID := make(map[contract.ID]bool, len(out.Artifacts))
	for _, a := range out.Artifacts {
		byID[a.ID] = true
	}
	for _, ref := range refs {
		if !byID[ref.ID] {
			return nil, artifactFault("artifact %s is not resolvable in scope", ref.ID)
		}
	}
	return out.Artifacts, nil
}

// validateArtifacts checks an attachment batch against the artifacts owner:
// every reference must resolve, carry the pinned digest, be in the same
// installation and be available before it may be disclosed to recipients.
func (s *Service) validateArtifacts(ctx context.Context, unit contract.Unit, scope contract.Scope, refs []wireArtifactRef) error {
	metas, err := s.peerArtifactMetadata(ctx, unit, scope, refs)
	if err != nil {
		return err
	}
	byID := make(map[contract.ID]peerArtifact, len(metas))
	for _, m := range metas {
		byID[m.ID] = m
	}
	for _, ref := range refs {
		m, ok := byID[ref.ID]
		if !ok {
			return artifactFault("artifact %s is missing from metadata", ref.ID)
		}
		if string(m.Digest) != string(ref.Digest) {
			return invalidInput("artifact %s digest does not match the pinned reference", ref.ID)
		}
		if m.Scope.InstallationID != scope.InstallationID {
			return permissionDenied("artifact %s is outside the installation scope", ref.ID)
		}
		if m.State != "available" {
			return artifactFault("artifact %s is not available", ref.ID)
		}
	}
	return nil
}

type peerPolicyCheckIn struct {
	Scope           wireScope `json:"scope"`
	Capability      string    `json:"capability"`
	CandidateDigest string    `json:"candidate_digest,omitempty"`
}

type peerPolicyCheckOut struct {
	Resource struct {
		Decision     string   `json:"decision"`
		Reasons      []string `json:"reasons"`
		Requirements []any    `json:"requirements"`
	} `json:"resource"`
}

// peerPolicyCheck intersects current grants and required conditions for the
// disclosure capability. Decisions map to exact faults: deny is
// permission_denied, review is review_required, prerequisite_missing is
// prerequisite_missing, and anything else fails closed.
func (s *Service) peerPolicyCheck(ctx context.Context, unit contract.Unit, scope contract.Scope, capability, candidateDigest string) error {
	var out peerPolicyCheckOut
	err := s.callPeer(ctx, unit, "_policy.check",
		peerPolicyCheckIn{Scope: scopeFromContract(scope), Capability: capability, CandidateDigest: candidateDigest}, &out)
	if err != nil {
		return err
	}
	switch out.Resource.Decision {
	case "allow":
		return nil
	case "deny":
		return permissionDenied("disclosure denied by policy: %s", joinReasons(out.Resource.Reasons))
	case "review":
		return reviewRequired("disclosure requires human review: %s", joinReasons(out.Resource.Reasons))
	case "prerequisite_missing":
		return prerequisiteMissing("disclosure prerequisites are missing: %s", joinReasons(out.Resource.Reasons))
	default:
		return permissionDenied("disclosure policy decision is not affirmative")
	}
}

// joinReasons renders peer reasons into fault messages without changing
// their content.
func joinReasons(reasons []string) string {
	out := ""
	for i, r := range reasons {
		if i > 0 {
			out += "; "
		}
		out += r
	}
	return out
}

type peerEvidenceOut struct {
	LastSequence int64 `json:"last_sequence"`
}

// peerEvidenceSnapshot reads the current event checkpoint so cursors are
// minted from committed event state, never from private invention.
func (s *Service) peerEvidenceSnapshot(ctx context.Context, unit contract.Unit, scope contract.Scope) (int64, error) {
	var out peerEvidenceOut
	if err := s.callPeer(ctx, unit, "_evidence.snapshot",
		peerSnapshotIn{Scope: scopeFromContract(scope)}, &out); err != nil {
		return 0, err
	}
	return out.LastSequence, nil
}
