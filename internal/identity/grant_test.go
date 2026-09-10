package identity

import (
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// TestAgentCannotSelfGrant proves Z05.agent_self_grant: a principal can never
// create a grant for itself, no matter what authority it holds.
func TestAgentCannotSelfGrant(t *testing.T) {
	env := newTestEnv(t)
	agent := env.mustCreatePrincipal("Agent", contract.KindClientAgent)
	env.mustGrant(agent.ID, contract.Scope{InstallationID: env.inst}, []string{"grant.create"}, []string{})
	agentAuth := contract.Actor{PrincipalID: agent.ID, Kind: contract.KindClientAgent}

	env.wantFault(agentAuth, opGrantCreate, grantCreateInput{
		Scope: contract.Scope{InstallationID: env.inst},
		Definition: grantDefinition{
			PrincipalID:  agent.ID,
			Scope:        contract.Scope{InstallationID: env.inst},
			Capabilities: []string{"principal.create"},
			Destinations: []string{},
			Denied:       false,
		},
	}, contract.CodePermissionDenied)
}

// TestGrantWideningRefused proves the delegation ceiling: a grant can only
// narrow the authority it flows from — scope, capabilities, destinations and
// expiry must all fit both the caller's envelope and the parent grant.
func TestGrantWideningRefused(t *testing.T) {
	env := newTestEnv(t)
	agent := env.mustCreatePrincipal("Agent", contract.KindClientAgent)
	agentScope := contract.Scope{InstallationID: env.inst, OrganizationID: contract.NewID()}
	expires := env.clock.Now().Add(24 * time.Hour)
	parent := env.mustGrantExpiringAt(agent.ID, agentScope, &expires)
	// The agent also holds the grant.create capability, bounded to the same
	// scope and expiry, so the refusal below comes from the ceiling check
	// rather than from missing admission authority.
	env.mustGrant(agent.ID, agentScope, []string{"grant.create"}, []string{})
	agentAuth := contract.Actor{PrincipalID: agent.ID, Kind: contract.KindClientAgent}
	worker := env.mustCreatePrincipal("Worker", contract.KindWorker)

	// Capability outside the parent grant.
	env.wantFault(agentAuth, opGrantCreate, grantCreateInput{
		Scope: agentScope,
		Definition: grantDefinition{
			PrincipalID:   worker.ID,
			Scope:         agentScope,
			Capabilities:  []string{"grant.create"},
			Destinations:  []string{},
			Denied:        false,
			ExpiresAt:     &expires,
			ParentGrantID: &parent.ID,
		},
	}, contract.CodePermissionDenied)

	// Scope wider than the envelope (installation-wide against an org-scoped
	// envelope).
	env.wantFault(agentAuth, opGrantCreate, grantCreateInput{
		Scope: agentScope,
		Definition: grantDefinition{
			PrincipalID:   worker.ID,
			Scope:         contract.Scope{InstallationID: env.inst},
			Capabilities:  []string{"principal.create"},
			Destinations:  []string{},
			Denied:        false,
			ExpiresAt:     &expires,
			ParentGrantID: &parent.ID,
		},
	}, contract.CodePermissionDenied)

	// A wildcard capability can never flow through a bounded parent.
	env.wantFault(agentAuth, opGrantCreate, grantCreateInput{
		Scope: agentScope,
		Definition: grantDefinition{
			PrincipalID:   worker.ID,
			Scope:         agentScope,
			Capabilities:  []string{"*"},
			Destinations:  []string{},
			Denied:        false,
			ExpiresAt:     &expires,
			ParentGrantID: &parent.ID,
		},
	}, contract.CodePermissionDenied)

	// An unbounded child cannot outlive a bounded parent.
	env.wantFault(agentAuth, opGrantCreate, grantCreateInput{
		Scope: agentScope,
		Definition: grantDefinition{
			PrincipalID:   worker.ID,
			Scope:         agentScope,
			Capabilities:  []string{"principal.create"},
			Destinations:  []string{},
			Denied:        false,
			ParentGrantID: &parent.ID,
		},
	}, contract.CodePermissionDenied)
}

// TestDelegationChain proves live delegation: a live parent authorizes the
// child, the delegated authority works, and revoking the parent kills the
// delegation immediately.
func TestDelegationChain(t *testing.T) {
	env := newTestEnv(t)
	agent := env.mustCreatePrincipal("Agent", contract.KindClientAgent)
	install := contract.Scope{InstallationID: env.inst}
	env.mustGrant(agent.ID, install, []string{"grant.create", "principal.create"}, []string{})
	agentAuth := contract.Actor{PrincipalID: agent.ID, Kind: contract.KindClientAgent}

	// The owner grants the agent a delegable parent grant.
	parent := env.mustGrant(agent.ID, install, []string{"principal.create"}, []string{})

	// The agent delegates to a worker, referencing the parent.
	worker := env.mustCreatePrincipal("Worker", contract.KindWorker)
	payload := env.mustCall(agentAuth, opGrantCreate, grantCreateInput{
		Scope: install,
		Definition: grantDefinition{
			PrincipalID:   worker.ID,
			Scope:         install,
			Capabilities:  []string{"principal.create"},
			Destinations:  []string{},
			Denied:        false,
			ParentGrantID: &parent.ID,
		},
	})
	var delegated resourceOut[grantOut]
	decode(t, payload, &delegated)
	if delegated.Resource.ParentGrantID == nil || *delegated.Resource.ParentGrantID != parent.ID {
		t.Fatalf("delegated grant missing parent: %+v", delegated.Resource)
	}

	// The worker can now act inside its delegation.
	workerAuth := contract.Actor{PrincipalID: worker.ID, Kind: contract.KindWorker}
	env.mustCall(workerAuth, opPrincipalCreate, principalCreateInput{
		Scope: install,
		Definition: principalDefinition{
			Kind: contract.KindService, Name: "svc", Scope: install, Revoked: false,
		},
	})

	// The agent cannot delegate from a grant it does not hold.
	other := env.mustCreatePrincipal("Other", contract.KindClientAgent)
	otherGrant := env.mustGrant(other.ID, install, []string{"principal.create"}, []string{})
	env.wantFault(agentAuth, opGrantCreate, grantCreateInput{
		Scope: install,
		Definition: grantDefinition{
			PrincipalID:   worker.ID,
			Scope:         install,
			Capabilities:  []string{"principal.create"},
			Destinations:  []string{},
			Denied:        false,
			ParentGrantID: &otherGrant.ID,
		},
	}, contract.CodePermissionDenied)

	// Revoking the parent kills the delegation immediately and blocks new
	// delegations from the dead parent.
	env.mustCall(env.owner, opGrantRevoke, grantRevokeInput{
		Scope: install, ID: parent.ID, ExpectedVersion: 1,
	})
	env.wantFault(workerAuth, opPrincipalCreate, principalCreateInput{
		Scope: install,
		Definition: principalDefinition{
			Kind: contract.KindService, Name: "svc2", Scope: install, Revoked: false,
		},
	}, contract.CodePermissionDenied)
	env.wantFault(agentAuth, opGrantCreate, grantCreateInput{
		Scope: install,
		Definition: grantDefinition{
			PrincipalID:   worker.ID,
			Scope:         install,
			Capabilities:  []string{"principal.create"},
			Destinations:  []string{},
			Denied:        false,
			ParentGrantID: &parent.ID,
		},
	}, contract.CodePermissionDenied)
}

// TestOldAuthorityCannotWiden proves Z04.old_authority: after the delegator's
// own authority is revoked, it can no longer widen — or even rewrite — the
// delegations beneath it through grant.update. Authority is always evaluated
// against current grants, never the state at grant creation.
func TestOldAuthorityCannotWiden(t *testing.T) {
	env := newTestEnv(t)
	agent := env.mustCreatePrincipal("Agent", contract.KindClientAgent)
	install := contract.Scope{InstallationID: env.inst}
	expires := env.clock.Now().Add(24 * time.Hour)
	parent := env.mustGrantExpiringAt(agent.ID, install, &expires)
	// Standing grant capabilities let the agent run grant.create and
	// grant.update below; after the parent is revoked the ceiling is what
	// refuses it.
	env.mustGrant(agent.ID, install, []string{"grant.create", "grant.update"}, []string{})
	agentAuth := contract.Actor{PrincipalID: agent.ID, Kind: contract.KindClientAgent}

	// The agent delegates a sub-grant to a worker, bounded by the parent's
	// expiry.
	worker := env.mustCreatePrincipal("Worker", contract.KindWorker)
	child := env.mustCall(agentAuth, opGrantCreate, grantCreateInput{
		Scope: install,
		Definition: grantDefinition{
			PrincipalID:   worker.ID,
			Scope:         install,
			Capabilities:  []string{"principal.get"},
			Destinations:  []string{},
			Denied:        false,
			ExpiresAt:     &expires,
			ParentGrantID: &parent.ID,
		},
	})
	var childOut resourceOut[grantOut]
	decode(t, child, &childOut)

	// Reassignment is refused while the delegator still holds authority.
	env.wantFault(agentAuth, opGrantUpdate, grantUpdateInput{
		Scope: install, ID: childOut.Resource.ID, ExpectedVersion: 1,
		Definition: grantDefinition{
			PrincipalID:  env.owner.PrincipalID,
			Scope:        install,
			Capabilities: []string{"principal.get"},
			Destinations: []string{},
			Denied:       false,
		},
	}, contract.CodeInvalidInput)

	// The owner revokes the agent's parent grant: the delegator's authority
	// is gone.
	env.mustCall(env.owner, opGrantRevoke, grantRevokeInput{
		Scope: install, ID: parent.ID, ExpectedVersion: 1,
	})

	// Widening is refused against the caller's CURRENT (now empty) envelope.
	env.wantFault(agentAuth, opGrantUpdate, grantUpdateInput{
		Scope: install, ID: childOut.Resource.ID, ExpectedVersion: 1,
		Definition: grantDefinition{
			PrincipalID:   worker.ID,
			Scope:         install,
			Capabilities:  []string{"principal.get", "principal.create", "grant.create"},
			Destinations:  []string{},
			Denied:        false,
			ParentGrantID: &parent.ID,
		},
	}, contract.CodePermissionDenied)

	// Even a no-op update fails: the delegator's authority is gone.
	env.wantFault(agentAuth, opGrantUpdate, grantUpdateInput{
		Scope: install, ID: childOut.Resource.ID, ExpectedVersion: 1,
		Definition: grantDefinition{
			PrincipalID:   worker.ID,
			Scope:         install,
			Capabilities:  []string{"principal.get"},
			Destinations:  []string{},
			Denied:        false,
			ParentGrantID: &parent.ID,
		},
	}, contract.CodePermissionDenied)
}

// TestGrantExpiryBoundsAuthority proves an expired grant stops authorizing.
func TestGrantExpiryBoundsAuthority(t *testing.T) {
	env := newTestEnv(t)
	agent := env.mustCreatePrincipal("Agent", contract.KindClientAgent)
	install := contract.Scope{InstallationID: env.inst}
	expires := env.clock.Now().Add(time.Hour)
	env.mustGrantExpiringAt(agent.ID, install, &expires)
	agentAuth := contract.Actor{PrincipalID: agent.ID, Kind: contract.KindClientAgent}

	// Inside the window the agent can act.
	env.mustCall(agentAuth, opPrincipalGet, principalGetInput{Scope: install, ID: agent.ID})

	// Advance the clock past expiry: authority is gone.
	env.clock.Add(25 * time.Hour)
	env.wantFault(agentAuth, opPrincipalGet, principalGetInput{Scope: install, ID: agent.ID}, contract.CodePermissionDenied)
}

// TestGrantListPagination exercises keyset pagination and cursor binding.
func TestGrantListPagination(t *testing.T) {
	env := newTestEnv(t)
	install := contract.Scope{InstallationID: env.inst}
	target := env.mustCreatePrincipal("Paged", contract.KindService)
	// Five grants plus the owner's bootstrap root grant make six rows: three
	// full pages of two.
	for i := 0; i < 5; i++ {
		env.mustGrant(target.ID, install, []string{"principal.get"}, []string{})
	}
	payload := env.mustCall(env.owner, opGrantList, listInput{
		Scope: install, Limit: int64p(2), Filter: &listFilter{},
	})
	var page1 itemsOut[grantOut]
	decode(t, payload, &page1)
	if len(page1.Items) != 2 || payload.NextCursor == nil {
		t.Fatalf("first page: %d items, cursor %v", len(page1.Items), payload.NextCursor)
	}
	page2Payload, err := env.call(env.owner, opGrantList, listInput{
		Scope: install, Limit: int64p(2), Cursor: payload.NextCursor, Filter: &listFilter{},
	})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	var page2 itemsOut[grantOut]
	decode(t, page2Payload, &page2)
	if len(page2.Items) != 2 || page2Payload.NextCursor == nil {
		t.Fatalf("second page: %d items, cursor %v", len(page2.Items), page2Payload.NextCursor)
	}
	page3Payload, err := env.call(env.owner, opGrantList, listInput{
		Scope: install, Limit: int64p(2), Cursor: page2Payload.NextCursor, Filter: &listFilter{},
	})
	if err != nil {
		t.Fatalf("third page: %v", err)
	}
	var page3 itemsOut[grantOut]
	decode(t, page3Payload, &page3)
	if len(page3.Items) != 2 || page3Payload.NextCursor != nil {
		t.Fatalf("third page: %d items, cursor %v", len(page3.Items), page3Payload.NextCursor)
	}

	// A cursor minted under a different filter is rejected.
	env.wantFault(env.owner, opGrantList, listInput{
		Scope: install, Limit: int64p(2), Cursor: payload.NextCursor,
		Filter: &listFilter{State: stringp("active")},
	}, contract.CodeInvalidInput)

	// A cursor presented without its filter is rejected too.
	env.wantFault(env.owner, opGrantList, listInput{
		Scope: install, Cursor: payload.NextCursor,
	}, contract.CodeInvalidInput)
}

// mustGrantExpiringAt grants principal.create and principal.get under the
// owner's root authority with an optional expiry.
func (e *testEnv) mustGrantExpiringAt(principalID contract.ID, scope contract.Scope, expires *time.Time) grantOut {
	e.t.Helper()
	payload := e.mustCall(e.owner, opGrantCreate, grantCreateInput{
		Scope: contract.Scope{InstallationID: e.inst},
		Definition: grantDefinition{
			PrincipalID:  principalID,
			Scope:        scope,
			Capabilities: []string{"principal.create", "principal.get"},
			Destinations: []string{},
			Denied:       false,
			ExpiresAt:    expires,
		},
	})
	var out resourceOut[grantOut]
	decode(e.t, payload, &out)
	return out.Resource
}

func int64p(v int64) *int64    { return &v }
func stringp(v string) *string { return &v }
