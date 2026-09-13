package evidence

import (
	"encoding/json"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Fault constructors. Every handler error is a *contract.Fault so the
// surrounding transaction rolls back and transports can map codes. Fault
// messages never include SQL, credential material, file paths or raw
// provider responses.

func invalidInput(format string, args ...any) error {
	return fault(contract.CodeInvalidInput, false, format, args...)
}

func notFound(format string, args ...any) error {
	return fault(contract.CodeNotFound, false, format, args...)
}

func permissionDenied(format string, args ...any) error {
	return fault(contract.CodePermissionDenied, false, format, args...)
}

func submissionConflict(format string, args ...any) error {
	return fault(contract.CodeSubmissionConflict, false, format, args...)
}

func internalFault(format string, args ...any) error {
	return fault(contract.CodeInternalError, false, format, args...)
}

// cursorExpired builds the named cursor-expiry fault: the client must fetch
// a fresh snapshot and resume, never presenting the gap as complete history.
func cursorExpired() error {
	f := fault(contract.CodeCursorExpired, false, "event cursor has expired")
	f.(*contract.Fault).Details = json.RawMessage(`{"snapshot_required":true}`)
	return f
}

// fault builds a fault with a formatted message.
func fault(code string, retryable bool, format string, args ...any) error {
	return &contract.Fault{
		Code:      code,
		Message:   fmt.Sprintf(format, args...),
		Retryable: retryable,
	}
}
