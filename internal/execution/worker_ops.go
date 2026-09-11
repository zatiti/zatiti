package execution

import (
	"context"

	"github.com/zatiti/zatiti/internal/contract"
)

// Worker admission gates. Pause immediately blocks new worker admissions —
// claim refuses before any inference or spending — while existing attempts
// and their external effects remain visible. Resume requires current normal
// authority: a worker principal cannot lift its own pause.

// handleWorkerPause is the worker.pause boundary.
func (s *Service) handleWorkerPause(ctx context.Context, unit contract.Unit, in workerGateInput) (contract.Outcome[dispositionBody], error) {
	return s.setWorkerGate(ctx, unit, in, true)
}

// handleWorkerResume is the worker.resume boundary. The caller may not be
// the worker itself: a paused worker must not control its own admission.
func (s *Service) handleWorkerResume(ctx context.Context, unit contract.Unit, in workerGateInput) (contract.Outcome[dispositionBody], error) {
	return s.setWorkerGate(ctx, unit, in, false)
}

// setWorkerGate applies the pause/resume fence with optimistic versioning.
func (s *Service) setWorkerGate(ctx context.Context, unit contract.Unit, in workerGateInput, paused bool) (contract.Outcome[dispositionBody], error) {
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[dispositionBody]{}, err
	}
	if in.ID == "" {
		return contract.Outcome[dispositionBody]{}, invalidInput("worker gate requires a worker identity")
	}
	if !paused && unit.Actor().PrincipalID == in.ID {
		return contract.Outcome[dispositionBody]{}, permissionDenied(
			"worker %s cannot resume its own admission gate", in.ID)
	}
	g, err := loadGate(ctx, unit, in.ID)
	if err != nil {
		return contract.Outcome[dispositionBody]{}, err
	}
	if g.InstallationID != "" && g.InstallationID != in.Scope.InstallationID {
		return contract.Outcome[dispositionBody]{}, permissionDenied(
			"worker %s belongs to another installation", in.ID)
	}
	if g.Version != in.ExpectedVersion {
		return contract.Outcome[dispositionBody]{}, staleVersion(
			"worker gate version %d does not match expected version %d", g.Version, in.ExpectedVersion)
	}
	now := s.now()
	g.InstallationID = in.Scope.InstallationID
	g.Paused = paused
	if paused {
		g.PausedBy = string(unit.Actor().PrincipalID)
	} else {
		g.PausedBy = ""
	}
	g.UpdatedAt = now
	if err := setGate(ctx, unit, g); err != nil {
		return contract.Outcome[dispositionBody]{}, err
	}
	kind := eventWorkerResumed
	state := "resumed"
	if paused {
		kind = eventWorkerPaused
		state = "paused"
	}
	if err := emitTransition(ctx, unit, kind, in.ID, g.Version); err != nil {
		return contract.Outcome[dispositionBody]{}, err
	}
	return completedOutcome(dispositionBody{Resource: wireDisposition{
		ID: in.ID, Version: g.Version, State: state,
	}})
}
