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
	"github.com/zatiti/zatiti/internal/storage"
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
	// restoreLifecycle overrides the production restoreLifecycle value
	// (restore.go) Collaborators.RestoreLifecycle is attached with.
	// Production never sets it. A test sets it to isolate this package's
	// own reassembly loop from the owner-merge machinery -- for example to
	// drive a swap whose merge is a no-op -- when the behavior under test
	// is the ErrRestoreHandoff handling rather than the merge itself.
	restoreLifecycle controller.RestoreLifecycle
}

// runServe is `zatiti serve`: the startup holder, the private socket and
// the one controller of this state directory. It returns when ctx is
// cancelled (orderly, nil), when the installation lock is lost or the
// generation is superseded (controller_unavailable), or when a startup
// prerequisite is missing (the named fault).
//
// It is an outer loop, not a one-shot sequence (P33 item 2): runServeOnce
// assembles and runs one controller/server lifetime, and a lifetime that
// itself performed a restore's atomic database swap ends by design with
// controller.ErrRestoreHandoff (internal/controller/restore.go), not a
// fault -- that lifetime's own Application is permanently stale (built over
// the database handle the swap closed), but the swap, overlay merge and
// storage resume it already completed are durable. On that signal this
// loop reassembles Application/Controller/server exactly as at first
// startup (reassembleAfterRestoreHandoff, assembly.go) over the freshly
// reopened database and runs again, under the SAME held installation lock
// -- never re-acquiring it -- so a restart mid-protocol always resumes
// forward instead of leaving the installation permanently unservable.
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
	logPendingRestoreMarker(ctx, h, log)

	for {
		err := runServeOnce(ctx, cfg, h, log, opts)
		if !errors.Is(err, controller.ErrRestoreHandoff) {
			return err
		}
		log.Info("restore handoff durable; this lifetime's own application is now stale, reassembling over the freshly reopened database", "previous_generation", h.generation)
		if rerr := h.reassembleAfterRestoreHandoff(ctx); rerr != nil {
			return fmt.Errorf("reassemble after restore handoff: %w", rerr)
		}
		log.Info("reassembled after restore handoff", "generation", h.generation)
		logPendingRestoreMarker(ctx, h, log)
	}
}

// logPendingRestoreMarker is P33 item 1's startup detection: read whether
// storage reports paused for restore right after the database is (re)opened
// -- before any adapter loads, before the listener binds, before this
// process admits a single request -- so a pending restore left by a dead
// process is visible in the log at the earliest possible point, not
// discovered only when a write later fails. It never drives the restore
// forward itself: that remains Controller.start's own
// recoverRestoreBeforeFence (internal/controller/restore.go), run from
// inside runServeOnce below via ctl.Run, before the ordinary generation
// fence and therefore before any ordinary admission. The actual
// write-safety guarantee (P33's required "restart never serves a partially
// restored writable installation" behavior) does not depend on this log
// line: storage.Write refuses outright while RestorePaused is true, checked
// fresh on every call, independent of whether this line ever runs.
func logPendingRestoreMarker(ctx context.Context, h *installationHandle, log *slog.Logger) {
	restorable, ok := h.db.(storage.Restorable)
	if !ok {
		return
	}
	paused, err := restorable.RestorePaused(ctx)
	if err != nil {
		log.Warn("could not read the restore-pause marker at startup", "error", err.Error())
		return
	}
	if paused {
		log.Warn("database reports paused for restore at startup; ordinary writes are refused until the pending restore is driven to resumed or explicitly failed")
	}
}

// runServeOnce assembles adapters, the private socket and one controller
// lifetime over h's already-open installation, and runs until ctx ends, the
// controller reports a fault, or the controller reports
// controller.ErrRestoreHandoff. h is never opened or closed here: runServe
// owns h's lifetime across every call this loop makes.
func runServeOnce(ctx context.Context, cfg config, h *installationHandle, log *slog.Logger, opts serveOptions) error {
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
	// server.New has bound the private listener. An already initialized
	// installation keeps its previous locator until the committed owner
	// credential is verified and the new locator is atomically published.
	// Clearing it here would misrepresent a failed recovery as first setup.
	installedID, discoveryErr := h.initialized(ctx)
	if discoveryErr == nil && installedID == "" {
		discoveryErr = h.publishPrebootstrapDiscovery(ctx)
	}
	if discoveryErr != nil {
		grace, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		_ = srv.Close(grace)
		return fmt.Errorf("preparing desktop discovery: %w", discoveryErr)
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
		controllerDone <- superviseController(serveCtx, h, adapters, unregistered, log, opts.pollInterval, opts.afterAttach, opts.restoreLifecycle, running)
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
// Identity/Blobs; P33 item 3: the restore lifecycle capability, through the
// same explicit Collaborators seam as every other trusted dependency) and
// runs the scheduler until ctx ends or admission stops. The controller is
// published on running right before Run so shutdown can Stop it; the
// return value is Run's verdict (including controller.ErrRestoreHandoff) or
// the startup failure.
func superviseController(ctx context.Context, h *installationHandle, adapters map[string]contract.Adapter, unregisteredAdapters []string, log *slog.Logger, poll time.Duration, afterAttach func(controller.Collaborators), restoreLifecycleOverride controller.RestoreLifecycle, running chan<- *controller.Controller) error {
	installationID, err := h.initialized(ctx)
	if err != nil {
		return err
	}
	newBootstrap := installationID == ""
	if installationID == "" {
		log.Info("installation is not initialized; serving bootstrap only until `zatiti init` completes")
		if installationID, err = awaitInitialized(ctx, h, poll); err != nil {
			return err
		}
	}
	profile, owner, err := recoverOwnerCredential(ctx, h, installationID)
	if err != nil {
		return fmt.Errorf("recovering committed owner credential: %w", err)
	}
	if err := h.publishInitializedDiscovery(ctx, installationID, owner); err != nil {
		return fmt.Errorf("publishing initialized desktop discovery: %w", err)
	}
	if newBootstrap {
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

	// restoreLifecycle is the production value (restore.go) unless a test
	// overrides it (serveOptions.restoreLifecycle; see runServeOnce). This
	// is the explicit lifecycle seam P33 item 3 requires: it is attached
	// here, alongside every other trusted collaborator, and nowhere else --
	// the CLI/MCP client processes (run.go, mcp.go) never construct a
	// Controller at all, so they never see it. Keeping work/secret access
	// out of ordinary client processes falls out of that structure rather
	// than a separate check.
	restoreLifecycleValue := restoreLifecycleOverride
	if restoreLifecycleValue == nil {
		restoreLifecycleValue = newRestoreLifecycle(h, installationID)
	}

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
		Operator:         h.app,
		Verifier:         verifier,
		RestoreLifecycle: restoreLifecycleValue,
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
