package storage

import (
	"errors"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// isBusy reports whether err is a SQLite busy or locked condition, the
// bounded-wait exhaustion the contract maps to controller_unavailable.
func isBusy(err error) bool {
	var serr *sqlite.Error
	if !errors.As(err, &serr) {
		return false
	}
	switch serr.Code() & 0xff {
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
		return true
	default:
		return false
	}
}

// FaultError pairs a transport-visible contract.Fault with the underlying
// cause. The fault message stays free of SQL, driver details and private
// paths; callers that need the cause for protected logs unwrap it.
type FaultError struct {
	Fault *contract.Fault
	Err   error
}

// Error returns the fault text only, so the error string is safe to surface.
func (e *FaultError) Error() string { return e.Fault.Error() }

// As supports errors.As for *contract.Fault, the transport's stable error
// vocabulary, without exposing the wrapped cause through Unwrap.
func (e *FaultError) As(target any) bool {
	if f, ok := target.(**contract.Fault); ok && e.Fault != nil {
		*f = e.Fault
		return true
	}
	return false
}

// Unwrap exposes the underlying cause for protected logging.
func (e *FaultError) Unwrap() error { return e.Err }

// storageFault wraps an unexpected driver failure as an internal_error fault.
// The driver message stays in the wrapped chain, never in the fault.
func storageFault(action string, err error) *FaultError {
	return &FaultError{
		Fault: &contract.Fault{
			Code:      contract.CodeInternalError,
			Message:   "storage operation failed",
			Retryable: false,
		},
		Err: fmt.Errorf("storage: %s: %w", action, err),
	}
}

// txFault wraps a transaction lifecycle failure. Busy exhaustion maps to a
// retryable controller_unavailable fault; everything else is internal.
func txFault(action string, err error) error {
	if isBusy(err) {
		return &FaultError{
			Fault: &contract.Fault{
				Code:      contract.CodeControllerUnavailable,
				Message:   "database busy; retry after backoff",
				Retryable: true,
			},
			Err: fmt.Errorf("storage: %s: %w", action, err),
		}
	}
	return storageFault(action, err)
}

// closedFault reports use of a closed database. The controller stops
// admission before closing, so this surfaces only during shutdown races.
func closedFault() *FaultError {
	return &FaultError{
		Fault: &contract.Fault{
			Code:      contract.CodeControllerUnavailable,
			Message:   "database is closed",
			Retryable: false,
		},
	}
}

// readOnlyFault rejects mutation attempts on a read snapshot unit.
func readOnlyFault(action string) *FaultError {
	return &FaultError{
		Fault: &contract.Fault{
			Code:      contract.CodePermissionDenied,
			Message:   "read-only unit; " + action + " requires a write transaction",
			Retryable: false,
		},
	}
}

// invalidInputFault builds an invalid_input fault for rejected caller input.
func invalidInputFault(format string, args ...any) *FaultError {
	return &FaultError{
		Fault: &contract.Fault{
			Code:      contract.CodeInvalidInput,
			Message:   fmt.Sprintf(format, args...),
			Retryable: false,
		},
	}
}

// validUUIDShape reports whether s is a canonical lowercase hyphenated UUID.
// It mirrors the contract's identity shape; only the format is checked.
func validUUIDShape(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch i {
		case 8, 13, 18, 23:
			if s[i] != '-' {
				return false
			}
		default:
			c := s[i]
			if ('0' > c || c > '9') && ('a' > c || c > 'f') {
				return false
			}
		}
	}
	return true
}
