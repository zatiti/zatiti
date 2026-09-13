package installation

import (
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Fault constructors. Every handler error is a *contract.Fault so the
// surrounding transaction rolls back and transports can map codes. Fault
// messages never include SQL, credential material, store references or raw
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

func conflictFault(format string, args ...any) error {
	return fault(contract.CodeConflict, false, format, args...)
}

func prerequisiteMissing(format string, args ...any) error {
	return fault(contract.CodePrerequisiteMissing, false, format, args...)
}

func artifactFault(format string, args ...any) error {
	return fault(contract.CodeArtifactFault, false, format, args...)
}

func internalError(format string, args ...any) error {
	return fault(contract.CodeInternalError, false, format, args...)
}

// fault builds a non-retryable fault with a formatted message.
func fault(code string, retryable bool, format string, args ...any) error {
	return &contract.Fault{
		Code:      code,
		Message:   fmt.Sprintf(format, args...),
		Retryable: retryable,
	}
}
