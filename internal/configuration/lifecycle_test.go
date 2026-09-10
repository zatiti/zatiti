package configuration

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Baseline lifecycle coverage: bootstrap, typed CRUD, draft lifecycle, plan
// and apply, moves, revision lineage, export/import, the descriptor surface
// and the read-only boundary.

func TestBootstrapCreatesRootOrgAndChief(t *testing.T) {
	env := newEnv(t)
	org := env.getOrg(env.org)
	if org.ID != env.org || org.Key != "personal" || org.Version != 1 {
		t.Fatalf("bootstrap org mismatch: %+v", org)
	}
	if org.ParentID != nil && *org.ParentID != "" {
		t.Fatalf("bootstrap root must have no parent, got %v", org.ParentID)
	}
	chief := env.getWorker(env.chief)
	if chief.OrganizationID != env.org || chief.Key != "chief" {
		t.Fatalf("bootstrap chief mismatch: %+v", chief)
	}
	if chief.Profile != nil || chief.Limits != nil {
		t.Fatalf("bootstrap chief must carry null profile and limits, got %+v", chief)
	}
	if env.head() != 1 {
		t.Fatalf("bootstrap head = %d, want 1", env.head())
	}
}

func TestBootstrapRefusesSecondRun(t *testing.T) {
	env := newEnv(t)
	_ = env.expectFault("_configuration.bootstrap", bootstrapIn{
		InstallationID: env.install, OwnerID: env.owner,
		OrganizationID: env.ids.New(), ChiefID: env.ids.New(),
	}, contract.CodeConflict)
}

func TestBootstrapRefusesWrongInstallation(t *testing.T) {
	env := newEnv(t)
	_ = env.expectFault("_configuration.bootstrap", bootstrapIn{
		InstallationID: env.ids.New(), OwnerID: env.owner,
		OrganizationID: env.ids.New(), ChiefID: env.ids.New(),
	}, contract.CodePermissionDenied)
}

func TestOrganizationCreateAppliesAtomicallyWithChief(t *testing.T) {
	env := newEnv(t)
	orgID, chiefID := env.createOrg(&env.org, "engineering")
	org := env.getOrg(orgID)
	if org.ParentID == nil || *org.ParentID != env.org {
		t.Fatalf("child org parent mismatch: %+v", org)
	}
	if org.ChiefID != chiefID {
		t.Fatalf("child org chief mismatch: %s want %s", org.ChiefID, chiefID)
	}
	chief := env.getWorker(chiefID)
	if chief.OrganizationID != orgID {
		t.Fatalf("chief not homed in its organization: %+v", chief)
	}
}

func (e *testEnv) workerUpdateIn(org contract.ID, w wireWorker, name string) updateIn[workerDefIn] {
	return updateIn[workerDefIn]{
		Scope: e.scope, ID: w.ID, ExpectedVersion: w.Version,
		Definition: workerDefIn{OrganizationID: org, Key: w.Key, Name: name,
			SkillVersions: w.SkillVersions, Bindings: w.Bindings, Profile: w.Profile, Limits: w.Limits},
	}
}

func TestCreateUpdateArchiveRoundtrip(t *testing.T) {
	env := newEnv(t)
	org := env.org
	workerID := env.createWorker(org, "writer")

	worker := env.getWorker(workerID)
	payload := env.mustOK("worker.update", env.workerUpdateIn(org, worker, "Writer Renamed"))
	var updated struct {
		Draft    wireDraft  `json:"draft"`
		Resource wireWorker `json:"resource"`
	}
	env.decode(payload.Data, &updated)
	env.applyDraft(updated.Draft)
	got := env.getWorker(workerID)
	if got.Name != "Writer Renamed" || got.Version != worker.Version+1 {
		t.Fatalf("worker update not applied: %+v", got)
	}

	payload = env.mustOK("worker.archive", archiveIn{Scope: env.scope, ID: workerID, ExpectedVersion: got.Version})
	var archived struct {
		Draft    wireDraft  `json:"draft"`
		Resource wireWorker `json:"resource"`
	}
	env.decode(payload.Data, &archived)
	env.applyDraft(archived.Draft)
	if state, _ := env.rowState(kindWorker, workerID); state != stateArchived {
		t.Fatalf("archived worker state = %q, want archived", state)
	}
}

func TestArchiveRefusesArchivedTarget(t *testing.T) {
	env := newEnv(t)
	org := env.org
	workerID := env.createWorker(org, "gone")
	worker := env.getWorker(workerID)
	payload := env.mustOK("worker.archive", archiveIn{Scope: env.scope, ID: workerID, ExpectedVersion: worker.Version})
	var out struct {
		Draft wireDraft `json:"draft"`
	}
	env.decode(payload.Data, &out)
	env.applyDraft(out.Draft)
	after := env.getWorker(workerID)

	// staging an update on the archived worker succeeds, but the plan carries
	// the inactive_target refusal and apply refuses
	update := updateIn[workerDefIn]{
		Scope: env.scope, ID: workerID, ExpectedVersion: after.Version,
		Definition: workerDefIn{OrganizationID: org, Key: after.Key, Name: "zombie",
			SkillVersions: []wireRef{}, Bindings: []contract.ID{}, Profile: nil, Limits: nil},
	}
	payload = env.mustOK("worker.update", update)
	var staged struct {
		Draft wireDraft `json:"draft"`
	}
	env.decode(payload.Data, &staged)
	plan := env.planDraft(staged.Draft)
	found := false
	for _, d := range plan.Diagnostics {
		if d.Code == "inactive_target" && d.Severity == "error" {
			found = true
		}
	}
	if !found {
		t.Fatalf("plan must carry inactive_target diagnostics: %+v", plan.Diagnostics)
	}
	_ = env.expectFault("configuration.apply", applyInput{
		Scope: env.scope, PlanID: plan.ID, BaseRevision: plan.BaseRevision,
		CandidateDigest: plan.CandidateDigest,
	}, contract.CodeConflict)
}

func TestStaleExpectedVersionRefused(t *testing.T) {
	env := newEnv(t)
	workerID := env.createWorker(env.org, "stale")
	payload := env.mustOK("worker.update", updateIn[workerDefIn]{
		Scope: env.scope, ID: workerID, ExpectedVersion: 99,
		Definition: workerDefIn{OrganizationID: env.org, Key: "stale", Name: "x",
			SkillVersions: []wireRef{}, Bindings: []contract.ID{}, Profile: nil, Limits: nil},
	})
	var staged struct {
		Draft wireDraft `json:"draft"`
	}
	env.decode(payload.Data, &staged)
	plan := env.planDraft(staged.Draft)
	found := false
	for _, d := range plan.Diagnostics {
		if d.Code == "stale_version" && d.Severity == "error" {
			found = true
		}
	}
	if !found {
		t.Fatalf("plan must carry stale_version diagnostics: %+v", plan.Diagnostics)
	}
	_ = env.expectFault("configuration.apply", applyInput{
		Scope: env.scope, PlanID: plan.ID, BaseRevision: plan.BaseRevision,
		CandidateDigest: plan.CandidateDigest,
	}, contract.CodeConflict)
}

func TestDraftLifecycle(t *testing.T) {
	env := newEnv(t)

	// draft.create pins the current revision
	payload := env.mustOK("configuration.draft.create", draftCreateInput{Scope: env.scope, BaseRevision: env.head()})
	var created struct {
		Resource wireDraft `json:"resource"`
	}
	env.decode(payload.Data, &created)
	draft := created.Resource
	if draft.BaseRevision != env.head() || draft.Version != 1 || len(draft.Changes) != 0 {
		t.Fatalf("fresh draft mismatch: %+v", draft)
	}

	// append one change
	draft = env.stageInto(draft, workerChange(env.ids.New(), env.org, "drafted"))
	if len(draft.Changes) != 1 || draft.Version != 2 {
		t.Fatalf("draft append mismatch: version %d changes %d", draft.Version, len(draft.Changes))
	}

	// draft.get and draft.list observe the draft
	payload = env.mustOK("configuration.draft.get", getInput{Scope: env.scope, ID: draft.ID})
	var fetched struct {
		Resource wireDraft `json:"resource"`
	}
	env.decode(payload.Data, &fetched)
	if fetched.Resource.ID != draft.ID || len(fetched.Resource.Changes) != 1 {
		t.Fatalf("draft.get mismatch: %+v", fetched.Resource)
	}
	items := env.listItems("configuration.draft.list", listInput{Scope: env.scope})
	if len(items) != 1 {
		t.Fatalf("draft.list items = %d, want 1", len(items))
	}

	// discard refuses a stale expected version, then discards
	_ = env.expectFault("configuration.draft.discard", discardInput{Scope: env.scope,
		ID: draft.ID, ExpectedVersion: 8}, contract.CodeStaleVersion)
	payload = env.mustOK("configuration.draft.discard", discardInput{Scope: env.scope,
		ID: draft.ID, ExpectedVersion: draft.Version})
	var disposed struct {
		Resource wireDisposition `json:"resource"`
	}
	env.decode(payload.Data, &disposed)
	if disposed.Resource.State != draftDiscarded {
		t.Fatalf("discarded draft state = %q", disposed.Resource.State)
	}

	// a discarded draft no longer accepts changes
	_ = env.expectFault("_configuration.stage", stageInput{Scope: env.scope,
		Change: workerChange(env.ids.New(), env.org, "late"), DraftID: &disposed.Resource.ID}, contract.CodeConflict)

	// plan refuses a draft with no staged changes
	payload = env.mustOK("configuration.draft.create", draftCreateInput{Scope: env.scope, BaseRevision: env.head()})
	var empty struct {
		Resource wireDraft `json:"resource"`
	}
	env.decode(payload.Data, &empty)
	_ = env.expectFault("configuration.plan", planInput{Scope: env.scope,
		DraftID: empty.Resource.ID, ExpectedVersion: empty.Resource.Version}, contract.CodeInvalidInput)
}

func TestPlanSealsIdentityAndDependencies(t *testing.T) {
	env := newEnv(t)
	plan := env.applyAll(workerChange(env.ids.New(), env.org, "planner"))

	payload := env.mustOK("configuration.plan.get", getInput{Scope: env.scope, ID: plan.ID})
	var got struct {
		Resource wirePlan `json:"resource"`
	}
	env.decode(payload.Data, &got)
	if got.Resource.ID != plan.ID || got.Resource.CandidateDigest != plan.CandidateDigest {
		t.Fatalf("plan.get mismatch: %+v", got.Resource)
	}
	if got.Resource.CompilerVersion != compilerVersion || got.Resource.SchemaVersion != schemaVersion {
		t.Fatalf("plan identity mismatch: %+v", got.Resource)
	}
	pinned := false
	for _, d := range got.Resource.Dependencies {
		if d.ID == env.org && d.Version == 1 {
			pinned = true
		}
	}
	if !pinned {
		t.Fatalf("worker create must pin its organization: %+v", got.Resource.Dependencies)
	}
	items := env.listItems("configuration.plan.list", listInput{Scope: env.scope})
	if len(items) != 1 {
		t.Fatalf("plan.list items = %d, want 1", len(items))
	}
}

func TestRevisionGetAndList(t *testing.T) {
	env := newEnv(t)
	plan := env.applyAll(workerChange(env.ids.New(), env.org, "revised"))
	rev := env.apply(plan) // replay returns the original revision

	payload := env.mustOK("configuration.revision.get", getInput{Scope: env.scope, ID: rev.ID})
	var got struct {
		Resource wireRevision `json:"resource"`
	}
	env.decode(payload.Data, &got)
	if got.Resource.PlanID != plan.ID {
		t.Fatalf("revision plan mismatch: %+v", got.Resource)
	}
	items := env.listItems("configuration.revision.list", listInput{Scope: env.scope})
	if len(items) != 1 {
		t.Fatalf("revision.list items = %d, want 1", len(items))
	}
}

func TestWorkerMoveAndOrgMove(t *testing.T) {
	env := newEnv(t)
	child, _ := env.createOrg(&env.org, "child")
	workerID := env.createWorker(env.org, "nomad")

	worker := env.getWorker(workerID)
	payload := env.mustOK("worker.move", workerMoveIn{
		Scope: env.scope, ID: workerID, ExpectedVersion: worker.Version, OrganizationID: child,
	})
	var moved struct {
		Resource wireDraft `json:"resource"`
	}
	env.decode(payload.Data, &moved)
	env.applyDraft(moved.Resource)
	if got := env.getWorker(workerID); got.OrganizationID != child {
		t.Fatalf("worker.move did not land: %+v", got)
	}

	// reparent the child back under the root
	grandchild, _ := env.createOrg(&child, "grandchild")
	co := env.getOrg(child)
	payload = env.mustOK("organization.move", orgMoveIn{
		Scope: env.scope, ID: child, ExpectedVersion: co.Version, ParentID: env.org,
	})
	var orgMoved struct {
		Resource wireDraft `json:"resource"`
	}
	env.decode(payload.Data, &orgMoved)
	env.applyDraft(orgMoved.Resource)
	movedOrg := env.getOrg(child)
	if movedOrg.ParentID == nil || *movedOrg.ParentID != env.org {
		t.Fatalf("organization.move did not land: %+v", movedOrg)
	}
	if got := env.getOrg(grandchild); got.ParentID == nil || *got.ParentID != child {
		t.Fatalf("grandchild reparented unexpectedly: %+v", got)
	}
}

func TestSnapshotReturnsAncestryBindingsAndWorker(t *testing.T) {
	env := newEnv(t)
	child, chiefID := env.createOrg(&env.org, "scoped")
	bindingID := env.createBinding(child, chiefID)

	scope := env.scope
	scope.OrganizationID = child
	scope.WorkerID = chiefID
	payload := env.mustOKAs(scope, "_configuration.snapshot", snapshotIn{Scope: scope})
	var snap struct {
		Resource wireScopeSnapshot `json:"resource"`
	}
	env.decode(payload.Data, &snap)
	if snap.Resource.Revision != env.head() {
		t.Fatalf("snapshot revision %d, want head %d", snap.Resource.Revision, env.head())
	}
	if len(snap.Resource.Ancestors) != 2 {
		t.Fatalf("snapshot ancestors = %d, want 2 (child + root)", len(snap.Resource.Ancestors))
	}
	if snap.Resource.Ancestors[0].ID != child || snap.Resource.Ancestors[1].ID != env.org {
		t.Fatalf("snapshot ancestry order wrong: %+v", snap.Resource.Ancestors)
	}
	if snap.Resource.Worker == nil || snap.Resource.Worker.ID != chiefID {
		t.Fatalf("snapshot worker mismatch: %+v", snap.Resource.Worker)
	}
	found := false
	for _, b := range snap.Resource.Bindings {
		if b.ID == bindingID {
			found = true
		}
	}
	if !found {
		t.Fatalf("snapshot missing effective binding %s: %+v", bindingID, snap.Resource.Bindings)
	}
}

func TestExportImportRoundtripSameInstallation(t *testing.T) {
	env := newEnv(t)
	orgID, _ := env.createOrg(&env.org, "portable")
	workerID := env.createWorker(orgID, "exported")

	payload := env.mustOK("organization.export", getInput{Scope: env.scope, ID: orgID})
	var exported struct {
		Job wireJob `json:"job"`
	}
	env.decode(payload.Data, &exported)
	if exported.Job.State != "succeeded" || exported.Job.ResultArtifact == nil {
		t.Fatalf("export job mismatch: %+v", exported.Job)
	}

	// the bundle is canonical: a second export carries the same digest
	payload = env.mustOK("organization.export", getInput{Scope: env.scope, ID: orgID})
	var again struct {
		Job wireJob `json:"job"`
	}
	env.decode(payload.Data, &again)
	if again.Job.ResultArtifact.Digest != exported.Job.ResultArtifact.Digest {
		t.Fatalf("export digest not stable: %s vs %s",
			exported.Job.ResultArtifact.Digest, again.Job.ResultArtifact.Digest)
	}
	var bundle exportBundle
	if err := contract.DecodeStrict(env.blobs.published[exported.Job.ResultArtifact.Digest], &bundle); err != nil {
		t.Fatalf("bundle decode: %v", err)
	}
	if bundle.Format != exportFormat || bundle.Organization == nil || bundle.Organization.ID != orgID {
		t.Fatalf("bundle identity mismatch: format %q org %+v", bundle.Format, bundle.Organization)
	}
	found := false
	for _, w := range bundle.Workers {
		if w.ID == workerID {
			found = true
		}
	}
	if !found {
		t.Fatalf("export bundle lost worker %s: %+v", workerID, bundle)
	}

	// importing the same bundle stages duplicate creates: the plan seals with
	// the identity collisions recorded and apply refuses, so live state never
	// changes
	payload = env.mustOK("organization.import", importRequest(env, exported.Job.ResultArtifact, nil))
	var imported struct {
		Draft       wireDraft        `json:"draft"`
		Diagnostics []wireDiagnostic `json:"diagnostics"`
	}
	env.decode(payload.Data, &imported)
	if len(imported.Diagnostics) != 0 {
		t.Fatalf("same-installation import must not need rebinding: %+v", imported.Diagnostics)
	}
	plan, err := env.tryPlan(imported.Draft)
	if err != nil {
		t.Fatalf("plan seals with diagnostics, refusing only at apply: %v", err)
	}
	collided := false
	for _, d := range plan.Diagnostics {
		if d.Code == "identity_collision" && d.Severity == "error" {
			collided = true
		}
	}
	if !collided {
		t.Fatalf("duplicate import plan must carry identity_collision diagnostics: %+v", plan.Diagnostics)
	}
	before := env.head()
	_ = env.expectFault("configuration.apply", applyInput{
		Scope: env.scope, PlanID: plan.ID, BaseRevision: plan.BaseRevision,
		CandidateDigest: plan.CandidateDigest,
	}, contract.CodeConflict)
	if after := env.head(); after != before {
		t.Fatalf("failed import changed head: %d -> %d", before, after)
	}
}

func TestExportExcludesArchivedObjectsAndSecretMaterial(t *testing.T) {
	env := newEnv(t)
	orgID, chiefID := env.createOrg(&env.org, "exported")
	projectID := env.ids.New()
	env.applyChange(createChange(kindProject, projectID, newProjectDef(projectID, orgID, "archived")))
	proj := env.getProject(projectID)
	payload := env.mustOK("project.archive", archiveIn{Scope: env.scope, ID: projectID, ExpectedVersion: proj.Version})
	var out struct {
		Draft wireDraft `json:"draft"`
	}
	env.decode(payload.Data, &out)
	env.applyDraft(out.Draft)

	payload = env.mustOK("organization.export", getInput{Scope: env.scope, ID: orgID})
	var exported struct {
		Job wireJob `json:"job"`
	}
	env.decode(payload.Data, &exported)
	var bundle exportBundle
	if err := contract.DecodeStrict(env.blobs.published[exported.Job.ResultArtifact.Digest], &bundle); err != nil {
		t.Fatalf("bundle decode: %v", err)
	}
	if len(bundle.Projects) != 0 {
		t.Fatalf("archived project must not export: %+v", bundle.Projects)
	}
	if len(bundle.Workers) != 1 || bundle.Workers[0].ID != chiefID {
		t.Fatalf("export must carry exactly the active chief worker: %+v", bundle.Workers)
	}
	raw := string(env.blobs.published[exported.Job.ResultArtifact.Digest])
	for _, banned := range []string{"\"secret", "credential_value", "\"task", "run_history"} {
		if strings.Contains(raw, banned) {
			t.Fatalf("export bundle carries %s: %s", banned, raw)
		}
	}
}

func TestUnknownOperationAndReadOnlyRefusals(t *testing.T) {
	env := newEnv(t)
	_ = env.expectFault("no.such.operation", map[string]any{}, contract.CodeNotFound)

	// a mutation on a read-only unit is refused
	raw, err := json.Marshal(draftCreateInput{Scope: env.scope, BaseRevision: env.head()})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	err = env.db.Read(env.ctx, env.actor, env.scope.toContract(), func(unit contract.Unit) error {
		_, err := env.svc.Handle(env.ctx, unit,
			contract.Invocation{Operation: "configuration.draft.create", Version: 1, Input: raw})
		return err
	})
	var f *contract.Fault
	if err == nil || !errors.As(err, &f) || f.Code != contract.CodeInvalidInput {
		t.Fatalf("mutation on read-only unit = %v, want invalid_input fault", err)
	}
}

func TestDescriptorsSurface(t *testing.T) {
	env := newEnv(t)
	descriptors := env.svc.Descriptors()
	if len(descriptors) != len(opMetas) {
		t.Fatalf("descriptors = %d, want %d", len(descriptors), len(opMetas))
	}
	catalog := map[string]contract.Descriptor{}
	for _, d := range descriptors {
		if d.Owner != ownerName || d.Version != 1 {
			t.Fatalf("descriptor %s owner/version mismatch: %+v", d.ID, d)
		}
		catalog[d.ID] = d
	}
	create := catalog["organization.create"]
	if create.Mode != contract.ModeMutation || !create.SubmissionKey || create.MCP != "zatiti_organization_create" {
		t.Fatalf("organization.create descriptor mismatch: %+v", create)
	}
	get := catalog["organization.get"]
	if get.Mode != contract.ModeQuery || get.Effect != contract.EffectLocal {
		t.Fatalf("organization.get descriptor mismatch: %+v", get)
	}
	if len(catalog["organization.export"].CompletionSchema) == 0 {
		t.Fatalf("export descriptor must carry a completion schema")
	}
	if catalog["organization.create"].InputSchema == nil {
		t.Fatalf("descriptors must carry input schemas")
	}
}
