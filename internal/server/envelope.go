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

// envelopeStatus is the single source of the HTTP status: it reads the
// envelope's own status and fault (completed 200, accepted 202, failed the
// fault's frozen mapping through contract.HTTPStatus). Nothing else decides
// a status, so the status line and the envelope body cannot disagree
// whether or not a Go error accompanied the result out of the application.
func envelopeStatus(result contract.Result) int {
	switch result.Status {
	case contract.StatusCompleted:
		return http.StatusOK
	case contract.StatusAccepted:
		return http.StatusAccepted
	default:
		return contract.HTTPStatus(result.Error)
	}
}

// authoritative reports whether result is a well-formed envelope the
// application minted or replayed: frozen schema, a command identity, and a
// status that agrees with the presence of a fault.
func authoritative(result contract.Result) bool {
	if result.Schema != contract.SchemaResult || result.CommandID == "" {
		return false
	}
	switch result.Status {
	case contract.StatusCompleted, contract.StatusAccepted:
		return result.Error == nil
	case contract.StatusFailed:
		return result.Error != nil
	default:
		return false
	}
}

// resultEnvelope reconciles what application.Application returned into the
// one envelope the wire carries. A well-formed failed envelope stands on its
// own, with its retained command identity and fault, whether or not an error
// came with it (a replayed refusal is such an envelope). Otherwise an error
// is the disposition: a completed or accepted envelope is never delivered
// past one. A result that is neither authoritative nor accompanied by an
// error is a local defect and surfaces as internal_error.
func resultEnvelope(result contract.Result, err error) contract.Result {
	switch {
	case authoritative(result) && result.Status == contract.StatusFailed:
		return result
	case err != nil:
		return faultEnvelope(faultFromError(err))
	case authoritative(result):
		return result
	default:
		return faultEnvelope(&contract.Fault{
			Code:    contract.CodeInternalError,
			Message: "operation returned a malformed result envelope",
		})
	}
}

// faultEnvelope builds a fresh result envelope around a fault the server or
// the application layer produced. Every code path that never reached
// application.Application's own command minting (authentication failures,
// malformed envelopes, transport refusals) still owes the caller a
// well-formed envelope with its own command identity; only the application
// layer's results carry an authoritative one.
func faultEnvelope(fault *contract.Fault) contract.Result {
	return contract.Result{
		Schema:    contract.SchemaResult,
		CommandID: contract.NewID(),
		Payload: contract.Payload{
			Status: contract.StatusFailed,
			Error:  fault,
		},
	}
}

// writeEnvelope renders one JSON result envelope at the HTTP status the
// envelope itself maps to and returns that status. It swallows encode
// errors: by the time encoding runs, an error can only mean the client
// vanished, and there is no fault envelope left to report a failed fault
// report.
func writeEnvelope(w http.ResponseWriter, result contract.Result) int {
	status := envelopeStatus(result)
	w.Header().Set("Content-Type", requestEnvelopeContentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
	return status
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
