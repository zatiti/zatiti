package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Bind binds a concrete input/output DTO pair to a descriptor and handler
// function, exactly as the shared contract freezes it:
//
//	Bind[I, O any](descriptor contract.Descriptor,
//	    fn func(context.Context, contract.Unit, I) (contract.Outcome[O], error)) (contract.Handler, error)
//
// The returned contract.Handler enforces the wire conventions on every
// invocation:
//
//   - the invocation targets the bound operation at its bound version,
//   - JSON decoding is strict (duplicate keys, unknown fields and trailing
//     data are rejected) and schema validation precedes execution: the
//     input is validated against the descriptor's input schema merged with
//     the shared $defs before the DTO is decoded and the handler runs,
//   - the typed outcome is marshaled, canonicalized and validated against
//     the descriptor's output schema; a mismatch is an internal fault
//     because it is an owner defect, not a caller error,
//   - handler errors pass through unchanged when they already carry a
//     *contract.Fault; otherwise they are wrapped as internal_error with
//     the original error preserved for internal inspection.
//
// Bind validates the descriptor structurally at bind time (ID format,
// version, visibility, mode, effect, mode/submission-key semantics and
// non-empty strict-JSON schemas) so misdescribed operations fail at
// registration instead of on the wire. Queries return completed outcomes
// only; accepted outcomes are reserved for mutations.
func Bind[I any, O any](descriptor contract.Descriptor, fn func(context.Context, contract.Unit, I) (contract.Outcome[O], error)) (contract.Handler, error) {
	if fn == nil {
		return nil, fmt.Errorf("registry: bind of operation %q requires a handler function", descriptor.ID)
	}
	if err := validateDescriptorShape(&descriptor); err != nil {
		return nil, fmt.Errorf("registry: bind of operation %q: %w", descriptor.ID, err)
	}
	cat, err := loadCatalog()
	if err != nil {
		return nil, fmt.Errorf("registry: bind of operation %q: %w", descriptor.ID, err)
	}
	mergedInput, err := mergedSchema(cat.defsJSON, descriptor.InputSchema)
	if err != nil {
		return nil, fmt.Errorf("registry: bind of operation %s: input schema: %w", descriptor.ID, err)
	}
	if err := checkSchemaDocument(mergedInput); err != nil {
		return nil, fmt.Errorf("registry: bind of operation %s: input schema: %w", descriptor.ID, err)
	}
	mergedOutput, err := mergedSchema(cat.defsJSON, descriptor.OutputSchema)
	if err != nil {
		return nil, fmt.Errorf("registry: bind of operation %s: output schema: %w", descriptor.ID, err)
	}
	if err := checkSchemaDocument(mergedOutput); err != nil {
		return nil, fmt.Errorf("registry: bind of operation %s: output schema: %w", descriptor.ID, err)
	}

	id := descriptor.ID
	query := descriptor.Mode == contract.ModeQuery
	return func(ctx context.Context, unit contract.Unit, invocation contract.Invocation) (contract.Payload, error) {
		if invocation.Operation != id {
			return contract.Payload{}, fault(contract.CodeInternalError,
				"handler for operation %s received invocation for %q", id, invocation.Operation)
		}
		if invocation.Version != descriptor.Version {
			return contract.Payload{}, fault(contract.CodeNotFound,
				"operation %s version %d is not registered", id, invocation.Version)
		}
		if err := contract.ValidateSchema(mergedInput, invocation.Input); err != nil {
			return contract.Payload{}, fault(contract.CodeInvalidInput,
				"operation %s input rejected: %v", id, err)
		}
		var input I
		if err := contract.DecodeStrict(invocation.Input, &input); err != nil {
			return contract.Payload{}, fault(contract.CodeInvalidInput,
				"operation %s input rejected: %v", id, err)
		}
		outcome, err := fn(ctx, unit, input)
		if err != nil {
			var f *contract.Fault
			if errors.As(err, &f) {
				return contract.Payload{}, f
			}
			wrapped := fault(contract.CodeInternalError, "operation %s failed", id)
			return contract.Payload{}, errors.Join(wrapped, err)
		}
		status := outcome.Status
		if status == "" {
			status = contract.StatusCompleted
		}
		if status != contract.StatusCompleted && status != contract.StatusAccepted {
			return contract.Payload{}, fault(contract.CodeInternalError,
				"operation %s returned invalid status %q", id, status)
		}
		if query && status != contract.StatusCompleted {
			return contract.Payload{}, fault(contract.CodeInternalError,
				"operation %s is a query and cannot return an accepted outcome", id)
		}
		data, err := json.Marshal(outcome.Data)
		if err != nil {
			return contract.Payload{}, fault(contract.CodeInternalError,
				"operation %s output could not be marshaled: %v", id, err)
		}
		if data, err = contract.Canonicalize(data); err != nil {
			return contract.Payload{}, fault(contract.CodeInternalError,
				"operation %s output is not canonical JSON: %v", id, err)
		}
		if err := contract.ValidateSchema(mergedOutput, data); err != nil {
			return contract.Payload{}, fault(contract.CodeInternalError,
				"operation %s output does not match its declared schema: %v", id, err)
		}
		return contract.Payload{
			Status:     status,
			Data:       data,
			NextCursor: outcome.NextCursor,
		}, nil
	}, nil
}
