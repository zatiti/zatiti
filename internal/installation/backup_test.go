package installation

import (
	"errors"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func TestBackupRequiresInstallationPaused(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	_, err := e.driveLocalIO(opBackup, scopeInput{Scope: e.scope}, true)
	if err == nil {
		t.Fatalf("expected backup to be refused while the installation is running")
	}
	var f *contract.Fault
	if !errors.As(err, &f) || f.Code != contract.CodePrerequisiteMissing {
		t.Fatalf("error = %v, want prerequisite_missing", err)
	}
}

// backup WAL+artifact consistency/encryption/verify: this package cannot
// obtain a consistent point-in-time digest of its own SQLite database (see
// backup.go's doc comment). This test proves every OTHER real piece of the
// pipeline runs -- the installation must be paused first, a durable job is
// created and locally shadowed, and the installation's backup encryption
// key is genuinely minted and custodied -- before the operation honestly
// fails at the one named, unavailable prerequisite instead of fabricating a
// completed backup.
func TestBackupDoesRealWorkThenFailsHonestlyOnTheMissingDatabaseSeam(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.mustOK(opPause, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})

	payload, err := e.driveLocalIO(opBackup, scopeInput{Scope: e.scope}, true)
	if err != nil {
		t.Fatalf("driveLocalIO(backup): %v", err)
	}
	if payload.Status != contract.StatusFailed {
		t.Fatalf("backup status = %q, want failed (a real backup cannot complete under this contract gap)", payload.Status)
	}
	if payload.Error == nil || payload.Error.Code != contract.CodePrerequisiteMissing {
		t.Fatalf("backup error = %v, want prerequisite_missing", payload.Error)
	}

	if _, err := e.secrets.Get(e.ctx, backupKeyRef(e.install)); err != nil {
		t.Fatalf("backup must mint and custody the installation's backup key before hitting the gap: %v", err)
	}

	if len(e.ports.callsOf(peerExecutionJobCreate)) != 1 {
		t.Fatalf("backup must create exactly one durable job")
	}
	if len(e.ports.callsOf(peerExecutionJobRecord)) != 1 {
		t.Fatalf("backup must record the job's terminal disposition")
	}

	var jobCount int64
	var jobState string
	if err := e.db.Read(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		if err := unit.QueryRowContext(e.ctx, `SELECT COUNT(*) FROM installation_jobs WHERE kind = 'backup'`).Scan(&jobCount); err != nil {
			return err
		}
		return unit.QueryRowContext(e.ctx, `SELECT state FROM installation_jobs WHERE kind = 'backup'`).Scan(&jobState)
	}); err != nil {
		t.Fatalf("read backup job: %v", err)
	}
	if jobCount != 1 {
		t.Fatalf("backup jobs = %d, want 1", jobCount)
	}
	if jobState != "failed" {
		t.Fatalf("backup job state = %q, want failed", jobState)
	}
}

func TestBackupWithoutSecretStoreFailsAtPerform(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	e.mustOK(opPause, versionedScopeInput{Scope: e.scope, ExpectedVersion: 1})

	svc, err := New(contract.Dependencies{Clock: e.clock, IDs: e.ids, Ports: e.ports, Blobs: e.blobs})
	if err != nil {
		t.Fatalf("New without secrets: %v", err)
	}
	e.svc = svc

	payload, err := e.driveLocalIO(opBackup, scopeInput{Scope: e.scope}, true)
	if err != nil {
		t.Fatalf("driveLocalIO(backup): %v", err)
	}
	if payload.Status != contract.StatusFailed || payload.Error == nil || payload.Error.Code != contract.CodePrerequisiteMissing {
		t.Fatalf("backup without a secret store: status=%q error=%v, want failed/prerequisite_missing", payload.Status, payload.Error)
	}
}
