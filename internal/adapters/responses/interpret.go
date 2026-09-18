package responses

import (
	"context"
	"fmt"
	"net/http"

	"github.com/zatiti/zatiti/internal/contract"
)

// interpretation turns one received HTTP response into a disposition,
// confirmation, model output and usage. It carries what every branch
// needs: the credential to scrub, the bounds the call was admitted under,
// and the classification staged outputs inherit from the request context.
type interpretation struct {
	secret         []byte
	bounds         admittedBounds
	classification string
	stageErr       error // staging the raw provider response failed
	disposition    string
}

// unknown records a response that was received but cannot be classified.
// The model step may have run and been billed, so the outcome stays
// unknown and the admitted worst case stays outstanding.
func (in *interpretation) unknown(physical *wirePhysicalCallEvidence, output *wireModelOutput, profile *responsesProfile, code, message string) {
	in.disposition = contract.DispositionUnknown
	physical.Confirmation = "unknown"
	physical.ErrorCode = code
	physical.ErrorMessage = sanitizeText(in.secret, message, maxMessageChars)
	output.FinishReason = finishUnknown
	output.Usage = profile.usageFor(usageInput{Bounds: in.bounds, Sent: true})
}

// interpret classifies a fully read, in-bounds response. It returns staged
// extended with any model text it staged.
//
// Status classes follow HTTP semantics, not any vendor's: a 3xx is a
// redirect this adapter never follows and a 4xx is the server refusing the
// request, both authoritative failures of this attempt; a 5xx is the
// server (or an intermediary) failing on an apparently valid request,
// which cannot rule out that the model step ran, so it stays unknown. Only
// a decoded 2xx can succeed.
func (in *interpretation) interpret(ctx context.Context, a *Adapter, resp *http.Response, result protocolResult, decodeErr error, physical *wirePhysicalCallEvidence, output *wireModelOutput, staged []wireStagedOutput) []wireStagedOutput {
	status := resp.StatusCode
	is2xx := status >= 200 && status < 300
	if decodeErr == nil {
		decodeErr = validateResult(result, is2xx)
	}

	if !is2xx {
		// A failure body is interpreted best-effort: it can add a
		// message, reported usage or a qualified no-charge fact, but it
		// cannot turn a non-2xx into anything better.
		var usage usageInput
		if decodeErr == nil {
			usage = usageInput{Reported: result.Usage, NoCharge: result.NoCharge, UsageReference: sanitizeText(in.secret, result.UsageReference, maxReferenceChars)}
			output.ResponseID = scrubSecret(in.secret, result.ResponseID)
			physical.ErrorMessage = sanitizeText(in.secret, result.ErrorMessage, maxMessageChars)
		}
		usage.Bounds, usage.Sent = in.bounds, true
		output.Usage = a.profile.usageFor(usage)
		physical.ProviderReference = output.ResponseID
		physical.ErrorCode = fmt.Sprintf("http_%d", status)
		switch {
		case status >= 500:
			in.disposition = contract.DispositionUnknown
			physical.Confirmation = "unknown"
		case status >= 300 && status < 400:
			in.disposition = contract.DispositionFailed
			physical.Confirmation = "authoritative_failure"
			physical.ErrorCode = "redirect_not_permitted"
			physical.ErrorMessage = "redirects are never followed; a different destination requires a profile that declares it"
			output.FinishReason = "failed"
		default:
			in.disposition = contract.DispositionFailed
			physical.Confirmation = "authoritative_failure"
			output.FinishReason = "failed"
		}
		return staged
	}

	if decodeErr != nil {
		in.unknown(physical, output, a.profile, "response_undecodable", decodeErr.Error())
		return staged
	}

	output.ResponseID = scrubSecret(in.secret, result.ResponseID)
	physical.ProviderReference = output.ResponseID
	output.Usage = a.profile.usageFor(usageInput{
		Bounds: in.bounds, Sent: true, Reported: result.Usage, NoCharge: result.NoCharge,
		UsageReference: sanitizeText(in.secret, result.UsageReference, maxReferenceChars),
	})

	switch result.State {
	case stateAccepted:
		// Provider acceptance is not completion and is never promoted to it.
		in.disposition = contract.DispositionAccepted
		physical.Confirmation = "provider_accepted"
		output.FinishReason = finishUnknown
		return staged
	case stateFailed:
		in.disposition = contract.DispositionFailed
		physical.Confirmation = "authoritative_failure"
		physical.ErrorCode = sanitizeText(in.secret, result.ErrorCode, maxErrorCodeChars)
		physical.ErrorMessage = sanitizeText(in.secret, result.ErrorMessage, maxMessageChars)
		output.FinishReason = "failed"
		return staged
	}

	in.disposition = contract.DispositionSucceeded
	physical.Confirmation = "authoritative_success"
	output.FinishReason = result.FinishReason
	output.Refusal = sanitizeText(in.secret, result.Refusal, maxRefusalChars)
	output.ContinuationReference = scrubSecret(in.secret, result.ContinuationReference)

	// Adapter-side problems after an authoritative provider success are
	// flagged on the physical call without rewriting what the provider
	// did: the step ran and its usage is final.
	if in.stageErr != nil {
		physical.ErrorCode = "provider_response_staging_failed"
		physical.ErrorMessage = "the raw provider response could not be staged as evidence"
	}
	for _, text := range result.Texts {
		stagedText, err := stageBytes(ctx, a.deps.Blobs, []byte(scrubSecret(in.secret, text)), textMediaType, in.classification, "model_text")
		if err != nil {
			physical.ErrorCode = "model_text_staging_failed"
			physical.ErrorMessage = "one or more model text outputs could not be staged and are absent from text_outputs"
			continue
		}
		staged = append(staged, stagedText)
		output.TextOutputs = append(output.TextOutputs, wireStagedLocator{Kind: "staged", StagingRef: stagedText.StagingRef, Digest: stagedText.Digest})
	}
	if n := len(result.ToolCalls); n > 0 {
		// KNOWN CONTRACT GAP: a ModelToolProposal requires operation_id
		// and operation_version ("the qualified mapping for that tool"),
		// but neither the action nor ContextToolDefinition carries that
		// mapping, so this package has no honest source for them. The
		// calls are not dropped -- they remain verbatim in the staged
		// provider_response -- but no typed proposal is fabricated.
		physical.ErrorCode = "tool_proposal_mapping_unspecified"
		physical.ErrorMessage = fmt.Sprintf("the model returned %d tool call(s); the frozen contract provides no tool-to-operation mapping, so no typed proposal was produced (raw calls are retained in the staged provider_response)", n)
	}
	return staged
}

// validateResult checks a decoded result against the bounds of the frozen
// evidence schema. A result outside them cannot be recorded faithfully, so
// it is treated as undecodable rather than truncated into something the
// provider never said. A response state is required only of a 2xx: a
// non-2xx is classified by its status, whatever its body claims.
func validateResult(r protocolResult, requireState bool) error {
	if requireState {
		switch r.State {
		case stateCompleted, stateFailed, stateAccepted:
		default:
			return fmt.Errorf("wire protocol reported unknown response state %q", r.State)
		}
		if r.State == stateCompleted && !finishReasons[r.FinishReason] {
			return fmt.Errorf("wire protocol reported unknown finish reason %q", r.FinishReason)
		}
	}
	if len(r.ResponseID) > maxReferenceChars || len(r.ContinuationReference) > maxReferenceChars {
		return fmt.Errorf("provider reference exceeds %d bytes", maxReferenceChars)
	}
	if len(r.Texts) > maxOutputItems || len(r.ToolCalls) > maxOutputItems {
		return fmt.Errorf("response carries more than %d outputs of one kind", maxOutputItems)
	}
	if r.Usage != nil && (r.Usage.InputTokens < 0 || r.Usage.OutputTokens < 0) {
		return fmt.Errorf("provider reported negative token usage")
	}
	return nil
}
