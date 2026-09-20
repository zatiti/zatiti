package policy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Behavioral tests for P07: worker-subject authority intersection over model
// calls and proposals, resolved-context classification/destination fencing
// before model dispatch, and exact-action review binding. Every fence here
// runs entirely inside _policy.check, before any effect is admitted or any
// provider is ever dispatched.

// TestEmptyToolBindingsDenyExternalToolCall proves Z05.empty_bindings for
// the worker-subject tool-invocation path: a worker's own broad standing
// capability grant (even a wildcard) never substitutes for an explicit tool
// binding naming the exact tool, and a binding that names a different tool
// does not rescue the call either. Only a binding naming the exact tool
// admits it. Removing the toolBound fence (or forgetting the target-id
// match) makes this fail, since the wildcard grant alone would otherwise
// admit every case.
func TestEmptyToolBindingsDenyExternalToolCall(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.bindWorker(worker, "model-x")
	// The worker's own resolved authority is a standing wildcard capability
	// grant -- the broadest possible administrative entitlement -- so any
	// denial below is attributable only to the missing tool binding.
	e.ports.setAuthority(authorityResource{
		Principal: peerPrincipal{ID: worker, Version: 1, Kind: "worker", Scope: e.scope},
		Grants:    []peerGrant{allowGrant(e.ids.New(), worker, contract.Scope{}, []string{capWildcard}, nil)},
	})
	scope := e.workerScope(worker)
	action := e.validAction(scope)
	actor := e.workerActor(worker)

	// No tool binding at all: the call is refused before any provider
	// dispatch, even though the wildcard grant would otherwise admit
	// "tool.invoke" outright and connections discovery would list the tool.
	got := e.checkAs(actor, scope, "tool.invoke", &action, "")
	expectDecision(t, got, decisionDeny, "no explicit tool binding")

	// A tool binding exists, but names a different tool: discovery of that
	// other bound tool grants nothing for this one.
	e.ports.setBindings([]peerBinding{{
		ID: e.ids.New(), Version: 1, Scope: contract.Scope{},
		Kind: "tool", TargetID: e.ids.New(), Permissions: []string{"tool.invoke"},
	}})
	got = e.checkAs(actor, scope, "tool.invoke", &action, "")
	expectDecision(t, got, decisionDeny, "no explicit tool binding")

	// Binding the exact tool the action names admits the call.
	e.ports.setBindings([]peerBinding{{
		ID: e.ids.New(), Version: 1, Scope: contract.Scope{},
		Kind: "tool", TargetID: action.Tool.ID, Permissions: []string{"tool.invoke"},
	}})
	got = e.checkAs(actor, scope, "tool.invoke", &action, "")
	expectDecision(t, got, decisionAllow, "")

	// The same missing-binding fence applies to a non-worker capability
	// name too: it is the actor kind and the action's tool reference that
	// trigger it, not a fixed capability string.
	e.ports.setBindings(nil)
	got = e.checkAs(actor, scope, "document.read", &action, "")
	expectDecision(t, got, decisionDeny, "no explicit tool binding")
}

// TestRestrictedAttachmentDeniesPublicModelDispatch proves the required
// context-disclosure fence: a worker whose execution profile is classified
// public may not disclose restricted-classified project content -- whether
// it originated as a skill, an attachment, a tool result or a memory
// excerpt, all of which arrive at this boundary as staged content
// references -- and the request never reaches a provider. Emptying the
// disclosed content, or narrowing the project's classification, is
// unaffected.
func TestRestrictedAttachmentDeniesPublicModelDispatch(t *testing.T) {
	e := newEnv(t)
	worker := e.ids.New()
	e.bindWorkerProfile(worker, "model-x", classificationPublic)
	e.ports.setProject(&peerProject{ID: e.ids.New(), OrganizationID: e.org, Classification: classificationRestricted})
	e.ports.setAuthority(authorityResource{
		Principal: peerPrincipal{ID: worker, Version: 1, Kind: "worker", Scope: e.scope},
		Grants:    []peerGrant{allowGrant(e.ids.New(), worker, contract.Scope{}, []string{capWildcard}, nil)},
	})
	scope := e.workerScope(worker)
	actor := e.workerActor(worker)

	action := e.validAction(scope)
	// Isolate the classification fence from the tool-binding fence proved
	// above: this attachment is disclosed directly, not through a tool.
	action.Tool = noObjectRef
	action.Content = []wireArtifactRef{{ID: e.ids.New(), Digest: strings.Repeat("a", 64)}}

	got := e.checkAs(actor, scope, "context.disclose", &action, "")
	expectDecision(t, got, decisionDeny, "restricted")
	if len(got.Requirements) != 0 {
		t.Fatalf("a restricted-content denial must not stand in as a reviewable requirement: %v", got.Requirements)
	}

	// A reply carrying no disclosed content is unaffected by the fence.
	bare := action
	bare.Content = []wireArtifactRef{}
	got = e.checkAs(actor, scope, "context.disclose", &bare, "")
	expectDecision(t, got, decisionAllow, "")

	// An internal-classified project may still reach a public profile.
	e.ports.setProject(&peerProject{ID: e.ids.New(), OrganizationID: e.org, Classification: classificationInternal})
	got = e.checkAs(actor, scope, "context.disclose", &action, "")
	expectDecision(t, got, decisionAllow, "")

	// A non-public (internal) execution profile may disclose restricted
	// project content: the fence names public destinations specifically,
	// not every disclosure.
	e.ports.setProject(&peerProject{ID: e.ids.New(), OrganizationID: e.org, Classification: classificationRestricted})
	e.bindWorkerProfile(worker, "model-x", classificationInternal)
	got = e.checkAs(actor, scope, "context.disclose", &action, "")
	expectDecision(t, got, decisionAllow, "")
}

// TestContentAccountHeadChangeInvalidatesEarlierReview proves that an exact
// review is bound to the complete action bytes -- disclosed content,
// connection account identity and repository precondition head alike -- so
// no broad conversational approval can stand in for review.decide on a
// changed action: an approval recorded for one exact digest never resolves
// a check for a changed one, and each change demands its own new exact
// decision. If exactActionDigest ever stopped covering one of these fields,
// the corresponding case here would keep resolving to the stale approval.
func TestContentAccountHeadChangeInvalidatesEarlierReview(t *testing.T) {
	e := newEnv(t)
	capability := "repository.merge" // a default review class; see defaultReviewRequired.

	action := e.validAction(e.scope)
	action.Content = []wireArtifactRef{{ID: e.ids.New(), Digest: strings.Repeat("a", 64)}}
	action.Preconditions = json.RawMessage(`{"head":"` + strings.Repeat("1", 40) + `"}`)

	digest := exactActionDigest(action)
	e.ports.setReview(digest, reviewsCheckBody{Eligible: true, Decision: &peerDecision{Decision: "approve"}})

	got := e.check(e.scope, capability, &action, "")
	expectDecision(t, got, decisionAllow, "exact review "+digest+" is approved")

	cases := []struct {
		name   string
		mutate func(*wireAction)
	}{
		{"content", func(a *wireAction) {
			a.Content = []wireArtifactRef{{ID: e.ids.New(), Digest: strings.Repeat("b", 64)}}
		}},
		{"account identity", func(a *wireAction) { a.AccountIdentity = "acct-2" }},
		{"repository head", func(a *wireAction) {
			a.Preconditions = json.RawMessage(`{"head":"` + strings.Repeat("2", 40) + `"}`)
		}},
	}
	for _, tc := range cases {
		changed := action
		tc.mutate(&changed)
		if exactActionDigest(changed) == digest {
			t.Fatalf("%s: mutation did not change the exact action digest", tc.name)
		}
		got = e.check(e.scope, capability, &changed, "")
		expectDecision(t, got, decisionReview, "")
		if len(got.Requirements) != 1 || got.Requirements[0].ActionDigest != exactActionDigest(changed) {
			t.Fatalf("%s: requirement did not bind the new exact action digest: %v", tc.name, got.Requirements)
		}
		if got.Requirements[0].ActionDigest == digest {
			t.Fatalf("%s: the earlier approval must not carry over to the changed action", tc.name)
		}
	}

	// The original approved bytes, unchanged, still stand approved.
	got = e.check(e.scope, capability, &action, "")
	expectDecision(t, got, decisionAllow, "exact review "+digest+" is approved")
}
