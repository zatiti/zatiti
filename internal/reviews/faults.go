package reviews

import (
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Fault constructors. Every handler error carries a *contract.Fault so the
// surrounding transaction rolls back and the transport maps the code.

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

func reviewRequired(format string, args ...any) error {
	return fault(contract.CodeReviewRequired, false, format, args...)
}

func conflict(format string, args ...any) error {
	return fault(contract.CodeConflict, false, format, args...)
}

func internalError(format string, args ...any) error {
	return fault(contract.CodeInternalError, false, format, args...)
}

// cursorExpired marks a list cursor past its lifetime; the details instruct
// clients to re-read a fresh snapshot instead of retrying the stale one.
func cursorExpired() error {
	return &contract.Fault{
		Code:      contract.CodeCursorExpired,
		Message:   "list cursor has expired; re-read the list from its first page",
		Retryable: false,
		Details:   []byte(`{"snapshot_required":true}`),
	}
}

func fault(code string, retryable bool, format string, args ...any) error {
	return &contract.Fault{
		Code:      code,
		Message:   fmt.Sprintf(format, args...),
		Retryable: retryable,
	}
}
