package effects

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Operation handlers. Every handler runs inside the caller's transaction and
// returns a *contract.Fault on refusal so the transaction rolls back.
//
// State machine mapping (R10-003):
//
//	prepared            staged action persisted, current policy cleared, not admitted
//	awaiting_review     staged, exact digest-bound review pending at reviews
//	ready               budget reserved, attempt and one-use claim exist
//	executing           claim consumed by the trusted dispatcher
//	awaiting_confirmation  provider accepted the call; authoritative completion pending
//	outcome_unknown     claimed and response lost; reservation retained
//	succeeded | failed  resolved by recorded evidence
//	denied | expired    unsent terminals
//
// Staging (prepare, propose, linked propose) persists the immutable action,
// resolves policy once and leaves the operation staged, awaiting review or
// denied. Admit rechecks everything current (authority, policy, review,
// configuration revision, artifacts, connection validation), completes the
// budget reservation and creates the attempt plus its one-use claim in one
// transaction. Claim consumes the one-use claim after generation, revocation
// and state rechecks; a claimed attempt can never be re-armed. Record appends
// the observation, settles cost or retains uncertainty, and appends
// corrections or disputes for contradictory late evidence without rewriting
// history.

const (
	claimTTL = 15 * time.Minute
)

// policy decisions mirrored from $defs/PolicyResult.
const (
	policyAllow               = "allow"
	policyDeny                = "deny"
	policyReview              = "review"
	policyPrerequisiteMissing = "prerequisite_missing"
)

// connection validation states mirrored from $defs/Connection.
const (
	connStateValid   = "valid"
	connStateInvalid = "invalid"
	connStateExpired = "expired"
	connStateRevoked = "revoked"
)

// review decision value mirrored from $defs/Decision.
const decisionApprove = "approve"

// artifact availability mirrored from $defs/Artifact.state.
const artifactStateAvailable = "available"

// unitScope converts the caller's contract scope to its wire form.
func unitScope(s contract.Scope) wireScope {
	return wireScope{
		InstallationID: s.InstallationID,
		OrganizationID: s.OrganizationID,
		ProjectID:      s.ProjectID,
		WorkerID:       s.WorkerID,
		TaskID:         s.TaskID,
	}
}

// requireRowInstallation refuses operations that address another
// installation's resources.
func requireRowInstallation(unit contract.Unit, install contract.ID) error {
	if install != unit.Scope().InstallationID {
		return permissionDenied("operation belongs to installation %s, not the caller's installation %s",
			install, unit.Scope().InstallationID)
	}
	return nil
}

// validateActionTiming checks the action's own validity window. A not-before
// in the future is a scheduled action; a self-contradictory window or an
// already-expired action is refused at staging time.
func validateActionTiming(action wireAction, now time.Time) error {
	if !action.NotBefore.IsZero() && action.ExpiresAt.Before(action.NotBefore) {
		return invalidInput("action is not effective before its own expiry")
	}
	if action.ExpiresAt.Before(now) {
		return invalidInput("action expired at %s", formatStamp(action.ExpiresAt))
	}
	return nil
}

// knownPreconditions are the precondition keys the effects owner supports;
// the tool contract fixes allowed keys per tool.
var knownPreconditions = map[string]bool{
	"repository_head":            true,
	"expected_resource_versions": true,
	"not_before":                 true,
	"expires_at":                 true,
	"allowed_automation":         true,
}

// validatePreconditions refuses actions carrying precondition keys outside
// the supported set. Precondition values stay inert: schema data bound by the
// action digest, never executable authority.
func validatePreconditions(action wireAction) error {
	if len(action.Preconditions) == 0 {
		return nil
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(action.Preconditions, &keys); err != nil {
		return invalidInput("action preconditions must be a JSON object")
	}
	for k := range keys {
		if !knownPreconditions[k] {
			return capabilityUnsupported("precondition %q is not supported", k)
		}
	}
	return nil
}

// ensureAction canonicalizes and persists the immutable action, returning the
// existing row when an identical action is already bound by digest.
func (s *Service) ensureAction(ctx context.Context, unit contract.Unit, install contract.ID, action wireAction, now time.Time) (*actionRow, error) {
	raw, err := canonicalJSON(action)
	if err != nil {
		return nil, err
	}
	digest := contract.Hash(raw)
	existing, err := loadActionByDigest(ctx, unit, install, digest)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}
	a := &actionRow{
		ID:         s.deps.IDs.New(),
		Version:    1,
		InstallID:  install,
		Digest:     digest,
		ActionJSON: string(raw),
		CreatedAt:  now,
	}
	if err := insertAction(ctx, unit, a); err != nil {
		return nil, err
	}
	return a, nil
}

// loadStoredAction loads the immutable action row bound to one operation and
// decodes its wire form.
func loadStoredAction(ctx context.Context, unit contract.Unit, o *operationRow) (*actionRow, wireAction, error) {
	row, err := loadActionByDigest(ctx, unit, o.InstallID, contract.Digest(o.ActionDigest))
	if err != nil {
		return nil, wireAction{}, err
	}
	if row == nil {
		return nil, wireAction{}, prerequisiteMissing(
			"operation %s references unknown action digest %s", o.ID, o.ActionDigest)
	}
	var action wireAction
	if err := json.Unmarshal([]byte(row.ActionJSON), &action); err != nil {
		return nil, wireAction{}, &contract.Fault{Code: contract.CodeInternalError,
			Message: fmt.Sprintf("stored action of operation %s does not decode", o.ID)}
	}
	return row, action, nil
}

// renderOperation projects one operation row onto its wire form.
func renderOperation(ctx context.Context, unit contract.Unit, o *operationRow) (wireOperation, error) {
	_, action, err := loadStoredAction(ctx, unit, o)
	if err != nil {
		return wireOperation{}, err
	}
	attempts, err := listAttempts(ctx, unit, o.ID)
	if err != nil {
		return wireOperation{}, err
	}
	ids := make([]contract.ID, 0, len(attempts))
	for _, a := range attempts {
		ids = append(ids, a.ID)
	}
	out := wireOperation{
		ID:           o.ID,
		Version:      o.Version,
		Action:       action,
		ActionDigest: o.ActionDigest,
		State:        o.State,
		AttemptIDs:   ids,
	}
	if o.LinkedOperation != "" {
		linked := o.LinkedOperation
		out.LinkedOperationID = &linked
	}
	if o.Relationship != "" {
		relationship := o.Relationship
		out.Relationship = &relationship
	}
	return out, nil
}

// requireActiveAuthority refuses revoked principals and principals under
// active restrictions, rechecking current identity state through the peer.
func (s *Service) requireActiveAuthority(ctx context.Context, unit contract.Unit) error {
	out, err := s.authority(ctx, unit, identityAuthorityInput{
		PrincipalID: unit.Actor().PrincipalID,
		Scope:       unitScope(unit.Scope()),
	})
	if err != nil {
		return err
	}
	if out.Resource.Principal.Revoked {
		return permissionDenied("principal %s is revoked", out.Resource.Principal.ID)
	}
	if len(out.Resource.Restrictions) > 0 {
		return permissionDenied("principal %s carries %d active restrictions",
			out.Resource.Principal.ID, len(out.Resource.Restrictions))
	}
	return nil
}

// requireArtifacts validates availability and digest binding of the action's
// pinned content through the artifacts owner.
func (s *Service) requireArtifacts(ctx context.Context, unit contract.Unit, scope wireScope, content []wireArtifactRef) error {
	if len(content) == 0 {
		return nil
	}
	out, err := s.artifactsMetadata(ctx, unit, artifactsMetadataInput{Scope: scope, Artifacts: content})
	if err != nil {
		return err
	}
	seen := make(map[contract.ID]wireArtifactState, len(out.Artifacts))
	for _, a := range out.Artifacts {
		seen[a.ID] = a
	}
	for _, ref := range content {
		artifact, ok := seen[ref.ID]
		if !ok {
			return prerequisiteMissing("artifact %s was not found in the artifacts owner", ref.ID)
		}
		if artifact.State != artifactStateAvailable {
			return prerequisiteMissing("artifact %s is %s", ref.ID, artifact.State)
		}
		if artifact.Digest != ref.Digest {
			return prerequisiteMissing("artifact %s digest changed since the action was prepared", ref.ID)
		}
	}
	return nil
}

// policyDecision evaluates current authorization for one action and returns
// the peer result.
func (s *Service) policyDecision(ctx context.Context, unit contract.Unit, scope wireScope, action wireAction) (wirePolicyResult, error) {
	out, err := s.policyCheck(ctx, unit, policyCheckInput{
		Scope:      scope,
		Capability: string(action.Tool.ID),
		Action:     &action,
	})
	if err != nil {
		return wirePolicyResult{}, err
	}
	return out.Resource, nil
}

// requirementFromPolicy picks the review requirement matching the action
// digest from the policy result, falling back to a human-required
// requirement, and synthesizing a conservative default when none applies.
func requirementFromPolicy(reqs []wireDecisionRequirement, digest string, now time.Time) wireDecisionRequirement {
	for _, r := range reqs {
		if r.ActionDigest == digest {
			return r
		}
	}
	for _, r := range reqs {
		if r.HumanRequired {
			fallback := r
			fallback.ActionDigest = digest
			return fallback
		}
	}
	return wireDecisionRequirement{
		ActionDigest:     digest,
		HumanRequired:    true,
		ExpiresAt:        now.Add(24 * time.Hour),
		SeparateProposer: false,
	}
}

// stageOperation persists the immutable action and the logical operation,
// resolves current policy and leaves the operation staged, awaiting review or
// denied. It is the shared core of prepare, propose and the linked propose
// operations. A submission-key replay returns the original operation
// unchanged; a different action behind the same key is a submission_conflict.
func (s *Service) stageOperation(ctx context.Context, unit contract.Unit, scope wireScope, action wireAction, sourceKey string, linked contract.ID, relationship string) (*operationRow, error) {
	if err := s.checkInstallation(unit, scope.InstallationID); err != nil {
		return nil, err
	}
	if action.Scope.InstallationID != scope.InstallationID {
		return nil, invalidInput("action scope installation %s does not match the requested scope %s",
			action.Scope.InstallationID, scope.InstallationID)
	}
	now := s.now()
	if err := validateActionTiming(action, now); err != nil {
		return nil, err
	}
	if err := validatePreconditions(action); err != nil {
		return nil, err
	}
	a, err := s.ensureAction(ctx, unit, scope.InstallationID, action, now)
	if err != nil {
		return nil, err
	}
	if sourceKey != "" {
		existing, err := loadOperationBySource(ctx, unit, scope.InstallationID, sourceKey)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			if existing.ActionDigest != string(a.Digest) {
				return nil, submissionConflict("source identity %s is already bound to action digest %s",
					sourceKey, existing.ActionDigest)
			}
			return existing, nil
		}
	}
	o := &operationRow{
		ID:              s.deps.IDs.New(),
		Version:         1,
		InstallID:       scope.InstallationID,
		OrganizationID:  action.Scope.OrganizationID,
		ProjectID:       action.Scope.ProjectID,
		WorkerID:        action.Scope.WorkerID,
		TaskID:          action.Scope.TaskID,
		ActionID:        a.ID,
		ActionDigest:    string(a.Digest),
		SourceKey:       sourceKey,
		State:           opStatePrepared,
		LinkedOperation: linked,
		Relationship:    relationship,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := insertOperation(ctx, unit, o); err != nil {
		return nil, err
	}
	if err := emitTransition(ctx, unit, eventOperationPrepared, o.ID, contract.Version(o.Version)); err != nil {
		return nil, err
	}
	if err := s.requireArtifacts(ctx, unit, scope, action.Content); err != nil {
		return nil, err
	}
	policy, err := s.policyDecision(ctx, unit, scope, action)
	if err != nil {
		return nil, err
	}
	switch policy.Decision {
	case policyAllow:
		// Current authorization cleared staging; admission rechecks before
		// any dispatch.
	case policyDeny:
		if err := transitionOperation(ctx, unit, o, opStateDenied, "", now); err != nil {
			return nil, err
		}
	case policyReview:
		requirement := requirementFromPolicy(policy.Requirements, string(a.Digest), now)
		if _, err := s.reviewsEnsure(ctx, unit, reviewsEnsureInput{
			Scope: scope, Action: action, Requirement: requirement,
		}); err != nil {
			return nil, err
		}
		if err := transitionOperation(ctx, unit, o, opStateAwaitingReview, "", now); err != nil {
			return nil, err
		}
	case policyPrerequisiteMissing:
		return nil, prerequisiteMissing("policy refuses capability %s: %s",
			string(action.Tool.ID), strings.Join(policy.Reasons, "; "))
	}
	return o, nil
}

// _effects.prepare persists the immutable action and logical effect for
// hosted steps, memory writes, probes and evaluation. No physical call
// happens here; admission and dispatch follow through admit and claim.
func (s *Service) handlePrepare(ctx context.Context, unit contract.Unit, in prepareInput) (contract.Outcome[operationResourceBody], error) {
	o, err := s.stageOperation(ctx, unit, in.Scope, in.Action, string(in.SourceID), "", "")
	if err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	return s.renderBody(ctx, unit, o)
}

// operation.propose canonicalizes the immutable exact action, resolves
// current prerequisites and policy and creates the logical operation. No
// provider call happens in the handler.
func (s *Service) handlePropose(ctx context.Context, unit contract.Unit, in proposeInput) (contract.Outcome[operationResourceBody], error) {
	o, err := s.stageOperation(ctx, unit, in.Scope, in.Action, "", "", "")
	if err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	return s.renderBody(ctx, unit, o)
}

// linkedPropose is the shared body of compensation and replacement
// proposals: a new linked operation with its own review and budget; the
// original history stays unchanged.
func (s *Service) linkedPropose(ctx context.Context, unit contract.Unit, in linkedProposeInput, relationship string) (contract.Outcome[operationResourceBody], error) {
	if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	o, err := loadOperation(ctx, unit, in.ID)
	if err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	if o == nil {
		return contract.Outcome[operationResourceBody]{}, notFound("operation %s not found", in.ID)
	}
	if err := requireRowInstallation(unit, o.InstallID); err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	if o.Version != in.ExpectedVersion {
		return contract.Outcome[operationResourceBody]{}, staleVersion("operation %s is at version %d, not %d",
			o.ID, o.Version, in.ExpectedVersion)
	}
	switch o.State {
	case opStateSucceeded, opStateFailed, opStateDenied, opStateExpired, opStateCancelled:
	default:
		return contract.Outcome[operationResourceBody]{}, conflict(
			"operation %s is %s; %s requires a concluded original", o.ID, o.State, relationship)
	}
	n, err := s.stageOperation(ctx, unit, in.Scope, in.Action, "", o.ID, relationship)
	if err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	return s.renderBody(ctx, unit, n)
}

// operation.compensation.propose creates a new linked compensation operation.
func (s *Service) handleCompensationPropose(ctx context.Context, unit contract.Unit, in linkedProposeInput) (contract.Outcome[operationResourceBody], error) {
	return s.linkedPropose(ctx, unit, in, "compensation")
}

// operation.replacement.propose creates a new linked replacement operation.
func (s *Service) handleReplacementPropose(ctx context.Context, unit contract.Unit, in linkedProposeInput) (contract.Outcome[operationResourceBody], error) {
	return s.linkedPropose(ctx, unit, in, "replacement")
}

// _effects.admit atomically rechecks current authority, policy, review,
// configuration, artifacts and connection validation, completes the budget
// reservation and creates the physical attempt with its one-use dispatch
// claim.
func (s *Service) handleAdmit(ctx context.Context, unit contract.Unit, in admitInput) (contract.Outcome[operationResourceBody], error) {
	o, err := loadOperation(ctx, unit, in.OperationID)
	if err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	if o == nil {
		return contract.Outcome[operationResourceBody]{}, notFound("operation %s not found", in.OperationID)
	}
	if err := requireRowInstallation(unit, o.InstallID); err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	if o.Version != in.ExpectedVersion {
		return contract.Outcome[operationResourceBody]{}, staleVersion("operation %s is at version %d, not %d",
			o.ID, o.Version, in.ExpectedVersion)
	}
	switch o.State {
	case opStatePrepared, opStateAwaitingReview:
	default:
		return contract.Outcome[operationResourceBody]{}, conflict(
			"operation %s is %s and not admissible", o.ID, o.State)
	}
	now := s.now()
	if err := s.requireActiveAuthority(ctx, unit); err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	actionRow, action, err := loadStoredAction(ctx, unit, o)
	if err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	if action.ExpiresAt.Before(now) {
		if err := transitionOperation(ctx, unit, o, opStateExpired, "", now); err != nil {
			return contract.Outcome[operationResourceBody]{}, err
		}
		return s.renderBody(ctx, unit, o)
	}
	if !action.NotBefore.IsZero() && now.Before(action.NotBefore) {
		return contract.Outcome[operationResourceBody]{}, conflict(
			"action of operation %s is not effective before %s", o.ID, formatStamp(action.NotBefore))
	}
	policy, err := s.policyDecision(ctx, unit, action.Scope, action)
	if err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	switch policy.Decision {
	case policyDeny:
		if err := transitionOperation(ctx, unit, o, opStateDenied, "", now); err != nil {
			return contract.Outcome[operationResourceBody]{}, err
		}
		return s.renderBody(ctx, unit, o)
	case policyPrerequisiteMissing:
		return contract.Outcome[operationResourceBody]{}, prerequisiteMissing(
			"policy refuses operation %s: %s", o.ID, strings.Join(policy.Reasons, "; "))
	case policyReview:
		out, err := s.reviewsCheck(ctx, unit, reviewsCheckInput{
			Scope:        action.Scope,
			ActionDigest: o.ActionDigest,
		})
		if err != nil {
			return contract.Outcome[operationResourceBody]{}, err
		}
		if !out.Eligible {
			return contract.Outcome[operationResourceBody]{}, reviewRequired(
				"review for action digest %s is not satisfied", o.ActionDigest)
		}
		if out.Decision == nil || out.Decision.Decision != decisionApprove {
			if err := transitionOperation(ctx, unit, o, opStateDenied, "", now); err != nil {
				return contract.Outcome[operationResourceBody]{}, err
			}
			return s.renderBody(ctx, unit, o)
		}
	}
	snapshot, err := s.configurationSnapshot(ctx, unit, configurationSnapshotInput{Scope: action.Scope})
	if err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	if snapshot.Resource.Revision != action.ConfigurationRevision {
		return contract.Outcome[operationResourceBody]{}, staleVersion(
			"action binds configuration revision %d but the current revision is %d",
			action.ConfigurationRevision, snapshot.Resource.Revision)
	}
	if err := s.requireArtifacts(ctx, unit, action.Scope, action.Content); err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	var rootTask *contract.ID
	if action.Scope.TaskID != "" {
		task, err := s.tasksSnapshot(ctx, unit, tasksSnapshotInput{Scope: action.Scope, ID: action.Scope.TaskID})
		if err != nil {
			return contract.Outcome[operationResourceBody]{}, err
		}
		if task.Resource.RootID != nil {
			root := task.Resource.RootID
			rootTask = root
		}
	}
	limits, err := s.accountingInspect(ctx, unit, accountingInspectInput{Scope: action.Scope})
	if err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	tool, conn, err := s.resolveDispatch(ctx, unit, action.Scope, action)
	if err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	reservation, err := s.accountingReserve(ctx, unit, accountingReserveInput{
		Scope:       action.Scope,
		RootTaskID:  rootTask,
		OperationID: o.ID,
		Amount:      action.CostBound,
		Limits:      limits.Limits,
	})
	if err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	attempt, err := s.createAttempt(ctx, unit, o, actionRow, action, tool, conn, reservation.Resource, now)
	if err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	if err := insertClaim(ctx, unit, &claimRow{
		AttemptID:  attempt.ID,
		Generation: unit.Generation(),
		ExpiresAt:  now.Add(claimTTL),
		CreatedAt:  now,
	}); err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	if err := transitionOperation(ctx, unit, o, opStateReady, "", now); err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	return s.renderBody(ctx, unit, o)
}

// resolveDispatch resolves the validated connection and tool contract for one
// action, refusing unvalidated or expired connections.
func (s *Service) resolveDispatch(ctx context.Context, unit contract.Unit, scope wireScope, action wireAction) (wireTool, wireConnection, error) {
	out, err := s.connectionsResolve(ctx, unit, connectionsResolveInput{
		Scope:       scope,
		Connection:  action.Connection,
		Tool:        action.Tool,
		Destination: action.Destination,
	})
	if err != nil {
		return wireTool{}, wireConnection{}, err
	}
	if out.Connection.ValidationState != connStateValid {
		return wireTool{}, wireConnection{}, prerequisiteMissing(
			"connection %s is %s, not validated for dispatch",
			out.Connection.ID, out.Connection.ValidationState)
	}
	if out.Connection.ValidUntil != nil && s.now().After(*out.Connection.ValidUntil) {
		return wireTool{}, wireConnection{}, prerequisiteMissing(
			"connection %s validation expired at %s", out.Connection.ID, formatStamp(*out.Connection.ValidUntil))
	}
	return out.Tool, out.Connection, nil
}

// createAttempt persists the physical attempt with its dispatch intent and
// bumps the operation's attempt counter. The dispatch intent embeds the
// stored canonical action bytes so the claim response replays the exact
// digest-bound action.
func (s *Service) createAttempt(ctx context.Context, unit contract.Unit, o *operationRow, a *actionRow, action wireAction, tool wireTool, conn wireConnection, reservation wireReservation, now time.Time) (*attemptRow, error) {
	dispatch := wireDispatch{
		OperationID:   o.ID,
		AttemptID:     s.deps.IDs.New(),
		Generation:    unit.Generation(),
		Adapter:       tool.Adapter,
		Action:        json.RawMessage(a.ActionJSON),
		CredentialRef: conn.CredentialRef,
		Deadline:      action.ExpiresAt,
	}
	if reservation.ID != "" && reservation.OperationID != o.ID {
		return nil, prerequisiteMissing(
			"reservation %s funds operation %s, not %s",
			reservation.ID, reservation.OperationID, o.ID)
	}
	raw, err := canonicalJSON(dispatch)
	if err != nil {
		return nil, err
	}
	attempt := &attemptRow{
		ID:                 dispatch.AttemptID,
		Version:            1,
		OperationID:        o.ID,
		AttemptNo:          o.AttemptCount + 1,
		State:              attemptStatePrepared,
		Adapter:            tool.Adapter,
		CredentialRef:      conn.CredentialRef,
		Deadline:           action.ExpiresAt,
		DispatchJSON:       string(raw),
		ReservationID:      reservation.ID,
		ReservationVersion: reservation.Version,
		ConnectionID:       conn.ID,
		ConnectionVersion:  conn.Version,
		Generation:         unit.Generation(),
		CreatedAt:          now,
	}
	if err := insertAttempt(ctx, unit, attempt); err != nil {
		return nil, err
	}
	if err := bumpAttemptCount(ctx, unit, o.ID); err != nil {
		return nil, err
	}
	o.AttemptCount++
	return attempt, nil
}

// _effects.claim consumes the one-use dispatch claim of one prepared attempt
// after generation, revocation and state rechecks, and returns the dispatch
// intent. A claimed attempt can never be re-armed: the claim update is fenced
// on consumed = 0, so replays, second claims and expired claims all conflict.
func (s *Service) handleClaim(ctx context.Context, unit contract.Unit, in claimInput) (contract.Outcome[dispatchResourceBody], error) {
	o, err := loadOperation(ctx, unit, in.OperationID)
	if err != nil {
		return contract.Outcome[dispatchResourceBody]{}, err
	}
	if o == nil {
		return contract.Outcome[dispatchResourceBody]{}, notFound("operation %s not found", in.OperationID)
	}
	if err := requireRowInstallation(unit, o.InstallID); err != nil {
		return contract.Outcome[dispatchResourceBody]{}, err
	}
	a, err := loadAttempt(ctx, unit, in.AttemptID)
	if err != nil {
		return contract.Outcome[dispatchResourceBody]{}, err
	}
	if a == nil || a.OperationID != o.ID {
		return contract.Outcome[dispatchResourceBody]{}, notFound(
			"attempt %s of operation %s not found", in.AttemptID, in.OperationID)
	}
	// Revocation before dispatch prevents dispatch; revocation after claim
	// cannot retract transmitted bytes (R10-006).
	if err := s.requireActiveAuthority(ctx, unit); err != nil {
		return contract.Outcome[dispatchResourceBody]{}, err
	}
	c, err := loadClaim(ctx, unit, a.ID)
	if err != nil {
		return contract.Outcome[dispatchResourceBody]{}, err
	}
	if c == nil {
		return contract.Outcome[dispatchResourceBody]{}, notFound("attempt %s has no dispatch claim", a.ID)
	}
	if in.Generation != c.Generation || c.Generation != unit.Generation() {
		return contract.Outcome[dispatchResourceBody]{}, conflict(
			"dispatch generation %d does not match claim generation %d and current generation %d",
			in.Generation, c.Generation, unit.Generation())
	}
	if o.State != opStateReady {
		return contract.Outcome[dispatchResourceBody]{}, conflict(
			"operation %s is %s; claim requires ready", o.ID, o.State)
	}
	if a.State != attemptStatePrepared {
		return contract.Outcome[dispatchResourceBody]{}, conflict(
			"attempt %s is %s and cannot be claimed", a.ID, a.State)
	}
	now := s.now()
	if err := consumeClaim(ctx, unit, a.ID, now); err != nil {
		return contract.Outcome[dispatchResourceBody]{}, err
	}
	if err := updateAttemptState(ctx, unit, a, attemptStateClaimed); err != nil {
		return contract.Outcome[dispatchResourceBody]{}, err
	}
	if err := transitionOperation(ctx, unit, o, opStateExecuting, "", now); err != nil {
		return contract.Outcome[dispatchResourceBody]{}, err
	}
	if err := emitTransition(ctx, unit, eventAttemptClaimed, a.ID, contract.Version(a.Version)); err != nil {
		return contract.Outcome[dispatchResourceBody]{}, err
	}
	var dispatch wireDispatch
	if err := json.Unmarshal([]byte(a.DispatchJSON), &dispatch); err != nil {
		return contract.Outcome[dispatchResourceBody]{}, &contract.Fault{Code: contract.CodeInternalError,
			Message: fmt.Sprintf("stored dispatch intent of attempt %s does not decode", a.ID)}
	}
	return completedOutcome(dispatchResourceBody{Resource: dispatch})
}

// evidenceConnection decodes the locally normalized connection-invalidation
// fields this package forwards to the connections owner when recorded
// evidence carries them.
type evidenceConnection struct {
	ConnectionID      contract.ID `json:"connection_id"`
	ConnectionVersion int64       `json:"connection_version"`
	ValidationState   string      `json:"validation_state"`
}

// recordConnectionValidation forwards normalized connection-invalidation
// evidence to the connections owner. Evidence without the normalized fields
// is a no-op.
func (s *Service) recordConnectionValidation(ctx context.Context, unit contract.Unit, observation wireObservation) error {
	if len(observation.Evidence) == 0 {
		return nil
	}
	var probe evidenceConnection
	if err := json.Unmarshal(observation.Evidence, &probe); err != nil {
		return nil
	}
	if probe.ConnectionID == "" {
		return nil
	}
	switch probe.ValidationState {
	case connStateInvalid, connStateExpired, connStateRevoked:
	default:
		return nil
	}
	_, err := s.connectionsValidationRecord(ctx, unit, connectionsValidationRecordInput{
		ConnectionID:    probe.ConnectionID,
		ExpectedVersion: probe.ConnectionVersion,
		Observation:     observation,
	})
	return err
}

// openObligation records one recovery obligation and emits its opening event.
func (s *Service) openObligation(ctx context.Context, unit contract.Unit, operationID contract.ID, kind string, detail map[string]any, version int64, now time.Time) error {
	detailJSON, err := canonicalJSON(detail)
	if err != nil {
		return err
	}
	if err := insertObligation(ctx, unit, &obligationRow{
		ID:          s.deps.IDs.New(),
		OperationID: operationID,
		Kind:        kind,
		State:       "open",
		DetailJSON:  string(detailJSON),
		CreatedAt:   now,
	}); err != nil {
		return err
	}
	return emitTransition(ctx, unit, eventObligationOpened, operationID, contract.Version(version))
}

// settleReservation settles the attempt's funding reservation when one
// exists. Attempts funded without a reservation carry nothing to settle.
func (s *Service) settleReservation(ctx context.Context, unit contract.Unit, a *attemptRow, usage wireUsage, nonexecution bool) error {
	if a.ReservationID == "" {
		return nil
	}
	_, err := s.accountingSettle(ctx, unit, accountingSettleInput{
		ReservationID:   a.ReservationID,
		ExpectedVersion: a.ReservationVersion,
		Usage:           usage,
		Nonexecution:    nonexecution,
	})
	return err
}

// priorUnknownEvidence reports whether any recorded observation of the
// operation carried an unknown disposition. Earlier unknown attempts survive
// later failures: a failure never proves that an earlier unknown attempt did
// not take effect.
func priorUnknownEvidence(observations []*observationRow) bool {
	for _, o := range observations {
		if o.Disposition == dispUnknown {
			return true
		}
	}
	return false
}

// resolveOpenObligations closes the open reconcile and confirm obligations of
// one operation and emits their resolution events. Called when recorded
// evidence resolves the operation to a terminal state.
func (s *Service) resolveOpenObligations(ctx context.Context, unit contract.Unit, operationID contract.ID, version int64, now time.Time) error {
	open, err := listOpenObligationsForOperation(ctx, unit, operationID)
	if err != nil {
		return err
	}
	for _, ob := range open {
		if ob.Kind != oblReconcile && ob.Kind != oblConfirm {
			continue
		}
		if err := resolveObligation(ctx, unit, ob.ID, now); err != nil {
			return err
		}
		if err := emitTransition(ctx, unit, eventObligationResolved, operationID, contract.Version(version)); err != nil {
			return err
		}
	}
	return nil
}

// recordDisposition applies the state-machine consequences of one fresh
// physical observation: reservation settlement or retention, operation
// transition and recovery obligations.
func (s *Service) recordDisposition(ctx context.Context, unit contract.Unit, o *operationRow, a *attemptRow, observation wireObservation, wasClaimed bool, observations []*observationRow, now time.Time) error {
	switch observation.Disposition {
	case dispSucceeded:
		if err := s.settleReservation(ctx, unit, a, observation.Usage, false); err != nil {
			return err
		}
		if err := transitionOperation(ctx, unit, o, opStateSucceeded, "", now); err != nil {
			return err
		}
		return s.resolveOpenObligations(ctx, unit, o.ID, o.Version, now)
	case dispAccepted:
		if err := s.settleReservation(ctx, unit, a, observation.Usage, false); err != nil {
			return err
		}
		if err := transitionOperation(ctx, unit, o, opStateAwaitingConfirmation, "", now); err != nil {
			return err
		}
		return s.openObligation(ctx, unit, o.ID, oblConfirm, map[string]any{
			"operation_id": o.ID,
		}, o.Version, now)
	case dispFailed:
		if err := s.settleReservation(ctx, unit, a, observation.Usage, false); err != nil {
			return err
		}
		if priorUnknownEvidence(observations) {
			return transitionOperation(ctx, unit, o, opStateOutcomeUnknown, "", now)
		}
		if err := transitionOperation(ctx, unit, o, opStateFailed, "", now); err != nil {
			return err
		}
		return s.resolveOpenObligations(ctx, unit, o.ID, o.Version, now)
	case dispUnknown:
		// Uncertainty retains the reservation until supported evidence
		// resolves it (R10-006).
		if err := transitionOperation(ctx, unit, o, opStateOutcomeUnknown, "", now); err != nil {
			return err
		}
		return s.openObligation(ctx, unit, o.ID, oblReconcile, map[string]any{
			"operation_id": o.ID,
		}, o.Version, now)
	case dispNotSent:
		if !wasClaimed {
			// The attempt was never claimed, so the evidence establishes
			// authoritative non-execution: the reservation settles as
			// non-execution and the operation fails as unsent.
			if err := s.settleReservation(ctx, unit, a, observation.Usage, true); err != nil {
				return err
			}
			if err := transitionOperation(ctx, unit, o, opStateFailed, "", now); err != nil {
				return err
			}
			return s.resolveOpenObligations(ctx, unit, o.ID, o.Version, now)
		}
		// Once claimed, a timeout, cancellation or weak not-found cannot
		// prove non-execution; the reservation is retained and reconciliation
		// is owed (R10-006).
		if err := transitionOperation(ctx, unit, o, opStateOutcomeUnknown, "", now); err != nil {
			return err
		}
		return s.openObligation(ctx, unit, o.ID, oblReconcile, map[string]any{
			"operation_id": o.ID,
		}, o.Version, now)
	default:
		return invalidInput("observation disposition %q is not supported", observation.Disposition)
	}
}

// _effects.record appends one observation for one attempt, settles the
// attempt's reservation or retains its uncertainty, and replays corrections
// and disputes for late evidence without rewriting history.
func (s *Service) handleRecord(ctx context.Context, unit contract.Unit, in recordInput) (contract.Outcome[operationResourceBody], error) {
	o, err := loadOperation(ctx, unit, in.OperationID)
	if err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	if o == nil {
		return contract.Outcome[operationResourceBody]{}, notFound("operation %s not found", in.OperationID)
	}
	if err := requireRowInstallation(unit, o.InstallID); err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	a, err := loadAttempt(ctx, unit, in.AttemptID)
	if err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	if a == nil || a.OperationID != o.ID {
		return contract.Outcome[operationResourceBody]{}, notFound(
			"attempt %s of operation %s not found", in.AttemptID, in.OperationID)
	}
	if in.Generation != a.Generation {
		return contract.Outcome[operationResourceBody]{}, conflict(
			"record generation %d does not match attempt generation %d", in.Generation, a.Generation)
	}
	now := s.now()
	if err := s.recordConnectionValidation(ctx, unit, in.Observation); err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	observations, err := listObservations(ctx, unit, o.ID)
	if err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	var prior *observationRow
	for _, obs := range observations {
		if obs.AttemptID == a.ID {
			prior = obs
		}
	}
	if prior != nil {
		// The attempt already carries an observation: idempotent replay,
		// correction or dispute. History is never rewritten.
		if prior.Disposition == in.Observation.Disposition {
			return s.renderBody(ctx, unit, o)
		}
		if prior.Disposition == dispUnknown {
			// Supported late evidence resolves recorded uncertainty: the
			// reservation settles with the resolving evidence and the operation
			// moves to the corrected state. An older unknown attempt of another
			// attempt survives a corrected failure.
			if err := appendObservation(ctx, unit, s.deps.IDs.New(), o.ID, a.ID, obsKindCorrection, in.Observation, now); err != nil {
				return contract.Outcome[operationResourceBody]{}, err
			}
			switch in.Observation.Disposition {
			case dispSucceeded:
				if err := s.settleReservation(ctx, unit, a, in.Observation.Usage, false); err != nil {
					return contract.Outcome[operationResourceBody]{}, err
				}
				if err := transitionOperation(ctx, unit, o, opStateSucceeded, "", now); err != nil {
					return contract.Outcome[operationResourceBody]{}, err
				}
				if err := s.resolveOpenObligations(ctx, unit, o.ID, o.Version, now); err != nil {
					return contract.Outcome[operationResourceBody]{}, err
				}
				return s.renderBody(ctx, unit, o)
			case dispFailed:
				if err := s.settleReservation(ctx, unit, a, in.Observation.Usage, false); err != nil {
					return contract.Outcome[operationResourceBody]{}, err
				}
				// An older unknown attempt of another physical call survives a
				// corrected failure: its uncertainty is not resolved by this
				// attempt's supported evidence.
				others := make([]*observationRow, 0, len(observations))
				for _, obs := range observations {
					if obs.ID != prior.ID {
						others = append(others, obs)
					}
				}
				if priorUnknownEvidence(others) {
					if err := transitionOperation(ctx, unit, o, opStateOutcomeUnknown, "", now); err != nil {
						return contract.Outcome[operationResourceBody]{}, err
					}
					return s.renderBody(ctx, unit, o)
				}
				if err := transitionOperation(ctx, unit, o, opStateFailed, "", now); err != nil {
					return contract.Outcome[operationResourceBody]{}, err
				}
				if err := s.resolveOpenObligations(ctx, unit, o.ID, o.Version, now); err != nil {
					return contract.Outcome[operationResourceBody]{}, err
				}
				return s.renderBody(ctx, unit, o)
			case dispAccepted:
				if err := s.settleReservation(ctx, unit, a, in.Observation.Usage, false); err != nil {
					return contract.Outcome[operationResourceBody]{}, err
				}
				if err := transitionOperation(ctx, unit, o, opStateAwaitingConfirmation, "", now); err != nil {
					return contract.Outcome[operationResourceBody]{}, err
				}
				if err := s.openObligation(ctx, unit, o.ID, oblConfirm, map[string]any{
					"operation_id": o.ID,
				}, o.Version, now); err != nil {
					return contract.Outcome[operationResourceBody]{}, err
				}
				return s.renderBody(ctx, unit, o)
			default:
				if err := transitionOperation(ctx, unit, o, correctedState(in.Observation.Disposition), "", now); err != nil {
					return contract.Outcome[operationResourceBody]{}, err
				}
				return s.renderBody(ctx, unit, o)
			}
		}
		// Contradictory late evidence against settled history is appended as
		// a dispute; the operation state and settlements stand, and the
		// dispute obligation directs operator attention to the conflict.
		if err := appendObservation(ctx, unit, s.deps.IDs.New(), o.ID, a.ID, obsKindDispute, in.Observation, now); err != nil {
			return contract.Outcome[operationResourceBody]{}, err
		}
		if err := emitTransition(ctx, unit, eventOperationDisputed, o.ID, contract.Version(o.Version)); err != nil {
			return contract.Outcome[operationResourceBody]{}, err
		}
		if err := s.openObligation(ctx, unit, o.ID, oblDispute, map[string]any{
			"operation_id": o.ID,
			"attempt_id":   a.ID,
		}, o.Version, now); err != nil {
			return contract.Outcome[operationResourceBody]{}, err
		}
		return s.renderBody(ctx, unit, o)
	}
	// Fresh physical evidence: the operation must be ready (the attempt was
	// never claimed) or executing (the claim was consumed).
	switch o.State {
	case opStateReady, opStateExecuting:
	default:
		return contract.Outcome[operationResourceBody]{}, conflict(
			"operation %s is %s and cannot record fresh evidence", o.ID, o.State)
	}
	wasClaimed := a.State == attemptStateClaimed
	evidenceJSON := ""
	if len(in.Observation.Evidence) > 0 {
		evidenceJSON = string(in.Observation.Evidence)
	}
	if err := recordAttempt(ctx, unit, a, in.Observation.Disposition, evidenceJSON,
		usageJSON(in.Observation.Usage), in.Observation.ProviderReference,
		confirmedAt(in.Observation.ConfirmedAt), now); err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	if err := emitTransition(ctx, unit, eventAttemptRecorded, a.ID, contract.Version(a.Version)); err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	if err := insertObservation(ctx, unit, &observationRow{
		ID:                s.deps.IDs.New(),
		OperationID:       o.ID,
		AttemptID:         a.ID,
		Kind:              obsKindPhysical,
		Disposition:       in.Observation.Disposition,
		EvidenceJSON:      evidenceJSON,
		UsageJSON:         usageJSON(in.Observation.Usage),
		ProviderReference: in.Observation.ProviderReference,
		ConfirmedAt:       confirmedAt(in.Observation.ConfirmedAt),
		RecordedAt:        now,
	}); err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	if err := s.recordDisposition(ctx, unit, o, a, in.Observation, wasClaimed, observations, now); err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	return s.renderBody(ctx, unit, o)
}

// appendObservation appends one late-evidence observation in the same
// transaction.
func appendObservation(ctx context.Context, unit contract.Unit, id, operationID, attemptID contract.ID, kind string, observation wireObservation, now time.Time) error {
	evidenceJSON := ""
	if len(observation.Evidence) > 0 {
		evidenceJSON = string(observation.Evidence)
	}
	return insertObservation(ctx, unit, &observationRow{
		ID:                id,
		OperationID:       operationID,
		AttemptID:         attemptID,
		Kind:              kind,
		Disposition:       observation.Disposition,
		EvidenceJSON:      evidenceJSON,
		UsageJSON:         usageJSON(observation.Usage),
		ProviderReference: observation.ProviderReference,
		ConfirmedAt:       confirmedAt(observation.ConfirmedAt),
		RecordedAt:        now,
	})
}

// usageJSON renders one usage value canonically for storage.
func usageJSON(u wireUsage) string {
	raw, err := canonicalJSON(u)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

// confirmedAt flattens the optional confirmation stamp.
func confirmedAt(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// correctedState maps a resolving disposition to the operation state a
// correction establishes.
func correctedState(disposition string) string {
	switch disposition {
	case dispSucceeded:
		return opStateSucceeded
	case dispFailed:
		return opStateFailed
	case dispAccepted:
		return opStateAwaitingConfirmation
	default:
		return opStateOutcomeUnknown
	}
}

// operation.reconcile opens a bounded external read job asking the provider
// for the authoritative outcome of one uncertain or unconfirmed operation.
// The original row is not mutated; the job coordinates live on the
// reconciliation obligation.
func (s *Service) handleReconcile(ctx context.Context, unit contract.Unit, in reconcileInput) (contract.Outcome[jobResourceBody], error) {
	if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[jobResourceBody]{}, err
	}
	o, err := loadOperation(ctx, unit, in.ID)
	if err != nil {
		return contract.Outcome[jobResourceBody]{}, err
	}
	if o == nil {
		return contract.Outcome[jobResourceBody]{}, notFound("operation %s not found", in.ID)
	}
	if err := requireRowInstallation(unit, o.InstallID); err != nil {
		return contract.Outcome[jobResourceBody]{}, err
	}
	if o.Version != in.ExpectedVersion {
		return contract.Outcome[jobResourceBody]{}, staleVersion("operation %s is at version %d, not %d",
			o.ID, o.Version, in.ExpectedVersion)
	}
	switch o.State {
	case opStateOutcomeUnknown, opStateAwaitingConfirmation:
	default:
		return contract.Outcome[jobResourceBody]{}, conflict(
			"operation %s is %s; reconciliation requires uncertainty or pending confirmation", o.ID, o.State)
	}
	attempts, err := listAttempts(ctx, unit, o.ID)
	if err != nil {
		return contract.Outcome[jobResourceBody]{}, err
	}
	attemptIDs := make([]contract.ID, 0, len(attempts))
	for _, a := range attempts {
		attemptIDs = append(attemptIDs, a.ID)
	}
	jobBody, err := canonicalJSON(map[string]any{
		"operation_id":  o.ID,
		"action_digest": o.ActionDigest,
		"attempt_ids":   attemptIDs,
	})
	if err != nil {
		return contract.Outcome[jobResourceBody]{}, err
	}
	job, err := s.executionJobCreate(ctx, unit, executionJobCreateInput{
		Scope:     in.Scope,
		Owner:     ownerName,
		Operation: opReconcile,
		Input:     jobBody,
		SourceID:  s.deps.IDs.New(),
	})
	if err != nil {
		return contract.Outcome[jobResourceBody]{}, err
	}
	if err := s.openObligation(ctx, unit, o.ID, oblReconcile, map[string]any{
		"operation_id": o.ID,
		"job_id":       job.Resource.ID,
	}, o.Version, s.now()); err != nil {
		return contract.Outcome[jobResourceBody]{}, err
	}
	return completedOutcome(job)
}

// operation.get returns one operation with its action and attempts. An
// operation of another installation is indistinguishable from an absent one.
func (s *Service) handleGet(ctx context.Context, unit contract.Unit, in getOperationInput) (contract.Outcome[operationResourceBody], error) {
	if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	o, err := loadOperation(ctx, unit, in.ID)
	if err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	if o == nil || o.InstallID != unit.Scope().InstallationID {
		return contract.Outcome[operationResourceBody]{}, notFound("operation %s not found", in.ID)
	}
	if in.Scope.OrganizationID != "" && o.OrganizationID != in.Scope.OrganizationID {
		return contract.Outcome[operationResourceBody]{}, notFound("operation %s not found", in.ID)
	}
	return s.renderBody(ctx, unit, o)
}

// _effects.pending lists the operations the controller must act on: staged
// operations awaiting admission, accepted calls awaiting confirmation and
// uncertain operations awaiting reconciliation.
func (s *Service) handlePending(ctx context.Context, unit contract.Unit, in pendingInput) (contract.Outcome[pendingOutput], error) {
	if in.Limit < 1 || in.Limit > 100 {
		return contract.Outcome[pendingOutput]{}, invalidInput(
			"limit %d must be between 1 and 100", in.Limit)
	}
	rows, err := listPendingOperations(ctx, unit, unit.Scope().InstallationID, in.Limit)
	if err != nil {
		return contract.Outcome[pendingOutput]{}, err
	}
	out := make([]wireOperation, 0, len(rows))
	for _, o := range rows {
		wire, err := renderOperation(ctx, unit, o)
		if err != nil {
			return contract.Outcome[pendingOutput]{}, err
		}
		out = append(out, wire)
	}
	return completedOutcome(pendingOutput{Operations: out})
}

// renderBody projects one operation row onto its resource envelope.
func (s *Service) renderBody(ctx context.Context, unit contract.Unit, o *operationRow) (contract.Outcome[operationResourceBody], error) {
	wire, err := renderOperation(ctx, unit, o)
	if err != nil {
		return contract.Outcome[operationResourceBody]{}, err
	}
	return completedOutcome(operationResourceBody{Resource: wire})
}
