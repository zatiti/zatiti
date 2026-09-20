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
// The rotation probe completes through the same _connections.validation.record
// path connection.validate uses, so its callback ownership is recorded the
// same way.
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
	job, err := s.createJob(ctx, unit, "connection.rotate", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := s.recordPendingProbe(ctx, unit, pendingProbeRow{
		ConnectionID: row.ID, JobID: job.ID, JobVersion: job.Version, Kind: "rotate",
	}, s.clock.Now()); err != nil {
		return contract.Payload{}, err
	}
	return s.completed(jobOut{Resource: job})
}

// handleValidatePublic admits a bounded, separately authorized provider
// probe for the connection's current credential. It generates the exact
// connection.validate action the connection's selected built-in adapter
// supports (implementation assignment step 3): never an invented generic
// probe sent to an adapter that accepts only domain-specific actions.
// Unmapped providers, and adapters whose every action needs a specific
// resource a Connection definition does not carry, refuse
// capability_unsupported instead of fabricating one. The observed account
// and scopes are recorded through _connections.validation.record, which
// completes this job (step 4) once the observation lands; mismatch cannot
// silently substitute accounts.
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
	toolName, ok := providerValidationTool[row.Provider]
	if !ok {
		return contract.Payload{}, capabilityUnsupportedFault(
			"provider %q names no built-in validation adapter", row.Provider)
	}
	action, faultErr := buildValidationAction(toolName, row)
	if faultErr != nil {
		return contract.Payload{}, faultErr
	}
	tool, found, terr := s.loadContractByName(ctx, unit, toolName)
	if terr != nil {
		return contract.Payload{}, terr
	}
	if !found {
		return contract.Payload{}, internalError("built-in tool %q is not seeded", toolName)
	}
	if verr := contract.ValidateSchema(tool.InputSchema, action); verr != nil {
		return contract.Payload{}, internalError(
			"generated connection.validate action does not match the %s adapter schema: %v", toolName, verr)
	}
	probeInput, err := marshalData(struct {
		Scope           wireScope       `json:"scope"`
		ConnectionID    contract.ID     `json:"connection_id"`
		ExpectedVersion int64           `json:"expected_version"`
		Tool            wireRef         `json:"tool"`
		Action          json.RawMessage `json:"action"`
	}{Scope: in.Scope, ConnectionID: in.ID, ExpectedVersion: in.ExpectedVersion,
		Tool: wireRef{ID: tool.ID, Version: tool.Version}, Action: action})
	if err != nil {
		return contract.Payload{}, err
	}
	job, err := s.createJob(ctx, unit, "connection.validate", probeInput)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := s.recordPendingProbe(ctx, unit, pendingProbeRow{
		ConnectionID: row.ID, JobID: job.ID, JobVersion: job.Version, Kind: "validate",
	}, s.clock.Now()); err != nil {
		return contract.Payload{}, err
	}
	return s.completed(jobOut{Resource: job})
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
// returns the created Job resource. The stored input is the original
// operation input (or, for connection.validate, the generated adapter
// action alongside it), so the controller replays the exact probe
// parameters.
func (s *Service) createJob(ctx context.Context, unit contract.Unit, operation string, input json.RawMessage) (wireJob, error) {
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
		Input:     input,
		SourceID:  s.ids.New(),
	})
	if err != nil {
		return wireJob{}, err
	}
	payload, err := s.ports.Call(ctx, unit, contract.Invocation{
		Operation: "_execution.job.create",
		Version:   1,
		Input:     inRaw,
	})
	if err != nil {
		return wireJob{}, err
	}
	var out jobOut
	if err := contract.DecodeStrict(payload.Data, &out); err != nil {
		return wireJob{}, internalError("job creation result decoding failed: %v", err)
	}
	return out.Resource, nil
}

// completeJob calls _execution.job.record to complete the execution-side job
// callback-owned by one connection probe (implementation assignment step 4).
// evidence_ids stays empty: connections records no artifact evidence for a
// validation/rotation probe, only the domain-corrected Connection result.
func (s *Service) completeJob(ctx context.Context, unit contract.Unit, jobID contract.ID, expectedVersion int64, state string, result any) error {
	resultRaw, err := marshalData(result)
	if err != nil {
		return err
	}
	inRaw, err := marshalData(struct {
		JobID           contract.ID     `json:"job_id"`
		ExpectedVersion int64           `json:"expected_version"`
		Generation      int64           `json:"generation"`
		State           string          `json:"state"`
		Result          json.RawMessage `json:"result"`
		EvidenceIDs     []contract.ID   `json:"evidence_ids"`
	}{
		JobID: jobID, ExpectedVersion: expectedVersion, Generation: unit.Generation(),
		State: state, Result: resultRaw, EvidenceIDs: []contract.ID{},
	})
	if err != nil {
		return err
	}
	_, err = s.ports.Call(ctx, unit, contract.Invocation{
		Operation: "_execution.job.record",
		Version:   1,
		Input:     inRaw,
	})
	return err
}
