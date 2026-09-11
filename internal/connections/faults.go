package connections

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

func staleVersion(format string, args ...any) *contract.Fault {
	return fault(contract.CodeStaleVersion, format, args...)
}

func conflictFault(format string, args ...any) *contract.Fault {
	return fault(contract.CodeConflict, format, args...)
}

func cursorExpired(format string, args ...any) *contract.Fault {
	return fault(contract.CodeCursorExpired, format, args...)
}

func prerequisiteMissing(format string, args ...any) *contract.Fault {
	return fault(contract.CodePrerequisiteMissing, format, args...)
}

func verificationFailed(format string, args ...any) *contract.Fault {
	return fault(contract.CodeVerificationFailed, format, args...)
}

func internalError(format string, args ...any) *contract.Fault {
	return fault(contract.CodeInternalError, format, args...)
}

// errorDiagnostic builds an error-severity wire Diagnostic.
func errorDiagnostic(path, code, message string) wireDiagnostic {
	return wireDiagnostic{Path: path, Code: code, Message: message, Severity: "error"}
}

// warnDiagnostic builds a warning-severity wire Diagnostic.
func warnDiagnostic(path, code, message string) wireDiagnostic {
	return wireDiagnostic{Path: path, Code: code, Message: message, Severity: "warning"}
}

// infoDiagnostic builds an info-severity wire Diagnostic.
func infoDiagnostic(path, code, message string) wireDiagnostic {
	return wireDiagnostic{Path: path, Code: code, Message: message, Severity: "info"}
}

// requirement builds a wire Requirement.
func requirement(code, message string, resourceID, challengeID contract.ID) wireRequirement {
	r := wireRequirement{Code: code, Message: message}
	if resourceID != "" {
		r.ResourceID = resourceID
	}
	if challengeID != "" {
		r.ChallengeID = challengeID
	}
	return r
}

// marshalData encodes v as the payload data body, failing closed.
func marshalData(v any) (json.RawMessage, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, internalError("result encoding failed")
	}
	return raw, nil
}
