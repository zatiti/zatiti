package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/zatiti/zatiti/internal/accounting"
	"github.com/zatiti/zatiti/internal/application"
	"github.com/zatiti/zatiti/internal/artifacts"
	"github.com/zatiti/zatiti/internal/configuration"
	"github.com/zatiti/zatiti/internal/connections"
	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/effects"
	"github.com/zatiti/zatiti/internal/evidence"
	"github.com/zatiti/zatiti/internal/execution"
	"github.com/zatiti/zatiti/internal/identity"
	"github.com/zatiti/zatiti/internal/installation"
	"github.com/zatiti/zatiti/internal/memory"
	"github.com/zatiti/zatiti/internal/messaging"
	"github.com/zatiti/zatiti/internal/platform"
	"github.com/zatiti/zatiti/internal/policy"
	"github.com/zatiti/zatiti/internal/registry"
	"github.com/zatiti/zatiti/internal/reviews"
	"github.com/zatiti/zatiti/internal/scheduling"
	"github.com/zatiti/zatiti/internal/skills"
	"github.com/zatiti/zatiti/internal/storage"
	"github.com/zatiti/zatiti/internal/tasks"
)

// systemClock is the production clock.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

// randomIDs is the production identity source.
type randomIDs struct{}

func (randomIDs) New() contract.ID { return contract.NewID() }

// databaseBackup is the one-method capability entrypoint assembly hands to
// installation through installation.WithDatabaseBackup. Its method set is
// exactly Backup: installation can never widen it into query, write,
// migration, event-feed or file-path access, and no other module, adapter,
// server or client ever receives it.
type databaseBackup struct{ db contract.Database }

func (b databaseBackup) Backup(ctx context.Context, w io.Writer) error {
	return b.db.Backup(ctx, w)
}

var _ contract.DatabaseBackup = databaseBackup{}

// bindInstallationBackup constructs the installation module with the
// consistent-backup capability bound. It is the single place the seam is
// wired; the wrapper, never the Database value, crosses it.
func bindInstallationBackup(deps contract.Dependencies, backup contract.DatabaseBackup) (*installation.Service, error) {
	if backup == nil {
		return nil, errors.New("installation backup capability wrapper is nil")
	}
	return installation.New(deps, installation.WithDatabaseBackup(backup))
}

// moduleOrder is the frozen assembly and migration order: identity first
// (it is also the authenticator), installation last (it composes the rest
// at bootstrap).
var moduleOrder = []string{
	"identity", "configuration", "skills", "connections", "policy", "reviews",
	"accounting", "tasks", "scheduling", "messaging", "execution", "effects",
	"memory", "artifacts", "evidence", "installation",
}

// modules constructs the sixteen landed domain modules with owner-bound
// ports, in assembly order. The identity module is returned separately: it
// is the application's authenticator and resolves the controller principal. No constructor queries a peer or starts
// a goroutine, so this is safe before Bind and safe in a descriptor-only
// process.
//
// The third return value is every constructed module that also implements
// contract.LocalJobRunner, keyed by its owner/module name (m.Name()) --
// discovered generically by type assertion rather than a hardcoded package
// list, so a future job-owning module needs no change here. cmd/zatiti's
// jobs.go (buildJobRunners) turns this into the controller.JobRunner map
// Collaborators.Jobs requires, keyed by the frozen owner/operation job
// kinds each of these owners actually implements (landedJobKinds).
func modules(router *application.PortRouter, clock contract.Clock, ids contract.IDSource, secrets contract.SecretStore, blobs contract.BlobStore, backup contract.DatabaseBackup, mcpProfiles ...json.RawMessage) ([]contract.Module, *identity.Service, map[string]contract.LocalJobRunner, error) {
	deps := func(owner string) contract.Dependencies {
		return contract.Dependencies{Clock: clock, IDs: ids, Ports: router.For(owner), Secrets: secrets, Blobs: blobs}
	}
	constructors := map[string]func(contract.Dependencies) (contract.Module, error){
		"configuration": func(d contract.Dependencies) (contract.Module, error) { return configuration.New(d) },
		"skills":        func(d contract.Dependencies) (contract.Module, error) { return skills.New(d) },
		"connections": func(d contract.Dependencies) (contract.Module, error) {
			var raw json.RawMessage
			if len(mcpProfiles) > 0 {
				raw = mcpProfiles[0]
			}
			return connections.NewWithMCPProfile(d, raw)
		},
		"policy":       func(d contract.Dependencies) (contract.Module, error) { return policy.New(d) },
		"reviews":      func(d contract.Dependencies) (contract.Module, error) { return reviews.New(d) },
		"accounting":   func(d contract.Dependencies) (contract.Module, error) { return accounting.New(d) },
		"tasks":        func(d contract.Dependencies) (contract.Module, error) { return tasks.New(d) },
		"scheduling":   func(d contract.Dependencies) (contract.Module, error) { return scheduling.New(d) },
		"messaging":    func(d contract.Dependencies) (contract.Module, error) { return messaging.New(d) },
		"execution":    func(d contract.Dependencies) (contract.Module, error) { return execution.New(d) },
		"effects":      func(d contract.Dependencies) (contract.Module, error) { return effects.New(d) },
		"memory":       func(d contract.Dependencies) (contract.Module, error) { return memory.New(d) },
		"artifacts":    func(d contract.Dependencies) (contract.Module, error) { return artifacts.New(d) },
		"evidence":     func(d contract.Dependencies) (contract.Module, error) { return evidence.New(d) },
		"installation": func(d contract.Dependencies) (contract.Module, error) { return bindInstallationBackup(d, backup) },
	}
	idn, err := identity.New(deps("identity"))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("identity: %w", err)
	}
	out := []contract.Module{idn}
	jobRunners := map[string]contract.LocalJobRunner{}
	addJobRunner(jobRunners, idn)
	for _, name := range moduleOrder[1:] {
		m, err := constructors[name](deps(name))
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", name, err)
		}
		if m.Name() != name {
			return nil, nil, nil, fmt.Errorf("module %s does not carry its assembly name %s", m.Name(), name)
		}
		out = append(out, m)
		addJobRunner(jobRunners, m)
	}
	return out, idn, jobRunners, nil
}

// addJobRunner records m under its own name when it implements
// contract.LocalJobRunner, discovered generically by type assertion (see
// modules' doc comment).
func addJobRunner(jobRunners map[string]contract.LocalJobRunner, m contract.Module) {
	if r, ok := m.(contract.LocalJobRunner); ok {
		jobRunners[m.Name()] = r
	}
}

// inertBlobs is the blob store of a descriptor-only assembly (the CLI and
// MCP processes, which need the operation catalog and nothing else). Every
// call fails closed: no bytes are ever staged or opened in a client process.
type inertBlobs struct{}

func (inertBlobs) Stage(context.Context, io.Reader, int64) (string, contract.Digest, int64, error) {
	return "", "", 0, inertBlobFault()
}
func (inertBlobs) Publish(context.Context, string, contract.Digest) error { return inertBlobFault() }
func (inertBlobs) Open(context.Context, contract.Digest, int64, int64) (io.ReadCloser, error) {
	return nil, inertBlobFault()
}
func (inertBlobs) RemoveStaged(context.Context, string) error { return inertBlobFault() }

func inertBlobFault() error {
	return &contract.Fault{Code: contract.CodeControllerUnavailable, Message: "this process holds no blob store; artifact bytes live only in the controller"}
}

// catalog derives the public operation catalog from the runtime typed
// registry over the real modules, exactly as the controller registers them.
// It opens no storage, no platform and no socket.
func catalog() ([]contract.Descriptor, error) {
	router := application.NewPorts()
	mods, _, _, err := modules(router, systemClock{}, randomIDs{}, nil, inertBlobs{}, databaseBackup{})
	if err != nil {
		return nil, err
	}
	reg, err := registry.New(mods)
	if err != nil {
		return nil, fmt.Errorf("registry over the landed modules: %w", err)
	}
	return reg.Public(), nil
}

// installationHandle is one opened installation: the startup holder's
// platform, held ownership, database and assembled application.
type installationHandle struct {
	cfg          config
	plat         *platform.Platform
	own          contract.Ownership
	db           contract.Database
	app          *application.Application
	reg          *registry.Registry
	identity     *identity.Service
	installation *installation.Service
	secrets      contract.SecretStore
	// mcpProfile is the one installation-local MCP profile snapshot shared
	// by connections admission and the adapter instance for this lifetime.
	mcpProfile json.RawMessage
	clock      contract.Clock
	generation int64
	// jobRunners is every constructed module that implements
	// contract.LocalJobRunner, keyed by owner/module name (modules()'s
	// third return value). superviseController turns it into the
	// controller.JobRunner map Collaborators.Jobs requires.
	jobRunners map[string]contract.LocalJobRunner
	// owners is every constructed domain module keyed by owner name. It
	// exists for exactly one caller: restore.go's restoreLifecycle, which
	// must reach internal/effects, internal/identity and internal/memory
	// through contract.Module.Handle directly while the database swap has
	// left this lifetime with no usable *application.Application to route
	// through. Nothing else may use it to bypass application dispatch.
	owners map[string]contract.Module
}

// openInstallation is the frozen startup order: platform.Open -> Acquire ->
// storage.Open -> modules -> Migrate -> StartGeneration -> registry.New ->
// application.New -> Bind. Any failure releases what was opened. The
// returned handle holds the installation lock until close.
func openInstallation(ctx context.Context, cfg config) (_ *installationHandle, err error) {
	plat, err := platform.Open(platform.Config{
		StateDir: cfg.StateDir, CredentialBackend: cfg.CredentialBackend, MasterKeyRef: cfg.MasterKeyRef,
	})
	if err != nil {
		return nil, fmt.Errorf("platform: %w", err)
	}
	h := &installationHandle{cfg: cfg, plat: plat, clock: systemClock{}}
	defer func() {
		if err != nil {
			h.close()
		}
	}()

	if h.own, err = plat.Acquire(ctx); err != nil {
		return nil, fmt.Errorf("installation lock: %w", err)
	}
	h.secrets = plat.Secrets()
	if err = h.assemble(ctx); err != nil {
		return nil, err
	}
	return h, nil
}

// assemble runs storage.Open -> modules -> Migrate -> StartGeneration ->
// registry.New -> application.New -> Bind against h's already-open platform
// and already-held installation lock, populating h.db/h.reg/h.identity/
// h.jobRunners/h.generation/h.app. It is openInstallation's own startup
// body, factored out so reassembleAfterRestoreHandoff (restore.go) can
// reuse it verbatim: controller.ErrRestoreHandoff's own contract is that the
// caller "reassembles Application/Controller exactly as at first startup"
// over the freshly reopened database, never re-acquiring platform.Acquire
// (P32's restore protocol retains exclusive installation ownership through
// the whole swap) and never skipping StartGeneration (calling it again here
// is exactly what fences out the swap-performing lifetime that just ended --
// P32's own required test, "no old-generation controller/worker can commit
// after reopen").
func (h *installationHandle) assemble(ctx context.Context) error {
	db, err := storage.Open(ctx, storage.Config{Path: h.cfg.databasePath()})
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	h.db = db

	router := application.NewPorts()
	profile, err := readMCPAdmissionProfile(h.cfg.adaptersDir())
	if err != nil {
		return err
	}
	h.mcpProfile = append(json.RawMessage(nil), profile...)
	mods, idn, jobRunners, err := modules(router, h.clock, randomIDs{}, h.secrets, h.plat.Blobs(), databaseBackup{db: h.db}, h.mcpProfile)
	if err != nil {
		return err
	}
	h.identity = idn
	h.jobRunners = jobRunners
	h.owners = make(map[string]contract.Module, len(mods))
	for _, m := range mods {
		h.owners[m.Name()] = m
	}
	installed, ok := h.owners["installation"].(*installation.Service)
	if !ok {
		return errors.New("installation module does not expose owner credential metadata")
	}
	h.installation = installed
	var migrations []contract.Migration
	for _, m := range mods {
		migrations = append(migrations, m.Migrations()...)
	}
	if err = h.db.Migrate(ctx, migrations); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if h.generation, err = h.db.StartGeneration(ctx); err != nil {
		return fmt.Errorf("generation: %w", err)
	}
	if h.reg, err = registry.New(mods); err != nil {
		return fmt.Errorf("registry over the landed modules: %w", err)
	}
	if h.app, err = application.New(h.db, h.reg, idn, h.clock, randomIDs{}); err != nil {
		return fmt.Errorf("application: %w", err)
	}
	if err = router.Bind(h.app); err != nil {
		return fmt.Errorf("bind: %w", err)
	}
	return nil
}

// reassembleAfterRestoreHandoff closes the stale application and database
// this lifetime's Controller reported controller.ErrRestoreHandoff over
// (its Application was built over the database handle CommitRestore closed
// as part of the atomic swap; see internal/controller/restore.go's package
// doc) and reassembles them fresh: an ordinary storage.Open at the same
// path already observes the swap CommitRestore made durable. It never
// touches h.own/h.plat -- the installation lock and platform stay held for
// this process's entire lifetime, exactly as the restore protocol's step 4
// requires ("retaining exclusive installation ownership").
func (h *installationHandle) reassembleAfterRestoreHandoff(ctx context.Context) error {
	if h.app != nil {
		h.app.Close()
		h.app = nil
	}
	if h.db != nil {
		// Idempotent: CommitRestore already closed the same underlying
		// database this field points to as part of the swap
		// (checkpointAndClose sets its own closed flag); calling Close
		// again here is the literal "close old ... database" step this
		// card's item 2 names, and every method on an already-closed
		// database reports a harmless closed fault rather than panicking.
		_ = h.db.Close()
		h.db = nil
	}
	return h.assemble(ctx)
}

// initialized reports the installation identity when bootstrap has
// committed. The first persisted event carries the installation scope; an
// eventless database is uninitialized. This is the same observation the
// controller and the application make; the entrypoint owns the database and
// reads nothing else from it.
func (h *installationHandle) initialized(ctx context.Context) (contract.ID, error) {
	events, err := h.db.Events(ctx, 0, 1)
	if err != nil {
		return "", err
	}
	if len(events) == 0 {
		return "", nil
	}
	return events[0].Scope.InstallationID, nil
}

// close releases everything in reverse order: application admission, the
// database, the installation lock, then the platform keys. It is safe on a
// partially opened handle.
func (h *installationHandle) close() {
	if h.app != nil {
		h.app.Close()
	}
	if h.db != nil {
		_ = h.db.Close()
	}
	if h.own != nil {
		_ = h.own.Close()
	}
	if h.plat != nil {
		_ = h.plat.Close()
	}
}
