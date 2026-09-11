package connections

import (
	"context"
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// Setup status and the asynchronous rotate/validate public operations.
// Rotate and validate admit governed provider probes: the handler stages a
// durable job through _execution.job.create and passes it through; the
// controller later claims the job and invokes the owner's local IO seam.
// No network or secret access ever happens inside a Unit.

type connRotateIn struct {
	Scope           wireScope   `json:"scope"`
	ID              contract.ID `json:"id"`
	ExpectedVersion int64       `json:"expected_version"`
	StoreRef        string      `json:"store_ref"`
}

type connValidateIn struct {
	Scope           wireScope   `json:"scope"`
	ID              contract.ID `json:"id"`
	ExpectedVersion int64       `json:"expected_version"`
}

type jobOut struct {
	Resource wireJob `json:"resource"`
}

type setupStatusIn struct {
	Scope       wireScope   `json:"scope"`
	ChallengeID contract.ID `json:"challenge_id"`
}

// handleSetupStatus reports one challenge exactly. A live challenge whose
// expiry has passed reports state expired in the response: the derived view
// is the honest observation even before any writer persists the terminal
// transition. Completion and cancellation recheck expiry against the clock.
func handleSetupStatus(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[setupStatusIn](s, "connection.setup.status", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if f := checkInstallation(unit, in.Scope); f != nil {
		return contract.Payload{}, f
	}
	row, found, lerr := s.loadChallenge(ctx, unit, in.ChallengeID)
	if lerr != nil {
		return contract.Payload{}, lerr
	}
	if !found || row.InstallationID != unit.Scope().InstallationID {
		return contract.Payload{}, notFound("challenge %s is unknown in this installation", in.ChallengeID)
	}
	state := row.State
	if (state == challengePending || state == challengeExternalActionRequired) &&
		!row.ExpiresAt.After(s.clock.Now()) {
		state = challengeExpired
	}
	return s.completed(resourceChallengeOut{Resource: wireChallenge{
		ID:           row.ID,
		Version:      row.Version,
		ConnectionID: row.ConnectionID,
		State:        state,
		ExpiresAt:    row.ExpiresAt,
		ConsentURL:   row.ConsentURL,
		HelperRef:    row.HelperRef,
		Requirements: row.Requirements,
	}})
}

// resourceChallengeOut is the {resource: Challenge} output body.
type resourceChallengeOut struct {
	Resource wireChallenge `json:"resource"`
}

// handleRotate admits a same-account rotation probe. The stored job carries
// the original input (including the new credential's store reference); the
// controller invokes the owner outside transactions to validate the account
// identity before any replacement. Account substitution is never a rotation.
func handleRotate(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[connRotateIn](s, "connection.rotate", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if f := checkInstallation(unit, in.Scope); f != nil {
		return contract.Payload{}, f
	}
	row, err := s.loadConnectionChecked(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !scopeCovers(in.Scope.toContract(), row.Scope) {
		return contract.Payload{}, notFound("connection %s is unknown in this scope", in.ID)
	}
	if f := refuseInactive(row); f != nil {
		return contract.Payload{}, f
	}
	if row.Version != in.ExpectedVersion {
		return contract.Payload{}, staleVersion("connection %s version %d does not match expected version %d",
			in.ID, row.Version, in.ExpectedVersion)
	}
	return s.createJob(ctx, unit, "connection.rotate", inv.Input)
}

// handleValidatePublic admits a bounded, separately authorized provider
// probe for the connection's current credential. The observed account and
// scopes are recorded through _connections.validation.record; mismatch
// cannot silently substitute accounts.
func handleValidatePublic(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[connValidateIn](s, "connection.validate", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if f := checkInstallation(unit, in.Scope); f != nil {
		return contract.Payload{}, f
	}
	row, err := s.loadConnectionChecked(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !scopeCovers(in.Scope.toContract(), row.Scope) {
		return contract.Payload{}, notFound("connection %s is unknown in this scope", in.ID)
	}
	if f := refuseInactive(row); f != nil {
		return contract.Payload{}, f
	}
	if row.Version != in.ExpectedVersion {
		return contract.Payload{}, staleVersion("connection %s version %d does not match expected version %d",
			in.ID, row.Version, in.ExpectedVersion)
	}
	return s.createJob(ctx, unit, "connection.validate", inv.Input)
}

// refuseInactive blocks new external work on archived or revoked
// connections; retained work and obligations stay visible.
func refuseInactive(row connectionRow) *contract.Fault {
	if row.LifecycleState != connLifecycleActive {
		return prerequisiteMissing("connection %s is archived and admits no new external work", row.ID)
	}
	if row.ValidationState == connStateRevoked {
		return prerequisiteMissing("connection %s is revoked and admits no new external work", row.ID)
	}
	return nil
}

// createJob stages a governed durable job through _execution.job.create and
// passes the Job resource through. The stored input is the original
// operation input, so the controller replays the exact probe parameters.
func (s *Service) createJob(ctx context.Context, unit contract.Unit, operation string, input []byte) (contract.Payload, error) {
	inRaw, err := marshalData(struct {
		Scope     wireScope       `json:"scope"`
		Owner     string          `json:"owner"`
		Operation string          `json:"operation"`
		Input     json.RawMessage `json:"input"`
		SourceID  contract.ID     `json:"source_id"`
	}{
		Scope:     scopeFromContract(unit.Scope()),
		Owner:     ownerName,
		Operation: operation,
		Input:     json.RawMessage(input),
		SourceID:  s.ids.New(),
	})
	if err != nil {
		return contract.Payload{}, err
	}
	payload, err := s.ports.Call(ctx, unit, contract.Invocation{
		Operation: "_execution.job.create",
		Version:   1,
		Input:     inRaw,
	})
	if err != nil {
		return contract.Payload{}, err
	}
	var out jobOut
	if err := contract.DecodeStrict(payload.Data, &out); err != nil {
		return contract.Payload{}, internalError("job creation result decoding failed: %v", err)
	}
	return s.completed(out)
}
