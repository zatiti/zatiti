package application

import (
	"context"

	"github.com/zatiti/zatiti/internal/contract"
)

// invokeMutation runs one public mutation. Command identity and replay come
// before any stale check; the handler, its state changes and every emitted
// event share one ordered transaction with the command record.
func (a *Application) invokeMutation(
	ctx context.Context,
	actor contract.Actor,
	desc contract.Descriptor,
	handler contract.Handler,
	invocation contract.Invocation,
	submissionKey string,
	scope contract.Scope,
) (contract.Result, error) {
	digest, err := requestDigest(invocation.Input)
	if err != nil {
		return contract.Result{}, err
	}

	var delivered contract.Result
	err = a.db.Write(ctx, actor, scope, func(u contract.Unit) error {
		wctx := withState(ctx, &dispatchState{chain: []string{desc.ID}, unit: u})

		if err := a.revalidateAuthority(wctx, u, actor, scope); err != nil {
			return err
		}
		if err := a.policyGate(wctx, u, scope, desc.ID, invocation.Input); err != nil {
			return err
		}

		commandID, replay, err := a.commandBegin(wctx, u, actor, desc, submissionKey, digest)
		if err != nil {
			return err
		}
		if replay != nil {
			// Identical retry: the original disposition is returned before
			// any stale-version validation and without re-executing.
			delivered = *replay
			return nil
		}

		payload, hErr := handler(wctx, u, invocation)
		if hErr != nil {
			// The whole transaction, including the command identity, rolls
			// back; a deterministic refusal is re-recorded afterwards.
			return hErr
		}
		if payload.Status == contract.StatusFailed {
			return payloadDefect(desc.ID, payload)
		}

		result := contract.Result{Schema: contract.SchemaResult, CommandID: commandID, Payload: payload}
		if err := a.commandFinish(wctx, u, commandID, result); err != nil {
			return err
		}
		delivered = result
		return nil
	})
	if err == nil {
		return disposition(delivered)
	}

	// The transaction rolled back. A review_required refusal rolled back the
	// pending review the policy gate ensured, so it is ensured again in its
	// own transaction: the review is the durable evidence the eligible owner
	// decides, and without it the refusal could never be resolved.
	if faultOf(err).Code == contract.CodeReviewRequired {
		a.persistReviewRequest(ctx, actor, desc, invocation, scope)
	}
	// Persist a known refusal in a separate short transaction so an
	// identical retry replays the same disposition; concurrent duplicates
	// keep serializing on the same command identity.
	return a.refused(ctx, actor, desc, submissionKey, digest, scope, err)
}

// persistReviewRequest re-runs the policy gate for a refused mutation in a
// separate short transaction so the pending review it ensures survives the
// rollback of the refused request. The gate is deterministic and its only
// state change is that ensure, so nothing else is admitted or recorded. A
// failure here leaves the refusal standing; the next identical request
// ensures again.
func (a *Application) persistReviewRequest(ctx context.Context, actor contract.Actor, desc contract.Descriptor, invocation contract.Invocation, scope contract.Scope) {
	_ = a.db.Write(ctx, actor, scope, func(u contract.Unit) error {
		wctx := withState(ctx, &dispatchState{chain: []string{desc.ID}, unit: u})
		if err := a.revalidateAuthority(wctx, u, actor, scope); err != nil {
			return err
		}
		gateErr := a.policyGate(wctx, u, scope, desc.ID, invocation.Input)
		if faultOf(gateErr).Code == contract.CodeReviewRequired {
			// The expected outcome: commit the ensured review.
			return nil
		}
		return gateErr
	})
}

// disposition pairs a result with its error the one way every path returns
// it: a failed result travels with its fault as the error, first time and on
// every replay, so a transport never renders a failure as a success.
func disposition(res contract.Result) (contract.Result, error) {
	if res.Status != contract.StatusFailed {
		return res, nil
	}
	if res.Error == nil {
		return contract.Result{}, internalFault("command %s failed without a fault", res.CommandID)
	}
	return res, faultErr(res.Error)
}

// refused returns a rolled-back mutation's refusal. A deterministic refusal
// is retained first, and the caller receives the retained failed envelope
// with its fault: exactly what an identical retry replays, including a retry
// the gates refuse again before command replay is reached. A refusal nothing
// retains (a transient fault, review_required, an identity whose retained
// disposition is a different one) has no durable command to name and returns
// the fault alone.
func (a *Application) refused(
	ctx context.Context,
	actor contract.Actor,
	desc contract.Descriptor,
	submissionKey string,
	digest contract.Digest,
	scope contract.Scope,
	primary error,
) (contract.Result, error) {
	retained, err := a.recordRefusal(ctx, actor, desc, submissionKey, digest, scope, primary)
	if err != nil || retained == nil {
		// Refusal persistence is best-effort; the primary fault stands.
		return contract.Result{}, primary
	}
	return disposition(*retained)
}

// invokeQuery runs one public query inside a consistent read snapshot.
// Queries do not create durable commands; the result envelope carries a
// fresh command identity that need not be retained.
func (a *Application) invokeQuery(
	ctx context.Context,
	actor contract.Actor,
	desc contract.Descriptor,
	handler contract.Handler,
	invocation contract.Invocation,
	scope contract.Scope,
) (contract.Result, error) {
	var delivered contract.Result
	err := a.db.Read(ctx, actor, scope, func(u contract.Unit) error {
		wctx := withState(ctx, &dispatchState{chain: []string{desc.ID}, unit: u})
		if err := a.revalidateAuthority(wctx, u, actor, scope); err != nil {
			return err
		}
		if err := a.policyGate(wctx, u, scope, desc.ID, invocation.Input); err != nil {
			return err
		}
		payload, hErr := handler(wctx, u, invocation)
		if hErr != nil {
			return hErr
		}
		if payload.Status == contract.StatusFailed {
			return payloadDefect(desc.ID, payload)
		}
		delivered = contract.Result{
			Schema:    contract.SchemaResult,
			CommandID: a.ids.New(),
			Payload:   payload,
		}
		return nil
	})
	if err != nil {
		return contract.Result{}, err
	}
	return delivered, nil
}

// invokeBootstrap runs the one-time installation initialization: no
// authority read (identity does not exist yet), no policy gate, no command
// identity. The minted installation identity becomes durable through the
// bootstrap transaction's scope and events.
func (a *Application) invokeBootstrap(
	ctx context.Context,
	actor contract.Actor,
	desc contract.Descriptor,
	handler contract.Handler,
	request contract.Request,
) (contract.Result, error) {
	if desc.ID != "installation.init" {
		return contract.Result{}, internalFault(
			"operation %s is a mutation without a submission key but not the registered bootstrap", desc.ID)
	}
	installation := a.ids.New()
	scope := contract.Scope{InstallationID: installation}
	invocation := contract.Invocation{Operation: desc.ID, Version: desc.Version, Input: request.Input}

	if isLocalIOOperation(desc.ID) {
		payload, err := a.runBootstrapLocalIO(ctx, actor, desc, handler, invocation, scope)
		if err != nil {
			return contract.Result{}, err
		}
		a.rememberInstallation(installation)
		return contract.Result{Schema: contract.SchemaResult, CommandID: a.ids.New(), Payload: *payload}, nil
	}

	var payload contract.Payload
	err := a.db.Write(ctx, actor, scope, func(u contract.Unit) error {
		wctx := withState(ctx, &dispatchState{chain: []string{desc.ID}, unit: u})
		p, hErr := handler(wctx, u, invocation)
		if hErr != nil {
			return hErr
		}
		if p.Status == contract.StatusFailed {
			return payloadDefect(desc.ID, p)
		}
		payload = p
		return nil
	})
	if err != nil {
		return contract.Result{}, err
	}
	a.rememberInstallation(installation)
	return contract.Result{Schema: contract.SchemaResult, CommandID: a.ids.New(), Payload: payload}, nil
}

func (a *Application) rememberInstallation(id contract.ID) {
	a.installMu.Lock()
	a.installation = id
	a.installMu.Unlock()
}

// recordRefusal persists a deterministic mutation refusal after its
// transaction rolled back, in a separate short write, and returns the
// retained envelope. The command identity serializes concurrent duplicates:
// if an identical call already created the command, its disposition stands
// and this refusal is dropped. That retained disposition is returned only
// when it is this very refusal; a current refusal never discloses or
// overrides a different retained disposition.
func (a *Application) recordRefusal(
	ctx context.Context,
	actor contract.Actor,
	desc contract.Descriptor,
	submissionKey string,
	digest contract.Digest,
	scope contract.Scope,
	primary error,
) (*contract.Result, error) {
	fault := faultOf(primary)
	if !deterministicRefusal(fault) {
		return nil, nil
	}
	refusal := contract.Result{
		Schema: contract.SchemaResult,
		Payload: contract.Payload{
			Status: contract.StatusFailed,
			Error:  fault,
		},
	}
	var retained *contract.Result
	err := a.db.Write(ctx, actor, scope, func(u contract.Unit) error {
		wctx := withState(ctx, &dispatchState{chain: []string{desc.ID}, unit: u})
		commandID, replay, err := a.commandBegin(wctx, u, actor, desc, submissionKey, digest)
		if err != nil {
			return err
		}
		if replay != nil {
			// A duplicate owns this command identity; its own disposition
			// stands. Dedupe concurrency is not lost.
			if sameRefusal(replay, fault) {
				retained = replay
			}
			return nil
		}
		refusal.CommandID = commandID
		if err := a.commandFinish(wctx, u, commandID, refusal); err != nil {
			return err
		}
		retained = &refusal
		return nil
	})
	if err != nil {
		return nil, err
	}
	return retained, nil
}

// sameRefusal reports whether a retained disposition is the refusal fault.
func sameRefusal(retained *contract.Result, fault *contract.Fault) bool {
	return retained.Status == contract.StatusFailed && retained.Error != nil &&
		retained.Error.Code == fault.Code && retained.Error.Message == fault.Message
}
