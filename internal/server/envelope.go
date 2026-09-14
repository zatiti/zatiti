package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"

	"github.com/zatiti/zatiti/internal/contract"
)

// requestEnvelopeContentType is the only content type the operation endpoint
// accepts. Strict content type checking rejects a protocol mismatch before
// any JSON parsing runs.
const requestEnvelopeContentType = "application/json"

// checkContentType requires an exact application/json media type (charset
// and other parameters are ignored). A missing or mismatched content type
// is a protocol violation, mapped invalid_input like any other malformed
// body.
func checkContentType(r *http.Request) *contract.Fault {
	header := r.Header.Get("Content-Type")
	if header == "" {
		return &contract.Fault{Code: contract.CodeInvalidInput, Message: "Content-Type is required"}
	}
	media, _, err := mime.ParseMediaType(header)
	if err != nil || media != requestEnvelopeContentType {
		return &contract.Fault{
			Code:    contract.CodeInvalidInput,
			Message: fmt.Sprintf("Content-Type must be %s", requestEnvelopeContentType),
		}
	}
	return nil
}

// decodeRequest reads the request body under maxBodyBytes and decodes it
// with zatiti wire strictness. An oversized or malformed body is a protocol
// violation and is mapped invalid_input, never a raw transport error.
func decodeRequest(r *http.Request, maxBodyBytes int64) (contract.Request, *contract.Fault) {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return contract.Request{}, &contract.Fault{
				Code:    contract.CodeInvalidInput,
				Message: fmt.Sprintf("request body exceeds %d bytes", tooLarge.Limit),
			}
		}
		return contract.Request{}, &contract.Fault{
			Code:    contract.CodeInvalidInput,
			Message: "request body could not be read",
		}
	}
	var req contract.Request
	if err := contract.DecodeStrict(body, &req); err != nil {
		return contract.Request{}, &contract.Fault{
			Code:    contract.CodeInvalidInput,
			Message: fmt.Sprintf("request envelope is not valid: %v", err),
		}
	}
	return req, nil
}

// writeResult encodes an authoritative application.Application result as the
// wire response: 200 for a completed command, 202 for an accepted one. The
// dispatcher never returns a failed result without an error, so this path
// only ever carries a non-failed payload.
func writeResult(w http.ResponseWriter, result contract.Result) {
	status := http.StatusOK
	if result.Status == contract.StatusAccepted {
		status = http.StatusAccepted
	}
	writeEnvelope(w, status, result)
}

// writeFault builds a fresh result envelope around a fault the server or the
// application layer produced and encodes it at the fault's mapped HTTP
// status. Every code path that never reached application.Application's own
// command minting (authentication failures, malformed envelopes, transport
// refusals) still owes the caller a well-formed, durable-looking envelope
// with its own command identity; only the application layer's completed and
// accepted results carry an authoritative one.
func writeFault(w http.ResponseWriter, fault *contract.Fault) {
	result := contract.Result{
		Schema:    contract.SchemaResult,
		CommandID: contract.NewID(),
		Payload: contract.Payload{
			Status: contract.StatusFailed,
			Error:  fault,
		},
	}
	writeEnvelope(w, contract.HTTPStatus(fault), result)
}

// writeEnvelope renders one JSON result envelope with a matching HTTP
// status. It swallows encode errors: by the time encoding runs, an error can
// only mean the client vanished, and there is no fault envelope left to
// report a failed fault report.
func writeEnvelope(w http.ResponseWriter, status int, result contract.Result) {
	w.Header().Set("Content-Type", requestEnvelopeContentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}

// faultFromError extracts the wire fault an application.Application call
// failed with. Every Application error is a *contract.Fault; a value that
// is not one is an unexpected local defect and surfaces as internal_error
// rather than leaking an unclassified error string to the wire.
func faultFromError(err error) *contract.Fault {
	var fault *contract.Fault
	if errors.As(err, &fault) && fault != nil {
		return fault
	}
	return &contract.Fault{
		Code:    contract.CodeInternalError,
		Message: "operation failed without a classified fault",
	}
}
