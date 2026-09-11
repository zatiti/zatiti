package policy

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

func staleVersion(format string, args ...any) error {
	return fault(contract.CodeStaleVersion, false, format, args...)
}

func conflict(format string, args ...any) error {
	return fault(contract.CodeConflict, false, format, args...)
}

// cursorExpired returns the cursor_expired fault. Snapshot-bound cursors
// always report snapshot_required=true so clients know to re-read a fresh
// consistent page instead of retrying the bound one.
func cursorExpired() error {
	f := &contract.Fault{
		Code:    contract.CodeCursorExpired,
		Message: "the authenticated cursor expired; re-read with a fresh snapshot",
	}
	f.Details = json.RawMessage(`{"snapshot_required":true}`)
	return f
}

// fault builds a non-retryable fault with a formatted message.
func fault(code string, retryable bool, format string, args ...any) error {
	return &contract.Fault{
		Code:      code,
		Message:   fmt.Sprintf(format, args...),
		Retryable: retryable,
	}
}
