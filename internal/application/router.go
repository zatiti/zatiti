package application

import (
	"context"
	"sync"

	"github.com/zatiti/zatiti/internal/contract"
)

// applicationCaller is the caller identity the dispatcher itself uses for
// its own internal calls: authority reads, policy checks and command
// evidence. The catalog allowlists it explicitly.
const applicationCaller = "application"

// PortRouter is the constructor-safe late-bound dispatcher seam. Assembly
// creates it first, hands owner-bound ports to every module constructor, and
// binds the finished application exactly once before serving. A domain can
// never change the caller identity its ports carry: it is fixed by For.
type PortRouter struct {
	mu    sync.Mutex
	ports map[string]contract.Ports
	app   *Application
	bound bool
}

// NewPorts returns an unbound port router. Modules constructed before Bind
// may hold their ports but every call rejects until Bind.
func NewPorts() *PortRouter {
	return &PortRouter{ports: make(map[string]contract.Ports)}
}

// For returns the ports bound to owner. The owner becomes the fixed caller
// identity every dispatch through the returned ports carries; domains cannot
// mint ports for another identity.
func (r *PortRouter) For(owner string) contract.Ports {
	if owner == "" {
		// An empty owner would bypass allowlists; never hand one out.
		return unboundPorts{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.ports[owner]; ok {
		return p
	}
	p := &boundPorts{router: r, owner: owner}
	r.ports[owner] = p
	return p
}

// Bind attaches the finished application. Bind runs exactly once; a second
// call is an assembly defect and fails without changing state.
func (r *PortRouter) Bind(app *Application) error {
	if app == nil {
		return internalFault("cannot bind a nil application")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bound {
		return internalFault("port router is already bound")
	}
	r.bound = true
	r.app = app
	app.routerMu.Lock()
	app.router = r
	app.routerMu.Unlock()
	return nil
}

func (r *PortRouter) boundApp() (*Application, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.bound {
		return nil, false
	}
	return r.app, true
}

// boundPorts is the contract.Ports implementation handed to modules. The
// owner is captured at For time and never changes.
type boundPorts struct {
	router *PortRouter
	owner  string
}

// Call routes one internal cross-owner invocation. The call retains the
// current Unit, actor, scope, generation and dispatch stack: it joins the
// caller's transaction, cannot mint authority and cannot open a new one.
func (p *boundPorts) Call(ctx context.Context, unit contract.Unit, invocation contract.Invocation) (contract.Payload, error) {
	app, ok := p.router.boundApp()
	if !ok {
		return contract.Payload{}, internalFault("ports are not bound to an application yet")
	}
	if unit == nil {
		return contract.Payload{}, invalidFault("internal calls require the current unit")
	}
	return app.dispatchNested(ctx, unit, p.owner, invocation)
}

// unboundPorts rejects every call. Assembly hands it out only before Bind.
type unboundPorts struct{}

func (unboundPorts) Call(context.Context, contract.Unit, contract.Invocation) (contract.Payload, error) {
	return contract.Payload{}, internalFault("ports are not bound to an application yet")
}
