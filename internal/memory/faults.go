package memory

import (
	"encoding/json"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Fault constructors. Every domain failure carries a named code, a message
// safe for wire exposure and retryability; none of them leak SQL, provider
// responses or private paths.

func invalidInput(format string, args ...any) error {
	return fault(contract.CodeInvalidInput, false, format, args...)
}

func notFound(format string, args ...any) error {
	return fault(contract.CodeNotFound, false, format, args...)
}

func permissionDenied(format string, args ...any) error {
	return fault(contract.CodePermissionDenied, false, format, args...)
}

func staleVersion(format string, args ...any) error {
	return fault(contract.CodeStaleVersion, false, format, args...)
}

func conflict(format string, args ...any) error {
	return fault(contract.CodeConflict, false, format, args...)
}

func reviewRequired(format string, args ...any) error {
	return fault(contract.CodeReviewRequired, false, format, args...)
}

func prerequisiteMissing(format string, args ...any) error {
	return fault(contract.CodePrerequisiteMissing, false, format, args...)
}

func capabilityUnsupported(format string, args ...any) error {
	return fault(contract.CodeCapabilityUnsupported, false, format, args...)
}

// fault builds a contract.Fault error with the shared conventions.
func fault(code string, retryable bool, format string, args ...any) error {
	return &contract.Fault{
		Code:      code,
		Message:   fmt.Sprintf(format, args...),
		Retryable: retryable,
	}
}

// cursorExpired marks an expired pagination cursor and directs the caller to
// re-run the query for a fresh snapshot.
func cursorExpired() error {
	return &contract.Fault{
		Code:      contract.CodeCursorExpired,
		Message:   "list cursor has expired; request a fresh page",
		Retryable: false,
		Details:   json.RawMessage(`{"snapshot_required":true}`),
	}
}
