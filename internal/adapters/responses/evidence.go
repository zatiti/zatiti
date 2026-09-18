package responses

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// Finish reasons of the frozen ModelOutput schema.
var finishReasons = map[string]bool{
	"completed": true, "tool_calls": true, "length_limit": true, "refused": true,
	"interrupted": true, "failed": true, "unknown": true,
}

const finishUnknown = "unknown"

// Schema bounds this package must respect when turning provider data into
// evidence fields.
const (
	maxReferenceChars = 1024
	maxRefusalChars   = 8192
	maxMessageChars   = 2048
	maxErrorCodeChars = 128
	maxOutputItems    = 256
	textMediaType     = "text/plain; charset=utf-8"
)

// requestContextRef returns the ArtifactRef both PhysicalCallEvidence and
// ModelOutput carry as request_context. Unlike a sibling adapter that
// builds its request from scalar action fields, this adapter is handed a
// real one: the action's context_artifact is the pre-dispatch persisted
// model-visible request context, which is exactly what request_context
// "identifies". The adapter additionally stages the literal upstream
// request body as a StagedOutput of purpose "context", because it cannot
// mint an artifact ID for those bytes itself.
func requestContextRef(act *wireResponsesParameters) wireArtifactRef {
	return act.ContextArtifact
}

// stageBytes stages data and describes it as a StagedOutput.
func stageBytes(ctx context.Context, blobs contract.BlobStore, data []byte, mediaType, classification, purpose string) (wireStagedOutput, error) {
	stagingRef, digest, size, err := blobs.Stage(ctx, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return wireStagedOutput{}, blobFault(err)
	}
	return wireStagedOutput{
		StagingRef:     stagingRef,
		Digest:         digest,
		Size:           size,
		MediaType:      mediaType,
		Classification: classification,
		Purpose:        purpose,
	}, nil
}

// usageInput is what is known about one attempt's charge.
type usageInput struct {
	Bounds         admittedBounds
	Sent           bool                // false only when the request provably never left
	Reported       *protocolTokenUsage // the provider's own usage report, if any
	NoCharge       bool                // the qualified contract establishes no charge
	UsageReference string
}

// usageFor builds the ProviderUsage for one attempt. The profile's rates
// are always carried so a reader sees the exact prices an amount was
// computed under; token counts appear only when the provider reported them.
func (p *responsesProfile) usageFor(in usageInput) wireProviderUsage {
	inRate, outRate := p.InputRate, p.OutputRate
	usage := wireProviderUsage{
		Accounting:             wireUsage{Currency: p.Currency, Advisory: in.Bounds.Advisory},
		InputRate:              &inRate,
		OutputRate:             &outRate,
		ProviderUsageReference: in.UsageReference,
	}
	switch {
	case !in.Sent || in.NoCharge:
		usage.Billing = "no_charge"
		usage.Accounting.Advisory = false
	case in.Reported != nil:
		spent, err := p.costOf(in.Reported.InputTokens, in.Reported.OutputTokens)
		if err != nil {
			// The provider reported tokens whose charge cannot be
			// represented. The tokens are still recorded exactly; the
			// amount stays unknown rather than being clamped.
			usage.Billing = "unknown"
			usage.Accounting.Unknown = in.Bounds.WorstCase
		} else {
			usage.Billing = "observed"
			usage.Accounting.Spent = spent
		}
		inTok, outTok := in.Reported.InputTokens, in.Reported.OutputTokens
		usage.InputTokens = &inTok
		usage.OutputTokens = &outTok
	default:
		// Bytes may have left and the provider reported no usage: the
		// charge is not zero and is not estimated. The whole admitted
		// worst case stays outstanding as an unknown amount.
		usage.Billing = "unknown"
		if in.Bounds.Advisory {
			usage.Billing = "advisory"
		}
		usage.Accounting.Unknown = in.Bounds.WorstCase
	}
	return usage
}

type builtEvidence struct {
	doc   json.RawMessage
	usage json.RawMessage
}

// buildEvidence assembles and marshals the zatiti.responses.evidence/v1
// document, plus a standalone marshal of its usage for Observation.Usage.
func buildEvidence(physical wirePhysicalCallEvidence, output wireModelOutput, staged []wireStagedOutput) (builtEvidence, error) {
	usageDoc, err := json.Marshal(output.Usage)
	if err != nil {
		return builtEvidence{}, internalError("encoding responses usage failed: %v", err)
	}
	if output.TextOutputs == nil {
		output.TextOutputs = []wireStagedLocator{}
	}
	if output.ToolProposals == nil {
		output.ToolProposals = []wireModelToolProposal{}
	}
	if staged == nil {
		staged = []wireStagedOutput{}
	}
	ev := wireResponsesEvidence{
		Schema:          "zatiti.responses.evidence/v1",
		PhysicalCall:    physical,
		ResponseID:      output.ResponseID,
		Output:          output,
		StagedOutputs:   staged,
		OutputArtifacts: []wireArtifactRef{}, // the adapter cannot mint artifact IDs; the controller publishes staged outputs
	}
	doc, err := json.Marshal(ev)
	if err != nil {
		return builtEvidence{}, internalError("encoding responses evidence failed: %v", err)
	}
	return builtEvidence{doc: doc, usage: usageDoc}, nil
}
