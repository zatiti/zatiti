// Command zatiti-pack is the packaging/install driver: a thin executable
// wrapper around packaging.RunCLI, which holds the whole implementation so
// that package's own tests exercise exactly the code this binary runs. See
// packaging/cli.go for the driver's documentation and packaging/README.md
// for how it fits the release manifest, signature and install library.
//
// This binary signs, installs, upgrades and uninstalls nothing on its own
// authority: every action needs an explicit flag, "install" and "uninstall"
// change nothing on disk without --apply, and no command here signs with a
// real release identity, notarizes, publishes or deploys.
package main

import (
	"os"

	"github.com/zatiti/zatiti/packaging"
)

func main() {
	os.Exit(packaging.RunCLI(os.Args[1:], os.Stdout, os.Stderr))
}
