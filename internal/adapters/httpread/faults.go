package httpread

import (
	"errors"
	"fmt"
	"net"

	"github.com/zatiti/zatiti/internal/contract"
)

// fault builds a *contract.Fault, the stable error vocabulary every
// transport shares. *contract.Fault implements error, so Invoke/Reconcile
// return it directly wherever a physical request was never sent.
func fault(code, format string, args ...any) *contract.Fault {
	return &contract.Fault{
		Code:    code,
		Message: fmt.Sprintf(format, args...),
	}
}

func invalidInput(format string, args ...any) *contract.Fault {
	return fault(contract.CodeInvalidInput, format, args...)
}

func permissionDenied(format string, args ...any) *contract.Fault {
	return fault(contract.CodePermissionDenied, format, args...)
}

func capabilityUnsupported(format string, args ...any) *contract.Fault {
	return fault(contract.CodeCapabilityUnsupported, format, args...)
}

func internalError(format string, args ...any) *contract.Fault {
	return fault(contract.CodeInternalError, format, args...)
}

// blobFault maps a BlobStore failure onto the shared fault vocabulary. This
// package's only allowed production import is internal/contract, so it
// cannot inspect a concrete platform-level error type; a bare
// *contract.Fault passes through unchanged and everything else fails
// closed as internal_error without leaking the underlying message.
func blobFault(err error) *contract.Fault {
	var f *contract.Fault
	if errors.As(err, &f) && f != nil {
		return f
	}
	return internalError("httpread: staging the response body failed")
}

// classifyPreResponseFailure maps a transport-level error that occurred
// before any HTTP response line was received into the Fault this adapter
// returns from call(). httpread's own zatiti.httpread.evidence/v1 schema
// requires a real HTTP status (100-599) on every recorded evidence
// document, so a failure with no status to report can never be honestly
// encoded as an Observation the way a sibling mutation adapter preserves an
// outcome_unknown Observation across a lost acknowledgement. Because a GET
// has no side effect whose non-execution must be proven, reporting these as
// ordinary (optionally retryable) faults instead is safe: the caller may
// simply retry the read.
//
// Three cases are distinguished:
//   - safeDialContext already produced a precise *contract.Fault (the
//     resolved destination is loopback/private/link-local/metadata, or no
//     candidate address survived validation): that fault passes through
//     unchanged, since it already carries the exact permission_denied
//     reason.
//   - a name resolution failure: outcome_unknown, retryable unless the
//     resolver authoritatively reports the name does not exist.
//   - any other pre-response transport failure (connection refused, TLS
//     failure, a context deadline hit before headers arrived): outcome
//     unknown and retryable.
func classifyPreResponseFailure(err error) *contract.Fault {
	var f *contract.Fault
	if errors.As(err, &f) && f != nil {
		return f
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return &contract.Fault{
			Code:      contract.CodeOutcomeUnknown,
			Message:   fmt.Sprintf("httpread: name resolution failed: %v", dnsErr),
			Retryable: !dnsErr.IsNotFound,
		}
	}
	return &contract.Fault{
		Code:      contract.CodeOutcomeUnknown,
		Message:   fmt.Sprintf("httpread: request failed before a response was received: %v", err),
		Retryable: true,
	}
}
