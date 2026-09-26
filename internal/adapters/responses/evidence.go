package responses

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"slices"
	"strings"

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
	maxToolCalls      = 256
	// maxTextOutputs keeps staged_outputs within its 256-item bound: every
	// observation also stages the request context and the raw provider
	// response.
	maxTextOutputs = 254
	textMediaType  = "text/plain; charset=utf-8"
)

// requestRecordSchema names the request record document below.
const requestRecordSchema = "zatiti.adapter.request-record/v1"

// requestRecordHeader is one permitted request header, in sorted order.
type requestRecordHeader struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// requestRecord is the exact secret-free record of the one physical request
// an attempt makes: method, destination, the permitted headers and the body
// as sent. The credential the wire protocol adds at send time is never part
// of it. The body is base64 so the record is exact for any upstream
// encoding; the model identifier and output ceiling the translation adds
// beyond the persisted context are inside that body.
type requestRecord struct {
	Schema      string                `json:"schema"`
	Method      string                `json:"method"`
	Destination string                `json:"destination"`
	Headers     []requestRecordHeader `json:"headers"`
	BodyDigest  contract.Digest       `json:"body_digest"`
	BodySize    int64                 `json:"body_size"`
	BodyBase64  string                `json:"body_base64"`
}

// stageRequestContext stages the request record before any byte is sent
// and returns the StagedOutput (purpose context) plus the staged
// ArtifactLocator that names it as request_context.
//
// The action's context_artifact is NOT passed through as kind artifact:
// that is allowed only when the translated provider request adds nothing
// model-visible beyond the persisted Action, and this translation adds the
// profile's model identifier and is shaped by a wire protocol, so the
// context artifact alone is not the complete request record.
//
// A record that contains the credential is refused rather than staged: the
// wire protocol contract makes encode secret-free, and this is the check
// that holds it to that.
func stageRequestContext(ctx context.Context, blobs contract.BlobStore, secret []byte, call protocolCall, classification string) (wireStagedOutput, wireStagedLocator, error) {
	if blobs == nil {
		return wireStagedOutput{}, wireStagedLocator{}, prerequisiteMissing("responses adapter requires a blob store dependency to stage the request context before sending")
	}
	record := requestRecord{
		Schema:      requestRecordSchema,
		Method:      call.Method,
		Destination: call.Destination,
		Headers:     make([]requestRecordHeader, 0, len(call.Header)),
		BodyDigest:  contract.Hash(call.Body),
		BodySize:    int64(len(call.Body)),
		BodyBase64:  base64.StdEncoding.EncodeToString(call.Body),
	}
	for name, values := range call.Header {
		record.Headers = append(record.Headers, requestRecordHeader{Name: name, Values: values})
	}
	slices.SortFunc(record.Headers, func(a, b requestRecordHeader) int { return strings.Compare(a.Name, b.Name) })
	doc, err := json.Marshal(record)
	if err != nil {
		return wireStagedOutput{}, wireStagedLocator{}, internalError("encoding the request record failed")
	}
	if len(secret) > 0 && (bytes.Contains(doc, secret) || bytes.Contains(call.Body, secret)) {
		return wireStagedOutput{}, wireStagedLocator{}, internalError("the wire protocol placed credential material in the request record; nothing was staged or sent")
	}
	staged, err := stageBytes(ctx, blobs, doc, "application/json", classification, "context")
	if err != nil {
		return wireStagedOutput{}, wireStagedLocator{}, err
	}
	return staged, wireStagedLocator{Kind: "staged", StagingRef: staged.StagingRef, Digest: staged.Digest}, nil
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
	Bounds             admittedBounds
	Sent               bool                // false only when the request provably never left
	Reported           *protocolTokenUsage // the provider's own usage report, if any
	Unpriceable        bool                // the reported tokens carry a price the profile's rates do not cover
	NoCharge           bool                // the qualified contract establishes no charge
	UsageReference     string
	RequestedModel     string
	ServedModel        string
	ServingProvider    string
	ProviderRequestID  string
	SourceCostDecimal  string
	SourceCostCurrency string
	SourceCostKind     string
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
		RequestedModel:         in.RequestedModel,
		ServedModel:            in.ServedModel,
		ServingProvider:        in.ServingProvider,
		ProviderRequestID:      in.ProviderRequestID,
		SourceCostDecimal:      in.SourceCostDecimal,
		SourceCostCurrency:     in.SourceCostCurrency,
		SourceCostKind:         in.SourceCostKind,
	}
	switch {
	case !in.Sent || in.NoCharge:
		usage.Billing = "no_charge"
		usage.Accounting.Advisory = false
	case in.Reported != nil:
		if p.Provider == "experiential" {
			usage.Billing = "advisory"
			usage.Accounting.Advisory = true
			usage.Accounting.Unknown = in.Bounds.WorstCase
			inTok, outTok := in.Reported.InputTokens, in.Reported.OutputTokens
			usage.InputTokens, usage.OutputTokens = &inTok, &outTok
			return usage
		}
		if in.SourceCostKind != "" {
			if in.SourceCostKind == "provider_billed" && in.SourceCostCurrency == p.Currency {
				if cost, ok := decimalMicroCeiling(in.SourceCostDecimal); ok {
					usage.Billing = "observed"
					usage.Accounting.Spent = cost
					inTok, outTok := in.Reported.InputTokens, in.Reported.OutputTokens
					usage.InputTokens, usage.OutputTokens = &inTok, &outTok
					return usage
				}
			}
			usage.Billing = "unknown"
			usage.Accounting.Unknown = in.Bounds.WorstCase
			inTok, outTok := in.Reported.InputTokens, in.Reported.OutputTokens
			usage.InputTokens, usage.OutputTokens = &inTok, &outTok
			return usage
		}
		spent, err := p.costOf(in.Reported.InputTokens, in.Reported.OutputTokens)
		if in.Unpriceable {
			err = errAmountOverflow // priced outside the profile's rates: the amount is not known
		}
		if err != nil {
			// The provider reported tokens whose charge cannot be
			// represented. The tokens are still recorded exactly; the
			// amount stays unknown rather than being clamped.
			usage.Billing = "unknown"
			usage.Accounting.Unknown = in.Bounds.WorstCase
		} else {
			if p.Provider == "openrouter" {
				usage.Billing = "bounded_estimate"
				usage.Accounting.Estimated = spent
			} else {
				usage.Billing = "observed"
				usage.Accounting.Spent = spent
			}
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

// decimalMicroCeiling converts a nonnegative decimal currency amount to
// integer micro-units without float arithmetic. A fractional micro-unit is
// rounded upward so observed charges are never understated.
func decimalMicroCeiling(s string) (int64, bool) {
	if s == "" || len(s) > 64 {
		return 0, false
	}
	parts := strings.Split(s, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && r != '.' {
			return 0, false
		}
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > 18 || (len(parts) == 2 && fraction == "") {
		return 0, false
	}
	n := new(big.Int)
	if _, ok := n.SetString(parts[0], 10); !ok {
		return 0, false
	}
	n.Mul(n, big.NewInt(1_000_000))
	if fraction != "" {
		f, ok := new(big.Int).SetString(fraction, 10)
		if !ok {
			return 0, false
		}
		den := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(len(fraction))), nil)
		n.Add(n, new(big.Int).Quo(new(big.Int).Mul(f, big.NewInt(1_000_000)), den))
		rem := new(big.Int).Rem(new(big.Int).Mul(f, big.NewInt(1_000_000)), den)
		if rem.Sign() > 0 {
			n.Add(n, big.NewInt(1))
		}
	}
	if !n.IsInt64() {
		return 0, false
	}
	return n.Int64(), true
}

type builtEvidence struct {
	doc   json.RawMessage
	usage json.RawMessage
}

// buildModelStepEvidence assembles and marshals the
// zatiti.responses.evidence/v1 document for a model_step attempt (Invoke or
// Reconcile), plus a standalone marshal of its usage for Observation.Usage.
// sessionHandle is the action's own session_handle, echoed back: it is
// known before any call is dispatched and does not depend on this
// attempt's outcome.
func buildModelStepEvidence(sessionHandle string, physical wirePhysicalCallEvidence, output wireModelOutput, staged []wireStagedOutput) (builtEvidence, error) {
	usageDoc, err := json.Marshal(output.Usage.Accounting)
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
		SessionHandle:   sessionHandle,
		ResponseID:      output.ResponseID,
		Output:          &output,
		StagedOutputs:   staged,
		OutputArtifacts: []wireArtifactRef{}, // the adapter cannot mint artifact IDs; the controller publishes staged outputs
	}
	doc, err := json.Marshal(ev)
	if err != nil {
		return builtEvidence{}, internalError("encoding responses evidence failed: %v", err)
	}
	return builtEvidence{doc: doc, usage: usageDoc}, nil
}

func buildModelStepEvidenceV2(sessionMode, sessionID, sessionHandle, kind string, physical wirePhysicalCallEvidence, output wireModelOutput, staged []wireStagedOutput, qualification *wireQualificationMetadataV2) (builtEvidence, error) {
	usageDoc, err := json.Marshal(output.Usage.Accounting)
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
	ev := wireResponsesEvidenceV2{Schema: "zatiti.responses.evidence/v2", Kind: kind, Qualification: qualification, PhysicalCall: physical, SessionHandle: sessionHandle, ResponseID: output.ResponseID, Output: &output, StagedOutputs: staged, OutputArtifacts: []wireArtifactRef{}, SessionMode: sessionMode, SessionID: sessionID}
	doc, err := json.Marshal(ev)
	if err != nil {
		return builtEvidence{}, internalError("encoding responses v2 evidence failed: %v", err)
	}
	return builtEvidence{doc: doc, usage: usageDoc}, nil
}

func buildPrepareSessionEvidenceV2(sessionID, handle string, physical wirePhysicalCallEvidence, usage wireProviderUsage, staged []wireStagedOutput) (builtEvidence, error) {
	usageDoc, err := json.Marshal(usage.Accounting)
	if err != nil {
		return builtEvidence{}, internalError("encoding responses usage failed: %v", err)
	}
	if staged == nil {
		staged = []wireStagedOutput{}
	}
	ev := wireResponsesEvidenceV2{Schema: "zatiti.responses.evidence/v2", PhysicalCall: physical, SessionHandle: handle, StagedOutputs: staged, OutputArtifacts: []wireArtifactRef{}, SessionMode: "provider_conversation", SessionID: sessionID}
	doc, err := json.Marshal(ev)
	if err != nil {
		return builtEvidence{}, internalError("encoding Responses v2 preparation evidence failed: %v", err)
	}
	return builtEvidence{doc: doc, usage: usageDoc}, nil
}

// buildPrepareSessionEvidence assembles and marshals the
// zatiti.responses.evidence/v1 document for a prepare_session attempt: no
// model output, "no model-visible content and no context_artifact"
// (AGENTS.md, P00-009) -- response_id/output/output_artifacts stay entirely
// absent, never a fabricated zero value.
//
// sessionHandle is the handle this attempt minted, only on authoritative
// success. On every other disposition (unknown, failed, not_sent) there is
// none to report, and the frozen ResponsesEvidence.session_handle is
// nonetheless required with minLength 1: this is a known, reported gap
// (see PROTOCOL.md "Where the frozen contract cannot be honoured" and the
// P13 PR) the adapter cannot close by fabricating a provider handle. An
// empty string is the honest value; it does not satisfy the frozen
// schema's minLength, which is the gap being reported, not a local
// workaround.
func buildPrepareSessionEvidence(sessionHandle string, physical wirePhysicalCallEvidence, usage wireProviderUsage, staged []wireStagedOutput) (builtEvidence, error) {
	usageDoc, err := json.Marshal(usage.Accounting)
	if err != nil {
		return builtEvidence{}, internalError("encoding responses usage failed: %v", err)
	}
	if staged == nil {
		staged = []wireStagedOutput{}
	}
	ev := wireResponsesEvidence{
		Schema:        "zatiti.responses.evidence/v1",
		PhysicalCall:  physical,
		SessionHandle: sessionHandle,
		StagedOutputs: staged,
	}
	doc, err := json.Marshal(ev)
	if err != nil {
		return builtEvidence{}, internalError("encoding responses evidence failed: %v", err)
	}
	return builtEvidence{doc: doc, usage: usageDoc}, nil
}
