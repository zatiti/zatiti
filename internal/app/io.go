// internal/app/io.go
package app

import (
	"io"
	"os"
)

var (
	osStdout io.Writer = os.Stdout
	osStderr io.Writer = os.Stderr
)

var osStdin io.Reader = os.Stdin

// setIO redirects the package-level sinks for the duration of a test.
// Not exported: only tests in this package may swap sinks.
func setIO(t interface{ Cleanup(func()) }, out, errOut io.Writer) {
	oldOut, oldErr := osStdout, osStderr
	osStdout, osStderr = out, errOut
	t.Cleanup(func() { osStdout, osStderr = oldOut, oldErr })
}
