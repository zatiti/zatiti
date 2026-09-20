package cli_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/zatiti/zatiti/internal/cli"
	"github.com/zatiti/zatiti/internal/contract"
)

// TestEveryDescriptorSubprocessMapping proves the local proving focus item
// "every descriptor subprocess mapping": each descriptor's declared CLI
// path reaches exactly its own operation, including nested paths and a
// command that is simultaneously a leaf (capabilities.list) and a group
// parent (capabilities.schema) at the same node.
func TestEveryDescriptorSubprocessMapping(t *testing.T) {
	descriptors := []contract.Descriptor{
		descriptor("capabilities.list", []string{"capabilities"}, contract.ModeQuery, false),
		descriptor("capabilities.schema", []string{"capabilities", "schema"}, contract.ModeQuery, false),
		descriptor("installation.init", []string{"init"}, contract.ModeMutation, false),
		descriptor("organization.create", []string{"organization", "create"}, contract.ModeMutation, true),
		descriptor("artifact.upload.begin", []string{"artifact", "upload", "begin"}, contract.ModeMutation, true),
	}

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"capabilities leaf", []string{"capabilities"}, "capabilities.list"},
		{"capabilities schema child", []string{"capabilities", "schema"}, "capabilities.schema"},
		{"bootstrap", []string{"init"}, "installation.init"},
		{"two-level path", []string{"organization", "create", "--submission-key", "k1"}, "organization.create"},
		{"three-level path", []string{"artifact", "upload", "begin", "--submission-key", "k1"}, "artifact.upload.begin"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			op := &fakeOperator{}
			_, stderr, code := run(t, tc.args, op, descriptors, nil)
			if code != 0 {
				t.Fatalf("exit code = %d, stderr = %q", code, stderr)
			}
			if len(op.calls) != 1 {
				t.Fatalf("operator calls = %d, want 1", len(op.calls))
			}
			if op.calls[0].operation != tc.want {
				t.Fatalf("operation = %q, want %q", op.calls[0].operation, tc.want)
			}
		})
	}
}

// TestDuplicateCommandPathRejected proves New refuses two descriptors that
// map to the same CLI path instead of silently letting the later one win.
func TestDuplicateCommandPathRejected(t *testing.T) {
	descriptors := []contract.Descriptor{
		descriptor("organization.create", []string{"organization", "create"}, contract.ModeMutation, true),
		descriptor("organization.create.v2", []string{"organization", "create"}, contract.ModeMutation, true),
	}
	if _, err := cli.New(&fakeOperator{}, descriptors, cli.IO{}); err == nil {
		t.Fatal("expected an error for a duplicate CLI path, got nil")
	}
}

// TestMissingCLIMappingRejected proves New refuses a descriptor that
// declares no CLI path at all rather than silently omitting the command.
func TestMissingCLIMappingRejected(t *testing.T) {
	descriptors := []contract.Descriptor{
		descriptor("orphan.op", nil, contract.ModeQuery, false),
	}
	if _, err := cli.New(&fakeOperator{}, descriptors, cli.IO{}); err == nil {
		t.Fatal("expected an error for a descriptor with no CLI mapping, got nil")
	}
}

// TestBootstrapHasNoSubmissionKeyFlag proves the "bootstrap ... parity"
// local proving focus item: installation.init (SubmissionKey: false)
// registers no --submission-key flag and succeeds without one, matching
// "Mutating requests require submission_key ... except one-time init."
func TestBootstrapHasNoSubmissionKeyFlag(t *testing.T) {
	descriptors := []contract.Descriptor{
		descriptor("installation.init", []string{"init"}, contract.ModeMutation, false),
	}
	op := &fakeOperator{}

	_, _, code := run(t, []string{"init"}, op, descriptors, nil)
	if code != 0 {
		t.Fatalf("bootstrap without a submission key: exit code = %d", code)
	}
	if len(op.calls) != 1 || op.calls[0].request.SubmissionKey != "" {
		t.Fatalf("expected one call with an empty submission key, got %+v", op.calls)
	}

	_, stderr, code := run(t, []string{"init", "--submission-key", "x"}, op, descriptors, nil)
	if code == 0 {
		t.Fatal("expected an error: installation.init has no --submission-key flag")
	}
	if stderr == "" {
		t.Fatal("expected a diagnostic on stderr for the unknown flag")
	}
}

// TestSubmissionKeyReplayParity proves the "key replay parity" local
// proving focus item: the CLI forwards the caller's exact submission key on
// every call — it never fabricates, rewrites or drops it — so an operator
// that deduplicates by key returns the identical disposition both times.
func TestSubmissionKeyReplayParity(t *testing.T) {
	descriptors := []contract.Descriptor{
		descriptor("organization.create", []string{"organization", "create"}, contract.ModeMutation, true),
	}
	seen := map[string]contract.Result{}
	op := &fakeOperator{fn: func(_ context.Context, _ string, req contract.Request) (contract.Result, error) {
		if r, ok := seen[req.SubmissionKey]; ok {
			return r, nil
		}
		r := contract.Result{
			Schema:    "zatiti.result/v1",
			CommandID: "00000000-0000-4000-8000-0000000000aa",
			Payload:   contract.Payload{Status: contract.StatusCompleted, Data: json.RawMessage(`{"draft_id":"x"}`)},
		}
		seen[req.SubmissionKey] = r
		return r, nil
	}}

	args := []string{"organization", "create", "--submission-key", "demo-org-create-001", "--json"}
	first, _, code1 := run(t, args, op, descriptors, nil)
	second, _, code2 := run(t, args, op, descriptors, nil)

	if code1 != 0 || code2 != 0 {
		t.Fatalf("exit codes = %d, %d, want 0, 0", code1, code2)
	}
	if first != second {
		t.Fatalf("replayed submission key produced different envelopes:\n%s\n%s", first, second)
	}
	if len(op.calls) != 2 {
		t.Fatalf("operator calls = %d, want 2", len(op.calls))
	}
	if op.calls[0].request.SubmissionKey != "demo-org-create-001" || op.calls[1].request.SubmissionKey != "demo-org-create-001" {
		t.Fatalf("submission key was not forwarded identically: %+v", op.calls)
	}
}

// TestJSONModeSingleEnvelopeRetainsCommandIdentityOnReplay proves --json
// emits exactly one decodable envelope for task.create — the new
// revision-3 work-start family — and that replaying the identical
// submission key returns the very same command_id rather than minting a
// new one, matching "recover lost acknowledgements by command lookup, not
// a new key." json.Decoder.More after one Decode is a stronger single-
// envelope proof than counting stdout lines: it fails on any trailing
// JSON value, not merely a second newline-delimited one.
func TestJSONModeSingleEnvelopeRetainsCommandIdentityOnReplay(t *testing.T) {
	descriptors := []contract.Descriptor{
		descriptor("task.create", []string{"task", "create"}, contract.ModeMutation, true),
	}
	seen := map[string]contract.Result{}
	op := &fakeOperator{fn: func(_ context.Context, _ string, req contract.Request) (contract.Result, error) {
		if r, ok := seen[req.SubmissionKey]; ok {
			return r, nil
		}
		r := contract.Result{
			Schema:    "zatiti.result/v1",
			CommandID: "00000000-0000-4000-8000-0000000000c1",
			Payload:   contract.Payload{Status: contract.StatusCompleted, Data: json.RawMessage(`{"resource":{"id":"task-1"}}`)},
		}
		seen[req.SubmissionKey] = r
		return r, nil
	}}

	args := []string{"task", "create", "--submission-key", "demo-task-create-001", "--json"}
	first, _, code1 := run(t, args, op, descriptors, nil)
	second, _, code2 := run(t, args, op, descriptors, nil)
	if code1 != 0 || code2 != 0 {
		t.Fatalf("exit codes = %d, %d, want 0, 0", code1, code2)
	}

	var firstEnv, secondEnv contract.Result
	for name, out := range map[string]*contract.Result{"first": &firstEnv, "second": &secondEnv} {
		raw := first
		if name == "second" {
			raw = second
		}
		dec := json.NewDecoder(strings.NewReader(raw))
		if err := dec.Decode(out); err != nil {
			t.Fatalf("%s call: stdout was not one valid envelope: %v", name, err)
		}
		if dec.More() {
			t.Fatalf("%s call: stdout carried more than one JSON document: %q", name, raw)
		}
	}

	if firstEnv.CommandID != secondEnv.CommandID {
		t.Fatalf("replay minted a new command_id: first %q, second %q", firstEnv.CommandID, secondEnv.CommandID)
	}
	if len(op.calls) != 2 {
		t.Fatalf("operator calls = %d, want 2", len(op.calls))
	}
	if op.calls[0].request.SubmissionKey != "demo-task-create-001" || op.calls[1].request.SubmissionKey != "demo-task-create-001" {
		t.Fatalf("submission key was not forwarded identically across replay: %+v", op.calls)
	}
}

// TestUnknownFlagIsInvalidInput proves a malformed invocation the CLI
// itself rejects (never reaching an operator) still exits under the
// contract's invalid_input code (2), not the catch-all 1.
func TestUnknownFlagIsInvalidInput(t *testing.T) {
	descriptors := []contract.Descriptor{
		descriptor("capabilities.list", []string{"capabilities"}, contract.ModeQuery, false),
	}
	op := &fakeOperator{}
	_, stderr, code := run(t, []string{"capabilities", "--does-not-exist"}, op, descriptors, nil)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (invalid_input); stderr = %q", code, stderr)
	}
	if len(op.calls) != 0 {
		t.Fatal("operator must not be called for a request the CLI could not construct")
	}
}

// TestTransportMechanicsNotShadowed proves capabilities.list's group node
// can also carry an unrelated command added by the caller (standing in for
// serve/mcp serve/help/completion, added by cmd assembly, not this
// package) without colliding with the generated product commands.
func TestTransportMechanicsNotShadowed(t *testing.T) {
	descriptors := []contract.Descriptor{
		descriptor("capabilities.list", []string{"capabilities"}, contract.ModeQuery, false),
	}
	root, err := cli.New(&fakeOperator{}, descriptors, cli.IO{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	names := map[string]bool{}
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	if !names["capabilities"] {
		t.Fatal("expected a generated \"capabilities\" command")
	}
	if names["serve"] || names["mcp"] {
		t.Fatal("cli.New must never register transport-mechanic commands itself")
	}
	// Assembly-added commands (serve, mcp serve, ...) must be addable
	// without New having reserved or collided with their names.
	root.AddCommand(&cobra.Command{Use: "serve", RunE: func(*cobra.Command, []string) error { return nil }})
	for _, c := range root.Commands() {
		if c.Name() == "serve" {
			return
		}
	}
	t.Fatal("caller-added \"serve\" command did not attach to the tree New returned")
}
