// internal/app/serve.go
//
// `zatiti serve`: run the controller. Signal handling is owned by
// main's NotifyContext; serve installs no additional handlers.

package app

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/zatiti/zatiti/internal/controller"
	"github.com/zatiti/zatiti/internal/state"
	"github.com/zatiti/zatiti/internal/transport"
)

func (a *App) runServe(ctx context.Context, args []string) (int, error) {
	opts, err := a.cli.ParseServe(args)
	if err != nil {
		return ExitInputError, err
	}

	logger := log.New(osStderr, "", log.LstdFlags|log.LUTC)
	ctrl, err := controller.Start(ctx, opts.DataDir, logger)
	if err != nil {
		if errors.Is(err, state.ErrLocked) {
			return ExitUnavailable, fmt.Errorf(
				"another controller owns this installation")
		}
		if errors.Is(err, state.ErrNotInitialized) {
			return ExitStale, fmt.Errorf("%w: run `zatiti init` first", err)
		}
		return ExitFailure, err
	}

	logger.Printf("serving MCP on stdio at generation %d", ctrl.Generation())
	srv := transport.NewMCPServer(a.registry, transport.Stdin(), transport.Stdout())
	err = srv.Serve(ctx)
	runErr := ctrl.Run(ctx) // always release ownership
	switch {
	case err != nil && ctx.Err() != nil:
		return ExitOK, nil // signal shutdown is a clean exit
	case err != nil:
		return ExitUnavailable, fmt.Errorf("serve: %w", err)
	case runErr != nil:
		return ExitFailure, runErr
	}
	return ExitOK, nil
}
