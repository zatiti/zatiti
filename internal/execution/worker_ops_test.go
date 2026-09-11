package execution

import (
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Worker admission gates: pause blocks new claims before any spending,
// resume requires normal authority (never the paused worker itself), and the
// gate is version-fenced and installation-bound.

func TestWorkerPauseGate(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	payload := e.mustOK(opWorkerPause, workerGateInput{Scope: e.scope, ID: worker, ExpectedVersion: 1})
	var body dispositionBody
	e.decode(payload.Data, &body)
	if body.Resource.State != "paused" || body.Resource.Version != 2 {
		t.Fatalf("pause disposition %+v", body.Resource)
	}
	gate := e.readGate(worker)
	if gate == nil || !gate.Paused || gate.Version != 2 {
		t.Fatalf("gate after pause %+v", gate)
	}
	if gate.PausedBy != string(e.actor.PrincipalID) {
		t.Fatalf("paused by %q, want the pausing principal", gate.PausedBy)
	}
	events := e.readEvents()
	found := false
	for _, ev := range events {
		if ev.Kind == eventWorkerPaused && ev.ResourceID == string(worker) {
			found = true
		}
	}
	if !found {
		t.Fatalf("worker.paused event missing")
	}
}

func TestWorkerResumeClearsGate(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.mustOK(opWorkerPause, workerGateInput{Scope: e.scope, ID: worker, ExpectedVersion: 1})
	payload := e.mustOK(opWorkerResume, workerGateInput{Scope: e.scope, ID: worker, ExpectedVersion: 2})
	var body dispositionBody
	e.decode(payload.Data, &body)
	if body.Resource.State != "resumed" {
		t.Fatalf("resume disposition %+v", body.Resource)
	}
	gate := e.readGate(worker)
	if gate.Paused || gate.PausedBy != "" {
		t.Fatalf("gate after resume %+v, want unpaused with no paused-by", gate)
	}
}

func TestWorkerSelfResumeDenied(t *testing.T) {
	e := newEnv(t)
	// The paused worker's own principal may not lift its own gate.
	self := e.actor.PrincipalID
	e.mustOK(opWorkerPause, workerGateInput{Scope: e.scope, ID: self, ExpectedVersion: 1})
	f := e.expectFault(opWorkerResume, workerGateInput{Scope: e.scope, ID: self, ExpectedVersion: 2},
		contract.CodePermissionDenied)
	if !strings.Contains(f.Message, "cannot resume its own admission gate") {
		t.Fatalf("fault message %q does not name the self-resume fence", f.Message)
	}
	if gate := e.readGate(self); gate == nil || !gate.Paused {
		t.Fatalf("gate %+v, want still paused after the denied resume", gate)
	}
}

func TestWorkerGateStaleVersion(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.mustOK(opWorkerPause, workerGateInput{Scope: e.scope, ID: worker, ExpectedVersion: 1})
	f := e.expectFault(opWorkerResume, workerGateInput{Scope: e.scope, ID: worker, ExpectedVersion: 1},
		contract.CodeStaleVersion)
	if !strings.Contains(f.Message, "worker gate version") {
		t.Fatalf("fault message %q does not name the gate version fence", f.Message)
	}
}

func TestWorkerGateEmptyIdentity(t *testing.T) {
	e := newEnv(t)
	_ = e.expectFault(opWorkerPause, workerGateInput{Scope: e.scope, ExpectedVersion: 1},
		contract.CodeInvalidInput)
}

func TestWorkerGateCrossInstallation(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	// A gate row left by another installation makes the worker foreign to
	// this caller even though the scope names this installation.
	foreign := e.ids.New()
	e.inWrite(func(unit contract.Unit) error {
		return setGate(e.ctx, unit, &gateRow{
			WorkerID: worker, InstallationID: foreign, Paused: true, Version: 1,
		})
	})
	f := e.expectFault(opWorkerResume, workerGateInput{Scope: e.scope, ID: worker, ExpectedVersion: 1},
		contract.CodePermissionDenied)
	if !strings.Contains(f.Message, "belongs to another installation") {
		t.Fatalf("fault message %q does not name the installation fence", f.Message)
	}
}

func TestWorkerPauseResumeClaimCycle(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.installWorkerSnapshot(worker, fixtureHostedProfile(worker))
	run := e.enqueueTask(worker, nil)
	e.mustOK(opWorkerPause, workerGateInput{Scope: e.scope, ID: worker, ExpectedVersion: 1})
	_ = e.expectFault(opRunClaim, runClaimInput{
		Scope: e.scope, RunID: run.ID, WorkerID: worker,
		ExpectedVersion: 1, Capabilities: []string{"model.steps"},
	}, contract.CodeConflict)
	e.mustOK(opWorkerResume, workerGateInput{Scope: e.scope, ID: worker, ExpectedVersion: 2})
	e.claimRun(run.ID, worker)
}
