// internal/app/version.go
package app

import (
	"context"
	"fmt"

	"github.com/zatiti/zatiti/internal/version"
)

// runVersion: no data-dir, no store, no lock. Version is a fact about
// the binary, not the installation — deliberately openable while
// another controller owns the lock.
func (a *App) runVersion(ctx context.Context, args []string) (int, error) {
	line, err := version.Attested()
	if err != nil {
		return ExitFailure, err // unstamped build: exit 1, per header contract
	}
	fmt.Fprintln(osStdout, line)
	return ExitOK, nil
}
