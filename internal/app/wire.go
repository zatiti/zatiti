// internal/app/wire.go
//
// Wiring: the identity provisioner satisfies state.PrincipalProvisioner
// structurally; this file pins that contract at compile time and provides
// the entry point constructor.

package app

import (
	"github.com/zatiti/zatiti/internal/identity"
	"github.com/zatiti/zatiti/internal/state"
)

// Compile-time port assertion: identity's provisioner satisfies the
// state-owned interface without identity importing state.
var _ state.PrincipalProvisioner = (*identity.Provisioner)(nil)

// app is what cmd/zatiti/main.go delegates to. Errors here are
// construction failures (report as generic failure, exit 1).
func app() *App {
	a, err := New()
	if err != nil {
		// New currently cannot fail; keep the call site honest for when
		// wiring gains fallible constructors (T1.8 controller lifecycle).
		panic("app: construction failure: " + err.Error())
	}
	return a
}
