package client

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zatiti/zatiti/internal/contract"
)

// operationIDMax bounds an operation path segment defensively; the frozen
// catalog's dot-separated IDs stay far below it.
const operationIDMax = 200

// submissionKeyMax is the frozen submission key bound: 1..128 printable
// ASCII characters.
const submissionKeyMax = 128

// marshalRequest validates the operation identifier and request envelope and
// renders the exact request body bytes. The input is validated with zatiti
// wire strictness (valid UTF-8, no duplicate keys, no trailing data,
// integers within int64) so caller-side mistakes fail before anything is
// sent. A nil input marshals as JSON null and the controller's schema
// decides whether that is acceptable.
func marshalRequest(operation string, req contract.Request) ([]byte, error) {
	if err := validateOperationID(operation); err != nil {
		return nil, err
	}
	switch req.Schema {
	case "":
		req.Schema = contract.SchemaRequest
	case contract.SchemaRequest:
	default:
		return nil, fmt.Errorf("%w: request schema %q, want %q", ErrInvalidRequest, req.Schema, contract.SchemaRequest)
	}
	if req.SubmissionKey != "" {
		if err := validateSubmissionKey(req.SubmissionKey); err != nil {
			return nil, err
		}
	}
	if len(req.Input) > 0 {
		if err := contract.DecodeStrict(req.Input, new(json.RawMessage)); err != nil {
			return nil, fmt.Errorf("%w: input is not strict JSON: %v", ErrInvalidRequest, err)
		}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("%w: request cannot be encoded: %v", ErrInvalidRequest, err)
	}
	return body, nil
}

// validateOperationID accepts the frozen catalog shape: non-empty
// dot-separated segments of [a-z0-9_], no empty segment, no leading or
// trailing dot. The identifier is used verbatim as the single path segment,
// so anything else is refused rather than escaped.
func validateOperationID(operation string) error {
	if operation == "" {
		return fmt.Errorf("%w: operation is required", ErrInvalidRequest)
	}
	if len(operation) > operationIDMax {
		return fmt.Errorf("%w: operation identifier exceeds %d bytes", ErrInvalidRequest, operationIDMax)
	}
	for i := 0; i < len(operation); i++ {
		c := operation[i]
		if c == '.' || c == '_' || ('a' <= c && c <= 'z') || ('0' <= c && c <= '9') {
			continue
		}
		return fmt.Errorf("%w: operation %q contains a byte outside [a-z0-9_.]", ErrInvalidRequest, operation)
	}
	for _, segment := range strings.Split(operation, ".") {
		if segment == "" {
			return fmt.Errorf("%w: operation %q has an empty segment", ErrInvalidRequest, operation)
		}
	}
	return nil
}

// validateSubmissionKey enforces the frozen bound: 1..128 printable ASCII
// characters (0x20..0x7E).
func validateSubmissionKey(key string) error {
	if key == "" {
		return fmt.Errorf("%w: submission key is required for mutations", ErrInvalidRequest)
	}
	if len(key) > submissionKeyMax {
		return fmt.Errorf("%w: submission key exceeds %d bytes", ErrInvalidRequest, submissionKeyMax)
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x20 || key[i] > 0x7e {
			return fmt.Errorf("%w: submission key contains a non-printable byte", ErrInvalidRequest)
		}
	}
	return nil
}

// envelopeStatusByHTTP pins the frozen mapping from HTTP status to the
// result envelope status it may carry: 200 completed, 202 accepted, and the
// documented domain-failure statuses failed. Any other HTTP status is a
// protocol violation; drift against contract.HTTPStatus is covered by the
// fault-matrix test.
var envelopeStatusByHTTP = map[int]string{
	200: contract.StatusCompleted,
	202: contract.StatusAccepted,
	400: contract.StatusFailed,
	403: contract.StatusFailed,
	404: contract.StatusFailed,
	409: contract.StatusFailed,
	410: contract.StatusFailed,
	422: contract.StatusFailed,
	500: contract.StatusFailed,
	503: contract.StatusFailed,
}

// validateResult enforces strict envelope structure and its agreement with
// the HTTP status: the frozen schema string, a durable command identity, an
// exactly-allowed status, error/status consistency, and the HTTP mapping.
func validateResult(httpStatus int, r *contract.Result) error {
	if r.Schema != contract.SchemaResult {
		return fmt.Errorf("result envelope schema %q, want %q", r.Schema, contract.SchemaResult)
	}
	if r.CommandID == "" {
		return fmt.Errorf("result envelope has no command_id")
	}
	switch r.Status {
	case contract.StatusCompleted, contract.StatusAccepted:
		if r.Error != nil {
			return fmt.Errorf("result envelope status %q carries a fault", r.Status)
		}
	case contract.StatusFailed:
		if r.Error == nil {
			return fmt.Errorf("failed result envelope carries no fault")
		}
	default:
		return fmt.Errorf("result envelope status %q is not completed, accepted or failed", r.Status)
	}
	want, ok := envelopeStatusByHTTP[httpStatus]
	if !ok {
		return fmt.Errorf("unexpected HTTP status %d for an operation result", httpStatus)
	}
	if want != r.Status {
		return fmt.Errorf("HTTP status %d disagrees with envelope status %q", httpStatus, r.Status)
	}
	return nil
}

// cursorExpired surfaces a cursor_expired fault as *CursorExpiredError so the
// caller refreshes its snapshot before replaying; every other fault is
// returned unchanged.
func cursorExpired(fault *contract.Fault) error {
	if fault.Code != contract.CodeCursorExpired {
		return fault
	}
	return &CursorExpiredError{Fault: fault, SnapshotRequired: snapshotRequired(fault.Details)}
}

// snapshotRequired reads the open details object for snapshot_required.
// Details are extension space, so the peek is tolerant of other fields and
// of a missing or non-object details value.
func snapshotRequired(details json.RawMessage) bool {
	if len(details) == 0 {
		return false
	}
	var d struct {
		SnapshotRequired bool `json:"snapshot_required"`
	}
	if err := json.Unmarshal(details, &d); err != nil {
		return false
	}
	return d.SnapshotRequired
}
