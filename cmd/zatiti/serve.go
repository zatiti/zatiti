package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/controller"
	"github.com/zatiti/zatiti/internal/execution"
	"github.com/zatiti/zatiti/internal/server"
)

// defaultPollInterval is how often serve looks for the bootstrap commit
// while the installation is uninitialized. It bounds the delay between
// `zatiti init` completing and the controller starting; nothing else ticks
// on it.
const defaultPollInterval = 200 * time.Millisecond

// serveOptions are the seams tests narrow. Production uses the zero value.
type serveOptions struct {
	pollInterval time.Duration
	// listening is called once the socket is bound, before any request is
	// served. Tests use it to know when to connect.
	listening func()
	// afterAttach, if set, is called with the exact Collaborators just
	// attached to the controller, right after ctl.Attach succeeds and
	// before Run starts admitting. Production never sets it; a test uses
	// it to inspect what was actually wired (P24: proving the attached
	// Verifier is the real, non-stub internal/execution.NewVerifier
	// construction, not merely that Attach returned nil).
	afterAttach func(controller.Collaborators)
}

// runServe is `zatiti serve`: the startup holder, the private socket and
// the one controller of this state directory. It returns when ctx is
// cancelled (orderly, nil), when the installation lock is lost or the
// generation is superseded (controller_unavailable), or when a startup
// prerequisite is missing (the named fault).
func runServe(ctx context.Context, cfg config, log *slog.Logger, opts serveOptions) error {
	if err := cfg.validateServe(); err != nil {
		return &contract.Fault{Code: contract.CodeInvalidInput, Message: err.Error()}
	}
	if opts.pollInterval <= 0 {
		opts.pollInterval = defaultPollInterval
	}

	h, err := openInstallation(ctx, cfg)
	if err != nil {
		return err
	}
	defer h.close()
	log.Info("installation opened", "generation", h.generation)

	adapters, missing, err := loadAdapters(cfg.adaptersDir(), adapterDependencies(h))
	if err != nil {
		return err
	}
	for name := range adapters {
		log.Info("adapter registered", "adapter", name)
	}
	for _, name := range missing {
		log.Warn("adapter has no profile and is not registered; dispatches to it are recorded not_sent", "adapter", name)
	}
	for _, name := range unimplementedAdapters {
		log.Warn("adapter package has not landed; dispatches to it are recorded not_sent", "adapter", name)
	}
	unregistered := append(append([]string{}, missing...), unimplementedAdapters...)

	var tlsCfg *tls.Config
	if cfg.RemoteAddress != "" {
		if tlsCfg, err = remoteTLSConfig(cfg); err != nil {
			return err
		}
	}
	srv, err := server.New(server.Config{
		SocketPath: cfg.SocketPath, RemoteAddress: cfg.RemoteAddress, TLSConfig: tlsCfg, MaxBodyBytes: defaultMaxBodySize,
	}, h.app)
	if err != nil {
		return err
	}
	log.Info("listening", "socket", cfg.SocketPath, "remote", cfg.RemoteAddress != "")
	if opts.listening != nil {
		opts.listening()
	}

	serveCtx, stopServe := context.WithCancel(ctx)
	defer stopServe()
	serverDone := make(chan error, 1)
	go func() { serverDone <- srv.Serve(serveCtx) }()

	controllerDone := make(chan error, 1)
	running := make(chan *controller.Controller, 1)
	go func() {
		controllerDone <- superviseController(serveCtx, h, adapters, unregistered, log, opts.pollInterval, opts.afterAttach, running)
	}()

	var cause error
	select {
	case <-ctx.Done():
	case err := <-serverDone:
		cause = fmt.Errorf("listener stopped: %w", err)
	case err := <-controllerDone:
		cause = err
		controllerDone = nil
	}

	// Bounded shutdown: stop accepting, let in-flight requests finish, stop
	// the controller's admission and wait for work outside transactions to
	// record its outcome, then release storage and the lock (deferred).
	grace, cancelGrace := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancelGrace()
	if err := srv.Close(grace); err != nil {
		log.Warn("listener did not close cleanly", "error", err.Error())
	}
	stopServe()
	if controllerDone != nil {
		select {
		case ctl := <-running:
			if err := ctl.Stop(grace); err != nil {
				log.Warn("controller did not stop cleanly", "error", err.Error())
			}
		default:
		}
		select {
		case err := <-controllerDone:
			if cause == nil && err != nil && !errors.Is(err, context.Canceled) {
				cause = err
			}
		case <-grace.Done():
			log.Warn("controller did not stop within the shutdown grace period")
		}
	}
	if cause != nil && !errors.Is(cause, context.Canceled) {
		return cause
	}
	log.Info("controller stopped")
	return nil
}

// superviseController brings the one controller of this installation up
// once the installation exists: it waits for bootstrap to commit, completes
// bootstrap in the process that served it, attaches the service identity
// and every supported collaborator (P24 item 2: the real trusted verifier,
// every landed local job adapter and the real worker operator, not only
// Identity/Blobs) and runs the scheduler until ctx ends or admission stops.
// The controller is published on running right before Run so shutdown can
// Stop it; the return value is Run's verdict or the startup failure.
func superviseController(ctx context.Context, h *installationHandle, adapters map[string]contract.Adapter, unregisteredAdapters []string, log *slog.Logger, poll time.Duration, afterAttach func(controller.Collaborators), running chan<- *controller.Controller) error {
	installationID, err := h.initialized(ctx)
	if err != nil {
		return err
	}
	if installationID == "" {
		log.Info("installation is not initialized; serving bootstrap only until `zatiti init` completes")
		if installationID, err = awaitInitialized(ctx, h, poll); err != nil {
			return err
		}
		profile, err := handOverOwnerCredential(ctx, h)
		if err != nil {
			return fmt.Errorf("completing bootstrap: %w", err)
		}
		log.Info("bootstrap completed", "installation_id", installationID, "owner_profile", profile)
	}

	identity, err := resolveControllerIdentity(ctx, h, installationID)
	if err != nil {
		return err
	}

	// The trusted verifier independently establishes task acceptance
	// (internal/execution.NewVerifier is the real, landed constructor --
	// never a stub); its own construction failure fails startup here rather
	// than silently leaving Collaborators.Verifier nil.
	verifier, err := execution.NewVerifier(contract.VerifierDependencies{Clock: h.clock, Blobs: h.plat.Blobs()})
	if err != nil {
		return fmt.Errorf("constructing the trusted verifier: %w", err)
	}
	jobs := buildJobRunners(h.jobRunners)

	ctl, err := controller.New(controller.Config{StateDir: h.cfg.StateDir, TickInterval: h.cfg.TickInterval}, h.app, h.db, h.own, adapters, h.clock)
	if err != nil {
		return err
	}
	collab := controller.Collaborators{
		Identity: identity,
		Blobs:    h.plat.Blobs(),
		Jobs:     jobs,
		// h.app (*application.Application) implements contract.WorkerOperator
		// directly (internal/application/worker.go); no separate
		// construction step exists.
		Operator: h.app,
		Verifier: verifier,
	}
	if err := ctl.Attach(collab); err != nil {
		return err
	}
	if afterAttach != nil {
		afterAttach(collab)
	}

	missingRunners := missingJobRunners(jobs)
	for _, k := range missingRunners {
		log.Warn("catalog job kind has no attached runner; a pending job of this kind is never claimed", "owner", k.Owner, "operation", k.Operation)
	}
	level, reqs := assemblyReadiness(adapters, unregisteredAdapters, missingRunners, true, helperReceiptKeyProvisioned(ctx, h.plat.Secrets()))
	for _, r := range reqs {
		log.Warn("startup requirement", "categories", r.Categories, "message", r.Message)
	}
	log.Info("controller running", "installation_id", installationID, "generation", h.generation, "controller_principal", identity.PrincipalID, "readiness", level)
	running <- ctl
	return ctl.Run(ctx)
}

// awaitInitialized returns the installation identity once bootstrap has
// committed. Losing the lock while waiting ends the process like it would
// end a running controller: nothing is admitted without ownership.
func awaitInitialized(ctx context.Context, h *installationHandle, poll time.Duration) (contract.ID, error) {
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-h.own.Lost():
			return "", &contract.Fault{Code: contract.CodeControllerUnavailable, Message: "installation ownership was lost; admission stopped"}
		case <-t.C:
			id, err := h.initialized(ctx)
			if err != nil {
				return "", err
			}
			if id != "" {
				return id, nil
			}
		}
	}
}
