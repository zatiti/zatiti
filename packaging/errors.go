package packaging

import (
	"errors"
	"fmt"
)

// Fault codes. The values repeat the frozen contract vocabulary; this package
// cannot import internal/contract, so they are declared locally.
const (
	CodeInvalidInput          = "invalid_input"
	CodeNotFound              = "not_found"
	CodeConflict              = "conflict"
	CodePrerequisiteMissing   = "prerequisite_missing"
	CodeCapabilityUnsupported = "capability_unsupported"
	CodeVerificationFailed    = "verification_failed"
	CodeInternalError         = "internal_error"
)

// Finding is one observed defect in a distribution tree or an installed
// layout. Path is relative to the audited root, never absolute.
type Finding struct {
	Path    string `json:"path"`
	Problem string `json:"problem"`
}

// Error is a coded, redacted packaging error. Message never contains an
// absolute local path, helper output or key material. The wrapped cause
// stays reachable through errors.Is and errors.As but Error never renders it.
type Error struct {
	Code    string
	Message string
	// Findings lists every observed defect when Code is
	// verification_failed.
	Findings []Finding
	cause    error
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

func errFindings(message string, findings []Finding) *Error {
	return &Error{Code: CodeVerificationFailed, Message: message, Findings: findings}
}

// Code reports the fault code carried by err, or internal_error for an error
// this package did not produce. A nil error has no code.
func Code(err error) string {
	if err == nil {
		return ""
	}
	var perr *Error
	if errors.As(err, &perr) && perr.Code != "" {
		return perr.Code
	}
	return CodeInternalError
}
