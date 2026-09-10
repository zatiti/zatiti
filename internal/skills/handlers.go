package skills

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Public operation handlers. Every handler decodes through the operation
// schema first, enforces the installation scope fence and serves only data
// the caller's scope already names — a missing resource is not_found, never
// a cross-scope disclosure.

// handleGet serves skill.get: one skill identity's latest version.
func handleGet(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[skillGetInput](s, "skill.get", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := checkInstallation(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	installation := unit.Scope().InstallationID
	row, err := fetchSkillByID(ctx, unit, installation, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if row == nil {
		return contract.Payload{}, notFound("skill %s was not found", in.ID)
	}
	resource, err := wireSkillOf(ctx, unit, row)
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(skillOutput{Resource: resource})
}

// listFilterNorm is the normalized filter state bound into list cursors, so
// a cursor minted under one filter can never serve another.
type listFilterNorm struct {
	State string `json:"state,omitempty"`
}

// normalizeListFilter validates the structured list filter. Filters are
// exact-match fields with AND semantics; any field the skills resource does
// not support refuses the request as invalid_input rather than being
// ignored, and filter values are bound as SQL parameters, never interpolated.
func normalizeListFilter(in *skillListInput) (listFilterNorm, error) {
	norm := listFilterNorm{}
	if in.Filter == nil {
		return norm, nil
	}
	f := in.Filter
	supported := []string{"state"}
	if f.State != nil {
		state := strings.ToLower(*f.State)
		switch state {
		case "draft", "active", "archived":
			norm.State = state
		default:
			return norm, invalidInput("filter state must be one of draft, active, archived")
		}
	}
	for field, present := range map[string]bool{
		"key":             f.Key != nil,
		"parent_id":       f.ParentID != nil,
		"worker_id":       f.WorkerID != nil,
		"task_id":         f.TaskID != nil,
		"organization_id": f.OrganizationID != nil,
		"descendants":     f.Descendants != nil,
		"needs_you":       f.NeedsYou != nil,
	} {
		if present {
			return norm, invalidInput("filter field %q is not supported for skills; supported fields: %s", field, strings.Join(supported, ", "))
		}
	}
	return norm, nil
}

// handleList serves skill.list: a keyset page over skill versions ordered
// by (name, version), filterable by state, with HMAC-sealed cursors.
func handleList(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[skillListInput](s, "skill.list", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := checkInstallation(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	norm, err := normalizeListFilter(in)
	if err != nil {
		return contract.Payload{}, err
	}
	limit := limitOf(valueOf(in.Limit))

	installation := unit.Scope().InstallationID
	query := `
		SELECT id, version, installation_id, name, description, state,
		       content_digest, instruction_digest, instruction_artifact_id,
		       manifest_json, requirements_json, dependencies_json,
		       input_schema_json, output_schema_json, source, license,
		       created_at, updated_at
		FROM skills_versions
		WHERE installation_id = ?`
	args := []any{string(installation)}
	if norm.State != "" {
		query += ` AND state = ?`
		args = append(args, norm.State)
	}
	if in.Cursor != nil {
		lastName, lastVersion, err := s.readCursor("skill.list", unit.Scope(), norm, *in.Cursor)
		if err != nil {
			return contract.Payload{}, err
		}
		query += ` AND (name > ? OR (name = ? AND version > ?))`
		args = append(args, lastName, lastName, lastVersion)
	}
	query += ` ORDER BY name, version LIMIT ?`
	args = append(args, limit+1)

	rows, err := querySkills(ctx, unit, query, args...)
	if err != nil {
		return contract.Payload{}, err
	}
	next := (*string)(nil)
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		cursor, err := s.mintCursor("skill.list", unit.Scope(), norm, last.Name, last.Version, s.clock.Now())
		if err != nil {
			return contract.Payload{}, err
		}
		next = &cursor
	}
	items := make([]wireSkill, 0, len(rows))
	for _, row := range rows {
		resource, err := wireSkillOf(ctx, unit, row)
		if err != nil {
			return contract.Payload{}, err
		}
		items = append(items, resource)
	}
	return listResult(listItems{Items: items}, next)
}

// listItems is the wire list page body.
type listItems struct {
	Items []wireSkill `json:"items"`
}

// valueOf dereferences an optional int64 input field.
func valueOf(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// handleArchive serves skill.archive: stages an archive change for one
// exact skill version into the configuration compiler. It never activates
// anything directly.
func handleArchive(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[skillArchiveInput](s, "skill.archive", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := checkInstallation(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	installation := unit.Scope().InstallationID
	row, err := fetchSkillVersion(ctx, unit, installation, in.ID, int64(in.ExpectedVersion))
	if err != nil {
		return contract.Payload{}, err
	}
	if row == nil {
		return contract.Payload{}, notFound("skill %s version %d was not found", in.ID, in.ExpectedVersion)
	}
	if row.State == "archived" {
		return contract.Payload{}, conflictFault("skill %s version %d is already archived", in.ID, in.ExpectedVersion)
	}
	resource, err := wireSkillOf(ctx, unit, row)
	if err != nil {
		return contract.Payload{}, err
	}
	stageIn := map[string]any{
		"scope": in.Scope.toContract(),
		"change": map[string]any{
			"kind":             "skill",
			"action":           "archive",
			"id":               in.ID,
			"expected_version": in.ExpectedVersion,
			"definition":       resource,
		},
	}
	if in.DraftID != nil {
		stageIn["draft_id"] = *in.DraftID
	}
	stageOut, err := s.callOwner(ctx, unit, "_configuration.stage", stageIn)
	if err != nil {
		return contract.Payload{}, err
	}
	var staged struct {
		Resource wireDraft `json:"resource"`
	}
	if err := json.Unmarshal(stageOut, &staged); err != nil {
		return contract.Payload{}, faultWrap(internalError("draft staging decoding failed"), err)
	}
	return s.completed(archiveOutput{Draft: staged.Resource, Resource: resource})
}

// handleEvaluate serves skill.evaluate: seals an evaluation record with the
// pinned verifier, acceptance and limits, then creates the execution job
// that the controller drives. The evaluation state is pending here; its
// outcome is recorded downstream, never self-declared.
func handleEvaluate(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[evaluateInput](s, "skill.evaluate", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := checkInstallation(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	installation := unit.Scope().InstallationID

	// The evaluated skill must exist at the pinned exact version and be
	// evaluatable: an archived version is retired, not evaluable.
	row, err := fetchSkillVersion(ctx, unit, installation, in.Skill.ID, int64(in.Skill.Version))
	if err != nil {
		return contract.Payload{}, err
	}
	if row == nil {
		return contract.Payload{}, notFound("skill %s version %d was not found", in.Skill.ID, in.Skill.Version)
	}
	if row.State == "archived" {
		return contract.Payload{}, conflictFault("skill %s version %d is archived and cannot be evaluated", in.Skill.ID, in.Skill.Version)
	}

	if err := validateAcceptance(ctx, s, unit, in.Scope, &in.Acceptance); err != nil {
		return contract.Payload{}, err
	}
	if err := validateLimits(s, &in.Limits); err != nil {
		return contract.Payload{}, err
	}

	acceptanceJSON, err := marshalJSON(in.Acceptance)
	if err != nil {
		return contract.Payload{}, err
	}
	profileJSON, err := marshalJSON(in.Profile)
	if err != nil {
		return contract.Payload{}, err
	}
	limitsJSON, err := marshalJSON(in.Limits)
	if err != nil {
		return contract.Payload{}, err
	}

	evaluationID := s.ids.New()
	now := s.clock.Now().UTC()
	if _, err := unit.ExecContext(ctx, `
		INSERT INTO skills_evaluations
			(id, version, installation_id, skill_id, skill_version, state,
			 verifier_id, verifier_version, mode,
			 acceptance_json, profile_json, limits_json,
			 observations_json, evidence_json, job_id, job_json,
			 created_at, updated_at)
		VALUES (?, 1, ?, ?, ?, 'pending', ?, ?, ?, ?, ?, ?, NULL, NULL, '', '', ?, ?)`,
		string(evaluationID), string(installation), string(in.Skill.ID), int64(in.Skill.Version),
		in.Acceptance.VerifierID, in.Acceptance.VerifierVersion, in.Acceptance.Mode,
		acceptanceJSON, profileJSON, limitsJSON,
		now.Format(timeLayout), now.Format(timeLayout)); err != nil {
		return contract.Payload{}, faultWrap(internalError("evaluation persistence failed"), err)
	}

	// The execution job input is inert data: identity, acceptance, posture
	// and bounds for the controller, no authority of any kind.
	jobInput := map[string]any{
		"evaluation_id": evaluationID,
		"skill":         in.Skill,
		"acceptance":    in.Acceptance,
		"profile":       in.Profile,
		"limits":        in.Limits,
	}
	jobOut, err := s.callOwner(ctx, unit, "_execution.job.create", map[string]any{
		"scope":     in.Scope.toContract(),
		"owner":     ownerName,
		"operation": "skill.evaluate",
		"input":     jobInput,
		"source_id": evaluationID,
	})
	if err != nil {
		return contract.Payload{}, err
	}
	var jobCreated struct {
		Resource wireJob `json:"resource"`
	}
	if err := json.Unmarshal(jobOut, &jobCreated); err != nil {
		return contract.Payload{}, faultWrap(internalError("job creation decoding failed"), err)
	}
	jobJSON, err := marshalJSON(jobCreated.Resource)
	if err != nil {
		return contract.Payload{}, err
	}
	if _, err := unit.ExecContext(ctx, `
		UPDATE skills_evaluations
		SET job_id = ?, job_json = ?, updated_at = ?
		WHERE id = ? AND installation_id = ?`,
		string(jobCreated.Resource.ID), jobJSON, now.Format(timeLayout),
		string(evaluationID), string(installation)); err != nil {
		return contract.Payload{}, faultWrap(internalError("evaluation persistence failed"), err)
	}

	if err := s.emitEvaluationCreated(ctx, unit, evaluationID, 1, in.Skill.ID, int64(in.Skill.Version)); err != nil {
		return contract.Payload{}, err
	}

	resource := wireJobOf(evaluationRow{
		ID:           evaluationID,
		Version:      1,
		State:        "pending",
		JobID:        jobCreated.Resource.ID,
		EvidenceJSON: "",
	})
	return s.completed(jobOutput{Resource: resource})
}

// validateAcceptance checks the semantic constraints the input schema
// cannot express: a pinned verifier identity, a known acceptance mode and
// sealed inputs that resolve to available artifacts at the pinned digests.
func validateAcceptance(ctx context.Context, s *Service, unit contract.Unit, scope wireScope, acceptance *wireAcceptance) error {
	if acceptance.VerifierID == "" || acceptance.VerifierVersion == "" {
		return invalidInput("acceptance requires a pinned verifier identity and version")
	}
	switch acceptance.Mode {
	case "independent", "manual":
	default:
		return invalidInput("acceptance mode must be independent or manual")
	}
	if len(acceptance.SealedInputs) == 0 {
		return nil
	}
	refs := make([]wireArtifactRef, 0, len(acceptance.SealedInputs))
	for _, ref := range acceptance.SealedInputs {
		refs = append(refs, wireArtifactRef{ID: ref.ID, Digest: ref.Digest})
	}
	meta, err := s.callOwner(ctx, unit, "_artifacts.metadata", map[string]any{
		"scope":     scope.toContract(),
		"artifacts": refs,
	})
	if err != nil {
		return err
	}
	var listed struct {
		Artifacts []struct {
			ID     contract.ID     `json:"id"`
			Digest contract.Digest `json:"digest"`
			State  string          `json:"state"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(meta, &listed); err != nil {
		return faultWrap(internalError("artifact metadata decoding failed"), err)
	}
	byID := make(map[contract.ID]struct {
		Digest contract.Digest
		State  string
	}, len(listed.Artifacts))
	for _, a := range listed.Artifacts {
		byID[a.ID] = struct {
			Digest contract.Digest
			State  string
		}{a.Digest, a.State}
	}
	for _, ref := range acceptance.SealedInputs {
		a, ok := byID[ref.ID]
		if !ok {
			return notFound("sealed input artifact %s was not found", ref.ID)
		}
		if a.State != "available" {
			return conflictFault("sealed input artifact %s is %s, not available", ref.ID, a.State)
		}
		if a.Digest != ref.Digest {
			return conflictFault("sealed input artifact %s digest does not match the pinned digest", ref.ID)
		}
	}
	return nil
}

// validateLimits checks the evaluation bounds: the root deadline must parse
// and remain in the future at submission time.
func validateLimits(s *Service, limits *wireLimits) error {
	if limits.RootDeadline == "" {
		return nil
	}
	deadline, err := time.Parse(time.RFC3339, limits.RootDeadline)
	if err != nil {
		return invalidInput("limits root_deadline must be an RFC3339 UTC timestamp")
	}
	if !deadline.After(s.clock.Now()) {
		return invalidInput("limits root_deadline must be in the future")
	}
	if limits.SpendMicroUnits < 0 || limits.Concurrency < 0 || limits.ModelSteps < 0 ||
		limits.ChildCount < 0 || limits.DelegationDepth < 0 || limits.AttemptSeconds < 0 {
		return invalidInput("limits must be non-negative")
	}
	return nil
}

// wireJobOf presents one evaluation record as the job resource the wire
// carries. State and evidence come from the skills-owned row; operation_id
// links the execution-side job the controller drives.
func wireJobOf(row evaluationRow) wireJob {
	resource := wireJob{
		ID:           row.ID,
		Version:      contract.Version(row.Version),
		Kind:         "skill.evaluation",
		State:        row.State,
		Requirements: []wireRequirement{},
		OperationID:  row.JobID,
		Owner:        ownerName,
		Operation:    "skill.evaluate",
	}
	if row.EvidenceJSON != "" {
		resource.Result = json.RawMessage(row.EvidenceJSON)
	}
	return resource
}

// handleEvaluationStatus serves skill.evaluation.status: the stored
// evaluation disposition and retained evidence. It never invents an
// outcome; until one is recorded downstream the state stays pending.
func handleEvaluationStatus(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[skillGetInput](s, "skill.evaluation.status", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := checkInstallation(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	row, err := fetchEvaluation(ctx, unit, unit.Scope().InstallationID, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if row == nil {
		return contract.Payload{}, notFound("evaluation %s was not found", in.ID)
	}
	return s.completed(jobOutput{Resource: wireJobOf(*row)})
}
