package memory

import (
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/storage"
)

// TestPromoteRequiresSourceAndDestinationAuthority proves promote demands
// both source disclosure authority (a binding on the source brain granting
// promote) and destination write/curate authority (R15-005); either
// missing refuses the call before any dispatch.
func TestPromoteRequiresSourceAndDestinationAuthority(t *testing.T) {
	e := newEnv(t)
	org, worker := e.ids.New(), e.ids.New()
	sourceBrain := e.provisionedBrain(brainKindOrganization, org, "")
	e.seedBrain(sourceBrain)
	destBrain := e.provisionedBrain(brainKindWorker, org, worker)
	e.seedBrain(destBrain)

	claim := &claimRow{BrainID: sourceBrain.ID, ID: e.ids.New(), Version: 1, Text: "a fact", Sources: []wireArtifactRef{},
		Confidence: 900000, Freshness: e.clock.Now(), Active: true, RecordedAt: e.clock.Now()}
	e.seedClaim(claim)

	destBinding := &bindingRow{ID: e.ids.New(), Version: 1, InstallationID: e.install, OrganizationID: org, WorkerID: worker,
		BrainID: destBrain.ID, Permissions: []string{permWrite}, Classification: classificationInternal, State: bindingActive,
		CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	e.seedBinding(destBinding)

	scope := e.scopeAt(org, worker)
	promoteIn := promoteInput{
		Scope: wireScope{InstallationID: e.install, OrganizationID: org, WorkerID: worker}, SourceBrainID: sourceBrain.ID,
		SourceClaim: wireRef{ID: claim.ID, Version: 1}, DestinationBindingID: destBinding.ID,
		Limits: wireLimits{Currency: "USD", SpendMicroUnits: 10, Concurrency: 1, ModelSteps: 1, AttemptSeconds: 60, RootDeadline: e.clock.Now().Add(time.Hour)},
	}

	// No binding at all grants promote on the source brain.
	_, err := e.callAs(e.actor, scope, opPromote, promoteIn)
	if f := decodeFault(err); f == nil || f.Code != contract.CodePermissionDenied {
		t.Fatalf("promote without source authority: got err=%v, want permission_denied", err)
	}

	// Grant source promote authority; destination binding only has write,
	// which IS sufficient, so this should now proceed -- prove the inverse
	// by pointing at a destination binding with neither write nor curate.
	sourceBinding := &bindingRow{ID: e.ids.New(), Version: 1, InstallationID: e.install, OrganizationID: org, WorkerID: worker,
		BrainID: sourceBrain.ID, Permissions: []string{permPromote}, Classification: classificationInternal, State: bindingActive,
		CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	e.seedBinding(sourceBinding)

	readOnlyDestBinding := &bindingRow{ID: e.ids.New(), Version: 1, InstallationID: e.install, OrganizationID: org, WorkerID: worker,
		BrainID: destBrain.ID, Permissions: []string{permRead}, Classification: classificationInternal, State: bindingActive,
		CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	e.seedBinding(readOnlyDestBinding)

	promoteIn.DestinationBindingID = readOnlyDestBinding.ID
	_, err = e.callAs(e.actor, scope, opPromote, promoteIn)
	if f := decodeFault(err); f == nil || f.Code != contract.CodePermissionDenied {
		t.Fatalf("promote with a read-only destination binding: got err=%v, want permission_denied", err)
	}

	// Both authorities present: promote proceeds to a durable job.
	promoteIn.DestinationBindingID = destBinding.ID
	payload, err := e.callAs(e.actor, scope, opPromote, promoteIn)
	if err != nil {
		t.Fatalf("promote with both authorities: %v", err)
	}
	var out jobResourceOutput
	e.decodePayload(payload, &out)
	if out.Resource.Kind != intentPromote {
		t.Fatalf("job kind = %q, want %s", out.Resource.Kind, intentPromote)
	}
}

// TestPromoteLineageAndRetractionPropagation proves a promoted claim
// retains durable lineage to its source (R15-005), and that retracting the
// source claim opens a correction obligation against every downstream
// promotion instead of erasing or silently ignoring it (R15-006).
func TestPromoteLineageAndRetractionPropagation(t *testing.T) {
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
	payload, err := e.callAs(e.actor, scope, opPromote, promoteInput{
		Scope: wireScope{InstallationID: e.install, OrganizationID: org, WorkerID: worker}, SourceBrainID: sourceBrain.ID,
		SourceClaim: wireRef{ID: sourceClaim.ID, Version: 1}, DestinationBindingID: destBinding.ID,
		Limits: wireLimits{Currency: "USD", SpendMicroUnits: 10, Concurrency: 1, ModelSteps: 1, AttemptSeconds: 60, RootDeadline: e.clock.Now().Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	var promoteOut jobResourceOutput
	e.decodePayload(payload, &promoteOut)

	destClaimID := e.ids.New()
	destClaim := newMemoryClaim(destBrain.ID, destClaimID, 1, true, e.clock.Now())
	sourceBrainID, sourceClaimIDPtr := sourceBrain.ID, wireRef{ID: sourceClaim.ID, Version: 1}
	destClaim.SourceBrainID, destClaim.SourceClaim = &sourceBrainID, &sourceClaimIDPtr
	evidence := recallEvidence(destBrain, "00000000-0000-4000-8000-000000000042", destClaim, e.clock.Now())
	evidence.Kind = intentPromote
	evidence.SelectedContext = nil // promote's completion body has no context artifact
	evidence.StagedOutputs = []serenityStagedOutput{}

	recordPayload, err := e.callAs(e.actor, scope, opRecord, recordInput{
		JobID: promoteOut.Resource.ID, OperationID: *promoteOut.Resource.OperationID,
		Observation: wireObservation{Disposition: contract.DispositionSucceeded, Evidence: mustMarshal(t, evidence), Usage: wireUsage{Currency: "USD"}},
	})
	if err != nil {
		t.Fatalf("record promote success: %v", err)
	}
	var recorded jobResourceOutput
	e.decodePayload(recordPayload, &recorded)
	if recorded.Resource.State != jobSucceeded {
		t.Fatalf("promote job state = %q, want succeeded", recorded.Resource.State)
	}

	err = e.db.Read(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		promotions, err := listPromotionsBySource(e.ctx, unit, sourceBrain.ID, sourceClaim.ID)
		if err != nil {
			return err
		}
		if len(promotions) != 1 {
			t.Fatalf("promotions from source = %d, want 1", len(promotions))
		}
		p := promotions[0]
		if p.State != "completed" || p.DestinationClaimID != destClaimID || p.DestinationBrainID != destBrain.ID {
			t.Fatalf("promotion lineage = %+v, want completed with destination claim %s on brain %s", p, destClaimID, destBrain.ID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Now retract the source claim: evidence reports it inactive at a new
	// version, which must open a correction obligation against the
	// completed promotion above.
	retractPayload, err := e.callAs(e.actor, scope, opRetract, retractInput{
		Scope: wireScope{InstallationID: e.install, OrganizationID: org, WorkerID: worker}, BrainID: sourceBrain.ID,
		Claim: wireRef{ID: sourceClaim.ID, Version: 1}, Reason: "no longer accurate",
	})
	if err != nil {
		t.Fatalf("retract: %v", err)
	}
	var retractOut jobResourceOutput
	e.decodePayload(retractPayload, &retractOut)

	retractedClaim := newMemoryClaim(sourceBrain.ID, sourceClaim.ID, 2, false, e.clock.Now())
	retractEvidence := recallEvidence(sourceBrain, "00000000-0000-4000-8000-000000000043", retractedClaim, e.clock.Now())
	retractEvidence.Kind = intentRetract
	retractEvidence.SelectedContext = nil
	retractEvidence.StagedOutputs = []serenityStagedOutput{}
	activeRemoved := true
	retractEvidence.ActiveRecallRemoved = &activeRemoved

	recordRetract, err := e.callAs(e.actor, scope, opRecord, recordInput{
		JobID: retractOut.Resource.ID, OperationID: *retractOut.Resource.OperationID,
		Observation: wireObservation{Disposition: contract.DispositionSucceeded, Evidence: mustMarshal(t, retractEvidence), Usage: wireUsage{Currency: "USD"}},
	})
	if err != nil {
		t.Fatalf("record retract success: %v", err)
	}
	var retractRecorded jobResourceOutput
	e.decodePayload(recordRetract, &retractRecorded)
	var retractResult struct {
		Obligations       []wireRequirement `json:"obligations"`
		HistoricalErasure bool              `json:"historical_erasure"`
	}
	if err := jsonUnmarshal(retractRecorded.Resource.Result, &retractResult); err != nil {
		t.Fatalf("decode retract result: %v", err)
	}
	if retractResult.HistoricalErasure {
		t.Fatalf("retract reported historical_erasure=true; retract never performs historical erasure")
	}
	if len(retractResult.Obligations) != 1 || retractResult.Obligations[0].Code != reqReconciliation {
		t.Fatalf("retract obligations = %+v, want one %s obligation for the downstream promotion", retractResult.Obligations, reqReconciliation)
	}

	err = e.db.Read(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		open, err := listOpenReconciliation(e.ctx, unit, e.install)
		if err != nil {
			return err
		}
		if len(open) != 1 || open[0].Kind != reconcileRetraction {
			t.Fatalf("open reconciliation = %+v, want exactly one %s obligation", open, reconcileRetraction)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
}

// countReconciliationKind counts open reconciliation rows of one kind for
// this installation.
func countReconciliationKind(t *testing.T, e *testEnv, kind string) int {
	t.Helper()
	n := 0
	if err := e.db.Read(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
		open, err := listOpenReconciliation(e.ctx, unit, e.install)
		if err != nil {
			return err
		}
		for _, r := range open {
			if r.Kind == kind {
				n++
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("read reconciliation: %v", err)
	}
	return n
}

// TestDuplicateCompletionAndRestartPreserveOnePromotionLineage proves a
// redelivered `_memory.record` success for an already-completed promotion
// intent -- the controller resending because its own acknowledgement of the
// first response was lost -- never re-applies the destination claim or
// re-finalizes promotion lineage a second time; it opens exactly one more
// `promotion_correction` reconciliation obligation per redelivery instead
// (handleRecord's `intent.State == intentCompleted` guard). The same
// redelivery sent again after closing and reopening the database against a
// brand new *Service proves this durable-intent-state guard survives a
// process restart: it is driven entirely by the persisted intent row, never
// by in-memory session state.
func TestDuplicateCompletionAndRestartPreserveOnePromotionLineage(t *testing.T) {
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
		BrainID: sourceBrain.ID, Permissions: []string{permPromote}, Classification: classificationInternal,
		State: bindingActive, CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	e.seedBinding(sourceBinding)
	destBinding := &bindingRow{ID: e.ids.New(), Version: 1, InstallationID: e.install, OrganizationID: org, WorkerID: worker,
		BrainID: destBrain.ID, Permissions: []string{permWrite}, Classification: classificationInternal, State: bindingActive,
		CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	e.seedBinding(destBinding)

	scope := e.scopeAt(org, worker)
	wireCallerScope := wireScope{InstallationID: e.install, OrganizationID: org, WorkerID: worker}
	payload, err := e.callAs(e.actor, scope, opPromote, promoteInput{
		Scope: wireCallerScope, SourceBrainID: sourceBrain.ID,
		SourceClaim: wireRef{ID: sourceClaim.ID, Version: 1}, DestinationBindingID: destBinding.ID,
		Limits: wireLimits{Currency: "USD", SpendMicroUnits: 10, Concurrency: 1, ModelSteps: 1, AttemptSeconds: 60, RootDeadline: e.clock.Now().Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	var promoteOut jobResourceOutput
	e.decodePayload(payload, &promoteOut)

	destClaimID := e.ids.New()
	destClaim := newMemoryClaim(destBrain.ID, destClaimID, 1, true, e.clock.Now())
	sourceBrainID, sourceClaimRef := sourceBrain.ID, wireRef{ID: sourceClaim.ID, Version: 1}
	destClaim.SourceBrainID, destClaim.SourceClaim = &sourceBrainID, &sourceClaimRef
	evidence := recallEvidence(destBrain, "00000000-0000-4000-8000-000000000077", destClaim, e.clock.Now())
	evidence.Kind = intentPromote
	evidence.SelectedContext = nil
	evidence.StagedOutputs = []serenityStagedOutput{}
	evidenceBytes := mustMarshal(t, evidence)

	sendRecord := func() jobResourceOutput {
		t.Helper()
		recordPayload, err := e.callAs(e.actor, scope, opRecord, recordInput{
			JobID: promoteOut.Resource.ID, OperationID: *promoteOut.Resource.OperationID,
			Observation: wireObservation{Disposition: contract.DispositionSucceeded, Evidence: evidenceBytes, Usage: wireUsage{Currency: "USD"}},
		})
		if err != nil {
			t.Fatalf("record: %v", err)
		}
		var out jobResourceOutput
		e.decodePayload(recordPayload, &out)
		return out
	}
	assertOneCompletedPromotion := func(step string) {
		t.Helper()
		err := e.db.Read(e.ctx, e.actor, e.scope, func(unit contract.Unit) error {
			promotions, err := listPromotionsBySource(e.ctx, unit, sourceBrain.ID, sourceClaim.ID)
			if err != nil {
				return err
			}
			if len(promotions) != 1 {
				t.Fatalf("%s: promotions from source = %d, want exactly 1", step, len(promotions))
			}
			p := promotions[0]
			if p.State != "completed" || p.DestinationClaimID != destClaimID || p.DestinationClaimVersion != 1 || p.DestinationBrainID != destBrain.ID {
				t.Fatalf("%s: promotion lineage = %+v, want unchanged completed lineage to claim %s@1 on brain %s",
					step, p, destClaimID, destBrain.ID)
			}
			var claimVersions int
			row := unit.QueryRowContext(e.ctx, `SELECT COUNT(*) FROM memory_claims WHERE brain_id = ? AND id = ?`,
				string(destBrain.ID), string(destClaimID))
			if err := row.Scan(&claimVersions); err != nil {
				return err
			}
			if claimVersions != 1 {
				t.Fatalf("%s: destination claim row count = %d, want exactly 1 (never re-inserted)", step, claimVersions)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("%s: read: %v", step, err)
		}
	}

	// First completion: applies the promotion for real.
	first := sendRecord()
	if first.Resource.State != jobSucceeded {
		t.Fatalf("first record = %+v, want succeeded", first.Resource)
	}
	assertOneCompletedPromotion("after first completion")
	if n := countReconciliationKind(t, e, reconcilePromotionCorrection); n != 0 {
		t.Fatalf("after first completion: %d promotion_correction obligations, want 0", n)
	}

	// Duplicate completion (lost ack, controller redelivers): the intent is
	// already terminal, so this must open one correction obligation and
	// change nothing about the recorded lineage or cached claim.
	second := sendRecord()
	if second.Resource.State != jobSucceeded {
		t.Fatalf("duplicate record = %+v, want the job to still read succeeded", second.Resource)
	}
	assertOneCompletedPromotion("after duplicate completion")
	if n := countReconciliationKind(t, e, reconcilePromotionCorrection); n != 1 {
		t.Fatalf("after duplicate completion: %d promotion_correction obligations, want exactly 1", n)
	}

	// Restart: close and reopen the database, and construct a brand new
	// Service against it. The guard that refused to re-apply the duplicate
	// above is driven entirely by the persisted intent row, so it must
	// refuse a redelivery identically after restart.
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

	third := sendRecord()
	if third.Resource.State != jobSucceeded {
		t.Fatalf("post-restart duplicate record = %+v, want the job to still read succeeded", third.Resource)
	}
	assertOneCompletedPromotion("after post-restart duplicate completion")
	if n := countReconciliationKind(t, e, reconcilePromotionCorrection); n != 2 {
		t.Fatalf("after post-restart duplicate completion: %d promotion_correction obligations, want exactly 2 (one per redelivery)", n)
	}
}
