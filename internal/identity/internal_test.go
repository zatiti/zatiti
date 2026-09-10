package identity

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Tests for the internal operations and the remaining identity acceptance
// cases: authority resolution, promotion activation, restriction enforcement,
// the compiler-candidate no-ops, cross-scope references and forged profiles.

// qualifiedQualification builds a fully-populated qualified qualification
// bound to the given worker and capability.
func qualifiedQualification(workerID contract.ID, capability string, dests []string, now time.Time) qualification {
	return qualification{
		ID:            contract.NewID(),
		Version:       1,
		WorkerID:      workerID,
		Capability:    capability,
		Destinations:  dests,
		Rule:          refOut{ID: contract.NewID(), Version: 1},
		Model:         "test-model",
		ToolVersions:  []refOut{},
		SkillVersions: []refOut{},
		EvidenceIDs:   []contract.ID{},
		WindowStart:   now,
		WindowEnd:     now,
		State:         "qualified",
		Explanation:   "proven under test",
	}
}

// TestAuthorityOutput proves _identity.authority resolves the principal's
// effective grants filtered to the requested scope, lists active restrictions,
// and refuses unknown principals. Grants outside the requested scope are not
// disclosed.
func TestAuthorityOutput(t *testing.T) {
	env := newTestEnv(t)
	agent := env.mustCreatePrincipal("Agent", contract.KindClientAgent)
	install := contract.Scope{InstallationID: env.inst}
	env.mustGrant(agent.ID, install, []string{"principal.create"}, []string{})
	org := contract.NewID()
	env.mustGrant(agent.ID, contract.Scope{InstallationID: env.inst, OrganizationID: org}, []string{"principal.get"}, []string{})

	payload := env.mustCall(env.owner, opAuthority, authorityInput{PrincipalID: agent.ID, Scope: install})
	var out resourceOut[authorityOut]
	decode(t, payload, &out)
	if len(out.Resource.Grants) != 1 || !containsCap(out.Resource.Grants[0].Capabilities, "principal.create") {
		t.Fatalf("installation-scoped authority returned %+v", out.Resource.Grants)
	}
	if len(out.Resource.Restrictions) != 0 {
		t.Fatalf("expected no restrictions, got %v", out.Resource.Restrictions)
	}

	// At the org scope both grants apply.
	orgPayload := env.mustCall(env.owner, opAuthority, authorityInput{
		PrincipalID: agent.ID, Scope: contract.Scope{InstallationID: env.inst, OrganizationID: org},
	})
	var orgOut resourceOut[authorityOut]
	decode(t, orgPayload, &orgOut)
	if len(orgOut.Resource.Grants) != 2 {
		t.Fatalf("org-scoped authority returned %d grants, want 2", len(orgOut.Resource.Grants))
	}

	// A restriction shows up in the authority view.
	env.mustCall(env.owner, opRestrict, restrictInput{
		PrincipalID: agent.ID, Capability: "principal.create", Reason: "under review",
	})
	afterPayload := env.mustCall(env.owner, opAuthority, authorityInput{PrincipalID: agent.ID, Scope: install})
	var afterOut resourceOut[authorityOut]
	decode(t, afterPayload, &afterOut)
	if len(afterOut.Resource.Restrictions) != 1 || afterOut.Resource.Restrictions[0] != "principal.create" {
		t.Fatalf("authority restrictions = %v", afterOut.Resource.Restrictions)
	}

	// Unknown principal: not_found without cross-scope disclosure.
	env.wantFault(env.owner, opAuthority, authorityInput{
		PrincipalID: contract.NewID(), Scope: install,
	}, contract.CodeNotFound)
}

func containsCap(caps []string, want string) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}

// TestPromoteActivatesQualifiedWorker proves promotion: a qualified
// qualification bound to the promoted principal converts the ceiling grant
// into a bounded child grant; the qualification replays exactly once; forged
// or unqualified states are refused; and the ceiling must be effective and
// carry the qualified capability.
func TestPromoteActivatesQualifiedWorker(t *testing.T) {
	env := newTestEnv(t)
	install := contract.Scope{InstallationID: env.inst}
	worker := env.mustCreatePrincipal("Worker", contract.KindWorker)

	// Before promotion the worker has no authority.
	workerAuth := contract.Actor{PrincipalID: worker.ID, Kind: contract.KindWorker}
	env.wantFault(workerAuth, opPrincipalGet, principalGetInput{Scope: install, ID: worker.ID}, contract.CodePermissionDenied)

	ceiling := env.mustGrant(worker.ID, install, []string{"principal.get"}, []string{})
	q := qualifiedQualification(worker.ID, "principal.get", []string{}, env.clock.Now())
	payload := env.mustCall(env.owner, opPromote, promoteInput{
		PrincipalID: worker.ID, Qualification: q, CeilingGrantID: ceiling.ID,
	})
	var out resourceOut[grantOut]
	decode(t, payload, &out)
	g := out.Resource
	if g.ParentGrantID == nil || *g.ParentGrantID != ceiling.ID {
		t.Fatalf("promotion grant parent = %v, want the ceiling", g.ParentGrantID)
	}
	if len(g.Capabilities) != 1 || g.Capabilities[0] != "principal.get" {
		t.Fatalf("promotion grant capabilities = %v", g.Capabilities)
	}

	// The promotion grant authorizes the worker immediately.
	env.mustCall(workerAuth, opPrincipalGet, principalGetInput{Scope: install, ID: worker.ID})

	// The same qualification can never activate twice.
	env.wantFault(env.owner, opPromote, promoteInput{
		PrincipalID: worker.ID, Qualification: q, CeilingGrantID: ceiling.ID,
	}, contract.CodeConflict)

	// An unqualified state is refused.
	unqualified := qualifiedQualification(worker.ID, "principal.get", []string{}, env.clock.Now())
	unqualified.State = "proposed"
	env.wantFault(env.owner, opPromote, promoteInput{
		PrincipalID: worker.ID, Qualification: unqualified, CeilingGrantID: ceiling.ID,
	}, contract.CodeVerificationFailed)

	// A qualification bound to another principal is refused.
	foreign := qualifiedQualification(env.owner.PrincipalID, "principal.get", []string{}, env.clock.Now())
	env.wantFault(env.owner, opPromote, promoteInput{
		PrincipalID: worker.ID, Qualification: foreign, CeilingGrantID: ceiling.ID,
	}, contract.CodeInvalidInput)

	// A ceiling without the qualified capability is refused.
	unrelated := env.mustGrant(worker.ID, install, []string{"principal.list"}, []string{})
	env.wantFault(env.owner, opPromote, promoteInput{
		PrincipalID:    worker.ID,
		Qualification:  qualifiedQualification(worker.ID, "principal.get", []string{}, env.clock.Now()),
		CeilingGrantID: unrelated.ID,
	}, contract.CodePermissionDenied)

	// A revoked ceiling is not effective.
	doomed := env.mustGrant(worker.ID, install, []string{"principal.get"}, []string{})
	env.mustCall(env.owner, opGrantRevoke, grantRevokeInput{
		Scope: install, ID: doomed.ID, ExpectedVersion: 1,
	})
	env.wantFault(env.owner, opPromote, promoteInput{
		PrincipalID:    worker.ID,
		Qualification:  qualifiedQualification(worker.ID, "principal.get", []string{}, env.clock.Now()),
		CeilingGrantID: doomed.ID,
	}, contract.CodePermissionDenied)

	// The promotion row records the activation exactly once per replay key.
	var promotions int64
	err := env.db.Read(context.Background(), env.owner, install, func(unit contract.Unit) error {
		return unit.QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM identity_promotions`).Scan(&promotions)
	})
	if err != nil {
		t.Fatal(err)
	}
	if promotions != 1 {
		t.Fatalf("expected exactly one promotion row, found %d", promotions)
	}
}

// TestRestrictBlocksImmediately proves immediate demotion: a restriction
// blocks the named capability on the next operation, leaves every other
// capability working, is idempotent, and refuses unknown principals.
func TestRestrictBlocksImmediately(t *testing.T) {
	env := newTestEnv(t)
	agent := env.mustCreatePrincipal("Agent", contract.KindClientAgent)
	install := contract.Scope{InstallationID: env.inst}
	env.mustGrant(agent.ID, install, []string{"principal.create", "principal.get"}, []string{})
	agentAuth := contract.Actor{PrincipalID: agent.ID, Kind: contract.KindClientAgent}

	payload := env.mustCall(env.owner, opRestrict, restrictInput{
		PrincipalID: agent.ID, Capability: "principal.create", Reason: "misbehaving",
	})
	var out resourceOut[dispositionOut]
	decode(t, payload, &out)
	if out.Resource.State != "applied" || out.Resource.Version != 1 {
		t.Fatalf("restriction disposition = %+v", out.Resource)
	}

	// The restricted capability is blocked immediately...
	env.wantFault(agentAuth, opPrincipalCreate, principalCreateInput{
		Scope: install,
		Definition: principalDefinition{
			Kind: contract.KindService, Name: "blocked", Scope: install, Revoked: false,
		},
	}, contract.CodePermissionDenied)

	// ...while unrelated capabilities keep working.
	env.mustCall(agentAuth, opPrincipalGet, principalGetInput{Scope: install, ID: agent.ID})

	// Re-applying is idempotent: same restriction, same version.
	again := env.mustCall(env.owner, opRestrict, restrictInput{
		PrincipalID: agent.ID, Capability: "principal.create", Reason: "still misbehaving",
	})
	var againOut resourceOut[dispositionOut]
	decode(t, again, &againOut)
	if againOut.Resource.ID != out.Resource.ID || againOut.Resource.Version != 1 {
		t.Fatalf("idempotent restriction changed the record: %+v", againOut.Resource)
	}

	// Unknown principal: not_found.
	env.wantFault(env.owner, opRestrict, restrictInput{
		PrincipalID: contract.NewID(), Capability: "principal.create", Reason: "nobody",
	}, contract.CodeNotFound)
}

// TestActivateAndValidateNoops proves the compiler-candidate operations are
// honest no-ops for identity: empty version list, empty validation with
// non-nil slices, and no state change.
func TestActivateAndValidateNoops(t *testing.T) {
	env := newTestEnv(t)
	in := candidateInput{Candidate: candidate{
		PlanID:          contract.NewID(),
		BaseRevision:    1,
		CandidateDigest: "0000000000000000000000000000000000000000000000000000000000000000",
		Changes:         []json.RawMessage{},
		Dependencies:    []refOut{},
	}}

	payload := env.mustCall(env.owner, opActivate, in)
	var versions versionsOut
	decode(t, payload, &versions)
	if len(versions.Versions) != 0 {
		t.Fatalf("activate returned %d versions, want 0", len(versions.Versions))
	}

	vpayload := env.mustCall(env.owner, opValidate, in)
	var validation validationOut
	decode(t, vpayload, &validation)
	if validation.Diagnostics == nil || validation.Requirements == nil || validation.Dependencies == nil {
		t.Fatalf("validate returned nil slices: %+v", validation)
	}
	if len(validation.Diagnostics) != 0 || len(validation.Requirements) != 0 || len(validation.Dependencies) != 0 {
		t.Fatalf("validate returned content: %+v", validation)
	}
}

// TestCrossScopePrincipalReference proves Z01.cross_scope_reference for the
// identity domain: a principal can never reference — create, read, or be the
// grantee of — an out-of-scope organization or installation, and profile
// arguments confer nothing.
func TestCrossScopePrincipalReference(t *testing.T) {
	env := newTestEnv(t)
	orgA, orgB := contract.NewID(), contract.NewID()
	agent := env.mustCreatePrincipal("Agent", contract.KindClientAgent, contract.Scope{InstallationID: env.inst, OrganizationID: orgA})
	env.mustGrant(agent.ID, contract.Scope{InstallationID: env.inst, OrganizationID: orgA}, []string{"principal.create", "grant.create"}, []string{})
	agentAuth := contract.Actor{PrincipalID: agent.ID, Kind: contract.KindClientAgent}

	// Referencing a foreign installation in a principal definition is refused.
	env.wantFault(agentAuth, opPrincipalCreate, principalCreateInput{
		Scope: contract.Scope{InstallationID: env.inst, OrganizationID: orgA},
		Definition: principalDefinition{
			Kind: contract.KindService, Name: "foreign", Scope: contract.Scope{InstallationID: contract.NewID()}, Revoked: false,
		},
	}, contract.CodeInvalidInput)

	// Referencing a foreign organization escapes the caller's envelope.
	env.wantFault(agentAuth, opPrincipalCreate, principalCreateInput{
		Scope: contract.Scope{InstallationID: env.inst, OrganizationID: orgA},
		Definition: principalDefinition{
			Kind: contract.KindService, Name: "other-org", Scope: contract.Scope{InstallationID: env.inst, OrganizationID: orgB}, Revoked: false,
		},
	}, contract.CodePermissionDenied)

	// Requests scoped to a foreign installation are refused outright, so no
	// cross-scope data is disclosed through get.
	env.wantFault(agentAuth, opPrincipalGet, principalGetInput{
		Scope: contract.Scope{InstallationID: contract.NewID()}, ID: agent.ID,
	}, contract.CodeInvalidInput)

	// A grant cannot reference a principal from outside this installation:
	// the grantee is unknown here, which is exactly not_found.
	env.wantFault(agentAuth, opGrantCreate, grantCreateInput{
		Scope: contract.Scope{InstallationID: env.inst, OrganizationID: orgA},
		Definition: grantDefinition{
			PrincipalID:  contract.NewID(),
			Scope:        contract.Scope{InstallationID: env.inst, OrganizationID: orgA},
			Capabilities: []string{"principal.get"},
			Destinations: []string{},
			Denied:       false,
		},
	}, contract.CodeNotFound)

	// A forged owner profile name cannot be created: names are reserved.
	env.wantFault(agentAuth, opPrincipalCreate, principalCreateInput{
		Scope: contract.Scope{InstallationID: env.inst, OrganizationID: orgA},
		Definition: principalDefinition{
			Kind: contract.KindHuman, Name: "Ada Owner", Scope: contract.Scope{InstallationID: env.inst, OrganizationID: orgA}, Revoked: false,
		},
	}, contract.CodeConflict)

	// Profile selection through tool arguments is structurally impossible:
	// unknown input fields are rejected by strict decoding and the schema.
	env.wantFault(agentAuth, opPrincipalCreate, map[string]any{
		"scope": contract.Scope{InstallationID: env.inst, OrganizationID: orgA},
		"definition": principalDefinition{
			Kind: contract.KindService, Name: "impersonator", Scope: contract.Scope{InstallationID: env.inst, OrganizationID: orgA}, Revoked: false,
		},
		"profile": "owner",
	}, contract.CodeInvalidInput)
}
