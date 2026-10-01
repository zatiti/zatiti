package application

import (
	"context"

	"github.com/zatiti/zatiti/internal/contract"
)

type effectSubjectLookup interface {
	EffectSubjectFor(string) (contract.EffectSubjectResolver, bool)
}

// subjectUnit keeps the same transaction and generation while application
// dispatch supplies the owner-resolved logical authority to all nested calls.
// Only application creates it, after validating the transport's principal.
type subjectUnit struct {
	contract.Unit
	actor contract.Actor
	scope contract.Scope
}

func (u *subjectUnit) Actor() contract.Actor { return u.actor }
func (u *subjectUnit) Scope() contract.Scope { return u.scope }

func (a *Application) effectSubject(ctx context.Context, unit contract.Unit, owner string, invocation contract.Invocation) (context.Context, contract.Unit, error) {
	lookup, ok := a.reg.(effectSubjectLookup)
	if !ok {
		return ctx, unit, internalFault("registry does not expose effect subject resolution")
	}
	resolver, ok := lookup.EffectSubjectFor(owner)
	if !ok {
		return ctx, unit, internalFault("%s does not implement effect subject resolution", owner)
	}
	actor, scope, err := resolver.ResolveEffectSubject(ctx, unit, invocation)
	if err != nil {
		return ctx, unit, err
	}
	if actor.PrincipalID == "" || scope.InstallationID != unit.Scope().InstallationID {
		return ctx, unit, permissionFault("resolved effect subject is outside this installation")
	}
	wrapped := &subjectUnit{Unit: unit, actor: actor, scope: scope}
	st, ok := stateFrom(ctx)
	if !ok {
		return ctx, unit, internalFault("effect subject resolution requires dispatched transaction")
	}
	scopedCtx := withState(ctx, &dispatchState{chain: st.chain, unit: wrapped})
	if err := a.revalidateAuthority(scopedCtx, wrapped, actor, scope); err != nil {
		return ctx, unit, err
	}
	return scopedCtx, wrapped, nil
}

func effectAdmission(operation string) bool {
	return operation == "_effects.admit" || operation == "_effects.claim" || operation == "_effects.reconciliation.prepare"
}
