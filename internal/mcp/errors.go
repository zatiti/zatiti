package mcp

import (
	"errors"

	"github.com/zatiti/zatiti/internal/client"
	"github.com/zatiti/zatiti/internal/contract"
)

// transportFailureResult synthesizes the result envelope for an operator
// call that produced no authoritative envelope at all: the exchange never
// reached a controller decision (connection failure, TLS failure, an
// unresolved unknown outcome, or a request the client itself refused to
// send). No command was ever created, so CommandID stays empty rather than
// a fabricated identity.
func transportFailureResult(err error) contract.Result {
	return contract.Result{
		Schema: contract.SchemaResult,
		Payload: contract.Payload{
			Status: contract.StatusFailed,
			Error:  transportFault(err),
		},
	}
}

// transportFault classifies a client-side transport error into the stable
// fault vocabulary. A missing controller is reported as
// controller_unavailable (retryable) rather than surfaced only as a bare
// protocol error: the shared contract requires it be a named, inspectable
// tool result an agent can act on, exactly like any other domain fault, and
// this package never starts a second controller or scheduler to work around
// it.
func transportFault(err error) *contract.Fault {
	switch {
	case errors.Is(err, client.ErrControllerUnavailable):
		return &contract.Fault{Code: contract.CodeControllerUnavailable, Message: err.Error(), Retryable: true}
	case errors.Is(err, client.ErrUnknownOutcome):
		return &contract.Fault{Code: contract.CodeOutcomeUnknown, Message: err.Error(), Retryable: false}
	case errors.Is(err, client.ErrTLSCertificate):
		return &contract.Fault{Code: contract.CodeInternalError, Message: err.Error(), Retryable: false}
	case errors.Is(err, client.ErrInvalidRequest):
		return &contract.Fault{Code: contract.CodeInvalidInput, Message: err.Error(), Retryable: false}
	default:
		return &contract.Fault{Code: contract.CodeInternalError, Message: err.Error(), Retryable: false}
	}
}
