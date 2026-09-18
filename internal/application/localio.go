package application

import (
	"context"

	"github.com/zatiti/zatiti/internal/contract"
)

// isLocalIOOperation reports whether the operation routes through the
// local IO phases. The set is frozen by the shared contract; the registry
// pairs these operation IDs with owners whose services implement
// contract.LocalIO, and the phase-capable handler carries them.
func isLocalIOOperation(operation string) bool {
	return localIOOperations[operation]
}

// invokeIOMutation runs one synchronous local IO mutation. Prepare records
// the replayable intent and the accepted pending disposition inside the
// admission transaction; Perform runs outside transactions; Finish rechecks
// authority and replaces the pending disposition, once, with the final one.
// Concurrent same-key calls join the accepted command instead of duplicating
// Perform. The evidence owner admits exactly that second finish and no other.
//
// The accepted disposition is the durable record of everything between the
// two transactions: a crash or a transient fault after Prepare leaves the
// command accepted, a same-key retry joins it without a second Perform, and
// the owner's recorded intent is what local recovery works from.
func (a *Application) invokeIOMutation(
	ctx context.Context,
	actor contract.Actor,
	desc contract.Descriptor,
	handler contract.Handler,
	invocation contract.Invocation,
	submissionKey string,
	scope contract.Scope,
) (contract.Result, error) {
	phases, err := a.ioFor(desc.ID)
	if err != nil {
		return contract.Result{}, err
	}
	_ = handler
	digest, err := requestDigest(invocation.Input)
	if err != nil {
		return contract.Result{}, err
	}

	var delivered contract.Result
	var st planState
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
			// Identical retry or concurrent join: the retained disposition
			// (accepted while in progress, terminal once finished) is
			// returned without running Perform again.
			delivered = *replay
			return nil
		}

		plan, pErr := phases.Prepare(wctx, u, invocation)
		if pErr != nil {
			return pErr
		}
		accepted := contract.Result{
			Schema:    contract.SchemaResult,
			CommandID: commandID,
			Payload: contract.Payload{
				Status: contract.StatusAccepted,
				Data:   plan.Prepared,
			},
		}
		if err := a.commandFinish(wctx, u, commandID, accepted); err != nil {
			return err
		}
		delivered = accepted
		st.plan = plan
		st.commandID = commandID
		return nil
	})
	if err != nil {
		return a.refused(ctx, actor, desc, submissionKey, digest, scope, err)
	}
	if st.plan.ID == "" {
		// Joined an in-progress command or replayed a finished one: the
		// retained disposition stands.
		return disposition(delivered)
	}

	ioResult := a.performIO(ctx, phases, st.plan)

	var final contract.Result
	err = a.db.Write(ctx, actor, scope, func(u contract.Unit) error {
		wctx := withState(ctx, &dispatchState{chain: []string{desc.ID}, unit: u})
		if err := a.revalidateAuthority(wctx, u, actor, scope); err != nil {
			return err
		}
		if err := a.policyGate(wctx, u, scope, desc.ID, invocation.Input); err != nil {
			return err
		}
		payload, fErr := phases.Finish(wctx, u, st.plan, ioResult)
		if fErr != nil {
			return fErr
		}
		if ioResult.Fault != nil {
			final = contract.Result{
				Schema:    contract.SchemaResult,
				CommandID: st.commandID,
				Payload: contract.Payload{
					Status: contract.StatusFailed,
					Data:   ioResult.Data,
					Error:  ioResult.Fault,
				},
			}
		} else {
			if payload.Status == contract.StatusFailed {
				return payloadDefect(desc.ID, payload)
			}
			final = contract.Result{Schema: contract.SchemaResult, CommandID: st.commandID, Payload: payload}
		}
		return a.commandFinish(wctx, u, st.commandID, final)
	})
	if err != nil {
		return a.refusedFinish(ctx, actor, desc, scope, st.commandID, err)
	}
	return disposition(final)
}

// refusedFinish handles a rolled-back Finish transaction. The caller is about
// to see this fault, so a deterministic refusal replaces the pending
// disposition in a separate short write and an identical retry replays it
// rather than reporting the command accepted. A transient fault retains
// nothing: the accepted disposition stays the honest record.
func (a *Application) refusedFinish(
	ctx context.Context,
	actor contract.Actor,
	desc contract.Descriptor,
	scope contract.Scope,
	commandID contract.ID,
	primary error,
) (contract.Result, error) {
	fault := faultOf(primary)
	if !deterministicRefusal(fault) {
		return contract.Result{}, primary
	}
	refusal := contract.Result{
		Schema:    contract.SchemaResult,
		CommandID: commandID,
		Payload:   contract.Payload{Status: contract.StatusFailed, Error: fault},
	}
	err := a.db.Write(ctx, actor, scope, func(u contract.Unit) error {
		wctx := withState(ctx, &dispatchState{chain: []string{desc.ID}, unit: u})
		return a.commandFinish(wctx, u, commandID, refusal)
	})
	if err != nil {
		// Replacement is best-effort; the primary fault stands.
		return contract.Result{}, primary
	}
	return disposition(refusal)
}

// planState carries the prepared plan across the transaction boundary into
// the completion transaction.
type planState struct {
	plan      contract.IOPlan
	commandID contract.ID
}

// invokeIOQuery runs a synchronous local IO query. The first snapshot
// validates authority and prepares; Perform resolves bytes outside
// transactions; the second snapshot repeats authorization before any bytes
// reach the caller.
func (a *Application) invokeIOQuery(
	ctx context.Context,
	actor contract.Actor,
	desc contract.Descriptor,
	handler contract.Handler,
	invocation contract.Invocation,
	scope contract.Scope,
) (contract.Result, error) {
	phases, err := a.ioFor(desc.ID)
	if err != nil {
		return contract.Result{}, err
	}
	_ = handler

	var plan contract.IOPlan
	err = a.db.Read(ctx, actor, scope, func(u contract.Unit) error {
		wctx := withState(ctx, &dispatchState{chain: []string{desc.ID}, unit: u})
		if err := a.revalidateAuthority(wctx, u, actor, scope); err != nil {
			return err
		}
		if err := a.policyGate(wctx, u, scope, desc.ID, invocation.Input); err != nil {
			return err
		}
		p, pErr := phases.Prepare(wctx, u, invocation)
		if pErr != nil {
			return pErr
		}
		plan = p
		return nil
	})
	if err != nil {
		return contract.Result{}, err
	}

	ioResult := a.performIO(ctx, phases, plan)

	var delivered contract.Result
	err = a.db.Read(ctx, actor, scope, func(u contract.Unit) error {
		wctx := withState(ctx, &dispatchState{chain: []string{desc.ID}, unit: u})
		if err := a.revalidateAuthority(wctx, u, actor, scope); err != nil {
			return err
		}
		if err := a.policyGate(wctx, u, scope, desc.ID, invocation.Input); err != nil {
			return err
		}
		payload, fErr := phases.Finish(wctx, u, plan, ioResult)
		if fErr != nil {
			return fErr
		}
		if ioResult.Fault != nil {
			delivered = contract.Result{
				Schema:    contract.SchemaResult,
				CommandID: a.ids.New(),
				Payload: contract.Payload{
					Status: contract.StatusFailed,
					Data:   ioResult.Data,
					Error:  ioResult.Fault,
				},
			}
			return nil
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
	return disposition(delivered)
}

// performIO runs the local performance phase outside transactions. A panic
// or error becomes an inspectable failed result instead of crashing the
// controller; the completion transaction records the disposition.
func (a *Application) performIO(ctx context.Context, phases contract.LocalIO, plan contract.IOPlan) (res contract.IOResult) {
	defer func() {
		if r := recover(); r != nil {
			res = contract.IOResult{Fault: internalFault("local IO performance panicked")}
		}
	}()
	res, err := phases.Perform(ctx, plan)
	if err != nil {
		return contract.IOResult{Fault: faultOf(err)}
	}
	return res
}

// runBootstrapLocalIO runs the one-time bootstrap through the local IO
// phases without command identity: no submission key exists at the
// pre-initialization boundary and the exclusive installation lock is held by
// the entrypoint for the whole call.
func (a *Application) runBootstrapLocalIO(
	ctx context.Context,
	actor contract.Actor,
	desc contract.Descriptor,
	handler contract.Handler,
	invocation contract.Invocation,
	scope contract.Scope,
) (*contract.Payload, error) {
	phases, err := a.ioFor(desc.ID)
	if err != nil {
		return nil, err
	}
	_ = handler

	var plan contract.IOPlan
	err = a.db.Write(ctx, actor, scope, func(u contract.Unit) error {
		wctx := withState(ctx, &dispatchState{chain: []string{desc.ID}, unit: u})
		p, pErr := phases.Prepare(wctx, u, invocation)
		if pErr != nil {
			return pErr
		}
		plan = p
		return nil
	})
	if err != nil {
		return nil, err
	}

	ioResult := a.performIO(ctx, phases, plan)

	var payload contract.Payload
	err = a.db.Write(ctx, actor, scope, func(u contract.Unit) error {
		wctx := withState(ctx, &dispatchState{chain: []string{desc.ID}, unit: u})
		p, fErr := phases.Finish(wctx, u, plan, ioResult)
		if fErr != nil {
			return fErr
		}
		if ioResult.Fault != nil {
			return ioResult.Fault
		}
		if p.Status == contract.StatusFailed {
			return payloadDefect(desc.ID, p)
		}
		payload = p
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &payload, nil
}
