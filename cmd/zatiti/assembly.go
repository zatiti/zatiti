package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
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

// backupCapability is the one-method consistent-backup seam the shared
// contract names contract.DatabaseBackup. The landed internal/contract does
// not declare it yet (the seam is a pending revision-2 follow-up), so the
// entrypoint declares the identical method set locally; when the contract
// type lands this alias is replaced by it without changing any caller.
type backupCapability interface {
	Backup(ctx context.Context, w io.Writer) error
}

// databaseBackup is the one-method capability entrypoint assembly hands to
// installation. Its method set is exactly Backup: installation can never
// widen it into query, write, migration, event-feed or file-path access, and
// no other module, adapter, server or client ever receives it.
type databaseBackup struct{ db contract.Database }

func (b databaseBackup) Backup(ctx context.Context, w io.Writer) error {
	return b.db.Backup(ctx, w)
}

var _ backupCapability = databaseBackup{}

// installationBackupBound reports whether the assembly binds the consistent
// backup capability into installation. The contract seam
// (installation.WithDatabaseBackup) is a pending follow-up on the landed
// installation package: until it exists the capability stays unbound,
// installation.backup and installation.restore report prerequisite_missing
// by the installation package's own rule, and serve logs the gap at startup.
const installationBackupBound = false

// bindInstallationBackup constructs the installation module with the backup
// capability. It is the single place the seam is wired; when
// installation.WithDatabaseBackup lands, this becomes
//
//	return installation.New(deps, installation.WithDatabaseBackup(backup))
//
// and installationBackupBound flips to true. The wrapper is built and
// type-checked either way so the seam cannot regress to passing the
// Database value.
func bindInstallationBackup(deps contract.Dependencies, backup backupCapability) (contract.Module, error) {
	if backup == nil {
		return nil, errors.New("installation backup capability wrapper is nil")
	}
	return installation.New(deps)
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
func modules(router *application.PortRouter, clock contract.Clock, ids contract.IDSource, secrets contract.SecretStore, blobs contract.BlobStore, backup backupCapability) ([]contract.Module, *identity.Service, error) {
	deps := func(owner string) contract.Dependencies {
		return contract.Dependencies{Clock: clock, IDs: ids, Ports: router.For(owner), Secrets: secrets, Blobs: blobs}
	}
	constructors := map[string]func(contract.Dependencies) (contract.Module, error){
		"configuration": func(d contract.Dependencies) (contract.Module, error) { return configuration.New(d) },
		"skills":        func(d contract.Dependencies) (contract.Module, error) { return skills.New(d) },
		"connections":   func(d contract.Dependencies) (contract.Module, error) { return connections.New(d) },
		"policy":        func(d contract.Dependencies) (contract.Module, error) { return policy.New(d) },
		"reviews":       func(d contract.Dependencies) (contract.Module, error) { return reviews.New(d) },
		"accounting":    func(d contract.Dependencies) (contract.Module, error) { return accounting.New(d) },
		"tasks":         func(d contract.Dependencies) (contract.Module, error) { return tasks.New(d) },
		"scheduling":    func(d contract.Dependencies) (contract.Module, error) { return scheduling.New(d) },
		"messaging":     func(d contract.Dependencies) (contract.Module, error) { return messaging.New(d) },
		"execution":     func(d contract.Dependencies) (contract.Module, error) { return execution.New(d) },
		"effects":       func(d contract.Dependencies) (contract.Module, error) { return effects.New(d) },
		"memory":        func(d contract.Dependencies) (contract.Module, error) { return memory.New(d) },
		"artifacts":     func(d contract.Dependencies) (contract.Module, error) { return artifacts.New(d) },
		"evidence":      func(d contract.Dependencies) (contract.Module, error) { return evidence.New(d) },
		"installation":  func(d contract.Dependencies) (contract.Module, error) { return bindInstallationBackup(d, backup) },
	}
	idn, err := identity.New(deps("identity"))
	if err != nil {
		return nil, nil, fmt.Errorf("identity: %w", err)
	}
	out := []contract.Module{idn}
	for _, name := range moduleOrder[1:] {
		m, err := constructors[name](deps(name))
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", name, err)
		}
		if m.Name() != name {
			return nil, nil, fmt.Errorf("module %s does not carry its assembly name %s", m.Name(), name)
		}
		out = append(out, m)
	}
	return out, idn, nil
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
	mods, _, err := modules(router, systemClock{}, randomIDs{}, nil, inertBlobs{}, databaseBackup{})
	if err != nil {
		return nil, err
	}
	reg, err := registry.New(mods)
	if err != nil {
		return nil, fmt.Errorf("registry over the landed modules: %w", err)
	}
	return reg.Public(), nil
}

// custodySecrets decorates the platform secret store so assembly learns the
// opaque reference bootstrap custodied the owner credential under. It is
// the only way the entrypoint ever sees that reference: installation returns
// metadata, identity stores a digest, and the reference itself is opaque.
type custodySecrets struct {
	contract.SecretStore
	mu   sync.Mutex
	puts []custodyPut
}

type custodyPut struct {
	key string
	ref string
}

func (s *custodySecrets) Put(ctx context.Context, key string, secret []byte) (string, error) {
	ref, err := s.SecretStore.Put(ctx, key, secret)
	if err == nil {
		s.mu.Lock()
		s.puts = append(s.puts, custodyPut{key: key, ref: ref})
		s.mu.Unlock()
	}
	return ref, err
}

// latest returns the most recent custodied reference, if any. Before the
// installation exists only bootstrap can custody anything, so the latest
// reference is the live owner credential of the bootstrap that committed.
func (s *custodySecrets) latest() (custodyPut, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.puts) == 0 {
		return custodyPut{}, false
	}
	return s.puts[len(s.puts)-1], true
}

// installationHandle is one opened installation: the startup holder's
// platform, held ownership, database and assembled application.
type installationHandle struct {
	cfg        config
	plat       *platform.Platform
	own        contract.Ownership
	db         contract.Database
	app        *application.Application
	reg        *registry.Registry
	identity   *identity.Service
	secrets    *custodySecrets
	clock      contract.Clock
	generation int64
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
	if h.db, err = storage.Open(ctx, storage.Config{Path: cfg.databasePath()}); err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}

	h.secrets = &custodySecrets{SecretStore: plat.Secrets()}
	router := application.NewPorts()
	mods, idn, err := modules(router, h.clock, randomIDs{}, h.secrets, plat.Blobs(), databaseBackup{db: h.db})
	if err != nil {
		return nil, err
	}
	h.identity = idn
	var migrations []contract.Migration
	for _, m := range mods {
		migrations = append(migrations, m.Migrations()...)
	}
	if err = h.db.Migrate(ctx, migrations); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if h.generation, err = h.db.StartGeneration(ctx); err != nil {
		return nil, fmt.Errorf("generation: %w", err)
	}
	if h.reg, err = registry.New(mods); err != nil {
		return nil, fmt.Errorf("registry over the landed modules: %w", err)
	}
	if h.app, err = application.New(h.db, h.reg, idn, h.clock, randomIDs{}); err != nil {
		return nil, fmt.Errorf("application: %w", err)
	}
	if err = router.Bind(h.app); err != nil {
		return nil, fmt.Errorf("bind: %w", err)
	}
	return h, nil
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
