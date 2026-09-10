package configuration

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/zatiti/zatiti/internal/contract"
)

// The sole definition compiler. Typed create/update/archive stage into
// drafts (no effective mutation); plan seals a candidate; apply atomically
// rechecks head, old authority, dependencies and decisions, then activates
// every owner slice. No network runs inside the compiler.

// stageInput is the wire input of _configuration.stage.
type stageInput struct {
	Scope   wireScope    `json:"scope"`
	Change  wireChange   `json:"change"`
	DraftID *contract.ID `json:"draft_id,omitempty"`
}

// candidate is the internal wire form shared by _*.validate/_*.activate.
type candidate struct {
	PlanID          contract.ID  `json:"plan_id"`
	BaseRevision    int64        `json:"base_revision"`
	CandidateDigest string       `json:"candidate_digest"`
	Changes         []wireChange `json:"changes"`
	Dependencies    []wireRef    `json:"dependencies"`
}

type candidateEnvelope struct {
	Candidate candidate `json:"candidate"`
}

// versionsOutput is the _*.activate output body.
type versionsOutput struct {
	Versions []wireRef `json:"versions"`
}

// validateOutputBody is the _*.validate output body.
type validateOutputBody struct {
	Resource wireValidation `json:"resource"`
}

// applyInput is the wire input of configuration.apply.
type applyInput struct {
	Scope           wireScope   `json:"scope"`
	PlanID          contract.ID `json:"plan_id"`
	BaseRevision    int64       `json:"base_revision"`
	CandidateDigest string      `json:"candidate_digest"`
}

// draftCreateInput is the wire input of configuration.draft.create.
type draftCreateInput struct {
	Scope        wireScope `json:"scope"`
	BaseRevision int64     `json:"base_revision"`
}

// draftUpdateInput is the wire input of configuration.draft.update.
type draftUpdateInput struct {
	Scope           wireScope    `json:"scope"`
	ID              contract.ID  `json:"id"`
	ExpectedVersion int64        `json:"expected_version"`
	Changes         []wireChange `json:"changes"`
}

// discardInput is the wire input of configuration.draft.discard.
type discardInput struct {
	Scope           wireScope   `json:"scope"`
	ID              contract.ID `json:"id"`
	ExpectedVersion int64       `json:"expected_version"`
}

// planInput is the wire input of configuration.plan.
type planInput struct {
	Scope           wireScope   `json:"scope"`
	DraftID         contract.ID `json:"draft_id"`
	ExpectedVersion int64       `json:"expected_version"`
}

// rollbackInput is the wire input of configuration.rollback.plan.
type rollbackInput struct {
	Scope        wireScope   `json:"scope"`
	RevisionID   contract.ID `json:"revision_id"`
	BaseRevision int64       `json:"base_revision"`
}

// policyCheckInput is the outgoing _policy.check request body.
type policyCheckInput struct {
	Scope           wireScope `json:"scope"`
	Capability      string    `json:"capability"`
	CandidateDigest string    `json:"candidate_digest,omitempty"`
}

// policyResult mirrors $defs/PolicyResult.
type policyResult struct {
	Decision     string                    `json:"decision"`
	Reasons      []string                  `json:"reasons"`
	Requirements []wireDecisionRequirement `json:"requirements"`
}

type policyCheckBody struct {
	Resource policyResult `json:"resource"`
}

// reviewsCheckInput is the outgoing _reviews.check request body.
type reviewsCheckInput struct {
	Scope        wireScope `json:"scope"`
	ActionDigest string    `json:"action_digest"`
}

// reviewsCheckBody mirrors the $defs/Decision subset apply rechecks.
type reviewsCheckBody struct {
	Eligible bool            `json:"eligible"`
	Decision *reviewDecision `json:"decision,omitempty"`
}

type reviewDecision struct {
	Decision string `json:"decision"`
}

// handleStage implements _configuration.stage: validate the typed change,
// then append it into the open (or newly created) draft. No effective
// mutation; creates allocate identity once and rely on submission replay.
func handleStage(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[stageInput](s, "_configuration.stage", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := validateChangeDef(in.Change); err != nil {
		return contract.Payload{}, err
	}
	if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Payload{}, err
	}
	draft, err := s.appendChanges(ctx, unit, in.Scope, in.DraftID, []wireChange{in.Change})
	if err != nil {
		return contract.Payload{}, err
	}
	return s.draftResourceBody(draft)
}

// handleDraftCreate implements configuration.draft.create: an empty draft
// against the stated revision.
func handleDraftCreate(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[draftCreateInput](s, "configuration.draft.create", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Payload{}, err
	}
	head, err := readHead(ctx, unit)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	if in.BaseRevision != head {
		return contract.Payload{}, staleVersion("draft base revision %d does not match current head %d",
			in.BaseRevision, head)
	}
	now := s.clock.Now().UTC()
	draft := &draftRow{
		ID:             s.ids.New(),
		Version:        1,
		InstallationID: in.Scope.InstallationID,
		OrganizationID: in.Scope.OrganizationID,
		BaseRevision:   in.BaseRevision,
		ChangesJSON:    "[]",
		State:          draftOpen,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := insertDraft(ctx, unit, draft); err != nil {
		return contract.Payload{}, err
	}
	return s.draftResourceBody(draft)
}

// handleDraftUpdate implements configuration.draft.update: validate each
// change against its concrete kind schema, then append. Complete typed
// definitions only; omission never deletes.
func handleDraftUpdate(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[draftUpdateInput](s, "configuration.draft.update", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	draft, err := loadDraft(ctx, unit, in.Scope.InstallationID, in.ID)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	if draft == nil {
		return contract.Payload{}, notFound("draft %s not found", in.ID)
	}
	if draft.State != draftOpen {
		return contract.Payload{}, conflictFault("draft %s is %s and no longer accepts changes", in.ID, draft.State)
	}
	if draft.Version != in.ExpectedVersion {
		return contract.Payload{}, staleVersion("draft %s version %d does not match expected %d",
			in.ID, draft.Version, in.ExpectedVersion)
	}
	for _, change := range in.Changes {
		if err := validateChangeDef(change); err != nil {
			return contract.Payload{}, err
		}
	}
	if err := s.appendDraftChanges(ctx, unit, draft, in.Changes); err != nil {
		return contract.Payload{}, err
	}
	return s.draftResourceBody(draft)
}

// handleDraftDiscard implements configuration.draft.discard: discard only the
// draft; plan lineage and evidence are preserved.
func handleDraftDiscard(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[discardInput](s, "configuration.draft.discard", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	draft, err := loadDraft(ctx, unit, in.Scope.InstallationID, in.ID)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	if draft == nil {
		return contract.Payload{}, notFound("draft %s not found", in.ID)
	}
	if draft.Version != in.ExpectedVersion {
		return contract.Payload{}, staleVersion("draft %s version %d does not match expected %d",
			in.ID, draft.Version, in.ExpectedVersion)
	}
	if draft.State != draftOpen {
		return contract.Payload{}, conflictFault("draft %s is already %s", in.ID, draft.State)
	}
	draft.State = draftDiscarded
	draft.Version++
	draft.UpdatedAt = s.clock.Now().UTC()
	if err := updateDraftState(ctx, unit, draft); err != nil {
		return contract.Payload{}, err
	}
	return s.completed(map[string]any{"resource": wireDisposition{
		ID: draft.ID, Version: draft.Version, State: draft.State,
	}})
}

// handleDraftGet implements configuration.draft.get.
func handleDraftGet(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[getInput](s, "configuration.draft.get", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	draft, err := loadDraft(ctx, unit, in.Scope.InstallationID, in.ID)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	if draft == nil {
		return contract.Payload{}, notFound("draft %s not found", in.ID)
	}
	return s.draftResourceBody(draft)
}

// handleListDrafts implements configuration.draft.list.
func handleListDrafts(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[listInput](s, "configuration.draft.list", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	rows, next, err := listDraftRows(ctx, unit, in, inv.Operation)
	if err != nil {
		return contract.Payload{}, err
	}
	items := make([]wireDraft, 0, len(rows))
	for _, d := range rows {
		changes, err := draftChanges(d.ChangesJSON)
		if err != nil {
			return contract.Payload{}, faultOf(err)
		}
		items = append(items, draftBody(d, changes))
	}
	return s.listBody(items, next)
}

// handlePlan implements configuration.plan: seal the draft's candidate with
// base revision, digest, dependency identities, compiler/schema versions,
// authority delta and requirements. No live activation.
func handlePlan(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[planInput](s, "configuration.plan", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	draft, err := loadDraft(ctx, unit, in.Scope.InstallationID, in.DraftID)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	if draft == nil {
		return contract.Payload{}, notFound("draft %s not found", in.DraftID)
	}
	if draft.State != draftOpen {
		return contract.Payload{}, conflictFault("draft %s is %s and cannot be planned", in.DraftID, draft.State)
	}
	if draft.Version != in.ExpectedVersion {
		return contract.Payload{}, staleVersion("draft %s version %d does not match expected %d",
			in.DraftID, draft.Version, in.ExpectedVersion)
	}
	changes, err := draftChanges(draft.ChangesJSON)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	if len(changes) == 0 {
		return contract.Payload{}, invalidInput("draft %s has no staged changes", in.DraftID)
	}
	head, err := readHead(ctx, unit)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	if head != draft.BaseRevision {
		return contract.Payload{}, staleVersion("draft %s is staged against revision %d but head is %d; stage a new draft",
			in.DraftID, draft.BaseRevision, head)
	}
	plan, err := s.sealPlan(ctx, unit, in.Scope, draft, head)
	if err != nil {
		return contract.Payload{}, err
	}
	return s.planBody(plan)
}

// sealPlan validates the candidate against current state and every peer
// owner, collects dependencies and requirements, and inserts the sealed
// plan. Validation diagnostics are sealed into the plan; apply rechecks
// everything and refuses when they persist.
func (s *Service) sealPlan(ctx context.Context, unit contract.Unit, scope wireScope, draft *draftRow, head int64) (*planRow, error) {
	changes, err := draftChanges(draft.ChangesJSON)
	if err != nil {
		return nil, faultOf(err)
	}
	planID := s.ids.New()
	validation, err := s.validateCandidate(ctx, unit, scope, planID, head, changes)
	if err != nil {
		return nil, err
	}
	// Authority preview runs under old effective state: nothing is activated
	// yet, so proposed policy cannot authorize its own application. The exact
	// decision requirements policy demands are sealed into the plan so apply
	// can verify them verbatim.
	authority, decisionReqs, err := s.authorityCheck(ctx, unit, scope, "")
	if err != nil {
		return nil, err
	}
	digest, err := computeCandidateDigest(changes)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now().UTC()
	plan := &planRow{
		ID:              planID,
		Version:         1,
		DraftID:         draft.ID,
		InstallationID:  scope.InstallationID,
		OrganizationID:  scope.OrganizationID,
		BaseRevision:    head,
		CandidateDigest: digest,
		ChangesJSON:     draft.ChangesJSON,
		Dependencies:    validation.Dependencies,
		CompilerVersion: compilerVersion,
		SchemaVersion:   schemaVersion,
		AuthorityReqs:   authority,
		Decisions:       decisionReqs,
		Diagnostics:     validation.Diagnostics,
		Requirements:    validation.Requirements,
		State:           planSealed,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := insertPlan(ctx, unit, plan); err != nil {
		return nil, err
	}
	return plan, nil
}

// authorityCheck calls _policy.check for the apply capability and converts
// the decision into plan-level requirements. digest empty means the plan-time
// preview call under pre-apply authority.
func (s *Service) authorityCheck(ctx context.Context, unit contract.Unit, scope wireScope, digest string) ([]wireRequirement, []wireDecisionRequirement, error) {
	raw, err := s.callOwner(ctx, unit, "_policy.check", policyCheckInput{
		Scope:           scope,
		Capability:      "configuration.apply",
		CandidateDigest: digest,
	})
	if err != nil {
		return nil, nil, err
	}
	var body policyCheckBody
	if err := contract.DecodeStrict(raw, &body); err != nil {
		return nil, nil, faultWrap(internalError("policy check returned an unreadable result"), err)
	}
	switch body.Resource.Decision {
	case "allow":
		return nil, nil, nil
	case "review":
		reqs := []wireRequirement{requirement(contract.CodeReviewRequired,
			"apply requires an eligible decision over candidate digest "+digest, "")}
		return reqs, body.Resource.Requirements, nil
	case "prerequisite_missing":
		reqs := make([]wireRequirement, 0, len(body.Resource.Reasons)+1)
		for _, reason := range body.Resource.Reasons {
			reqs = append(reqs, requirement(contract.CodePrerequisiteMissing, reason, ""))
		}
		if len(reqs) == 0 {
			reqs = append(reqs, requirement(contract.CodePrerequisiteMissing,
				"policy reports missing prerequisites", ""))
		}
		return reqs, nil, nil
	default: // deny and unknown decisions fail closed.
		return nil, nil, permissionDenied("policy denies this apply: %s", firstReason(body.Resource.Reasons))
	}
}

// handleApply implements configuration.apply: atomically recheck current
// head, old authority, restrictions, dependency and prerequisite validity and
// exact decisions; activate the complete bundle; record the revision and
// event. Stale plans fail; an identical retry of an applied plan returns its
// original result without another revision or event.
func handleApply(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[applyInput](s, "configuration.apply", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	plan, err := loadPlan(ctx, unit, in.Scope.InstallationID, in.PlanID)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	if plan == nil {
		return contract.Payload{}, notFound("plan %s not found", in.PlanID)
	}
	if plan.State == planApplied {
		// Exact replay of a completed apply returns the original revision.
		if plan.BaseRevision == in.BaseRevision && plan.CandidateDigest == in.CandidateDigest {
			rev, err := loadRevisionByPlan(ctx, unit, plan.InstallationID, plan.ID)
			if err != nil {
				return contract.Payload{}, faultOf(err)
			}
			if rev == nil {
				return contract.Payload{}, internalError("applied plan %s has no recorded revision", in.PlanID)
			}
			return s.revisionBody(rev)
		}
		return contract.Payload{}, staleVersion("plan %s was applied with different parameters", in.PlanID)
	}
	if plan.BaseRevision != in.BaseRevision || plan.CandidateDigest != in.CandidateDigest {
		return contract.Payload{}, staleVersion("apply parameters do not match sealed plan %s", in.PlanID)
	}
	head, err := readHead(ctx, unit)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	if head != plan.BaseRevision {
		return contract.Payload{}, staleVersion("configuration head is %d but plan %s was sealed against %d; regenerate the plan",
			head, in.PlanID, plan.BaseRevision)
	}
	if err := s.recheckCandidate(ctx, unit, in.Scope, plan); err != nil {
		return contract.Payload{}, err
	}
	if err := s.recheckAuthority(ctx, unit, in.Scope, plan); err != nil {
		return contract.Payload{}, err
	}
	rev, err := s.activate(ctx, unit, plan)
	if err != nil {
		return contract.Payload{}, err
	}
	return s.revisionBody(rev)
}

// recheckCandidate revalidates every peer slice and the owned slice against
// current effective state and compares the resolved dependencies with the
// pins sealed into the plan.
func (s *Service) recheckCandidate(ctx context.Context, unit contract.Unit, scope wireScope, plan *planRow) error {
	changes, err := draftChanges(plan.ChangesJSON)
	if err != nil {
		return faultOf(err)
	}
	validation, err := s.validateCandidate(ctx, unit, scope, plan.ID, plan.BaseRevision, changes)
	if err != nil {
		return err
	}
	for _, d := range validation.Diagnostics {
		if d.Severity == "error" {
			return conflictFault("candidate no longer validates: %s", d.Message)
		}
	}
	return requireDependencyPins(plan.Dependencies, validation.Dependencies)
}

// requireDependencyPins refuses activation when a pinned dependency changed:
// every sealed ref must still resolve at the same version, and every newly
// discovered dependency invalidates the plan (it must be regenerated).
func requireDependencyPins(sealed, current []wireRef) error {
	sealedSet := make(map[wireRef]bool, len(sealed))
	for _, r := range sealed {
		sealedSet[r] = true
	}
	currentSet := make(map[wireRef]bool, len(current))
	for _, r := range current {
		currentSet[r] = true
		if !sealedSet[r] {
			return staleVersion("dependency %s version %d is not pinned by the plan; regenerate the plan", r.ID, r.Version)
		}
	}
	for _, r := range sealed {
		if !currentSet[r] {
			return staleVersion("sealed dependency %s version %d no longer resolves; regenerate the plan", r.ID, r.Version)
		}
	}
	return nil
}

// recheckAuthority re-runs the old-state policy check and verifies any
// digest-bound decision. Proposed policy and imported instructions cannot
// authorize their own activation because policy still sees pre-apply state.
func (s *Service) recheckAuthority(ctx context.Context, unit contract.Unit, scope wireScope, plan *planRow) error {
	_, decisions, err := s.authorityCheck(ctx, unit, scope, plan.CandidateDigest)
	if err != nil {
		return err
	}
	if len(plan.Decisions) == 0 && len(decisions) == 0 {
		return nil
	}
	return s.recheckDecisions(ctx, unit, scope, plan)
}

// recheckDecisions verifies an eligible approved decision exists for the
// exact candidate digest; false is not permission.
func (s *Service) recheckDecisions(ctx context.Context, unit contract.Unit, scope wireScope, plan *planRow) error {
	raw, err := s.callOwner(ctx, unit, "_reviews.check", reviewsCheckInput{
		Scope:        scope,
		ActionDigest: plan.CandidateDigest,
	})
	if err != nil {
		return err
	}
	var body reviewsCheckBody
	if err := contract.DecodeStrict(raw, &body); err != nil {
		return faultWrap(internalError("review check returned an unreadable result"), err)
	}
	if body.Eligible && body.Decision != nil && body.Decision.Decision == "approve" {
		return nil
	}
	return reviewRequiredFault(plan)
}

// reviewRequiredFault builds the review_required fault carrying the plan's
// decision requirements.
func reviewRequiredFault(plan *planRow) *contract.Fault {
	f := fault(contract.CodeReviewRequired,
		"apply %s requires an approved decision bound to digest %s", plan.ID, plan.CandidateDigest)
	if len(plan.Decisions) > 0 {
		if details, err := json.Marshal(plan.Decisions); err == nil {
			f.Details = details
		}
	}
	return f
}

// activate applies the sealed candidate: peer slices first (identity lands
// worker/principal state, then the domains whose effective slices the bundle
// touches, then accounting ceilings), then the owned slice, invariants,
// revision lineage, head advance and events. Everything shares the caller's
// transaction; any failure rolls the whole bundle back.
func (s *Service) activate(ctx context.Context, unit contract.Unit, plan *planRow) (*revisionRow, error) {
	changes, err := draftChanges(plan.ChangesJSON)
	if err != nil {
		return nil, faultOf(err)
	}
	for _, owner := range compilerOwnerOrder {
		slice := sliceForOwner(changes, owner)
		if len(slice) == 0 {
			continue
		}
		if err := s.activateSlice(ctx, unit, "_"+owner+".activate", plan, slice); err != nil {
			return nil, err
		}
	}
	objects, err := s.activateOwned(ctx, unit, plan, changes)
	if err != nil {
		return nil, err
	}
	if err := s.checkInvariants(ctx, unit, plan.InstallationID); err != nil {
		return nil, err
	}
	rev, err := s.recordRevision(ctx, unit, plan, objects)
	if err != nil {
		return nil, err
	}
	if err := s.emitRevision(ctx, unit, plan, rev, changes); err != nil {
		return nil, err
	}
	return rev, nil
}

// recordRevision inserts the revision row with its object lineage, advances
// the head fence, marks the plan applied — all inside the caller's
// transaction.
func (s *Service) recordRevision(ctx context.Context, unit contract.Unit, plan *planRow, objects []revisionObject) (*revisionRow, error) {
	rev := &revisionRow{
		ID:              s.ids.New(),
		Version:         1,
		InstallationID:  plan.InstallationID,
		PlanID:          plan.ID,
		CandidateDigest: plan.CandidateDigest,
		ActivatedAt:     s.clock.Now().UTC(),
	}
	if err := insertRevision(ctx, unit, rev, objects); err != nil {
		return nil, err
	}
	if err := bumpHead(ctx, unit, plan.BaseRevision+1); err != nil {
		return nil, err
	}
	if err := markPlanApplied(ctx, unit, plan.ID, s.clock.Now().UTC()); err != nil {
		return nil, err
	}
	return rev, nil
}

// activateSlice calls one peer's internal activation with its candidate
// slice and returns the activated versions.
func (s *Service) activateSlice(ctx context.Context, unit contract.Unit, op string, plan *planRow, slice []wireChange) error {
	raw, err := s.callOwner(ctx, unit, op, candidateEnvelope{Candidate: candidate{
		PlanID:          plan.ID,
		BaseRevision:    plan.BaseRevision,
		CandidateDigest: plan.CandidateDigest,
		Changes:         slice,
		Dependencies:    plan.Dependencies,
	}})
	if err != nil {
		return err
	}
	var out versionsOutput
	if err := contract.DecodeStrict(raw, &out); err != nil {
		return faultWrap(internalError("%s returned an unreadable result", op), err)
	}
	return nil
}

// handleActivate implements _configuration.activate: apply the owned exact
// sealed candidate slice inside the compiler transaction. The caller must
// hold the configuration-apply context established by application.
func handleActivate(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[candidateEnvelope](s, "_configuration.activate", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	cand := in.Candidate
	plan, err := loadPlan(ctx, unit, unit.Scope().InstallationID, cand.PlanID)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	if plan == nil {
		return contract.Payload{}, notFound("plan %s not found", cand.PlanID)
	}
	if plan.State == planApplied {
		rev, err := loadRevisionByPlan(ctx, unit, plan.InstallationID, plan.ID)
		if err != nil {
			return contract.Payload{}, faultOf(err)
		}
		if rev != nil && plan.BaseRevision == cand.BaseRevision && plan.CandidateDigest == cand.CandidateDigest {
			objects, err := loadRevisionObjects(ctx, unit, rev.ID)
			if err != nil {
				return contract.Payload{}, faultOf(err)
			}
			return s.completed(versionsOutput{Versions: revisionObjectRefs(objects)})
		}
		return contract.Payload{}, staleVersion("plan %s was applied with different parameters", cand.PlanID)
	}
	if plan.BaseRevision != cand.BaseRevision || plan.CandidateDigest != cand.CandidateDigest {
		return contract.Payload{}, staleVersion("candidate does not match sealed plan %s", cand.PlanID)
	}
	head, err := readHead(ctx, unit)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	if head != plan.BaseRevision {
		return contract.Payload{}, staleVersion("configuration head is %d but plan %s was sealed against %d",
			head, cand.PlanID, plan.BaseRevision)
	}
	changes, err := draftChanges(plan.ChangesJSON)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	objects, err := s.activateOwned(ctx, unit, plan, changes)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := s.checkInvariants(ctx, unit, plan.InstallationID); err != nil {
		return contract.Payload{}, err
	}
	rev, err := s.recordRevision(ctx, unit, plan, objects)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := s.emitRevision(ctx, unit, plan, rev, changes); err != nil {
		return contract.Payload{}, err
	}
	return s.completed(versionsOutput{Versions: revisionObjectRefs(objects)})
}

// handleValidate implements _configuration.validate: validate only the owned
// candidate slice against current state and collect dependency identities.
func handleValidate(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[candidateEnvelope](s, "_configuration.validate", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	validation, err := s.validateOwnedSlice(ctx, unit, scopeFromContract(unit.Scope()), in.Candidate.Changes)
	if err != nil {
		return contract.Payload{}, err
	}
	return s.validationBody(validation)
}

// validateCandidate runs the owned-slice validation and every peer
// _<owner>.validate call for the non-owned kinds, merging diagnostics,
// requirements and dependencies.
func (s *Service) validateCandidate(ctx context.Context, unit contract.Unit, scope wireScope, planID contract.ID, baseRevision int64, changes []wireChange) (wireValidation, error) {
	merged := wireValidation{
		Diagnostics:  []wireDiagnostic{},
		Requirements: []wireRequirement{},
		Dependencies: []wireRef{},
	}
	owned, err := s.validateOwnedSlice(ctx, unit, scope, changes)
	if err != nil {
		return merged, err
	}
	merged.Diagnostics = append(merged.Diagnostics, owned.Diagnostics...)
	merged.Requirements = append(merged.Requirements, owned.Requirements...)
	merged.Dependencies = append(merged.Dependencies, owned.Dependencies...)
	for _, owner := range compilerOwnerOrder {
		slice := sliceForOwner(changes, owner)
		if len(slice) == 0 {
			continue
		}
		raw, err := s.callOwner(ctx, unit, "_"+owner+".validate", candidateEnvelope{Candidate: candidate{
			PlanID:          planID,
			BaseRevision:    baseRevision,
			CandidateDigest: "",
			Changes:         slice,
			Dependencies:    merged.Dependencies,
		}})
		if err != nil {
			return merged, err
		}
		var body validateOutputBody
		if err := contract.DecodeStrict(raw, &body); err != nil {
			return merged, faultWrap(internalError("peer validation returned an unreadable result"), err)
		}
		merged.Diagnostics = append(merged.Diagnostics, body.Resource.Diagnostics...)
		merged.Requirements = append(merged.Requirements, body.Resource.Requirements...)
		merged.Dependencies = appendPeerDeps(merged.Dependencies, body.Resource.Dependencies)
	}
	return merged, nil
}

// sliceForOwner returns the changes owned by one domain. Configuration-owned
// kinds never leave this package; worker definitions additionally flow to
// identity so principal state lands with the bundle.
func sliceForOwner(changes []wireChange, owner string) []wireChange {
	var out []wireChange
	for _, c := range changes {
		if ownerForKind[c.Kind] == owner {
			out = append(out, c)
			continue
		}
		if owner == "identity" && c.Kind == kindWorker {
			out = append(out, c)
		}
	}
	return out
}

// callOwner marshals input and calls a peer owner's internal operation
// through the injected ports; the same unit, actor, scope, generation and
// transaction are retained and no authority is minted.
func (s *Service) callOwner(ctx context.Context, unit contract.Unit, op string, input any) (json.RawMessage, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, internalError("outgoing call encoding failed")
	}
	payload, err := s.ports.Call(ctx, unit, contract.Invocation{Operation: op, Version: 1, Input: raw})
	if err != nil {
		return nil, err
	}
	if payload.Error != nil {
		return nil, &faultError{fault: payload.Error}
	}
	return payload.Data, nil
}

// emitRevision appends the revision activation event plus one event per
// activated owned object, atomically with the revision row.
func (s *Service) emitRevision(ctx context.Context, unit contract.Unit, plan *planRow, rev *revisionRow, changes []wireChange) error {
	data := marshalJSON(map[string]any{
		"type":             "configuration.revision.activated",
		"plan_id":          plan.ID,
		"base_revision":    plan.BaseRevision,
		"candidate_digest": plan.CandidateDigest,
		"changes":          changeSummaries(changes),
	})
	if err := unit.Emit(ctx, contract.Event{
		Kind:            "configuration.revision.activated",
		ResourceID:      rev.ID,
		ResourceVersion: contract.Version(rev.Version),
		Data:            json.RawMessage(data),
	}); err != nil {
		return err
	}
	for _, c := range changes {
		if ownerForKind[c.Kind] != "configuration" {
			continue
		}
		edata := marshalJSON(map[string]any{
			"type": "configuration." + c.Kind + "." + c.Action, "kind": c.Kind, "action": c.Action,
		})
		if err := unit.Emit(ctx, contract.Event{
			Kind:            "configuration." + c.Kind + "." + c.Action,
			ResourceID:      c.ID,
			ResourceVersion: contract.Version(objectVersionOrOne(c)),
			Data:            json.RawMessage(edata),
		}); err != nil {
			return err
		}
	}
	return nil
}

func changeSummaries(changes []wireChange) []map[string]string {
	out := make([]map[string]string, 0, len(changes))
	for _, c := range changes {
		out = append(out, map[string]string{"kind": c.Kind, "action": c.Action, "id": string(c.ID)})
	}
	return out
}

// ---------- rollback ----------

// handleRollbackPlan implements configuration.rollback.plan: build a new
// sealed plan against the current head restoring the eligible definitions
// recorded in the revision. No database rewind, no erased obligations.
func handleRollbackPlan(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[rollbackInput](s, "configuration.rollback.plan", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	rev, err := loadRevision(ctx, unit, in.Scope.InstallationID, in.RevisionID)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	if rev == nil {
		return contract.Payload{}, notFound("revision %s not found", in.RevisionID)
	}
	head, err := readHead(ctx, unit)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	if head != in.BaseRevision {
		return contract.Payload{}, staleVersion("configuration head is %d but rollback was requested against %d",
			head, in.BaseRevision)
	}
	objects, err := loadRevisionObjects(ctx, unit, rev.ID)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	changes, err := rollbackChanges(objects)
	if err != nil {
		return contract.Payload{}, err
	}
	if len(changes) == 0 {
		return contract.Payload{}, conflictFault("revision %s touched no restorable configuration objects", in.RevisionID)
	}
	now := s.clock.Now().UTC()
	raw, err := marshalChanges(changes)
	if err != nil {
		return contract.Payload{}, err
	}
	draft := &draftRow{
		ID:             s.ids.New(),
		Version:        1,
		InstallationID: in.Scope.InstallationID,
		OrganizationID: in.Scope.OrganizationID,
		BaseRevision:   head,
		ChangesJSON:    raw,
		Diagnostics: []wireDiagnostic{infoDiagnostic("", "rollback_plan",
			"restores the definitions activated by revision "+string(rev.ID))},
		State:     draftOpen,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := insertDraft(ctx, unit, draft); err != nil {
		return contract.Payload{}, err
	}
	plan, err := s.sealPlan(ctx, unit, in.Scope, draft, head)
	if err != nil {
		return contract.Payload{}, err
	}
	return s.planBody(plan)
}

// rollbackChanges derives inverse changes from the recorded object lineage:
// created objects are deleted (they never carried prior state), updated and
// archived objects are restored to their before definitions.
func rollbackChanges(objects []revisionObject) ([]wireChange, error) {
	changes := make([]wireChange, 0, len(objects))
	for _, o := range objects {
		if ownerForKind[o.Kind] != "configuration" {
			continue
		}
		switch o.Action {
		case actionCreate:
			changes = append(changes, wireChange{
				Kind: o.Kind, Action: actionDelete, ID: o.ObjectID,
				ExpectedVersion: objectVersionOf(o.AfterJSON, 1),
				Definition:      json.RawMessage(o.AfterJSON),
			})
		case actionUpdate, actionArchive:
			if o.BeforeJSON == "" {
				continue
			}
			after := objectVersionOf(o.AfterJSON, 1)
			def, err := withDefinitionVersion(json.RawMessage(o.BeforeJSON), after+1)
			if err != nil {
				return nil, err
			}
			changes = append(changes, wireChange{
				Kind: o.Kind, Action: actionUpdate, ID: o.ObjectID,
				ExpectedVersion: after,
				Definition:      def,
			})
		}
	}
	return changes, nil
}

// withDefinitionVersion splices the post-apply version into a restored
// definition without re-encoding any other value: raw JSON splicing keeps
// int64 maxima, key order and skill bytes intact.
func withDefinitionVersion(def json.RawMessage, version int64) (json.RawMessage, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(def, &doc); err != nil {
		return nil, internalError("restorable definition is not a JSON object")
	}
	raw, err := json.Marshal(version)
	if err != nil {
		return nil, internalError("rollback version encoding failed")
	}
	doc["version"] = raw
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, internalError("rollback definition encoding failed")
	}
	return out, nil
}

// ---------- shared helpers ----------

// checkInstallation rejects inputs whose installation differs from the
// unit's authenticated scope (no cross-scope data disclosure).
func (s *Service) checkInstallation(unit contract.Unit, installation contract.ID) error {
	unitScope := unit.Scope()
	if unitScope.InstallationID != "" && unitScope.InstallationID != installation {
		return permissionDenied("scope installation %s does not match the authenticated installation", installation)
	}
	if installation == "" {
		return invalidInput("scope requires installation_id")
	}
	return nil
}

// getInput is the wire input of every *.get operation.
type getInput struct {
	Scope wireScope   `json:"scope"`
	ID    contract.ID `json:"id"`
}

// listInput is the wire input of every *.list operation.
type listInput struct {
	Scope  wireScope       `json:"scope"`
	Cursor string          `json:"cursor,omitempty"`
	Limit  int64           `json:"limit,omitempty"`
	Filter *wireListFilter `json:"filter,omitempty"`
}

// wireListFilter mirrors the shared structured filter; every field is an
// exact match and AND semantics apply. Fields unsupported by a resource are
// refused as invalid_input before any SQL runs.
type wireListFilter struct {
	State          string      `json:"state,omitempty"`
	Key            string      `json:"key,omitempty"`
	ParentID       contract.ID `json:"parent_id,omitempty"`
	WorkerID       contract.ID `json:"worker_id,omitempty"`
	TaskID         contract.ID `json:"task_id,omitempty"`
	OrganizationID contract.ID `json:"organization_id,omitempty"`
	Descendants    *bool       `json:"descendants,omitempty"`
	NeedsYou       *bool       `json:"needs_you,omitempty"`
}

// listBody builds the common list payload.
func (s *Service) listBody(items any, next *string) (contract.Payload, error) {
	data, err := marshalData(map[string]any{"items": items})
	if err != nil {
		return contract.Payload{}, err
	}
	return contract.Payload{Status: contract.StatusCompleted, Data: data, NextCursor: next}, nil
}

// draftBody converts a draft row to the wire shape.
func draftBody(d *draftRow, changes []wireChange) wireDraft {
	raws := make([]json.RawMessage, 0, len(changes))
	for _, c := range changes {
		raws = append(raws, marshalChangeRaw(c))
	}
	return wireDraft{
		ID:           d.ID,
		Version:      d.Version,
		BaseRevision: d.BaseRevision,
		Changes:      raws,
		Diagnostics:  d.Diagnostics,
	}
}

func marshalChangeRaw(c wireChange) json.RawMessage {
	raw, err := json.Marshal(c)
	if err != nil {
		return json.RawMessage("null")
	}
	return raw
}

// draftResourceBody returns the {resource: Draft} payload.
func (s *Service) draftResourceBody(d *draftRow) (contract.Payload, error) {
	changes, err := draftChanges(d.ChangesJSON)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	return s.completed(map[string]any{"resource": draftBody(d, changes)})
}

// planBody converts a plan row to the {resource: Plan} payload.
func (s *Service) planBody(p *planRow) (contract.Payload, error) {
	changes, err := draftChanges(p.ChangesJSON)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	raws := make([]json.RawMessage, 0, len(changes))
	for _, c := range changes {
		raws = append(raws, marshalChangeRaw(c))
	}
	wire := wirePlan{
		ID:                    p.ID,
		Version:               p.Version,
		DraftID:               p.DraftID,
		BaseRevision:          p.BaseRevision,
		CandidateDigest:       p.CandidateDigest,
		Changes:               raws,
		Dependencies:          p.Dependencies,
		CompilerVersion:       p.CompilerVersion,
		SchemaVersion:         p.SchemaVersion,
		AuthorityRequirements: p.AuthorityReqs,
		Decisions:             p.Decisions,
		Diagnostics:           p.Diagnostics,
		Requirements:          p.Requirements,
	}
	return s.completed(map[string]any{"resource": wire})
}

// revisionBody converts a revision row to the {resource: Revision} payload.
func (s *Service) revisionBody(r *revisionRow) (contract.Payload, error) {
	return s.completed(map[string]any{"resource": wireRevision{
		ID:              r.ID,
		Version:         r.Version,
		PlanID:          r.PlanID,
		CandidateDigest: r.CandidateDigest,
		ActivatedAt:     r.ActivatedAt,
	}})
}

// validationBody returns the {resource: Validation} payload.
func (s *Service) validationBody(v wireValidation) (contract.Payload, error) {
	return s.completed(map[string]any{"resource": v})
}

func firstReason(reasons []string) string {
	if len(reasons) == 0 {
		return "no reason recorded"
	}
	return strings.Join(reasons, "; ")
}
