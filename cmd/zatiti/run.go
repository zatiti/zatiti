package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/spf13/cobra"

	"github.com/zatiti/zatiti/internal/cli"
	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/platform"
)

// exitError carries the process exit code of a transport command. Its
// diagnostic has already been written by the time it is returned.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

// runOptions are the seams tests narrow; production uses the zero value.
type runOptions struct {
	env           lookupEnv
	catalog       func() ([]contract.Descriptor, error)
	operator      contract.Operator
	serve         serveOptions
	bootstrapWait time.Duration
}

// run is the testable entrypoint: it builds the generated product command
// tree over the controller client, adds the transport mechanics (serve, mcp
// serve; cobra supplies help and completion) and executes args, returning
// the process exit code. Nothing but the CLI envelope (or MCP frames) ever
// reaches stdout; diagnostics and logs go to stderr.
func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return runWith(ctx, args, stdin, stdout, stderr, runOptions{})
}

func runWith(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, opts runOptions) int {
	if opts.env == nil {
		opts.env = osEnv
	}
	if opts.catalog == nil {
		opts.catalog = catalog
	}
	log := newLogger(stderr, slog.LevelInfo)
	slog.SetDefault(log)

	cfg, err := defaultConfig(opts.env)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "zatiti: %v\n", err)
		return 1
	}
	descriptors, err := opts.catalog()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "zatiti: operation catalog: %v\n", err)
		return 1
	}

	out := &countingWriter{Writer: stdout}
	diag := &countingWriter{Writer: stderr}
	streams := cli.IO{In: stdin, Out: out, Err: diag}
	var op contract.Operator = &socketOperator{cfg: &cfg, diagnostics: diag, bootstrapWait: opts.bootstrapWait}
	if opts.operator != nil {
		op = opts.operator
	}
	rec := &recordingOperator{inner: op}

	root, err := cli.New(rec, descriptors, streams)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "zatiti: %v\n", err)
		return 1
	}
	root.Long = "Zatiti is a single-tenant, local-first AI agent controller. Product commands are generated from the operation catalog; serve and mcp serve are transport mechanics."
	pf := root.PersistentFlags()
	pf.StringVar(&cfg.StateDir, "state-dir", cfg.StateDir, "installation state directory ("+envStateDir+")")
	pf.StringVar(&cfg.SocketPath, "socket", cfg.SocketPath, "controller private Unix socket (default <state-dir>/"+socketFileName+"; "+envSocket+")")
	pf.StringVar(&cfg.Profile, "profile", cfg.Profile, "client credential profile under <state-dir>/"+profilesDirName+" ("+envProfile+")")
	root.PersistentPreRunE = func(*cobra.Command, []string) error {
		if err := cfg.finalize(); err != nil {
			_, _ = fmt.Fprintf(diag, "zatiti: %v\n", err)
			return &exitError{code: contract.CLIExit(&contract.Fault{Code: contract.CodeInvalidInput}), err: err}
		}
		return nil
	}
	annotateFirstTask(root)
	root.AddCommand(serveCommand(&cfg, log, diag, opts.serve))
	root.AddCommand(mcpCommand(&cfg, descriptors, op, streams))
	attachConnectionHelper(root, &cfg, op, streams)
	root.SetArgs(args)

	err = root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	var exit *exitError
	if errors.As(err, &exit) {
		return exit.code
	}
	if !out.written() && !diag.written() {
		// A cobra-level failure (unknown command or flag) reached no RunE
		// and nothing has been reported yet.
		_, _ = fmt.Fprintf(stderr, "zatiti: %v\n", err)
		return contract.CLIExit(&contract.Fault{Code: contract.CodeInvalidInput})
	}
	return rec.exitCode()
}

// serveCommand is `zatiti serve`.
func serveCommand(cfg *config, log *slog.Logger, diag io.Writer, opts serveOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "serve",
		Short:         "run the controller: installation lock, migrations, scheduler and the private socket",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, _ []string) error {
			if err := runServe(c.Context(), *cfg, log, opts); err != nil {
				_, _ = fmt.Fprintf(diag, "zatiti serve: %v\n", err)
				return &exitError{code: exitFor(err), err: err}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&cfg.CredentialBackend, "credential-backend", cfg.CredentialBackend, "secret custody: keychain or headless ("+envCredentialBackend+")")
	f.StringVar(&cfg.MasterKeyRef, "master-key", cfg.MasterKeyRef, "master key reference, file:<path> or secret:<key> ("+envMasterKeyRef+")")
	f.StringVar(&cfg.ControllerPrincipal, "controller-principal", cfg.ControllerPrincipal, "explicitly provisioned service principal the controller runs as ("+envControllerPrincipal+")")
	f.DurationVar(&cfg.TickInterval, "tick-interval", 0, "scheduler tick (default 1s)")
	f.StringVar(&cfg.RemoteAddress, "remote-address", "", "optional mutual-TLS listener address for an explicitly configured remote desktop")
	f.StringVar(&cfg.TLSCertFile, "tls-cert", "", "remote listener certificate (PEM)")
	f.StringVar(&cfg.TLSKeyFile, "tls-key", "", "remote listener private key (PEM)")
	f.StringVar(&cfg.TLSClientCAFile, "tls-client-ca", "", "CA bundle remote clients must chain to (PEM)")
	return cmd
}

// mcpCommand is the `zatiti mcp` group with its one subcommand, serve.
func mcpCommand(cfg *config, descriptors []contract.Descriptor, op contract.Operator, streams cli.IO) *cobra.Command {
	group := &cobra.Command{
		Use:           "mcp",
		Short:         "Model Context Protocol transport",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	var bootstrap bool
	serve := &cobra.Command{
		Use:           "serve",
		Short:         "serve the stdio MCP adapter over the controller socket as the selected profile",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, _ []string) error {
			if err := runMCP(c.Context(), *cfg, streams.In, streams.Out, streams.Err, descriptors, op, bootstrap); err != nil && !errors.Is(err, context.Canceled) {
				_, _ = fmt.Fprintf(streams.Err, "zatiti mcp serve: %v\n", err)
				return &exitError{code: exitFor(err), err: err}
			}
			return nil
		},
	}
	serve.Flags().BoolVar(&bootstrap, "bootstrap", false, "one-time local initializer session: offers only installation.init and exits after it completes")
	group.AddCommand(serve)
	return group
}

// exitFor maps a transport command's failure to the CLI exit table: named
// faults (including platform and storage faults) by their code, anything
// else 1.
func exitFor(err error) int {
	var fault *contract.Fault
	if errors.As(err, &fault) {
		return contract.CLIExit(fault)
	}
	if code := platform.Code(err); code != "" {
		return contract.CLIExit(&contract.Fault{Code: code})
	}
	return 1
}
