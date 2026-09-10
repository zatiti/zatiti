package application

import (
	"errors"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// faultOf extracts the transport-visible fault from err. Handler and storage
// errors carry a *contract.Fault through errors.As; an error without one is
// an application defect and surfaces as internal_error.
func faultOf(err error) *contract.Fault {
	var f *contract.Fault
	if errors.As(err, &f) && f != nil {
		return f
	}
	return internalFault("operation failed without a fault: %v", err)
}

func faultErr(f *contract.Fault) error { return f }

func invalidFault(format string, args ...any) *contract.Fault {
	return &contract.Fault{
		Code:    contract.CodeInvalidInput,
		Message: fmt.Sprintf(format, args...),
	}
}

func notFoundFault(format string, args ...any) *contract.Fault {
	return &contract.Fault{
		Code:    contract.CodeNotFound,
		Message: fmt.Sprintf(format, args...),
	}
}

func permissionFault(format string, args ...any) *contract.Fault {
	return &contract.Fault{
		Code:    contract.CodePermissionDenied,
		Message: fmt.Sprintf(format, args...),
	}
}

func submissionConflictFault(format string, args ...any) *contract.Fault {
	return &contract.Fault{
		Code:    contract.CodeSubmissionConflict,
		Message: fmt.Sprintf(format, args...),
	}
}

func unavailableFault(format string, args ...any) *contract.Fault {
	return &contract.Fault{
		Code:      contract.CodeControllerUnavailable,
		Message:   fmt.Sprintf(format, args...),
		Retryable: true,
	}
}

func internalFault(format string, args ...any) *contract.Fault {
	return &contract.Fault{
		Code:    contract.CodeInternalError,
		Message: fmt.Sprintf(format, args...),
	}
}

// payloadDefect converts a handler's failed payload into the error the
// dispatcher records. A failed payload must carry its fault; the dispatcher
// surfaces it as the operation's disposition. Without one the payload is a
// handler defect and fails closed as internal_error.
func payloadDefect(operation string, p contract.Payload) error {
	if p.Error != nil {
		return faultErr(p.Error)
	}
	return internalFault("operation %s returned a failed payload without an error", operation)
}

// retryableFault reports whether retrying the same request could plausibly
// produce a different disposition. Refusals with these codes are recorded
// durably after rollback so an identical replay returns the identical
// refusal instead of re-executing.
func deterministicRefusal(f *contract.Fault) bool {
	switch f.Code {
	case contract.CodeInvalidInput,
		contract.CodePermissionDenied,
		contract.CodeStaleVersion,
		contract.CodeConflict,
		contract.CodePrerequisiteMissing,
		contract.CodeExternalActionRequired,
		contract.CodeCapabilityUnsupported,
		contract.CodeVerificationFailed,
		contract.CodeArtifactFault:
		return true
	default:
		return false
	}
}
