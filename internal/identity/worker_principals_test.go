package identity

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func (e *testEnv) workerSync(workers ...workerPrincipalInput) {
	e.t.Helper()
	e.mustCall(e.owner, opWorkerSync, workerSyncInput{Workers: workers})
}

func (e *testEnv) workerGrants(id contract.ID) []grantOut {
	e.t.Helper()
	payload := e.mustCall(e.owner, opAuthority, authorityInput{PrincipalID: id, Scope: contract.Scope{InstallationID: e.inst}})
	var out resourceOut[authorityOut]
	if err := json.Unmarshal(payload.Data, &out); err != nil {
		e.t.Fatal(err)
	}
	return out.Resource.Grants
}

func (e *testEnv) principal(id contract.ID) principalOut {
	e.t.Helper()
	payload := e.mustCall(e.owner, opAuthority, authorityInput{PrincipalID: id, Scope: contract.Scope{InstallationID: e.inst}})
	var out resourceOut[authorityOut]
	if err := json.Unmarshal(payload.Data, &out); err != nil {
		e.t.Fatal(err)
	}
	return out.Resource.Principal
}

func TestWorkerSyncRegistersExactlyTheStandingGrant(t *testing.T) {
	env := newTestEnv(t)
	worker, org := contract.NewID(), contract.NewID()
	env.workerSync(workerPrincipalInput{WorkerID: worker, OrganizationID: org, Active: true})

	p := env.principal(worker)
	if p.Kind != contract.KindWorker || p.Revoked || p.Scope.OrganizationID != org {
		t.Fatalf("principal = %+v, want a live worker principal in org %s", p, org)
	}
	grants := env.workerGrants(worker)
	if len(grants) != 1 {
		t.Fatalf("grants = %+v, want exactly one", grants)
	}
	g := grants[0]
	if !slices.Equal(g.Capabilities, workerStandingCapabilities()) || slices.Contains(g.Capabilities, "*") ||
		g.Denied || g.ParentGrantID != nil || g.ExpiresAt != nil || g.Scope != (contract.Scope{InstallationID: env.inst}) {
		t.Fatalf("standing grant = %+v, want the exact allowlist, installation-scoped, no wildcard/parent/expiry", g)
	}
	for _, forbidden := range []string{opGrantCreate, opPrincipalCreate, opCredProvision, "configuration.apply", "policy.create"} {
		if slices.Contains(g.Capabilities, forbidden) {
			t.Fatalf("standing grant carries %s", forbidden)
		}
	}

	// Idempotent: a second sync changes nothing.
	env.workerSync(workerPrincipalInput{WorkerID: worker, OrganizationID: org, Active: true})
	if got := env.workerGrants(worker); len(got) != 1 || got[0].ID != g.ID {
		t.Fatalf("grants after re-sync = %+v, want the same single grant", got)
	}
}

func TestWorkerSyncNeverRegrantsOrReactivates(t *testing.T) {
	env := newTestEnv(t)
	worker, org := contract.NewID(), contract.NewID()
	env.workerSync(workerPrincipalInput{WorkerID: worker, OrganizationID: org, Active: true})
	g := env.workerGrants(worker)[0]
	env.mustCall(env.owner, opGrantRevoke, grantRevokeInput{
		Scope: contract.Scope{InstallationID: env.inst}, ID: g.ID, ExpectedVersion: g.Version,
	})
	env.workerSync(workerPrincipalInput{WorkerID: worker, OrganizationID: org, Active: true})
	if got := env.workerGrants(worker); len(got) != 0 {
		t.Fatalf("grants after owner revocation and re-sync = %+v, want none", got)
	}

	env.workerSync(workerPrincipalInput{WorkerID: worker, OrganizationID: org, Active: false})
	if !env.principal(worker).Revoked {
		t.Fatal("inactive sync did not revoke the worker principal")
	}
	env.workerSync(workerPrincipalInput{WorkerID: worker, OrganizationID: org, Active: true})
	if !env.principal(worker).Revoked {
		t.Fatal("sync reactivated a revoked worker principal")
	}
}

func TestWorkerSyncMovesOrganizationAndRefusesNonWorkers(t *testing.T) {
	env := newTestEnv(t)
	worker, org, org2 := contract.NewID(), contract.NewID(), contract.NewID()
	env.workerSync(workerPrincipalInput{WorkerID: worker, OrganizationID: org, Active: true})
	env.workerSync(workerPrincipalInput{WorkerID: worker, OrganizationID: org2, Active: true})
	if got := env.principal(worker).Scope.OrganizationID; got != org2 {
		t.Fatalf("moved principal organization = %s, want %s", got, org2)
	}

	human := env.mustCreatePrincipal("Someone", contract.KindHuman)
	env.wantFault(env.owner, opWorkerSync, workerSyncInput{Workers: []workerPrincipalInput{
		{WorkerID: human.ID, OrganizationID: org, Active: true},
	}}, contract.CodeConflict)
	if env.principal(human.ID).Kind != contract.KindHuman {
		t.Fatal("sync changed a human principal")
	}
}

func TestActivateRegistersAndRetiresWorkersFromTheCandidate(t *testing.T) {
	env := newTestEnv(t)
	worker, org := contract.NewID(), contract.NewID()
	change := func(action string, version int64) json.RawMessage {
		def := map[string]any{
			"id": worker, "version": max(version, 1), "organization_id": org, "key": "helper", "name": "Helper",
			"purpose": "p", "instructions": "i", "skill_versions": []any{}, "bindings": []any{},
			"profile": nil, "limits": nil,
		}
		raw, _ := json.Marshal(map[string]any{"kind": "worker", "action": action, "id": worker, "expected_version": version, "definition": def})
		return raw
	}
	activate := func(c json.RawMessage) {
		env.mustCall(env.owner, opActivate, candidateInput{Candidate: candidate{
			PlanID: contract.NewID(), BaseRevision: 1, CandidateDigest: "0000000000000000000000000000000000000000000000000000000000000000",
			Changes: []json.RawMessage{c}, Dependencies: []refOut{},
		}})
	}
	activate(change("create", 0))
	if p := env.principal(worker); p.Kind != contract.KindWorker || p.Revoked {
		t.Fatalf("created worker principal = %+v", p)
	}
	activate(change("archive", 1))
	if !env.principal(worker).Revoked {
		t.Fatal("archive did not revoke the worker principal")
	}
}
