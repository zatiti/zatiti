package application

import (
	"context"

	"github.com/zatiti/zatiti/internal/contract"
)

// maxDispatchDepth bounds the internal call chain. Owner modules compose
// through Ports.Call; a chain longer than any legitimate composition is a
// recursive operation loop and fails closed.
const maxDispatchDepth = 32

// dispatchState travels in the context the dispatcher hands to handlers. It
// marks an in-flight transaction so reentrant public dispatch is rejected
// before reaching storage, and carries the active call chain so recursive
// operation loops fail closed. The unit pins same-unit propagation: nested
// calls must join exactly this transaction.
type dispatchState struct {
	chain []string
	unit  contract.Unit
}

type dispatchStateKey struct{}

func stateFrom(ctx context.Context) (*dispatchState, bool) {
	st, ok := ctx.Value(dispatchStateKey{}).(*dispatchState)
	return st, ok
}

func withState(ctx context.Context, st *dispatchState) context.Context {
	return context.WithValue(ctx, dispatchStateKey{}, st)
}

// IOLookup is the registry capability the dispatcher needs to route the
// registered local IO operations to their owning services. The registry
// detects contract.LocalIO implementers at assembly; *registry.Registry is
// expected to expose this lookup. Without it, local IO operations fail with
// a precise internal error instead of taking an ordinary handler path.
type IOLookup interface {
	LocalIOFor(operation string) (contract.LocalIO, bool)
}

// ioFor resolves the LocalIO service owning a registered local IO operation.
func (a *Application) ioFor(operation string) (contract.LocalIO, error) {
	if a.ioLookup == nil {
		return nil, internalFault(
			"registry does not expose local IO routing for %s", operation)
	}
	io, ok := a.ioLookup.LocalIOFor(operation)
	if !ok || io == nil {
		return nil, internalFault(
			"registered local IO operation %s has no owning LocalIO service", operation)
	}
	return io, nil
}

// Invoke executes one public operation. The caller carries the actor the
// transport authenticated; the dispatcher revalidates it, derives the unit
// scope from the validated input, gates the operation on current policy and
// composes the ordered transaction around the handler.
func (a *Application) Invoke(ctx context.Context, actor contract.Actor, operationID string, request contract.Request) (contract.Result, error) {
	if err := a.entryGuard(); err != nil {
		return contract.Result{}, err
	}
	if st, ok := stateFrom(ctx); ok && len(st.chain) > 0 {
		return contract.Result{}, internalFault(
			"reentrant public dispatch is forbidden; nested work joins the current unit through ports")
	}
	if actor.PrincipalID == "" || actor.Kind == "" {
		return contract.Result{}, permissionFault("an authenticated actor is required")
	}
	if request.Schema != contract.SchemaRequest {
		return contract.Result{}, invalidFault("request envelope schema %q is not %s", request.Schema, contract.SchemaRequest)
	}
	desc, handler, err := a.reg.Lookup(operationID, 0)
	if err != nil {
		// Public routing never discloses internal operations: unknown and
		// internal ids are indistinguishable not_found.
		return contract.Result{}, notFoundFault("unknown operation %q", operationID)
	}
	if desc.Visibility != contract.VisibilityPublic {
		return contract.Result{}, notFoundFault("unknown operation %q", operationID)
	}
	if desc.Mode != contract.ModeQuery && desc.Mode != contract.ModeMutation {
		return contract.Result{}, internalFault("operation %s declares unknown mode %q", desc.ID, desc.Mode)
	}
	if err := checkRequestLimits(desc, request); err != nil {
		return contract.Result{}, err
	}
	if err := contract.ValidateSchema(desc.InputSchema, request.Input); err != nil {
		return contract.Result{}, invalidFault("input does not match the %s schema: %v", operationID, err)
	}
	if desc.SubmissionKey {
		if err := validateSubmissionKey(request.SubmissionKey); err != nil {
			return contract.Result{}, err
		}
	}

	installation, err := a.installationID(ctx)
	if err != nil {
		return contract.Result{}, err
	}

	// One-time bootstrap runs before the installation exists: no authority
	// read, no policy gate, no command identity (no submission key exists).
	if desc.Mode == contract.ModeMutation && !desc.SubmissionKey {
		return a.invokeBootstrap(ctx, actor, desc, handler, request)
	}

	if installation == "" {
		return contract.Result{}, permissionFault("installation is not initialized")
	}
	scope, err := deriveScope(desc, request.Input, installation)
	if err != nil {
		return contract.Result{}, err
	}
	if err := checkScopeShape(scope); err != nil {
		return contract.Result{}, err
	}

	invocation := contract.Invocation{Operation: desc.ID, Version: desc.Version, Input: request.Input}
	if desc.Mode == contract.ModeMutation {
		if isLocalIOOperation(desc.ID) {
			return a.invokeIOMutation(ctx, actor, desc, handler, invocation, request.SubmissionKey, scope)
		}
		return a.invokeMutation(ctx, actor, desc, handler, invocation, request.SubmissionKey, scope)
	}
	if isLocalIOOperation(desc.ID) {
		return a.invokeIOQuery(ctx, actor, desc, handler, invocation, scope)
	}
	return a.invokeQuery(ctx, actor, desc, handler, invocation, scope)
}

// Internal executes one internal operation on behalf of the trusted local
// controller: job claims and records, recovery scans, application-driven
// activation. Only a service principal may enter, the operation must be an
// internal descriptor allowlisting the controller, and the explicit scope
// must name this installation.
func (a *Application) Internal(ctx context.Context, actor contract.Actor, scope contract.Scope, invocation contract.Invocation) (contract.Payload, error) {
	if err := a.entryGuard(); err != nil {
		return contract.Payload{}, err
	}
	if st, ok := stateFrom(ctx); ok && len(st.chain) > 0 {
		return contract.Payload{}, internalFault(
			"reentrant dispatch is forbidden; nested calls join the current unit through ports")
	}
	if actor.Kind != contract.KindService {
		return contract.Payload{}, permissionFault("internal dispatch requires a service principal")
	}
	if actor.PrincipalID == "" {
		return contract.Payload{}, permissionFault("internal dispatch requires an authenticated principal")
	}
	if scope.InstallationID == "" {
		return contract.Payload{}, invalidFault("internal dispatch requires an explicit installation scope")
	}
	installation, err := a.installationID(ctx)
	if err != nil {
		return contract.Payload{}, err
	}
	if installation == "" {
		return contract.Payload{}, permissionFault("installation is not initialized")
	}
	if scope.InstallationID != installation {
		return contract.Payload{}, permissionFault("internal dispatch targets installation %s outside this controller", scope.InstallationID)
	}
	desc, handler, err := a.reg.Lookup(invocation.Operation, 0)
	if err != nil {
		return contract.Payload{}, notFoundFault("unknown operation %q", invocation.Operation)
	}
	if desc.Visibility != contract.VisibilityInternal {
		return contract.Payload{}, permissionFault("operation %s is not an internal operation", desc.ID)
	}
	if !callerAllowed(desc.Callers, controllerCaller) {
		return contract.Payload{}, permissionFault("internal operation %s does not allow the controller", desc.ID)
	}
	if invocation.Version != 0 && invocation.Version != desc.Version {
		return contract.Payload{}, faultErr(&contract.Fault{
			Code:    contract.CodeCapabilityUnsupported,
			Message: "operation " + desc.ID + " version " + itoa64(invocation.Version) + " is not supported",
		})
	}
	if invocation.Version == 0 {
		invocation.Version = desc.Version
	}

	if desc.Mode == contract.ModeMutation {
		var payload contract.Payload
		err = a.db.Write(ctx, actor, scope, func(u contract.Unit) error {
			wctx := withState(ctx, &dispatchState{chain: []string{desc.ID}, unit: u})
			if err := a.revalidateAuthority(wctx, u, actor, scope); err != nil {
				return err
			}
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
			return contract.Payload{}, err
		}
		return payload, nil
	}

	var payload contract.Payload
	err = a.db.Read(ctx, actor, scope, func(u contract.Unit) error {
		wctx := withState(ctx, &dispatchState{chain: []string{desc.ID}, unit: u})
		if err := a.revalidateAuthority(wctx, u, actor, scope); err != nil {
			return err
		}
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
		return contract.Payload{}, err
	}
	return payload, nil
}

// controllerCaller is the caller identity of the trusted local controller at
// the Internal entry point.
const controllerCaller = "controller"

// callerAllowed reports whether caller is in the descriptor's allowlist. An
// empty allowlist never authorizes an internal call.
func callerAllowed(callers []string, caller string) bool {
	for _, c := range callers {
		if c == caller {
			return true
		}
	}
	return false
}

// checkRequestLimits enforces the wire limits before any work: ordinary
// requests are 1 MiB of JSON; the artifact chunk upload carries a 2 MiB
// encoded cap for its 1 MiB decoded chunk plus envelope.
func checkRequestLimits(desc contract.Descriptor, request contract.Request) error {
	limit := int64(1 << 20)
	if desc.ID == "artifact.upload.chunk" {
		limit = 2 << 20
	}
	if int64(len(request.Input)) > limit {
		return invalidFault("request input exceeds the %d byte limit for %s", limit, desc.ID)
	}
	return nil
}

// dispatchNested routes one internal cross-owner call. The call joins the
// caller's transaction: same unit, same actor, same scope, same generation,
// extended call chain. It cannot mint authority, cannot open a transaction
// and cannot call a public operation.
func (a *Application) dispatchNested(ctx context.Context, unit contract.Unit, caller string, invocation contract.Invocation) (contract.Payload, error) {
	st, ok := stateFrom(ctx)
	if !ok || len(st.chain) == 0 {
		return contract.Payload{}, internalFault("internal call outside a dispatched transaction")
	}
	if len(st.chain) >= maxDispatchDepth {
		return contract.Payload{}, internalFault("internal dispatch depth exceeded")
	}
	for _, op := range st.chain {
		if op == invocation.Operation {
			return contract.Payload{}, internalFault(
				"recursive operation loop detected at %s", invocation.Operation)
		}
	}
	desc, handler, err := a.reg.Lookup(invocation.Operation, 0)
	if err != nil {
		return contract.Payload{}, notFoundFault("unknown operation %q", invocation.Operation)
	}
	if desc.Visibility != contract.VisibilityInternal {
		return contract.Payload{}, permissionFault("operation %s is not callable internally", desc.ID)
	}
	if !callerAllowed(desc.Callers, caller) {
		return contract.Payload{}, permissionFault(
			"operation %s does not allow caller %s", desc.ID, caller)
	}
	if invocation.Version != 0 && invocation.Version != desc.Version {
		return contract.Payload{}, faultErr(&contract.Fault{
			Code:    contract.CodeCapabilityUnsupported,
			Message: "operation " + desc.ID + " version " + itoa64(invocation.Version) + " is not supported",
		})
	}
	if unit.ReadOnly() && desc.Mode == contract.ModeMutation {
		return contract.Payload{}, permissionFault(
			"mutation operation %s cannot run under a read snapshot", desc.ID)
	}
	if !sameUnit(st.unit, unit) {
		return contract.Payload{}, internalFault("internal call must join the current transaction unit")
	}
	version := invocation.Version
	if version == 0 {
		version = desc.Version
	}
	next := make([]string, len(st.chain)+1)
	copy(next, st.chain)
	next[len(st.chain)] = desc.ID
	wctx := withState(ctx, &dispatchState{chain: next, unit: unit})
	invocation.Version = version
	invocation.Operation = desc.ID
	return handler(wctx, unit, invocation)
}

// sameUnit reports whether the unit a nested call carries is the unit of the
// transaction in flight. Retaining a unit across transactions is a contract
// violation; a foreign unit would break actor and scope propagation. Storage
// hands out pointer-backed units, so identity comparison is exact; the
// recover guard keeps a pathological non-comparable unit implementation from
// crashing the writer.
func sameUnit(a, b contract.Unit) (same bool) {
	if a == nil || b == nil {
		return false
	}
	defer func() {
		if recover() != nil {
			same = false
		}
	}()
	return a == b
}
