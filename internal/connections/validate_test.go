package connections

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// connection.validate action-generation and job-completion behavior
// (implementation assignment steps 1, 3 and 4): every built-in validation
// action passes its adapter's strict schema and completes job.get; an
// adapter that accepts only domain-specific actions this connection cannot
// honestly supply refuses capability_unsupported instead of an invented
// generic probe.

// TestBuiltInValidationActionsMatchAdapterSchema is the required behavioral
// property: for every provider whose selected adapter supports a
// connection-general probe, connection.validate generates the exact
// supported adapter action, it independently validates against that tool's
// own strict schema, and the resulting observation completes the
// execution-side job (job.get would show it done).
func TestBuiltInValidationActionsMatchAdapterSchema(t *testing.T) {
	cases := []struct {
		name       string
		provider   string
		toolName   string
		wantSchema string
		mutate     func(*wireConnection)
	}{
		{
			name: "model-responses-prepare-session", provider: "openai-responses",
			toolName: toolNameModelResponses, wantSchema: "zatiti.responses.action/v1",
		},
		{
			name: "public-http-read", provider: "http", toolName: toolNameHTTPPublicRead,
			wantSchema: "zatiti.httpread.action/v1",
			mutate:     func(w *wireConnection) { w.Destinations = []string{"https://status.example.test/health"} },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newEnv(t)
			conn := env.seedConnection(func(w *wireConnection) {
				w.Provider = tc.provider
				if tc.mutate != nil {
					tc.mutate(w)
				}
			})

			payload := env.mustOK("connection.validate", connValidateIn{
				Scope: env.scope, ID: conn.ID, ExpectedVersion: conn.Version,
			})
			var out jobOut
			env.decode(payload.Data, &out)
			if out.Resource.Owner != "connections" || out.Resource.Operation != "connection.validate" {
				t.Fatalf("admitted job carries owner=%q operation=%q", out.Resource.Owner, out.Resource.Operation)
			}

			// The generated action rode inside the job.create call; decode it
			// back out and independently validate it against the selected
			// tool's own strict schema (never trusting the handler's own
			// internal self-check alone).
			calls := env.ports.callsOf("_execution.job.create")
			if len(calls) != 1 {
				t.Fatalf("connection.validate made %d job-creation calls, want 1", len(calls))
			}
			var stored struct {
				Input json.RawMessage `json:"input"`
			}
			env.decode(calls[0].Input, &stored)
			var probe struct {
				Tool   wireRef         `json:"tool"`
				Action json.RawMessage `json:"action"`
			}
			env.decode(stored.Input, &probe)
			var schemaProbe struct {
				Schema string `json:"schema"`
			}
			env.decode(probe.Action, &schemaProbe)
			if schemaProbe.Schema != tc.wantSchema {
				t.Fatalf("generated action schema %q, want %q", schemaProbe.Schema, tc.wantSchema)
			}
			tool := env.mustOK("tool.get", connGetIn{Scope: env.scope, ID: probe.Tool.ID})
			var toolOut resourceToolOut
			env.decode(tool.Data, &toolOut)
			if toolOut.Resource.Name != tc.toolName {
				t.Fatalf("selected tool %q, want %q", toolOut.Resource.Name, tc.toolName)
			}
			if verr := contract.ValidateSchema(toolOut.Resource.InputSchema, probe.Action); verr != nil {
				t.Fatalf("generated action does not pass the %s adapter's own strict schema: %v", tc.toolName, verr)
			}

			// The observation completes the execution-side job: job.get
			// would show it done.
			env.mustOK("_connections.validation.record", validationRecordIn{
				ConnectionID: conn.ID, ExpectedVersion: conn.Version,
				Observation: wireObservation{
					Disposition: obsSucceeded,
					Evidence: mustRaw(map[string]any{
						"account_identity": conn.AccountIdentity, "allowed_scopes": conn.AllowedScopes,
					}),
					Usage: &wireUsage{Currency: "USD", Advisory: true},
				},
			})
			recorded := env.ports.callsOf("_execution.job.record")
			if len(recorded) != 1 {
				t.Fatalf("validation.record made %d job.record calls, want 1", len(recorded))
			}
			var completedJob struct {
				JobID           contract.ID `json:"job_id"`
				ExpectedVersion int64       `json:"expected_version"`
				State           string      `json:"state"`
			}
			env.decode(recorded[0].Input, &completedJob)
			if completedJob.JobID != out.Resource.ID || completedJob.ExpectedVersion != out.Resource.Version {
				t.Fatalf("job.record completed %+v, want job %s at version %d", completedJob, out.Resource.ID, out.Resource.Version)
			}
			if completedJob.State != "succeeded" {
				t.Fatalf("job.record state %q, want succeeded", completedJob.State)
			}
		})
	}
}

// TestValidateRefusesInventedGenericProbe is the do-not-invent-a-probe
// property (implementation assignment step 3): providers whose selected
// adapter accepts only domain-specific actions a Connection definition
// cannot honestly supply (GitHub's five actions all need a specific
// repository; Serenity's supported reads all need a specific brain) refuse
// capability_unsupported. No job is ever admitted for a fabricated action.
func TestValidateRefusesInventedGenericProbe(t *testing.T) {
	for _, provider := range []string{"github", "serenity"} {
		t.Run(provider, func(t *testing.T) {
			env := newEnv(t)
			conn := env.seedConnection(func(w *wireConnection) { w.Provider = provider })
			f := env.expectFault("connection.validate", connValidateIn{
				Scope: env.scope, ID: conn.ID, ExpectedVersion: conn.Version,
			}, contract.CodeCapabilityUnsupported)
			if !strings.Contains(f.Message, "resource-specific") {
				t.Fatalf("refusal message %q does not explain the domain-specific-only rule", f.Message)
			}
			if calls := env.ports.callsOf("_execution.job.create"); len(calls) != 0 {
				t.Fatalf("refused validate still admitted %d jobs", len(calls))
			}
		})
	}
	t.Run("unmapped-provider", func(t *testing.T) {
		env := newEnv(t)
		conn := env.seedConnection(func(w *wireConnection) { w.Provider = "no-such-provider" })
		_ = env.expectFault("connection.validate", connValidateIn{
			Scope: env.scope, ID: conn.ID, ExpectedVersion: conn.Version,
		}, contract.CodeCapabilityUnsupported)
	})
}
