package configuration

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func TestPublicQualificationPinsCandidateEffectAndDurableJob(t *testing.T) {
	e := newEnv(t)
	connectionID := e.ids.New()
	const destination = "https://openrouter.ai/api/v1/responses"
	const model = "z-ai/glm-flash-latest"
	resolvedConnection := resolvedQualificationConnection{
		Connection: struct {
			ID              contract.ID `json:"id"`
			Version         int64       `json:"version"`
			Provider        string      `json:"provider"`
			AccountIdentity string      `json:"account_identity"`
		}{ID: connectionID, Version: 7, Provider: "openrouter", AccountIdentity: "synthetic-account"},
		Tool: struct {
			ID      contract.ID `json:"id"`
			Version int64       `json:"version"`
			Adapter string      `json:"adapter"`
		}{ID: modelResponsesToolID, Version: 1, Adapter: "zatiti/model-responses/v1"},
	}
	resolvedConnection.Connection.Provider = "openai"
	e.ports.qualificationConnections[connectionID] = resolvedConnection

	cost := wireMoney{Currency: "USD", MicroUnits: 20_000}
	candidate := map[string]any{
		"executor": "hosted", "model": model, "connection_id": connectionID,
		"provider_destination": destination, "capabilities": []string{}, "cost_bound": cost,
		"classification": "internal", "context_capture": "complete", "connection_version": 7,
		"adapter_profile": map[string]any{
			"schema": "zatiti.responses/v2", "endpoint": destination, "model": model,
			"connection_id": connectionID, "max_input_tokens": 32768, "max_output_tokens": 2048,
			"max_response_bytes": 65536, "timeout_seconds": 30, "currency": "USD",
			"input_rate":  map[string]any{"numerator_micro_units": 500000, "denominator_units": 1000000, "unit": "input_token"},
			"output_rate": map[string]any{"numerator_micro_units": 1500000, "denominator_units": 1000000, "unit": "output_token"},
			"enforcement": map[string]any{
				"cost": "enforced", "disclosure": "enforced", "maximum_cost": cost,
				"provider_destinations": []string{destination},
			},
			"provider": "openrouter", "session_mode": "stateless",
			"routing": map[string]any{
				"only": []string{model}, "allow_fallbacks": false, "require_parameters": true,
				"price_ceiling": map[string]any{"currency": "USD", "input_per_million": "0.5", "output_per_million": "1.5"},
			},
		},
	}
	request := qualifyExecutionProfileInput{
		Scope: e.scope, Definition: mustJSON(t, candidate), QualificationCostBound: cost,
	}
	_ = e.expectFault("execution_profile.qualify", request, contract.CodePrerequisiteMissing)
	if got := len(e.ports.callsOf("_effects.prepare")); got != 0 {
		t.Fatalf("provider/connection mismatch prepared %d effects, want none", got)
	}
	resolvedConnection.Connection.Provider = "openrouter"
	e.ports.qualificationConnections[connectionID] = resolvedConnection
	payload := e.mustAccepted("execution_profile.qualify", request)
	var accepted struct {
		Job wireJob `json:"job"`
	}
	e.decode(payload.Data, &accepted)
	if accepted.Job.ID == "" || accepted.Job.State != "pending" || accepted.Job.Operation != "execution_profile.qualify" {
		t.Fatalf("qualification did not create one pending durable job: %+v", accepted.Job)
	}
	_ = e.expectFault("execution_profile.qualify", request, contract.CodePrerequisiteMissing)

	prepareCalls := e.ports.callsOf("_effects.prepare")
	if len(prepareCalls) != 1 {
		t.Fatalf("qualification prepared %d effects, want exactly one", len(prepareCalls))
	}
	var prepared struct {
		Scope           wireScope       `json:"scope"`
		QualificationID contract.ID     `json:"qualification_id"`
		SourceID        contract.ID     `json:"source_id"`
		Action          json.RawMessage `json:"action"`
	}
	if err := contract.DecodeStrict(prepareCalls[0].Input, &prepared); err != nil {
		t.Fatal(err)
	}
	var action struct {
		Connection struct {
			ID      contract.ID `json:"id"`
			Version int64       `json:"version"`
		} `json:"connection"`
		AccountIdentity string          `json:"account_identity"`
		Destination     string          `json:"destination"`
		CostBound       wireMoney       `json:"cost_bound"`
		Parameters      json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal(prepared.Action, &action); err != nil {
		t.Fatal(err)
	}
	if prepared.QualificationID == "" || prepared.SourceID != prepared.QualificationID ||
		action.Connection.ID != connectionID || action.Connection.Version != 7 ||
		action.AccountIdentity != "synthetic-account" || action.Destination != destination ||
		action.CostBound != cost {
		t.Fatalf("effect was not pinned to the verified model connection and spend bound: %+v", prepared)
	}
	var probe struct {
		Schema        string    `json:"schema"`
		Kind          string    `json:"kind"`
		Model         string    `json:"model"`
		Provider      string    `json:"provider"`
		ProbeText     string    `json:"probe_text"`
		ProfileDigest string    `json:"profile_digest"`
		CostBound     wireMoney `json:"qualification_cost_bound"`
	}
	if err := json.Unmarshal(action.Parameters, &probe); err != nil {
		t.Fatal(err)
	}
	if probe.Schema != "zatiti.responses.action/v2" || probe.Kind != "qualification_probe" ||
		probe.Model != model || probe.Provider != "openrouter" || probe.ProfileDigest == "" ||
		probe.CostBound != cost || probe.ProbeText != qualificationProbeText {
		t.Fatalf("bounded probe did not retain the selected model and candidate identity: %+v", probe)
	}
	e.ports.mu.Lock()
	effectID := e.ports.qualificationEffects[prepared.QualificationID]
	e.ports.mu.Unlock()
	if effectID == "" || accepted.Job.OperationID != effectID {
		t.Fatalf("job operation ID %q does not match the effect ID %q", accepted.Job.OperationID, effectID)
	}

	resolved := e.mustOK("_configuration.execution_profile.qualification.resolve", qualificationResolveInput{QualificationID: prepared.QualificationID})
	var pinned qualificationResolveOutput
	e.decode(resolved.Data, &pinned)
	var got qualificationCandidate
	if err := contract.DecodeStrict(pinned.Candidate, &got); err != nil {
		t.Fatal(err)
	}
	if pinned.ProfileDigest != probe.ProfileDigest || got.Model != model || got.ConnectionID != connectionID || got.ConnectionVersion != 7 {
		t.Fatalf("private candidate resolution drifted from the dispatched probe: digest=%s candidate=%+v", pinned.ProfileDigest, got)
	}

	// A separate client using a fresh submission key still cannot submit the
	// same profile after this probe's outcome becomes unknown.
	e.mustOK("_configuration.execution_profile.qualify.finish", finishQualifiedExecutionProfileInput{
		JobID: accepted.Job.ID, ExpectedVersion: accepted.Job.Version, Generation: e.generation(),
		OperationID: effectID, ProfileDigest: probe.ProfileDigest, State: "outcome_unknown",
	})
	_ = e.expectFault("execution_profile.qualify", request, contract.CodePrerequisiteMissing)
	if got := len(e.ports.callsOf("_effects.prepare")); got != 1 {
		t.Fatalf("pending/unknown duplicate prepared %d paid effects, want the original one", got)
	}
}

func TestQualificationTerminalOutcomeClosesCandidateIdempotently(t *testing.T) {
	e := newEnv(t)
	qualificationID, jobID, effectID := e.ids.New(), e.ids.New(), e.ids.New()
	digest := string(contract.Hash([]byte("candidate")))
	scopeJSON, err := contract.Canonicalize(mustJSON(t, e.scope))
	if err != nil {
		t.Fatal(err)
	}
	now := e.clock.Now().UTC()
	job := wireJob{ID: jobID, Version: 3, State: "running", Owner: ownerName, Operation: "execution_profile.qualify", OperationID: effectID}
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		if err := insertQualification(e.ctx, unit, qualificationRow{
			ID: qualificationID, InstallationID: e.install, ScopeJSON: string(scopeJSON),
			CandidateJSON: `{}`, ProfileDigest: digest, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		return bindQualification(e.ctx, unit, e.install, qualificationID, effectID, job, now)
	}); err != nil {
		t.Fatalf("seed pending qualification: %v", err)
	}

	finish := finishQualifiedExecutionProfileInput{
		JobID: jobID, ExpectedVersion: job.Version, Generation: e.generation(),
		OperationID: effectID, ProfileDigest: digest, State: "outcome_unknown",
	}
	e.mustOK("_configuration.execution_profile.qualify.finish", finish)
	// Controller callback redelivery is safe, while it cannot reopen the
	// private candidate for a second paid provider request.
	e.mustOK("_configuration.execution_profile.qualify.finish", finish)
	row, err := fetchTestQualification(e, qualificationID)
	if err != nil {
		t.Fatal(err)
	}
	if row.State != "outcome_unknown" {
		t.Fatalf("qualification state = %q, want terminal outcome_unknown", row.State)
	}
	_ = e.expectFault("_configuration.execution_profile.qualification.resolve", map[string]any{
		"qualification_id": qualificationID,
	}, contract.CodePrerequisiteMissing)
}

func TestFailedQualificationCannotBeRepeatedWithoutNonExecutionEvidence(t *testing.T) {
	e := newEnv(t)
	qualificationID, jobID, effectID := e.ids.New(), e.ids.New(), e.ids.New()
	digest := string(contract.Hash([]byte("same-candidate")))
	now := e.clock.Now().UTC()
	job := wireJob{ID: jobID, Version: 2, State: "failed", Owner: ownerName, Operation: "execution_profile.qualify", OperationID: effectID}
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		if err := insertQualification(e.ctx, unit, qualificationRow{
			ID: qualificationID, InstallationID: e.install, CandidateJSON: `{}`, ProfileDigest: digest,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		if err := bindQualification(e.ctx, unit, e.install, qualificationID, effectID, job, now); err != nil {
			return err
		}
		_, err := unit.ExecContext(e.ctx, `UPDATE configuration_profile_qualifications SET state = 'failed' WHERE qualification_id = ?`, qualificationID)
		return err
	}); err != nil {
		t.Fatalf("seed failed qualification: %v", err)
	}
	err := e.db.Read(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		return refuseRepeatedQualification(e.ctx, unit, e.install, digest)
	})
	var f *contract.Fault
	if !errors.As(err, &f) || f.Code != contract.CodePrerequisiteMissing {
		t.Fatalf("same candidate after failure returned %v, want prerequisite_missing absent authoritative non-execution proof", err)
	}
}

func fetchTestQualification(e *testEnv, id contract.ID) (*qualificationRow, error) {
	var row *qualificationRow
	err := e.db.Read(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		var err error
		row, err = fetchQualification(e.ctx, unit, e.install, id)
		return err
	})
	return row, err
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
