package responses

import (
	"encoding/json"
	"net/http"
)

// wireProtocol is the seam between this package's frozen Zatiti-side
// contract and one pinned, separately qualified upstream wire shape. An
// implementation is the ONLY place that may know an upstream's HTTP
// methods, paths, credential placement, request bodies or response bodies.
// Everything else in this package -- bounds, accounting, evidence,
// transport discipline -- is protocol-independent.
//
// An implementation is selected by the exact
// capability_evidence.protocol_revision string of the profile. It must be
// pure: no I/O, no clock, no retained state between calls. Every physical
// request it describes is sent by the adapter core under the same
// discipline (staged request record, no redirects, no retry, bounded
// response) and every response it decodes arrives as bounded bytes.
type wireProtocol interface {
	// limits states what the qualified upstream contract lets this
	// protocol guarantee. A capability that was not established by
	// qualification is reported false, never inferred.
	limits() protocolLimits
	// validateProfile rejects a profile the qualified contract cannot
	// honour (for example an input bound above a pricing tier break).
	validateProfile(p protocolProfile) error
	// inputTokenBound returns a qualified upper bound on the input tokens
	// the provider can bill for the model step, or nil when the protocol
	// cannot bound them. It does not depend on any preparatory handle.
	inputTokenBound(in protocolRequest) *int64
	// prepare returns the preparatory physical call the qualified contract
	// requires before the model step can be dispatched reconcilably, or
	// nil when none is required. Its response is decoded by decodePrepare
	// into the client-known handle the model step and any later
	// reconciliation are keyed by.
	prepare(in protocolRequest) (*protocolCall, error)
	decodePrepare(status int, header http.Header, body []byte) (string, error)
	// encode translates one model step into the exact upstream request.
	// The returned header and body are secret-free; the credential is
	// added separately by authorize.
	encode(in protocolRequest, handle string) (protocolCall, error)
	// authorize places the resolved credential on the outgoing request
	// headers. It is the only code that touches the raw secret.
	authorize(h http.Header, secret []byte)
	// decode interprets one complete, bounded upstream response to the
	// model step. It is called for every HTTP status. An error means the
	// body cannot be interpreted under the qualified contract.
	decode(status int, header http.Header, body []byte) (protocolResult, error)
	// reconcile returns the documented authoritative lookup for a model
	// step whose outcome is unknown, keyed by the handle prepare produced,
	// and decodeReconcile interprets its response. A result of state
	// stateUnresolved means the lookup found no evidence either way.
	reconcile(p protocolProfile, handle string) (protocolCall, error)
	decodeReconcile(status int, header http.Header, body []byte) (protocolResult, error)
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
	// MinOutputTokens is the smallest output ceiling the upstream accepts;
	// an action below it is refused before sending.
	MinOutputTokens int64
	// SupportsReconcile is true only when reconcile is a documented
	// authoritative lookup rather than a refusal.
	SupportsReconcile bool
}

// protocolProfile is the profile-level configuration a protocol sees: the
// destination, the model, the input ceiling and the qualified capability
// strings. Prices never reach the protocol; accounting is core-owned.
type protocolProfile struct {
	Endpoint       string
	Model          string
	MaxInputTokens int64
	Capabilities   []string
}

// protocolRequest is one model step in Zatiti terms. Every value comes
// from the profile, the action or the persisted context; none is defaulted.
type protocolRequest struct {
	Profile               protocolProfile
	OperationID           string
	AttemptID             string
	MaxOutputTokens       int64
	Context               *contextDocument
	ContinuationReference string
}

// protocolCall is one encoded upstream request.
type protocolCall struct {
	Method      string
	Destination string // absolute https URL; must be permitted by the profile's provider_destinations
	Header      http.Header
	Body        []byte
}

// Response states a protocol reports for a decoded response.
const (
	stateCompleted  = "completed"  // the model step finished; outputs are final
	stateFailed     = "failed"     // the provider reports the step failed
	stateAccepted   = "accepted"   // the provider accepted the step but has not finished it
	stateUnresolved = "unresolved" // a reconciliation lookup found no evidence either way
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
	State          string
	ResponseID     string
	FinishReason   string
	Texts          []string
	ToolCalls      []protocolToolCall
	Usage          *protocolTokenUsage // nil when the provider reported no usage
	UsageReference string
	// UsageUnpriceable names why reported tokens cannot be priced under
	// the profile's two rates (for example a service tier or cache
	// activity with its own price). The tokens are still recorded; the
	// amount is reported unknown.
	UsageUnpriceable      string
	NoCharge              bool // true only when the qualified contract establishes this response is not billed
	Refusal               string
	ContinuationReference string
	ErrorCode             string
	ErrorMessage          string
}

// qualifiedProtocols returns the wire protocols this build has qualified,
// keyed by protocol revision. Each entry is pinned to a documented
// upstream revision in the package's PROTOCOL.md; live-endpoint
// qualification is a separate step recorded in the profile's
// capability_evidence, never assumed here.
func qualifiedProtocols() map[string]wireProtocol {
	return map[string]wireProtocol{
		openaiProtocolRevision: openaiProtocol{},
	}
}
