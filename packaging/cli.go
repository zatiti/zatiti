package packaging

// This file and its cli_*.go siblings are the packaging/install driver named
// in this root's AGENTS.md brief: "a documented executable packaging/install
// driver" built on the manifest, signature and install library the rest of
// this package provides. RunCLI is the whole implementation; the executable
// at packaging/cmd/zatiti-pack is a five-line main package that calls it, so
// this package's own tests exercise exactly the code the built binary runs.
//
// The driver adds no capability the library above does not already have: it
// parses flags, loads the caller's inputs from disk, and calls the same
// exported functions the package's own tests call. It performs real
// filesystem changes and, when a service manager is selected, real
// subprocess actions (through ServiceManager, exactly as service.go and
// servicemanager.go already do) - it is not a simulation, and "--apply" is
// required before anything on disk changes.
//
// What the driver does not do: it does not sign or notarize with a real
// release identity (see "sign" and "keygen" in cli_sign.go), it does not
// publish or upload anything, and it invents no working install command
// ahead of qualification. See QUALIFICATION.md for what remains unproven
// until it runs against a real host.
import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

// cliUsage documents every driver subcommand. RunCLI prints it for "help",
// for a missing command, and (with the offending name) for an unknown one.
const cliUsage = `zatiti-pack: the packaging/install driver

Usage: zatiti-pack <command> [flags]

Release assembly:
  assemble         measure a staged tree into a signed-ready manifest.json
  assemble-bundle   archive a built Flutter bundle deterministically (desktop)
  keygen            generate a local Ed25519 key pair for development signing

Signature:
  sign              sign a manifest with an Ed25519 key
  verify            verify a manifest signature, and optionally its tree

Installation lifecycle (controller or desktop, "--apply" to act, else a dry run):
  install           plan (and, with --apply, install or upgrade) a release
  uninstall         plan (and, with --apply, uninstall) a release
  service           load, unload or restart one launcher directly
  audit             check an installed release against its manifest
  inspect           list the releases installed in a layout

Secure helper:
  master-key provision   provision a headless master key file
  master-key check       check an existing master key file's permissions

Run "zatiti-pack <command> -h" for a command's own flags. Every command
prints one JSON document to stdout on success, or one JSON error document
("error": {"code", "message", "findings"}) to stderr on failure.

No command here signs with a real release identity, notarizes, publishes or
deploys. See packaging/QUALIFICATION.md for what a real host still has to
prove.
`

// RunCLI is the packaging/install driver's entry point. args excludes the
// program name (os.Args[1:]). It returns the process exit code: 0 on
// success, and otherwise the same failure-family mapping the frozen CLI
// convention (docs/implementation-remediation/assignments' embedded
// contract, "Wire conventions and limits") uses for the coded faults this
// package declares in errors.go - invalid_input=2, conflict=4,
// prerequisite_missing/capability_unsupported=5, everything else=1. This
// package's fault vocabulary is a strict subset of the frozen contract's
// (it has no stale_version, submission_conflict, review_required,
// outcome_unknown, budget_unavailable, external_action_required,
// controller_unavailable or artifact_fault: none of those apply to a local,
// non-transactional install driver), so the mapping below is modeled on that
// convention for a consistent operator experience across Zatiti's tools, not
// governed by it.
func RunCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, cliUsage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		_, _ = fmt.Fprint(stdout, cliUsage)
		return 0
	}
	run, ok := cliCommands[cmd]
	if !ok {
		_, _ = fmt.Fprintf(stderr, "zatiti-pack: unknown command %q\n\n%s", cmd, cliUsage)
		return 2
	}
	if err := run(rest, stdout); err != nil {
		writeCLIError(stderr, err)
		return cliExitCode(err)
	}
	return 0
}

var cliCommands = map[string]func([]string, io.Writer) error{
	"assemble":        cliAssemble,
	"assemble-bundle": cliAssembleBundle,
	"keygen":          cliKeygen,
	"sign":            cliSign,
	"verify":          cliVerify,
	"install":         cliInstall,
	"uninstall":       cliUninstall,
	"service":         cliService,
	"audit":           cliAudit,
	"inspect":         cliInspect,
	"master-key":      cliMasterKey,
}

// cliExitCode maps a driver error to a process exit code. A nil error is 0.
func cliExitCode(err error) int {
	if err == nil {
		return 0
	}
	switch Code(err) {
	case CodeInvalidInput:
		return 2
	case CodeConflict:
		return 4
	case CodePrerequisiteMissing, CodeCapabilityUnsupported:
		return 5
	default: // not_found, verification_failed, internal_error, or an
		// error this package did not produce.
		return 1
	}
}

// writeCLIError writes one JSON error document: {"error": {"code",
// "message", "findings"}}. Findings is present only for a verification
// failure that named specific paths, exactly as *Error.Findings does.
func writeCLIError(w io.Writer, err error) {
	doc := struct {
		Error struct {
			Code     string    `json:"code"`
			Message  string    `json:"message"`
			Findings []Finding `json:"findings,omitempty"`
		} `json:"error"`
	}{}
	doc.Error.Code = Code(err)
	doc.Error.Message = err.Error()
	var perr *Error
	if errors.As(err, &perr) {
		doc.Error.Findings = perr.Findings
	}
	if jsonErr := writeJSONDoc(w, doc); jsonErr != nil {
		_, _ = fmt.Fprintln(w, err.Error())
	}
}

// newFlagSet returns a FlagSet that reports its own errors through the
// driver's normal error path instead of printing to the real stderr or
// calling os.Exit, so a bad flag is just another coded failure.
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return fs
}

func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return errWrap(CodeInvalidInput, fs.Name()+": "+err.Error(), err)
	}
	return nil
}

// stringListFlag collects a repeatable string flag, such as --trusted or
// --env.
type stringListFlag []string

func (s *stringListFlag) String() string { return strings.Join(*s, ",") }
func (s *stringListFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}
