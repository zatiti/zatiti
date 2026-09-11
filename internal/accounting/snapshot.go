package accounting

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Scope snapshot. Reservation, inspection and limit views resolve the live
// configuration ancestry through _configuration.snapshot on the caller's
// transaction; the trusted peer payload decodes strictly, so every field
// configuration emits must be mirrored (dto.go).

// loadSnapshot fetches the configuration snapshot for one scope through the
// declared ports. A missing ports dependency is a prerequisite failure, never
// an invented empty snapshot.
func (s *Service) loadSnapshot(ctx context.Context, unit contract.Unit, scope wireScope) (*snapshotScope, error) {
	raw, err := s.callConfiguration(ctx, unit, opConfigSnapshot, scopeInput{Scope: scope})
	if err != nil {
		return nil, err
	}
	var body snapshotScopeBody
	if err := contract.DecodeStrict(raw, &body); err != nil {
		return nil, fmt.Errorf("accounting: configuration snapshot payload does not decode: %w", err)
	}
	return &body.Resource, nil
}

// levelsForScope assembles the reservation order for one scope: the
// installation level always, plus ancestor organizations, project and worker
// when the scope names them. A scope that names a dimension beyond the
// installation requires the snapshot; a bare installation scope reads only
// the budget table and needs no peer call.
func (s *Service) levelsForScope(ctx context.Context, unit contract.Unit, scope wireScope) ([]level, error) {
	var snap *snapshotScope
	if scope.dimensions() {
		var err error
		snap, err = s.loadSnapshot(ctx, unit, scope)
		if err != nil {
			return nil, err
		}
	}
	return s.buildLevels(ctx, unit, scope, snap)
}

// callConfiguration marshals input, calls one configuration operation on the
// same transaction and returns its data payload. Faults from the peer pass
// through unchanged; a missing ports dependency is a prerequisite failure.
func (s *Service) callConfiguration(ctx context.Context, unit contract.Unit, op string, input any) (json.RawMessage, error) {
	if s.deps.Ports == nil {
		return nil, prerequisiteMissing("configuration ports are unavailable; %s cannot run", op)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("accounting: encode %s call: %w", op, err)
	}
	payload, err := s.deps.Ports.Call(ctx, unit, contract.Invocation{Operation: op, Version: 1, Input: raw})
	if err != nil {
		return nil, err
	}
	if payload.Error != nil {
		return nil, payload.Error
	}
	return payload.Data, nil
}
