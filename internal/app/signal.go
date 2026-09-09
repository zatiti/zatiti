// internal/app/signal.go
package app

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// signalContext returns a context canceled on SIGINT or SIGTERM.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
