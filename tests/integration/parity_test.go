package integration_test

import (
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// parityScript is the same operation sequence every transport runs against
// its own isolated, identically prepared fixture: a query, a keyed mutation,
// its identical replay, a changed-input reuse of the key, a handler refusal,
// a missing resource, a stale version and a schema violation.
func parityScript(f *fixture) []call {
	principal := func(name string) map[string]any {
		return map[string]any{"scope": f.scope(), "definition": map[string]any{
			"kind": "client_agent", "name": name, "scope": f.scope(), "revoked": false,
		}}
	}
	return []call{
		{name: "query", op: "installation.status", input: map[string]any{"scope": f.scope()}},
		{name: "mutation", op: "principal.create", key: "parity-1", input: principal("parity-agent")},
		{name: "identical replay", op: "principal.create", key: "parity-1", input: principal("parity-agent")},
		{name: "changed input under the same key", op: "principal.create", key: "parity-1", input: principal("parity-other")},
		{name: "list after mutation", op: "principal.list", input: map[string]any{"scope": f.scope()}, instanceOnly: true},
		{name: "missing resource", op: "principal.get", input: map[string]any{"scope": f.scope(), "id": "00000000-0000-4000-8000-00000000dead"}},
		{name: "stale version", op: "installation.pause", key: "parity-2", input: map[string]any{"scope": f.scope(), "expected_version": 41}},
		{name: "schema violation", op: "principal.get", input: map[string]any{"scope": f.scope()}},
		{name: "foreign installation scope", op: "installation.status", input: map[string]any{
			"scope": contract.Scope{InstallationID: "00000000-0000-4000-8000-00000000beef"}}},
	}
}

// wantCodes pins the expected fault code per script step, so parity cannot
// pass by every transport being equally wrong.
var wantCodes = map[string]string{
	"query":                            "",
	"mutation":                         "",
	"identical replay":                 "",
	"changed input under the same key": contract.CodeSubmissionConflict,
	"list after mutation":              "",
	"missing resource":                 contract.CodeNotFound,
	"stale version":                    contract.CodeStaleVersion,
	"schema violation":                 contract.CodeInvalidInput,
	"foreign installation scope":       contract.CodePermissionDenied,
}

type parityRun struct {
	transport string
	envelopes []string
	codes     []string
	commands  []contract.ID
	signals   []string
	// sameInstance holds, per step, the in-process application's envelope
	// for the same query on the same instance, taken right after the
	// transport's own call.
	sameInstance []string
}

// runParity prepares one isolated fixture, serves it, and runs the script
// through the transport build makes for it.
func runParity(t *testing.T, build func(t *testing.T, f *fixture) transport) parityRun {
	t.Helper()
	f := newBootstrappedFixture(t)
	f.provisionTransportCredential(transportToken)
	tr := build(t, f)
	norm := newNormalizer()
	// Seed the explicit mapping with the identities every fixture generates
	// before the script starts, so first-seen order matches across fixtures.
	// The owner and the controller service principal are seeded in the order
	// this instance lists them: bootstrap inserts both in one transaction, so
	// they share a created_at tick and principal.list (internal/identity/
	// principal_ops.go:183, ORDER BY created_at, id) breaks the tie on the
	// random id, an order that is stable per installation but differs across
	// installations. A list step therefore compares against the in-process
	// application on the same instance (instanceOnly), which is the parity
	// claim itself: the same state through different transports.
	norm.value(string(f.installationID))
	for _, p := range f.principals() {
		norm.value(string(p.ID))
	}
	run := parityRun{transport: tr.name()}
	app := applicationTransport{f: f}
	for _, c := range parityScript(f) {
		out := tr.run(t, c)
		same := ""
		if c.instanceOnly {
			// A query mints a fresh, non-durable command identity per call
			// (frozen contract: "Query command IDs need not be durable"), so
			// the two calls compare without it.
			out.envelope.CommandID = ""
			twin := app.run(t, c).envelope
			twin.CommandID = ""
			same = norm.envelope(t, twin)
		}
		run.sameInstance = append(run.sameInstance, same)
		if out.envelope.CommandID == "" && out.code != "" {
			// A transport renders a refusal as a failed envelope with its own
			// command identity (internal/server writeFault); the in-process
			// error path carries no envelope. Reserve that identity's slot so
			// later first-seen placeholders line up across runs.
			norm.value(string(contract.NewID()))
		}
		run.envelopes = append(run.envelopes, norm.envelope(t, out.envelope))
		run.codes = append(run.codes, out.code)
		run.commands = append(run.commands, out.envelope.CommandID)
		run.signals = append(run.signals, out.signal)
	}
	return run
}

// scriptedDescriptors returns the landed descriptors the parity script uses.
func scriptedDescriptors(f *fixture) []contract.Descriptor {
	used := map[string]bool{}
	for _, c := range parityScript(f) {
		used[c.op] = true
	}
	var out []contract.Descriptor
	for _, d := range f.catalog.Public() {
		if used[d.ID] {
			out = append(out, d)
		}
	}
	return out
}

func servedOperator(t *testing.T, f *fixture) contract.Operator {
	return newClient(t, f.serve(), staticCredential(transportToken))
}

// TestTransportParity (Z02, R12-004): the same operations through the
// in-process application, internal/client over internal/server's Unix
// socket, the desktop-side raw wire driver over that socket, internal/cli
// over the client, and internal/mcp with a real go-sdk client over the
// client produce equivalent result envelopes, error codes and
// submission-key semantics on isolated matching fixtures.
func TestTransportParity(t *testing.T) {
	t.Parallel()
	builders := []func(t *testing.T, f *fixture) transport{
		func(t *testing.T, f *fixture) transport { return applicationTransport{f: f} },
		func(t *testing.T, f *fixture) transport { return clientTransport{op: servedOperator(t, f)} },
		func(t *testing.T, f *fixture) transport { return wireTransport{sock: f.serve(), token: transportToken} },
		func(t *testing.T, f *fixture) transport {
			return cliTransport{op: servedOperator(t, f), descs: f.catalog.Public()}
		},
		func(t *testing.T, f *fixture) transport {
			// Only the scripted operations: the full tool surface is listed
			// once, in TestEveryLandedOperationIsExposedInBothInterfaces.
			return newMCPTransport(t, servedOperator(t, f), scriptedDescriptors(f))
		},
	}
	var runs []parityRun
	for _, build := range builders {
		runs = append(runs, runParity(t, build))
	}
	script := parityScript(&fixture{})
	for _, run := range runs {
		for i, c := range script {
			if run.codes[i] != wantCodes[c.name] {
				t.Errorf("%s: %s: fault code %q, want %q (signal %s)", run.transport, c.name, run.codes[i], wantCodes[c.name], run.signals[i])
			}
		}
		// Submission-key semantics: the identical replay returns the retained
		// command, not a second one.
		if run.commands[1] == "" || run.commands[1] != run.commands[2] {
			t.Errorf("%s: identical replay returned command %q, the original was %q", run.transport, run.commands[2], run.commands[1])
		}
	}
	// The socket client is the envelope baseline. application.Invoke returns
	// a refusal as a Go error without an envelope (the transport renders the
	// failed envelope), so the in-process run compares envelopes on its
	// successful steps and fault codes on every step.
	base := runs[1]
	for _, run := range runs {
		for i, c := range script {
			if run.transport == "application" && run.codes[i] != "" {
				continue
			}
			if c.instanceOnly {
				if run.envelopes[i] != run.sameInstance[i] {
					t.Errorf("%s: %s: envelope differs from the application on the same instance\n  application: %s\n  %s: %s",
						run.transport, c.name, run.sameInstance[i], run.transport, run.envelopes[i])
				}
				continue
			}
			if run.envelopes[i] != base.envelopes[i] {
				t.Errorf("%s: %s: envelope differs from %s\n  %s: %s\n  %s: %s",
					run.transport, c.name, base.transport, base.transport, base.envelopes[i], run.transport, run.envelopes[i])
			}
		}
	}
}
