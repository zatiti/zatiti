package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Handler business logic for every owned operation. Authorization for reads
// and governed dispatch is derived purely from this owner's own binding
// rows (R15-004: parentage and group membership grant no implicit read);
// `_policy.check` gates the four disclosure/mutation operations and the
// binding definition lifecycle for capability-level admission.

// idPtr returns a pointer to id, or nil for the empty identity.
func idPtr(id contract.ID) *contract.ID {
	if id == "" {
		return nil
	}
	return &id
}

// checkScope verifies that an operation's own declared scope agrees with
// the caller's authenticated scope on every dimension it sets.
func (s *Service) checkScope(unit contract.Unit, scope wireScope) error {
	if err := s.checkInstallation(unit, scope.InstallationID); err != nil {
		return err
	}
	callerScope := unit.Scope()
	if scope.OrganizationID != "" && scope.OrganizationID != callerScope.OrganizationID {
		return permissionDenied("scope organization %s does not match the caller's organization", scope.OrganizationID)
	}
	if scope.ProjectID != "" && scope.ProjectID != callerScope.ProjectID {
		return permissionDenied("scope project %s does not match the caller's project", scope.ProjectID)
	}
	if scope.WorkerID != "" && scope.WorkerID != callerScope.WorkerID {
		return permissionDenied("scope worker %s does not match the caller's worker", scope.WorkerID)
	}
	if scope.TaskID != "" && scope.TaskID != callerScope.TaskID {
		return permissionDenied("scope task %s does not match the caller's task", scope.TaskID)
	}
	return nil
}

// checkDefinitionScope allows a memory binding definition to name any
// scope within the caller's own installation: granting access to another
// worker or project is a legitimate curator action, gated by policy
// (admitCapability), not by requiring the definition to match the caller's
// own identity scope.
func (s *Service) checkDefinitionScope(callerScope, definitionScope wireScope) error {
	if definitionScope.InstallationID != callerScope.InstallationID {
		return invalidInput("definition scope installation %s does not match the caller's installation %s",
			definitionScope.InstallationID, callerScope.InstallationID)
	}
	return nil
}

// admitCapability intersects current policy for one capability before this
// owner proceeds. Explicit deny wins; unknown decisions fail closed.
func (s *Service) admitCapability(ctx context.Context, unit contract.Unit, scope wireScope, capability string) error {
	result, err := s.policyCheck(ctx, unit, policyCheckInput{Scope: scope, Capability: capability})
	if err != nil {
		return err
	}
	switch result.Resource.Decision {
	case "allow":
		return nil
	case "deny":
		return permissionDenied("policy denies capability %s: %s", capability, strings.Join(result.Resource.Reasons, "; "))
	case "review":
		return reviewRequired("capability %s requires human review", capability)
	case "prerequisite_missing":
		return prerequisiteMissing("capability %s has an unmet prerequisite", capability)
	default:
		return &contract.Fault{Code: contract.CodeInternalError,
			Message: fmt.Sprintf("policy returned unknown decision %q", result.Resource.Decision)}
	}
}

// scopeContains reports whether a binding's declared scope covers the
// caller's operating scope: every non-empty binding scope field must equal
// the caller's corresponding field. Parentage and group membership grant no
// implicit access (R15-004) -- a binding scoped to one worker never covers
// a sibling worker, and one scoped to a project never covers its parent
// organization's broader reach.
func scopeContains(bindingScope, callerScope wireScope) bool {
	if bindingScope.InstallationID != "" && bindingScope.InstallationID != callerScope.InstallationID {
		return false
	}
	if bindingScope.OrganizationID != "" && bindingScope.OrganizationID != callerScope.OrganizationID {
		return false
	}
	if bindingScope.ProjectID != "" && bindingScope.ProjectID != callerScope.ProjectID {
		return false
	}
	if bindingScope.WorkerID != "" && bindingScope.WorkerID != callerScope.WorkerID {
		return false
	}
	if bindingScope.TaskID != "" && bindingScope.TaskID != callerScope.TaskID {
		return false
	}
	return true
}

// authorizeBinding loads one binding by id and verifies it is active,
// grants the requested permission, belongs to the caller's installation and
// its declared scope covers the caller's scope.
func (s *Service) authorizeBinding(ctx context.Context, unit contract.Unit, callerScope wireScope, id contract.ID, permission string) (*bindingRow, error) {
	b, err := loadBinding(ctx, unit, id)
	if err != nil {
		if isNoRows(err) {
			return nil, notFound("memory binding %s does not exist", id)
		}
		return nil, err
	}
	if b.InstallationID != callerScope.InstallationID {
		return nil, notFound("memory binding %s does not exist", id)
	}
	if b.State != bindingActive {
		return nil, permissionDenied("memory binding %s is not active", id)
	}
	if !b.hasPermission(permission) {
		return nil, permissionDenied("memory binding %s does not grant %s", id, permission)
	}
	if !scopeContains(b.scope(), callerScope) {
		return nil, permissionDenied("memory binding %s does not cover the caller's scope", id)
	}
	return b, nil
}

// findAuthorizingBinding locates any active binding on one brain that
// grants the requested permission and covers the caller's scope, for
// operations that name a brain directly rather than a binding id.
func (s *Service) findAuthorizingBinding(ctx context.Context, unit contract.Unit, callerScope wireScope, brainID contract.ID, permission string) (*bindingRow, error) {
	rows, err := unit.QueryContext(ctx, `SELECT `+bindingColumns+` FROM memory_bindings
		WHERE brain_id = ? AND state = ? ORDER BY created_at, id`, string(brainID), bindingActive)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		b, err := scanBinding(rows.Scan)
		if err != nil {
			return nil, err
		}
		if b.InstallationID != callerScope.InstallationID {
			continue
		}
		if !b.hasPermission(permission) {
			continue
		}
		if !scopeContains(b.scope(), callerScope) {
			continue
		}
		return b, nil
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return nil, permissionDenied("no active binding on brain %s grants %s to the caller", brainID, permission)
}

// selectBindings authorizes every named binding for one permission and
// checks its brain's availability and freshness. Unavailable freshness
// creates a prerequisite failure rather than silently serving stale
// context (R15-007).
func (s *Service) selectBindings(ctx context.Context, unit contract.Unit, callerScope wireScope, ids []contract.ID, permission string, minimumFreshness time.Time) ([]*bindingRow, error) {
	out := make([]*bindingRow, 0, len(ids))
	for _, id := range ids {
		b, err := s.authorizeBinding(ctx, unit, callerScope, id, permission)
		if err != nil {
			return nil, err
		}
		brain, err := loadBrain(ctx, unit, b.BrainID)
		if err != nil {
			if isNoRows(err) {
				return nil, notFound("memory brain for binding %s does not exist", id)
			}
			return nil, err
		}
		if brain.State == brainUnavailable {
			return nil, prerequisiteMissing("brain %s is unavailable", brain.ID)
		}
		if !minimumFreshness.IsZero() {
			if brain.FreshnessAt.IsZero() || brain.FreshnessAt.Before(minimumFreshness) {
				return nil, prerequisiteMissing("brain %s freshness is unavailable at the required bound", brain.ID)
			}
		}
		out = append(out, b)
	}
	return out, nil
}

// renderBinding projects a stored binding row onto its wire shape.
func renderBinding(b *bindingRow) wireMemoryBinding {
	return wireMemoryBinding{ID: b.ID, Version: b.Version, Scope: b.scope(), BrainID: b.BrainID,
		Permissions: b.Permissions, Classification: b.Classification}
}

// renderJob projects a stored job row onto its wire shape.
func renderJob(j *jobRow) wireJob {
	var opID *contract.ID
	if len(j.OperationIDs) > 0 {
		opID = idPtr(j.OperationIDs[0])
	}
	var artifact *wireArtifactRef
	if j.ResultArtifactID != "" {
		artifact = &wireArtifactRef{ID: j.ResultArtifactID, Digest: j.ResultArtifactDigest}
	}
	reqs := j.Requirements
	if reqs == nil {
		reqs = []wireRequirement{}
	}
	return wireJob{
		ID: j.ID, Version: j.Version, Kind: j.Kind, State: j.State, Requirements: reqs,
		ResultArtifact: artifact, OperationID: opID, Owner: ownerName, Operation: j.Operation, Result: j.Result,
	}
}

// renderClaim projects a stored claim row onto its wire shape.
func renderClaim(c *claimRow) wireClaim {
	sourceBrain := idPtr(c.SourceBrainID)
	var sourceClaim *wireRef
	if c.SourceClaimID != "" {
		sourceClaim = &wireRef{ID: c.SourceClaimID, Version: c.SourceClaimVersion}
	}
	curator := idPtr(c.CuratorID)
	sources := c.Sources
	if sources == nil {
		sources = []wireArtifactRef{}
	}
	return wireClaim{
		ID: c.ID, Version: c.Version, BrainID: c.BrainID, Text: c.Text, Sources: sources,
		Confidence: c.Confidence, Freshness: c.Freshness, Active: c.Active,
		SourceBrainID: sourceBrain, SourceClaim: sourceClaim, CuratorID: curator, Redaction: c.Redaction,
	}
}

// reconciliationRequirementCode maps one obligation kind to its wire
// requirement code for manifest and job reporting.
func reconciliationRequirementCode(kind string) string {
	if kind == reconcileWriterAck {
		return reqWriteUnknown
	}
	return reqReconciliation
}

// openReconciliation persists one durable obligation tied to a writer
// intent and emits its transition event.
func (s *Service) openReconciliation(ctx context.Context, unit contract.Unit, kind string, intent *intentRow, promotionID contract.ID, reason string, now time.Time) error {
	r := &reconciliationRow{ID: s.newID(), Kind: kind, InstallationID: intent.InstallationID, IntentID: intent.ID,
		PromotionID: promotionID, SourceBrainID: intent.BrainID, Reason: reason, State: reconciliationOpen,
		CreatedAt: now, UpdatedAt: now}
	if err := insertReconciliation(ctx, unit, r); err != nil {
		return err
	}
	return emit(ctx, unit, eventReconciliationOpened, r.ID, contract.Version(1))
}

// dispatch persists a durable writer intent and job for one governed
// Serenity command. When the target brain has no provisioned writer, the
// job stays pending with a requirement describing the missing prerequisite
// and no peer is called: R15-009 requires persisting intent before dispatch,
// never fabricating a physical call with no destination. When the brain is
// provisioned, it stamps the real configuration_revision, prepares the
// effect and opens the recoverable execution job the controller drives to
// invoke the adapter and report back through `_memory.record`.
func (s *Service) dispatch(ctx context.Context, unit contract.Unit, callerScope wireScope, kind string, brain *brainRow, bindingIDs []contract.ID, adapterCommandID contract.ID, params any, publicOp string, costBound wireMoney, expiresAt time.Time, now time.Time) (*jobRow, *intentRow, error) {
	paramsJSON, err := canonicalJSON(params)
	if err != nil {
		return nil, nil, err
	}
	payloadDigest := contract.Hash(paramsJSON)

	job := &jobRow{
		ID: s.newID(), Version: 1, InstallationID: callerScope.InstallationID, OrganizationID: callerScope.OrganizationID,
		ProjectID: callerScope.ProjectID, WorkerID: callerScope.WorkerID, TaskID: callerScope.TaskID,
		Kind: kind, State: jobPending, Operation: publicOp, OperationIDs: []contract.ID{}, BindingIDs: bindingIDs,
		Requirements: []wireRequirement{}, CreatedAt: now, UpdatedAt: now,
	}
	intent := &intentRow{
		ID: s.newID(), Kind: kind, InstallationID: callerScope.InstallationID, BrainID: brain.ID,
		AdapterCommandID: adapterCommandID, JobID: job.ID, PayloadDigest: payloadDigest, State: intentPrepared,
		CreatedAt: now, UpdatedAt: now,
	}

	if !brain.provisioned() {
		job.Requirements = []wireRequirement{{Code: reqWriterProvision,
			Message: "brain has no provisioned Serenity writer; the request is durably queued and will dispatch once one is bound"}}
		if err := insertJob(ctx, unit, job); err != nil {
			return nil, nil, err
		}
		if err := insertIntent(ctx, unit, intent); err != nil {
			return nil, nil, err
		}
		if err := emit(ctx, unit, eventJobRecorded, job.ID, contract.Version(job.Version)); err != nil {
			return nil, nil, err
		}
		if err := emit(ctx, unit, eventIntentPrepared, intent.ID, contract.Version(1)); err != nil {
			return nil, nil, err
		}
		return job, intent, nil
	}

	snap, err := s.configurationSnapshot(ctx, unit, configurationSnapshotInput{Scope: callerScope})
	if err != nil {
		return nil, nil, err
	}
	action := wireAction{
		Scope:                 callerScope,
		Tool:                  wireRef{ID: brain.ToolRefID, Version: brain.ToolRefVersion},
		Connection:            wireRef{ID: brain.ConnectionRefID, Version: brain.ConnectionRefVersion},
		AccountIdentity:       brain.WriterOwner,
		Destination:           brain.Endpoint,
		Content:               []wireArtifactRef{},
		NotBefore:             now,
		ExpiresAt:             expiresAt,
		Preconditions:         json.RawMessage(`{}`),
		ConfigurationRevision: snap.Resource.Revision,
		Parameters:            paramsJSON,
		CostBound:             costBound,
	}
	prepared, err := s.effectsPrepare(ctx, unit, effectsPrepareInput{Scope: callerScope, Action: action, SourceID: adapterCommandID})
	if err != nil {
		return nil, nil, err
	}
	intent.OperationID = prepared.Resource.ID
	job.OperationIDs = []contract.ID{prepared.Resource.ID}

	if err := insertJob(ctx, unit, job); err != nil {
		return nil, nil, err
	}
	if err := insertIntent(ctx, unit, intent); err != nil {
		return nil, nil, err
	}
	if err := emit(ctx, unit, eventIntentPrepared, intent.ID, contract.Version(1)); err != nil {
		return nil, nil, err
	}

	dispatchInput, err := canonicalJSON(struct {
		OperationID contract.ID `json:"operation_id"`
		JobID       contract.ID `json:"job_id"`
	}{prepared.Resource.ID, job.ID})
	if err != nil {
		return nil, nil, err
	}
	execJob, err := s.executionJobCreate(ctx, unit, executionJobCreateInput{
		Scope: callerScope, Owner: ownerName, Operation: publicOp, Input: dispatchInput, SourceID: adapterCommandID,
	})
	if err != nil {
		return nil, nil, err
	}
	if err := linkExecutionJob(ctx, unit, job, execJob.Resource.ID, execJob.Resource.Version, now); err != nil {
		return nil, nil, err
	}
	if err := setIntentState(ctx, unit, intent, intentDispatched, now); err != nil {
		return nil, nil, err
	}
	if err := emit(ctx, unit, eventIntentDispatched, intent.ID, contract.Version(1)); err != nil {
		return nil, nil, err
	}
	return job, intent, nil
}

// decodeSerenityEvidence validates and strictly decodes one recorded
// evidence document, checking it names the brain this intent actually
// targeted.
func decodeSerenityEvidence(raw json.RawMessage, intent *intentRow) (*serenityEvidence, error) {
	schema, err := serenityEvidenceValidationSchema()
	if err != nil {
		return nil, err
	}
	if err := contract.ValidateSchema(schema, raw); err != nil {
		return nil, invalidInput("recorded evidence does not match the Serenity evidence schema: %v", err)
	}
	var evidence serenityEvidence
	if err := contract.DecodeStrict(raw, &evidence); err != nil {
		return nil, invalidInput("recorded evidence does not decode: %v", err)
	}
	if evidence.BrainID != intent.BrainID {
		return nil, invalidInput("evidence brain %s does not match intent brain %s", evidence.BrainID, intent.BrainID)
	}
	return &evidence, nil
}

// recordBrainRevisions applies every attestation in evidence that names
// this brain, advancing its observed revision/digest/freshness.
func (s *Service) recordBrainRevisions(ctx context.Context, unit contract.Unit, brain *brainRow, evidence *serenityEvidence, now time.Time) error {
	for _, rev := range evidence.BrainRevisions {
		if rev.BrainID != brain.ID {
			continue
		}
		indexRevision := ""
		if rev.IndexRevision != nil {
			indexRevision = *rev.IndexRevision
		}
		if err := recordBrainRevision(ctx, unit, brain, rev.Revision, rev.Digest, indexRevision, rev.ObservedAt, now); err != nil {
			return err
		}
		if err := emitJSON(ctx, unit, eventBrainRevised, brain.ID, contract.Version(brain.Version),
			map[string]string{"revision": rev.Revision}); err != nil {
			return err
		}
	}
	return nil
}

// recordClaims appends every claim version in evidence as a new, immutable
// local row and returns their wire projections in evidence order.
func (s *Service) recordClaims(ctx context.Context, unit contract.Unit, evidence *serenityEvidence, now time.Time) ([]wireClaim, error) {
	claims := make([]wireClaim, 0, len(evidence.Claims))
	for _, ec := range evidence.Claims {
		row := &claimRow{BrainID: ec.BrainID, ID: ec.ID, Version: ec.Version, Text: ec.Text, Sources: ec.Sources,
			Confidence: ec.Confidence, Freshness: ec.Freshness, Active: ec.Active, RecordedAt: now}
		if ec.SourceBrainID != nil {
			row.SourceBrainID = *ec.SourceBrainID
		}
		if ec.SourceClaim != nil {
			row.SourceClaimID, row.SourceClaimVersion = ec.SourceClaim.ID, ec.SourceClaim.Version
		}
		if ec.CuratorID != nil {
			row.CuratorID = *ec.CuratorID
		}
		row.Redaction = ec.Redaction
		if err := insertClaim(ctx, unit, row); err != nil {
			return nil, err
		}
		if err := emit(ctx, unit, eventClaimRecorded, row.ID, contract.Version(row.Version)); err != nil {
			return nil, err
		}
		claims = append(claims, renderClaim(row))
	}
	return claims, nil
}

// resolveContextArtifact turns a recall's selected-context locator into a
// real ArtifactRef: an already-published locator is used as-is, a staged
// one is published from its matching StagedOutput entry. The publish scope
// is the brain's own scope, since the intent row does not retain the
// original caller's finer-grained project/task scope.
func (s *Service) resolveContextArtifact(ctx context.Context, unit contract.Unit, brain *brainRow, evidence *serenityEvidence) (wireArtifactRef, error) {
	loc := evidence.SelectedContext
	if loc == nil {
		return wireArtifactRef{}, invalidInput("evidence has no selected context for a recall")
	}
	switch loc.Kind {
	case "artifact":
		if loc.Artifact == nil {
			return wireArtifactRef{}, invalidInput("evidence selected context is missing its artifact reference")
		}
		return *loc.Artifact, nil
	case "staged":
		var staged *serenityStagedOutput
		for i := range evidence.StagedOutputs {
			if evidence.StagedOutputs[i].StagingRef == loc.StagingRef {
				staged = &evidence.StagedOutputs[i]
				break
			}
		}
		if staged == nil {
			return wireArtifactRef{}, invalidInput("evidence selected context references an unstaged locator %q", loc.StagingRef)
		}
		scope := wireScope{InstallationID: brain.InstallationID, OrganizationID: brain.OrganizationID, WorkerID: brain.WorkerID}
		published, err := s.artifactsPublish(ctx, unit, artifactsPublishInput{
			Scope: scope, Digest: staged.Digest, Size: staged.Size, MediaType: staged.MediaType,
			Classification: staged.Classification, Encrypted: true,
		})
		if err != nil {
			return wireArtifactRef{}, err
		}
		return wireArtifactRef{ID: published.Resource.ID, Digest: published.Resource.Digest}, nil
	default:
		return wireArtifactRef{}, invalidInput("evidence selected context kind %q is not supported", loc.Kind)
	}
}

// applyRecallSuccess builds a recall's completion result: cached claims,
// the published context artifact, observed brain versions and freshness.
func (s *Service) applyRecallSuccess(ctx context.Context, unit contract.Unit, brain *brainRow, evidence *serenityEvidence, now time.Time) (json.RawMessage, error) {
	if err := s.recordBrainRevisions(ctx, unit, brain, evidence, now); err != nil {
		return nil, err
	}
	claims, err := s.recordClaims(ctx, unit, evidence, now)
	if err != nil {
		return nil, err
	}
	contextArtifact, err := s.resolveContextArtifact(ctx, unit, brain, evidence)
	if err != nil {
		return nil, err
	}
	freshness := now
	if len(evidence.BrainRevisions) > 0 {
		freshness = evidence.BrainRevisions[0].ObservedAt
	}
	result := struct {
		ContextArtifact wireArtifactRef   `json:"context_artifact"`
		Claims          []wireClaim       `json:"claims"`
		BrainVersions   []wireRef         `json:"brain_versions"`
		Freshness       time.Time         `json:"freshness"`
		Requirements    []wireRequirement `json:"requirements"`
	}{contextArtifact, claims, []wireRef{{ID: brain.ID, Version: brain.Version}}, freshness, []wireRequirement{}}
	return canonicalJSON(result)
}

// applyClaimsResult builds the shared {claims, obligations} completion body
// used by remember and promote.
func (s *Service) applyClaimsResult(ctx context.Context, unit contract.Unit, brain *brainRow, evidence *serenityEvidence, now time.Time) ([]wireClaim, json.RawMessage, error) {
	if err := s.recordBrainRevisions(ctx, unit, brain, evidence, now); err != nil {
		return nil, nil, err
	}
	claims, err := s.recordClaims(ctx, unit, evidence, now)
	if err != nil {
		return nil, nil, err
	}
	result := struct {
		Claims      []wireClaim       `json:"claims"`
		Obligations []wireRequirement `json:"obligations"`
	}{claims, []wireRequirement{}}
	raw, err := canonicalJSON(result)
	return claims, raw, err
}

// applyPromoteSuccess finalizes the pending promotion lineage row with the
// destination claim's identity once Serenity confirms it, in addition to
// the shared claims/obligations result body.
func (s *Service) applyPromoteSuccess(ctx context.Context, unit contract.Unit, intent *intentRow, brain *brainRow, evidence *serenityEvidence, now time.Time) (json.RawMessage, error) {
	claims, result, err := s.applyClaimsResult(ctx, unit, brain, evidence, now)
	if err != nil {
		return nil, err
	}
	promotion, err := loadPromotionByIntent(ctx, unit, intent.ID)
	if err != nil {
		return nil, err
	}
	if len(claims) > 0 {
		dest := evidence.Claims[0]
		if err := finalizePromotion(ctx, unit, promotion, dest.ID, dest.Version, now); err != nil {
			return nil, err
		}
		if err := emit(ctx, unit, eventPromotionRecorded, promotion.ID, contract.Version(1)); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// applyRetractSuccess records the retraction's claim versions and opens a
// durable correction obligation against every downstream promotion copied
// from the retracted claim (R15-006). Retract never performs historical
// erasure; evidence claiming otherwise is a contract violation.
func (s *Service) applyRetractSuccess(ctx context.Context, unit contract.Unit, brain *brainRow, evidence *serenityEvidence, now time.Time) (json.RawMessage, error) {
	if err := s.recordBrainRevisions(ctx, unit, brain, evidence, now); err != nil {
		return nil, err
	}
	claims, err := s.recordClaims(ctx, unit, evidence, now)
	if err != nil {
		return nil, err
	}
	if evidence.HistoricalErasure != nil && *evidence.HistoricalErasure {
		return nil, invalidInput("evidence claims historical erasure, which retract never performs")
	}
	var obligations []wireRequirement
	for _, ec := range evidence.Claims {
		if ec.Active {
			continue
		}
		promotions, err := listPromotionsBySource(ctx, unit, ec.BrainID, ec.ID)
		if err != nil {
			return nil, err
		}
		for _, p := range promotions {
			exists, err := openReconciliationExists(ctx, unit, brain.InstallationID, reconcileRetraction, p.ID)
			if err != nil {
				return nil, err
			}
			if exists {
				continue
			}
			r := &reconciliationRow{ID: s.newID(), Kind: reconcileRetraction, InstallationID: brain.InstallationID,
				PromotionID: p.ID, SourceBrainID: ec.BrainID, SourceClaimID: ec.ID, SourceClaimVersion: ec.Version,
				DestinationBrainID: p.DestinationBrainID, Reason: "source claim retracted", State: reconciliationOpen,
				CreatedAt: now, UpdatedAt: now}
			if err := insertReconciliation(ctx, unit, r); err != nil {
				return nil, err
			}
			if err := emit(ctx, unit, eventReconciliationOpened, r.ID, contract.Version(1)); err != nil {
				return nil, err
			}
			obligations = append(obligations, wireRequirement{Code: reqReconciliation,
				Message: "a promoted copy of this claim needs review after retraction", ResourceID: idPtr(p.DestinationBindingID)})
		}
	}
	if obligations == nil {
		obligations = []wireRequirement{}
	}
	result := struct {
		Claims            []wireClaim       `json:"claims"`
		Obligations       []wireRequirement `json:"obligations"`
		HistoricalErasure bool              `json:"historical_erasure"`
	}{claims, obligations, false}
	return canonicalJSON(result)
}

// memoryBindingChange is the memory_binding branch of the shared Candidate
// Change oneOf: the only change kind this owner understands.
type memoryBindingChange struct {
	Kind            string            `json:"kind"`
	Action          string            `json:"action"`
	ID              contract.ID       `json:"id"`
	ExpectedVersion int64             `json:"expected_version"`
	Definition      wireMemoryBinding `json:"definition"`
}

type changeKindPeek struct {
	Kind string `json:"kind"`
}

// decodeMemoryBindingChange peeks one candidate change's discriminator
// before strictly decoding it, so a change of another owner's kind is
// reported as an ownership mismatch rather than a spurious schema error.
func decodeMemoryBindingChange(raw json.RawMessage) (*memoryBindingChange, error) {
	var peek changeKindPeek
	if err := json.Unmarshal(raw, &peek); err != nil {
		return nil, invalidInput("candidate change does not parse: %v", err)
	}
	if peek.Kind != "memory_binding" {
		return nil, invalidInput("candidate change kind %q is not owned by memory", peek.Kind)
	}
	var change memoryBindingChange
	if err := contract.DecodeStrict(raw, &change); err != nil {
		return nil, invalidInput("candidate change does not match its declared schema: %v", err)
	}
	return &change, nil
}

// buildMemoryBindingChange encodes one memory_binding change for
// `_configuration.stage`.
func buildMemoryBindingChange(action string, id contract.ID, expectedVersion int64, definition wireMemoryBinding) (json.RawMessage, error) {
	change := memoryBindingChange{Kind: "memory_binding", Action: action, ID: id, ExpectedVersion: expectedVersion, Definition: definition}
	return canonicalJSON(change)
}

// handleValidate checks the memory-owned slice of a compiler candidate
// against current storage, without mutating anything or making network
// calls: brain existence and version fences only.
func (s *Service) handleValidate(ctx context.Context, unit contract.Unit, in validateInput) (contract.Outcome[validateOutput], error) {
	var diagnostics []wireDiagnostic
	var dependencies []wireRef
	for i, raw := range in.Candidate.Changes {
		change, err := decodeMemoryBindingChange(raw)
		if err != nil {
			diagnostics = append(diagnostics, wireDiagnostic{Path: fmt.Sprintf("changes[%d]", i),
				Code: "invalid_change", Message: err.Error(), Severity: "error"})
			continue
		}
		if brain, err := loadBrain(ctx, unit, change.Definition.BrainID); err != nil {
			if !isNoRows(err) {
				return contract.Outcome[validateOutput]{}, err
			}
			diagnostics = append(diagnostics, wireDiagnostic{Path: fmt.Sprintf("changes[%d].definition.brain_id", i),
				Code: "brain_not_found", Message: "referenced brain does not exist", Severity: "error"})
		} else {
			dependencies = append(dependencies, wireRef{ID: brain.ID, Version: brain.Version})
		}
		switch change.Action {
		case "create":
			if change.ExpectedVersion != 0 {
				diagnostics = append(diagnostics, wireDiagnostic{Path: fmt.Sprintf("changes[%d].expected_version", i),
					Code: "invalid_expected_version", Message: "create requires expected_version 0", Severity: "error"})
			}
		case "update", "archive":
			existing, eerr := loadBinding(ctx, unit, change.ID)
			if eerr != nil {
				if !isNoRows(eerr) {
					return contract.Outcome[validateOutput]{}, eerr
				}
				diagnostics = append(diagnostics, wireDiagnostic{Path: fmt.Sprintf("changes[%d].id", i),
					Code: "not_found", Message: "binding does not exist", Severity: "error"})
			} else if existing.Version != change.ExpectedVersion {
				diagnostics = append(diagnostics, wireDiagnostic{Path: fmt.Sprintf("changes[%d].expected_version", i),
					Code: "stale_version", Message: "expected_version does not match the current binding version", Severity: "error"})
			}
		case "delete":
			diagnostics = append(diagnostics, wireDiagnostic{Path: fmt.Sprintf("changes[%d].action", i),
				Code: "capability_unsupported", Message: "memory_binding deletion is not supported; archive instead", Severity: "error"})
		default:
			diagnostics = append(diagnostics, wireDiagnostic{Path: fmt.Sprintf("changes[%d].action", i),
				Code: "invalid_action", Message: fmt.Sprintf("unknown action %q", change.Action), Severity: "error"})
		}
	}
	if diagnostics == nil {
		diagnostics = []wireDiagnostic{}
	}
	if dependencies == nil {
		dependencies = []wireRef{}
	}
	return completedOutcome(validateOutput{Resource: wireValidation{
		Diagnostics: diagnostics, Requirements: []wireRequirement{}, Dependencies: dependencies,
	}})
}

// handleActivate applies the memory-owned slice of a sealed candidate
// inside the compiler's own transaction. The caller (configuration or
// application) already holds activation authority; this owner only applies
// the already-validated changes.
func (s *Service) handleActivate(ctx context.Context, unit contract.Unit, in activateInput) (contract.Outcome[versionsOutput], error) {
	now := s.now()
	versions := make([]wireRef, 0, len(in.Candidate.Changes))
	for i, raw := range in.Candidate.Changes {
		change, err := decodeMemoryBindingChange(raw)
		if err != nil {
			return contract.Outcome[versionsOutput]{}, err
		}
		switch change.Action {
		case "create":
			if change.ExpectedVersion != 0 {
				return contract.Outcome[versionsOutput]{}, invalidInput("changes[%d]: create requires expected_version 0", i)
			}
			if _, err := loadBrain(ctx, unit, change.Definition.BrainID); err != nil {
				if isNoRows(err) {
					return contract.Outcome[versionsOutput]{}, notFound("changes[%d]: brain %s does not exist", i, change.Definition.BrainID)
				}
				return contract.Outcome[versionsOutput]{}, err
			}
			row := &bindingRow{ID: change.ID, Version: 1, InstallationID: change.Definition.Scope.InstallationID,
				OrganizationID: change.Definition.Scope.OrganizationID, ProjectID: change.Definition.Scope.ProjectID,
				WorkerID: change.Definition.Scope.WorkerID, TaskID: change.Definition.Scope.TaskID,
				BrainID: change.Definition.BrainID, Permissions: change.Definition.Permissions,
				Classification: change.Definition.Classification, State: bindingActive, CreatedAt: now, UpdatedAt: now}
			if err := insertBinding(ctx, unit, row); err != nil {
				return contract.Outcome[versionsOutput]{}, err
			}
			if err := emit(ctx, unit, eventBindingActivated, row.ID, contract.Version(row.Version)); err != nil {
				return contract.Outcome[versionsOutput]{}, err
			}
			versions = append(versions, wireRef{ID: row.ID, Version: row.Version})
		case "update":
			existing, err := loadBinding(ctx, unit, change.ID)
			if err != nil {
				if isNoRows(err) {
					return contract.Outcome[versionsOutput]{}, notFound("changes[%d]: binding %s does not exist", i, change.ID)
				}
				return contract.Outcome[versionsOutput]{}, err
			}
			if existing.Version != change.ExpectedVersion {
				return contract.Outcome[versionsOutput]{}, staleVersion("changes[%d]: binding %s is at version %d, not %d",
					i, change.ID, existing.Version, change.ExpectedVersion)
			}
			if _, err := loadBrain(ctx, unit, change.Definition.BrainID); err != nil {
				if isNoRows(err) {
					return contract.Outcome[versionsOutput]{}, notFound("changes[%d]: brain %s does not exist", i, change.Definition.BrainID)
				}
				return contract.Outcome[versionsOutput]{}, err
			}
			if err := updateBinding(ctx, unit, existing, change.Definition.Scope, change.Definition.BrainID,
				change.Definition.Permissions, change.Definition.Classification, now); err != nil {
				return contract.Outcome[versionsOutput]{}, err
			}
			if err := emit(ctx, unit, eventBindingActivated, existing.ID, contract.Version(existing.Version)); err != nil {
				return contract.Outcome[versionsOutput]{}, err
			}
			versions = append(versions, wireRef{ID: existing.ID, Version: existing.Version})
		case "archive":
			existing, err := loadBinding(ctx, unit, change.ID)
			if err != nil {
				if isNoRows(err) {
					return contract.Outcome[versionsOutput]{}, notFound("changes[%d]: binding %s does not exist", i, change.ID)
				}
				return contract.Outcome[versionsOutput]{}, err
			}
			if existing.Version != change.ExpectedVersion {
				return contract.Outcome[versionsOutput]{}, staleVersion("changes[%d]: binding %s is at version %d, not %d",
					i, change.ID, existing.Version, change.ExpectedVersion)
			}
			blocked, err := hasOpenObligationsForBrain(ctx, unit, existing.BrainID)
			if err != nil {
				return contract.Outcome[versionsOutput]{}, err
			}
			if blocked {
				return contract.Outcome[versionsOutput]{}, conflict("changes[%d]: binding %s has retained work or unresolved obligations", i, change.ID)
			}
			if err := setBindingState(ctx, unit, existing, bindingArchived, now); err != nil {
				return contract.Outcome[versionsOutput]{}, err
			}
			if err := emit(ctx, unit, eventBindingActivated, existing.ID, contract.Version(existing.Version)); err != nil {
				return contract.Outcome[versionsOutput]{}, err
			}
			versions = append(versions, wireRef{ID: existing.ID, Version: existing.Version})
		case "delete":
			return contract.Outcome[versionsOutput]{}, capabilityUnsupported("changes[%d]: memory_binding deletion is not supported; archive instead", i)
		default:
			return contract.Outcome[versionsOutput]{}, invalidInput("changes[%d]: unknown action %q", i, change.Action)
		}
	}
	return completedOutcome(versionsOutput{Versions: versions})
}

// handleBootstrap allocates the distinct installation, organization and
// chief-worker brains and their minimal bindings (R15-003). It is idempotent
// per scope slot: the installation brain and its installation-wide curate
// binding are created only the first time this installation is bootstrapped
// (R2.1-004's root organization); every later call -- one per newly created
// organization -- reuses the existing installation brain and grants its new
// chief only its own worker brain and the shared organization brain
// (R15-005: the personal chief curates installation-wide memory, not every
// chief).
func (s *Service) handleBootstrap(ctx context.Context, unit contract.Unit, in bootstrapInput) (contract.Outcome[bindingsOutput], error) {
	if err := s.checkInstallation(unit, in.InstallationID); err != nil {
		return contract.Outcome[bindingsOutput]{}, err
	}
	if in.OrganizationID == "" {
		return contract.Outcome[bindingsOutput]{}, invalidInput("organization_id is required")
	}
	if in.ChiefID == "" {
		return contract.Outcome[bindingsOutput]{}, invalidInput("chief_id is required")
	}
	now := s.now()

	installBrain, err := loadBrainByScope(ctx, unit, in.InstallationID, "", "", brainKindInstallation)
	isRoot := false
	if err != nil {
		if !isNoRows(err) {
			return contract.Outcome[bindingsOutput]{}, err
		}
		isRoot = true
		installBrain = &brainRow{ID: s.newID(), Version: 1, InstallationID: in.InstallationID, Kind: brainKindInstallation,
			Classification: classificationInternal, State: brainProvisioning, AllowedDestinations: []string{}, CreatedAt: now, UpdatedAt: now}
		if err := insertBrain(ctx, unit, installBrain); err != nil {
			return contract.Outcome[bindingsOutput]{}, err
		}
		if err := emit(ctx, unit, eventBrainCreated, installBrain.ID, contract.Version(installBrain.Version)); err != nil {
			return contract.Outcome[bindingsOutput]{}, err
		}
	}

	orgBrain, err := loadBrainByScope(ctx, unit, in.InstallationID, in.OrganizationID, "", brainKindOrganization)
	if err != nil {
		if !isNoRows(err) {
			return contract.Outcome[bindingsOutput]{}, err
		}
		orgBrain = &brainRow{ID: s.newID(), Version: 1, InstallationID: in.InstallationID, OrganizationID: in.OrganizationID,
			Kind: brainKindOrganization, Classification: classificationInternal, State: brainProvisioning,
			AllowedDestinations: []string{}, CreatedAt: now, UpdatedAt: now}
		if err := insertBrain(ctx, unit, orgBrain); err != nil {
			return contract.Outcome[bindingsOutput]{}, err
		}
		if err := emit(ctx, unit, eventBrainCreated, orgBrain.ID, contract.Version(orgBrain.Version)); err != nil {
			return contract.Outcome[bindingsOutput]{}, err
		}
	}

	workerBrain, err := loadBrainByScope(ctx, unit, in.InstallationID, in.OrganizationID, in.ChiefID, brainKindWorker)
	if err != nil {
		if !isNoRows(err) {
			return contract.Outcome[bindingsOutput]{}, err
		}
		workerBrain = &brainRow{ID: s.newID(), Version: 1, InstallationID: in.InstallationID, OrganizationID: in.OrganizationID,
			WorkerID: in.ChiefID, Kind: brainKindWorker, Classification: classificationInternal, State: brainProvisioning,
			AllowedDestinations: []string{}, CreatedAt: now, UpdatedAt: now}
		if err := insertBrain(ctx, unit, workerBrain); err != nil {
			return contract.Outcome[bindingsOutput]{}, err
		}
		if err := emit(ctx, unit, eventBrainCreated, workerBrain.ID, contract.Version(workerBrain.Version)); err != nil {
			return contract.Outcome[bindingsOutput]{}, err
		}
	}

	chiefScope := wireScope{InstallationID: in.InstallationID, OrganizationID: in.OrganizationID, WorkerID: in.ChiefID}
	bindings := make([]wireMemoryBinding, 0, 3)

	ownBinding := &bindingRow{ID: s.newID(), Version: 1, InstallationID: chiefScope.InstallationID,
		OrganizationID: chiefScope.OrganizationID, WorkerID: chiefScope.WorkerID, BrainID: workerBrain.ID,
		Permissions: []string{permRead, permWrite, permCurate, permPromote, permRetract},
		Classification: classificationInternal, State: bindingActive, CreatedAt: now, UpdatedAt: now}
	if err := insertBinding(ctx, unit, ownBinding); err != nil {
		return contract.Outcome[bindingsOutput]{}, err
	}
	if err := emit(ctx, unit, eventBindingActivated, ownBinding.ID, contract.Version(ownBinding.Version)); err != nil {
		return contract.Outcome[bindingsOutput]{}, err
	}
	bindings = append(bindings, renderBinding(ownBinding))

	orgBinding := &bindingRow{ID: s.newID(), Version: 1, InstallationID: chiefScope.InstallationID,
		OrganizationID: chiefScope.OrganizationID, WorkerID: chiefScope.WorkerID, BrainID: orgBrain.ID,
		Permissions: []string{permRead, permCurate, permPromote}, Classification: classificationInternal,
		State: bindingActive, CreatedAt: now, UpdatedAt: now}
	if err := insertBinding(ctx, unit, orgBinding); err != nil {
		return contract.Outcome[bindingsOutput]{}, err
	}
	if err := emit(ctx, unit, eventBindingActivated, orgBinding.ID, contract.Version(orgBinding.Version)); err != nil {
		return contract.Outcome[bindingsOutput]{}, err
	}
	bindings = append(bindings, renderBinding(orgBinding))

	if isRoot {
		installBinding := &bindingRow{ID: s.newID(), Version: 1, InstallationID: chiefScope.InstallationID,
			OrganizationID: chiefScope.OrganizationID, WorkerID: chiefScope.WorkerID, BrainID: installBrain.ID,
			Permissions: []string{permRead, permCurate, permPromote}, Classification: classificationInternal,
			State: bindingActive, CreatedAt: now, UpdatedAt: now}
		if err := insertBinding(ctx, unit, installBinding); err != nil {
			return contract.Outcome[bindingsOutput]{}, err
		}
		if err := emit(ctx, unit, eventBindingActivated, installBinding.ID, contract.Version(installBinding.Version)); err != nil {
			return contract.Outcome[bindingsOutput]{}, err
		}
		bindings = append(bindings, renderBinding(installBinding))
	}

	return completedOutcome(bindingsOutput{Bindings: bindings})
}

// handleManifest returns qualified brain revisions and unresolved
// writer/promotion obligations for paused backup/restore (R15-009).
func (s *Service) handleManifest(ctx context.Context, unit contract.Unit, in manifestInput) (contract.Outcome[manifestOutput], error) {
	if err := s.checkScope(unit, in.Scope); err != nil {
		return contract.Outcome[manifestOutput]{}, err
	}
	brains, err := listBrains(ctx, unit, in.Scope.InstallationID)
	if err != nil {
		return contract.Outcome[manifestOutput]{}, err
	}
	refs := make([]wireRef, 0, len(brains))
	for _, b := range brains {
		refs = append(refs, wireRef{ID: b.ID, Version: b.Version})
	}

	var obligations []wireRequirement
	open, err := listOpenReconciliation(ctx, unit, in.Scope.InstallationID)
	if err != nil {
		return contract.Outcome[manifestOutput]{}, err
	}
	for _, r := range open {
		obligations = append(obligations, wireRequirement{Code: reconciliationRequirementCode(r.Kind), Message: r.Reason, ResourceID: idPtr(r.PromotionID)})
	}
	pending, err := listPendingIntents(ctx, unit, in.Scope.InstallationID)
	if err != nil {
		return contract.Outcome[manifestOutput]{}, err
	}
	for _, i := range pending {
		code := reqWriterProvision
		if i.State == intentUnknown || i.State == intentDispatched {
			code = reqWriteUnknown
		}
		obligations = append(obligations, wireRequirement{Code: code,
			Message: "writer intent has not reached a terminal disposition", ResourceID: idPtr(i.BrainID)})
	}
	if obligations == nil {
		obligations = []wireRequirement{}
	}
	return completedOutcome(manifestOutput{BrainRevisions: refs, Obligations: obligations})
}

// handleSelect filters the named bindings to those the caller's scope
// currently authorizes for one permission, above one freshness bound.
func (s *Service) handleSelect(ctx context.Context, unit contract.Unit, in selectInput) (contract.Outcome[selectOutput], error) {
	if err := s.checkScope(unit, in.Scope); err != nil {
		return contract.Outcome[selectOutput]{}, err
	}
	bindings, err := s.selectBindings(ctx, unit, in.Scope, in.BindingIDs, in.Permission, in.MinimumFreshness)
	if err != nil {
		return contract.Outcome[selectOutput]{}, err
	}
	out := make([]wireMemoryBinding, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, renderBinding(b))
	}
	return completedOutcome(selectOutput{Bindings: out})
}

// execCompletionState maps one memory job's terminal state to the
// execution job disposition this owner reports through
// `_execution.job.record`.
func execCompletionState(jobState string) string {
	switch jobState {
	case jobFailed:
		return "failed"
	case jobOutcomeUnkown:
		return "outcome_unknown"
	default:
		return "succeeded"
	}
}

// handleRecord applies one controller-reported observation to the writer
// intent and job it belongs to. A late observation received after a
// terminal disposition opens a correction obligation instead of silently
// rewriting recorded claims or lineage (R15-006). Every physical call has
// exactly one intent, found by the effects operation id the controller
// names; the caller-supplied job_id is a cross-check against either this
// owner's own job id or the execution job id it opened.
func (s *Service) handleRecord(ctx context.Context, unit contract.Unit, in recordInput) (contract.Outcome[jobResourceOutput], error) {
	intent, err := loadIntentByOperation(ctx, unit, in.OperationID)
	if err != nil {
		if isNoRows(err) {
			return contract.Outcome[jobResourceOutput]{}, notFound("no memory intent is tracking operation %s", in.OperationID)
		}
		return contract.Outcome[jobResourceOutput]{}, err
	}
	job, err := loadJob(ctx, unit, intent.JobID)
	if err != nil {
		if isNoRows(err) {
			return contract.Outcome[jobResourceOutput]{}, notFound("memory job %s does not exist", intent.JobID)
		}
		return contract.Outcome[jobResourceOutput]{}, err
	}
	if in.JobID != "" && in.JobID != job.ID && in.JobID != job.ExecutionJobID {
		return contract.Outcome[jobResourceOutput]{}, invalidInput("job_id %s does not match the tracked job for operation %s", in.JobID, in.OperationID)
	}
	now := s.now()

	if intent.State == intentCompleted || intent.State == intentFailed {
		if err := s.openReconciliation(ctx, unit, reconcilePromotionCorrection, intent, "",
			"late observation received after a terminal disposition", now); err != nil {
			return contract.Outcome[jobResourceOutput]{}, err
		}
		return completedOutcome(jobResourceOutput{Resource: renderJob(job)})
	}

	brain, err := loadBrain(ctx, unit, intent.BrainID)
	if err != nil {
		return contract.Outcome[jobResourceOutput]{}, err
	}

	switch in.Observation.Disposition {
	case contract.DispositionSucceeded:
		evidence, err := decodeSerenityEvidence(in.Observation.Evidence, intent)
		if err != nil {
			return contract.Outcome[jobResourceOutput]{}, err
		}
		var result json.RawMessage
		var artifactID contract.ID
		var artifactDigest contract.Digest
		switch intent.Kind {
		case intentRecall:
			result, err = s.applyRecallSuccess(ctx, unit, brain, evidence, now)
			if err == nil && evidence.SelectedContext != nil {
				var ref wireArtifactRef
				ref, err = s.resolveContextArtifact(ctx, unit, brain, evidence)
				if err == nil {
					artifactID, artifactDigest = ref.ID, ref.Digest
				}
			}
		case intentRemember:
			_, result, err = s.applyClaimsResult(ctx, unit, brain, evidence, now)
		case intentPromote:
			result, err = s.applyPromoteSuccess(ctx, unit, intent, brain, evidence, now)
		case intentRetract:
			result, err = s.applyRetractSuccess(ctx, unit, brain, evidence, now)
		default:
			err = &contract.Fault{Code: contract.CodeInternalError, Message: fmt.Sprintf("unknown intent kind %q", intent.Kind)}
		}
		if err != nil {
			return contract.Outcome[jobResourceOutput]{}, err
		}
		if err := setIntentState(ctx, unit, intent, intentCompleted, now); err != nil {
			return contract.Outcome[jobResourceOutput]{}, err
		}
		if err := emit(ctx, unit, eventIntentRecorded, intent.ID, contract.Version(1)); err != nil {
			return contract.Outcome[jobResourceOutput]{}, err
		}
		if err := updateJob(ctx, unit, job, jobSucceeded, result, artifactID, artifactDigest, []wireRequirement{}, now); err != nil {
			return contract.Outcome[jobResourceOutput]{}, err
		}
		if err := emit(ctx, unit, eventJobRecorded, job.ID, contract.Version(job.Version)); err != nil {
			return contract.Outcome[jobResourceOutput]{}, err
		}
	case contract.DispositionFailed, contract.DispositionNotSent:
		var requirements []wireRequirement
		if in.Observation.Disposition == contract.DispositionNotSent {
			requirements = []wireRequirement{{Code: reqWriteNotSent, Message: "the write was never physically sent"}}
		} else {
			requirements = []wireRequirement{}
		}
		if err := setIntentState(ctx, unit, intent, intentFailed, now); err != nil {
			return contract.Outcome[jobResourceOutput]{}, err
		}
		if err := emit(ctx, unit, eventIntentRecorded, intent.ID, contract.Version(1)); err != nil {
			return contract.Outcome[jobResourceOutput]{}, err
		}
		if err := updateJob(ctx, unit, job, jobFailed, nil, "", "", requirements, now); err != nil {
			return contract.Outcome[jobResourceOutput]{}, err
		}
		if err := emit(ctx, unit, eventJobRecorded, job.ID, contract.Version(job.Version)); err != nil {
			return contract.Outcome[jobResourceOutput]{}, err
		}
	case contract.DispositionAccepted:
		if err := setIntentState(ctx, unit, intent, intentDispatched, now); err != nil {
			return contract.Outcome[jobResourceOutput]{}, err
		}
	case contract.DispositionUnknown:
		if err := setIntentState(ctx, unit, intent, intentUnknown, now); err != nil {
			return contract.Outcome[jobResourceOutput]{}, err
		}
		requirements := []wireRequirement{{Code: reqWriteUnknown,
			Message: "writer acknowledgement was lost; outcome is preserved as unknown pending reconciliation"}}
		if err := updateJob(ctx, unit, job, jobOutcomeUnkown, nil, "", "", requirements, now); err != nil {
			return contract.Outcome[jobResourceOutput]{}, err
		}
		if err := emit(ctx, unit, eventJobRecorded, job.ID, contract.Version(job.Version)); err != nil {
			return contract.Outcome[jobResourceOutput]{}, err
		}
		if err := s.openReconciliation(ctx, unit, reconcileWriterAck, intent, "", "writer acknowledgement unknown", now); err != nil {
			return contract.Outcome[jobResourceOutput]{}, err
		}
	default:
		return contract.Outcome[jobResourceOutput]{}, invalidInput("unknown observation disposition %q", in.Observation.Disposition)
	}

	if job.ExecutionJobID != "" && (job.State == jobSucceeded || job.State == jobFailed || job.State == jobOutcomeUnkown) {
		result := job.Result
		if result == nil {
			result = json.RawMessage(`{}`)
		}
		if _, err := s.executionJobRecord(ctx, unit, executionJobRecordInput{
			JobID: job.ExecutionJobID, ExpectedVersion: job.ExecutionJobVersion, Generation: unit.Generation(),
			State: execCompletionState(job.State), Result: result, EvidenceIDs: []contract.ID{},
		}); err != nil {
			return contract.Outcome[jobResourceOutput]{}, err
		}
	}

	return completedOutcome(jobResourceOutput{Resource: renderJob(job)})
}

// handleBindingCreate stages a typed create in configuration; only
// configuration.apply (via `_memory.activate`) makes it effective.
func (s *Service) handleBindingCreate(ctx context.Context, unit contract.Unit, in bindingCreateInput) (contract.Outcome[stagedResourceOutput], error) {
	if err := s.checkScope(unit, in.Scope); err != nil {
		return contract.Outcome[stagedResourceOutput]{}, err
	}
	if err := s.checkDefinitionScope(in.Scope, in.Definition.Scope); err != nil {
		return contract.Outcome[stagedResourceOutput]{}, err
	}
	if err := s.admitCapability(ctx, unit, in.Scope, opBindingCreate); err != nil {
		return contract.Outcome[stagedResourceOutput]{}, err
	}
	if _, err := loadBrain(ctx, unit, in.Definition.BrainID); err != nil {
		if isNoRows(err) {
			return contract.Outcome[stagedResourceOutput]{}, notFound("brain %s does not exist", in.Definition.BrainID)
		}
		return contract.Outcome[stagedResourceOutput]{}, err
	}
	newID := s.newID()
	definition := wireMemoryBinding{ID: newID, Version: 1, Scope: in.Definition.Scope, BrainID: in.Definition.BrainID,
		Permissions: in.Definition.Permissions, Classification: in.Definition.Classification}
	change, err := buildMemoryBindingChange("create", newID, 0, definition)
	if err != nil {
		return contract.Outcome[stagedResourceOutput]{}, err
	}
	staged, err := s.configurationStage(ctx, unit, configurationStageInput{Scope: in.Scope, Change: change, DraftID: in.DraftID})
	if err != nil {
		return contract.Outcome[stagedResourceOutput]{}, err
	}
	return completedOutcome(stagedResourceOutput{Draft: staged.Resource, Resource: definition})
}

// handleBindingUpdate stages a typed update in configuration.
func (s *Service) handleBindingUpdate(ctx context.Context, unit contract.Unit, in bindingUpdateInput) (contract.Outcome[stagedResourceOutput], error) {
	if err := s.checkScope(unit, in.Scope); err != nil {
		return contract.Outcome[stagedResourceOutput]{}, err
	}
	if err := s.checkDefinitionScope(in.Scope, in.Definition.Scope); err != nil {
		return contract.Outcome[stagedResourceOutput]{}, err
	}
	if err := s.admitCapability(ctx, unit, in.Scope, opBindingUpdate); err != nil {
		return contract.Outcome[stagedResourceOutput]{}, err
	}
	existing, err := loadBinding(ctx, unit, in.ID)
	if err != nil {
		if isNoRows(err) {
			return contract.Outcome[stagedResourceOutput]{}, notFound("memory binding %s does not exist", in.ID)
		}
		return contract.Outcome[stagedResourceOutput]{}, err
	}
	if existing.InstallationID != in.Scope.InstallationID {
		return contract.Outcome[stagedResourceOutput]{}, notFound("memory binding %s does not exist", in.ID)
	}
	if existing.Version != in.ExpectedVersion {
		return contract.Outcome[stagedResourceOutput]{}, staleVersion("memory binding %s is at version %d, not %d", in.ID, existing.Version, in.ExpectedVersion)
	}
	if _, err := loadBrain(ctx, unit, in.Definition.BrainID); err != nil {
		if isNoRows(err) {
			return contract.Outcome[stagedResourceOutput]{}, notFound("brain %s does not exist", in.Definition.BrainID)
		}
		return contract.Outcome[stagedResourceOutput]{}, err
	}
	definition := wireMemoryBinding{ID: in.ID, Version: in.ExpectedVersion, Scope: in.Definition.Scope, BrainID: in.Definition.BrainID,
		Permissions: in.Definition.Permissions, Classification: in.Definition.Classification}
	change, err := buildMemoryBindingChange("update", in.ID, in.ExpectedVersion, definition)
	if err != nil {
		return contract.Outcome[stagedResourceOutput]{}, err
	}
	staged, err := s.configurationStage(ctx, unit, configurationStageInput{Scope: in.Scope, Change: change, DraftID: in.DraftID})
	if err != nil {
		return contract.Outcome[stagedResourceOutput]{}, err
	}
	return completedOutcome(stagedResourceOutput{Draft: staged.Resource, Resource: definition})
}

// handleBindingArchive loads the current binding to complete its archive
// change definition, then stages it in configuration. Explicit archive
// checks retained work and obligations at activation time; no omission
// deletion.
func (s *Service) handleBindingArchive(ctx context.Context, unit contract.Unit, in bindingArchiveInput) (contract.Outcome[stagedResourceOutput], error) {
	if err := s.checkScope(unit, in.Scope); err != nil {
		return contract.Outcome[stagedResourceOutput]{}, err
	}
	if err := s.admitCapability(ctx, unit, in.Scope, opBindingArchive); err != nil {
		return contract.Outcome[stagedResourceOutput]{}, err
	}
	existing, err := loadBinding(ctx, unit, in.ID)
	if err != nil {
		if isNoRows(err) {
			return contract.Outcome[stagedResourceOutput]{}, notFound("memory binding %s does not exist", in.ID)
		}
		return contract.Outcome[stagedResourceOutput]{}, err
	}
	if existing.InstallationID != in.Scope.InstallationID {
		return contract.Outcome[stagedResourceOutput]{}, notFound("memory binding %s does not exist", in.ID)
	}
	if existing.Version != in.ExpectedVersion {
		return contract.Outcome[stagedResourceOutput]{}, staleVersion("memory binding %s is at version %d, not %d", in.ID, existing.Version, in.ExpectedVersion)
	}
	definition := wireMemoryBinding{ID: existing.ID, Version: existing.Version, Scope: existing.scope(), BrainID: existing.BrainID,
		Permissions: existing.Permissions, Classification: existing.Classification}
	change, err := buildMemoryBindingChange("archive", in.ID, in.ExpectedVersion, definition)
	if err != nil {
		return contract.Outcome[stagedResourceOutput]{}, err
	}
	staged, err := s.configurationStage(ctx, unit, configurationStageInput{Scope: in.Scope, Change: change, DraftID: in.DraftID})
	if err != nil {
		return contract.Outcome[stagedResourceOutput]{}, err
	}
	return completedOutcome(stagedResourceOutput{Draft: staged.Resource, Resource: definition})
}

// handleBindingGet resolves one binding under current authorization; a
// cross-scope or foreign binding is not_found, never permission_denied, to
// avoid disclosing cross-scope existence.
func (s *Service) handleBindingGet(ctx context.Context, unit contract.Unit, in bindingGetInput) (contract.Outcome[bindingResourceOutput], error) {
	if err := s.checkScope(unit, in.Scope); err != nil {
		return contract.Outcome[bindingResourceOutput]{}, err
	}
	b, err := loadBinding(ctx, unit, in.ID)
	if err != nil {
		if isNoRows(err) {
			return contract.Outcome[bindingResourceOutput]{}, notFound("memory binding %s does not exist", in.ID)
		}
		return contract.Outcome[bindingResourceOutput]{}, err
	}
	if b.InstallationID != in.Scope.InstallationID || !scopeContains(b.scope(), in.Scope) {
		return contract.Outcome[bindingResourceOutput]{}, notFound("memory binding %s does not exist", in.ID)
	}
	return completedOutcome(bindingResourceOutput{Resource: renderBinding(b)})
}

// handleInspect is the authorized local read facade over one cached claim:
// no external paid work. A claim never recalled locally directs the caller
// to memory.recall instead of fabricating a result.
func (s *Service) handleInspect(ctx context.Context, unit contract.Unit, in inspectInput) (contract.Outcome[claimResourceOutput], error) {
	if err := s.checkScope(unit, in.Scope); err != nil {
		return contract.Outcome[claimResourceOutput]{}, err
	}
	if _, err := s.findAuthorizingBinding(ctx, unit, in.Scope, in.BrainID, permRead); err != nil {
		return contract.Outcome[claimResourceOutput]{}, err
	}
	claim, err := loadLatestClaim(ctx, unit, in.BrainID, in.ClaimID)
	if err != nil {
		if isNoRows(err) {
			return contract.Outcome[claimResourceOutput]{}, prerequisiteMissing("claim %s is not cached locally; run memory.recall to populate it", in.ClaimID)
		}
		return contract.Outcome[claimResourceOutput]{}, err
	}
	return completedOutcome(claimResourceOutput{Resource: renderClaim(claim)})
}

// handleJobGet inspects one memory job's actual disposition, prerequisites,
// context artifact and unresolved requirements.
func (s *Service) handleJobGet(ctx context.Context, unit contract.Unit, in jobGetInput) (contract.Outcome[jobResourceOutput], error) {
	if err := s.checkScope(unit, in.Scope); err != nil {
		return contract.Outcome[jobResourceOutput]{}, err
	}
	job, err := loadJob(ctx, unit, in.ID)
	if err != nil {
		if isNoRows(err) {
			return contract.Outcome[jobResourceOutput]{}, notFound("memory job %s does not exist", in.ID)
		}
		return contract.Outcome[jobResourceOutput]{}, err
	}
	if job.InstallationID != in.Scope.InstallationID {
		return contract.Outcome[jobResourceOutput]{}, notFound("memory job %s does not exist", in.ID)
	}
	return completedOutcome(jobResourceOutput{Resource: renderJob(job)})
}

// checkArtifacts validates scope, availability and classification of pinned
// source artifacts before they are disclosed to a Serenity writer.
func (s *Service) checkArtifacts(ctx context.Context, unit contract.Unit, scope wireScope, sources []wireArtifactRef) error {
	body, err := s.artifactsMetadata(ctx, unit, artifactsMetadataInput{Scope: scope, Artifacts: sources})
	if err != nil {
		return err
	}
	found := make(map[contract.ID]string, len(body.Artifacts))
	for _, a := range body.Artifacts {
		found[a.ID] = a.State
	}
	for _, ref := range sources {
		state, ok := found[ref.ID]
		if !ok {
			return notFound("source artifact %s is not available", ref.ID)
		}
		if state != "available" {
			return prerequisiteMissing("source artifact %s is not available", ref.ID)
		}
	}
	return nil
}

// handleRecall authorizes every named binding for read and one shared
// brain, then governs a Serenity recall through the durable dispatch
// pipeline. Authorization happens before any retrieval or model
// composition (R15-004/R15-007).
func (s *Service) handleRecall(ctx context.Context, unit contract.Unit, in recallInput) (contract.Outcome[jobResourceOutput], error) {
	if err := s.checkScope(unit, in.Scope); err != nil {
		return contract.Outcome[jobResourceOutput]{}, err
	}
	if len(in.BindingIDs) == 0 {
		return contract.Outcome[jobResourceOutput]{}, invalidInput("binding_ids must name at least one authorized binding")
	}
	if err := s.admitCapability(ctx, unit, in.Scope, opRecall); err != nil {
		return contract.Outcome[jobResourceOutput]{}, err
	}
	bindings, err := s.selectBindings(ctx, unit, in.Scope, in.BindingIDs, permRead, in.MinimumFreshness)
	if err != nil {
		return contract.Outcome[jobResourceOutput]{}, err
	}
	brainID := bindings[0].BrainID
	for _, b := range bindings[1:] {
		if b.BrainID != brainID {
			return contract.Outcome[jobResourceOutput]{}, invalidInput("binding_ids must resolve to exactly one brain")
		}
	}
	brain, err := loadBrain(ctx, unit, brainID)
	if err != nil {
		return contract.Outcome[jobResourceOutput]{}, err
	}
	now := s.now()
	adapterCommandID := s.newID()
	params := serenityRecall{
		Schema: serenitySchema, BrainID: brain.ID, AdapterCommandID: adapterCommandID, Kind: intentRecall,
		Query: in.Query, MinimumFreshness: in.MinimumFreshness, MaxClaims: 4096,
		MaximumCost:         wireMoney{Currency: in.Limits.Currency, MicroUnits: in.Limits.SpendMicroUnits},
		AllowedDestinations: brain.AllowedDestinations, Classification: brain.Classification,
	}
	costBound := wireMoney{Currency: in.Limits.Currency, MicroUnits: in.Limits.SpendMicroUnits}
	job, _, err := s.dispatch(ctx, unit, in.Scope, intentRecall, brain, in.BindingIDs, adapterCommandID, params, opRecall, costBound, in.Limits.RootDeadline, now)
	if err != nil {
		return contract.Outcome[jobResourceOutput]{}, err
	}
	return contract.Outcome[jobResourceOutput]{Status: contract.StatusAccepted, Data: jobResourceOutput{Resource: renderJob(job)}}, nil
}

// handleRemember authorizes the named binding for write, validates any
// pinned sources are available, then governs a Serenity remember through
// the durable dispatch pipeline.
func (s *Service) handleRemember(ctx context.Context, unit contract.Unit, in rememberInput) (contract.Outcome[jobResourceOutput], error) {
	if err := s.checkScope(unit, in.Scope); err != nil {
		return contract.Outcome[jobResourceOutput]{}, err
	}
	if err := s.admitCapability(ctx, unit, in.Scope, opRemember); err != nil {
		return contract.Outcome[jobResourceOutput]{}, err
	}
	binding, err := s.authorizeBinding(ctx, unit, in.Scope, in.BindingID, permWrite)
	if err != nil {
		return contract.Outcome[jobResourceOutput]{}, err
	}
	brain, err := loadBrain(ctx, unit, binding.BrainID)
	if err != nil {
		return contract.Outcome[jobResourceOutput]{}, err
	}
	if brain.State == brainUnavailable {
		return contract.Outcome[jobResourceOutput]{}, prerequisiteMissing("brain %s is unavailable", brain.ID)
	}
	if len(in.Sources) > 0 {
		if err := s.checkArtifacts(ctx, unit, in.Scope, in.Sources); err != nil {
			return contract.Outcome[jobResourceOutput]{}, err
		}
	}
	now := s.now()
	adapterCommandID := s.newID()
	params := serenityRemember{
		Schema: serenitySchema, BrainID: brain.ID, AdapterCommandID: adapterCommandID, Kind: intentRemember,
		Text: in.Text, Sources: in.Sources, WriterOwner: brain.WriterOwner,
		MaximumCost:         wireMoney{Currency: in.Limits.Currency, MicroUnits: in.Limits.SpendMicroUnits},
		AllowedDestinations: brain.AllowedDestinations,
	}
	costBound := wireMoney{Currency: in.Limits.Currency, MicroUnits: in.Limits.SpendMicroUnits}
	job, _, err := s.dispatch(ctx, unit, in.Scope, intentRemember, brain, []contract.ID{binding.ID}, adapterCommandID, params, opRemember, costBound, in.Limits.RootDeadline, now)
	if err != nil {
		return contract.Outcome[jobResourceOutput]{}, err
	}
	return contract.Outcome[jobResourceOutput]{Status: contract.StatusAccepted, Data: jobResourceOutput{Resource: renderJob(job)}}, nil
}

// handlePromote requires both source disclosure authority (a binding on the
// source brain granting promote) and destination write authority (a
// binding on the destination granting write or curate), then governs a
// Serenity promote that links the new destination claim to its source
// brain, claim version, curator and redaction (R15-005).
func (s *Service) handlePromote(ctx context.Context, unit contract.Unit, in promoteInput) (contract.Outcome[jobResourceOutput], error) {
	if err := s.checkScope(unit, in.Scope); err != nil {
		return contract.Outcome[jobResourceOutput]{}, err
	}
	if err := s.admitCapability(ctx, unit, in.Scope, opPromote); err != nil {
		return contract.Outcome[jobResourceOutput]{}, err
	}
	if _, err := s.findAuthorizingBinding(ctx, unit, in.Scope, in.SourceBrainID, permPromote); err != nil {
		return contract.Outcome[jobResourceOutput]{}, err
	}
	destBinding, err := s.authorizeBinding(ctx, unit, in.Scope, in.DestinationBindingID, permWrite)
	if err != nil {
		destBinding, err = s.authorizeBinding(ctx, unit, in.Scope, in.DestinationBindingID, permCurate)
		if err != nil {
			return contract.Outcome[jobResourceOutput]{}, err
		}
	}
	if _, err := loadBrain(ctx, unit, in.SourceBrainID); err != nil {
		if isNoRows(err) {
			return contract.Outcome[jobResourceOutput]{}, notFound("source brain %s does not exist", in.SourceBrainID)
		}
		return contract.Outcome[jobResourceOutput]{}, err
	}
	destBrain, err := loadBrain(ctx, unit, destBinding.BrainID)
	if err != nil {
		return contract.Outcome[jobResourceOutput]{}, err
	}
	sourceClaim, err := loadLatestClaim(ctx, unit, in.SourceBrainID, in.SourceClaim.ID)
	if err != nil {
		if isNoRows(err) {
			return contract.Outcome[jobResourceOutput]{}, prerequisiteMissing("source claim %s is not cached locally; recall it first", in.SourceClaim.ID)
		}
		return contract.Outcome[jobResourceOutput]{}, err
	}
	if sourceClaim.Version != in.SourceClaim.Version {
		return contract.Outcome[jobResourceOutput]{}, staleVersion("source claim %s is at version %d, not %d", in.SourceClaim.ID, sourceClaim.Version, in.SourceClaim.Version)
	}
	if !sourceClaim.Active {
		return contract.Outcome[jobResourceOutput]{}, conflict("source claim %s has been retracted", in.SourceClaim.ID)
	}
	now := s.now()
	adapterCommandID := s.newID()
	curatorID := unit.Actor().PrincipalID
	params := serenityPromote{
		Schema: serenitySchema, BrainID: destBrain.ID, AdapterCommandID: adapterCommandID, Kind: intentPromote,
		SourceBrainID: in.SourceBrainID, SourceClaim: in.SourceClaim, Text: sourceClaim.Text, Sources: sourceClaim.Sources,
		CuratorID: curatorID, WriterOwner: destBrain.WriterOwner,
		MaximumCost:         wireMoney{Currency: in.Limits.Currency, MicroUnits: in.Limits.SpendMicroUnits},
		AllowedDestinations: destBrain.AllowedDestinations, Redaction: in.Redaction,
	}
	costBound := wireMoney{Currency: in.Limits.Currency, MicroUnits: in.Limits.SpendMicroUnits}
	job, intent, err := s.dispatch(ctx, unit, in.Scope, intentPromote, destBrain, []contract.ID{destBinding.ID}, adapterCommandID, params, opPromote, costBound, in.Limits.RootDeadline, now)
	if err != nil {
		return contract.Outcome[jobResourceOutput]{}, err
	}

	promotion := &promotionRow{ID: s.newID(), IntentID: intent.ID, JobID: job.ID, SourceBrainID: in.SourceBrainID,
		SourceClaimID: in.SourceClaim.ID, SourceClaimVersion: in.SourceClaim.Version, DestinationBindingID: destBinding.ID,
		DestinationBrainID: destBrain.ID, CuratorID: curatorID, Redaction: in.Redaction, State: "pending", CreatedAt: now, UpdatedAt: now}
	if err := insertPromotion(ctx, unit, promotion); err != nil {
		return contract.Outcome[jobResourceOutput]{}, err
	}
	if err := emit(ctx, unit, eventPromotionRecorded, promotion.ID, contract.Version(1)); err != nil {
		return contract.Outcome[jobResourceOutput]{}, err
	}
	return contract.Outcome[jobResourceOutput]{Status: contract.StatusAccepted, Data: jobResourceOutput{Resource: renderJob(job)}}, nil
}

// handleRetract requires a binding on the named brain granting retract,
// then governs a Serenity retract. retract's wire contract carries no
// caller Limits, so dispatch uses a nominal zero-cost bound and a short
// validity window rather than inventing an installation default currency
// (out of this package's scope; see the final report).
func (s *Service) handleRetract(ctx context.Context, unit contract.Unit, in retractInput) (contract.Outcome[jobResourceOutput], error) {
	if err := s.checkScope(unit, in.Scope); err != nil {
		return contract.Outcome[jobResourceOutput]{}, err
	}
	if err := s.admitCapability(ctx, unit, in.Scope, opRetract); err != nil {
		return contract.Outcome[jobResourceOutput]{}, err
	}
	binding, err := s.findAuthorizingBinding(ctx, unit, in.Scope, in.BrainID, permRetract)
	if err != nil {
		return contract.Outcome[jobResourceOutput]{}, err
	}
	brain, err := loadBrain(ctx, unit, in.BrainID)
	if err != nil {
		if isNoRows(err) {
			return contract.Outcome[jobResourceOutput]{}, notFound("brain %s does not exist", in.BrainID)
		}
		return contract.Outcome[jobResourceOutput]{}, err
	}
	claim, err := loadLatestClaim(ctx, unit, in.BrainID, in.Claim.ID)
	if err != nil {
		if isNoRows(err) {
			return contract.Outcome[jobResourceOutput]{}, notFound("claim %s is not cached locally", in.Claim.ID)
		}
		return contract.Outcome[jobResourceOutput]{}, err
	}
	if claim.Version != in.Claim.Version {
		return contract.Outcome[jobResourceOutput]{}, staleVersion("claim %s is at version %d, not %d", in.Claim.ID, claim.Version, in.Claim.Version)
	}
	now := s.now()
	adapterCommandID := s.newID()
	params := serenityRetract{Schema: serenitySchema, BrainID: brain.ID, AdapterCommandID: adapterCommandID, Kind: intentRetract,
		Claim: in.Claim, Reason: in.Reason, WriterOwner: brain.WriterOwner, Removal: "active_recall"}
	costBound := wireMoney{Currency: "USD", MicroUnits: 0}
	job, _, err := s.dispatch(ctx, unit, in.Scope, intentRetract, brain, []contract.ID{binding.ID}, adapterCommandID, params, opRetract, costBound, now.Add(time.Hour), now)
	if err != nil {
		return contract.Outcome[jobResourceOutput]{}, err
	}
	return contract.Outcome[jobResourceOutput]{Status: contract.StatusAccepted, Data: jobResourceOutput{Resource: renderJob(job)}}, nil
}
