// cmd/zatiti/main.go
//
// Entry point. Wiring only: construct the app root, run, map the
// result to a process exit code. All behavior lives in the roots;
// this file must never grow logic.

package main

import (
	"fmt"
	"os"

	"zatiti/internal/app"
)

func main() {
	a, err := app.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(app.ExitFailure)
	}
	code, err := a.RunMain(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(code)
}
