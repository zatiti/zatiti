package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/zatiti/zatiti/internal/client"
	"github.com/zatiti/zatiti/internal/contract"
)

// bootstrapOperation is the one operation a client sends without a
// credential; the controller's local socket admits it before any principal
// exists and refuses it afterwards.
const bootstrapOperation = "installation.init"

// defaultBootstrapWait bounds how long `zatiti init` waits for the
// controller to hand the owner credential over after the bootstrap commits.
const defaultBootstrapWait = 30 * time.Second

// socketOperator is the CLI's and MCP adapter's contract.Operator over the
// controller's private socket. The clients are built on first use, after
// flags are parsed; the authenticated client carries the selected profile
// and the bootstrap client carries nothing. After a completed bootstrap it
// waits for the owner profile the controller writes, so `zatiti init`
// returning means the next command can authenticate.
type socketOperator struct {
	cfg           *config
	diagnostics   io.Writer
	bootstrapWait time.Duration

	once      sync.Once
	authed    *client.Client
	bootstrap *client.Client
	err       error
}

func (o *socketOperator) clients() (*client.Client, *client.Client, error) {
	o.once.Do(func() {
		store := profileStore{dir: o.cfg.profilesDir()}
		o.authed, o.err = client.New(client.Config{SocketPath: o.cfg.SocketPath, Timeout: defaultTimeout}, profileCredential{store: store, name: o.cfg.Profile})
		if o.err != nil {
			return
		}
		o.bootstrap, o.err = client.New(client.Config{SocketPath: o.cfg.SocketPath, Timeout: defaultTimeout}, nil)
	})
	return o.authed, o.bootstrap, o.err
}

func (o *socketOperator) Call(ctx context.Context, operation string, req contract.Request) (contract.Result, error) {
	authed, bootstrap, err := o.clients()
	if err != nil {
		return contract.Result{}, err
	}
	if operation != bootstrapOperation {
		return authed.Call(ctx, operation, req)
	}
	res, err := bootstrap.Call(ctx, operation, req)
	if err == nil && res.Status == contract.StatusCompleted {
		o.awaitOwnerProfile(ctx)
	}
	return res, err
}

// awaitOwnerProfile waits, bounded, for the controller's bootstrap
// completion to hand the owner credential over. It reports where the
// credential landed, never what it is; a timeout is a diagnostic, not a
// failure of the committed bootstrap.
func (o *socketOperator) awaitOwnerProfile(ctx context.Context) {
	wait := o.bootstrapWait
	if wait <= 0 {
		wait = defaultBootstrapWait
	}
	store := profileStore{dir: o.cfg.profilesDir()}
	deadline := time.Now().Add(wait)
	for {
		ok, err := store.exists(defaultProfile)
		if err == nil && ok {
			_, _ = fmt.Fprintf(o.diagnostics, "zatiti: owner credential stored in profile %q under %s; select it with --profile %s\n", defaultProfile, o.cfg.profilesDir(), defaultProfile)
			return
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			_, _ = fmt.Fprintf(o.diagnostics, "zatiti: bootstrap completed but the controller has not handed the owner credential to profile %q yet; check the controller log\n", defaultProfile)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// recordingOperator remembers the outcome of the last call so the process
// exit code can be derived after the generated command tree has run: the
// generated leaf reports its result and diagnostics itself and returns an
// unexported error, so the exit mapping is reconstructed from what the
// operator saw, with the same table internal/cli applies.
type recordingOperator struct {
	inner contract.Operator

	mu     sync.Mutex
	called bool
	err    error
}

func (r *recordingOperator) Call(ctx context.Context, operation string, req contract.Request) (contract.Result, error) {
	res, err := r.inner.Call(ctx, operation, req)
	r.mu.Lock()
	r.called = true
	r.err = err
	r.mu.Unlock()
	return res, err
}

// exitCode is the CLI exit code for a generated command that returned an
// error: invalid_input when the request never reached the operator (an
// input the CLI refused, or a command-line error), the fault's mapping for
// a domain failure, controller_unavailable's for a transport failure that
// names it, and 1 for anything else.
func (r *recordingOperator) exitCode() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.called {
		return contract.CLIExit(&contract.Fault{Code: contract.CodeInvalidInput})
	}
	if r.err == nil {
		return 1
	}
	var fault *contract.Fault
	if errors.As(r.err, &fault) {
		return contract.CLIExit(fault)
	}
	if errors.Is(r.err, client.ErrControllerUnavailable) || errors.Is(r.err, client.ErrUnknownOutcome) {
		return contract.CLIExit(&contract.Fault{Code: contract.CodeControllerUnavailable})
	}
	return 1
}

// countingWriter reports whether anything was written through it.
type countingWriter struct {
	io.Writer
	mu    sync.Mutex
	wrote bool
}

func (w *countingWriter) Write(p []byte) (int, error) {
	if len(p) > 0 {
		w.mu.Lock()
		w.wrote = true
		w.mu.Unlock()
	}
	return w.Writer.Write(p)
}

func (w *countingWriter) written() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.wrote
}
