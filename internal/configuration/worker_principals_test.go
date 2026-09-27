package configuration

import (
	"encoding/json"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func workerSyncCalls(t *testing.T, env *testEnv) [][]workerPrincipalRef {
	t.Helper()
	var out [][]workerPrincipalRef
	for _, c := range env.ports.callsOf("_identity.worker.sync") {
		var in struct {
			Workers []workerPrincipalRef `json:"workers"`
		}
		if err := json.Unmarshal(c.Input, &in); err != nil {
			t.Fatal(err)
		}
		out = append(out, in.Workers)
	}
	return out
}

// Bootstrap registers the chief's principal in the same transaction, and
// the controller's backfill passes every configured worker to identity.
func TestBootstrapAndBackfillRegisterWorkerPrincipals(t *testing.T) {
	env := newEnv(t)
	calls := workerSyncCalls(t, env)
	if len(calls) != 1 || len(calls[0]) != 1 ||
		calls[0][0] != (workerPrincipalRef{WorkerID: env.chief, OrganizationID: env.org, Active: true}) {
		t.Fatalf("bootstrap worker syncs = %+v, want exactly the active chief", calls)
	}

	env.mustOK("_configuration.worker.principals.sync", workerPrincipalsSyncIn{InstallationID: env.install})
	calls = workerSyncCalls(t, env)
	if len(calls) != 2 || len(calls[1]) != 1 || calls[1][0].WorkerID != env.chief {
		t.Fatalf("backfill worker syncs = %+v, want the chief passed again", calls)
	}

	other := contract.NewID()
	env.expectFault("_configuration.worker.principals.sync", workerPrincipalsSyncIn{InstallationID: other}, contract.CodePermissionDenied)
}
