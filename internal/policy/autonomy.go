package policy

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Earned autonomy: proposals, deterministic evaluation and immediate
// restriction of qualifications.
//
// A qualification is created by autonomy.propose citing independently
// established evidence task ids, pinned to one exact promotion-rule version.
// autonomy.evaluate verifies that evidence against the pinned rule inside the
// transaction: every cited task is read from tasks' current state, successes
// are counted against the rule's minimum, disqualifying observed states
// reject, and evidence kinds this package cannot independently establish
// reject with an inspectable explanation (Z19.unsupported_evidence). A
// qualifying evaluation activates the narrow earned grant through
// _identity.promote under the rule's ceiling; identity's refusal is recorded
// as a rejection rather than invented success.
//
// Fences: a worker or client agent may propose only for itself
// (Z05.agent_self_grant), and no worker may evaluate its own qualification —
// workers cannot approve their own evidence (Z19.self_promotion). The
// criteria and ceiling come only from the pinned rule version; a proposal
// carries no editable criteria. autonomy.restrict and autonomy.demote commit
// the capability-specific restriction locally and at identity before any
// further admission (Z19.immediate_demotion) and retain the evidence history.

// evidenceKindTask is the only evidence kind this package can independently
// establish: a task's recorded outcome, read from tasks' current state.
// "task.succeeded" is accepted as its success-oriented alias.
const evidenceKindTask = "task"

// supportedEvidenceKind reports whether an evidence kind is independently
// verifiable by this package. Model summaries, memory judgments and worker
// test narration are evidence candidates, never policy authority.
func supportedEvidenceKind(kind string) bool {
	return kind == evidenceKindTask || kind == "task.succeeded"
}

// isAgentKind reports whether a principal kind is an automation subject:
// such principals propose within rules but never approve their own evidence.
func isAgentKind(kind string) bool {
	return kind == "worker" || kind == "client_agent"
}

// dedupeIDs drops repeated ids preserving first-occurrence order, so a cited
// id can never double-count toward a rule's minimum successes.
func dedupeIDs(ids []contract.ID) []contract.ID {
	seen := make(map[contract.ID]bool, len(ids))
	out := make([]contract.ID, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// promotionRefusal reports whether err is a named identity refusal that
// records the qualification as rejected instead of failing the transaction.
// Infrastructure faults still propagate.
func promotionRefusal(err error) bool {
	var f *contract.Fault
	if !errors.As(err, &f) {
		return false
	}
	switch f.Code {
	case contract.CodePermissionDenied, contract.CodePrerequisiteMissing,
		contract.CodeStaleVersion, contract.CodeConflict, contract.CodeNotFound,
		contract.CodeCapabilityUnsupported, contract.CodeVerificationFailed,
		contract.CodeBudgetUnavailable:
		return true
	}
	return false
}

// recordEvidenceLinks inserts first-observation links for rows whose
// (qualification, evidence) pair is not yet recorded; the links are
// immutable, so repeats are silently skipped.
func (s *Service) recordEvidenceLinks(ctx context.Context, unit contract.Unit, qualification contract.ID, rows []evidenceRow) error {
	existing, err := s.loadEvidence(ctx, unit, qualification)
	if err != nil {
		return err
	}
	seen := make(map[contract.ID]bool, len(existing))
	for _, e := range existing {
		seen[e.EvidenceID] = true
	}
	for _, e := range rows {
		if seen[e.EvidenceID] {
			continue
		}
		if err := s.insertEvidence(ctx, unit, e); err != nil {
			return err
		}
	}
	return nil
}

// autonomyPropose implements autonomy.propose: record one scoped proposal
// pinned to an exact active rule version, citing evidence task ids for later
// independent verification.
func (s *Service) autonomyPropose(ctx context.Context, unit contract.Unit, in autonomyProposeInput) (contract.Payload, error) {
	if err := requireMatchingInstallation(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	// A worker or client agent may propose a promotion only for itself;
	// proposing for another principal is an expansion attempt.
	if kind := unit.Actor().Kind; isAgentKind(kind) && unit.Actor().PrincipalID != in.WorkerID {
		return contract.Payload{}, permissionDenied(
			"a %s principal may propose a promotion only for itself", kind)
	}
	// The subject worker must be bound in the current configuration.
	workerScope := in.Scope
	workerScope.WorkerID = in.WorkerID
	snap, err := s.callSnapshot(ctx, unit, workerScope)
	if err != nil {
		return contract.Payload{}, err
	}
	if snap.Worker == nil || snap.Worker.ID != in.WorkerID {
		return contract.Payload{}, notFound("worker %s is not bound in the current configuration", in.WorkerID)
	}
	// The rule ref pins the exact version the proposal is judged against.
	rule, found, err := s.loadRuleRow(ctx, unit, in.Rule.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("promotion rule %s is unknown in this installation", in.Rule.ID)
	}
	if !scopeContains(in.Scope, rule.Scope) {
		return contract.Payload{}, permissionDenied("promotion rule %s is outside the request scope", rule.ID)
	}
	if rule.Archived {
		return contract.Payload{}, conflict("promotion rule %s is archived and cannot receive proposals", rule.ID)
	}
	if rule.Version != int64(in.Rule.Version) {
		return contract.Payload{}, staleVersion(
			"promotion rule %s is at version %d, not the proposed %d", rule.ID, rule.Version, in.Rule.Version)
	}
	now := s.deps.Clock.Now()
	q := qualificationRow{
		ID:           s.deps.IDs.New(),
		Version:      1,
		Scope:        in.Scope,
		WorkerID:     in.WorkerID,
		Capability:   rule.Capability,
		Destinations: rule.Destinations,
		RuleID:       rule.ID,
		RuleVersion:  rule.Version,
		EvidenceIDs:  dedupeIDs(in.EvidenceIDs),
		WindowStart:  now,
		WindowEnd:    now.Add(time.Duration(rule.EvidenceWindowSeconds) * time.Second),
		State:        stateProposed,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.insertQualification(ctx, unit, q); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventQualificationEvent, q.ID, contract.Version(q.Version)); err != nil {
		return contract.Payload{}, err
	}
	return completed(qualificationBody{Resource: q.wire()})
}

// autonomyEvaluate implements autonomy.evaluate: deterministically verify the
// cited evidence against the exact pinned rule version and, when it
// qualifies, activate the narrow earned grant through identity within the
// rule's ceiling. Every non-qualifying outcome is committed with an
// inspectable explanation.
func (s *Service) autonomyEvaluate(ctx context.Context, unit contract.Unit, in autonomyEvaluateInput) (contract.Payload, error) {
	if err := requireMatchingInstallation(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	q, found, err := s.loadQualificationRow(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("qualification %s is unknown in this installation", in.ID)
	}
	if !scopeContains(in.Scope, q.Scope) {
		return contract.Payload{}, permissionDenied("qualification %s is outside the request scope", in.ID)
	}
	if contract.Version(q.Version) != in.ExpectedVersion {
		return contract.Payload{}, staleVersion(
			"qualification %s is at version %d, not the expected %d", in.ID, q.Version, in.ExpectedVersion)
	}
	if q.State != stateProposed {
		return contract.Payload{}, conflict(
			"qualification %s is %s and cannot be evaluated again", in.ID, q.State)
	}
	// Workers cannot approve their own evidence (Z19.self_promotion).
	if unit.Actor().PrincipalID == q.WorkerID {
		return contract.Payload{}, permissionDenied(
			"the subject worker cannot evaluate its own qualification; evidence is approved independently")
	}
	rule, found, err := s.loadRuleRow(ctx, unit, q.RuleID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("promotion rule %s is unknown in this installation", q.RuleID)
	}
	if !scopeContains(in.Scope, rule.Scope) {
		return contract.Payload{}, permissionDenied("promotion rule %s is outside the request scope", rule.ID)
	}
	if rule.Archived {
		return contract.Payload{}, conflict("promotion rule %s is archived", rule.ID)
	}
	if rule.Version != q.RuleVersion {
		return contract.Payload{}, staleVersion(
			"promotion rule %s moved to version %d; the proposal pins version %d",
			rule.ID, rule.Version, q.RuleVersion)
	}

	now := s.deps.Clock.Now()

	// An evaluation after the evidence window closed cannot credit
	// stale evidence; the qualification expires instead.
	if !now.Before(q.WindowEnd) {
		q.State = stateExpired
		q.Explanation = fmt.Sprintf(
			"the evidence window closed at %s before evaluation; stale evidence is never credited",
			formatStamp(q.WindowEnd))
		q.Version = q.Version + 1
		q.UpdatedAt = now
		if err := s.updateQualificationRow(ctx, unit, q); err != nil {
			return contract.Payload{}, err
		}
		if err := emitTransition(ctx, unit, eventQualificationEvent, q.ID, contract.Version(q.Version)); err != nil {
			return contract.Payload{}, err
		}
		return completed(qualificationBody{Resource: q.wire()})
	}

	// Verify every cited evidence task against tasks' current state.
	var (
		observed   []evidenceRow
		successes  int64
		unobserved []contract.ID
		aliens     []contract.ID
	)
	for _, id := range q.EvidenceIDs {
		task, err := s.callTaskSnapshot(ctx, unit, in.Scope, id)
		if err != nil {
			if isNotFound(err) {
				unobserved = append(unobserved, id)
				continue
			}
			return contract.Payload{}, err
		}
		link := evidenceRow{
			QualificationID: q.ID,
			EvidenceID:      id,
			Kind:            evidenceKindTask,
			FirstState:      task.State,
			FirstVersion:    int64(task.Version),
			FirstAt:         now,
		}
		if task.State == "succeeded" {
			if task.WorkerID != q.WorkerID {
				aliens = append(aliens, id)
				continue
			}
			successes++
			link.SucceededAt = &now
			v := int64(task.Version)
			link.SucceededVersion = &v
		}
		observed = append(observed, link)
		// A disqualifying observed state rejects the qualification
		// outright (Z19.immediate_demotion, evidence side).
		for _, pattern := range rule.DisqualifyingEvents {
			if pattern == "*" || pattern == task.State || pattern == "task."+task.State {
				return s.rejectQualification(ctx, unit, q, fmt.Sprintf(
					"cited evidence %s is observed in state %q, matching disqualifying event %q of rule %s v%d",
					id, task.State, pattern, rule.ID, rule.Version), now, observed)
			}
		}
	}

	// Evidence kinds this package cannot independently establish reject
	// with an inspectable explanation: competence narration and remembered
	// judgments never activate authority.
	for _, kind := range rule.RequiredEvidence {
		if !supportedEvidenceKind(kind) {
			return s.rejectQualification(ctx, unit, q, fmt.Sprintf(
				"rule %s v%d requires evidence kind %q which cannot be independently established; model summaries, memory judgments and unverified tests are evidence candidates, never authority",
				rule.ID, rule.Version, kind), now, observed)
		}
	}

	// Insufficient successes reject with the full observation record.
	if successes < rule.MinimumSuccesses {
		return s.rejectQualification(ctx, unit, q, fmt.Sprintf(
			"insufficient evidence for rule %s v%d: %d of %d required successes observed (unobserved evidence: %s; evidence of other workers: %s)",
			rule.ID, rule.Version, successes, rule.MinimumSuccesses,
			joinIDs(unobserved), joinIDs(aliens)), now, observed)
	}

	// Bind the worker's current model and skill versions: the earned grant
	// is version-bound for later requalification (Z19.version_requalification).
	workerScope := in.Scope
	workerScope.WorkerID = q.WorkerID
	snap, err := s.callSnapshot(ctx, unit, workerScope)
	if err != nil {
		return contract.Payload{}, err
	}
	if snap.Worker != nil && snap.Worker.ID == q.WorkerID {
		if snap.Worker.Profile != nil {
			q.Model = snap.Worker.Profile.Model
		}
		q.SkillVersions = snap.Worker.SkillVersions
	}

	// Commit the qualified state in memory and ask identity to activate
	// the narrow grant under the rule's ceiling. A named identity refusal
	// records a rejection instead of inventing success.
	q.State = stateQualified
	q.Explanation = fmt.Sprintf(
		"qualified under promotion rule %s v%d with %d independently established successes; earned grant capped by ceiling grant %s",
		rule.ID, rule.Version, successes, rule.CeilingGrantID)
	q.Version = q.Version + 1
	q.UpdatedAt = now
	if _, err := s.callPeer(ctx, unit, "_identity.promote", promoteCallInput{
		PrincipalID:    q.WorkerID,
		Qualification:  q.wire(),
		CeilingGrantID: rule.CeilingGrantID,
	}); err != nil {
		if !promotionRefusal(err) {
			return contract.Payload{}, err
		}
		var f *contract.Fault
		_ = errors.As(err, &f)
		q.State = stateRejected
		q.Explanation = fmt.Sprintf("identity refused promotion: %s", f.Message)
	}
	if err := s.updateQualificationRow(ctx, unit, q); err != nil {
		return contract.Payload{}, err
	}
	if err := s.recordEvidenceLinks(ctx, unit, q.ID, observed); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventQualificationEvent, q.ID, contract.Version(q.Version)); err != nil {
		return contract.Payload{}, err
	}
	return completed(qualificationBody{Resource: q.wire()})
}

// rejectQualification commits one rejected evaluation with its inspectable
// explanation, retaining every observation made before the refusal.
func (s *Service) rejectQualification(ctx context.Context, unit contract.Unit, q qualificationRow, explanation string, now time.Time, observed []evidenceRow) (contract.Payload, error) {
	q.State = stateRejected
	q.Explanation = explanation
	q.Version = q.Version + 1
	q.UpdatedAt = now
	if err := s.updateQualificationRow(ctx, unit, q); err != nil {
		return contract.Payload{}, err
	}
	if err := s.recordEvidenceLinks(ctx, unit, q.ID, observed); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventQualificationEvent, q.ID, contract.Version(q.Version)); err != nil {
		return contract.Payload{}, err
	}
	return completed(qualificationBody{Resource: q.wire()})
}

// autonomyRestrict implements autonomy.restrict: commit an immediate
// capability-specific restriction locally and at identity, before any further
// admission, retaining the evidence and grant history.
func (s *Service) autonomyRestrict(ctx context.Context, unit contract.Unit, in autonomyRestrictInput) (contract.Payload, error) {
	return s.restrictQualification(ctx, unit, in, false)
}

// autonomyDemote implements autonomy.demote: the incident-driven demotion of
// an active earned grant. Only a qualified qualification carries a grant to
// demote.
func (s *Service) autonomyDemote(ctx context.Context, unit contract.Unit, in autonomyRestrictInput) (contract.Payload, error) {
	return s.restrictQualification(ctx, unit, in, true)
}

// restrictQualification is the shared restriction commit. When requireEarned
// is set the qualification must currently be qualified (demotion of an active
// grant); otherwise any open qualification may be restricted.
func (s *Service) restrictQualification(ctx context.Context, unit contract.Unit, in autonomyRestrictInput, requireEarned bool) (contract.Payload, error) {
	if err := requireMatchingInstallation(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	q, found, err := s.loadQualificationRow(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("qualification %s is unknown in this installation", in.ID)
	}
	if !scopeContains(in.Scope, q.Scope) {
		return contract.Payload{}, permissionDenied("qualification %s is outside the request scope", in.ID)
	}
	if contract.Version(q.Version) != in.ExpectedVersion {
		return contract.Payload{}, staleVersion(
			"qualification %s is at version %d, not the expected %d", in.ID, q.Version, in.ExpectedVersion)
	}
	switch {
	case q.State == stateRestricted:
		return contract.Payload{}, conflict("qualification %s is already restricted", in.ID)
	case requireEarned && q.State != stateQualified:
		return contract.Payload{}, conflict(
			"qualification %s is %s; only an active earned grant can be demoted", in.ID, q.State)
	case q.State == stateRejected || q.State == stateExpired:
		return contract.Payload{}, conflict(
			"qualification %s is %s and carries no open grant to restrict", in.ID, q.State)
	}
	now := s.deps.Clock.Now()
	q.State = stateRestricted
	q.Explanation = in.Reason
	q.Version = q.Version + 1
	q.UpdatedAt = now
	if err := s.updateQualificationRow(ctx, unit, q); err != nil {
		return contract.Payload{}, err
	}
	// Retain the incident evidence: link every cited evidence id to the
	// qualification as an immutable incident observation.
	links := make([]evidenceRow, 0, len(in.EvidenceIDs))
	for _, id := range dedupeIDs(in.EvidenceIDs) {
		links = append(links, evidenceRow{
			QualificationID: q.ID,
			EvidenceID:      id,
			Kind:            "incident",
			FirstState:      "incident",
			FirstVersion:    1,
			FirstAt:         now,
		})
	}
	if err := s.recordEvidenceLinks(ctx, unit, q.ID, links); err != nil {
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
	return completed(qualificationBody{Resource: q.wire()})
}

// joinIDs renders an id list for an explanation; empty renders as "none".
func joinIDs(ids []contract.ID) string {
	if len(ids) == 0 {
		return "none"
	}
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += ", "
		}
		out += string(id)
	}
	return out
}
