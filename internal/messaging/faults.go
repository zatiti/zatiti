package messaging

import (
	"encoding/json"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// fault builds a contract fault with a formatted message. Messages never
// carry SQL, credentials, provider payloads or private paths.
func fault(code string, format string, args ...any) error {
	return &faultError{fault: &contract.Fault{Code: code, Message: fmt.Sprintf(format, args...)}}
}

func invalidInput(format string, args ...any) error {
	return fault(contract.CodeInvalidInput, format, args...)
}

func notFound(format string, args ...any) error {
	return fault(contract.CodeNotFound, format, args...)
}

func permissionDenied(format string, args ...any) error {
	return fault(contract.CodePermissionDenied, format, args...)
}

func staleVersion(format string, args ...any) error {
	return fault(contract.CodeStaleVersion, format, args...)
}

func submissionConflict(format string, args ...any) error {
	return fault(contract.CodeSubmissionConflict, format, args...)
}

func conflictFault(format string, args ...any) error {
	return fault(contract.CodeConflict, format, args...)
}

func reviewRequired(format string, args ...any) error {
	return fault(contract.CodeReviewRequired, format, args...)
}

func prerequisiteMissing(format string, args ...any) error {
	return fault(contract.CodePrerequisiteMissing, format, args...)
}

func artifactFault(format string, args ...any) error {
	return fault(contract.CodeArtifactFault, format, args...)
}

func internalError(format string, args ...any) error {
	return fault(contract.CodeInternalError, format, args...)
}

// cursorExpired carries snapshot_required=true so clients know to restart
// the listing from a fresh snapshot rather than retry the stale cursor.
func cursorExpired(format string, args ...any) error {
	return &faultError{fault: &contract.Fault{
		Code:    contract.CodeCursorExpired,
		Message: fmt.Sprintf(format, args...),
		Details: json.RawMessage(`{"snapshot_required":true}`),
	}}
}

// faultError carries a *contract.Fault through the error path so transports
// map it to HTTP status and CLI exit code without string parsing.
type faultError struct {
	fault *contract.Fault
	err   error
}

func (e *faultError) Error() string {
	if e.err != nil {
		return e.fault.Message + ": " + e.err.Error()
	}
	return e.fault.Message
}

func (e *faultError) Unwrap() error { return e.err }

func (e *faultError) As(target any) bool {
	if t, ok := target.(**contract.Fault); ok {
		*t = e.fault
		return true
	}
	return false
}

// faultWrap re-wraps err under an existing fault so context is preserved.
func faultWrap(f *contract.Fault, err error) error {
	return &faultError{fault: f, err: err}
}

// marshalData encodes a payload data value; failures are internal faults.
func marshalData(v any) (json.RawMessage, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, internalError("payload encoding failed: %v", err)
	}
	return raw, nil
}
