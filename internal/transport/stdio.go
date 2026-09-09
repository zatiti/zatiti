// internal/transport/stdio.go
package transport

import (
	"io"
	"os"
)

// Stdin / Stdout return the process stdio streams for MCP serving.
// Indirection exists so tests can prove serve paths write nowhere
// except the protocol stream.
func Stdin() io.Reader  { return os.Stdin }
func Stdout() io.Writer { return os.Stdout }
