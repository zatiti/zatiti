package controller

import (
	"context"
	"errors"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Fault constructors. Every controller error that crosses the package
// boundary is a *contract.Fault with a stable code. Messages never include
// SQL, credential material, local paths or raw provider responses.

func invalidInput(format string, args ...any) *contract.Fault {
	return fault(contract.CodeInvalidInput, false, format, args...)
}

func prerequisiteMissing(format string, args ...any) *contract.Fault {
	return fault(contract.CodePrerequisiteMissing, false, format, args...)
}

func capabilityUnsupported(format string, args ...any) *contract.Fault {
	return fault(contract.CodeCapabilityUnsupported, false, format, args...)
}

func conflictFault(format string, args ...any) *contract.Fault {
	return fault(contract.CodeConflict, false, format, args...)
}

func unavailable(format string, args ...any) *contract.Fault {
	return fault(contract.CodeControllerUnavailable, true, format, args...)
}

func internalFault(format string, args ...any) *contract.Fault {
	return fault(contract.CodeInternalError, false, format, args...)
}

func fault(code string, retryable bool, format string, args ...any) *contract.Fault {
	return &contract.Fault{
		Code:      code,
		Message:   fmt.Sprintf(format, args...),
		Retryable: retryable,
	}
}

// faultOf projects any error onto a fault. Owner errors already are faults;
// anything else is reported as a retryable unavailability without echoing
// the underlying text, which may carry storage or provider detail.
func faultOf(err error) *contract.Fault {
	if err == nil {
		return nil
	}
	var f *contract.Fault
	if errors.As(err, &f) && f != nil {
		return f
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return unavailable("the call was interrupted before it completed")
	}
	return unavailable("the call failed without a contract fault")
}

// isFault reports whether err is an owner's contract fault. A handler fault
// rolls its transaction back; any other error leaves the commit unknown.
func isFault(err error) bool {
	var f *contract.Fault
	return errors.As(err, &f) && f != nil
}

// transient reports whether a failed owner call may succeed when repeated
// unchanged. A non-fault error (interrupted call, closed database) and an
// explicitly retryable or unavailable fault are transient; every other fault
// is the owner's durable refusal and repeating it changes nothing.
func transient(err error) bool {
	if err == nil {
		return false
	}
	var f *contract.Fault
	if !errors.As(err, &f) || f == nil {
		return true
	}
	return f.Retryable || f.Code == contract.CodeControllerUnavailable
}
