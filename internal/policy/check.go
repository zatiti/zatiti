package policy

import (
	"context"

	"github.com/zatiti/zatiti/internal/contract"
)

// _policy.check and policy.explain: the authority intersection boundary.
// Both run the same deterministic evaluate; check is the internal gate every
// admission consults, explain is the public inspectable projection for the
// caller's own exact action.

// check evaluates one capability request against current authority, the
// scope envelope, standing policy and exact review state. It never faults on
// a refused request: refusals are decisions with inspectable reasons. Peer
// failures and storage errors propagate so the caller's transaction fails
// closed.
func (s *Service) check(ctx context.Context, unit contract.Unit, in checkInput) (contract.Payload, error) {
	res, err := s.evaluate(ctx, unit, in.Scope, in.Capability, in.Action, in.CandidateDigest)
	if err != nil {
		return contract.Payload{}, err
	}
	return completed(policyResultBody{Resource: res})
}

// explain evaluates the caller's exact action in capability-less mode: only
// wildcard standing rules and capability-independent fences (scope coverage,
// destinations, restrictions, task envelope, conditions) apply, and the
// returned requirement binds the exact action digest. Explain never grants
// or dispatches; it reports the same decision material check would.
func (s *Service) explain(ctx context.Context, unit contract.Unit, in explainInput) (contract.Payload, error) {
	res, err := s.evaluate(ctx, unit, in.Scope, "", &in.Action, "")
	if err != nil {
		return contract.Payload{}, err
	}
	return completed(explainBody(res))
}
