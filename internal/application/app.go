// Package application owns authenticated operation execution, transaction
// composition, internal port allowlists and durable submission replay. It
// composes owner methods through a checked in-process dispatcher: ordinary
// Go calls, not another server or scheduler.
package application

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/zatiti/zatiti/internal/contract"
)

// Catalog is the registry surface the dispatcher consumes. *registry.Registry
// satisfies it; the narrow interface keeps the dispatcher independent of the
// registry's construction and makes the seam explicit.
type Catalog interface {
	Lookup(id string, version int64) (contract.Descriptor, contract.Handler, error)
	Public() []contract.Descriptor
}

// localIOOperations is the frozen set of registered local IO operations the
// dispatcher routes through Prepare/Perform/Finish instead of an ordinary
// handler call. Every other operation, including every other operation of
// the same owners, takes the ordinary path.
var localIOOperations = map[string]bool{
	"artifact.upload.chunk":     true,
	"artifact.upload.finish":    true,
	"artifact.upload.cancel":    true,
	"artifact.read":             true,
	"artifact.export":           true,
	"skill.import":              true,
	"connection.setup.begin":    true,
	"connection.setup.complete": true,
	"connection.setup.cancel":   true,
	"installation.init":         true,
	"installation.backup":       true,
	"installation.restore":      true,
}

// Application executes authenticated operations against the composed owner
// modules. It owns the one ordered dispatch: envelope validation, scope
// derivation, authority and policy gates, submission replay, transaction
// composition and result envelopes.
type Application struct {
	db    contract.Database
	reg   Catalog
	auth  contract.Authenticator
	clock contract.Clock
	ids   contract.IDSource

	// ioLookup resolves the registered local IO operations to their owning
	// services when the registry exposes the capability (see IOLookup).
	ioLookup IOLookup

	// systemActor identifies application-owned reads (authentication
	// snapshots). It is well-formed so storage accepts the snapshot but
	// carries no grantable authority: handlers never see it as their actor.
	systemActor contract.Actor

	routerMu sync.RWMutex
	router   *PortRouter

	closed atomic.Bool

	installMu    sync.RWMutex
	installation contract.ID
}

// New composes the application over an opened database and a fully
// constructed registry. The registry must already hold every module with
// their ports injected through NewPorts/For; binding the port router happens
// after New and before serving. Constructor execution opens no transaction
// and queries no peer.
func New(
	db contract.Database,
	registry Catalog,
	auth contract.Authenticator,
	clock contract.Clock,
	ids contract.IDSource,
) (*Application, error) {
	switch {
	case db == nil:
		return nil, internalFault("application requires a database")
	case registry == nil:
		return nil, internalFault("application requires an operation registry")
	case auth == nil:
		return nil, internalFault("application requires an identity authenticator")
	case clock == nil:
		return nil, internalFault("application requires a clock")
	case ids == nil:
		return nil, internalFault("application requires an identity source")
	}
	app := &Application{
		db:          db,
		reg:         registry,
		auth:        auth,
		clock:       clock,
		ids:         ids,
		systemActor: contract.Actor{PrincipalID: ids.New(), Kind: contract.KindService},
	}
	if lookup, ok := registry.(IOLookup); ok {
		app.ioLookup = lookup
	}
	return app, nil
}

// Ports returns the application-side ports view, bound to the "application"
// caller identity. Components that run on behalf of the dispatcher itself —
// verifiers, outbox consumers — use it for in-transaction internal calls;
// the ordinary domain modules receive their own identity from PortRouter.
// Before Bind the returned ports reject every call: no service runs before
// assembly completes.
func (a *Application) Ports() contract.Ports {
	a.routerMu.RLock()
	router := a.router
	a.routerMu.RUnlock()
	if router == nil {
		return unboundPorts{}
	}
	return router.For(applicationCaller)
}

// Close releases nothing the application owns directly — storage and
// ownership belong to assembly — but stops admission. Further Invoke,
// Internal and Authenticate calls fail with controller_unavailable.
func (a *Application) Close() { a.closed.Store(true) }

func (a *Application) entryGuard() error {
	if a.closed.Load() {
		return unavailableFault("controller is shutting down")
	}
	return nil
}

// installationID resolves the local installation identity. Events carry the
// installation scope on every row, so the first persisted event reveals it;
// an eventless database is uninitialized and only bootstrap may run.
func (a *Application) installationID(ctx context.Context) (contract.ID, error) {
	a.installMu.RLock()
	id := a.installation
	a.installMu.RUnlock()
	if id != "" {
		return id, nil
	}
	events, err := a.db.Events(ctx, 0, 1)
	if err != nil {
		return "", err
	}
	if len(events) == 0 {
		return "", nil
	}
	resolved := events[0].Scope.InstallationID
	if resolved == "" {
		return "", internalFault("stored event carries no installation scope")
	}
	a.installMu.Lock()
	if a.installation == "" {
		a.installation = resolved
	}
	a.installMu.Unlock()
	return resolved, nil
}

// Authenticate resolves a bearer credential to the current actor on a
// dedicated read snapshot. Application never reads identity-owned tables
// itself; the identity Authenticator sees the snapshot unit.
func (a *Application) Authenticate(ctx context.Context, credential []byte) (contract.Actor, error) {
	if err := a.entryGuard(); err != nil {
		return contract.Actor{}, err
	}
	installation, err := a.installationID(ctx)
	if err != nil {
		return contract.Actor{}, err
	}
	if installation == "" {
		return contract.Actor{}, permissionFault("installation is not initialized")
	}
	var actor contract.Actor
	err = a.db.Read(ctx, a.systemActor, contract.Scope{InstallationID: installation}, func(u contract.Unit) error {
		resolved, authErr := a.auth.Authenticate(ctx, u, credential)
		if authErr != nil {
			return authErr
		}
		actor = resolved
		return nil
	})
	if err != nil {
		return contract.Actor{}, err
	}
	return actor, nil
}

// AuthenticateCertificate maps a verified TLS peer's certificate SPKI
// fingerprint to the current actor on a dedicated read snapshot. Only the
// TLS layer may call it; operation input cannot supply the fingerprint.
func (a *Application) AuthenticateCertificate(ctx context.Context, fingerprint contract.Digest) (contract.Actor, error) {
	if err := a.entryGuard(); err != nil {
		return contract.Actor{}, err
	}
	if fingerprint == "" {
		return contract.Actor{}, invalidFault("certificate fingerprint is required")
	}
	installation, err := a.installationID(ctx)
	if err != nil {
		return contract.Actor{}, err
	}
	if installation == "" {
		return contract.Actor{}, permissionFault("installation is not initialized")
	}
	var actor contract.Actor
	err = a.db.Read(ctx, a.systemActor, contract.Scope{InstallationID: installation}, func(u contract.Unit) error {
		resolved, authErr := a.auth.AuthenticateCertificate(ctx, u, fingerprint)
		if authErr != nil {
			return authErr
		}
		actor = resolved
		return nil
	})
	if err != nil {
		return contract.Actor{}, err
	}
	return actor, nil
}
