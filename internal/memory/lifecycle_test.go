package memory

import (
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/storage"
)

// P28: memory lifecycle against the qualified adapter. These tests prove the
// three behaviors the card requires by name, plus the stable-command-identity
// reconciliation fix item 2 also calls for. They exercise real dispatch
// (recall/remember/promote/retract building the actual SerenityXxx action
// and going through _effects.prepare/_execution.job.create) and real
// _memory.record application; only the physical Serenity call itself is
// faked, at the fakePorts boundary this package's own tests have always
// used -- internal/adapters/serenity (P27) is out of this package's allowed
// writes and is never imported here.

// TestRecallRecordsSourceRefsAndAuditReadReproducesHistoricalContext proves
// two things named by the card's first required test in one scenario: a
// scoped recall records real selected claims carrying source refs (R15-007),
// and a later audit read of that SAME job reproduces the exact original
// context byte-for-byte even after memory has moved on -- a second recall
// against the same binding, with a new claim and a newer brain revision,
// changes what the LATEST cache holds but never rewrites the first job's
// already-recorded result or context artifact (Z18.retained_context).
func TestRecallRecordsSourceRefsAndAuditReadReproducesHistoricalContext(t *testing.T) {
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

	doRecall := func() jobResourceOutput {
		t.Helper()
		payload, err := e.callAs(e.actor, scope, opRecall, recallInput{
			Scope: wireCallerScope, Query: "anything", BindingIDs: []contract.ID{binding.ID}, MinimumFreshness: time.Time{},
			Limits: wireLimits{Currency: "USD", SpendMicroUnits: 100, Concurrency: 1, ModelSteps: 1, AttemptSeconds: 60, RootDeadline: e.clock.Now().Add(time.Hour)},
		})
		if err != nil {
			t.Fatalf("recall: %v", err)
		}
		var out jobResourceOutput
		e.decodePayload(payload, &out)
		return out
	}

	// First recall: one claim with a real, non-empty source ref, at the
	// brain's first observed revision.
	firstJob := doRecall()
	source := wireArtifactRef{ID: e.ids.New(), Digest: contract.Digest("1111111111111111111111111111111111111111111111111111111111111111"[:64])}
	firstClaim := newMemoryClaim(brain.ID, e.ids.New(), 1, true, e.clock.Now())
	firstClaim.Sources = []wireArtifactRef{source}
	firstEvidence := recallEvidence(brain, "00000000-0000-4000-8000-000000000101", firstClaim, e.clock.Now())
	firstEvidence.BrainRevisions[0].Revision = "rev-1"

	firstRecordPayload, err := e.callAs(e.actor, scope, opRecord, recordInput{
		JobID: firstJob.Resource.ID, OperationID: *firstJob.Resource.OperationID,
		Observation: wireObservation{Disposition: contract.DispositionSucceeded, Evidence: mustMarshal(t, firstEvidence), Usage: wireUsage{Currency: "USD"}},
	})
	if err != nil {
		t.Fatalf("record first recall success: %v", err)
	}
	var firstRecorded jobResourceOutput
	e.decodePayload(firstRecordPayload, &firstRecorded)
	if firstRecorded.Resource.State != jobSucceeded || firstRecorded.Resource.ResultArtifact == nil {
		t.Fatalf("first recall job = %+v, want succeeded with a context artifact", firstRecorded.Resource)
	}
	var firstResult struct {
		ContextArtifact wireArtifactRef `json:"context_artifact"`
		Claims          []wireClaim     `json:"claims"`
	}
	if err := jsonUnmarshal(firstRecorded.Resource.Result, &firstResult); err != nil {
		t.Fatalf("decode first recall result: %v", err)
	}
	if len(firstResult.Claims) != 1 || len(firstResult.Claims[0].Sources) != 1 || firstResult.Claims[0].Sources[0].ID != source.ID {
		t.Fatalf("first recall claims = %+v, want exactly one claim carrying source ref %s", firstResult.Claims, source.ID)
	}
	firstArtifact := *firstRecorded.Resource.ResultArtifact

	// Advance the clock and memory itself: a second recall against the same
	// binding, a different claim, a newer brain revision.
	e.clock.Advance(time.Hour)
	secondJob := doRecall()
	secondClaim := newMemoryClaim(brain.ID, e.ids.New(), 1, true, e.clock.Now())
	secondClaim.Text = "the ocean is deep"
	secondEvidence := recallEvidence(brain, "00000000-0000-4000-8000-000000000102", secondClaim, e.clock.Now())
	secondEvidence.BrainRevisions[0].Revision = "rev-2"
	secondEvidence.StagedOutputs[0].StagingRef = "stage-2"
	secondEvidence.SelectedContext = &serenityArtifactLocator{Kind: "staged", StagingRef: "stage-2", Digest: secondEvidence.StagedOutputs[0].Digest}

	secondRecordPayload, err := e.callAs(e.actor, scope, opRecord, recordInput{
		JobID: secondJob.Resource.ID, OperationID: *secondJob.Resource.OperationID,
		Observation: wireObservation{Disposition: contract.DispositionSucceeded, Evidence: mustMarshal(t, secondEvidence), Usage: wireUsage{Currency: "USD"}},
	})
	if err != nil {
		t.Fatalf("record second recall success: %v", err)
	}
	var secondRecorded jobResourceOutput
	e.decodePayload(secondRecordPayload, &secondRecorded)
	if secondRecorded.Resource.ResultArtifact == nil || *secondRecorded.Resource.ResultArtifact == firstArtifact {
		t.Fatalf("second recall context artifact = %+v, want a distinct artifact from the first recall's %+v",
			secondRecorded.Resource.ResultArtifact, firstArtifact)
	}

	// Memory has moved on: the local cache now reflects the second claim.
	var latest *claimRow
	if err := e.db.Read(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		var err error
		latest, err = loadLatestClaim(e.ctx, unit, brain.ID, secondClaim.ID)
		return err
	}); err != nil {
		t.Fatalf("read latest claim: %v", err)
	}
	if latest.Text != secondClaim.Text {
		t.Fatalf("latest cached claim text = %q, want %q", latest.Text, secondClaim.Text)
	}

	// An audit read of the FIRST job reproduces its exact original context:
	// the same result bytes and the same context artifact, unaffected by
	// the second recall and the brain's advanced revision.
	auditPayload, err := e.callAs(e.actor, scope, opJobGet, jobGetInput{Scope: wireCallerScope, ID: firstJob.Resource.ID})
	if err != nil {
		t.Fatalf("job.get for audit read: %v", err)
	}
	var audited jobResourceOutput
	e.decodePayload(auditPayload, &audited)
	if audited.Resource.ResultArtifact == nil || *audited.Resource.ResultArtifact != firstArtifact {
		t.Fatalf("audited first job artifact = %+v, want unchanged %+v", audited.Resource.ResultArtifact, firstArtifact)
	}
	var auditedResult struct {
		ContextArtifact wireArtifactRef `json:"context_artifact"`
		Claims          []wireClaim     `json:"claims"`
	}
	if err := jsonUnmarshal(audited.Resource.Result, &auditedResult); err != nil {
		t.Fatalf("decode audited result: %v", err)
	}
	if len(auditedResult.Claims) != 1 || auditedResult.Claims[0].ID != firstClaim.ID || auditedResult.Claims[0].Text != firstClaim.Text {
		t.Fatalf("audited first job claims = %+v, want the original first-recall claim %s (%q) unchanged",
			auditedResult.Claims, firstClaim.ID, firstClaim.Text)
	}
	if len(auditedResult.Claims[0].Sources) != 1 || auditedResult.Claims[0].Sources[0].ID != source.ID {
		t.Fatalf("audited first job claim sources = %+v, want the original source ref %s", auditedResult.Claims[0].Sources, source.ID)
	}
}

// TestRetractionSurvivesRestartWithoutLeavingPromotedClaimFalselyCurrent
// proves a correction propagates from a retracted source claim to every
// promoted copy -- flipping it inactive at a new, provenance-preserving
// version, never merely leaving an inert reconciliation obligation while the
// copy keeps reading as current -- and that this corrected state, plus the
// obligation recording why, both survive a full process restart (R15-006,
// R15-009).
func TestRetractionSurvivesRestartWithoutLeavingPromotedClaimFalselyCurrent(t *testing.T) {
	e := newEnv(t)
	org, worker := e.ids.New(), e.ids.New()
	sourceBrain := e.provisionedBrain(brainKindOrganization, org, "")
	e.seedBrain(sourceBrain)
	destBrain := e.provisionedBrain(brainKindWorker, org, worker)
	e.seedBrain(destBrain)

	sourceClaim := &claimRow{BrainID: sourceBrain.ID, ID: e.ids.New(), Version: 1, Text: "a fact worth sharing",
		Sources: []wireArtifactRef{}, Confidence: 900000, Freshness: e.clock.Now(), Active: true, RecordedAt: e.clock.Now()}
	e.seedClaim(sourceClaim)

	sourceBinding := &bindingRow{ID: e.ids.New(), Version: 1, InstallationID: e.install, OrganizationID: org, WorkerID: worker,
		BrainID: sourceBrain.ID, Permissions: []string{permPromote, permRetract}, Classification: classificationInternal,
		State: bindingActive, CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	e.seedBinding(sourceBinding)
	destBinding := &bindingRow{ID: e.ids.New(), Version: 1, InstallationID: e.install, OrganizationID: org, WorkerID: worker,
		BrainID: destBrain.ID, Permissions: []string{permWrite}, Classification: classificationInternal, State: bindingActive,
		CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	e.seedBinding(destBinding)

	scope := e.scopeAt(org, worker)
	wireCallerScope := wireScope{InstallationID: e.install, OrganizationID: org, WorkerID: worker}

	// Promote the source claim into the destination brain.
	promotePayload, err := e.callAs(e.actor, scope, opPromote, promoteInput{
		Scope: wireCallerScope, SourceBrainID: sourceBrain.ID, SourceClaim: wireRef{ID: sourceClaim.ID, Version: 1},
		DestinationBindingID: destBinding.ID,
		Limits:               wireLimits{Currency: "USD", SpendMicroUnits: 10, Concurrency: 1, ModelSteps: 1, AttemptSeconds: 60, RootDeadline: e.clock.Now().Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	var promoteOut jobResourceOutput
	e.decodePayload(promotePayload, &promoteOut)

	destClaimID := e.ids.New()
	destClaim := newMemoryClaim(destBrain.ID, destClaimID, 1, true, e.clock.Now())
	sourceBrainID, sourceClaimRef := sourceBrain.ID, wireRef{ID: sourceClaim.ID, Version: 1}
	destClaim.SourceBrainID, destClaim.SourceClaim = &sourceBrainID, &sourceClaimRef
	promoteEvidence := recallEvidence(destBrain, "00000000-0000-4000-8000-000000000201", destClaim, e.clock.Now())
	promoteEvidence.Kind = intentPromote
	promoteEvidence.SelectedContext = nil
	promoteEvidence.StagedOutputs = []serenityStagedOutput{}

	if _, err := e.callAs(e.actor, scope, opRecord, recordInput{
		JobID: promoteOut.Resource.ID, OperationID: *promoteOut.Resource.OperationID,
		Observation: wireObservation{Disposition: contract.DispositionSucceeded, Evidence: mustMarshal(t, promoteEvidence), Usage: wireUsage{Currency: "USD"}},
	}); err != nil {
		t.Fatalf("record promote success: %v", err)
	}

	assertDestClaim := func(step string, wantActive bool, wantVersion int64) {
		t.Helper()
		if err := e.db.Read(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
			dest, err := loadLatestClaim(e.ctx, unit, destBrain.ID, destClaimID)
			if err != nil {
				return err
			}
			if dest.Active != wantActive || dest.Version != wantVersion {
				t.Fatalf("%s: destination claim = active=%v version=%d, want active=%v version=%d",
					step, dest.Active, dest.Version, wantActive, wantVersion)
			}
			return nil
		}); err != nil {
			t.Fatalf("%s: read destination claim: %v", step, err)
		}
	}
	assertDestClaim("after promote", true, 1)

	// Retract the source claim.
	retractPayload, err := e.callAs(e.actor, scope, opRetract, retractInput{
		Scope: wireCallerScope, BrainID: sourceBrain.ID, Claim: wireRef{ID: sourceClaim.ID, Version: 1}, Reason: "no longer accurate",
	})
	if err != nil {
		t.Fatalf("retract: %v", err)
	}
	var retractOut jobResourceOutput
	e.decodePayload(retractPayload, &retractOut)

	retractedSourceClaim := newMemoryClaim(sourceBrain.ID, sourceClaim.ID, 2, false, e.clock.Now())
	retractEvidence := recallEvidence(sourceBrain, "00000000-0000-4000-8000-000000000202", retractedSourceClaim, e.clock.Now())
	retractEvidence.Kind = intentRetract
	retractEvidence.SelectedContext = nil
	retractEvidence.StagedOutputs = []serenityStagedOutput{}
	activeRemoved := true
	retractEvidence.ActiveRecallRemoved = &activeRemoved

	if _, err := e.callAs(e.actor, scope, opRecord, recordInput{
		JobID: retractOut.Resource.ID, OperationID: *retractOut.Resource.OperationID,
		Observation: wireObservation{Disposition: contract.DispositionSucceeded, Evidence: mustMarshal(t, retractEvidence), Usage: wireUsage{Currency: "USD"}},
	}); err != nil {
		t.Fatalf("record retract success: %v", err)
	}

	// The promoted copy is corrected immediately: a new, inactive version,
	// never merely an inert obligation while it keeps reading as current.
	assertDestClaim("after retract", false, 2)
	if n := countReconciliationKind(t, e, reconcileRetraction); n != 1 {
		t.Fatalf("after retract: %d retraction_propagation obligations, want exactly 1", n)
	}

	// Restart: close and reopen the database against a brand new Service.
	if err := e.db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	reopened, err := storage.Open(e.ctx, storage.Config{Path: e.dbPath})
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restartedSvc, err := New(contract.Dependencies{Clock: e.clock, IDs: e.ids, Ports: e.ports})
	if err != nil {
		t.Fatalf("memory.New after restart: %v", err)
	}
	if err := reopened.Migrate(e.ctx, restartedSvc.Migrations()); err != nil {
		t.Fatalf("re-migrate after restart: %v", err)
	}
	e.db, e.svc = reopened, restartedSvc

	// The correction persisted through the restart: still inactive, still
	// one obligation, never resurrected as current.
	assertDestClaim("after restart", false, 2)
	if n := countReconciliationKind(t, e, reconcileRetraction); n != 1 {
		t.Fatalf("after restart: %d retraction_propagation obligations, want exactly 1", n)
	}

	// A redelivered retract observation (the controller resending because
	// its own acknowledgement of the first response was lost) never
	// resurrects the claim as active or compounds its version: the intent
	// is already terminal, so this opens one more promotion_correction
	// obligation instead (the same guard TestDuplicateCompletionAndRestart-
	// PreserveOnePromotionLineage proves for a plain success) and leaves the
	// already-corrected claim exactly as it was.
	redelivered, err := e.callAs(e.actor, scope, opRecord, recordInput{
		JobID: retractOut.Resource.ID, OperationID: *retractOut.Resource.OperationID,
		Observation: wireObservation{Disposition: contract.DispositionSucceeded, Evidence: mustMarshal(t, retractEvidence), Usage: wireUsage{Currency: "USD"}},
	})
	if err != nil {
		t.Fatalf("redelivered record retract: %v", err)
	}
	var redeliveredOut jobResourceOutput
	e.decodePayload(redelivered, &redeliveredOut)
	if redeliveredOut.Resource.State != jobSucceeded {
		t.Fatalf("redelivered retract job = %+v, want it to still read succeeded", redeliveredOut.Resource)
	}
	assertDestClaim("after post-restart redelivery", false, 2)
}

// TestHardBoundModeRefusesAdvisoryProviderConfirmation proves hard-bound
// mode (P28): a caller of a governed single-writer commit -- remember,
// promote or retract -- that requires guaranteed enforcement refuses an
// evidence document whose provider confirmation is explicitly advisory
// (lookup_authoritative=false) rather than silently recording it as a
// guaranteed claim. It also proves the two necessary boundaries: an evidence
// document that is simply silent about authoritativeness (the common,
// legitimate case) is NOT refused -- hard-bound mode never second-guesses an
// absent signal, only an explicit one -- and recall, a disclosure read
// R15-008 explicitly permits to run advisory, is exempt entirely.
func TestHardBoundModeRefusesAdvisoryProviderConfirmation(t *testing.T) {
	e := newEnv(t)
	org, worker := e.ids.New(), e.ids.New()
	brain := e.provisionedBrain(brainKindWorker, org, worker)
	e.seedBrain(brain)
	binding := &bindingRow{ID: e.ids.New(), Version: 1, InstallationID: e.install, OrganizationID: org, WorkerID: worker,
		BrainID: brain.ID, Permissions: []string{permRead, permWrite}, Classification: classificationInternal, State: bindingActive,
		CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	e.seedBinding(binding)

	scope := e.scopeAt(org, worker)
	wireCallerScope := wireScope{InstallationID: e.install, OrganizationID: org, WorkerID: worker}
	falseVal := false

	// A remember whose evidence explicitly reports a non-authoritative,
	// advisory-only confirmation is refused, not silently accepted.
	rememberPayload, err := e.callAs(e.actor, scope, opRemember, rememberInput{
		Scope: wireCallerScope, BindingID: binding.ID, Text: "remember this", Sources: []wireArtifactRef{},
		Limits: wireLimits{Currency: "USD", SpendMicroUnits: 10, Concurrency: 1, ModelSteps: 1, AttemptSeconds: 60, RootDeadline: e.clock.Now().Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	var rememberOut jobResourceOutput
	e.decodePayload(rememberPayload, &rememberOut)

	advisoryClaim := newMemoryClaim(brain.ID, e.ids.New(), 1, true, e.clock.Now())
	advisoryEvidence := recallEvidence(brain, "00000000-0000-4000-8000-000000000301", advisoryClaim, e.clock.Now())
	advisoryEvidence.Kind = intentRemember
	advisoryEvidence.SelectedContext = nil
	advisoryEvidence.StagedOutputs = []serenityStagedOutput{}
	advisoryEvidence.LookupAuthoritative = &falseVal

	_, err = e.callAs(e.actor, scope, opRecord, recordInput{
		JobID: rememberOut.Resource.ID, OperationID: *rememberOut.Resource.OperationID,
		Observation: wireObservation{Disposition: contract.DispositionSucceeded, Evidence: mustMarshal(t, advisoryEvidence), Usage: wireUsage{Currency: "USD"}},
	})
	f := decodeFault(err)
	if f == nil || f.Code != contract.CodeCapabilityUnsupported {
		t.Fatalf("record remember with an advisory (lookup_authoritative=false) confirmation: got err=%v, want capability_unsupported", err)
	}
	if err := e.db.Read(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		_, err := loadLatestClaim(e.ctx, unit, brain.ID, advisoryClaim.ID)
		if err == nil {
			t.Fatalf("advisory-confirmed claim %s was recorded; hard-bound mode must refuse it, not apply it", advisoryClaim.ID)
		}
		if !isNoRows(err) {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("read: %v", err)
	}

	// A second remember whose evidence is simply silent about
	// authoritativeness (the field absent, as every other test in this
	// package already exercises) is NOT refused: absence is not itself a
	// refusal signal.
	rememberPayload2, err := e.callAs(e.actor, scope, opRemember, rememberInput{
		Scope: wireCallerScope, BindingID: binding.ID, Text: "remember this too", Sources: []wireArtifactRef{},
		Limits: wireLimits{Currency: "USD", SpendMicroUnits: 10, Concurrency: 1, ModelSteps: 1, AttemptSeconds: 60, RootDeadline: e.clock.Now().Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("second remember: %v", err)
	}
	var rememberOut2 jobResourceOutput
	e.decodePayload(rememberPayload2, &rememberOut2)

	silentClaim := newMemoryClaim(brain.ID, e.ids.New(), 1, true, e.clock.Now())
	silentEvidence := recallEvidence(brain, "00000000-0000-4000-8000-000000000302", silentClaim, e.clock.Now())
	silentEvidence.Kind = intentRemember
	silentEvidence.SelectedContext = nil
	silentEvidence.StagedOutputs = []serenityStagedOutput{}
	// LookupAuthoritative left nil (absent).

	silentRecordPayload, err := e.callAs(e.actor, scope, opRecord, recordInput{
		JobID: rememberOut2.Resource.ID, OperationID: *rememberOut2.Resource.OperationID,
		Observation: wireObservation{Disposition: contract.DispositionSucceeded, Evidence: mustMarshal(t, silentEvidence), Usage: wireUsage{Currency: "USD"}},
	})
	if err != nil {
		t.Fatalf("record remember with no authoritativeness signal: %v", err)
	}
	var silentRecorded jobResourceOutput
	e.decodePayload(silentRecordPayload, &silentRecorded)
	if silentRecorded.Resource.State != jobSucceeded {
		t.Fatalf("remember with silent evidence = %+v, want succeeded (absence is not a refusal)", silentRecorded.Resource)
	}

	// Recall is exempt: R15-008 explicitly permits a disclosure read to run
	// advisory where policy allows, so the same lookup_authoritative=false
	// signal on a recall's evidence must not be refused.
	recallPayload, err := e.callAs(e.actor, scope, opRecall, recallInput{
		Scope: wireCallerScope, Query: "anything", BindingIDs: []contract.ID{binding.ID}, MinimumFreshness: time.Time{},
		Limits: wireLimits{Currency: "USD", SpendMicroUnits: 100, Concurrency: 1, ModelSteps: 1, AttemptSeconds: 60, RootDeadline: e.clock.Now().Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	var recallOut jobResourceOutput
	e.decodePayload(recallPayload, &recallOut)

	recallClaim := newMemoryClaim(brain.ID, e.ids.New(), 1, true, e.clock.Now())
	recallEv := recallEvidence(brain, "00000000-0000-4000-8000-000000000303", recallClaim, e.clock.Now())
	recallEv.LookupAuthoritative = &falseVal

	recallRecordPayload, err := e.callAs(e.actor, scope, opRecord, recordInput{
		JobID: recallOut.Resource.ID, OperationID: *recallOut.Resource.OperationID,
		Observation: wireObservation{Disposition: contract.DispositionSucceeded, Evidence: mustMarshal(t, recallEv), Usage: wireUsage{Currency: "USD"}},
	})
	if err != nil {
		t.Fatalf("record recall with lookup_authoritative=false: %v", err)
	}
	var recallRecorded jobResourceOutput
	e.decodePayload(recallRecordPayload, &recallRecorded)
	if recallRecorded.Resource.State != jobSucceeded {
		t.Fatalf("recall with advisory evidence = %+v, want succeeded (recall is exempt from hard-bound mode)", recallRecorded.Resource)
	}
}

// TestRecordReconcilesByStableJobIdentityWhenOperationIdUnrecognized proves
// P28 item 2's reconciliation fix: a bounded reconciliation read admits its
// own, separate effects Operation (R15-009), so a redelivered or reconciled
// observation may name an operation_id this owner never recorded against its
// writer intent. handleRecord falls back to this owner's own stable job
// identity -- the job_id `_memory.record` always carries -- instead of
// refusing the observation as untracked.
func TestRecordReconcilesByStableJobIdentityWhenOperationIdUnrecognized(t *testing.T) {
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

	// An operation_id this owner never dispatched (as a reconciliation
	// read's own, separately admitted Operation would be) but the correct,
	// stable job_id this owner minted at dispatch.
	unrecognizedOperation := contract.ID("00000000-0000-4000-8000-999999999999")
	claim := newMemoryClaim(brain.ID, e.ids.New(), 1, true, e.clock.Now())
	evidence := recallEvidence(brain, "00000000-0000-4000-8000-000000000401", claim, e.clock.Now())

	recordPayload, err := e.callAs(e.actor, scope, opRecord, recordInput{
		JobID: out.Resource.ID, OperationID: unrecognizedOperation,
		Observation: wireObservation{Disposition: contract.DispositionSucceeded, Evidence: mustMarshal(t, evidence), Usage: wireUsage{Currency: "USD"}},
	})
	if err != nil {
		t.Fatalf("record with an unrecognized operation_id but the correct job_id: %v", err)
	}
	var recorded jobResourceOutput
	e.decodePayload(recordPayload, &recorded)
	if recorded.Resource.State != jobSucceeded {
		t.Fatalf("job state = %q, want succeeded (resolved by stable job identity)", recorded.Resource.State)
	}

	// Neither operation_id nor job_id resolves to anything: a genuinely
	// untracked observation still refuses not_found.
	_, err = e.callAs(e.actor, scope, opRecord, recordInput{
		JobID: contract.ID("00000000-0000-4000-8000-888888888888"), OperationID: unrecognizedOperation,
		Observation: wireObservation{Disposition: contract.DispositionSucceeded, Evidence: mustMarshal(t, evidence), Usage: wireUsage{Currency: "USD"}},
	})
	if f := decodeFault(err); f == nil || f.Code != contract.CodeNotFound {
		t.Fatalf("record with neither operation_id nor job_id recognized: got err=%v, want not_found", err)
	}
}
