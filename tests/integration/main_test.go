package integration_test

import (
	"io"
	"log/slog"
	"os"
	"testing"
)

// Every case assembles its own installation on its own temp directories and
// socket, so the cases run in parallel; nothing is shared between them but
// the process-wide logger set here.
//
// TestMain silences the landed packages' structured request logging so a
// failing case's own evidence is what the test output retains.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}
