package serenity

import (
	"encoding/json"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// requestContextRef returns the ArtifactRef PhysicalCallEvidence.request_context
// carries. It follows internal/adapters/github/evidence.go: request_context
// is a bare ArtifactRef no adapter can mint, so the one durable reference the
// adapter legitimately holds, the profile's capability_evidence.artifact, is
// reused. That is not a claim about request bytes; this adapter sends none.
func requestContextRef(profile *serenityProfile) wireArtifactRef {
	return profile.CapabilityEvidence.Artifact
}

// noRequestUsage is the usage of an attempt that sent nothing: an exact zero
// in the profile's currency. It says nothing about the original command's
// cost, which stays with that command's own attempt record.
func noRequestUsage(currency string) wireProviderUsage {
	return wireProviderUsage{
		Accounting: wireUsage{Currency: currency},
		Billing:    "no_charge",
	}
}

type builtEvidence struct {
	doc   json.RawMessage
	usage json.RawMessage
}

// buildUnknownEvidence assembles the zatiti.serenity.evidence/v1 document
// for a Reconcile that could not look anything up: no request sent, command
// status unknown, lookup not authoritative. The document is validated
// against the frozen evidence schema before it leaves the adapter.
func (a *Adapter) buildUnknownEvidence(dispatch contract.Dispatch, act *action, brain wireBrainMapping, now time.Time) (builtEvidence, error) {
	usage := noRequestUsage(a.profile.Enforcement.MaximumCost.Currency)
	usageDoc, err := json.Marshal(usage)
	if err != nil {
		return builtEvidence{}, internalError("encoding serenity usage failed: %v", err)
	}

	authoritative := false
	ev := wireSerenityEvidence{
		Schema: "zatiti.serenity.evidence/v1",
		PhysicalCall: wirePhysicalCallEvidence{
			OperationID:          dispatch.OperationID,
			AttemptID:            dispatch.AttemptID,
			AccountIdentity:      "credential:" + dispatch.CredentialRef,
			RequestedDestination: brain.Endpoint,
			ResolvedDestination:  brain.Endpoint,
			ProfileDigest:        a.profile.Digest,
			CapabilityEvidence:   a.profile.CapabilityEvidence.Artifact,
			StartedAt:            now,
			FinishedAt:           now,
			RequestContext:       requestContextRef(a.profile),
			RequestSent:          "no",
			Confirmation:         "unknown",
			ErrorCode:            gapCommandStatusLookup,
			ErrorMessage:         "the pinned upstream serves no command status lookup; the original outcome stays unknown",
		},
		Kind:                act.Kind,
		BrainID:             act.BrainID,
		AdapterCommandID:    act.AdapterCommandID,
		CommandStatus:       "unknown",
		Claims:              []json.RawMessage{},
		BrainRevisions:      []json.RawMessage{},
		Usage:               usage,
		StagedOutputs:       []json.RawMessage{},
		OutputArtifacts:     []wireArtifactRef{},
		WriterOwner:         act.WriterOwner,
		LookupAuthoritative: &authoritative,
	}
	doc, err := json.Marshal(ev)
	if err != nil {
		return builtEvidence{}, internalError("encoding serenity evidence failed: %v", err)
	}
	schema, err := evidenceSchema()
	if err != nil {
		return builtEvidence{}, internalError("serenity evidence schema composition failed: %v", err)
	}
	if err := contract.ValidateSchema(schema, doc); err != nil {
		return builtEvidence{}, internalError("serenity evidence does not match the zatiti.serenity.evidence/v1 schema: %v", err)
	}
	return builtEvidence{doc: doc, usage: usageDoc}, nil
}
