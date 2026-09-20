package cli_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// TestNewTaskHistoryIdentityCommandsResolveToExactOperations proves the
// revision-3 catalog's task work-journey family (task.create/task.start,
// alongside the unchanged task.get), the evidence "history" family
// (event.list/event.get/command.get — the durable audit trail and lost-ack
// recovery lookup) and the identity "current principal" query
// (principal.get) each reach exactly their own operation ID through the
// same generic descriptor-driven dispatch every other command uses. No
// bespoke command wiring exists for any one of them; New's descriptor walk
// is the only mechanism, and this locks that down against the real,
// frozen revision-3 CLI paths rather than a synthetic stand-in.
func TestNewTaskHistoryIdentityCommandsResolveToExactOperations(t *testing.T) {
	descriptors := []contract.Descriptor{
		descriptor("task.create", []string{"task", "create"}, contract.ModeMutation, true),
		descriptor("task.start", []string{"task", "start"}, contract.ModeMutation, true),
		descriptor("task.get", []string{"task", "get"}, contract.ModeQuery, false),
		descriptor("event.list", []string{"event", "list"}, contract.ModeQuery, false),
		descriptor("event.get", []string{"event", "get"}, contract.ModeQuery, false),
		descriptor("command.get", []string{"command", "get"}, contract.ModeQuery, false),
		descriptor("principal.get", []string{"principal", "get"}, contract.ModeQuery, false),
	}

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"task create starts a new work journey", []string{"task", "create", "--submission-key", "k1"}, "task.create"},
		{"task start readies an eligible task", []string{"task", "start", "--submission-key", "k2"}, "task.start"},
		{"task get inspects one task", []string{"task", "get"}, "task.get"},
		{"event list reads history after a cursor", []string{"event", "list"}, "event.list"},
		{"event get reads one history entry", []string{"event", "get"}, "event.get"},
		{"command get recovers a lost disposition", []string{"command", "get"}, "command.get"},
		{"principal get resolves an identity", []string{"principal", "get"}, "principal.get"},
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

// TestNewOperationFixturesForwardExactly loads the CLI-local fixtures under
// testdata/ — one per new-shape operation input this card adds CLI
// discoverability for — through --input @file and proves the exact fixture
// bytes reach the operator unchanged, the same equivalence
// TestInputStdinFileInlineEquivalence proves for the inline/stdin forms.
// Each fixture independently validates against its operation's frozen
// input_schema in docs/implementation/operations.json (checked with
// jsonschema at authoring time); the CLI itself never re-validates schema
// shape, so this proves only client-side forwarding fidelity, not server
// acceptance.
func TestNewOperationFixturesForwardExactly(t *testing.T) {
	descriptors := []contract.Descriptor{
		descriptor("task.create", []string{"task", "create"}, contract.ModeMutation, true),
		descriptor("task.start", []string{"task", "start"}, contract.ModeMutation, true),
		descriptor("command.get", []string{"command", "get"}, contract.ModeQuery, false),
		descriptor("principal.get", []string{"principal", "get"}, contract.ModeQuery, false),
	}

	cases := []struct {
		name    string
		args    []string
		fixture string
	}{
		{"task create", []string{"task", "create", "--submission-key", "k1", "--input", "@testdata/task_create_input.json"}, "testdata/task_create_input.json"},
		{"task start", []string{"task", "start", "--submission-key", "k2", "--input", "@testdata/task_start_input.json"}, "testdata/task_start_input.json"},
		{"command get", []string{"command", "get", "--input", "@testdata/command_get_input.json"}, "testdata/command_get_input.json"},
		{"principal get", []string{"principal", "get", "--input", "@testdata/principal_get_input.json"}, "testdata/principal_get_input.json"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, err := os.ReadFile(tc.fixture)
			if err != nil {
				t.Fatalf("reading fixture: %v", err)
			}
			op := &fakeOperator{}
			_, stderr, code := run(t, tc.args, op, descriptors, nil)
			if code != 0 {
				t.Fatalf("exit code = %d, stderr = %q", code, stderr)
			}
			if len(op.calls) != 1 {
				t.Fatalf("operator calls = %d, want 1", len(op.calls))
			}

			var wantJSON, gotJSON any
			if err := json.Unmarshal(want, &wantJSON); err != nil {
				t.Fatalf("unmarshal fixture: %v", err)
			}
			if err := json.Unmarshal(op.calls[0].request.Input, &gotJSON); err != nil {
				t.Fatalf("unmarshal forwarded input: %v", err)
			}
			wantBytes, _ := json.Marshal(wantJSON)
			gotBytes, _ := json.Marshal(gotJSON)
			if string(wantBytes) != string(gotBytes) {
				t.Fatalf("forwarded input diverged from the fixture:\nfixture: %s\nforwarded: %s", wantBytes, gotBytes)
			}
		})
	}
}
