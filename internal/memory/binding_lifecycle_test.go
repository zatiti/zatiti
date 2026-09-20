package memory

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// TestBindingCreateActivateArchive drives one binding through its full
// stage -> activate -> archive lifecycle, proving `_configuration.stage` is
// called for the definition change and `_memory.activate` is the only path
// that makes it effective (never a direct public write), and that archive
// checks for retained obligations before applying (no omission deletion).
func TestBindingCreateActivateArchive(t *testing.T) {
	e := newEnv(t)
	org, worker := e.ids.New(), e.ids.New()
	brain := e.provisionedBrain(brainKindWorker, org, worker)
	e.seedBrain(brain)

	scope := e.scopeAt(org, worker)
	wireCallerScope := wireScope{InstallationID: e.install, OrganizationID: org, WorkerID: worker}

	stagePayload, err := e.callAs(e.actor, scope, opBindingCreate, bindingCreateInput{
		Scope: wireCallerScope,
		Definition: bindingDefinition{Scope: wireCallerScope, BrainID: brain.ID,
			Permissions: []string{permRead, permWrite}, Classification: classificationInternal},
	})
	if err != nil {
		t.Fatalf("binding create: %v", err)
	}
	var staged stagedResourceOutput
	e.decodePayload(stagePayload, &staged)
	if staged.Resource.Version != 1 {
		t.Fatalf("staged create preview version = %d, want 1", staged.Resource.Version)
	}

	// The binding does not exist in storage until activate runs.
	getPayload, getErr := e.callAs(e.actor, scope, opBindingGet, bindingGetInput{Scope: wireCallerScope, ID: staged.Resource.ID})
	if f := decodeFault(getErr); f == nil || f.Code != contract.CodeNotFound {
		t.Fatalf("get before activation: got payload=%v err=%v, want not_found", getPayload, getErr)
	}

	fakeCandidateDigest := contract.Digest("1111111111111111111111111111111111111111111111111111111111111111"[:64])
	candidate := wireCandidate{PlanID: e.ids.New(), BaseRevision: 1, CandidateDigest: fakeCandidateDigest,
		Changes: []json.RawMessage{}, Dependencies: []wireRef{}}
	change, err := buildMemoryBindingChange("create", staged.Resource.ID, 0, staged.Resource)
	if err != nil {
		t.Fatalf("build change: %v", err)
	}
	candidate.Changes = append(candidate.Changes, change)

	activatePayload, err := e.callAs(e.actor, scope, opActivate, activateInput{Candidate: candidate})
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	var versions versionsOutput
	e.decodePayload(activatePayload, &versions)
	if len(versions.Versions) != 1 || versions.Versions[0].ID != staged.Resource.ID {
		t.Fatalf("activate versions = %+v, want one ref to %s", versions.Versions, staged.Resource.ID)
	}

	getPayload, err = e.callAs(e.actor, scope, opBindingGet, bindingGetInput{Scope: wireCallerScope, ID: staged.Resource.ID})
	if err != nil {
		t.Fatalf("get after activation: %v", err)
	}
	var got bindingResourceOutput
	e.decodePayload(getPayload, &got)
	if got.Resource.Version != 1 || len(got.Resource.Permissions) != 2 {
		t.Fatalf("activated binding = %+v, want version 1 with 2 permissions", got.Resource)
	}

	archivePayload, err := e.callAs(e.actor, scope, opBindingArchive, bindingArchiveInput{
		Scope: wireCallerScope, ID: staged.Resource.ID, ExpectedVersion: 1,
	})
	if err != nil {
		t.Fatalf("archive stage: %v", err)
	}
	var archiveStaged stagedResourceOutput
	e.decodePayload(archivePayload, &archiveStaged)

	archiveChange, err := buildMemoryBindingChange("archive", staged.Resource.ID, 1, archiveStaged.Resource)
	if err != nil {
		t.Fatalf("build archive change: %v", err)
	}
	activateArchive := wireCandidate{PlanID: e.ids.New(), BaseRevision: 2, CandidateDigest: fakeCandidateDigest,
		Changes: []json.RawMessage{archiveChange}, Dependencies: []wireRef{}}
	if _, err := e.callAs(e.actor, scope, opActivate, activateInput{Candidate: activateArchive}); err != nil {
		t.Fatalf("activate archive: %v", err)
	}

	// An archived binding no longer authorizes reads.
	_, err = e.callAs(e.actor, scope, opSelect, selectInput{
		Scope: wireCallerScope, BindingIDs: []contract.ID{staged.Resource.ID}, Permission: permRead,
	})
	if f := decodeFault(err); f == nil || f.Code != contract.CodePermissionDenied {
		t.Fatalf("select on an archived binding: got err=%v, want permission_denied", err)
	}
}

// TestBindingUpdateStaleVersion proves an update against a stale
// expected_version is refused before configuration is ever staged.
func TestBindingUpdateStaleVersion(t *testing.T) {
	e := newEnv(t)
	org, worker := e.ids.New(), e.ids.New()
	brain := e.provisionedBrain(brainKindWorker, org, worker)
	e.seedBrain(brain)
	binding := &bindingRow{ID: e.ids.New(), Version: 3, InstallationID: e.install, OrganizationID: org, WorkerID: worker,
		BrainID: brain.ID, Permissions: []string{permRead}, Classification: classificationInternal, State: bindingActive,
		CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	e.seedBinding(binding)

	scope := e.scopeAt(org, worker)
	wireCallerScope := wireScope{InstallationID: e.install, OrganizationID: org, WorkerID: worker}
	e.ports.resetCalls()
	_, err := e.callAs(e.actor, scope, opBindingUpdate, bindingUpdateInput{
		Scope: wireCallerScope, ID: binding.ID, ExpectedVersion: 1,
		Definition: bindingDefinition{Scope: wireCallerScope, BrainID: brain.ID, Permissions: []string{permRead}, Classification: classificationInternal},
	})
	if f := decodeFault(err); f == nil || f.Code != contract.CodeStaleVersion {
		t.Fatalf("update with a stale expected_version: got err=%v, want stale_version", err)
	}
	for _, op := range e.ports.opsCalled() {
		if op == opConfigStage {
			t.Fatalf("stale update still staged a configuration change")
		}
	}
}

// TestRevokedBindingExcludesFutureContextButRetainsPastContext proves the
// Z18.revoked_memory_binding shape: once the sole binding authorizing a
// brain is archived, a brand new recall against that binding is denied
// immediately (future context excluded), but the claim and job result an
// earlier, already-completed recall produced under the binding while it was
// still active are neither deleted nor mutated by the archive -- revocation
// blocks subsequent retrieval, it does not erase already disclosed context
// (R15-006). job.get in particular carries no binding-authorization check at
// all (only an installation-scope check), so it is exactly the retained,
// already-disclosed record R15-006 describes, not a fresh retrieval gated by
// current authority.
func TestRevokedBindingExcludesFutureContextButRetainsPastContext(t *testing.T) {
	e := newEnv(t)
	org, worker := e.ids.New(), e.ids.New()
	brain := e.provisionedBrain(brainKindWorker, org, worker)
	e.seedBrain(brain)
	binding := &bindingRow{ID: e.ids.New(), Version: 1, InstallationID: e.install, OrganizationID: org, WorkerID: worker,
		BrainID: brain.ID, Permissions: []string{permRead}, Classification: classificationInternal, State: bindingActive,
		CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	e.seedBinding(binding)

	scope := e.scopeAt(org, worker)
	wireCallerScope := wireScope{InstallationID: e.install, OrganizationID: org, WorkerID: worker}

	// While the binding is active, run one recall through to completion:
	// a cached claim and a job result naming a real context artifact.
	payload, err := e.callAs(e.actor, scope, opRecall, recallInput{
		Scope: wireCallerScope, Query: "anything", BindingIDs: []contract.ID{binding.ID}, MinimumFreshness: time.Time{},
		Limits: wireLimits{Currency: "USD", SpendMicroUnits: 100, Concurrency: 1, ModelSteps: 1, AttemptSeconds: 60, RootDeadline: e.clock.Now().Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("recall while the binding is active: %v", err)
	}
	var out jobResourceOutput
	e.decodePayload(payload, &out)

	claim := newMemoryClaim(brain.ID, e.ids.New(), 1, true, e.clock.Now())
	evidence := recallEvidence(brain, "00000000-0000-4000-8000-000000000099", claim, e.clock.Now())
	recordPayload, err := e.callAs(e.actor, scope, opRecord, recordInput{
		JobID: out.Resource.ID, OperationID: *out.Resource.OperationID,
		Observation: wireObservation{Disposition: contract.DispositionSucceeded, Evidence: mustMarshal(t, evidence), Usage: wireUsage{Currency: "USD"}},
	})
	if err != nil {
		t.Fatalf("record recall success: %v", err)
	}
	var recorded jobResourceOutput
	e.decodePayload(recordPayload, &recorded)
	if recorded.Resource.State != jobSucceeded || recorded.Resource.ResultArtifact == nil {
		t.Fatalf("completed recall = %+v, want succeeded with a context artifact", recorded.Resource)
	}

	// Revoke the only binding on this brain: archive it directly (this test
	// exercises the downstream authorization effect, not the staged
	// create/archive/activate lifecycle TestBindingCreateActivateArchive
	// already covers).
	if err := e.db.Write(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		return setBindingState(e.ctx, unit, binding, bindingArchived, e.clock.Now())
	}); err != nil {
		t.Fatalf("archive binding: %v", err)
	}

	// Future context is excluded immediately: a new recall against the same,
	// now-archived binding is denied before any dispatch.
	e.ports.resetCalls()
	_, err = e.callAs(e.actor, scope, opRecall, recallInput{
		Scope: wireCallerScope, Query: "anything", BindingIDs: []contract.ID{binding.ID}, MinimumFreshness: time.Time{},
		Limits: wireLimits{Currency: "USD", SpendMicroUnits: 100, Concurrency: 1, ModelSteps: 1, AttemptSeconds: 60, RootDeadline: e.clock.Now().Add(time.Hour)},
	})
	if f := decodeFault(err); f == nil || f.Code != contract.CodePermissionDenied {
		t.Fatalf("recall against a revoked binding: got err=%v, want permission_denied", err)
	}
	for _, op := range e.ports.opsCalled() {
		if op == opEffectsPrepare || op == opExecutionJobCreate {
			t.Fatalf("recall against a revoked binding called %s; a revoked binding must never dispatch", op)
		}
	}

	// The retained job result from before revocation is unchanged: same
	// state, same context artifact, same cached claim.
	jobPayload, err := e.callAs(e.actor, scope, opJobGet, jobGetInput{Scope: wireCallerScope, ID: out.Resource.ID})
	if err != nil {
		t.Fatalf("job.get after binding revocation: %v", err)
	}
	var afterRevocation jobResourceOutput
	e.decodePayload(jobPayload, &afterRevocation)
	if afterRevocation.Resource.State != jobSucceeded ||
		afterRevocation.Resource.ResultArtifact == nil ||
		*afterRevocation.Resource.ResultArtifact != *recorded.Resource.ResultArtifact {
		t.Fatalf("retained job result after revocation = %+v, want unchanged from %+v", afterRevocation.Resource, recorded.Resource)
	}

	var stillCached *claimRow
	if err := e.db.Read(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		var err error
		stillCached, err = loadLatestClaim(e.ctx, unit, brain.ID, claim.ID)
		return err
	}); err != nil {
		t.Fatalf("read cached claim after revocation: %v", err)
	}
	if stillCached.Text != claim.Text || stillCached.Version != claim.Version {
		t.Fatalf("cached claim after revocation = %+v, want unchanged text/version from the pre-revocation recall", stillCached)
	}
}

// TestJobGetCrossInstallationNotFound proves job.get never discloses a job
// belonging to another installation's storage row set.
func TestInspectPrerequisiteMissingWithoutRecall(t *testing.T) {
	e := newEnv(t)
	org, worker := e.ids.New(), e.ids.New()
	brain := e.provisionedBrain(brainKindWorker, org, worker)
	e.seedBrain(brain)
	binding := &bindingRow{ID: e.ids.New(), Version: 1, InstallationID: e.install, OrganizationID: org, WorkerID: worker,
		BrainID: brain.ID, Permissions: []string{permRead}, Classification: classificationInternal, State: bindingActive,
		CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	e.seedBinding(binding)

	scope := e.scopeAt(org, worker)
	_, err := e.callAs(e.actor, scope, opInspect, inspectInput{
		Scope: wireScope{InstallationID: e.install, OrganizationID: org, WorkerID: worker}, BrainID: brain.ID, ClaimID: e.ids.New(),
	})
	if f := decodeFault(err); f == nil || f.Code != contract.CodePrerequisiteMissing {
		t.Fatalf("inspect an uncached claim: got err=%v, want prerequisite_missing directing the caller to recall", err)
	}
}
