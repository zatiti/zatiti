package client

import (
	"errors"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Sentinel error classes a caller can branch on with errors.Is. Typed
// failures additionally carry structured detail; see UnknownAckError and
// CursorExpiredError.
var (
	// ErrInvalidRequest reports a client-side request validation failure.
	// Nothing was sent; fixing the request is required.
	ErrInvalidRequest = errors.New("invalid request")

	// ErrControllerUnavailable reports a failure to establish the transport
	// connection: no request byte reached the controller. The identical call
	// may be reissued; for a mutation, resubmit with the same submission key.
	ErrControllerUnavailable = errors.New("controller unavailable")

	// ErrTLSCertificate reports that the remote controller's TLS certificate
	// failed verification. This is a permanent configuration or trust failure,
	// not transient unavailability: the client does not retry it.
	ErrTLSCertificate = errors.New("controller TLS certificate verification failed")

	// ErrUnknownOutcome reports that request bytes were sent but no
	// authoritative disposition was obtained. For keyed mutations the client
	// has already attempted bounded replay and command lookup; the caller must
	// resolve with the original submission key and never a new one.
	ErrUnknownOutcome = errors.New("command outcome unknown")
)

// UnknownAckError reports that request bytes were sent but the exchange
// produced no authoritative envelope, so the command's disposition is
// unknown. For a keyed mutation (SubmissionKey non-empty) resolve it by
// looking up the command under the SAME submission key — CommandGetOperation
// — or by replaying the identical request; never resubmit with a new key.
// For a query (SubmissionKey empty) simply reissue it.
type UnknownAckError struct {
	Operation     string
	SubmissionKey string
	Err           error
}

func (e *UnknownAckError) Error() string {
	if e.SubmissionKey != "" {
		return fmt.Sprintf("outcome unknown for %s (submission key %q); resolve by command lookup with the original key, never a new one: %v",
			e.Operation, e.SubmissionKey, e.Err)
	}
	return fmt.Sprintf("outcome unknown for %s; the request may be reissued: %v", e.Operation, e.Err)
}

func (e *UnknownAckError) Unwrap() error { return e.Err }

// Is makes every UnknownAckError match ErrUnknownOutcome so callers can
// classify without type assertions.
func (e *UnknownAckError) Is(target error) bool { return target == ErrUnknownOutcome }

// CursorExpiredError surfaces a cursor_expired fault. SnapshotRequired is the
// fault's snapshot_required detail: when true the caller must fetch a fresh
// authorized snapshot and resume from a new cursor; the client never fills an
// event gap silently.
type CursorExpiredError struct {
	Fault            *contract.Fault
	SnapshotRequired bool
}

func (e *CursorExpiredError) Error() string {
	if e.SnapshotRequired {
		return fmt.Sprintf("cursor expired; fetch a fresh snapshot before replaying: %v", e.Fault)
	}
	return fmt.Sprintf("cursor expired: %v", e.Fault)
}

func (e *CursorExpiredError) Unwrap() error { return e.Fault }
