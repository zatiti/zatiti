package configuration

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/storage"
)

// Acceptance cases assigned to configuration by the AGENTS.md acceptance
// matrix. Each test drives the public surface through Service.Handle inside
// real storage transactions and asserts behavior, not shape.

// newImportDest opens a second installation on its own database — object
// identities are globally unique per database, so cross-installation imports
// need a clean store — sharing the clock, ids, ports and the blob store the
// artifact travels through. Both installations bootstrap the same root
// organization and chief identities (a shared bootstrap template), so an
// imported child organization resolves its parent reference while drafts
// satisfy the base_revision >= 1 requirement.
func newImportDest(t *testing.T, e *testEnv) *testEnv {
	t.Helper()
	ctx := e.ctx
	db, err := storage.Open(ctx, storage.Config{Path: filepath.Join(t.TempDir(), "configuration-dest.db")})
	if err != nil {
		t.Fatalf("storage.Open destination: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc, err := New(contract.Dependencies{Clock: e.clock, IDs: e.ids, Ports: e.ports, Blobs: e.blobs})
	if err != nil {
		t.Fatalf("configuration.New destination: %v", err)
	}
	if err := db.Migrate(ctx, svc.Migrations()); err != nil {
		t.Fatalf("migrate destination: %v", err)
	}
	clone := &testEnv{
		t: t, ctx: ctx, db: db, svc: svc,
		ports: e.ports, blobs: e.blobs, clock: e.clock, ids: e.ids,
		install: e.ids.New(), owner: e.ids.New(),
	}
	clone.actor = contract.Actor{PrincipalID: clone.owner, Kind: contract.KindService}
	clone.scope = wireScope{InstallationID: clone.install}
	clone.mustOK("_configuration.bootstrap", bootstrapIn{
		InstallationID: clone.install, OwnerID: clone.owner,
		OrganizationID: e.org, ChiefID: e.chief,
	})
	clone.org, clone.chief = e.org, e.chief
	return clone
}

// deleteChange stages one delete wireChange around a fetched definition.
func deleteChange(kind string, id contract.ID, expectedVersion int64, def any) wireChange {
	return wireChange{Kind: kind, Action: actionDelete, ID: id, ExpectedVersion: expectedVersion, Definition: rawDef(def)}
}

// planDiagnostics fetches the sealed diagnostics of one plan.
func planDiagnostics(e *testEnv, plan wirePlan) []wireDiagnostic {
	e.t.Helper()
	payload := e.mustOK("configuration.plan.get", getInput{Scope: e.scope, ID: plan.ID})
	var out struct {
		Resource wirePlan `json:"resource"`
	}
	e.decode(payload.Data, &out)
	return out.Resource.Diagnostics
}

func hasDiagnostic(diags []wireDiagnostic, code, severity string) bool {
	for _, d := range diags {
		if d.Code == code && d.Severity == severity {
			return true
		}
	}
	return false
}

// Z01.cross_scope_reference: a team in one organization may not reference a
// worker homed in another organization; the plan seals the refusal as an
// error diagnostic, apply refuses, and no effective state changes.
func TestCrossScopeReferenceRefusedWithoutBinding(t *testing.T) {
	env := newEnv(t)
	child, _ := env.createOrg(&env.org, "other")
	wid := env.createWorker(env.org, "outsider")
	teamID := env.ids.New()

	draft := env.stage(createChange(kindTeam, teamID,
		newTeamDef(teamID, child, "poachers", []contract.ID{wid})))
	plan := env.planDraft(draft)
	if !hasDiagnostic(planDiagnostics(env, plan), "cross_organization_reference", "error") {
		t.Fatalf("plan must seal cross_organization_reference: %+v", plan.Diagnostics)
	}

	before := env.head()
	_ = env.expectFault("configuration.apply", applyInput{
		Scope: env.scope, PlanID: plan.ID, BaseRevision: plan.BaseRevision,
		CandidateDigest: plan.CandidateDigest,
	}, contract.CodeConflict)
	if _, found := env.rowState(kindTeam, teamID); found {
		t.Fatalf("refused cross-scope team must not exist")
	}
	if after := env.head(); after != before {
		t.Fatalf("refused apply moved head %d -> %d", before, after)
	}
	if got := env.getWorker(wid); got.OrganizationID != env.org {
		t.Fatalf("referenced worker must be untouched: %+v", got)
	}
}

// Z04.concurrent_apply: two plans sealed against one base revision; the first
// to activate wins, the second is stale. The losing bundle must be atomic —
// none of its objects land and the head advanced exactly once.
func TestConcurrentApplySecondPlanStale(t *testing.T) {
	env := newEnv(t)
	base := env.head()
	idA, idB := env.ids.New(), env.ids.New()

	draftA := env.stage(workerChange(idA, env.org, "winner"))
	planA := env.planDraft(draftA)
	draftB := env.stage(workerChange(idB, env.org, "loser"))
	planB := env.planDraft(draftB)
	if planA.BaseRevision != base || planB.BaseRevision != base {
		t.Fatalf("both plans must share base %d: %d and %d", base, planA.BaseRevision, planB.BaseRevision)
	}

	env.apply(planA)
	if after := env.head(); after != base+1 {
		t.Fatalf("head = %d, want exactly one advance to %d", after, base+1)
	}
	_ = env.expectFault("configuration.apply", applyInput{
		Scope: env.scope, PlanID: planB.ID, BaseRevision: planB.BaseRevision,
		CandidateDigest: planB.CandidateDigest,
	}, contract.CodeStaleVersion)
	if _, found := env.rowState(kindWorker, idB); found {
		t.Fatalf("losing plan's worker must not exist")
	}
	if got := env.getWorker(idA); got.Key != "winner" {
		t.Fatalf("winning plan's worker missing: %+v", got)
	}
}

// Z04.stale_dependencies: a pinned dependency that moved invalidates the
// sealed plan. requireDependencyPins is bidirectional — an unpinned new
// dependency and an unresolvable sealed dependency both refuse with
// stale_version — and a plan sealed against a superseded revision cannot
// activate end to end; regenerating repins and applies.
func TestStaleDependenciesInvalidatePlan(t *testing.T) {
	env := newEnv(t)
	org := env.ids.New()
	other := env.ids.New()

	// a dependency discovered after sealing is not pinned by the plan
	err := requireDependencyPins(
		[]wireRef{{ID: org, Version: 1}},
		[]wireRef{{ID: org, Version: 1}, {ID: other, Version: 2}})
	var f *contract.Fault
	if !errors.As(err, &f) || f.Code != contract.CodeStaleVersion || !strings.Contains(f.Message, "not pinned by the plan") {
		t.Fatalf("new dependency must refuse as stale_version, got %v", err)
	}

	// a sealed dependency that no longer resolves also refuses
	err = requireDependencyPins(
		[]wireRef{{ID: org, Version: 1}, {ID: other, Version: 1}},
		[]wireRef{{ID: org, Version: 1}})
	if !errors.As(err, &f) || f.Code != contract.CodeStaleVersion || !strings.Contains(f.Message, "no longer resolves") {
		t.Fatalf("unresolvable dependency must refuse as stale_version, got %v", err)
	}
	if err := requireDependencyPins([]wireRef{{ID: org, Version: 1}}, []wireRef{{ID: org, Version: 1}}); err != nil {
		t.Fatalf("matching pins must pass: %v", err)
	}

	// end to end: a plan sealed against the current head cannot activate
	// after any other bundle advanced the configuration
	wid := env.ids.New()
	draft := env.stage(workerChange(wid, env.org, "pinned"))
	plan := env.planDraft(draft)
	env.applyChange(workerChange(env.ids.New(), env.org, "interloper"))
	_ = env.expectFault("configuration.apply", applyInput{
		Scope: env.scope, PlanID: plan.ID, BaseRevision: plan.BaseRevision,
		CandidateDigest: plan.CandidateDigest,
	}, contract.CodeStaleVersion)

	// regeneration repins against the new revision and applies
	regen := env.stage(workerChange(wid, env.org, "pinned"))
	fresh := env.planDraft(regen)
	env.apply(fresh)
	if got := env.getWorker(wid); got.Key != "pinned" {
		t.Fatalf("regenerated plan did not apply: %+v", got)
	}
}

// Z04.explicit_removal: omission from a bundle is never deletion, and
// explicit removal refuses while obligations remain — an organization with
// children or workers and a worker still designated chief of an active
// organization cannot be archived.
func TestExplicitRemovalOmissionAndGuards(t *testing.T) {
	env := newEnv(t)
	wid := env.createWorker(env.org, "omitted")
	teamID := env.createTeam(env.org, "kept", wid)

	// a bundle touching only the worker leaves the team untouched
	worker := env.getWorker(wid)
	payload := env.mustOK("worker.update", env.workerUpdateIn(env.org, worker, "Renamed Only"))
	var staged struct {
		Draft wireDraft `json:"draft"`
	}
	env.decode(payload.Data, &staged)
	env.applyDraft(staged.Draft)
	team := env.getTeam(teamID)
	if team.Version != 1 || team.Key != "kept" {
		t.Fatalf("omission must not delete: team %+v", team)
	}
	if state, _ := env.rowState(kindTeam, teamID); state != stateActive {
		t.Fatalf("team state = %q, want active", state)
	}

	// deleting the root organization refuses while it still has children
	// and workers; archiving is the disable-new-work path and is allowed
	child, _ := env.createOrg(&env.org, "dependent")
	before := env.head()
	root := env.getOrg(env.org)
	orgPlan := env.planDraft(env.stage(deleteChange(kindOrganization, env.org, root.Version, root)))
	_ = env.expectFault("configuration.apply", applyInput{
		Scope: env.scope, PlanID: orgPlan.ID,
		BaseRevision: orgPlan.BaseRevision, CandidateDigest: orgPlan.CandidateDigest,
	}, contract.CodeConflict)
	if state, _ := env.rowState(kindOrganization, env.org); state != stateActive {
		t.Fatalf("root organization state = %q, want active", state)
	}
	if state, _ := env.rowState(kindOrganization, child); state != stateActive {
		t.Fatalf("child organization must survive: state %q", state)
	}

	// deleting the designated chief refuses while it serves an active org
	chief := env.getWorker(env.chief)
	chiefPlan := env.planDraft(env.stage(deleteChange(kindWorker, env.chief, chief.Version, chief)))
	_ = env.expectFault("configuration.apply", applyInput{
		Scope: env.scope, PlanID: chiefPlan.ID,
		BaseRevision: chiefPlan.BaseRevision, CandidateDigest: chiefPlan.CandidateDigest,
	}, contract.CodeConflict)
	if state, _ := env.rowState(kindWorker, env.chief); state != stateActive {
		t.Fatalf("serving chief state = %q, want active", state)
	}
	if after := env.head(); after != before {
		t.Fatalf("guarded deletes must not move the head: %d -> %d", before, after)
	}
}

// Z04.old_authority: the plan previews authority under pre-apply state and
// apply re-runs the check against the real digest. A deny decision fails
// closed at planning; a review decision seals a review requirement and its
// decision requirements, and activation demands an eligible approval bound to
// the exact candidate digest — false is not permission.
func TestApplyAuthorityOldStateAndDecisions(t *testing.T) {
	env := newEnv(t)

	// deny fails closed at plan time, before anything is staged for apply
	env.ports.mu.Lock()
	env.ports.decision = "deny"
	env.ports.reasons = []string{"parent organization forbids configuration changes"}
	env.ports.mu.Unlock()
	draft := env.stage(workerChange(env.ids.New(), env.org, "denied"))
	_, err := env.tryPlan(draft)
	var pf *contract.Fault
	if !errors.As(err, &pf) || pf.Code != contract.CodePermissionDenied || !strings.Contains(pf.Message, "parent organization forbids") {
		t.Fatalf("deny decision must fail closed at plan, got %v", err)
	}

	// review seals the requirement and decision requirements but defers
	env.ports.mu.Lock()
	env.ports.decision = "review"
	env.ports.decReqs = []wireDecisionRequirement{{HumanRequired: true}}
	env.ports.mu.Unlock()
	draft = env.stage(workerChange(env.ids.New(), env.org, "reviewed"))
	plan := env.planDraft(draft)
	found := false
	for _, r := range plan.AuthorityRequirements {
		if r.Code == contract.CodeReviewRequired {
			found = true
		}
	}
	if !found || len(plan.Decisions) == 0 {
		t.Fatalf("review plan must seal review requirement and decisions: %+v / %+v",
			plan.AuthorityRequirements, plan.Decisions)
	}

	// an eligible approval over the candidate digest lets the apply through
	env.ports.mu.Lock()
	env.ports.approveOK = true
	env.ports.mu.Unlock()
	env.apply(plan)

	// the next plan under the same review decision activates only with a
	// real approval; without one the apply refuses carrying the sealed
	// decision requirements
	draft = env.stage(workerChange(env.ids.New(), env.org, "unapproved"))
	plan2 := env.planDraft(draft)
	env.ports.mu.Lock()
	env.ports.approveOK = false
	env.ports.mu.Unlock()
	fault := env.expectFault("configuration.apply", applyInput{
		Scope: env.scope, PlanID: plan2.ID, BaseRevision: plan2.BaseRevision,
		CandidateDigest: plan2.CandidateDigest,
	}, contract.CodeReviewRequired)
	if fault.Details == nil || !strings.Contains(string(fault.Details), "human_required") {
		t.Fatalf("review_required fault must carry the sealed decision requirements, got %s", fault.Details)
	}
}

// Z04.submission_replay: an identical retry of an applied plan returns the
// original revision without a new revision or event; changed parameters are
// refused as stale.
func TestSubmissionReplayReturnsOriginalRevision(t *testing.T) {
	env := newEnv(t)
	wid := env.ids.New()
	plan := env.applyAll(workerChange(wid, env.org, "replayable"))
	rev := env.apply(plan)

	again := env.apply(plan)
	if again.ID != rev.ID {
		t.Fatalf("replay returned revision %s, want original %s", again.ID, rev.ID)
	}
	items := env.listItems("configuration.revision.list", listInput{Scope: env.scope})
	if len(items) != 1 {
		t.Fatalf("revision.list items = %d, want 1 (replay must not add a revision)", len(items))
	}
	if after := env.head(); after != plan.BaseRevision+1 {
		t.Fatalf("head = %d after replay, want %d", after, plan.BaseRevision+1)
	}
	if got := env.getWorker(wid); got.Version != 1 {
		t.Fatalf("replay re-applied the change: worker version %d", got.Version)
	}

	// a retry with different parameters is refused
	_ = env.expectFault("configuration.apply", applyInput{
		Scope: env.scope, PlanID: plan.ID,
		BaseRevision:    plan.BaseRevision,
		CandidateDigest: string(contract.Digest(strings.Repeat("ab", 32))),
	}, contract.CodeStaleVersion)
}

// Z05.empty_bindings: a worker without bindings sees none in its snapshot,
// and a sibling's binding does not leak — effective bindings match the
// worker exactly.
func TestSnapshotBindingsExactWorkerScope(t *testing.T) {
	env := newEnv(t)
	child, chiefID := env.createOrg(&env.org, "scoped")
	wid := env.createWorker(child, "plain")
	bindingID := env.createBinding(child, chiefID)

	snap := func(worker contract.ID) wireScopeSnapshot {
		scope := env.scope
		scope.OrganizationID = child
		scope.WorkerID = worker
		payload := env.mustOKAs(scope, "_configuration.snapshot", snapshotIn{Scope: scope})
		var out struct {
			Resource wireScopeSnapshot `json:"resource"`
		}
		env.decode(payload.Data, &out)
		return out.Resource
	}

	plain := snap(wid)
	if len(plain.Bindings) != 0 {
		t.Fatalf("worker without bindings must see none: %+v", plain.Bindings)
	}
	chief := snap(chiefID)
	if len(chief.Bindings) != 1 || chief.Bindings[0].ID != bindingID {
		t.Fatalf("chief snapshot bindings mismatch: %+v", chief.Bindings)
	}
}

// Z15.canonical_roundtrip: export, import, plan, activate and export again
// preserves meaning, identities, list order and skill pins byte for byte —
// the digests of both exports are equal. Both installations share the
// bootstrap template identities, so the exported child organization's parent
// reference resolves in the destination without rebinding.
func TestCanonicalRoundtripAcrossInstallations(t *testing.T) {
	env := newEnv(t)
	orgID, childChief := env.createOrg(&env.org, "portable")
	w1 := env.createWorker(orgID, "alpha")
	w2 := env.createWorker(orgID, "beta")
	env.createTeam(orgID, "ordered", w2, w1, childChief) // deliberate non-id order
	projectID := env.ids.New()
	env.applyChange(createChange(kindProject, projectID, newProjectDef(projectID, orgID, "portable-project")))
	env.createBinding(orgID, w1)

	payload := env.mustOK("organization.export", getInput{Scope: env.scope, ID: orgID})
	var exported struct {
		Job wireJob `json:"job"`
	}
	env.decode(payload.Data, &exported)

	dest := newImportDest(t, env)
	payload = dest.mustOK("organization.import", importRequest(dest, exported.Job.ResultArtifact, nil))
	var imported struct {
		Draft       wireDraft        `json:"draft"`
		Diagnostics []wireDiagnostic `json:"diagnostics"`
	}
	dest.decode(payload.Data, &imported)
	if len(imported.Diagnostics) != 0 {
		t.Fatalf("clean import must need no rebinding: %+v", imported.Diagnostics)
	}
	dest.applyDraft(imported.Draft)

	payload = dest.mustOK("organization.export", getInput{Scope: dest.scope, ID: orgID})
	var reExported struct {
		Job wireJob `json:"job"`
	}
	dest.decode(payload.Data, &reExported)
	if reExported.Job.ResultArtifact.Digest != exported.Job.ResultArtifact.Digest {
		t.Fatalf("roundtrip digest drift: %s vs %s",
			exported.Job.ResultArtifact.Digest, reExported.Job.ResultArtifact.Digest)
	}
	var before, after exportBundle
	if err := contract.DecodeStrict(env.blobs.published[exported.Job.ResultArtifact.Digest], &before); err != nil {
		t.Fatalf("source bundle decode: %v", err)
	}
	if err := contract.DecodeStrict(env.blobs.published[reExported.Job.ResultArtifact.Digest], &after); err != nil {
		t.Fatalf("destination bundle decode: %v", err)
	}
	if got := after.Teams[0].WorkerIDs; len(got) != 3 || got[0] != w2 || got[1] != w1 || got[2] != childChief {
		t.Fatalf("roundtrip lost list order: %v", got)
	}
	if len(after.Workers) != len(before.Workers) || after.Organization.ID != orgID || after.Organization.Key != "portable" {
		t.Fatalf("roundtrip identity drift: org %+v workers %d", after.Organization, len(after.Workers))
	}
}

// Z15.cross_installation_rebind: an opaque credential reference never
// becomes live authority automatically — import without an explicit
// destination rebinding refuses, and with one the staged definition carries
// the destination's connection while the source stays untouched.
func TestCrossInstallationImportRequiresRebinding(t *testing.T) {
	env := newEnv(t)
	orgID, _ := env.createOrg(&env.org, "profiled")
	wid := env.ids.New()
	connSource, connDest := env.ids.New(), env.ids.New()
	profile := wireExecutionProfile{
		ID: env.ids.New(), Version: 1, Executor: "hosted", Model: "test-model",
		ConnectionID: connSource, ProviderDestination: "https://provider.example",
		Capabilities: []string{"completion"}, CostBound: wireMoney{Currency: "USD", MicroUnits: 100},
		Classification: "internal", ContextCapture: "advisory",
	}
	def := newWorkerDef(wid, orgID, "profiled-worker")
	def.Profile = &profile
	env.applyChange(createChange(kindWorker, wid, def))

	payload := env.mustOK("organization.export", getInput{Scope: env.scope, ID: orgID})
	var exported struct {
		Job wireJob `json:"job"`
	}
	env.decode(payload.Data, &exported)

	dest := newImportDest(t, env)
	_ = dest.expectFault("organization.import", importRequest(dest, exported.Job.ResultArtifact, nil),
		contract.CodeInvalidInput)

	payload = dest.mustOK("organization.import", importRequest(dest, exported.Job.ResultArtifact,
		[]importRebinding{{SourceRef: string(connSource), DestinationRef: string(connDest)}}))
	var imported struct {
		Draft       wireDraft        `json:"draft"`
		Diagnostics []wireDiagnostic `json:"diagnostics"`
	}
	dest.decode(payload.Data, &imported)
	if len(imported.Diagnostics) != 1 || imported.Diagnostics[0].Path != "connection_id" ||
		imported.Diagnostics[0].Code != "rebound" {
		t.Fatalf("rebinding must be surfaced as a diagnostic: %+v", imported.Diagnostics)
	}
	dest.applyDraft(imported.Draft)

	migrated := dest.getWorker(wid)
	if migrated.Profile == nil || migrated.Profile.ConnectionID != connDest {
		t.Fatalf("activation must use the rebound connection: %+v", migrated.Profile)
	}
	if source := env.getWorker(wid); source.Profile == nil || source.Profile.ConnectionID != connSource {
		t.Fatalf("source installation must be untouched: %+v", source.Profile)
	}
}

// Z17.atomic_chief_creation: the organization and its chief stage and
// activate together or neither lands — a peer activation failure rolls the
// whole bundle back, and the same plan applies once the failure is gone.
func TestOrganizationAndChiefActivateAtomically(t *testing.T) {
	env := newEnv(t)
	orgID, chiefID := env.ids.New(), env.ids.New()
	draft := env.stage(orgChangeDef(&orgID, &env.org, chiefID, "atomic"))
	draft = env.stageInto(draft, workerChange(chiefID, orgID, "atomic-chief"))
	plan := env.planDraft(draft)

	env.ports.mu.Lock()
	env.ports.fail["_identity.activate"] = &contract.Fault{
		Code: contract.CodeInternalError, Message: "identity store unavailable",
	}
	env.ports.mu.Unlock()
	before := env.head()
	_ = env.expectFault("configuration.apply", applyInput{
		Scope: env.scope, PlanID: plan.ID, BaseRevision: plan.BaseRevision,
		CandidateDigest: plan.CandidateDigest,
	}, contract.CodeInternalError)

	if _, found := env.rowState(kindOrganization, orgID); found {
		t.Fatalf("failed bundle must not leave the organization behind")
	}
	if _, found := env.rowState(kindWorker, chiefID); found {
		t.Fatalf("failed bundle must not leave the chief behind")
	}
	if after := env.head(); after != before {
		t.Fatalf("failed bundle moved head %d -> %d", before, after)
	}

	env.ports.mu.Lock()
	delete(env.ports.fail, "_identity.activate")
	env.ports.mu.Unlock()
	env.apply(plan)
	if len(env.ports.callsOf("_identity.activate")) == 0 {
		t.Fatalf("activation must delegate to the identity owner")
	}
	if state, _ := env.rowState(kindOrganization, orgID); state != stateActive {
		t.Fatalf("retry did not activate the organization: %q", state)
	}
	if state, _ := env.rowState(kindWorker, chiefID); state != stateActive {
		t.Fatalf("retry did not activate the chief: %q", state)
	}
	if got := env.getWorker(chiefID); got.OrganizationID != orgID {
		t.Fatalf("chief not homed in its organization: %+v", got)
	}
}

// Z17.hierarchy_cycles: a self-parent is refused at planning; a deep cycle
// built through a reparent passes planning and is rejected at activation by
// the acyclicity invariant, leaving the hierarchy unchanged.
func TestHierarchyCycleRefusal(t *testing.T) {
	env := newEnv(t)

	// self-parent is caught at planning as a cycle diagnostic
	self := env.getOrg(env.org)
	payload := env.mustOK("organization.move", orgMoveIn{
		Scope: env.scope, ID: env.org, ExpectedVersion: self.Version, ParentID: env.org,
	})
	var staged struct {
		Resource wireDraft `json:"resource"`
	}
	env.decode(payload.Data, &staged)
	plan := env.planDraft(staged.Resource)
	if !hasDiagnostic(planDiagnostics(env, plan), "cycle", "error") {
		t.Fatalf("plan must seal a cycle diagnostic for self-parent: %+v", plan.Diagnostics)
	}
	_ = env.expectFault("configuration.apply", applyInput{
		Scope: env.scope, PlanID: plan.ID, BaseRevision: plan.BaseRevision,
		CandidateDigest: plan.CandidateDigest,
	}, contract.CodeConflict)

	// a deep cycle root -> mid -> leaf, then root reparented under leaf
	b, _ := env.createOrg(&env.org, "cycle-b")
	c, _ := env.createOrg(&b, "cycle-c")
	bRow := env.getOrg(b)
	payload = env.mustOK("organization.move", orgMoveIn{
		Scope: env.scope, ID: b, ExpectedVersion: bRow.Version, ParentID: c,
	})
	env.decode(payload.Data, &staged)
	deep := env.planDraft(staged.Resource)
	for _, d := range deep.Diagnostics {
		if d.Severity == "error" {
			t.Fatalf("deep cycle passes planning, got error diagnostic %+v", d)
		}
	}
	before := env.head()
	_ = env.expectFault("configuration.apply", applyInput{
		Scope: env.scope, PlanID: deep.ID, BaseRevision: deep.BaseRevision,
		CandidateDigest: deep.CandidateDigest,
	}, contract.CodeConflict)
	if got := env.getOrg(b); got.ParentID == nil || *got.ParentID != env.org {
		t.Fatalf("rejected cycle must leave the hierarchy unchanged: b parent %+v", got.ParentID)
	}
	if after := env.head(); after != before {
		t.Fatalf("rejected cycle moved head %d -> %d", before, after)
	}
	if got := env.getOrg(c); got.ParentID == nil || *got.ParentID != b {
		t.Fatalf("cycle refusal disturbed sibling hierarchy: %+v", got)
	}
}

// Z17.chief_replacement: replacing the chief keeps the organization
// identity, history and obligations, leaves exactly one active chief, and
// the replaced chief is no longer guarded from archive.
func TestChiefReplacementKeepsIdentityAndSingleChief(t *testing.T) {
	env := newEnv(t)
	child, first := env.createOrg(&env.org, "dynasty")
	second := env.createWorker(child, "heir")
	env.createWorker(child, "bystander")

	org := env.getOrg(child)
	payload := env.mustOK("organization.chief.replace", chiefReplaceIn{
		Scope: env.scope, ID: child, ExpectedVersion: org.Version, ChiefID: second,
	})
	var staged struct {
		Resource wireDraft `json:"resource"`
	}
	env.decode(payload.Data, &staged)
	env.applyDraft(staged.Resource)

	replaced := env.getOrg(child)
	if replaced.ID != child || replaced.Key != "dynasty" || replaced.Version != org.Version+1 {
		t.Fatalf("chief replacement disturbed organization identity: %+v", replaced)
	}
	if replaced.ChiefID != second {
		t.Fatalf("chief replacement did not land: chief %s want %s", replaced.ChiefID, second)
	}
	if state, _ := env.rowState(kindWorker, first); state != stateActive {
		t.Fatalf("former chief must be retained as history, state %q", state)
	}

	// the former chief no longer serves an organization: archive succeeds,
	// leaving exactly one active chief in the installation
	payload = env.mustOK("worker.archive", archiveIn{
		Scope: env.scope, ID: first, ExpectedVersion: env.getWorker(first).Version,
	})
	var out struct {
		Draft wireDraft `json:"draft"`
	}
	env.decode(payload.Data, &out)
	env.applyDraft(out.Draft)
	if state, _ := env.rowState(kindWorker, first); state != stateArchived {
		t.Fatalf("former chief state = %q, want archived", state)
	}
	if state, _ := env.rowState(kindWorker, second); state != stateActive {
		t.Fatalf("new chief state = %q, want active", state)
	}
	if got := env.getOrg(child); got.ChiefID != second {
		t.Fatalf("organization lost its chief: %+v", got)
	}
}

// Z17.scoped_reparenting: moving a worker between organizations re-homes it
// and its effective bindings are recomputed from the new ancestry — a
// binding scoped to the old organization stops matching without an explicit
// transfer.
func TestScopedReparentingRecomputesEffectiveScope(t *testing.T) {
	env := newEnv(t)
	child, _ := env.createOrg(&env.org, "origin")
	wid := env.createWorker(child, "transferred")
	bindingID := env.createBinding(child, wid)

	snap := func(org contract.ID) wireScopeSnapshot {
		scope := env.scope
		scope.OrganizationID = org
		scope.WorkerID = wid
		payload := env.mustOKAs(scope, "_configuration.snapshot", snapshotIn{Scope: scope})
		var out struct {
			Resource wireScopeSnapshot `json:"resource"`
		}
		env.decode(payload.Data, &out)
		return out.Resource
	}

	if got := snap(child); len(got.Bindings) != 1 || got.Bindings[0].ID != bindingID {
		t.Fatalf("binding must bind in its own organization: %+v", got.Bindings)
	}

	worker := env.getWorker(wid)
	payload := env.mustOK("worker.move", workerMoveIn{
		Scope: env.scope, ID: wid, ExpectedVersion: worker.Version, OrganizationID: env.org,
	})
	var staged struct {
		Resource wireDraft `json:"resource"`
	}
	env.decode(payload.Data, &staged)
	env.applyDraft(staged.Resource)

	if got := env.getWorker(wid); got.OrganizationID != env.org {
		t.Fatalf("worker.move did not land: %+v", got)
	}
	if got := snap(env.org); len(got.Bindings) != 0 {
		t.Fatalf("old-organization binding must not follow the worker: %+v", got.Bindings)
	}
}

// configuration.rollback.plan: a sealed revision restores the definitions it
// replaced as an ordinary forward plan against the current head — an update
// revision rolls back to the prior definition with continuous versioning, a
// create revision rolls back to deletion, and a stale base revision refuses.
// No database rewind and no erased obligations.
func TestRollbackPlanRestoresPriorDefinitions(t *testing.T) {
	env := newEnv(t)

	// an update revision: rollback restores the prior name at the post-apply
	// version, and a rollback requested against a stale head refuses
	wid := env.ids.New()
	createPlan := env.applyAll(workerChange(wid, env.org, "original"))
	env.apply(createPlan) // replay returns the create revision
	worker := env.getWorker(wid)
	payload := env.mustOK("worker.update", env.workerUpdateIn(env.org, worker, "renamed"))
	var staged struct {
		Draft wireDraft `json:"draft"`
	}
	env.decode(payload.Data, &staged)
	updateRev := env.applyDraft(staged.Draft)
	if got := env.getWorker(wid); got.Name != "renamed" || got.Version != 2 {
		t.Fatalf("update did not land: %+v", got)
	}

	_ = env.expectFault("configuration.rollback.plan", rollbackInput{
		Scope: env.scope, RevisionID: updateRev.ID, BaseRevision: 1,
	}, contract.CodeStaleVersion)

	head := env.head()
	payload = env.mustOK("configuration.rollback.plan", rollbackInput{
		Scope: env.scope, RevisionID: updateRev.ID, BaseRevision: head,
	})
	var rolled struct {
		Resource wirePlan `json:"resource"`
	}
	env.decode(payload.Data, &rolled)
	rollback := rolled.Resource
	if rollback.BaseRevision != head || rollback.ID == updateRev.ID {
		t.Fatalf("rollback plan must seal fresh against the current head: %+v", rollback)
	}
	// the lineage marker is inspectable on the staged draft the plan sealed
	payload = env.mustOK("configuration.draft.get", getInput{Scope: env.scope, ID: rollback.DraftID})
	var lineage struct {
		Resource wireDraft `json:"resource"`
	}
	env.decode(payload.Data, &lineage)
	if !hasDiagnostic(lineage.Resource.Diagnostics, "rollback_plan", "info") {
		t.Fatalf("rollback draft must carry the rollback_plan diagnostic: %+v", lineage.Resource.Diagnostics)
	}
	if len(rollback.Changes) != 1 {
		t.Fatalf("rollback of a one-object revision carries one change: %+v", rollback.Changes)
	}
	var change wireChange
	if err := json.Unmarshal(rollback.Changes[0], &change); err != nil {
		t.Fatalf("decode rollback change: %v", err)
	}
	if change.Kind != kindWorker || change.Action != actionUpdate || change.ID != wid || change.ExpectedVersion != 2 {
		t.Fatalf("rollback change mismatch: %+v", change)
	}
	var restored wireWorker
	if err := json.Unmarshal(change.Definition, &restored); err != nil {
		t.Fatalf("decode restored definition: %v", err)
	}
	if restored.Name != "Worker original" || restored.Version != 3 {
		t.Fatalf("restored definition must carry the prior name at the post-apply version: %+v", restored)
	}
	env.apply(rollback)
	if got := env.getWorker(wid); got.Name != "Worker original" || got.Version != 3 {
		t.Fatalf("rollback did not restore the prior definition: %+v", got)
	}

	// a create revision: rollback deletes the created object and nothing else
	victimID := env.ids.New()
	victimPlan := env.applyAll(workerChange(victimID, env.org, "victim"))
	victimRev := env.apply(victimPlan)
	head = env.head()
	payload = env.mustOK("configuration.rollback.plan", rollbackInput{
		Scope: env.scope, RevisionID: victimRev.ID, BaseRevision: head,
	})
	rolled = struct {
		Resource wirePlan `json:"resource"`
	}{}
	env.decode(payload.Data, &rolled)
	if len(rolled.Resource.Changes) != 1 {
		t.Fatalf("rollback of a create revision carries one change: %+v", rolled.Resource.Changes)
	}
	if err := json.Unmarshal(rolled.Resource.Changes[0], &change); err != nil {
		t.Fatalf("decode create-rollback change: %v", err)
	}
	if change.Kind != kindWorker || change.Action != actionDelete || change.ID != victimID || change.ExpectedVersion != 1 {
		t.Fatalf("create rollback must derive a delete at the created version: %+v", change)
	}
	env.apply(rolled.Resource)
	if _, found := env.rowState(kindWorker, victimID); found {
		t.Fatalf("create rollback must remove the created worker")
	}
	if after := env.head(); after != head+1 {
		t.Fatalf("rollback advanced head %d -> %d, want %d", head, after, head+1)
	}
}

// An execution profile whose connection does not resolve still stages, plans
// and activates — definitions remain usable while unavailable, and the plan
// names the missing connection as a prerequisite_missing requirement bound to
// the profile so executable use is gated separately.
func TestProfilePlanNamesMissingConnectionPrerequisite(t *testing.T) {
	env := newEnv(t)
	profileID := env.ids.New()
	conn := env.ids.New() // well-formed but unresolved connection reference
	profile := wireExecutionProfile{
		ID: profileID, Version: 1, Executor: "hosted", Model: "test-model",
		ConnectionID: conn, ProviderDestination: "https://provider.example",
		Capabilities: []string{"completion"}, CostBound: wireMoney{Currency: "USD", MicroUnits: 100},
		Classification: "internal", ContextCapture: "advisory",
	}
	draft := env.stage(createChange(kindExecutionProfile, profileID, profile))
	plan := env.planDraft(draft)
	found := false
	for _, r := range plan.Requirements {
		if r.Code == contract.CodePrerequisiteMissing && r.ResourceID == conn {
			found = true
		}
	}
	if !found {
		t.Fatalf("plan must name the unresolved connection prerequisite: %+v", plan.Requirements)
	}
	env.apply(plan)

	payload := env.mustOK("execution_profile.get", getInput{Scope: env.scope, ID: profileID})
	var got struct {
		Resource wireExecutionProfile `json:"resource"`
	}
	env.decode(payload.Data, &got)
	if got.Resource.ConnectionID != conn || got.Resource.Version != 1 {
		t.Fatalf("profile definition must activate without its connection: %+v", got.Resource)
	}
}
