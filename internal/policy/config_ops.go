package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Internal compiler operations. _policy.validate and _policy.activate are the
// two ends of the owned-slice protocol configuration drives: validate reads
// the owned slice against current state and never faults on slice content
// (diagnostics carry the problems), activate applies the exact sealed slice
// inside the caller's transaction and faults on anything unappliable.
// _policy.invalidate strips dependent qualifications the moment a dependency
// they cite changes, restricting the affected grants before further admission.

// Input aliases: validate and activate both take the shared Candidate
// wrapper configuration sends to every owner.
type (
	validateInput = candidateEnvelope
	activateInput = candidateEnvelope
)

// diag returns an error-severity diagnostic; warnDiag an advisory one.
func diag(path, code, message string) wireDiagnostic {
	return wireDiagnostic{Path: path, Code: code, Message: message, Severity: "error"}
}

func warnDiag(path, code, message string) wireDiagnostic {
	return wireDiagnostic{Path: path, Code: code, Message: message, Severity: "warning"}
}

// validationOut accumulates one validate run: diagnostics, prerequisites and
// versioned dependency identities.
type validationOut struct {
	out  wireValidation
	deps map[wireRef]bool
}

func newValidationOut() *validationOut {
	return &validationOut{
		out: wireValidation{
			Diagnostics:  []wireDiagnostic{},
			Requirements: []wireRequirement{},
			Dependencies: []wireRef{},
		},
		deps: make(map[wireRef]bool),
	}
}

func (v *validationOut) diag(path, code, message string) {
	v.out.Diagnostics = append(v.out.Diagnostics, diag(path, code, message))
}

func (v *validationOut) warn(path, code, message string) {
	v.out.Diagnostics = append(v.out.Diagnostics, warnDiag(path, code, message))
}

// validateChangeIdentity decodes one owned change's definition strictly for
// its kind and checks that it carries the change's identity.
func validateChangeIdentity(c wireChange) (*wirePolicy, *wirePromotionRule, *wireDiagnostic) {
	problem := func(path, code, message string) *wireDiagnostic {
		d := diag(path, code, message)
		return &d
	}
	switch c.Kind {
	case kindPolicy:
		var def wirePolicy
		if err := contract.DecodeStrict(c.Definition, &def); err != nil {
			return nil, nil, problem("definition", "decode", "policy definition is not a decodable Policy: "+err.Error())
		}
		if def.ID != c.ID {
			return nil, nil, problem("definition.id", "identity_mismatch", "definition identity does not match the change")
		}
		return &def, nil, nil
	case kindAutonomyRule:
		var def wirePromotionRule
		if err := contract.DecodeStrict(c.Definition, &def); err != nil {
			return nil, nil, problem("definition", "decode", "promotion rule definition is not a decodable PromotionRule: "+err.Error())
		}
		if def.ID != c.ID {
			return nil, nil, problem("definition.id", "identity_mismatch", "definition identity does not match the change")
		}
		return nil, &def, nil
	}
	return nil, nil, problem("definition", "decode", "unknown owned kind "+c.Kind)
}

// conditionDependencies collects the versioned tool references standing-rule
// conditions cite; the compiler records them so later tool changes reach
// _policy.invalidate.
func conditionDependencies(rules []wireRule, deps map[wireRef]bool) {
	for i := range rules {
		if len(rules[i].Conditions) == 0 {
			continue
		}
		conds := map[string]json.RawMessage{}
		if err := json.Unmarshal(rules[i].Conditions, &conds); err != nil {
			continue
		}
		if raw, ok := conds[condRequiredToolVersion]; ok {
			var ref wireRef
			if err := json.Unmarshal(raw, &ref); err == nil && ref.ID != "" && ref.Version >= 1 {
				deps[ref] = true
			}
		}
	}
}

// validateExistingChange mirrors configuration's owned-change fencing: the
// target must exist and sit at the expected version; update definitions
// carry the post-apply version.
func (s *Service) validateExistingChange(ctx context.Context, unit contract.Unit, path string, c wireChange, resource string, lookup func() (int64, bool, error), v *validationOut) {
	version, found, err := lookup()
	if err != nil {
		v.diag(path, "storage", err.Error())
		return
	}
	if !found {
		v.diag(path+".id", "unknown_reference", resource+" "+string(c.ID)+" does not exist")
		return
	}
	if c.ExpectedVersion != version {
		v.diag(path+".expected_version", "stale_version",
			resource+" "+string(c.ID)+" is at version "+strconv.FormatInt(version, 10))
		return
	}
	if c.Action == changeUpdate {
		var def struct {
			Version int64 `json:"version"`
		}
		if err := json.Unmarshal(c.Definition, &def); err != nil || def.Version != c.ExpectedVersion+1 {
			v.diag(path+".definition.version", "version",
				resource+" update definition must carry the post-apply version "+
					strconv.FormatInt(c.ExpectedVersion+1, 10))
		}
	}
	if c.Action == changeArchive {
		v.warn(path, "restrictive_side_effect",
			"archive disables new admissions when applied; definitions and obligations are retained")
	}
}

// validatePolicyChange validates one policy change against current state.
func (s *Service) validatePolicyChange(ctx context.Context, unit contract.Unit, path string, c wireChange, def wirePolicy, install contract.ID, v *validationOut) {
	if def.Scope.InstallationID != install {
		v.diag(path+".definition.scope", "installation_mismatch",
			"policy definition is homed in another installation")
		return
	}
	for i := range def.Rules {
		for _, p := range validateConditions(def.Rules[i]) {
			v.diag(fmt.Sprintf("%s.definition.rules[%d]", path, i), "conditions", p)
		}
	}
	conditionDependencies(def.Rules, v.deps)
	switch c.Action {
	case changeCreate:
		if c.ExpectedVersion != 0 {
			v.diag(path+".expected_version", "expected_version", "policy create must carry expected_version 0")
			return
		}
		if def.Version != 1 {
			v.diag(path+".definition.version", "identity_mismatch", "policy create definition must carry version 1")
			return
		}
		if _, found, err := s.loadPolicyRow(ctx, unit, def.ID); err != nil {
			v.diag(path, "storage", err.Error())
		} else if found {
			v.diag(path+".id", "conflict", "policy "+string(def.ID)+" already exists")
		}
	case changeUpdate, changeArchive:
		s.validateExistingChange(ctx, unit, path, c, "policy", func() (int64, bool, error) {
			row, found, err := s.loadPolicyRow(ctx, unit, def.ID)
			return row.Version, found, err
		}, v)
	}
}

// validateRuleChange validates one promotion-rule change against current
// state. Every rule change names its ceiling grant as a prerequisite the
// apply flow must confirm.
func (s *Service) validateRuleChange(ctx context.Context, unit contract.Unit, path string, c wireChange, def wirePromotionRule, install contract.ID, v *validationOut) {
	if def.Scope.InstallationID != install {
		v.diag(path+".definition.scope", "installation_mismatch",
			"promotion rule definition is homed in another installation")
		return
	}
	if len(def.RequiredEvidence) == 0 {
		v.diag(path+".definition.required_evidence", "definition",
			"required_evidence must name at least one evidence kind")
	}
	v.out.Requirements = append(v.out.Requirements, wireRequirement{
		Code: contract.CodePrerequisiteMissing,
		Message: "promotion rule " + string(def.ID) + " requires ceiling grant " + string(def.CeilingGrantID) +
			" to be current authority covering capability " + def.Capability + " and its destinations",
		ResourceID: def.CeilingGrantID,
	})
	switch c.Action {
	case changeCreate:
		if c.ExpectedVersion != 0 {
			v.diag(path+".expected_version", "expected_version",
				"promotion rule create must carry expected_version 0")
			return
		}
		if def.Version != 1 {
			v.diag(path+".definition.version", "identity_mismatch",
				"promotion rule create definition must carry version 1")
			return
		}
		if _, found, err := s.loadRuleRow(ctx, unit, def.ID); err != nil {
			v.diag(path, "storage", err.Error())
		} else if found {
			v.diag(path+".id", "conflict", "promotion rule "+string(def.ID)+" already exists")
		}
	case changeUpdate, changeArchive:
		s.validateExistingChange(ctx, unit, path, c, "promotion rule", func() (int64, bool, error) {
			row, found, err := s.loadRuleRow(ctx, unit, def.ID)
			return row.Version, found, err
		}, v)
	}
}

// validate implements _policy.validate: validate only the owned candidate
// slice against the current snapshot, collect dependency identities and
// requirements; no live changes and no network. Slice content problems are
// diagnostics, never faults; only a malformed envelope faults (which strict
// dispatch already rejects).
func (s *Service) validate(ctx context.Context, unit contract.Unit, in validateInput) (contract.Payload, error) {
	v := newValidationOut()
	install := unit.Scope().InstallationID
	seen := make(map[string]bool, len(in.Candidate.Changes))
	for i, c := range in.Candidate.Changes {
		if c.Kind != kindPolicy && c.Kind != kindAutonomyRule {
			continue // other owners validate their own slices
		}
		dup := c.Kind + "|" + string(c.ID)
		if seen[dup] {
			v.diag("changes["+strconv.Itoa(i)+"]", "duplicate_change",
				c.Kind+" "+string(c.ID)+" is changed more than once in this candidate")
			continue
		}
		seen[dup] = true
		path := "changes[" + strconv.Itoa(i) + "]"
		if c.Action != changeCreate && c.Action != changeUpdate && c.Action != changeArchive {
			v.diag(path+".action", "unsupported_action",
				"policy-owned resources support create, update and archive only")
			continue
		}
		policyDef, ruleDef, problem := validateChangeIdentity(c)
		if problem != nil {
			v.out.Diagnostics = append(v.out.Diagnostics, *problem)
			continue
		}
		switch c.Kind {
		case kindPolicy:
			s.validatePolicyChange(ctx, unit, path, c, *policyDef, install, v)
		case kindAutonomyRule:
			s.validateRuleChange(ctx, unit, path, c, *ruleDef, install, v)
		}
	}
	for ref := range v.deps {
		v.out.Dependencies = append(v.out.Dependencies, ref)
	}
	sort.Slice(v.out.Dependencies, func(a, b int) bool {
		if v.out.Dependencies[a].ID != v.out.Dependencies[b].ID {
			return v.out.Dependencies[a].ID < v.out.Dependencies[b].ID
		}
		return v.out.Dependencies[a].Version < v.out.Dependencies[b].Version
	})
	return completed(validationBody{Resource: v.out})
}

// activate implements _policy.activate: apply the owned exact sealed
// candidate slice inside the caller's compiler transaction. Everything the
// slice needs must already hold: any unappliable change faults and rolls the
// whole bundle back.
func (s *Service) activate(ctx context.Context, unit contract.Unit, in activateInput) (contract.Payload, error) {
	now := s.deps.Clock.Now()
	versions := []wireRef{}
	for _, c := range in.Candidate.Changes {
		if c.Kind != kindPolicy && c.Kind != kindAutonomyRule {
			continue
		}
		policyDef, ruleDef, problem := validateChangeIdentity(c)
		if problem != nil {
			return contract.Payload{}, invalidInput("candidate slice rejected: %s", problem.Message)
		}
		switch c.Kind {
		case kindPolicy:
			out, err := s.activatePolicyChange(ctx, unit, c, *policyDef, now)
			if err != nil {
				return contract.Payload{}, err
			}
			versions = append(versions, out)
		case kindAutonomyRule:
			out, err := s.activateRuleChange(ctx, unit, c, *ruleDef, now)
			if err != nil {
				return contract.Payload{}, err
			}
			versions = append(versions, out)
		}
	}
	return completed(versionsBody{Versions: versions})
}

// activatePolicyChange applies one policy change with optimistic version
// fencing.
func (s *Service) activatePolicyChange(ctx context.Context, unit contract.Unit, c wireChange, def wirePolicy, now time.Time) (wireRef, error) {
	if def.Scope.InstallationID != unit.Scope().InstallationID {
		return wireRef{}, invalidInput("policy %s is homed in another installation", def.ID)
	}
	switch c.Action {
	case changeCreate:
		if c.ExpectedVersion != 0 || def.Version != 1 {
			return wireRef{}, invalidInput("policy create %s must carry expected_version 0 and definition version 1", def.ID)
		}
		row := policyRow{
			ID: def.ID, Version: 1, Scope: def.Scope, Rules: def.Rules,
			Extensions: def.Extensions, CreatedAt: now, UpdatedAt: now,
		}
		if err := s.insertPolicy(ctx, unit, row); err != nil {
			return wireRef{}, err
		}
		if err := emitTransition(ctx, unit, eventPolicyActivated, def.ID, 1); err != nil {
			return wireRef{}, err
		}
		return wireRef{ID: def.ID, Version: 1}, nil
	case changeUpdate:
		row, found, err := s.loadPolicyRow(ctx, unit, def.ID)
		if err != nil {
			return wireRef{}, err
		}
		if !found {
			return wireRef{}, notFound("policy %s is unknown in this installation", def.ID)
		}
		if row.Archived {
			return wireRef{}, conflict("policy %s is archived and refuses changes", def.ID)
		}
		if row.Version != c.ExpectedVersion {
			return wireRef{}, staleVersion(
				"policy %s is at version %d, not the expected %d", def.ID, row.Version, c.ExpectedVersion)
		}
		if int64(def.Version) != c.ExpectedVersion+1 {
			return wireRef{}, invalidInput(
				"policy update %s definition must carry the post-apply version %d", def.ID, c.ExpectedVersion+1)
		}
		row.Scope, row.Rules, row.Extensions = def.Scope, def.Rules, def.Extensions
		row.Version = int64(def.Version)
		row.UpdatedAt = now
		if err := s.updatePolicyRow(ctx, unit, row); err != nil {
			return wireRef{}, err
		}
		if err := emitTransition(ctx, unit, eventPolicyActivated, def.ID, contract.Version(row.Version)); err != nil {
			return wireRef{}, err
		}
		return wireRef{ID: def.ID, Version: contract.Version(row.Version)}, nil
	case changeArchive:
		row, found, err := s.loadPolicyRow(ctx, unit, def.ID)
		if err != nil {
			return wireRef{}, err
		}
		if !found {
			return wireRef{}, notFound("policy %s is unknown in this installation", def.ID)
		}
		if row.Version != c.ExpectedVersion {
			return wireRef{}, staleVersion(
				"policy %s is at version %d, not the expected %d", def.ID, row.Version, c.ExpectedVersion)
		}
		row.Archived = true
		row.Version = row.Version + 1
		row.UpdatedAt = now
		if err := s.updatePolicyRow(ctx, unit, row); err != nil {
			return wireRef{}, err
		}
		if err := emitTransition(ctx, unit, eventPolicyArchived, def.ID, contract.Version(row.Version)); err != nil {
			return wireRef{}, err
		}
		return wireRef{ID: def.ID, Version: contract.Version(row.Version)}, nil
	default:
		return wireRef{}, invalidInput("policy-owned resources support create, update and archive only")
	}
}

// activateRuleChange applies one promotion-rule change with optimistic
// version fencing.
func (s *Service) activateRuleChange(ctx context.Context, unit contract.Unit, c wireChange, def wirePromotionRule, now time.Time) (wireRef, error) {
	if def.Scope.InstallationID != unit.Scope().InstallationID {
		return wireRef{}, invalidInput("promotion rule %s is homed in another installation", def.ID)
	}
	switch c.Action {
	case changeCreate:
		if c.ExpectedVersion != 0 || def.Version != 1 {
			return wireRef{}, invalidInput("promotion rule create %s must carry expected_version 0 and definition version 1", def.ID)
		}
		row := promotionRuleRow{
			ID: def.ID, Version: 1, Scope: def.Scope, Capability: def.Capability,
			Destinations: def.Destinations, RequiredEvidence: def.RequiredEvidence,
			MinimumSuccesses: def.MinimumSuccesses, EvidenceWindowSeconds: def.EvidenceWindowSeconds,
			DisqualifyingEvents: def.DisqualifyingEvents, CeilingGrantID: def.CeilingGrantID,
			HumanRequiredPreserved: def.HumanRequiredPreserved, CreatedAt: now, UpdatedAt: now,
		}
		if err := s.insertRule(ctx, unit, row); err != nil {
			return wireRef{}, err
		}
		if err := emitTransition(ctx, unit, eventRuleActivated, def.ID, 1); err != nil {
			return wireRef{}, err
		}
		return wireRef{ID: def.ID, Version: 1}, nil
	case changeUpdate:
		row, found, err := s.loadRuleRow(ctx, unit, def.ID)
		if err != nil {
			return wireRef{}, err
		}
		if !found {
			return wireRef{}, notFound("promotion rule %s is unknown in this installation", def.ID)
		}
		if row.Archived {
			return wireRef{}, conflict("promotion rule %s is archived and refuses changes", def.ID)
		}
		if row.Version != c.ExpectedVersion {
			return wireRef{}, staleVersion(
				"promotion rule %s is at version %d, not the expected %d", def.ID, row.Version, c.ExpectedVersion)
		}
		if int64(def.Version) != c.ExpectedVersion+1 {
			return wireRef{}, invalidInput(
				"promotion rule update %s definition must carry the post-apply version %d", def.ID, c.ExpectedVersion+1)
		}
		row.Scope, row.Capability, row.Destinations = def.Scope, def.Capability, def.Destinations
		row.RequiredEvidence, row.MinimumSuccesses = def.RequiredEvidence, def.MinimumSuccesses
		row.EvidenceWindowSeconds, row.DisqualifyingEvents = def.EvidenceWindowSeconds, def.DisqualifyingEvents
		row.CeilingGrantID, row.HumanRequiredPreserved = def.CeilingGrantID, def.HumanRequiredPreserved
		row.Version = int64(def.Version)
		row.UpdatedAt = now
		if err := s.updateRuleRow(ctx, unit, row); err != nil {
			return wireRef{}, err
		}
		if err := emitTransition(ctx, unit, eventRuleActivated, def.ID, contract.Version(row.Version)); err != nil {
			return wireRef{}, err
		}
		return wireRef{ID: def.ID, Version: contract.Version(row.Version)}, nil
	case changeArchive:
		row, found, err := s.loadRuleRow(ctx, unit, def.ID)
		if err != nil {
			return wireRef{}, err
		}
		if !found {
			return wireRef{}, notFound("promotion rule %s is unknown in this installation", def.ID)
		}
		if row.Version != c.ExpectedVersion {
			return wireRef{}, staleVersion(
				"promotion rule %s is at version %d, not the expected %d", def.ID, row.Version, c.ExpectedVersion)
		}
		row.Archived = true
		row.Version = row.Version + 1
		row.UpdatedAt = now
		if err := s.updateRuleRow(ctx, unit, row); err != nil {
			return wireRef{}, err
		}
		if err := emitTransition(ctx, unit, eventRuleArchived, def.ID, contract.Version(row.Version)); err != nil {
			return wireRef{}, err
		}
		return wireRef{ID: def.ID, Version: contract.Version(row.Version)}, nil
	default:
		return wireRef{}, invalidInput("policy-owned resources support create, update and archive only")
	}
}

// dependencyHit reports whether one changed reference invalidates a
// qualification: a superseded governing rule, a superseded tool or skill
// version, or a cited evidence id (Z19.version_requalification,
// Z19.unsupported_evidence).
func dependencyHit(q qualificationRow, refs []wireRef) bool {
	for _, ref := range refs {
		if q.RuleID == ref.ID && q.RuleVersion != int64(ref.Version) {
			return true
		}
		for _, tool := range q.ToolVersions {
			if tool.ID == ref.ID && tool.Version != ref.Version {
				return true
			}
		}
		for _, skill := range q.SkillVersions {
			if skill.ID == ref.ID && skill.Version != ref.Version {
				return true
			}
		}
		for _, evidence := range q.EvidenceIDs {
			if evidence == ref.ID {
				return true
			}
		}
	}
	return false
}

// invalidate implements _policy.invalidate: restrict only dependent
// qualifications, immediately restrict the affected grants through identity
// and emit explanations, all inside the caller's transaction.
func (s *Service) invalidate(ctx context.Context, unit contract.Unit, in invalidateInput) (contract.Payload, error) {
	now := s.deps.Clock.Now()
	open, err := s.loadOpenQualifications(ctx, unit)
	if err != nil {
		return contract.Payload{}, err
	}
	affected := []contract.ID{}
	for _, q := range open {
		if !dependencyHit(q, in.ChangedDependencies) {
			continue
		}
		q.State = stateRestricted
		q.Explanation = in.Reason
		q.Version = q.Version + 1
		q.UpdatedAt = now
		if err := s.updateQualificationRow(ctx, unit, q); err != nil {
			return contract.Payload{}, err
		}
		if _, err := s.callPeer(ctx, unit, "_identity.restrict", restrictCallInput{
			PrincipalID: q.WorkerID,
			Capability:  q.Capability,
			Reason:      in.Reason,
		}); err != nil {
			return contract.Payload{}, err
		}
		if err := emitTransition(ctx, unit, eventQualificationEvent, q.ID, contract.Version(q.Version)); err != nil {
			return contract.Payload{}, err
		}
		affected = append(affected, q.ID)
	}
	return completed(qualificationIDsBody{QualificationIDs: affected})
}
