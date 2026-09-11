package scheduling

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Peer calls. Scheduling never guesses peer state: staging hands sealed
// changes to configuration's compiler, worker dependencies resolve through
// configuration's scope snapshot, and admitted work lands through tasks,
// accounting and execution. A peer fault is returned unchanged so the
// surrounding transaction rolls back with the peer's own code.

// descriptorVersion (service.go) is the version this module pins on every
// outgoing call.

// callPeer invokes one peer operation and returns its completed payload
// data. A payload fault is returned directly; a transport failure wraps the
// underlying error.
func (s *Service) callPeer(ctx context.Context, unit contract.Unit, operation string, input any) (json.RawMessage, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("scheduling: encode %s input: %w", operation, err)
	}
	payload, err := s.deps.Ports.Call(ctx, unit, contract.Invocation{
		Operation: operation,
		Version:   descriptorVersion,
		Input:     raw,
	})
	if err != nil {
		return nil, err
	}
	if payload.Error != nil {
		return nil, payload.Error
	}
	return payload.Data, nil
}

// stageChange hands one sealed typed change to configuration's compiler and
// returns the resulting draft.
func (s *Service) stageChange(ctx context.Context, unit contract.Unit, scope contract.Scope, change wireChange, draftID contract.ID) (wireDraft, error) {
	data, err := s.callPeer(ctx, unit, "_configuration.stage", stageCallInput{
		Scope:   scope,
		Change:  change,
		DraftID: draftID,
	})
	if err != nil {
		return wireDraft{}, err
	}
	var body draftResource
	if err := json.Unmarshal(data, &body); err != nil {
		return wireDraft{}, fmt.Errorf("scheduling: decode configuration stage draft: %w", err)
	}
	return body.Resource, nil
}

// callSnapshot reads configuration's scope snapshot: the scope-level worker,
// if any, and the scope bindings that may bind a worker by target.
func (s *Service) callSnapshot(ctx context.Context, unit contract.Unit, scope contract.Scope) (snapshotResource, error) {
	data, err := s.callPeer(ctx, unit, "_configuration.snapshot", snapshotCallInput{Scope: scope})
	if err != nil {
		return snapshotResource{}, err
	}
	var body snapshotBody
	if err := json.Unmarshal(data, &body); err != nil {
		return snapshotResource{}, fmt.Errorf("scheduling: decode configuration scope snapshot: %w", err)
	}
	return body.Resource, nil
}

// requireMatchingInstallation rejects definitions homed in another
// installation; cross-installation scheduling is never visible here.
func requireMatchingInstallation(unit contract.Unit, scope contract.Scope) error {
	if scope.InstallationID != unit.Scope().InstallationID {
		return invalidInput("definition scope installation does not match the transaction scope")
	}
	return nil
}
