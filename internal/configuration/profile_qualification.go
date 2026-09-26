package configuration

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

const (
	modelResponsesToolID   = "0a000000-0000-4000-8000-0000000000c1"
	qualificationProbeText = "Reply with exactly: ZATITI_MODEL_QUALIFIED"
)

type qualifyExecutionProfileInput struct {
	Scope                  wireScope       `json:"scope"`
	Definition             json.RawMessage `json:"definition"`
	QualificationCostBound wireMoney       `json:"qualification_cost_bound"`
}

type qualificationCandidate struct {
	Executor            string          `json:"executor"`
	Model               string          `json:"model"`
	ConnectionID        contract.ID     `json:"connection_id"`
	ProviderDestination string          `json:"provider_destination"`
	Capabilities        []string        `json:"capabilities"`
	CostBound           wireMoney       `json:"cost_bound"`
	Classification      string          `json:"classification"`
	ContextCapture      string          `json:"context_capture"`
	AdapterProfile      json.RawMessage `json:"adapter_profile"`
	ConnectionVersion   int64           `json:"connection_version"`
}

type qualificationRow struct {
	ID             contract.ID
	InstallationID contract.ID
	ScopeJSON      string
	CandidateJSON  string
	ProfileDigest  string
	State          string
	EffectID       contract.ID
	JobID          contract.ID
	JobVersion     int64
	ResultJSON     string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type qualificationResolveInput struct {
	QualificationID contract.ID `json:"qualification_id"`
}

type qualificationResolveOutput struct {
	Candidate     json.RawMessage `json:"candidate"`
	ProfileDigest string          `json:"profile_digest"`
}

type recordQualifiedExecutionProfileInput struct {
	JobID            contract.ID     `json:"job_id"`
	ExpectedVersion  int64           `json:"expected_version"`
	Generation       int64           `json:"generation"`
	OperationID      contract.ID     `json:"operation_id"`
	ProfileDigest    string          `json:"profile_digest"`
	Artifact         wireArtifactRef `json:"artifact"`
	QualifiedAt      time.Time       `json:"qualified_at"`
	AdapterVersion   string          `json:"adapter_version"`
	SourceRevision   string          `json:"source_revision"`
	ProtocolRevision string          `json:"protocol_revision"`
	Capabilities     []string        `json:"capabilities"`
	Limitations      []string        `json:"limitations"`
}

type recordQualifiedExecutionProfileOutput struct {
	Resource json.RawMessage `json:"resource"`
}

type finishQualifiedExecutionProfileInput struct {
	JobID           contract.ID `json:"job_id"`
	ExpectedVersion int64       `json:"expected_version"`
	Generation      int64       `json:"generation"`
	OperationID     contract.ID `json:"operation_id"`
	ProfileDigest   string      `json:"profile_digest"`
	State           string      `json:"state"`
}

// handleQualifyExecutionProfile pins the evidence-free candidate, one normal
// governed Responses effect, and the durable job in one owner transaction.
func handleQualifyExecutionProfile(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[qualifyExecutionProfileInput](s, inv.Operation, inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Payload{}, err
	}
	if err := validateQualificationCandidate(in.Definition, in.QualificationCostBound); err != nil {
		return contract.Payload{}, err
	}
	var candidate qualificationCandidate
	if err := contract.DecodeStrict(in.Definition, &candidate); err != nil {
		return contract.Payload{}, invalidInput("definition is not a strict provider-profile candidate")
	}
	canonical, err := contract.Canonicalize(in.Definition)
	if err != nil {
		return contract.Payload{}, invalidInput("qualification candidate is not canonical JSON")
	}
	profileDigest := string(contract.Hash(canonical))
	// The Desktop persists an uncertain qualification, but clients can bypass
	// it or retry with a new submission key. Keep the single-probe guarantee at
	// the owner boundary as well: an identical candidate cannot create another
	// paid effect while an earlier job is pending or has an unknown outcome.
	if err := refuseRepeatedQualification(ctx, unit, in.Scope.InstallationID, profileDigest); err != nil {
		return contract.Payload{}, err
	}
	qualificationID := s.ids.New()
	now := s.clock.Now().UTC()
	scopeJSON, err := contract.Canonicalize([]byte(marshalJSON(in.Scope)))
	if err != nil {
		return contract.Payload{}, internalError("qualification scope could not be canonicalized")
	}
	row := qualificationRow{
		ID: qualificationID, InstallationID: in.Scope.InstallationID,
		ScopeJSON: string(scopeJSON), CandidateJSON: string(canonical),
		ProfileDigest: profileDigest, State: "pending", CreatedAt: now, UpdatedAt: now,
	}
	if err := insertQualification(ctx, unit, row); err != nil {
		return contract.Payload{}, faultOf(err)
	}

	resolved, err := s.resolveQualificationConnection(ctx, unit, in.Scope, candidate)
	if err != nil {
		return contract.Payload{}, err
	}
	revision, err := currentConfigurationRevision(ctx, unit)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	action := map[string]any{
		"scope":                  in.Scope,
		"tool":                   map[string]any{"id": modelResponsesToolID, "version": 1},
		"connection":             map[string]any{"id": candidate.ConnectionID, "version": candidate.ConnectionVersion},
		"account_identity":       resolved.Connection.AccountIdentity,
		"destination":            candidate.ProviderDestination,
		"content":                []any{},
		"not_before":             now,
		"expires_at":             now.Add(time.Duration(profileTimeout(candidate.AdapterProfile)) * time.Second),
		"preconditions":          map[string]any{},
		"configuration_revision": revision,
		"parameters":             qualificationAction(candidate, profileDigest, in.QualificationCostBound),
		"cost_bound":             in.QualificationCostBound,
	}
	effectRaw, err := s.callOwner(ctx, unit, "_effects.prepare", map[string]any{
		"scope": in.Scope, "action": action, "source_id": qualificationID,
		"qualification_id": qualificationID,
	})
	if err != nil {
		return contract.Payload{}, err
	}
	var effect struct {
		Resource wireOperationRef `json:"resource"`
	}
	if err := json.Unmarshal(effectRaw, &effect); err != nil {
		return contract.Payload{}, internalError("effects.prepare returned an invalid operation resource")
	}
	if effect.Resource.ID == "" {
		return contract.Payload{}, internalError("effects.prepare returned an empty operation identity")
	}
	jobInput, err := json.Marshal(map[string]any{"qualification_id": qualificationID})
	if err != nil {
		return contract.Payload{}, internalError("qualification job input could not be encoded")
	}
	jobRaw, err := s.callOwner(ctx, unit, "_execution.job.create", executionJobCreateIn{
		Scope: in.Scope, Owner: ownerName, Operation: "execution_profile.qualify",
		Input: jobInput, SourceID: qualificationID, OperationID: effect.Resource.ID,
	})
	if err != nil {
		return contract.Payload{}, err
	}
	var job struct {
		Resource wireJob `json:"resource"`
	}
	if err := contract.DecodeStrict(jobRaw, &job); err != nil || job.Resource.ID == "" {
		return contract.Payload{}, internalError("execution.job.create returned an invalid qualification job")
	}
	if err := bindQualification(ctx, unit, in.Scope.InstallationID, qualificationID, effect.Resource.ID, job.Resource, now); err != nil {
		return contract.Payload{}, faultOf(err)
	}
	return s.accepted(map[string]any{"job": job.Resource})
}

func refuseRepeatedQualification(ctx context.Context, unit contract.Unit, installationID contract.ID, profileDigest string) error {
	var qualificationID contract.ID
	var state string
	err := unit.QueryRowContext(ctx, `SELECT qualification_id, state
		FROM configuration_profile_qualifications
		WHERE installation_id = ? AND profile_digest = ?
		ORDER BY created_at DESC LIMIT 1`, installationID, profileDigest).Scan(&qualificationID, &state)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return faultOf(err)
	}
	return fault(contract.CodePrerequisiteMissing,
		"identical provider profile already has qualification %s in %s state; do not repeat the probe without authoritative proof that the prior request was not executed", qualificationID, state)
}

type wireOperationRef struct {
	ID contract.ID `json:"id"`
}

type resolvedQualificationConnection struct {
	Connection struct {
		ID              contract.ID `json:"id"`
		Version         int64       `json:"version"`
		Provider        string      `json:"provider"`
		AccountIdentity string      `json:"account_identity"`
	} `json:"connection"`
	Tool struct {
		ID      contract.ID `json:"id"`
		Version int64       `json:"version"`
		Adapter string      `json:"adapter"`
	} `json:"tool"`
}

func (s *Service) resolveQualificationConnection(ctx context.Context, unit contract.Unit, scope wireScope, candidate qualificationCandidate) (resolvedQualificationConnection, error) {
	raw, err := s.callOwner(ctx, unit, "_connections.resolve", map[string]any{
		"scope":       scope,
		"connection":  map[string]any{"id": candidate.ConnectionID, "version": candidate.ConnectionVersion},
		"tool":        map[string]any{"id": modelResponsesToolID, "version": 1},
		"destination": candidate.ProviderDestination,
	})
	if err != nil {
		return resolvedQualificationConnection{}, err
	}
	var out resolvedQualificationConnection
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, internalError("connections.resolve returned an invalid model connection")
	}
	if out.Connection.ID != candidate.ConnectionID || out.Connection.Version != candidate.ConnectionVersion ||
		out.Tool.ID != modelResponsesToolID || out.Tool.Adapter != "zatiti/model-responses/v1" {
		return out, fault(contract.CodePrerequisiteMissing, "the exact provider connection or Responses adapter contract is unavailable")
	}
	if provider := adapterProfileProvider(candidate.AdapterProfile); out.Connection.Provider != provider {
		return out, fault(contract.CodePrerequisiteMissing, "the selected Responses provider does not match the verified provider connection")
	}
	if strings.TrimSpace(out.Connection.AccountIdentity) == "" {
		return out, fault(contract.CodePrerequisiteMissing, "provider connection has no verified account identity")
	}
	return out, nil
}

func validateQualificationCandidate(raw json.RawMessage, bound wireMoney) error {
	if err := contract.ValidateSchema(withDefs(`{"$schema":"https://json-schema.org/draft/2020-12/schema","$ref":"#/$defs/ExecutionProfileCandidate"}`, defsRaw()), raw); err != nil {
		return invalidInput("definition is not a valid ExecutionProfileCandidate: %v", err)
	}
	var c qualificationCandidate
	if err := contract.DecodeStrict(raw, &c); err != nil {
		return invalidInput("definition is not a strict ExecutionProfileCandidate")
	}
	if c.Executor != "hosted" || c.Model == "" || c.ConnectionID == "" || c.ConnectionVersion < 1 ||
		c.ProviderDestination == "" || c.CostBound.Currency != bound.Currency ||
		bound.Currency == "" || bound.MicroUnits < 1 || bound.MicroUnits > c.CostBound.MicroUnits {
		return invalidInput("qualification requires a hosted candidate and an explicit positive cost ceiling within the profile bound")
	}
	if c.AdapterProfileSchema() != "zatiti.responses/v2" {
		return invalidInput("provider qualification currently requires a zatiti.responses/v2 profile draft")
	}
	var profile struct {
		Schema         string      `json:"schema"`
		Endpoint       string      `json:"endpoint"`
		Model          string      `json:"model"`
		ConnectionID   contract.ID `json:"connection_id"`
		TimeoutSeconds int64       `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(c.AdapterProfile, &profile); err != nil || profile.Model != c.Model ||
		profile.Endpoint != c.ProviderDestination || profile.ConnectionID != c.ConnectionID || profile.TimeoutSeconds < 1 {
		return invalidInput("adapter_profile does not match the selected model, endpoint, connection or timeout")
	}
	return nil
}

func defsRaw() json.RawMessage {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(schemaDefs), &doc); err != nil {
		return nil
	}
	return doc["$defs"]
}

func qualificationAction(c qualificationCandidate, profileDigest string, qualificationCostBound wireMoney) map[string]any {
	var mode string
	if c.AdapterProfileSchema() == "zatiti.responses/v1" {
		mode = "provider_conversation"
	} else {
		var v struct {
			SessionMode string `json:"session_mode"`
		}
		_ = json.Unmarshal(c.AdapterProfile, &v)
		mode = v.SessionMode
	}
	// The OpenAI Responses contract requires at least 16 output tokens;
	// the other supported stateless gateways accept the same bounded value.
	maxOutput := int64(16)
	var p struct {
		MaxOutputTokens int64 `json:"max_output_tokens"`
	}
	_ = json.Unmarshal(c.AdapterProfile, &p)
	if p.MaxOutputTokens > 0 && maxOutput > p.MaxOutputTokens {
		maxOutput = p.MaxOutputTokens
	}
	return map[string]any{
		"schema": "zatiti.responses.action/v2", "kind": "qualification_probe",
		"session_mode": mode, "model": c.Model, "probe_text": qualificationProbeText,
		"max_output_tokens":        maxOutput,
		"profile_digest":           profileDigest,
		"qualification_cost_bound": qualificationCostBound,
		"provider":                 adapterProfileProvider(c.AdapterProfile),
	}
}

func (c qualificationCandidate) AdapterProfileSchema() string {
	var p struct {
		Schema string `json:"schema"`
	}
	_ = json.Unmarshal(c.AdapterProfile, &p)
	return p.Schema
}

func profileTimeout(raw json.RawMessage) int64 {
	var p struct {
		TimeoutSeconds int64 `json:"timeout_seconds"`
	}
	_ = json.Unmarshal(raw, &p)
	if p.TimeoutSeconds <= 0 {
		return 60
	}
	return p.TimeoutSeconds
}

func currentConfigurationRevision(ctx context.Context, unit contract.Unit) (int64, error) {
	var revision int64
	err := unit.QueryRowContext(ctx, "SELECT revision FROM configuration_head WHERE id = 1").Scan(&revision)
	if err != nil {
		return 0, err
	}
	if revision < 1 {
		return 0, fault(contract.CodePrerequisiteMissing, "configuration must be bootstrapped before profile qualification")
	}
	return revision, nil
}

func insertQualification(ctx context.Context, unit contract.Unit, r qualificationRow) error {
	_, err := unit.ExecContext(ctx, `INSERT INTO configuration_profile_qualifications
		(qualification_id, installation_id, scope_json, candidate_json, profile_digest, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'pending', ?, ?)`, r.ID, r.InstallationID, r.ScopeJSON, r.CandidateJSON, r.ProfileDigest,
		r.CreatedAt.Format(timeLayout), r.UpdatedAt.Format(timeLayout))
	return err
}

func bindQualification(ctx context.Context, unit contract.Unit, install, id, effectID contract.ID, job wireJob, now time.Time) error {
	res, err := unit.ExecContext(ctx, `UPDATE configuration_profile_qualifications
		SET effect_operation_id = ?, job_id = ?, job_version = ?, updated_at = ?
		WHERE qualification_id = ? AND installation_id = ? AND state = 'pending'`,
		effectID, job.ID, job.Version, now.Format(timeLayout), id, install)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fault(contract.CodeConflict, "qualification %s was not pending when its job was linked", id)
	}
	return nil
}

func fetchQualification(ctx context.Context, unit contract.Unit, install, id contract.ID) (*qualificationRow, error) {
	r := &qualificationRow{}
	var created, updated string
	err := unit.QueryRowContext(ctx, `SELECT qualification_id, installation_id, scope_json, candidate_json,
		profile_digest, state, effect_operation_id, job_id, job_version, COALESCE(result_json, ''), created_at, updated_at
		FROM configuration_profile_qualifications WHERE qualification_id = ? AND installation_id = ?`, id, install).
		Scan(&r.ID, &r.InstallationID, &r.ScopeJSON, &r.CandidateJSON, &r.ProfileDigest, &r.State, &r.EffectID, &r.JobID,
			&r.JobVersion, &r.ResultJSON, &created, &updated)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.CreatedAt, err = time.Parse(timeLayout, created)
	if err != nil {
		return nil, err
	}
	r.UpdatedAt, err = time.Parse(timeLayout, updated)
	return r, err
}

func handleResolveExecutionProfileQualification(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[qualificationResolveInput](s, inv.Operation, inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	row, err := fetchQualification(ctx, unit, unit.Scope().InstallationID, in.QualificationID)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	if row == nil {
		return contract.Payload{}, notFound("profile qualification %s not found", in.QualificationID)
	}
	if row.State != "pending" {
		return contract.Payload{}, fault(contract.CodePrerequisiteMissing, "profile qualification %s is not pending dispatch", in.QualificationID)
	}
	return s.completed(qualificationResolveOutput{Candidate: json.RawMessage(row.CandidateJSON), ProfileDigest: row.ProfileDigest})
}

func handleRecordQualifiedExecutionProfile(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[recordQualifiedExecutionProfileInput](s, inv.Operation, inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if in.Generation != unit.Generation() {
		return contract.Payload{}, fault(contract.CodeConflict, "qualification completion generation %d is not current generation %d", in.Generation, unit.Generation())
	}
	var qualificationID contract.ID
	var installation contract.ID
	err = unit.QueryRowContext(ctx, `SELECT qualification_id, installation_id FROM configuration_profile_qualifications
		WHERE job_id = ?`, in.JobID).Scan(&qualificationID, &installation)
	if err == sql.ErrNoRows {
		return contract.Payload{}, notFound("profile qualification job %s not found", in.JobID)
	}
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	if err := s.checkInstallation(unit, installation); err != nil {
		return contract.Payload{}, err
	}
	row, err := fetchQualification(ctx, unit, installation, qualificationID)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	if row == nil {
		return contract.Payload{}, internalError("qualification job index points to a missing candidate")
	}
	if row.JobID != in.JobID || row.JobVersion != in.ExpectedVersion || row.EffectID != in.OperationID || row.ProfileDigest != in.ProfileDigest {
		return contract.Payload{}, fault(contract.CodeConflict, "qualification callback does not match the exact job version, effects operation and candidate digest")
	}
	if row.State == "succeeded" {
		if row.ResultJSON == "" {
			return contract.Payload{}, internalError("succeeded qualification has no persisted result")
		}
		var saved recordQualifiedExecutionProfileOutput
		if err := contract.DecodeStrict([]byte(row.ResultJSON), &saved); err != nil || len(saved.Resource) == 0 {
			return contract.Payload{}, internalError("persisted qualification result is malformed")
		}
		return s.completed(saved)
	}
	if row.State != "pending" {
		return contract.Payload{}, fault(contract.CodeConflict, "qualification %s is already %s", row.ID, row.State)
	}
	if in.Artifact.ID == "" || in.Artifact.Digest == "" || in.QualifiedAt.IsZero() ||
		in.AdapterVersion != "responses-adapter/v2" || in.SourceRevision == "" {
		return contract.Payload{}, invalidInput("qualification callback evidence is incomplete")
	}
	var candidate qualificationCandidate
	if err := contract.DecodeStrict([]byte(row.CandidateJSON), &candidate); err != nil {
		return contract.Payload{}, internalError("stored qualification candidate is malformed")
	}
	protocol := map[string]string{
		"openai":       "openai-openapi/2.3.0@ddface9b",
		"openrouter":   "openrouter-responses/stateless-v1",
		"experiential": "experiential-responses/stateless-v1",
	}
	provider := adapterProfileProvider(candidate.AdapterProfile)
	if protocol[provider] == "" || protocol[provider] != in.ProtocolRevision ||
		!sameStrings(in.Capabilities, []string{"responses.text_generation", "responses.single_request"}) ||
		!sameStrings(in.Limitations, []string{"Only the fixed qualification prompt was exercised; provider context-window and other model capabilities remain unqualified."}) {
		return contract.Payload{}, invalidInput("qualification metadata does not match the supported Responses adapter evidence")
	}
	if _, err := s.resolveQualificationConnection(ctx, unit, wireScope{InstallationID: installation}, candidate); err != nil {
		return contract.Payload{}, err
	}
	qualified, adapterDigest, err := qualifiedCandidate(candidate, *in, row.ProfileDigest)
	if err != nil {
		return contract.Payload{}, err
	}
	qualified.AdapterProfile = adapterDigest
	resource, err := json.Marshal(qualified)
	if err != nil {
		return contract.Payload{}, internalError("qualified profile could not be encoded")
	}
	if err := contract.ValidateSchema(withDefs(`{"$schema":"https://json-schema.org/draft/2020-12/schema","$ref":"#/$defs/QualifiedExecutionProfile"}`, defsRaw()), resource); err != nil {
		return contract.Payload{}, internalError("trusted qualified profile does not match its schema: %v", err)
	}
	result, err := json.Marshal(recordQualifiedExecutionProfileOutput{Resource: resource})
	if err != nil {
		return contract.Payload{}, internalError("qualified profile result could not be encoded")
	}
	canonical, err := contract.Canonicalize(result)
	if err != nil {
		return contract.Payload{}, internalError("qualified profile result could not be canonicalized")
	}
	res, err := unit.ExecContext(ctx, `UPDATE configuration_profile_qualifications SET state = 'succeeded', result_json = ?, updated_at = ?
		WHERE qualification_id = ? AND job_id = ? AND job_version = ? AND effect_operation_id = ? AND profile_digest = ? AND state = 'pending'`,
		string(canonical), s.clock.Now().UTC().Format(timeLayout), row.ID, in.JobID, in.ExpectedVersion, in.OperationID, in.ProfileDigest)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	if n != 1 {
		return contract.Payload{}, fault(contract.CodeConflict, "qualification candidate changed before completion")
	}
	return s.completed(recordQualifiedExecutionProfileOutput{Resource: resource})
}

func handleFinishQualifiedExecutionProfile(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[finishQualifiedExecutionProfileInput](s, inv.Operation, inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if in.State != "failed" && in.State != "outcome_unknown" {
		return contract.Payload{}, invalidInput("qualification finish accepts only failed or outcome_unknown")
	}
	if in.Generation != unit.Generation() {
		return contract.Payload{}, fault(contract.CodeConflict, "qualification completion generation %d is not current generation %d", in.Generation, unit.Generation())
	}
	var installation, qualificationID contract.ID
	err = unit.QueryRowContext(ctx, `SELECT installation_id, qualification_id
		FROM configuration_profile_qualifications WHERE job_id = ?`, in.JobID).Scan(&installation, &qualificationID)
	if err == sql.ErrNoRows {
		return contract.Payload{}, notFound("profile qualification job %s not found", in.JobID)
	}
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	if err := s.checkInstallation(unit, installation); err != nil {
		return contract.Payload{}, err
	}
	row, err := fetchQualification(ctx, unit, installation, qualificationID)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	if row == nil || row.JobID != in.JobID || row.JobVersion != in.ExpectedVersion ||
		row.EffectID != in.OperationID || row.ProfileDigest != in.ProfileDigest {
		return contract.Payload{}, fault(contract.CodeConflict, "qualification terminal callback does not match the exact job, effect and candidate")
	}
	if row.State == in.State {
		return s.completed(map[string]any{})
	}
	if row.State != "pending" {
		return contract.Payload{}, fault(contract.CodeConflict, "qualification %s is already %s", row.ID, row.State)
	}
	res, err := unit.ExecContext(ctx, `UPDATE configuration_profile_qualifications
		SET state = ?, updated_at = ?
		WHERE qualification_id = ? AND job_id = ? AND job_version = ?
		AND effect_operation_id = ? AND profile_digest = ? AND state = 'pending'`,
		in.State, s.clock.Now().UTC().Format(timeLayout), row.ID, in.JobID,
		in.ExpectedVersion, in.OperationID, in.ProfileDigest)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	if n != 1 {
		return contract.Payload{}, fault(contract.CodeConflict, "qualification candidate changed before terminal completion")
	}
	return s.completed(map[string]any{})
}

func qualifiedCandidate(candidate qualificationCandidate, in recordQualifiedExecutionProfileInput, candidateDigest string) (qualificationCandidate, json.RawMessage, error) {
	// Capability claims come exclusively from the observed adapter evidence;
	// discard any client-supplied draft claims before constructing the result.
	candidate.Capabilities = append([]string(nil), in.Capabilities...)
	var adapter map[string]any
	if err := json.Unmarshal(candidate.AdapterProfile, &adapter); err != nil || adapter == nil {
		return qualificationCandidate{}, nil, internalError("stored Responses profile draft is malformed")
	}
	enforcement, ok := adapter["enforcement"].(map[string]any)
	if !ok {
		return qualificationCandidate{}, nil, internalError("stored Responses profile draft has no enforcement settings")
	}
	evidence := map[string]any{
		"artifact": in.Artifact, "adapter_version": in.AdapterVersion,
		"source_revision": in.SourceRevision, "protocol_revision": in.ProtocolRevision,
		"profile_digest": candidateDigest, "qualified_at": in.QualifiedAt.UTC(),
		"capabilities": in.Capabilities, "limitations": in.Limitations,
	}
	enforcement["classifications"] = []string{candidate.Classification}
	enforcement["evidence"] = evidence
	adapter["enforcement"] = enforcement
	withoutTopEvidence, err := contract.Canonicalize([]byte(marshalJSON(adapter)))
	if err != nil {
		return qualificationCandidate{}, nil, internalError("qualified Responses profile could not be canonicalized")
	}
	adapterDigest := string(contract.Hash(withoutTopEvidence))
	topEvidence := make(map[string]any, len(evidence))
	for key, value := range evidence {
		topEvidence[key] = value
	}
	topEvidence["profile_digest"] = adapterDigest
	adapter["capability_evidence"] = topEvidence
	canonical, err := contract.Canonicalize([]byte(marshalJSON(adapter)))
	if err != nil {
		return qualificationCandidate{}, nil, internalError("qualified Responses profile with evidence could not be canonicalized")
	}
	return candidate, canonical, nil
}

func adapterProfileProvider(raw json.RawMessage) string {
	var profile struct {
		Provider string `json:"provider"`
	}
	_ = json.Unmarshal(raw, &profile)
	return profile.Provider
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
