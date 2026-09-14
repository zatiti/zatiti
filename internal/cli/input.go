package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/zatiti/zatiti/internal/contract"
)

// maxInputBytes bounds every --input read at the frozen wire default for a
// JSON request ("Default limits: JSON request 1 MiB"). It is a client-side
// ceiling only; the controller enforces its own limit independently.
const maxInputBytes = 1 << 20

// errInputTooLarge reports a client-side --input size refusal, distinct
// from a malformed-JSON refusal, so callers can tell the two apart without
// string matching.
var errInputTooLarge = errors.New("input exceeds the maximum request size")

// defaultInput is sent when --input is omitted: an empty JSON object. The
// operator, never the CLI, reports invalid_input for a missing required
// field, so an omitted flag never blocks or silently prompts.
var defaultInput = json.RawMessage(`{}`)

// resolveInput turns one --input flag value into a validated JSON object
// for contract.Request.Input:
//
//   - "" (flag omitted): defaultInput,
//   - "-": stdin, read once and in full,
//   - "@path": the named local file, opened by the CLI client itself —
//     never a path the controller opens,
//   - anything else: the literal flag value, parsed as inline JSON.
//
// stdin is read only for the explicit "-" form, so an omitted or inline
// --input never blocks on a TTY. The returned error never repeats the
// offending bytes; it names what was wrong, not what was sent.
func resolveInput(stdin io.Reader, raw string) (json.RawMessage, error) {
	switch {
	case raw == "":
		return defaultInput, nil
	case raw == "-":
		data, err := readBounded(stdin)
		if err != nil {
			return nil, fmt.Errorf("reading --input from stdin: %w", err)
		}
		return validateInputObject(data)
	case strings.HasPrefix(raw, "@"):
		path := strings.TrimPrefix(raw, "@")
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("opening --input file %q: %w", path, err)
		}
		defer func() { _ = f.Close() }()
		data, err := readBounded(f)
		if err != nil {
			return nil, fmt.Errorf("reading --input file %q: %w", path, err)
		}
		return validateInputObject(data)
	default:
		return validateInputObject([]byte(raw))
	}
}

// readBounded reads r fully, refusing anything past maxInputBytes without
// ever holding more than one byte over the limit in memory.
func readBounded(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxInputBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxInputBytes {
		return nil, errInputTooLarge
	}
	return data, nil
}

// validateInputObject applies the same wire strictness the controller uses
// (contract.DecodeStrict: duplicate keys, malformed UTF-8, trailing data
// and out-of-range integers are all rejected) and requires a JSON object,
// matching every operation's input schema. On failure it reports only that
// the value was rejected, never the value itself.
func validateInputObject(data []byte) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return defaultInput, nil
	}
	var probe any
	if err := contract.DecodeStrict(trimmed, &probe); err != nil {
		return nil, errors.New("--input is not valid structured JSON")
	}
	if _, ok := probe.(map[string]any); !ok {
		return nil, errors.New("--input must be a JSON object")
	}
	return json.RawMessage(trimmed), nil
}
