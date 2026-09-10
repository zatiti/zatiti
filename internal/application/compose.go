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
		if err := a.policyGate(wctx, u, scope, desc.ID); err != nil {
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
		return delivered, nil
	}

	// The transaction rolled back. Persist a known refusal in a separate
	// short transaction so an identical retry replays the same disposition;
	// concurrent duplicates keep serializing on the same command identity.
	if refusalErr := a.recordRefusal(ctx, actor, desc, submissionKey, digest, scope, err); refusalErr != nil {
		_ = refusalErr // refusal persistence is best-effort; the primary fault stands
	}
	return contract.Result{}, err
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
		if err := a.policyGate(wctx, u, scope, desc.ID); err != nil {
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
// transaction rolled back, in a separate short write. The command identity
// serializes concurrent duplicates: if a concurrent identical call already
// created the command, its disposition stands and this refusal is dropped.
func (a *Application) recordRefusal(
	ctx context.Context,
	actor contract.Actor,
	desc contract.Descriptor,
	submissionKey string,
	digest contract.Digest,
	scope contract.Scope,
	primary error,
) error {
	fault := faultOf(primary)
	if !deterministicRefusal(fault) {
		return nil
	}
	refusal := contract.Result{
		Schema: contract.SchemaResult,
		Payload: contract.Payload{
			Status: contract.StatusFailed,
			Error:  fault,
		},
	}
	return a.db.Write(ctx, actor, scope, func(u contract.Unit) error {
		wctx := withState(ctx, &dispatchState{chain: []string{desc.ID}, unit: u})
		commandID, replay, err := a.commandBegin(wctx, u, actor, desc, submissionKey, digest)
		if err != nil {
			return err
		}
		if replay != nil {
			// A concurrent duplicate owns this command identity; its own
			// disposition stands. Dedupe concurrency is not lost.
			return nil
		}
		refusal.CommandID = commandID
		return a.commandFinish(wctx, u, commandID, refusal)
	})
}
