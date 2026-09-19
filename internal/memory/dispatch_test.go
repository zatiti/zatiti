package memory

import (
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// validPhysicalCall builds one schema-valid PhysicalCallEvidence document
// with a fixed, syntactically valid but fake digest/UUID; the fake ports
// never re-validate its content, only this owner's own decode does.
func validPhysicalCall(now time.Time) serenityPhysicalCall {
	fakeDigest := contract.Digest("0000000000000000000000000000000000000000000000000000000000000000"[:64])
	fakeArtifact := wireArtifactRef{ID: "00000000-0000-4000-8000-000000000001", Digest: fakeDigest}
	return serenityPhysicalCall{
		OperationID: "00000000-0000-4000-8000-000000000002", AttemptID: "00000000-0000-4000-8000-000000000003",
		AccountIdentity: "serenity-writer", RequestedDestination: "serenity://test", ResolvedDestination: "serenity://test",
		ProfileDigest: fakeDigest, CapabilityEvidence: fakeArtifact, StartedAt: now, FinishedAt: now,
		RequestContext: fakeArtifact, RequestSent: "yes", Confirmation: "authoritative_success",
	}
}

// recallEvidence builds one schema-valid succeeded recall evidence
// document: one claim, one brain revision, and a staged context locator.
func recallEvidence(brain *brainRow, adapterCommandID contract.ID, claim *serenityMemoryClaim, now time.Time) serenityEvidence {
	fakeDigest := contract.Digest("0000000000000000000000000000000000000000000000000000000000000000"[:64])
	return serenityEvidence{
		Schema: serenityEvidenceSchema, PhysicalCall: validPhysicalCall(now), Kind: intentRecall,
		BrainID: brain.ID, AdapterCommandID: adapterCommandID, CommandStatus: serenityCommandCompleted,
		Claims:         []serenityMemoryClaim{*claim},
		BrainRevisions: []serenityBrainRevision{{BrainID: brain.ID, Revision: "rev-1", Digest: fakeDigest, ObservedAt: now}},
		Usage:          serenityProviderUsage{Accounting: wireUsage{Currency: "USD"}, Billing: "no_charge"},
		StagedOutputs: []serenityStagedOutput{{StagingRef: "stage-1", Digest: fakeDigest, Size: 42,
			MediaType: "text/plain", Classification: classificationInternal, Purpose: "context"}},
		OutputArtifacts: []wireArtifactRef{},
		SelectedContext: &serenityArtifactLocator{Kind: "staged", StagingRef: "stage-1", Digest: fakeDigest},
	}
}

func newMemoryClaim(brainID, claimID contract.ID, version int64, active bool, freshness time.Time) *serenityMemoryClaim {
	return &serenityMemoryClaim{ID: claimID, Version: version, BrainID: brainID, Text: "the sky is blue",
		Sources: []wireArtifactRef{}, Confidence: 900000, Freshness: freshness, Active: active}
}

// TestRecallBoundedWhenUnprovisioned proves recall never fabricates a
// physical dispatch against a brain with no bound Serenity writer: the
// request is durably queued (a pending job naming the missing prerequisite)
// and neither `_effects.prepare` nor `_execution.job.create` is called.
func TestRecallBoundedWhenUnprovisioned(t *testing.T) {
	e := newEnv(t)
	org, worker := e.ids.New(), e.ids.New()
	brain := &brainRow{ID: e.ids.New(), Version: 1, InstallationID: e.install, OrganizationID: org, WorkerID: worker,
		Kind: brainKindWorker, Classification: classificationInternal, State: brainProvisioning,
		AllowedDestinations: []string{}, CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	e.seedBrain(brain)
	binding := &bindingRow{ID: e.ids.New(), Version: 1, InstallationID: e.install, OrganizationID: org, WorkerID: worker,
		BrainID: brain.ID, Permissions: []string{permRead}, Classification: classificationInternal, State: bindingActive,
		CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	e.seedBinding(binding)

	scope := e.scopeAt(org, worker)
	e.ports.resetCalls()
	payload, err := e.callAs(e.actor, scope, opRecall, recallInput{
		Scope: wireScope{InstallationID: e.install, OrganizationID: org, WorkerID: worker}, Query: "anything",
		BindingIDs: []contract.ID{binding.ID}, MinimumFreshness: time.Time{},
		Limits: wireLimits{Currency: "USD", SpendMicroUnits: 100, Concurrency: 1, ModelSteps: 1, AttemptSeconds: 60, RootDeadline: e.clock.Now().Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("recall on an unprovisioned brain: %v", err)
	}
	if payload.Status != contract.StatusAccepted {
		t.Fatalf("status = %q, want accepted", payload.Status)
	}
	var out jobResourceOutput
	e.decodePayload(payload, &out)
	if out.Resource.State != jobPending {
		t.Fatalf("job state = %q, want pending", out.Resource.State)
	}
	found := false
	for _, r := range out.Resource.Requirements {
		if r.Code == reqWriterProvision {
			found = true
		}
	}
	if !found {
		t.Fatalf("job requirements = %+v, want %s", out.Resource.Requirements, reqWriterProvision)
	}
	for _, op := range e.ports.opsCalled() {
		if op == opEffectsPrepare || op == opExecutionJobCreate {
			t.Fatalf("recall on an unprovisioned brain called %s; bounded recall must never dispatch with no writer", op)
		}
	}
}

// TestRecallProvisionedResolvesContextArtifact runs a full governed recall
// against a provisioned brain, then simulates the controller's success
// report through `_memory.record` and proves the recall's job result
// carries a real, published context artifact plus the cached claim
// (R15-007).
func TestRecallProvisionedResolvesContextArtifact(t *testing.T) {
	e := newEnv(t)
	org, worker := e.ids.New(), e.ids.New()
	brain := e.provisionedBrain(brainKindWorker, org, worker)
	e.seedBrain(brain)
	binding := &bindingRow{ID: e.ids.New(), Version: 1, InstallationID: e.install, OrganizationID: org, WorkerID: worker,
		BrainID: brain.ID, Permissions: []string{permRead}, Classification: classificationInternal, State: bindingActive,
		CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	e.seedBinding(binding)

	scope := e.scopeAt(org, worker)
	payload, err := e.callAs(e.actor, scope, opRecall, recallInput{
		Scope: wireScope{InstallationID: e.install, OrganizationID: org, WorkerID: worker}, Query: "anything",
		BindingIDs: []contract.ID{binding.ID}, MinimumFreshness: time.Time{},
		Limits: wireLimits{Currency: "USD", SpendMicroUnits: 100, Concurrency: 1, ModelSteps: 1, AttemptSeconds: 60, RootDeadline: e.clock.Now().Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	var out jobResourceOutput
	e.decodePayload(payload, &out)
	if out.Resource.OperationID == nil {
		t.Fatalf("provisioned recall did not prepare an effects operation")
	}
	for _, want := range []string{opPolicyCheck, opConfigSnapshot, opEffectsPrepare, opExecutionJobCreate} {
		ok := false
		for _, got := range e.ports.opsCalled() {
			if got == want {
				ok = true
			}
		}
		if !ok {
			t.Fatalf("provisioned recall never called %s; calls were %v", want, e.ports.opsCalled())
		}
	}

	claim := newMemoryClaim(brain.ID, e.ids.New(), 1, true, e.clock.Now())
	// The handler only checks the evidence's brain_id against the intent it
	// looked up by operation_id; adapter_command_id is carried for audit but
	// not re-verified here, so a fixed placeholder is accepted.
	evidence := recallEvidence(brain, "00000000-0000-4000-8000-000000000099", claim, e.clock.Now())

	recordPayload, err := e.callAs(e.actor, scope, opRecord, recordInput{
		JobID: out.Resource.ID, OperationID: *out.Resource.OperationID,
		Observation: wireObservation{Disposition: contract.DispositionSucceeded, Evidence: mustMarshal(t, evidence), Usage: wireUsage{Currency: "USD"}},
	})
	if err != nil {
		t.Fatalf("record success: %v", err)
	}
	var recorded jobResourceOutput
	e.decodePayload(recordPayload, &recorded)
	if recorded.Resource.State != jobSucceeded {
		t.Fatalf("job state after record = %q, want succeeded", recorded.Resource.State)
	}
	if recorded.Resource.ResultArtifact == nil {
		t.Fatalf("job result has no context artifact")
	}
	var result struct {
		ContextArtifact wireArtifactRef `json:"context_artifact"`
		Claims          []wireClaim     `json:"claims"`
	}
	if err := jsonUnmarshal(recorded.Resource.Result, &result); err != nil {
		t.Fatalf("decode job result: %v", err)
	}
	if len(result.Claims) != 1 || result.Claims[0].ID != claim.ID {
		t.Fatalf("job result claims = %+v, want one claim %s", result.Claims, claim.ID)
	}
	if result.ContextArtifact.ID == "" {
		t.Fatalf("job result context_artifact is empty")
	}

	// The claim is now locally inspectable without any further paid work.
	inspectPayload, err := e.callAs(e.actor, scope, opInspect, inspectInput{
		Scope: wireScope{InstallationID: e.install, OrganizationID: org, WorkerID: worker}, BrainID: brain.ID, ClaimID: claim.ID,
	})
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	var inspected claimResourceOutput
	e.decodePayload(inspectPayload, &inspected)
	if inspected.Resource.Text != claim.Text {
		t.Fatalf("inspected claim text = %q, want %q", inspected.Resource.Text, claim.Text)
	}
}

// TestRecordClosesExecutionJobAsOwner pins the network-job protocol with the
// execution owner: the job memory opens names the prepared effects
// operation in its input (execution exposes it as Job.operation_id, which
// is how the controller routes the effect's outcome back here), nothing
// claims that job, and memory records it at the version create returned
// under the current generation — the only claim an unclaimed job binds.
func TestRecordClosesExecutionJobAsOwner(t *testing.T) {
	e := newEnv(t)
	e.ports.setExecJobVersion(4)
	org, worker := e.ids.New(), e.ids.New()
	brain := e.provisionedBrain(brainKindWorker, org, worker)
	e.seedBrain(brain)
	binding := &bindingRow{ID: e.ids.New(), Version: 1, InstallationID: e.install, OrganizationID: org, WorkerID: worker,
		BrainID: brain.ID, Permissions: []string{permRead}, Classification: classificationInternal, State: bindingActive,
		CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	e.seedBinding(binding)

	scope := e.scopeAt(org, worker)
	payload, err := e.callAs(e.actor, scope, opRecall, recallInput{
		Scope: wireScope{InstallationID: e.install, OrganizationID: org, WorkerID: worker}, Query: "anything",
		BindingIDs: []contract.ID{binding.ID}, MinimumFreshness: time.Time{},
		Limits: wireLimits{Currency: "USD", SpendMicroUnits: 100, Concurrency: 1, ModelSteps: 1, AttemptSeconds: 60, RootDeadline: e.clock.Now().Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	var out jobResourceOutput
	e.decodePayload(payload, &out)

	creates := e.ports.inputsFor(opExecutionJobCreate)
	if len(creates) != 1 {
		t.Fatalf("job.create called %d times, want once", len(creates))
	}
	var created struct {
		Owner string `json:"owner"`
		Input struct {
			OperationID contract.ID `json:"operation_id"`
			JobID       contract.ID `json:"job_id"`
		} `json:"input"`
	}
	if err := jsonUnmarshal(creates[0], &created); err != nil {
		t.Fatalf("decode job.create input: %v", err)
	}
	if created.Owner != ownerName || created.Input.OperationID != *out.Resource.OperationID || created.Input.JobID != out.Resource.ID {
		t.Fatalf("job.create input %+v, want owner %s naming operation %s and job %s",
			created, ownerName, *out.Resource.OperationID, out.Resource.ID)
	}

	var stored *jobRow
	err = e.db.Read(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		var err error
		stored, err = loadJob(e.ctx, unit, out.Resource.ID)
		return err
	})
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if stored.ExecutionJobID == "" || stored.ExecutionJobVersion != 4 {
		t.Fatalf("stored execution job link %s@%d, want the id and version create returned", stored.ExecutionJobID, stored.ExecutionJobVersion)
	}

	claim := newMemoryClaim(brain.ID, e.ids.New(), 1, true, e.clock.Now())
	evidence := recallEvidence(brain, "00000000-0000-4000-8000-000000000099", claim, e.clock.Now())
	generation, err := e.db.Generation(e.ctx)
	if err != nil {
		t.Fatalf("generation: %v", err)
	}
	if _, err := e.callAs(e.actor, scope, opRecord, recordInput{
		JobID: stored.ExecutionJobID, OperationID: *out.Resource.OperationID,
		Observation: wireObservation{Disposition: contract.DispositionSucceeded, Evidence: mustMarshal(t, evidence), Usage: wireUsage{Currency: "USD"}},
	}); err != nil {
		t.Fatalf("record success: %v", err)
	}

	records := e.ports.inputsFor(opExecutionJobRecord)
	if len(records) != 1 {
		t.Fatalf("job.record called %d times, want once", len(records))
	}
	var recorded executionJobRecordInput
	if err := jsonUnmarshal(records[0], &recorded); err != nil {
		t.Fatalf("decode job.record input: %v", err)
	}
	if recorded.JobID != stored.ExecutionJobID || recorded.ExpectedVersion != 4 || recorded.Generation != generation || recorded.State != "succeeded" {
		t.Fatalf("job.record input %+v, want job %s at version 4 under generation %d succeeded",
			recorded, stored.ExecutionJobID, generation)
	}
	if !containsOp(e.ports.opsCalled(), opExecutionJobRecord) || containsOp(e.ports.opsCalled(), "_execution.job.claim") {
		t.Fatalf("memory calls %v: the owner records its network job and never claims it", e.ports.opsCalled())
	}
}

func containsOp(ops []string, want string) bool {
	for _, op := range ops {
		if op == want {
			return true
		}
	}
	return false
}

// TestRecallDeniedByPolicy proves an explicit policy deny wins before any
// dispatch: recall never prepares an effect or opens an execution job once
// `_policy.check` refuses the capability.
func TestRecallDeniedByPolicy(t *testing.T) {
	e := newEnv(t)
	org, worker := e.ids.New(), e.ids.New()
	brain := e.provisionedBrain(brainKindWorker, org, worker)
	e.seedBrain(brain)
	binding := &bindingRow{ID: e.ids.New(), Version: 1, InstallationID: e.install, OrganizationID: org, WorkerID: worker,
		BrainID: brain.ID, Permissions: []string{permRead}, Classification: classificationInternal, State: bindingActive,
		CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	e.seedBinding(binding)
	e.ports.setPolicy("deny", "capability withheld by installation policy")

	scope := e.scopeAt(org, worker)
	e.ports.resetCalls()
	_, err := e.callAs(e.actor, scope, opRecall, recallInput{
		Scope: wireScope{InstallationID: e.install, OrganizationID: org, WorkerID: worker}, Query: "anything",
		BindingIDs: []contract.ID{binding.ID}, MinimumFreshness: time.Time{},
		Limits: wireLimits{Currency: "USD", SpendMicroUnits: 100, Concurrency: 1, ModelSteps: 1, AttemptSeconds: 60, RootDeadline: e.clock.Now().Add(time.Hour)},
	})
	if f := decodeFault(err); f == nil || f.Code != contract.CodePermissionDenied {
		t.Fatalf("recall denied by policy: got err=%v, want permission_denied", err)
	}
	for _, op := range e.ports.opsCalled() {
		if op == opEffectsPrepare || op == opExecutionJobCreate {
			t.Fatalf("policy-denied recall called %s; explicit deny must win before dispatch", op)
		}
	}
}

// TestRememberRejectsUnavailableSource proves a pinned source artifact's
// availability is checked before it can be disclosed to a Serenity writer.
func TestRememberRejectsUnavailableSource(t *testing.T) {
	e := newEnv(t)
	org, worker := e.ids.New(), e.ids.New()
	brain := e.provisionedBrain(brainKindWorker, org, worker)
	e.seedBrain(brain)
	binding := &bindingRow{ID: e.ids.New(), Version: 1, InstallationID: e.install, OrganizationID: org, WorkerID: worker,
		BrainID: brain.ID, Permissions: []string{permWrite}, Classification: classificationInternal, State: bindingActive,
		CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	e.seedBinding(binding)

	source := wireArtifactRef{ID: e.ids.New(), Digest: contract.Digest("2222222222222222222222222222222222222222222222222222222222222222"[:64])}
	e.ports.setArtifactState(source.ID, "fault")

	scope := e.scopeAt(org, worker)
	_, err := e.callAs(e.actor, scope, opRemember, rememberInput{
		Scope: wireScope{InstallationID: e.install, OrganizationID: org, WorkerID: worker}, BindingID: binding.ID,
		Text: "remember this", Sources: []wireArtifactRef{source},
		Limits: wireLimits{Currency: "USD", SpendMicroUnits: 10, Concurrency: 1, ModelSteps: 1, AttemptSeconds: 60, RootDeadline: e.clock.Now().Add(time.Hour)},
	})
	if f := decodeFault(err); f == nil || f.Code != contract.CodePrerequisiteMissing {
		t.Fatalf("remember with an unavailable source: got err=%v, want prerequisite_missing", err)
	}
}

// TestRecordUnknownPreservesOutcome proves a lost writer acknowledgement is
// preserved as outcome_unknown, never silently treated as success or
// failure, and opens a durable reconciliation obligation (R15-009).
func TestRecordUnknownPreservesOutcome(t *testing.T) {
	e := newEnv(t)
	org, worker := e.ids.New(), e.ids.New()
	brain := e.provisionedBrain(brainKindWorker, org, worker)
	e.seedBrain(brain)
	binding := &bindingRow{ID: e.ids.New(), Version: 1, InstallationID: e.install, OrganizationID: org, WorkerID: worker,
		BrainID: brain.ID, Permissions: []string{permWrite}, Classification: classificationInternal, State: bindingActive,
		CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	e.seedBinding(binding)

	scope := e.scopeAt(org, worker)
	payload, err := e.callAs(e.actor, scope, opRemember, rememberInput{
		Scope: wireScope{InstallationID: e.install, OrganizationID: org, WorkerID: worker}, BindingID: binding.ID,
		Text: "remember this", Sources: []wireArtifactRef{},
		Limits: wireLimits{Currency: "USD", SpendMicroUnits: 10, Concurrency: 1, ModelSteps: 1, AttemptSeconds: 60, RootDeadline: e.clock.Now().Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	var out jobResourceOutput
	e.decodePayload(payload, &out)

	recordPayload, err := e.callAs(e.actor, scope, opRecord, recordInput{
		JobID: out.Resource.ID, OperationID: *out.Resource.OperationID,
		Observation: wireObservation{Disposition: contract.DispositionUnknown, Evidence: []byte(`{}`), Usage: wireUsage{Currency: "USD"}},
	})
	if err != nil {
		t.Fatalf("record unknown: %v", err)
	}
	var recorded jobResourceOutput
	e.decodePayload(recordPayload, &recorded)
	if recorded.Resource.State != jobOutcomeUnkown {
		t.Fatalf("job state = %q, want %s", recorded.Resource.State, jobOutcomeUnkown)
	}

	err = e.db.Read(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		open, err := listOpenReconciliation(e.ctx, unit, e.install)
		if err != nil {
			return err
		}
		if len(open) != 1 || open[0].Kind != reconcileWriterAck {
			t.Fatalf("open reconciliation = %+v, want exactly one %s obligation", open, reconcileWriterAck)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
}
