package configuration

import (
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// P05 step 1 coverage: a new worker snapshot resolves the exact worker,
// instructions, skill version closure, tool bindings, execution profile,
// connection and classification constraints context assembly needs, and a
// stale connection version blocks apply.

// TestStaleConnectionVersionBlocksApply exercises the generic
// requireDependencyPins staleness mechanism (compiler.go, already proven
// abstractly by TestStaleDependenciesInvalidatePlan) against a real
// connection-kind dependency: a plan pins the connection's version as
// reported by _connections.validate, and if that version advances behind
// the plan's back before apply, activation refuses as stale rather than
// silently running against a since-changed connection.
func TestStaleConnectionVersionBlocksApply(t *testing.T) {
	env := newEnv(t)
	connID := env.ids.New()
	connDef := map[string]any{
		"id": connID, "version": 1, "scope": env.scope,
		"provider": "test-provider", "account_identity": "acct-1",
		"credential_ref":   "ref-1",
		"destinations":     []string{"https://example.com"},
		"allowed_scopes":   []string{"read"},
		"validation_state": "unverified",
	}
	env.ports.connVersions = map[contract.ID]int64{connID: 1}

	draft := env.stage(createChange(kindConnection, connID, connDef))
	plan := env.planDraft(draft)
	pinned := false
	for _, d := range plan.Dependencies {
		if d.ID == connID && d.Version == 1 {
			pinned = true
		}
	}
	if !pinned {
		t.Fatalf("plan must pin the connection's current version as a dependency: %+v", plan.Dependencies)
	}

	// The connection's version advances behind the plan's back.
	env.ports.connVersions[connID] = 2

	_ = env.expectFault("configuration.apply", applyInput{
		Scope: env.scope, PlanID: plan.ID, BaseRevision: plan.BaseRevision, CandidateDigest: plan.CandidateDigest,
	}, contract.CodeStaleVersion)

	// Regenerating the plan against the new connection version applies.
	fresh := env.planDraft(env.stage(createChange(kindConnection, env.ids.New(), connDef)))
	env.apply(fresh)
}

// TestWorkerSnapshotResolvesSkillProfileBindingsAndLimits proves
// _configuration.snapshot resolves the complete worker context assembly
// needs off one call: instructions, the skill version closure, tool/skill/
// connection bindings, the execution profile (naming its connection), and
// limits -- not merely the worker's identity.
func TestWorkerSnapshotResolvesSkillProfileBindingsAndLimits(t *testing.T) {
	env := newEnv(t)
	skillA, skillB := env.ids.New(), env.ids.New()
	connID := env.ids.New()
	profileID := env.ids.New()
	wid := env.ids.New()

	profile := wireExecutionProfile{
		ID: profileID, Version: 1, Executor: "hosted", Model: "test-model",
		ConnectionID: connID, ProviderDestination: "https://provider.example",
		Capabilities: []string{"completion"}, CostBound: wireMoney{Currency: "USD", MicroUnits: 500},
		Classification: "internal", ContextCapture: "complete",
	}
	def := newWorkerDef(wid, env.org, "context-worker")
	def.Instructions = "act as a release manager"
	def.SkillVersions = []wireRef{{ID: skillA, Version: 3}, {ID: skillB, Version: 1}}
	def.Profile = &profile
	def.Limits = &wireLimits{
		Currency: "USD", SpendMicroUnits: 1000, Concurrency: 1, ModelSteps: 100,
		ChildCount: 8, DelegationDepth: 3, AttemptSeconds: 1800,
		RootDeadline: env.clock.Now().Add(24 * 3600 * 1e9),
	}
	env.applyChange(createChange(kindWorker, wid, def))
	bindingID := env.createBinding(env.org, wid)

	scope := env.scope
	scope.WorkerID = wid
	payload := env.mustOKAs(scope, "_configuration.snapshot", snapshotIn{Scope: scope})
	var snap struct {
		Resource wireScopeSnapshot `json:"resource"`
	}
	env.decode(payload.Data, &snap)

	w := snap.Resource.Worker
	if w == nil || w.ID != wid {
		t.Fatalf("snapshot did not resolve the worker: %+v", snap.Resource.Worker)
	}
	if w.Instructions != def.Instructions {
		t.Fatalf("snapshot lost worker instructions: %q", w.Instructions)
	}
	if len(w.SkillVersions) != 2 || w.SkillVersions[0] != def.SkillVersions[0] || w.SkillVersions[1] != def.SkillVersions[1] {
		t.Fatalf("snapshot did not resolve the pinned skill version closure: %+v", w.SkillVersions)
	}
	if w.Profile == nil || w.Profile.ID != profileID || w.Profile.ConnectionID != connID {
		t.Fatalf("snapshot did not resolve the worker's execution profile/connection: %+v", w.Profile)
	}
	if w.Limits == nil || w.Limits.Currency != "USD" || w.Limits.SpendMicroUnits != 1000 {
		t.Fatalf("snapshot did not resolve worker limits: %+v", w.Limits)
	}
	boundFound := false
	for _, b := range snap.Resource.Bindings {
		if b.ID == bindingID {
			boundFound = true
		}
	}
	if !boundFound {
		t.Fatalf("snapshot did not resolve the worker's tool binding: %+v", snap.Resource.Bindings)
	}
}
