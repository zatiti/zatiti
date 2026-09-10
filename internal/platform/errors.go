package platform

import (
	"errors"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Local aliases of the frozen contract fault codes; platform errors always
// speak the shared vocabulary.
const (
	contractCodeInvalidInput          = contract.CodeInvalidInput
	contractCodeNotFound              = contract.CodeNotFound
	contractCodePrerequisiteMissing   = contract.CodePrerequisiteMissing
	contractCodeCapabilityUnsupported = contract.CodeCapabilityUnsupported
	contractCodeControllerUnavailable = contract.CodeControllerUnavailable
	contractCodeArtifactFaultAlias    = contract.CodeArtifactFault
	contractCodeInternalErrorAlias    = contract.CodeInternalError
)

// Error is a coded, redacted platform error. Message never contains local
// absolute paths, command output or secret material; the wrapped cause stays
// reachable through errors.Is/errors.As for programmatic inspection but is
// never rendered by Error, so product surfaces cannot leak it. Retryable
// mirrors contract.Fault.Retryable for callers that map platform errors onto
// the shared fault vocabulary.
type Error struct {
	Code      string
	Message   string
	Retryable bool
	cause     error
}

// Error returns the redacted message only.
func (e *Error) Error() string { return e.Message }

// Unwrap exposes the internal cause to programmatic inspection.
func (e *Error) Unwrap() error { return e.cause }

func errf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

func errWrap(code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, cause: cause}
}

// Code reports the stable fault code carried by a platform error, a
// contract.Fault, or internal_error for anything else. Transports use it to
// map platform failures onto the shared fault vocabulary without parsing
// message text.
func Code(err error) string {
	if err == nil {
		return ""
	}
	var perr *Error
	if errors.As(err, &perr) && perr.Code != "" {
		return perr.Code
	}
	var fault *contract.Fault
	if errors.As(err, &fault) && fault.Code != "" {
		return fault.Code
	}
	return contract.CodeInternalError
}

// artifactFault builds the product-facing fault for artifact storage
// failures. Messages are redacted: they never contain local paths.
func artifactFault(format string, args ...any) *contract.Fault {
	return &contract.Fault{
		Code:      contract.CodeArtifactFault,
		Message:   fmt.Sprintf(format, args...),
		Retryable: false,
	}
}
