package installation

import (
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func TestStatusAndDoctorAgreeAndReportRequirements(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()

	for _, op := range []string{opStatus, opDoctor} {
		payload := mustQueryOK(t, e, op, scopeInput{Scope: e.scope})
		var out resourceOut[wireStatus]
		e.decode(payload.Data, &out)
		if !out.Resource.Initialized || out.Resource.Paused || out.Resource.Maintenance {
			t.Fatalf("%s: unexpected status %+v", op, out.Resource)
		}
		if out.Resource.Version != 1 {
			t.Fatalf("%s: version = %d, want 1", op, out.Resource.Version)
		}
	}

	// An unconfigured currency/budget surfaces as a visible requirement, not
	// a silent zero.
	e.ports.set(peerAccountingInspect, func(contract.Invocation) (contract.Payload, error) {
		return okPayload(accountingInspectOutput{Limits: peerLimits{}, Usage: peerUsage{}})
	})
	payload := mustQueryOK(t, e, opStatus, scopeInput{Scope: e.scope})
	var out resourceOut[wireStatus]
	e.decode(payload.Data, &out)
	found := false
	for _, r := range out.Resource.Requirements {
		if r.Code == "prerequisite_missing" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an unconfigured budget to surface as a requirement, got %+v", out.Resource.Requirements)
	}
}

func TestStatusDegradesRatherThanFailingWhenPeersAreUnavailable(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.ports.set(peerEffectsPending, func(contract.Invocation) (contract.Payload, error) {
		return failPayload(contract.CodeControllerUnavailable, "effects offline")
	})
	payload := mustQueryOK(t, e, opStatus, scopeInput{Scope: e.scope})
	var out resourceOut[wireStatus]
	e.decode(payload.Data, &out)
	found := false
	for _, r := range out.Resource.Requirements {
		if r.Code == "effects_unavailable" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an effects_unavailable requirement, got %+v", out.Resource.Requirements)
	}
}

func TestPauseBlocksAdmissionAndIsVersioned(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()

	payload := e.mustOK(opPause, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})
	var out resourceOut[wireStatus]
	e.decode(payload.Data, &out)
	if !out.Resource.Paused || out.Resource.Maintenance {
		t.Fatalf("after pause: %+v", out.Resource)
	}
	if out.Resource.Version != 2 {
		t.Fatalf("version after pause = %d, want 2", out.Resource.Version)
	}

	// A stale expected_version is rejected.
	_ = e.expectFault(opPause, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1}, contract.CodeStaleVersion)
}

func TestMaintenanceEnterFencesAttemptsAndImpliesPause(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()

	payload := e.mustOK(opMaintenanceEnter, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})
	var out resourceOut[wireStatus]
	e.decode(payload.Data, &out)
	if !out.Resource.Paused || !out.Resource.Maintenance {
		t.Fatalf("after maintenance.enter: %+v", out.Resource)
	}
	if len(e.ports.callsOf(peerExecutionFence)) != 1 {
		t.Fatalf("maintenance.enter must fence current attempts exactly once")
	}
}

func TestResumeClearsRestrictionsAndRefusesWithUnresolvedRestore(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.mustOK(opMaintenanceEnter, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})

	// Seed an unresolved restore job directly, as a prior installation.restore
	// call would have left one pending.
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		return insertJob(e.ctx, unit, jobRow{
			ID: e.ids.New(), Version: 1, InstallationID: e.install, Kind: "restore", State: "pending",
			Requirements: []wireRequirement{}, CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now(),
		})
	}); err != nil {
		t.Fatalf("seed restore job: %v", err)
	}

	_ = e.expectFault(opResume, versionedScopeInput{Scope: e.scope, ExpectedVersion: 2}, contract.CodePrerequisiteMissing)

	// Resolving the restore job lets resume proceed.
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		_, err := unit.ExecContext(e.ctx, `UPDATE installation_jobs SET state = 'succeeded' WHERE installation_id = ?`,
			string(e.install))
		return err
	}); err != nil {
		t.Fatalf("resolve restore job: %v", err)
	}
	payload := e.mustOK(opResume, versionedScopeInput{Scope: e.scope, ExpectedVersion: 2})
	var out resourceOut[wireStatus]
	e.decode(payload.Data, &out)
	if out.Resource.Paused || out.Resource.Maintenance {
		t.Fatalf("after resume: %+v", out.Resource)
	}
}

func TestJobGetAnswersFromLocalShadowAndScopesByInstallation(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	jobID := e.ids.New()
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		return insertJob(e.ctx, unit, jobRow{
			ID: jobID, Version: 1, InstallationID: e.install, Kind: "backup", State: "pending",
			Requirements: []wireRequirement{}, CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now(),
		})
	}); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	payload := mustQueryOK(t, e, opJobGet, jobGetInput{Scope: e.scope, ID: jobID})
	var out resourceOut[wireJob]
	e.decode(payload.Data, &out)
	if out.Resource.ID != jobID || out.Resource.Kind != "backup" || out.Resource.State != "pending" {
		t.Fatalf("job.get returned %+v", out.Resource)
	}

	_ = e.expectQueryFault(opJobGet, jobGetInput{Scope: e.scope, ID: e.ids.New()}, contract.CodeNotFound)
}

// mustQueryOK runs a query operation and requires a completed payload.
func mustQueryOK(t *testing.T, e *testEnv, op string, in any) contract.Payload {
	t.Helper()
	payload, err := e.callQuery(op, in)
	if err != nil {
		t.Fatalf("%s failed: %v", op, err)
	}
	if payload.Status != contract.StatusCompleted {
		t.Fatalf("%s status %q, want completed (error %v)", op, payload.Status, payload.Error)
	}
	return payload
}
