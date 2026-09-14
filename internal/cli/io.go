package cli

import "io"

// IO carries the CLI's process-facing streams. In is read only for the
// explicit "--input -" form; Out receives exactly one thing per invocation
// (the JSON envelope or its human rendering); Err receives diagnostics —
// anything that is not the command's own result.
type IO struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}
