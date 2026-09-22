package packaging

import (
	"io"
	"path/filepath"
)

// cliMasterKey dispatches the master-key subcommands: provision and check.
func cliMasterKey(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errf(CodeInvalidInput, "master-key: expected a subcommand, provision or check")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "provision":
		return cliMasterKeyProvision(rest, stdout)
	case "check":
		return cliMasterKeyCheck(rest, stdout)
	default:
		return errf(CodeInvalidInput, "master-key: unknown subcommand %q", verb)
	}
}

// cliMasterKeyProvision is the installation-time secure helper for a
// headless host: it is ProvisionMasterKey behind a flag parser. It never
// prints the key; the reference is the only output.
func cliMasterKeyProvision(args []string, stdout io.Writer) error {
	fs := newFlagSet("master-key provision")
	var lf layoutFlags
	lf.register(fs)
	keyPath := fs.String("key-path", "", "the master key file path, outside the state and distribution directories (required)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *keyPath == "" {
		return errf(CodeInvalidInput, "master-key provision: --key-path is required")
	}
	layout, err := lf.resolve()
	if err != nil {
		return err
	}
	absKey, err := filepath.Abs(*keyPath)
	if err != nil {
		return errWrap(CodeInvalidInput, "the key path could not be resolved", err)
	}
	ref, err := ProvisionMasterKey(layout, absKey)
	if err != nil {
		return err
	}
	return writeJSONDoc(stdout, map[string]any{"reference": ref})
}

// cliMasterKeyCheck reports whether an existing master key file is safe to
// reference, without reading the key: it is CheckMasterKey behind a flag
// parser.
func cliMasterKeyCheck(args []string, stdout io.Writer) error {
	fs := newFlagSet("master-key check")
	keyPath := fs.String("key-path", "", "required")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *keyPath == "" {
		return errf(CodeInvalidInput, "master-key check: --key-path is required")
	}
	absKey, err := filepath.Abs(*keyPath)
	if err != nil {
		return errWrap(CodeInvalidInput, "the key path could not be resolved", err)
	}
	if err := CheckMasterKey(absKey); err != nil {
		return err
	}
	return writeJSONDoc(stdout, map[string]any{"ok": true})
}
