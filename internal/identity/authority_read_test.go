package identity

import (
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// TestAuthorityReadIsGatedByCallerNotCapability: _identity.authority is an
// allowlisted internal read, so a registered, unrevoked actor in this
// installation gets its answer whatever its own grants say — the grants are
// the answer, not the ticket. A revoked actor and a foreign-installation
// request scope are refused.
func TestAuthorityReadIsGatedByCallerNotCapability(t *testing.T) {
	env := newTestEnv(t)
	install := contract.Scope{InstallationID: env.inst}
	agent := env.mustCreatePrincipal("Narrow Agent", contract.KindClientAgent)
	env.mustGrant(agent.ID, install, []string{"task.list"}, []string{})
	agentAuth := contract.Actor{PrincipalID: agent.ID, Kind: contract.KindClientAgent}

	// The narrowly granted agent, as the acting principal, learns its own
	// authority: exactly the one grant it holds, no _identity.authority
	// capability required.
	payload := env.mustCall(agentAuth, opAuthority, authorityInput{PrincipalID: agent.ID, Scope: install})
	var out resourceOut[authorityOut]
	decode(t, payload, &out)
	if out.Resource.Principal.ID != agent.ID || out.Resource.Principal.Revoked {
		t.Fatalf("authority principal = %+v, want the live agent", out.Resource.Principal)
	}
	if len(out.Resource.Grants) != 1 || !containsCap(out.Resource.Grants[0].Capabilities, "task.list") ||
		containsCap(out.Resource.Grants[0].Capabilities, opAuthority) {
		t.Fatalf("authority grants = %+v, want only the task.list grant", out.Resource.Grants)
	}

	// A principal with no grant at all still gets its (empty) answer.
	bare := env.mustCreatePrincipal("Bare Agent", contract.KindClientAgent)
	bareAuth := contract.Actor{PrincipalID: bare.ID, Kind: contract.KindClientAgent}
	payload = env.mustCall(bareAuth, opAuthority, authorityInput{PrincipalID: bare.ID, Scope: install})
	decode(t, payload, &out)
	if len(out.Resource.Grants) != 0 {
		t.Fatalf("ungranted principal reported grants %+v", out.Resource.Grants)
	}

	// A request scope outside this installation is refused before any
	// lookup, whoever asks.
	env.wantFault(agentAuth, opAuthority, authorityInput{
		PrincipalID: agent.ID, Scope: contract.Scope{InstallationID: contract.NewID()},
	}, contract.CodeInvalidInput)

	// An unregistered actor is refused.
	env.wantFault(contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindClientAgent}, opAuthority,
		authorityInput{PrincipalID: agent.ID, Scope: install}, contract.CodePermissionDenied)

	// A revoked actor must not learn its former authority.
	env.mustCall(env.owner, opPrincipalRevoke, principalRevokeInput{Scope: install, ID: agent.ID, ExpectedVersion: agent.Version})
	env.wantFault(agentAuth, opAuthority, authorityInput{PrincipalID: agent.ID, Scope: install}, contract.CodePermissionDenied)
}
