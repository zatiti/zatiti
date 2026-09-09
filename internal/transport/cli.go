// internal/transport/cli.go
//
// CLI transport adapter: argv parsing, usage text, stdout/stderr
// discipline (diagnostics → stderr, results → stdout). Holds no
// domain logic.

package transport

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"zatiti/internal/operations"
)

// CLI is the human transport adapter.
type CLI struct{}

// NewCLI constructs the adapter.
func NewCLI() *CLI { return &CLI{} }

// InitOptions are the parsed inputs of `zatiti init`.
type InitOptions struct {
	DataDir    string
	OwnerLabel string
}

// ParseInit parses init flags. Unknown flags are input errors.
func (c *CLI) ParseInit(args []string) (InitOptions, error) {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := InitOptions{}
	fs.StringVar(&opts.DataDir, "data-dir", "", "installation data directory (required)")
	fs.StringVar(&opts.OwnerLabel, "owner-label", "root", "label recorded for the owner principal")
	if err := fs.Parse(args); err != nil {
		return opts, fmt.Errorf("transport: %v", err)
	}
	if opts.DataDir == "" {
		return opts, fmt.Errorf("transport: -data-dir is required")
	}
	return opts, nil
}

// PrintUsage writes top-level usage. Registered commands only —
// unimplemented tasks are not advertised.
func (c *CLI) PrintUsage(w io.Writer) {
	fmt.Fprintf(w, "zatiti — enterprise-class AI operations controller\n\n")
	fmt.Fprintf(w, "Commands:\n")
	fmt.Fprintf(w, "  init     initialize an installation\n")
	fmt.Fprintf(w, "  serve    run the controller\n")
	fmt.Fprintf(w, "  mcp serve run the MCP stdio adapter\n")
	fmt.Fprintf(w, "  version  print version\n\n")
	fmt.Fprintf(w, "Use <command> -h for details.\n")
	fmt.Fprintln(w, strings.Repeat("-", 0)) // keep trailing newline stable
}

// ParseDataDirFlag parses the common "-data-dir" flag shared by
// generated operation commands.
func (c *CLI) ParseDataDirFlag(args []string, cmd string) (InitOptions, error) {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := InitOptions{}
	fs.StringVar(&opts.DataDir, "data-dir", "", "installation data directory (required)")
	if err := fs.Parse(args); err != nil {
		return opts, fmt.Errorf("transport: %v", err)
	}
	if opts.DataDir == "" {
		return opts, fmt.Errorf("transport: -data-dir is required")
	}
	return opts, nil
}

// ParseServe parses serve flags.
func (c *CLI) ParseServe(args []string) (InitOptions, error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := InitOptions{}
	fs.StringVar(&opts.DataDir, "data-dir", "", "installation data directory (required)")
	if err := fs.Parse(args); err != nil {
		return opts, fmt.Errorf("transport: %v", err)
	}
	if opts.DataDir == "" {
		return opts, fmt.Errorf("transport: -data-dir is required")
	}
	return opts, nil
}

// ExtractDataDir pulls "-data-dir VALUE" from args, returning the rest.
// Generated commands take -data-dir plus typed positional/flag fields;
// the flag form is used here for uniformity with init/serve.
func (c *CLI) ExtractDataDir(args []string, cmd string) (string, []string, error) {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dataDir := ""
	fs.StringVar(&dataDir, "data-dir", "", "installation data directory (required)")
	if err := fs.Parse(args); err != nil {
		return "", nil, fmt.Errorf("transport: %v", err)
	}
	if dataDir == "" {
		return "", nil, fmt.Errorf("transport: -data-dir is required")
	}
	return dataDir, fs.Args(), nil
}

// ParseOpFields parses the operation's declared fields as flags,
// enforcing required fields. Unknown flags are input errors.
func (c *CLI) ParseOpFields(op *operations.Op, args []string) (map[string]any, error) {
	fs := flag.NewFlagSet(op.Name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	inputs := map[string]any{}
	raws := map[string]*string{}
	bools := map[string]*bool{}
	uints := map[string]*uint64{}
	for _, f := range op.Fields {
		switch f.Type {
		case operations.TBool:
			b := new(bool)
			bools[f.Name] = b
			fs.BoolVar(b, f.Name, false, f.Help)
		case operations.TUint:
			u := new(uint64)
			uints[f.Name] = u
			fs.Uint64Var(u, f.Name, 0, f.Help)
		default: // TString
			s := new(string)
			raws[f.Name] = s
			fs.StringVar(s, f.Name, "", f.Help)
		}
	}
	if err := fs.Parse(args); err != nil {
		return nil, fmt.Errorf("transport: %v", err)
	}
	for _, f := range op.Fields {
		var v any
		switch f.Type {
		case operations.TBool:
			v = *bools[f.Name]
		case operations.TUint:
			v = *uints[f.Name]
		default:
			v = *raws[f.Name]
		}
		if f.Required {
			if s, ok := v.(string); ok && s == "" {
				return nil, fmt.Errorf("transport: -%s is required", f.Name)
			}
		}
		inputs[f.Name] = v
	}
	return inputs, nil
}
