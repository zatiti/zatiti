package tasks

import (
	"context"
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// Peer call plumbing. Tasks calls exactly the eight declared outgoing
// operations. Peer responses are decoded into typed structs; the peers own
// the strictness of their own wire documents, and typed int64 fields decode
// exactly through encoding/json, so lenient field extraction is safe here.

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

// peerScopeSnapshot extracts the fields tasks uses from ScopeSnapshot.
// Ancestors and bindings are validated by configuration; tasks needs the
// worker identity and the ancestry organization ids for scope containment.
type peerScopeSnapshot struct {
	Resource struct {
		Scope     wireScope `json:"scope"`
		Revision  int64     `json:"revision"`
		Ancestors []struct {
			ID       string `json:"id"`
			ParentID string `json:"parent_id"`
		} `json:"ancestors"`
		Worker *struct {
			ID             string `json:"id"`
			OrganizationID string `json:"organization_id"`
			State          string `json:"state"`
		} `json:"worker"`
		Project *struct {
			ID             string `json:"id"`
			OrganizationID string `json:"organization_id"`
			State          string `json:"state"`
		} `json:"project"`
	} `json:"resource"`
}

type peerMetadataIn struct {
	Scope     wireScope         `json:"scope"`
	Artifacts []wireArtifactRef `json:"artifacts"`
}

// peerArtifact is the Artifact def fields tasks consumes.
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

type peerEnqueueIn struct {
	Task wireTask `json:"task"`
}

type peerEnqueueOut struct {
	Resource struct {
		ID      contract.ID `json:"id"`
		Version int64       `json:"version"`
		TaskID  contract.ID `json:"task_id"`
		State   string      `json:"state"`
	} `json:"resource"`
}

type peerReserveIn struct {
	Scope       wireScope   `json:"scope"`
	RootTaskID  contract.ID `json:"root_task_id,omitempty"`
	OperationID contract.ID `json:"operation_id"`
	Amount      wireMoney   `json:"amount"`
	Limits      wireLimits  `json:"limits"`
}

type peerReserveOut struct {
	Resource struct {
		ID      contract.ID `json:"id"`
		Version int64       `json:"version"`
		State   string      `json:"state"`
	} `json:"resource"`
}

type peerInspectIn struct {
	Scope wireScope `json:"scope"`
}

type peerInspectOut struct {
	Limits wireLimits `json:"limits"`
	Usage  struct {
		Currency  string `json:"currency"`
		Spent     int64  `json:"spent"`
		Reserved  int64  `json:"reserved"`
		Estimated int64  `json:"estimated"`
		Unknown   int64  `json:"unknown"`
		Advisory  bool   `json:"advisory"`
	} `json:"usage"`
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

type peerPolicyInvalidateIn struct {
	ChangedDependencies []wireRef `json:"changed_dependencies"`
	Reason              string    `json:"reason"`
}

type peerReviewsCheckIn struct {
	Scope        wireScope `json:"scope"`
	ActionDigest string    `json:"action_digest"`
}

type peerReviewsCheckOut struct {
	Eligible bool `json:"eligible"`
	Decision *struct {
		ID         string `json:"id"`
		ReviewerID string `json:"reviewer_id"`
		Decision   string `json:"decision"`
	} `json:"decision"`
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

// validateArtifacts checks a pinned reference batch against the artifacts
// owner: every reference must resolve, carry the pinned digest, be in an
// installation-compatible scope and be available.
func (s *Service) validateArtifacts(ctx context.Context, unit contract.Unit, scope contract.Scope, refs []wireArtifactRef) ([]peerArtifact, error) {
	metas, err := s.peerArtifactMetadata(ctx, unit, scope, refs)
	if err != nil {
		return nil, err
	}
	byID := make(map[contract.ID]peerArtifact, len(metas))
	for _, m := range metas {
		byID[m.ID] = m
	}
	for _, ref := range refs {
		m, ok := byID[ref.ID]
		if !ok {
			return nil, artifactFault("artifact %s is missing from metadata", ref.ID)
		}
		if string(m.Digest) != string(ref.Digest) {
			return nil, invalidInput("artifact %s digest does not match the pinned reference", ref.ID)
		}
		if m.Scope.InstallationID != scope.InstallationID {
			return nil, permissionDenied("artifact %s is outside the installation scope", ref.ID)
		}
		if m.State != "available" {
			return nil, artifactFault("artifact %s is not available", ref.ID)
		}
	}
	return metas, nil
}
