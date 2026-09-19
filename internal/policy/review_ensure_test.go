package policy

import (
	"encoding/json"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// checkRead runs _policy.check under a read snapshot, the way a public
// query's gate does.
func (e *testEnv) checkRead(scope contract.Scope, capability string) wirePolicyResult {
	e.t.Helper()
	raw, err := json.Marshal(checkInput{Scope: scope, Capability: capability})
	if err != nil {
		e.t.Fatalf("marshal: %v", err)
	}
	var payload contract.Payload
	err = e.db.Read(e.ctx, e.actor, scope, func(unit contract.Unit) error {
		p, err := e.svc.Handle(e.ctx, unit, contract.Invocation{Operation: opCheck, Version: 1, Input: raw})
		payload = p
		return err
	})
	if err != nil || payload.Status != contract.StatusCompleted {
		e.t.Fatalf("read check: %v (status %q)", err, payload.Status)
	}
	var out policyResultBody
	e.decode(payload.Data, &out)
	return out.Resource
}

// TestDefaultReviewClassEnsuresTheReviewAndConsultsIt: a capability in a
// default review class (grant.create) has its exact review ensured at the
// gate, keyed by the digest of the deterministic capability action, so an
// eligible decision can exist; the next check consults that review and an
// approval turns the decision into allow.
func TestDefaultReviewClassEnsuresTheReviewAndConsultsIt(t *testing.T) {
	e := newEnv(t)
	got := e.check(e.scope, "grant.create", nil, "")
	expectDecision(t, got, decisionReview, "capability grant.create is a default review class")
	if len(got.Requirements) != 1 {
		t.Fatalf("requirements = %v, want one", got.Requirements)
	}
	digest := got.Requirements[0].ActionDigest

	ensures := e.ports.callsOf("_reviews.ensure")
	if len(ensures) != 1 {
		t.Fatalf("_reviews.ensure calls = %d, want 1", len(ensures))
	}
	var in reviewsEnsureInput
	if err := contract.DecodeStrict(ensures[0].Input, &in); err != nil {
		t.Fatalf("ensure input: %v", err)
	}
	if in.Requirement.ActionDigest != digest || in.Scope != e.scope || in.Action.Scope != e.scope {
		t.Fatalf("ensure bound digest %s scope %+v action scope %+v, want %s %+v", in.Requirement.ActionDigest, in.Scope, in.Action.Scope, digest, e.scope)
	}
	if reviewsDigest(t, in.Action) != digest {
		t.Fatalf("the requirement digest is not the reviews owner's digest of the ensured action")
	}
	var params struct {
		Capability string `json:"capability"`
	}
	if err := json.Unmarshal(in.Action.Parameters, &params); err != nil || params.Capability != "grant.create" {
		t.Fatalf("ensured action parameters %s do not name the capability", in.Action.Parameters)
	}
	if in.Action.Destination != "grant.create" || in.Action.ConfigurationRevision != 7 {
		t.Fatalf("ensured action = %+v, want the capability as destination under configuration revision 7", in.Action)
	}

	// The review is pending: the decision stays review, the digest is
	// stable, and re-ensuring is idempotent.
	again := e.check(e.scope, "grant.create", nil, "")
	expectDecision(t, again, decisionReview, "")
	if again.Requirements[0].ActionDigest != digest {
		t.Fatalf("digest changed between checks: %s then %s", digest, again.Requirements[0].ActionDigest)
	}
	if n := e.ports.countOf("_reviews.check"); n != 2 {
		t.Fatalf("_reviews.check calls = %d, want one per check", n)
	}

	// An eligible approval of exactly that review clears the requirement.
	e.ports.approve(t, digest)
	approved := e.check(e.scope, "grant.create", nil, "")
	expectDecision(t, approved, decisionAllow, "exact review "+digest+" is approved")
	if len(approved.Requirements) != 0 {
		t.Fatalf("approved review must clear the requirement: %v", approved.Requirements)
	}

	// A different scope is a different exact action.
	other := e.check(e.orgScope(e.org), "grant.create", nil, "")
	expectDecision(t, other, decisionReview, "")
	if other.Requirements[0].ActionDigest == digest {
		t.Fatalf("a different scope must not share the review digest")
	}
}

// TestReadSnapshotCheckDoesNotEnsure: under a read snapshot (a public
// query's gate) the requirement is still reported but no review mutation
// is attempted.
func TestReadSnapshotCheckDoesNotEnsure(t *testing.T) {
	e := newEnv(t)
	got := e.checkRead(e.scope, "grant.create")
	expectDecision(t, got, decisionReview, "")
	if len(got.Requirements) != 1 {
		t.Fatalf("requirements = %v, want one", got.Requirements)
	}
	if n := e.ports.countOf("_reviews.ensure"); n != 0 {
		t.Fatalf("_reviews.ensure calls under a read snapshot = %d, want 0", n)
	}
}

// TestHumanActorIsAnEligibleReviewer: the requirement's eligible principals
// are the ancestor chiefs plus the requesting human itself, so an owner can
// decide the review its own request created; workers, agents and services
// never make themselves eligible.
func TestHumanActorIsAnEligibleReviewer(t *testing.T) {
	e := newEnv(t)
	for kind, want := range map[string][]contract.ID{
		contract.KindHuman:   {e.chief, e.owner},
		contract.KindService: {e.chief},
		"worker":             {e.chief},
		"client_agent":       {e.chief},
	} {
		auth := e.ports.authority
		auth.Principal.Kind = kind
		e.ports.setAuthority(auth)
		actor := contract.Actor{PrincipalID: e.owner, Kind: kind}
		payload := e.mustOKAs(actor, e.scope, opCheck, checkInput{Scope: e.scope, Capability: "grant.create"})
		var out policyResultBody
		e.decode(payload.Data, &out)
		if len(out.Resource.Requirements) != 1 {
			t.Fatalf("%s: requirements = %v", kind, out.Resource.Requirements)
		}
		got := out.Resource.Requirements[0].EligiblePrincipals
		if len(got) != len(want) {
			t.Fatalf("%s: eligible = %v, want %v", kind, got, want)
		}
		seen := map[contract.ID]bool{}
		for _, id := range got {
			seen[id] = true
		}
		for _, id := range want {
			if !seen[id] {
				t.Fatalf("%s: eligible = %v, want %v", kind, got, want)
			}
		}
	}
}

// TestExactActionDigestIsTheReviewsDigest: the digest policy binds to an
// exact action is the reviews owner's digest of that action (canonical JSON),
// so a review ensured for it is the review policy later consults.
func TestExactActionDigestIsTheReviewsDigest(t *testing.T) {
	e := newEnv(t)
	action := e.validAction(e.scope)
	if got, want := exactActionDigest(action), reviewsDigest(t, action); got != want {
		t.Fatalf("exactActionDigest = %s, want the canonical reviews digest %s", got, want)
	}
}
