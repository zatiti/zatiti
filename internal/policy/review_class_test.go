package policy

import "testing"

// The default review classes cover permission expansion, not every grant
// operation. "Pause, revoke, and cancel commit restrictive state immediately
// under authorized access ... Resume and expansion use normal authorization"
// (R5-010), and a read expands nothing: only creating or widening a grant,
// and activating configuration, wait for an eligible owner's decision.
func TestDefaultReviewCoversExpansionNotRestriction(t *testing.T) {
	e := newEnv(t)
	for _, capability := range []string{"grant.create", "grant.update", "configuration.apply"} {
		got := e.check(e.scope, capability, nil, "")
		expectDecision(t, got, decisionReview,
			"capability "+capability+" is a default review class and no narrower standing policy governs it")
	}
	for _, capability := range []string{"grant.revoke", "grant.get", "grant.list"} {
		got := e.check(e.scope, capability, nil, "")
		expectDecision(t, got, decisionAllow, "")
	}
}
