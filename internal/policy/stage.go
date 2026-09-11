package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Public definition staging: policy.create/update/archive and
// autonomy.rule.create/update/archive. Staging never activates anything: it
// validates the owned definition, applies the governance fences (standing
// policy and promotion rules are administrative acts that shape authority)
// and hands one sealed typed change to configuration's compiler. Activation
// happens only through _policy.activate inside an authorized apply.
//
// Agent self-grant fence (Z05.agent_self_grant, Z04.old_authority): worker
// and client-agent principals cannot stage policy or promotion-rule
// definitions at all, and a rule's ceiling grant must sit inside the
// staging principal's own current authority envelope.

// validateConditions checks one rule's condition object against the
// supported condition keys and value shapes. Unsupported executable
// conditions are rejected at the boundary instead of failing decisions
// later; the engine would deny them anyway, but staging-time rejection
// keeps un-decidable definitions out of the store.
func validateConditions(r wireRule) []string {
	var problems []string
	if len(r.Conditions) == 0 {
		return nil
	}
	conds := map[string]json.RawMessage{}
	if err := json.Unmarshal(r.Conditions, &conds); err != nil {
		return []string{"conditions must be a JSON object"}
	}
	for key, raw := range conds {
		switch key {
		case condMaxCost:
			var m wireMoney
			if err := json.Unmarshal(raw, &m); err != nil || m.MicroUnit < 0 {
				problems = append(problems, "condition max_cost must be a Money object with non-negative micro_units")
			}
		case condRequiredClassification:
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				problems = append(problems, "condition required_classification must be a string")
				continue
			}
			switch v {
			case "internal", "public", "restricted":
			default:
				problems = append(problems, "condition required_classification must be internal, public or restricted")
			}
		case condNotBefore, condExpiresAt:
			if _, ok := parseConditionTime(raw); !ok {
				problems = append(problems, "condition "+key+" must be an RFC3339 timestamp")
			}
		case condRequiredToolVersion:
			var v wireRef
			if err := json.Unmarshal(raw, &v); err != nil || v.ID == "" || v.Version < 1 {
				problems = append(problems, "condition required_tool_version must be a Ref with version >= 1")
			}
		case condRequiredWorkerID:
			var v contract.ID
			if err := json.Unmarshal(raw, &v); err != nil || v == "" {
				problems = append(problems, "condition required_worker_id must be a worker id")
			}
		case condRequiredReview:
			var v bool
			if err := json.Unmarshal(raw, &v); err != nil {
				problems = append(problems, "condition required_review must be a boolean")
			}
		default:
			problems = append(problems, "unsupported condition "+key)
		}
	}
	return problems
}

// policyDefinitionProblems returns the semantic problems of one standing
// policy definition beyond what the wire schema enforces.
func policyDefinitionProblems(def policyDefinitionInput) []string {
	var problems []string
	for i := range def.Rules {
		for _, p := range validateConditions(def.Rules[i]) {
			problems = append(problems, fmt.Sprintf("rules[%d]: %s", i, p))
		}
	}
	return problems
}

// ruleDefinitionProblems returns the semantic problems of one promotion-rule
// definition beyond what the wire schema enforces. A rule without evidence
// kinds could never gather the successes it requires.
func ruleDefinitionProblems(def ruleDefinitionInput) []string {
	var problems []string
	if len(def.RequiredEvidence) == 0 {
		problems = append(problems, "required_evidence must name at least one evidence kind")
	}
	return problems
}

// requireGovernance enforces the administrative-actor fence: only human and
// service principals may establish or change standing policy and promotion
// rules. Workers and client agents propose within rules (Z05.agent_self_grant).
func requireGovernance(unit contract.Unit) error {
	if !mayGovernAutonomy(unit.Actor().Kind) {
		return permissionDenied(
			"establishing or changing governing policy is an administrative act; principal kind %q may not stage policy or promotion-rule definitions", unit.Actor().Kind)
	}
	return nil
}

// ceilingWithinEnvelope verifies that one grant in the actor's authority
// covers a promotion rule's capability and destinations. A rule's earned
// grants can never exceed the ceiling its author held (Z04.old_authority).
func ceilingWithinEnvelope(grants []peerGrant, def ruleDefinitionInput, now time.Time) bool {
	for _, g := range grants {
		if g.ID != def.CeilingGrantID || g.Denied {
			continue
		}
		if g.ExpiresAt != nil && !g.ExpiresAt.After(now) {
			continue
		}
		capOK := false
		for _, c := range g.Capabilities {
			if c == capWildcard || c == def.Capability {
				capOK = true
				break
			}
		}
		if !capOK {
			continue
		}
		destOK := len(g.Destinations) == 0
		for _, d := range def.Destinations {
			found := false
			for _, gd := range g.Destinations {
				if gd == d {
					found = true
					break
				}
			}
			if !found {
				destOK = false
				break
			}
		}
		if destOK {
			return true
		}
	}
	return false
}

// requireCeiling checks that the staging actor's own authority envelope
// contains a usable ceiling grant for the rule definition.
func (s *Service) requireCeiling(ctx context.Context, unit contract.Unit, def ruleDefinitionInput) error {
	authority, err := s.callAuthority(ctx, unit, unit.Actor().PrincipalID, unit.Scope())
	if err != nil {
		if isNotFound(err) {
			return permissionDenied("the staging principal holds no authority; no ceiling grant can be established")
		}
		return err
	}
	if !ceilingWithinEnvelope(authority.Grants, def, s.deps.Clock.Now()) {
		return permissionDenied(
			"ceiling grant %s is not current authority of the staging principal with coverage for capability %s and its destinations",
			def.CeilingGrantID, def.Capability)
	}
	return nil
}

// stageChange hands one sealed typed change to configuration's compiler and
// returns the resulting draft.
func (s *Service) stageChange(ctx context.Context, unit contract.Unit, scope contract.Scope, change wireChange, draftID contract.ID) (wireDraft, error) {
	data, err := s.callPeer(ctx, unit, "_configuration.stage", stageCallInput{
		Scope:   scope,
		Change:  change,
		DraftID: draftID,
	})
	if err != nil {
		return wireDraft{}, err
	}
	var body draftResource
	if err := json.Unmarshal(data, &body); err != nil {
		return wireDraft{}, fmt.Errorf("policy: decode configuration stage draft: %w", err)
	}
	return body.Resource, nil
}

// requireMatchingInstallation rejects definitions homed in another
// installation; cross-installation policy is never visible here.
func requireMatchingInstallation(unit contract.Unit, scope contract.Scope) error {
	if scope.InstallationID != unit.Scope().InstallationID {
		return invalidInput("definition scope installation does not match the transaction scope")
	}
	return nil
}

// policyCreate stages a new standing policy.
func (s *Service) policyCreate(ctx context.Context, unit contract.Unit, in policyCreateInput) (contract.Payload, error) {
	if err := requireGovernance(unit); err != nil {
		return contract.Payload{}, err
	}
	if err := requireMatchingInstallation(unit, in.Definition.Scope); err != nil {
		return contract.Payload{}, err
	}
	if problems := policyDefinitionProblems(in.Definition); len(problems) > 0 {
		return contract.Payload{}, invalidInput("policy definition rejected: %s", problems[0])
	}
	id := s.deps.IDs.New()
	resource := wirePolicy{
		ID:         id,
		Version:    1,
		Scope:      in.Definition.Scope,
		Rules:      in.Definition.Rules,
		Extensions: in.Definition.Extensions,
	}
	defJSON, err := json.Marshal(resource)
	if err != nil {
		return contract.Payload{}, fmt.Errorf("policy: encode policy definition: %w", err)
	}
	draft, err := s.stageChange(ctx, unit, in.Scope, wireChange{
		Kind:            kindPolicy,
		Action:          changeCreate,
		ID:              id,
		ExpectedVersion: 0,
		Definition:      defJSON,
	}, in.DraftID)
	if err != nil {
		return contract.Payload{}, err
	}
	return completed(stagedPolicyBody{Draft: draft, Resource: resource})
}

// policyUpdate stages a new version of an existing standing policy. The
// staged change carries the post-apply version so the compiler can fence
// optimistic concurrency.
func (s *Service) policyUpdate(ctx context.Context, unit contract.Unit, in policyUpdateInput) (contract.Payload, error) {
	if err := requireGovernance(unit); err != nil {
		return contract.Payload{}, err
	}
	row, found, err := s.loadPolicyRow(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("policy %s is unknown in this installation", in.ID)
	}
	if row.Archived {
		return contract.Payload{}, conflict("policy %s is archived and cannot be updated", in.ID)
	}
	if contract.Version(row.Version) != in.ExpectedVersion {
		return contract.Payload{}, staleVersion(
			"policy %s is at version %d, not the expected %d", in.ID, row.Version, in.ExpectedVersion)
	}
	if err := requireMatchingInstallation(unit, in.Definition.Scope); err != nil {
		return contract.Payload{}, err
	}
	if problems := policyDefinitionProblems(in.Definition); len(problems) > 0 {
		return contract.Payload{}, invalidInput("policy definition rejected: %s", problems[0])
	}
	resource := wirePolicy{
		ID:         in.ID,
		Version:    contract.Version(row.Version + 1),
		Scope:      in.Definition.Scope,
		Rules:      in.Definition.Rules,
		Extensions: in.Definition.Extensions,
	}
	defJSON, err := json.Marshal(resource)
	if err != nil {
		return contract.Payload{}, fmt.Errorf("policy: encode policy definition: %w", err)
	}
	draft, err := s.stageChange(ctx, unit, in.Scope, wireChange{
		Kind:            kindPolicy,
		Action:          changeUpdate,
		ID:              in.ID,
		ExpectedVersion: int64(in.ExpectedVersion),
		Definition:      defJSON,
	}, in.DraftID)
	if err != nil {
		return contract.Payload{}, err
	}
	return completed(stagedPolicyBody{Draft: draft, Resource: resource})
}

// policyArchive stages the archive of an existing standing policy. The
// staged change carries the current definition content so the compiler
// retains the archived body; nothing is deleted.
func (s *Service) policyArchive(ctx context.Context, unit contract.Unit, in policyArchiveInput) (contract.Payload, error) {
	if err := requireGovernance(unit); err != nil {
		return contract.Payload{}, err
	}
	row, found, err := s.loadPolicyRow(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("policy %s is unknown in this installation", in.ID)
	}
	if contract.Version(row.Version) != in.ExpectedVersion {
		return contract.Payload{}, staleVersion(
			"policy %s is at version %d, not the expected %d", in.ID, row.Version, in.ExpectedVersion)
	}
	resource := row.wire()
	resource.Version = contract.Version(row.Version + 1)
	defJSON, err := json.Marshal(resource)
	if err != nil {
		return contract.Payload{}, fmt.Errorf("policy: encode policy definition: %w", err)
	}
	draft, err := s.stageChange(ctx, unit, in.Scope, wireChange{
		Kind:            kindPolicy,
		Action:          changeArchive,
		ID:              in.ID,
		ExpectedVersion: int64(in.ExpectedVersion),
		Definition:      defJSON,
	}, in.DraftID)
	if err != nil {
		return contract.Payload{}, err
	}
	return completed(stagedPolicyBody{Draft: draft, Resource: resource})
}

// ruleResource assembles the full promotion-rule definition carried by a
// staged change.
func ruleResource(id contract.ID, version contract.Version, def ruleDefinitionInput) wirePromotionRule {
	return wirePromotionRule{
		ID:                     id,
		Version:                version,
		Scope:                  def.Scope,
		Capability:             def.Capability,
		Destinations:           def.Destinations,
		RequiredEvidence:       def.RequiredEvidence,
		MinimumSuccesses:       def.MinimumSuccesses,
		EvidenceWindowSeconds:  def.EvidenceWindowSeconds,
		DisqualifyingEvents:    def.DisqualifyingEvents,
		CeilingGrantID:         def.CeilingGrantID,
		HumanRequiredPreserved: def.HumanRequiredPreserved,
	}
}

// ruleCreate stages a new promotion rule under the actor's own ceiling.
func (s *Service) ruleCreate(ctx context.Context, unit contract.Unit, in ruleCreateInput) (contract.Payload, error) {
	if err := requireGovernance(unit); err != nil {
		return contract.Payload{}, err
	}
	if err := requireMatchingInstallation(unit, in.Definition.Scope); err != nil {
		return contract.Payload{}, err
	}
	if problems := ruleDefinitionProblems(in.Definition); len(problems) > 0 {
		return contract.Payload{}, invalidInput("promotion rule definition rejected: %s", problems[0])
	}
	if err := s.requireCeiling(ctx, unit, in.Definition); err != nil {
		return contract.Payload{}, err
	}
	id := s.deps.IDs.New()
	resource := ruleResource(id, 1, in.Definition)
	defJSON, err := json.Marshal(resource)
	if err != nil {
		return contract.Payload{}, fmt.Errorf("policy: encode promotion rule definition: %w", err)
	}
	draft, err := s.stageChange(ctx, unit, in.Scope, wireChange{
		Kind:            kindAutonomyRule,
		Action:          changeCreate,
		ID:              id,
		ExpectedVersion: 0,
		Definition:      defJSON,
	}, in.DraftID)
	if err != nil {
		return contract.Payload{}, err
	}
	return completed(stagedRuleBody{Draft: draft, Resource: resource})
}

// ruleUpdate stages a new version of an existing promotion rule. The
// ceiling fence re-runs: an update cannot move the ceiling outside the
// actor's current envelope either.
func (s *Service) ruleUpdate(ctx context.Context, unit contract.Unit, in ruleUpdateInput) (contract.Payload, error) {
	if err := requireGovernance(unit); err != nil {
		return contract.Payload{}, err
	}
	row, found, err := s.loadRuleRow(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("promotion rule %s is unknown in this installation", in.ID)
	}
	if row.Archived {
		return contract.Payload{}, conflict("promotion rule %s is archived and cannot be updated", in.ID)
	}
	if contract.Version(row.Version) != in.ExpectedVersion {
		return contract.Payload{}, staleVersion(
			"promotion rule %s is at version %d, not the expected %d", in.ID, row.Version, in.ExpectedVersion)
	}
	if err := requireMatchingInstallation(unit, in.Definition.Scope); err != nil {
		return contract.Payload{}, err
	}
	if problems := ruleDefinitionProblems(in.Definition); len(problems) > 0 {
		return contract.Payload{}, invalidInput("promotion rule definition rejected: %s", problems[0])
	}
	if err := s.requireCeiling(ctx, unit, in.Definition); err != nil {
		return contract.Payload{}, err
	}
	resource := ruleResource(in.ID, contract.Version(row.Version+1), in.Definition)
	defJSON, err := json.Marshal(resource)
	if err != nil {
		return contract.Payload{}, fmt.Errorf("policy: encode promotion rule definition: %w", err)
	}
	draft, err := s.stageChange(ctx, unit, in.Scope, wireChange{
		Kind:            kindAutonomyRule,
		Action:          changeUpdate,
		ID:              in.ID,
		ExpectedVersion: int64(in.ExpectedVersion),
		Definition:      defJSON,
	}, in.DraftID)
	if err != nil {
		return contract.Payload{}, err
	}
	return completed(stagedRuleBody{Draft: draft, Resource: resource})
}

// ruleArchive stages the archive of an existing promotion rule, carrying the
// current definition so retained work and obligations stay inspectable.
func (s *Service) ruleArchive(ctx context.Context, unit contract.Unit, in ruleArchiveInput) (contract.Payload, error) {
	if err := requireGovernance(unit); err != nil {
		return contract.Payload{}, err
	}
	row, found, err := s.loadRuleRow(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("promotion rule %s is unknown in this installation", in.ID)
	}
	if contract.Version(row.Version) != in.ExpectedVersion {
		return contract.Payload{}, staleVersion(
			"promotion rule %s is at version %d, not the expected %d", in.ID, row.Version, in.ExpectedVersion)
	}
	resource := row.wire()
	resource.Version = contract.Version(row.Version + 1)
	defJSON, err := json.Marshal(resource)
	if err != nil {
		return contract.Payload{}, fmt.Errorf("policy: encode promotion rule definition: %w", err)
	}
	draft, err := s.stageChange(ctx, unit, in.Scope, wireChange{
		Kind:            kindAutonomyRule,
		Action:          changeArchive,
		ID:              in.ID,
		ExpectedVersion: int64(in.ExpectedVersion),
		Definition:      defJSON,
	}, in.DraftID)
	if err != nil {
		return contract.Payload{}, err
	}
	return completed(stagedRuleBody{Draft: draft, Resource: resource})
}
