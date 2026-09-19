// Command cigate holds the decisions the workflows in this directory depend
// on, so they can be tested locally instead of living in untestable shell:
// workflow policy validation, the release qualification verdict, bounded test
// evidence, release input resolution, the toolchain and dependency lock
// check, and clean-tree drift detection. It imports the standard library
// only and never publishes, tags, or uploads anything.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
)

// Fault codes follow the repository's shared failure vocabulary.
const (
	codeInvalidInput        = "invalid_input"
	codePrerequisiteMissing = "prerequisite_missing"
	codeVerificationFailed  = "verification_failed"
)

// fault is a named failure. Every command that blocks a gate returns one.
type fault struct {
	Code    string
	Message string
}

func (f *fault) Error() string { return f.Code + ": " + f.Message }

func faultf(code, format string, args ...any) *fault {
	return &fault{Code: code, Message: fmt.Sprintf(format, args...)}
}

// exitCode maps a failure to the repository's CLI exit-code convention.
func exitCode(err error) int {
	var f *fault
	if !errors.As(err, &f) {
		return 1
	}
	switch f.Code {
	case codeInvalidInput:
		return 2
	case codePrerequisiteMissing:
		return 5
	default:
		return 1
	}
}

type command struct {
	summary string
	run     func(ctx context.Context, args []string, stdout, stderr io.Writer) error
}

var commands = map[string]command{
	"lint":          {"validate the workflow files against the policy", runLint},
	"gate":          {"evaluate the release qualification verdict from the needs context", runGate},
	"gotest":        {"run go test and retain a bounded summary and log", runGoTest},
	"inputs":        {"resolve and validate release qualification inputs", runInputs},
	"lock":          {"verify the Go toolchain and dependency lock agree", runLock},
	"drift":         {"require a clean working tree after regeneration", runDrift},
	"require":       {"require a prerequisite package to exist", runRequire},
	"notices":       {"verify the license text is the unmodified Apache License 2.0", runNotices},
	"environ":       {"record the build environment as evidence", runEnviron},
	"provenance":    {"record and verify build provenance of built binaries", runProvenance},
	"flutterpin":    {"resolve the pinned Flutter SDK from the dependency lock report", runFlutterPin},
	"flutterverify": {"verify the installed Flutter SDK matches the pin", runFlutterVerify},
	"flutterlayout": {"report which optional Flutter application parts exist", runFlutterLayout},
	"fluttertest":   {"run flutter test and retain a bounded summary and log", runFlutterTest},
	"bounded":       {"run a command and retain its output up to a size bound", runBounded},
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	cmd, ok := commands[args[0]]
	if !ok {
		say(stderr, "cigate: unknown command %q", args[0])
		usage(stderr)
		return 2
	}
	if err := cmd.run(ctx, args[1:], stdout, stderr); err != nil {
		say(stderr, "cigate %s: %v", args[0], err)
		return exitCode(err)
	}
	return 0
}

func usage(w io.Writer) {
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	say(w, "usage: cigate <command> [flags]")
	for _, name := range names {
		say(w, "  %-13s %s", name, commands[name].summary)
	}
}

// say writes one console line. Console output is advisory: verdicts travel in
// exit codes and evidence files, so a failed console write is ignored.
func say(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format+"\n", args...)
}

// under resolves path relative to root unless it is already absolute.
func under(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}

// newFlags returns a flag set whose parse errors become invalid_input.
func newFlags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return faultf(codeInvalidInput, "%v", err)
	}
	return nil
}

func runLint(_ context.Context, args []string, stdout, stderr io.Writer) error {
	fs := newFlags("lint", stderr)
	dir := fs.String("dir", ".github/workflows", "workflow directory")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	findings, err := lintDir(*dir)
	if err != nil {
		return err
	}
	const maxPrinted = 100
	for i, f := range findings {
		if i == maxPrinted {
			say(stdout, "... %d more findings omitted", len(findings)-maxPrinted)
			break
		}
		say(stdout, "%v", f)
	}
	if len(findings) > 0 {
		return faultf(codeVerificationFailed, "%d workflow policy findings", len(findings))
	}
	names := make([]string, 0, len(registeredWorkflows))
	for name := range registeredWorkflows {
		names = append(names, name)
	}
	sort.Strings(names)
	say(stdout, "workflow policy: %s pass", strings.Join(names, ", "))
	return nil
}
