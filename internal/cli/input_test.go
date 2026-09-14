package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// TestInputStdinFileInlineEquivalence proves the "stdin/file/inline
// equivalence" local proving focus item: the same JSON content reaches the
// operator identically regardless of which of the three --input forms
// supplied it.
func TestInputStdinFileInlineEquivalence(t *testing.T) {
	descriptors := []contract.Descriptor{
		descriptor("organization.create", []string{"organization", "create"}, contract.ModeMutation, true),
	}
	const payload = `{"key":"demo","name":"Demo organization"}`

	inlineFile := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(inlineFile, []byte(payload), 0o600); err != nil {
		t.Fatalf("writing fixture file: %v", err)
	}

	cases := []struct {
		name string
		args []string
		in   string
	}{
		{"inline", []string{"organization", "create", "--submission-key", "k1", "--input", payload}, ""},
		{"file", []string{"organization", "create", "--submission-key", "k1", "--input", "@" + inlineFile}, ""},
		{"stdin", []string{"organization", "create", "--submission-key", "k1", "--input", "-"}, payload},
	}

	var inputs []json.RawMessage
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			op := &fakeOperator{}
			var stdin *strings.Reader
			if tc.in != "" {
				stdin = strings.NewReader(tc.in)
			} else {
				stdin = strings.NewReader("")
			}
			_, stderr, code := run(t, tc.args, op, descriptors, stdin)
			if code != 0 {
				t.Fatalf("exit code = %d, stderr = %q", code, stderr)
			}
			if len(op.calls) != 1 {
				t.Fatalf("operator calls = %d, want 1", len(op.calls))
			}
			inputs = append(inputs, op.calls[0].request.Input)
		})
	}

	for i := 1; i < len(inputs); i++ {
		var a, b any
		if err := json.Unmarshal(inputs[0], &a); err != nil {
			t.Fatalf("unmarshal baseline: %v", err)
		}
		if err := json.Unmarshal(inputs[i], &b); err != nil {
			t.Fatalf("unmarshal case %d: %v", i, err)
		}
		aj, _ := json.Marshal(a)
		bj, _ := json.Marshal(b)
		if string(aj) != string(bj) {
			t.Fatalf("input form %d diverged: %s != %s", i, aj, bj)
		}
	}
}

// TestNoTTYDefaultInput proves the "no TTY" local proving focus item: with
// --input omitted, the CLI never reads stdin at all (a poison reader would
// panic if it were read) and sends the default empty object instead of
// blocking or prompting.
func TestNoTTYDefaultInput(t *testing.T) {
	descriptors := []contract.Descriptor{
		descriptor("capabilities.list", []string{"capabilities"}, contract.ModeQuery, false),
	}
	op := &fakeOperator{}
	_, stderr, code := run(t, []string{"capabilities"}, op, descriptors, poisonReader{})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr)
	}
	if len(op.calls) != 1 {
		t.Fatalf("operator calls = %d, want 1", len(op.calls))
	}
	if string(op.calls[0].request.Input) != "{}" {
		t.Fatalf("default input = %s, want {}", op.calls[0].request.Input)
	}
}

// TestInputSizeLimit proves --input refuses a payload over the frozen 1
// MiB request default locally, without ever calling the operator.
func TestInputSizeLimit(t *testing.T) {
	descriptors := []contract.Descriptor{
		descriptor("organization.create", []string{"organization", "create"}, contract.ModeMutation, true),
	}
	oversized := `{"pad":"` + strings.Repeat("a", 1<<20) + `"}`
	op := &fakeOperator{}
	_, stderr, code := run(t, []string{"organization", "create", "--submission-key", "k1", "--input", "-"}, op, descriptors, strings.NewReader(oversized))
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (invalid_input); stderr = %q", code, stderr)
	}
	if len(op.calls) != 0 {
		t.Fatal("operator must not be called for oversized input")
	}
}

// TestInputSecretsNeverEchoed proves the "stderr secrets" local proving
// focus item: a rejected --input value's own bytes never appear in the
// CLI's own diagnostics, whether the rejection is malformed JSON or a
// missing file.
func TestInputSecretsNeverEchoed(t *testing.T) {
	const marker = "TOP-SECRET-MARKER-4f3c9a"
	descriptors := []contract.Descriptor{
		descriptor("organization.create", []string{"organization", "create"}, contract.ModeMutation, true),
	}

	t.Run("malformed JSON", func(t *testing.T) {
		op := &fakeOperator{}
		malformed := `{"token":"` + marker // missing closing quote and brace
		_, stderr, code := run(t, []string{"organization", "create", "--submission-key", "k1", "--input", malformed}, op, descriptors, nil)
		if code != 2 {
			t.Fatalf("exit code = %d, want 2", code)
		}
		if strings.Contains(stderr, marker) {
			t.Fatalf("stderr echoed the rejected input bytes: %q", stderr)
		}
	})

	t.Run("malformed file content", func(t *testing.T) {
		// The path itself is an ordinary, expected diagnostic (it names
		// what the CLI client opened); the file's own bytes — where a
		// real secret could live — must never appear in a diagnostic.
		path := filepath.Join(t.TempDir(), "input.json")
		if err := os.WriteFile(path, []byte(`{"token":"`+marker), 0o600); err != nil {
			t.Fatalf("writing fixture file: %v", err)
		}
		op := &fakeOperator{}
		_, stderr, code := run(t, []string{"organization", "create", "--submission-key", "k1", "--input", "@" + path}, op, descriptors, nil)
		if code != 2 {
			t.Fatalf("exit code = %d, want 2", code)
		}
		if strings.Contains(stderr, marker) {
			t.Fatalf("stderr echoed the rejected file's content: %q", stderr)
		}
	})
}

// TestInputMustBeObject proves a syntactically valid but non-object inline
// value (every operation's input schema is a JSON object) is refused
// locally rather than forwarded.
func TestInputMustBeObject(t *testing.T) {
	descriptors := []contract.Descriptor{
		descriptor("organization.create", []string{"organization", "create"}, contract.ModeMutation, true),
	}
	op := &fakeOperator{}
	_, _, code := run(t, []string{"organization", "create", "--submission-key", "k1", "--input", `"just a string"`}, op, descriptors, nil)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if len(op.calls) != 0 {
		t.Fatal("operator must not be called for non-object input")
	}
}
