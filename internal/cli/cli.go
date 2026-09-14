package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zatiti/zatiti/internal/client"
	"github.com/zatiti/zatiti/internal/contract"
)

// requestSchema is the frozen request envelope schema every CLI-issued
// request declares.
const requestSchema = "zatiti.request/v1"

// cliError carries a process exit code alongside a diagnostic message. By
// the time a RunE returns one, the command's result or diagnostic has
// already been written to IO.Out or IO.Err — the message exists only so
// Execute can recover the exit code cobra's own error return would
// otherwise discard.
type cliError struct {
	code int
	msg  string
}

func (e *cliError) Error() string { return e.msg }

// New builds the root "zatiti" command from the given operation catalog:
// one Cobra command per descriptor, at its declared CLI path, calling op
// with that descriptor's operation ID. Intermediate path segments become
// plain grouping commands (for example "capabilities" groups
// "capabilities schema" while still itself running capabilities.list).
// Two descriptors mapping to the same CLI path is a contract defect and is
// reported as an error rather than silently resolved by overwrite order.
//
// Transport mechanics — serve, mcp serve, help, completion — are not
// product operations and are never registered here; the caller adds them
// to the returned command after New succeeds.
func New(op contract.Operator, descriptors []contract.Descriptor, streams IO) (*cobra.Command, error) {
	root := &cobra.Command{
		Use:           "zatiti",
		Short:         "Zatiti agent controller",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetIn(streams.In)
	root.SetOut(streams.Out)
	root.SetErr(streams.Err)

	jsonMode := new(bool)
	root.PersistentFlags().BoolVar(jsonMode, "json", false, "emit exactly one JSON result envelope to stdout")

	registered := make(map[string]string, len(descriptors)) // CLI path -> operation ID
	for _, d := range descriptors {
		if d.ID == "" {
			return nil, errors.New("cli: descriptor has no operation ID")
		}
		if len(d.CLI) == 0 {
			return nil, fmt.Errorf("cli: descriptor %s declares no CLI mapping", d.ID)
		}
		path := strings.Join(d.CLI, " ")
		if existing, ok := registered[path]; ok {
			return nil, fmt.Errorf("cli: command %q maps to both %s and %s", path, existing, d.ID)
		}
		registered[path] = d.ID

		node := resolveNode(root, d.CLI)
		if node.RunE != nil {
			return nil, fmt.Errorf("cli: command %q already registered", path)
		}
		configureLeaf(node, d, op, streams, jsonMode)
	}
	return root, nil
}

// Execute builds the command tree with New and runs args against it under
// ctx, returning the process exit code: 0 for a completed or accepted
// command, contract.CLIExit of a domain fault for a failed one, or a
// client-side exit code for a request the CLI itself could not send.
func Execute(ctx context.Context, args []string, op contract.Operator, descriptors []contract.Descriptor, streams IO) int {
	root, err := New(op, descriptors, streams)
	if err != nil {
		_, _ = fmt.Fprintf(streams.Err, "cli: %v\n", err)
		return 1
	}
	root.SetArgs(args)

	if err := root.ExecuteContext(ctx); err != nil {
		var ce *cliError
		if errors.As(err, &ce) {
			return ce.code
		}
		// A cobra-level failure (unknown command, unknown or malformed
		// flag) never reached a RunE, so nothing has been printed yet.
		_, _ = fmt.Fprintf(streams.Err, "cli: %v\n", err)
		return contract.CLIExit(&contract.Fault{Code: contract.CodeInvalidInput})
	}
	return 0
}

// resolveNode finds or creates the command at path under root, creating a
// plain grouping command for any missing intermediate segment.
func resolveNode(root *cobra.Command, path []string) *cobra.Command {
	cur := root
	for _, name := range path {
		var next *cobra.Command
		for _, c := range cur.Commands() {
			if c.Name() == name {
				next = c
				break
			}
		}
		if next == nil {
			next = &cobra.Command{Use: name, SilenceUsage: true, SilenceErrors: true}
			cur.AddCommand(next)
		}
		cur = next
	}
	return cur
}

// configureLeaf attaches the operation's flags and RunE to its resolved
// command node.
func configureLeaf(cmd *cobra.Command, d contract.Descriptor, op contract.Operator, streams IO, jsonMode *bool) {
	cmd.Short = fmt.Sprintf("%s (%s/%s)", d.ID, d.Mode, d.Effect)
	cmd.Args = cobra.NoArgs

	var input string
	cmd.Flags().StringVar(&input, "input", "", "structured input: @file, - for stdin, or inline JSON (default {})")

	var submissionKey string
	if d.SubmissionKey {
		cmd.Flags().StringVar(&submissionKey, "submission-key", "", "caller-generated idempotency key for this mutation (required)")
	}

	descriptor := d
	cmd.RunE = func(c *cobra.Command, _ []string) error {
		return runOperation(c.Context(), op, streams, descriptor, input, submissionKey, *jsonMode)
	}
}

// runOperation resolves --input, calls the operator and renders the
// result. It never blocks on a TTY: stdin is read only when --input is
// exactly "-".
func runOperation(ctx context.Context, op contract.Operator, streams IO, d contract.Descriptor, rawInput, submissionKey string, jsonMode bool) error {
	input, err := resolveInput(streams.In, rawInput)
	if err != nil {
		_, _ = fmt.Fprintf(streams.Err, "cli: %s: %v\n", d.ID, err)
		return &cliError{code: contract.CLIExit(&contract.Fault{Code: contract.CodeInvalidInput}), msg: err.Error()}
	}

	req := contract.Request{
		Schema:        requestSchema,
		SubmissionKey: submissionKey,
		Input:         input,
	}

	result, callErr := op.Call(ctx, d.ID, req)
	if callErr != nil {
		var fault *contract.Fault
		if !errors.As(callErr, &fault) {
			// A transport-level failure: no trustworthy envelope exists,
			// so nothing goes to stdout — only a diagnostic on stderr.
			_, _ = fmt.Fprintf(streams.Err, "cli: %s: %v\n", d.ID, callErr)
			return &cliError{code: exitForTransportError(callErr), msg: callErr.Error()}
		}
		// A domain failure: result already carries the full, authoritative
		// envelope (contract.Result.Error == callErr); render it normally.
	}
	return finish(streams, jsonMode, result)
}

// exitForTransportError maps a non-domain operator error to the closest
// named exit code. client's sentinel errors identify controller
// unavailability and an unresolved unknown outcome precisely; anything
// else is an unclassified failure.
func exitForTransportError(err error) int {
	if errors.Is(err, client.ErrControllerUnavailable) || errors.Is(err, client.ErrUnknownOutcome) {
		return contract.CLIExit(&contract.Fault{Code: contract.CodeControllerUnavailable})
	}
	return 1
}
