package policy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Stored-row access. Every query pins the installation id; scope dimension
// columns back filtered reads and scope-containment decisions, while the
// canonical scope JSON snapshot is the wire source of truth. Timestamps are
// stored as UTC RFC3339Nano strings so keyset pagination is lexical and
// stable.

// expectOneRow asserts that one exactly-matched write landed; zero rows
// means a concurrent writer moved the row first.
func expectOneRow(res sql.Result, kind string, id contract.ID) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("policy: %s %s write: %w", kind, id, err)
	}
	if n != 1 {
		return conflict("%s %s changed concurrently; retry with the current version", kind, id)
	}
	return nil
}

// Policies.

const policyColumns = `id, version, scope_json, rules_json, extensions_json, archived, created_at, updated_at`

// scanPolicy reads one policy_policies row.
func scanPolicy(scan func(dest ...any) error) (policyRow, error) {
	var (
		r                    policyRow
		scopeJSON            string
		rulesJSON            string
		extensions           sql.NullString
		archivedInt          int64
		createdAt, updatedAt string
	)
	if err := scan(&r.ID, &r.Version, &scopeJSON, &rulesJSON, &extensions, &archivedInt, &createdAt, &updatedAt); err != nil {
		return policyRow{}, err
	}
	scope, err := decodeScope(scopeJSON)
	if err != nil {
		return policyRow{}, err
	}
	rules, err := decodeRules(rulesJSON)
	if err != nil {
		return policyRow{}, err
	}
	created, err := parseStamp(createdAt)
	if err != nil {
		return policyRow{}, err
	}
	updated, err := parseStamp(updatedAt)
	if err != nil {
		return policyRow{}, err
	}
	r.Scope = scope
	r.Rules = rules
	if extensions.Valid {
		r.Extensions = json.RawMessage(extensions.String)
	}
	r.Archived = archivedInt == 1
	r.CreatedAt, r.UpdatedAt = created, updated
	return r, nil
}

// loadPolicyRow reads one policy by id; found is false when absent.
func (s *Service) loadPolicyRow(ctx context.Context, unit contract.Unit, id contract.ID) (policyRow, bool, error) {
	row := unit.QueryRowContext(ctx, `
		SELECT `+policyColumns+`
		FROM policy_policies
		WHERE installation_id = ? AND id = ?`,
		string(unit.Scope().InstallationID), string(id))
	r, err := scanPolicy(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return policyRow{}, false, nil
	}
	if err != nil {
		return policyRow{}, false, fmt.Errorf("policy: load policy %s: %w", id, err)
	}
	return r, true, nil
}

// loadActivePolicies reads every non-archived policy of the installation in
// stable (created_at, id) order; the intersection engine depends on that
// order for deterministic deny reasons.
func (s *Service) loadActivePolicies(ctx context.Context, unit contract.Unit, installation contract.ID) ([]policyRow, error) {
	rows, err := unit.QueryContext(ctx, `
		SELECT `+policyColumns+`
		FROM policy_policies
		WHERE installation_id = ? AND archived = 0
		ORDER BY created_at, id`,
		string(installation))
	if err != nil {
		return nil, fmt.Errorf("policy: list active policies: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []policyRow{}
	for rows.Next() {
		r, err := scanPolicy(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("policy: iterate active policies: %w", err)
	}
	return out, nil
}

// insertPolicy writes one new policy row at its declared version.
func (s *Service) insertPolicy(ctx context.Context, unit contract.Unit, p policyRow) error {
	scopeJSON, err := encodeScope(p.Scope)
	if err != nil {
		return err
	}
	rulesJSON, err := encodeRules(p.Rules)
	if err != nil {
		return err
	}
	var extensions any
	if p.Extensions != nil {
		extensions = string(p.Extensions)
	}
	_, err = unit.ExecContext(ctx, `
		INSERT INTO policy_policies
			(id, version, installation_id, organization_id, project_id, worker_id, task_id,
			 scope_json, rules_json, extensions_json, archived, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(p.ID), p.Version,
		string(p.Scope.InstallationID), string(p.Scope.OrganizationID), string(p.Scope.ProjectID),
		string(p.Scope.WorkerID), string(p.Scope.TaskID),
		scopeJSON, rulesJSON, extensions, boolInt(p.Archived), formatStamp(p.CreatedAt), formatStamp(p.UpdatedAt))
	if err != nil {
		return fmt.Errorf("policy: insert policy %s: %w", p.ID, err)
	}
	return nil
}

// updatePolicyRow overwrites one existing policy row under a version fence.
func (s *Service) updatePolicyRow(ctx context.Context, unit contract.Unit, p policyRow) error {
	scopeJSON, err := encodeScope(p.Scope)
	if err != nil {
		return err
	}
	rulesJSON, err := encodeRules(p.Rules)
	if err != nil {
		return err
	}
	var extensions any
	if p.Extensions != nil {
		extensions = string(p.Extensions)
	}
	res, err := unit.ExecContext(ctx, `
		UPDATE policy_policies
		SET version = ?, organization_id = ?, project_id = ?, worker_id = ?, task_id = ?,
		    scope_json = ?, rules_json = ?, extensions_json = ?, archived = ?, updated_at = ?
		WHERE id = ? AND installation_id = ? AND version = ?`,
		p.Version, string(p.Scope.OrganizationID), string(p.Scope.ProjectID),
		string(p.Scope.WorkerID), string(p.Scope.TaskID),
		scopeJSON, rulesJSON, extensions, boolInt(p.Archived), formatStamp(p.UpdatedAt),
		string(p.ID), string(p.Scope.InstallationID), p.Version-1)
	if err != nil {
		return fmt.Errorf("policy: update policy %s: %w", p.ID, err)
	}
	return expectOneRow(res, "policy", p.ID)
}

// listPolicies pages policies with an optional organization filter and
// keyset position.
func (s *Service) listPolicies(ctx context.Context, unit contract.Unit, organization string, keyset string, keysetArgs []any, limit int64) ([]policyRow, error) {
	where := "installation_id = ?"
	args := []any{string(unit.Scope().InstallationID)}
	if organization != "" {
		where += " AND organization_id = ?"
		args = append(args, organization)
	}
	where += keyset
	args = append(args, keysetArgs...)
	args = append(args, limit)
	rows, err := unit.QueryContext(ctx, `
		SELECT `+policyColumns+`
		FROM policy_policies
		WHERE `+where+`
		ORDER BY created_at, id
		LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("policy: list policies: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []policyRow{}
	for rows.Next() {
		r, err := scanPolicy(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("policy: iterate policies: %w", err)
	}
	return out, nil
}

// Promotion rules.

const ruleColumns = `id, version, scope_json, capability, destinations_json, required_evidence_json,
	minimum_successes, evidence_window_seconds, disqualifying_events_json, ceiling_grant_id,
	human_required_preserved, archived, created_at, updated_at`

// scanPromotionRule reads one policy_promotion_rules row.
func scanPromotionRule(scan func(dest ...any) error) (promotionRuleRow, error) {
	var (
		r                    promotionRuleRow
		scopeJSON            string
		destJSON             string
		evidenceJSON         string
		disqualifyJSON       string
		preservedInt         int64
		archivedInt          int64
		createdAt, updatedAt string
	)
	if err := scan(&r.ID, &r.Version, &scopeJSON, &r.Capability, &destJSON, &evidenceJSON,
		&r.MinimumSuccesses, &r.EvidenceWindowSeconds, &disqualifyJSON, &r.CeilingGrantID,
		&preservedInt, &archivedInt, &createdAt, &updatedAt); err != nil {
		return promotionRuleRow{}, err
	}
	scope, err := decodeScope(scopeJSON)
	if err != nil {
		return promotionRuleRow{}, err
	}
	dests, err := decodeStrings(destJSON)
	if err != nil {
		return promotionRuleRow{}, err
	}
	evidence, err := decodeStrings(evidenceJSON)
	if err != nil {
		return promotionRuleRow{}, err
	}
	disqualifying, err := decodeStrings(disqualifyJSON)
	if err != nil {
		return promotionRuleRow{}, err
	}
	created, err := parseStamp(createdAt)
	if err != nil {
		return promotionRuleRow{}, err
	}
	updated, err := parseStamp(updatedAt)
	if err != nil {
		return promotionRuleRow{}, err
	}
	r.Scope = scope
	r.Destinations = dests
	r.RequiredEvidence = evidence
	r.DisqualifyingEvents = disqualifying
	r.HumanRequiredPreserved = preservedInt == 1
	r.Archived = archivedInt == 1
	r.CreatedAt, r.UpdatedAt = created, updated
	return r, nil
}

// loadRuleRow reads one promotion rule by id; found is false when absent.
func (s *Service) loadRuleRow(ctx context.Context, unit contract.Unit, id contract.ID) (promotionRuleRow, bool, error) {
	row := unit.QueryRowContext(ctx, `
		SELECT `+ruleColumns+`
		FROM policy_promotion_rules
		WHERE installation_id = ? AND id = ?`,
		string(unit.Scope().InstallationID), string(id))
	r, err := scanPromotionRule(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return promotionRuleRow{}, false, nil
	}
	if err != nil {
		return promotionRuleRow{}, false, fmt.Errorf("policy: load promotion rule %s: %w", id, err)
	}
	return r, true, nil
}

// insertRule writes one new promotion rule row.
func (s *Service) insertRule(ctx context.Context, unit contract.Unit, r promotionRuleRow) error {
	scopeJSON, err := encodeScope(r.Scope)
	if err != nil {
		return err
	}
	destJSON, err := encodeStrings(r.Destinations)
	if err != nil {
		return err
	}
	evidenceJSON, err := encodeStrings(r.RequiredEvidence)
	if err != nil {
		return err
	}
	disqualifyJSON, err := encodeStrings(r.DisqualifyingEvents)
	if err != nil {
		return err
	}
	_, err = unit.ExecContext(ctx, `
		INSERT INTO policy_promotion_rules
			(id, version, installation_id, organization_id, project_id, worker_id, task_id,
			 scope_json, capability, destinations_json, required_evidence_json,
			 minimum_successes, evidence_window_seconds, disqualifying_events_json,
			 ceiling_grant_id, human_required_preserved, archived, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(r.ID), r.Version,
		string(r.Scope.InstallationID), string(r.Scope.OrganizationID), string(r.Scope.ProjectID),
		string(r.Scope.WorkerID), string(r.Scope.TaskID),
		scopeJSON, r.Capability, destJSON, evidenceJSON,
		r.MinimumSuccesses, r.EvidenceWindowSeconds, disqualifyJSON,
		string(r.CeilingGrantID), boolInt(r.HumanRequiredPreserved), boolInt(r.Archived),
		formatStamp(r.CreatedAt), formatStamp(r.UpdatedAt))
	if err != nil {
		return fmt.Errorf("policy: insert promotion rule %s: %w", r.ID, err)
	}
	return nil
}

// updateRuleRow overwrites one existing promotion rule row under a version
// fence.
func (s *Service) updateRuleRow(ctx context.Context, unit contract.Unit, r promotionRuleRow) error {
	scopeJSON, err := encodeScope(r.Scope)
	if err != nil {
		return err
	}
	destJSON, err := encodeStrings(r.Destinations)
	if err != nil {
		return err
	}
	evidenceJSON, err := encodeStrings(r.RequiredEvidence)
	if err != nil {
		return err
	}
	disqualifyJSON, err := encodeStrings(r.DisqualifyingEvents)
	if err != nil {
		return err
	}
	res, err := unit.ExecContext(ctx, `
		UPDATE policy_promotion_rules
		SET version = ?, organization_id = ?, project_id = ?, worker_id = ?, task_id = ?,
		    scope_json = ?, capability = ?, destinations_json = ?, required_evidence_json = ?,
		    minimum_successes = ?, evidence_window_seconds = ?, disqualifying_events_json = ?,
		    ceiling_grant_id = ?, human_required_preserved = ?, archived = ?, updated_at = ?
		WHERE id = ? AND installation_id = ? AND version = ?`,
		r.Version, string(r.Scope.OrganizationID), string(r.Scope.ProjectID),
		string(r.Scope.WorkerID), string(r.Scope.TaskID),
		scopeJSON, r.Capability, destJSON, evidenceJSON,
		r.MinimumSuccesses, r.EvidenceWindowSeconds, disqualifyJSON,
		string(r.CeilingGrantID), boolInt(r.HumanRequiredPreserved), boolInt(r.Archived),
		formatStamp(r.UpdatedAt),
		string(r.ID), string(r.Scope.InstallationID), r.Version-1)
	if err != nil {
		return fmt.Errorf("policy: update promotion rule %s: %w", r.ID, err)
	}
	return expectOneRow(res, "promotion rule", r.ID)
}

// listRules pages promotion rules with an optional organization filter and
// keyset position.
func (s *Service) listRules(ctx context.Context, unit contract.Unit, organization string, keyset string, keysetArgs []any, limit int64) ([]promotionRuleRow, error) {
	where := "installation_id = ?"
	args := []any{string(unit.Scope().InstallationID)}
	if organization != "" {
		where += " AND organization_id = ?"
		args = append(args, organization)
	}
	where += keyset
	args = append(args, keysetArgs...)
	args = append(args, limit)
	rows, err := unit.QueryContext(ctx, `
		SELECT `+ruleColumns+`
		FROM policy_promotion_rules
		WHERE `+where+`
		ORDER BY created_at, id
		LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("policy: list promotion rules: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []promotionRuleRow{}
	for rows.Next() {
		r, err := scanPromotionRule(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("policy: iterate promotion rules: %w", err)
	}
	return out, nil
}

// Qualifications.

const qualificationColumns = `id, version, scope_json, worker_ref, capability, destinations_json,
	rule_id, rule_version, model, tool_versions_json, skill_versions_json, evidence_ids_json,
	window_start, window_end, state, explanation, created_at, updated_at`

// scanQualification reads one policy_qualifications row.
func scanQualification(scan func(dest ...any) error) (qualificationRow, error) {
	var (
		r                    qualificationRow
		scopeJSON            string
		destJSON             string
		toolJSON             string
		skillJSON            string
		evidenceJSON         string
		windowStart          string
		windowEnd            string
		createdAt, updatedAt string
	)
	if err := scan(&r.ID, &r.Version, &scopeJSON, &r.WorkerID, &r.Capability, &destJSON,
		&r.RuleID, &r.RuleVersion, &r.Model, &toolJSON, &skillJSON, &evidenceJSON,
		&windowStart, &windowEnd, &r.State, &r.Explanation, &createdAt, &updatedAt); err != nil {
		return qualificationRow{}, err
	}
	scope, err := decodeScope(scopeJSON)
	if err != nil {
		return qualificationRow{}, err
	}
	dests, err := decodeStrings(destJSON)
	if err != nil {
		return qualificationRow{}, err
	}
	tools, err := decodeRefs(toolJSON)
	if err != nil {
		return qualificationRow{}, err
	}
	skills, err := decodeRefs(skillJSON)
	if err != nil {
		return qualificationRow{}, err
	}
	evidence, err := decodeIDs(evidenceJSON)
	if err != nil {
		return qualificationRow{}, err
	}
	start, err := parseStamp(windowStart)
	if err != nil {
		return qualificationRow{}, err
	}
	end, err := parseStamp(windowEnd)
	if err != nil {
		return qualificationRow{}, err
	}
	created, err := parseStamp(createdAt)
	if err != nil {
		return qualificationRow{}, err
	}
	updated, err := parseStamp(updatedAt)
	if err != nil {
		return qualificationRow{}, err
	}
	r.Scope = scope
	r.Destinations = dests
	r.ToolVersions = tools
	r.SkillVersions = skills
	r.EvidenceIDs = evidence
	r.WindowStart, r.WindowEnd = start, end
	r.CreatedAt, r.UpdatedAt = created, updated
	return r, nil
}

// loadQualificationRow reads one qualification by id; found is false when
// absent.
func (s *Service) loadQualificationRow(ctx context.Context, unit contract.Unit, id contract.ID) (qualificationRow, bool, error) {
	row := unit.QueryRowContext(ctx, `
		SELECT `+qualificationColumns+`
		FROM policy_qualifications
		WHERE installation_id = ? AND id = ?`,
		string(unit.Scope().InstallationID), string(id))
	r, err := scanQualification(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return qualificationRow{}, false, nil
	}
	if err != nil {
		return qualificationRow{}, false, fmt.Errorf("policy: load qualification %s: %w", id, err)
	}
	return r, true, nil
}

// insertQualification writes one new qualification row.
func (s *Service) insertQualification(ctx context.Context, unit contract.Unit, q qualificationRow) error {
	scopeJSON, err := encodeScope(q.Scope)
	if err != nil {
		return err
	}
	destJSON, err := encodeStrings(q.Destinations)
	if err != nil {
		return err
	}
	toolJSON, err := encodeRefs(q.ToolVersions)
	if err != nil {
		return err
	}
	skillJSON, err := encodeRefs(q.SkillVersions)
	if err != nil {
		return err
	}
	evidenceJSON, err := encodeIDs(q.EvidenceIDs)
	if err != nil {
		return err
	}
	_, err = unit.ExecContext(ctx, `
		INSERT INTO policy_qualifications
			(id, version, installation_id, organization_id, project_id, worker_id, task_id,
			 scope_json, worker_ref, capability, destinations_json, rule_id, rule_version,
			 model, tool_versions_json, skill_versions_json, evidence_ids_json,
			 window_start, window_end, state, explanation, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(q.ID), q.Version,
		string(q.Scope.InstallationID), string(q.Scope.OrganizationID), string(q.Scope.ProjectID),
		string(q.Scope.WorkerID), string(q.Scope.TaskID),
		scopeJSON, string(q.WorkerID), q.Capability, destJSON,
		string(q.RuleID), q.RuleVersion, q.Model, toolJSON, skillJSON, evidenceJSON,
		formatStamp(q.WindowStart), formatStamp(q.WindowEnd), q.State, q.Explanation,
		formatStamp(q.CreatedAt), formatStamp(q.UpdatedAt))
	if err != nil {
		return fmt.Errorf("policy: insert qualification %s: %w", q.ID, err)
	}
	return nil
}

// updateQualificationRow overwrites one existing qualification row under a
// version fence.
func (s *Service) updateQualificationRow(ctx context.Context, unit contract.Unit, q qualificationRow) error {
	scopeJSON, err := encodeScope(q.Scope)
	if err != nil {
		return err
	}
	destJSON, err := encodeStrings(q.Destinations)
	if err != nil {
		return err
	}
	toolJSON, err := encodeRefs(q.ToolVersions)
	if err != nil {
		return err
	}
	skillJSON, err := encodeRefs(q.SkillVersions)
	if err != nil {
		return err
	}
	evidenceJSON, err := encodeIDs(q.EvidenceIDs)
	if err != nil {
		return err
	}
	res, err := unit.ExecContext(ctx, `
		UPDATE policy_qualifications
		SET version = ?, organization_id = ?, project_id = ?, worker_id = ?, task_id = ?,
		    scope_json = ?, worker_ref = ?, capability = ?, destinations_json = ?,
		    rule_id = ?, rule_version = ?, model = ?, tool_versions_json = ?,
		    skill_versions_json = ?, evidence_ids_json = ?, window_start = ?, window_end = ?,
		    state = ?, explanation = ?, updated_at = ?
		WHERE id = ? AND installation_id = ? AND version = ?`,
		q.Version, string(q.Scope.OrganizationID), string(q.Scope.ProjectID),
		string(q.Scope.WorkerID), string(q.Scope.TaskID),
		scopeJSON, string(q.WorkerID), q.Capability, destJSON,
		string(q.RuleID), q.RuleVersion, q.Model, toolJSON, skillJSON, evidenceJSON,
		formatStamp(q.WindowStart), formatStamp(q.WindowEnd), q.State, q.Explanation,
		formatStamp(q.UpdatedAt),
		string(q.ID), string(q.Scope.InstallationID), q.Version-1)
	if err != nil {
		return fmt.Errorf("policy: update qualification %s: %w", q.ID, err)
	}
	return expectOneRow(res, "qualification", q.ID)
}

// loadOpenQualifications reads every proposed or qualified qualification of
// the installation; invalidation matching runs over this set in Go.
func (s *Service) loadOpenQualifications(ctx context.Context, unit contract.Unit) ([]qualificationRow, error) {
	rows, err := unit.QueryContext(ctx, `
		SELECT `+qualificationColumns+`
		FROM policy_qualifications
		WHERE installation_id = ? AND state IN ('proposed', 'qualified')
		ORDER BY created_at, id`,
		string(unit.Scope().InstallationID))
	if err != nil {
		return nil, fmt.Errorf("policy: list open qualifications: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []qualificationRow{}
	for rows.Next() {
		r, err := scanQualification(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("policy: iterate open qualifications: %w", err)
	}
	return out, nil
}

// listQualifications pages qualifications with structured filters and a
// keyset position.
func (s *Service) listQualifications(ctx context.Context, unit contract.Unit, state, worker, organization string, keyset string, keysetArgs []any, limit int64) ([]qualificationRow, error) {
	where := "installation_id = ?"
	args := []any{string(unit.Scope().InstallationID)}
	if state != "" {
		where += " AND state = ?"
		args = append(args, state)
	}
	if worker != "" {
		where += " AND worker_ref = ?"
		args = append(args, worker)
	}
	if organization != "" {
		where += " AND organization_id = ?"
		args = append(args, organization)
	}
	where += keyset
	args = append(args, keysetArgs...)
	args = append(args, limit)
	rows, err := unit.QueryContext(ctx, `
		SELECT `+qualificationColumns+`
		FROM policy_qualifications
		WHERE `+where+`
		ORDER BY created_at, id
		LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("policy: list qualifications: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []qualificationRow{}
	for rows.Next() {
		r, err := scanQualification(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("policy: iterate qualifications: %w", err)
	}
	return out, nil
}

// Evidence links.

// insertEvidence records one immutable first-observation link. The
// (qualification, evidence) pair is the primary key: a repeated observation
// of the same evidence inside one qualification is a programming error and
// fails the transaction.
func (s *Service) insertEvidence(ctx context.Context, unit contract.Unit, e evidenceRow) error {
	var succeededAt any
	if e.SucceededAt != nil {
		succeededAt = formatStamp(*e.SucceededAt)
	}
	var succeededVersion any
	if e.SucceededVersion != nil {
		succeededVersion = *e.SucceededVersion
	}
	_, err := unit.ExecContext(ctx, `
		INSERT INTO policy_evidence
			(qualification_id, evidence_id, kind, first_state, first_version, first_at,
			 succeeded_at, succeeded_version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		string(e.QualificationID), string(e.EvidenceID), e.Kind, e.FirstState, e.FirstVersion,
		formatStamp(e.FirstAt), succeededAt, succeededVersion)
	if err != nil {
		return fmt.Errorf("policy: insert evidence %s for qualification %s: %w", e.EvidenceID, e.QualificationID, err)
	}
	return nil
}

// loadEvidence reads every evidence link of one qualification in insertion
// (evidence id) order.
func (s *Service) loadEvidence(ctx context.Context, unit contract.Unit, qualification contract.ID) ([]evidenceRow, error) {
	rows, err := unit.QueryContext(ctx, `
		SELECT qualification_id, evidence_id, kind, first_state, first_version, first_at,
		       succeeded_at, succeeded_version
		FROM policy_evidence
		WHERE qualification_id = ?
		ORDER BY evidence_id`, string(qualification))
	if err != nil {
		return nil, fmt.Errorf("policy: load evidence for qualification %s: %w", qualification, err)
	}
	defer func() { _ = rows.Close() }()
	out := []evidenceRow{}
	for rows.Next() {
		var (
			e                evidenceRow
			firstAt          string
			succeededAt      sql.NullString
			succeededVersion sql.NullInt64
		)
		if err := rows.Scan(&e.QualificationID, &e.EvidenceID, &e.Kind, &e.FirstState,
			&e.FirstVersion, &firstAt, &succeededAt, &succeededVersion); err != nil {
			return nil, fmt.Errorf("policy: scan evidence row: %w", err)
		}
		firstAtTime, err := parseStamp(firstAt)
		if err != nil {
			return nil, err
		}
		e.FirstAt = firstAtTime
		if succeededAt.Valid {
			t, err := parseStamp(succeededAt.String)
			if err != nil {
				return nil, err
			}
			e.SucceededAt = &t
		}
		if succeededVersion.Valid {
			v := succeededVersion.Int64
			e.SucceededVersion = &v
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("policy: iterate evidence rows: %w", err)
	}
	return out, nil
}
