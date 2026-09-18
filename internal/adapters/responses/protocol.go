package responses

import (
	"encoding/json"
	"net/http"
)

// wireProtocol is the seam between this package's frozen Zatiti-side
// contract and one pinned, separately qualified upstream wire shape. An
// implementation is the ONLY place that may know an upstream's HTTP method,
// credential placement, request body or response body. Everything else in
// this package -- bounds, accounting, evidence, transport discipline -- is
// protocol-independent.
//
// An implementation is selected by the exact
// capability_evidence.protocol_revision string of the profile. It must be
// pure: no I/O, no clock, no retained state between calls.
type wireProtocol interface {
	// limits states what the qualified upstream contract lets this
	// protocol guarantee. A capability that was not established by
	// qualification is reported false, never inferred.
	limits() protocolLimits
	// encode translates one model step into the exact upstream request.
	// The returned header and body are secret-free; the credential is
	// added separately by authorize.
	encode(in protocolRequest) (protocolCall, error)
	// authorize places the resolved credential on the outgoing request
	// headers. It is the only code that touches the raw secret.
	authorize(h http.Header, secret []byte)
	// decode interprets one complete, bounded upstream response. It is
	// called for every HTTP status. An error means the body cannot be
	// interpreted under the qualified contract.
	decode(status int, header http.Header, body []byte) (protocolResult, error)
}

// protocolLimits are the enforcement guarantees a wire protocol provides.
type protocolLimits struct {
	// BoundsOutputTokens is true only when the encoded request makes the
	// provider enforce protocolRequest.MaxOutputTokens as a ceiling on
	// every billed output token.
	BoundsOutputTokens bool
	// SupportsContinuation is true only when the protocol can express
	// protocolRequest.ContinuationReference.
	SupportsContinuation bool
}

// protocolRequest is one model step in Zatiti terms. Every value comes
// from the profile, the action or the persisted context; none is defaulted.
type protocolRequest struct {
	Model                 string
	MaxOutputTokens       int64
	Context               *contextDocument
	ContinuationReference string
}

// protocolCall is one encoded upstream request.
type protocolCall struct {
	Method string
	Header http.Header
	Body   []byte
	// InputTokenBound is a qualified upper bound on the input tokens the
	// provider can bill for Body, or nil when the protocol cannot bound
	// them.
	InputTokenBound *int64
}

// Response states a protocol reports for a decoded 2xx response.
const (
	stateCompleted = "completed" // the model step finished; outputs are final
	stateFailed    = "failed"    // the provider reports the step failed
	stateAccepted  = "accepted"  // the provider accepted the step but has not finished it
)

// protocolTokenUsage is the provider's own reported token usage.
type protocolTokenUsage struct {
	InputTokens  int64
	OutputTokens int64
}

// protocolToolCall is one model tool call as the provider expressed it.
type protocolToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// protocolResult is one decoded upstream response in Zatiti terms.
type protocolResult struct {
	State                 string
	ResponseID            string
	FinishReason          string
	Texts                 []string
	ToolCalls             []protocolToolCall
	Usage                 *protocolTokenUsage // nil when the provider reported no usage
	UsageReference        string
	NoCharge              bool // true only when the qualified contract establishes this response is not billed
	Refusal               string
	ContinuationReference string
	ErrorCode             string
	ErrorMessage          string
}

// qualifiedProtocols returns the wire protocols this build has qualified,
// keyed by protocol revision.
//
// It is empty. The frozen specification for this package defines the
// Zatiti-side profile, action, context and evidence schemas but leaves the
// upstream wire shape to be pinned "during qualification", and the
// dependency lock records the hosted Responses endpoint as not resolved.
// Registering a protocol here from recollection of any vendor's API would
// be a guessed endpoint contract, so none is registered: every production
// Invoke refuses with capability_unsupported at the encode boundary until a
// revision is qualified against a real endpoint and added here.
func qualifiedProtocols() map[string]wireProtocol {
	return map[string]wireProtocol{}
}
