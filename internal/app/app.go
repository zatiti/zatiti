// internal/app/app.go
//
// Composition root: wiring + top-level command dispatch. Domain logic
// stays in owning roots; this file routes and normalizes exit codes
// (RFC §8.4) only. The operation registry is the single source of
// truth for what dispatch accepts.

package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"zatiti/internal/identity"
	"zatiti/internal/operations"
	"zatiti/internal/state"
	"zatiti/internal/transport"
)

// Exit codes, per RFC §8.4.
const (
	ExitOK            = 0
	ExitFailure       = 1
	ExitInputError    = 2
	ExitDenied        = 3
	ExitStale         = 4
	ExitPrereqMissing = 5
	ExitUnavailable   = 6
)

const Version = "0.0.0-scaffold"

// App is the controller/CLI application. One instance per process.
type App struct {
	registry *operations.Registry
	cli      *transport.CLI
}

// New wires the application. Registry construction failure is a
// programming error and surfaces as an error here, not a runtime panic.
func New() (*App, error) {
	reg, err := operations.NewBuiltin()
	if err != nil {
		return nil, fmt.Errorf("app: operation registry: %w", err)
	}
	return &App{
		registry: reg,
		cli:      transport.NewCLI(),
	}, nil
}

// Run executes one CLI invocation and returns the process exit code.
func (a *App) Run(ctx context.Context, args []string) (int, error) {
	if len(args) == 0 {
		a.cli.PrintUsage(osStderr)
		return ExitInputError, errors.New("no command given")
	}

	switch args[0] {
	case "init":
		return a.runInit(ctx, args[1:])
	case "serve":
		return a.runServe(ctx, args[1:])
	case "mcp":
		if len(args) >= 2 && args[1] == "serve" {
			return a.runMCPServe(ctx, args[2:])
		}
		return ExitInputError, fmt.Errorf("unknown mcp subcommand (expected: mcp serve)")
	case "version", "--version", "-v":
		return a.runVersion(ctx, args)
	default:
		// Dotted operation names dispatch through the registry.
		if _, ok := a.registry.Lookup(args[0]); ok {
			return a.runOperation(ctx, args[0], args[1:])
		}
		return ExitInputError, fmt.Errorf("unknown command %q", args[0])
	}
}

// runOrgList: the T2.1 read path. Opens state read-only-ish (the
// installation lock is still taken: even reads assert single-controller
// ownership), enumerates organizations, prints one per line to stdout.
func (a *App) runOrgList(ctx context.Context, args []string) (int, error) {
	opts, err := a.cli.ParseDataDirFlag(args, "organization.list")
	if err != nil {
		return ExitInputError, err
	}
	store, err := state.Open(opts.DataDir)
	if err != nil {
		if errors.Is(err, state.ErrLocked) {
			return ExitUnavailable, err
		}
		return ExitUnavailable, fmt.Errorf("open state: %w", err)
	}
	defer store.Close()

	if _, err := store.Generation(ctx); err != nil {
		if errors.Is(err, state.ErrNotInitialized) {
			return ExitStale, fmt.Errorf("%w: run `zatiti init` first", err)
		}
		return ExitFailure, err
	}
	orgs, err := state.ListOrganizations(ctx, store.DB())
	if err != nil {
		return ExitFailure, err
	}
	for _, o := range orgs {
		fmt.Fprintf(osStdout, "%s\t%s\t%d\n", o.ID, o.Name, o.CreatedAt)
	}
	return ExitOK, nil
}

func (a *App) runInit(ctx context.Context, args []string) (int, error) {
	opts, err := a.cli.ParseInit(args)
	if err != nil {
		return ExitInputError, err
	}

	store, err := state.Open(opts.DataDir)
	if err != nil {
		if errors.Is(err, state.ErrLocked) {
			return ExitUnavailable, err
		}
		return ExitUnavailable, fmt.Errorf("open state: %w", err)
	}
	defer store.Close()

	provisioner, err := identity.NewProvisioner(filepath.Join(opts.DataDir, "keystore"))
	if err != nil {
		return ExitUnavailable, err
	}
	if err := state.Bootstrap(ctx, store, provisioner); err != nil {
		switch {
		case errors.Is(err, state.ErrAlreadyInitialized):
			return ExitStale, err
		default:
			return ExitFailure, err
		}
	}
	fmt.Fprintf(osStdout, "initialized installation at %s\n", opts.DataDir)
	return ExitOK, nil
}

// RunMain: Run with a process-lifetime context. Signal handling
// (SIGINT/SIGTERM → ctx cancel) lives here so serve/mcp shutdown
// ordering runs identically regardless of entry path.
func (a *App) RunMain(args []string) (int, error) {
	ctx, cancel := signalContext()
	defer cancel()
	return a.Run(ctx, args)
}
