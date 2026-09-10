package reviews

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/zatiti/zatiti/internal/contract"
)

// Outgoing owner calls. The only port reviews consult is
// _identity.authority: every eligibility path rechecks the current
// principal — never a claimed profile, a stored snapshot, or an
// approved_by_human boolean. Calls retain the current unit, actor, scope,
// generation and transaction; no authority is minted here.

const opIdentityAuthority = "_identity.authority"

// loadAuthority resolves one principal's current authority inside the given
// scope. A peer not_found fault maps to permission denied at this boundary:
// an unregistered principal is never an eligible reviewer. Other peer
// faults and transport failures propagate unchanged.
func (s *Service) loadAuthority(ctx context.Context, unit contract.Unit, principal contract.ID, scope contract.Scope) (*wireAuthority, error) {
	auth, err := s.loadPeerAuthority(ctx, unit, principal, scope)
	if err != nil {
		if faultCode(err) == contract.CodeNotFound {
			return nil, permissionDenied("principal %s is not registered in this installation", principal)
		}
		return nil, err
	}
	return auth, nil
}

// loadPeerAuthority resolves one principal through the port and passes peer
// faults through unchanged, so callers can distinguish an unknown
// principal from one that is merely unauthorized.
func (s *Service) loadPeerAuthority(ctx context.Context, unit contract.Unit, principal contract.ID, scope contract.Scope) (*wireAuthority, error) {
	raw, err := json.Marshal(wireAuthorityInput{PrincipalID: principal, Scope: scope})
	if err != nil {
		return nil, internalError("authority call encoding failed")
	}
	payload, err := s.deps.Ports.Call(ctx, unit, contract.Invocation{
		Operation: opIdentityAuthority,
		Version:   1,
		Input:     raw,
	})
	if err != nil {
		return nil, err
	}
	if payload.Error != nil {
		return nil, payload.Error
	}
	var out resourceOut[wireAuthority]
	if err := json.Unmarshal(payload.Data, &out); err != nil {
		return nil, internalError("authority output decoding failed")
	}
	return &out.Resource, nil
}

// faultCode extracts the fault code from a handler error, or the empty
// string when the error carries no fault.
func faultCode(err error) string {
	var f *contract.Fault
	if errors.As(err, &f) {
		return f.Code
	}
	return ""
}

// reviewerStanding evaluates one decision's reviewer against the review's
// stored requirement at the given instant: not revoked, the required
// principal kind, and proposer separation where the requirement demands it.
// Data failures are faults; a false return means the reviewer no longer
// stands and eligibility must be refused.
func (s *Service) reviewerStanding(ctx context.Context, unit contract.Unit, review reviewRow, reviewer contract.ID) error {
	req, err := review.requirement()
	if err != nil {
		return err
	}
	auth, err := s.loadAuthority(ctx, unit, reviewer, review.Scope)
	if err != nil {
		return err
	}
	if auth.Principal.Revoked {
		return permissionDenied("reviewer %s is revoked", reviewer)
	}
	if req.HumanRequired && auth.Principal.Kind != kindHuman {
		return permissionDenied("review %s requires a human reviewer; principal %s is a %s",
			review.ID, reviewer, auth.Principal.Kind)
	}
	if req.SeparateProposer && reviewer == review.ProposerID {
		return permissionDenied("review %s requires separation between proposer and reviewer", review.ID)
	}
	return nil
}

// checkGate reports whether the executing unit's scope matches the scope in
// the request body. A mismatch is invalid input: storage is already
// installation-scoped, so a foreign installation identifier can only be a
// caller error, and refusing loudly beats silently answering a different
// installation's data.
func checkGate(unit contract.Unit, requested contract.Scope) error {
	if requested.InstallationID != unit.Scope().InstallationID {
		return invalidInput("scope does not match the current execution scope")
	}
	return nil
}
