package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// main delegates to run for testability. SIGINT and SIGTERM cancel the root
// context: serve stops admission and shuts down within its grace period, a
// CLI call stops waiting locally (the accepted command is not cancelled),
// and the MCP session closes.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
