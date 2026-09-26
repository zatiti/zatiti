package responses

import (
	"context"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// interpretation turns one received HTTP response into a disposition,
// confirmation, model output and usage. It carries what every branch
// needs: the credential to scrub, the bounds the call was admitted under,
// and the classification staged outputs inherit from the request context.
type interpretation struct {
	secret         []byte
	bounds         admittedBounds
	inputBound     *int64 // the input token bound the step was admitted under, if any
	classification string
	stageErr       error // staging the raw provider response failed
	reconcile      bool  // the response answers a reconciliation lookup, not the step itself
	disposition    string
}

// unknown records a response that was received but cannot be classified.
// The model step may have run and been billed, so the outcome stays
// unknown and the admitted worst case stays outstanding.
func (in *interpretation) unknown(physical *wirePhysicalCallEvidence, output *wireModelOutput, profile *responsesProfile, code, message string) {
	in.disposition = contract.DispositionUnknown
	physical.Confirmation = "unknown"
	physical.ErrorCode = sanitizeText(in.secret, code, maxErrorCodeChars)
	physical.ErrorMessage = sanitizeText(in.secret, message, maxMessageChars)
	output.FinishReason = finishUnknown
	meta := usageInput{Bounds: in.bounds, Sent: true}
	if profile.Version == "zatiti.responses/v2" {
		meta.RequestedModel, meta.ServingProvider = profile.Model, profile.Provider
	}
	output.Usage = profile.usageFor(meta)
}

// interpret classifies a fully read, in-bounds response. It returns staged
// extended with any model text it staged.
//
// For the model step, status classes follow HTTP semantics, not any
// vendor's: a 3xx is a redirect this adapter never follows and a 4xx is
// the server refusing the request, both authoritative failures of this
// attempt; a 5xx is the server (or an intermediary) failing on an
// apparently valid request, which cannot rule out that the model step ran,
// so it stays unknown. Only a decoded 2xx can succeed.
//
// For a reconciliation lookup, any status but a decoded 2xx resolves
// nothing: the lookup failing (a 404 included) never proves the step did
// not execute.
func (in *interpretation) interpret(ctx context.Context, a *Adapter, status int, result protocolResult, decodeErr error, physical *wirePhysicalCallEvidence, output *wireModelOutput, staged []wireStagedOutput) []wireStagedOutput {
	is2xx := status >= 200 && status < 300
	if decodeErr == nil {
		decodeErr = validateResult(result, is2xx)
	}
	if a.profile.Version == "zatiti.responses/v2" {
		if result.RequestedModel == "" {
			result.RequestedModel = a.profile.Model
		}
		if result.ServingProvider == "" {
			result.ServingProvider = a.profile.Provider
		}
	}

	if !is2xx {
		// A failure body is interpreted best-effort: it can add a
		// message, reported usage or a qualified no-charge fact, but it
		// cannot turn a non-2xx into anything better.
		var usage usageInput
		if decodeErr == nil {
			usage = usageInput{Reported: result.Usage, Unpriceable: result.UsageUnpriceable != "", NoCharge: result.NoCharge, UsageReference: sanitizeText(in.secret, result.UsageReference, maxReferenceChars), RequestedModel: sanitizeText(in.secret, result.RequestedModel, 256), ServedModel: sanitizeText(in.secret, result.ServedModel, 256), ServingProvider: result.ServingProvider, ProviderRequestID: sanitizeText(in.secret, result.ProviderRequestID, 256), SourceCostDecimal: result.SourceCostDecimal, SourceCostCurrency: result.SourceCostCurrency, SourceCostKind: result.SourceCostKind}
			output.ResponseID = scrubSecret(in.secret, result.ResponseID)
			physical.ErrorMessage = sanitizeText(in.secret, result.ErrorMessage, maxMessageChars)
		}
		usage.Bounds, usage.Sent = in.bounds, true
		output.Usage = a.profile.usageFor(usage)
		physical.ProviderReference = output.ResponseID
		physical.ErrorCode = fmt.Sprintf("http_%d", status)
		switch {
		case in.reconcile || status >= 500:
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
		Bounds: in.bounds, Sent: true, Reported: result.Usage, Unpriceable: result.UsageUnpriceable != "", NoCharge: result.NoCharge,
		UsageReference:     sanitizeText(in.secret, result.UsageReference, maxReferenceChars),
		RequestedModel:     sanitizeText(in.secret, result.RequestedModel, 256),
		ServedModel:        sanitizeText(in.secret, result.ServedModel, 256),
		ServingProvider:    result.ServingProvider,
		ProviderRequestID:  sanitizeText(in.secret, result.ProviderRequestID, 256),
		SourceCostDecimal:  result.SourceCostDecimal,
		SourceCostCurrency: result.SourceCostCurrency,
		SourceCostKind:     result.SourceCostKind,
	})

	switch result.State {
	case stateUnresolved:
		// The documented lookup found no evidence either way.
		in.disposition = contract.DispositionUnknown
		physical.Confirmation = "unknown"
		physical.ErrorCode = "reconcile_unresolved"
		physical.ErrorMessage = "the authoritative lookup shows no completed output yet; the outcome remains unknown"
		output.FinishReason = finishUnknown
		return staged
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

	// Adapter-side findings after an authoritative provider success are
	// flagged on the physical call without rewriting what the provider
	// did: the step ran and its usage is final. The first finding wins.
	flag := func(code, message string) {
		if physical.ErrorCode == "" {
			physical.ErrorCode = sanitizeText(in.secret, code, maxErrorCodeChars)
			physical.ErrorMessage = sanitizeText(in.secret, message, maxMessageChars)
		}
	}
	if result.ErrorCode != "" {
		flag(result.ErrorCode, result.ErrorMessage)
	}
	if result.UsageUnpriceable != "" {
		flag("usage_unpriceable", result.UsageUnpriceable)
	}
	if result.Usage != nil && in.inputBound != nil && result.Usage.InputTokens > *in.inputBound {
		flag("input_token_bound_exceeded", fmt.Sprintf("the provider reported %d input tokens, above the %d-token bound the step was admitted under; the bound's qualification is unsound", result.Usage.InputTokens, *in.inputBound))
	}
	if in.stageErr != nil {
		flag("provider_response_staging_failed", "the raw provider response could not be staged as evidence")
	}
	for _, text := range result.Texts {
		stagedText, err := stageBytes(ctx, a.deps.Blobs, []byte(scrubSecret(in.secret, text)), textMediaType, in.classification, "model_text")
		if err != nil {
			flag("model_text_staging_failed", "one or more model text outputs could not be staged and are absent from text_outputs")
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
		flag("tool_proposal_mapping_unspecified", fmt.Sprintf("the model returned %d tool call(s); the frozen contract provides no tool-to-operation mapping, so no typed proposal was produced (raw calls are retained in the staged provider_response)", n))
	}
	return staged
}

// interpretPrepareSession classifies one prepare_session physical outcome
// and reports its disposition and, only on authoritative success, the
// minted session handle. It never charges anything: session creation is
// never billed by the pinned protocol (PROTOCOL.md's accounting section),
// which the caller (invokePrepareSession) accounts for, not this function.
//
// A prepare_session whose response is lost stays unknown, exactly like a
// model_step: "the caller must not create a second session on an
// unconfirmed prepare_session outcome" (AGENTS.md, P00-009). Status
// classification mirors interpretation.interpret: a decoded 2xx can
// succeed, a 5xx or an undecodable 2xx body cannot rule out that the
// provider created the session and stays unknown, and any other status is
// this attempt's authoritative failure.
func interpretPrepareSession(protocol wireProtocol, secret []byte, po *physicalOutcome, physical *wirePhysicalCallEvidence) (disposition, handle string) {
	if reason, message := po.failure(); reason != "" {
		if po.doErr != nil {
			var confirmation string
			physical.RequestSent, disposition, confirmation = classifyNetworkError(po.doErr)
			physical.Confirmation = confirmation
			physical.ErrorCode = "transport_error"
		} else {
			disposition = contract.DispositionUnknown
			physical.Confirmation = "unknown"
			physical.ErrorCode = sanitizeText(secret, reason, maxErrorCodeChars)
		}
		physical.ErrorMessage = sanitizeText(secret, message, maxMessageChars)
		return disposition, ""
	}

	is2xx := po.status >= 200 && po.status < 300
	h, decodeErr := protocol.decodePrepare(po.status, po.header, po.body)
	switch {
	case decodeErr == nil && is2xx:
		physical.Confirmation = "authoritative_success"
		return contract.DispositionSucceeded, sanitizeText(secret, h, maxReferenceChars)
	case is2xx:
		// A 2xx status this protocol cannot decode into a handle: bytes
		// left and the provider reported success, so the session may
		// exist even though its reply cannot be trusted as the handle.
		physical.Confirmation = "unknown"
		physical.ErrorCode = "response_undecodable"
		physical.ErrorMessage = sanitizeText(secret, decodeErr.Error(), maxMessageChars)
		return contract.DispositionUnknown, ""
	case po.status >= 500:
		physical.Confirmation = "unknown"
		physical.ErrorCode = fmt.Sprintf("http_%d", po.status)
		physical.ErrorMessage = sanitizeText(secret, decodeErr.Error(), maxMessageChars)
		return contract.DispositionUnknown, ""
	default:
		physical.Confirmation = "authoritative_failure"
		physical.ErrorCode = fmt.Sprintf("http_%d", po.status)
		physical.ErrorMessage = sanitizeText(secret, decodeErr.Error(), maxMessageChars)
		return contract.DispositionFailed, ""
	}
}

// validateResult checks a decoded result against the bounds of the frozen
// evidence schema. A result outside them cannot be recorded faithfully, so
// it is treated as undecodable rather than truncated into something the
// provider never said. A response state is required only of a 2xx: a
// non-2xx is classified by its status, whatever its body claims.
func validateResult(r protocolResult, requireState bool) error {
	if requireState {
		switch r.State {
		case stateCompleted, stateFailed, stateAccepted, stateUnresolved:
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
	if len(r.Texts) > maxTextOutputs {
		return fmt.Errorf("response carries more than %d text outputs", maxTextOutputs)
	}
	if len(r.ToolCalls) > maxToolCalls {
		return fmt.Errorf("response carries more than %d tool calls", maxToolCalls)
	}
	if r.Usage != nil && (r.Usage.InputTokens < 0 || r.Usage.OutputTokens < 0) {
		return fmt.Errorf("provider reported negative token usage")
	}
	return nil
}
