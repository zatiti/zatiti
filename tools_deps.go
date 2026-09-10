//go:build tools

// Root dependency anchor for the frozen implementation families in
// docs/implementation/dependencies.lock.json. This file keeps module
// requirements in go.mod while no package source imports them yet; a future
// go mod tidy after real imports land replaces this mechanism. It belongs to
// the integration owner's serialized root-file assignment (ADR 001).
package zatiti

import (
	_ "fyne.io/fyne/v2"
	_ "github.com/modelcontextprotocol/go-sdk/mcp"
	_ "github.com/spf13/cobra"
	_ "modernc.org/sqlite"
)
