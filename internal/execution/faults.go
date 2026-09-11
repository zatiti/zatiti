package execution

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

// cursorExpired carries snapshot_required=true: clients must re-read a
// fresh page instead of retrying a stale snapshot.
func cursorExpired(format string, args ...any) error {
	f := fault(contract.CodeCursorExpired, false, format, args...)
	f.Details = json.RawMessage(`{"snapshot_required":true}`)
	return f
}

func prerequisiteMissing(format string, args ...any) error {
	return fault(contract.CodePrerequisiteMissing, false, format, args...)
}

func capabilityUnsupported(format string, args ...any) error {
	return fault(contract.CodeCapabilityUnsupported, false, format, args...)
}

func verificationFailed(format string, args ...any) error {
	return fault(contract.CodeVerificationFailed, false, format, args...)
}

// fault builds a fault with a formatted message. controller_unavailable is
// never raised here: busy storage exhaustion surfaces from the shared
// storage layer itself.
func fault(code string, retryable bool, format string, args ...any) *contract.Fault {
	return &contract.Fault{
		Code:      code,
		Message:   fmt.Sprintf(format, args...),
		Retryable: retryable,
	}
}
