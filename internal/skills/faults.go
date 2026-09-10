package skills

import (
	"encoding/json"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// fault builds a *Fault carrying the given code. Handler errors carry a
// *Fault so the dispatcher rolls the transaction back and transports can map
// the code to status/exit without string matching.
func fault(code, format string, args ...any) *contract.Fault {
	return &contract.Fault{
		Code:      code,
		Message:   fmt.Sprintf(format, args...),
		Retryable: false,
	}
}

func invalidInput(format string, args ...any) *contract.Fault {
	return fault(contract.CodeInvalidInput, format, args...)
}

func notFound(format string, args ...any) *contract.Fault {
	return fault(contract.CodeNotFound, format, args...)
}

func permissionDenied(format string, args ...any) *contract.Fault {
	return fault(contract.CodePermissionDenied, format, args...)
}

func conflictFault(format string, args ...any) *contract.Fault {
	return fault(contract.CodeConflict, format, args...)
}

func cursorExpired(format string, args ...any) *contract.Fault {
	return fault(contract.CodeCursorExpired, format, args...)
}

func internalError(format string, args ...any) *contract.Fault {
	return fault(contract.CodeInternalError, format, args...)
}

// faultOf wraps err as an internal_error fault when it does not already
// carry one, preserving the cause for protected logs only.
func faultOf(err error) *contract.Fault {
	var f *contract.Fault
	if ok := asFault(err, &f); ok && f != nil {
		return f
	}
	return internalError("skills operation failed")
}

// faultError carries a *Fault through handler returns so errors.As finds the
// stable vocabulary; the wrapped cause stays available for protected logs.
type faultError struct {
	fault *contract.Fault
	err   error
}

func (e *faultError) Error() string { return e.fault.Error() }

func (e *faultError) Unwrap() error { return e.err }

// As implements errors.As for **contract.Fault.
func (e *faultError) As(target any) bool {
	if f, ok := target.(**contract.Fault); ok && e.fault != nil {
		*f = e.fault
		return true
	}
	return false
}

// faultWrap pairs a fault with an underlying cause.
func faultWrap(f *contract.Fault, err error) error {
	return &faultError{fault: f, err: err}
}

// asFault is errors.As specialised to *contract.Fault without importing
// errors at every call site. It matches a bare *contract.Fault, the
// faultError wrapper and anything else exposing the errors.As protocol.
func asFault(err error, target **contract.Fault) bool {
	for err != nil {
		if f, ok := err.(*contract.Fault); ok {
			*target = f
			return true
		}
		if fe, ok := err.(*faultError); ok {
			*target = fe.fault
			return true
		}
		if fe, ok := err.(interface{ As(any) bool }); ok {
			if fe.As(target) {
				return true
			}
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// marshalData encodes v as the payload data body, failing closed.
func marshalData(v any) (json.RawMessage, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, internalError("result encoding failed")
	}
	return raw, nil
}
