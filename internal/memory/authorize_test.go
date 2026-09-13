package memory

import (
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// TestSelectUnauthorizedBrainNeverQueried proves `_memory.select` refuses a
// binding the caller's scope does not cover (R15-004: parentage and group
// membership grant no implicit read) instead of returning any brain data.
func TestSelectUnauthorizedBrainNeverQueried(t *testing.T) {
	e := newEnv(t)
	org := e.ids.New()
	otherWorker := e.ids.New()
	brain := e.provisionedBrain(brainKindWorker, org, otherWorker)
	e.seedBrain(brain)

	// Binding scoped to a different worker than the caller.
	binding := &bindingRow{ID: e.ids.New(), Version: 1, InstallationID: e.install, OrganizationID: org, WorkerID: otherWorker,
		BrainID: brain.ID, Permissions: []string{permRead}, Classification: classificationInternal, State: bindingActive,
		CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	e.seedBinding(binding)

	callerWorker := e.ids.New()
	scope := e.scopeAt(org, callerWorker)
	payload, err := e.callAs(e.actor, scope, opSelect, selectInput{
		Scope:      wireScope{InstallationID: e.install, OrganizationID: org, WorkerID: callerWorker},
		BindingIDs: []contract.ID{binding.ID}, Permission: permRead, MinimumFreshness: time.Time{},
	})
	f := decodeFault(err)
	if f == nil || f.Code != contract.CodePermissionDenied {
		t.Fatalf("select on a binding scoped to another worker: got payload=%v err=%v, want permission_denied", payload, err)
	}
	if calls := e.ports.opsCalled(); len(calls) != 0 {
		t.Fatalf("unauthorized select called peers %v, want none", calls)
	}
}

// TestBindingScopeRestrictedToProject proves a binding scoped to one
// project does not authorize a call made under a different project in the
// same organization: scope containment is exact, not hierarchical.
func TestBindingScopeRestrictedToProject(t *testing.T) {
	e := newEnv(t)
	org := e.ids.New()
	worker := e.ids.New()
	brain := e.provisionedBrain(brainKindOrganization, org, "")
	e.seedBrain(brain)

	projectA := e.ids.New()
	binding := &bindingRow{ID: e.ids.New(), Version: 1, InstallationID: e.install, OrganizationID: org, ProjectID: projectA,
		BrainID: brain.ID, Permissions: []string{permRead}, Classification: classificationInternal, State: bindingActive,
		CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	e.seedBinding(binding)

	projectB := e.ids.New()
	scope := contract.Scope{InstallationID: e.install, OrganizationID: org, WorkerID: worker, ProjectID: projectB}
	_, err := e.callAs(e.actor, scope, opSelect, selectInput{
		Scope:      wireScope{InstallationID: e.install, OrganizationID: org, WorkerID: worker, ProjectID: projectB},
		BindingIDs: []contract.ID{binding.ID}, Permission: permRead, MinimumFreshness: time.Time{},
	})
	f := decodeFault(err)
	if f == nil || f.Code != contract.CodePermissionDenied {
		t.Fatalf("select under a different project: got err=%v, want permission_denied", err)
	}

	// The same binding DOES authorize a call actually under project A.
	scope.ProjectID = projectA
	payload, err := e.callAs(e.actor, scope, opSelect, selectInput{
		Scope:      wireScope{InstallationID: e.install, OrganizationID: org, WorkerID: worker, ProjectID: projectA},
		BindingIDs: []contract.ID{binding.ID}, Permission: permRead, MinimumFreshness: time.Time{},
	})
	if err != nil {
		t.Fatalf("select under the authorized project: %v", err)
	}
	var out selectOutput
	e.decodePayload(payload, &out)
	if len(out.Bindings) != 1 || out.Bindings[0].ID != binding.ID {
		t.Fatalf("select under the authorized project: got %+v, want binding %s", out, binding.ID)
	}
}

// TestSelectStaleFreshness proves an unavailable freshness bound is a
// prerequisite failure (R15-007), never silently treated as current.
func TestSelectStaleFreshness(t *testing.T) {
	e := newEnv(t)
	org, worker := e.ids.New(), e.ids.New()
	brain := e.provisionedBrain(brainKindWorker, org, worker)
	brain.FreshnessAt = e.clock.Now().Add(-24 * time.Hour)
	e.seedBrain(brain)

	binding := &bindingRow{ID: e.ids.New(), Version: 1, InstallationID: e.install, OrganizationID: org, WorkerID: worker,
		BrainID: brain.ID, Permissions: []string{permRead}, Classification: classificationInternal, State: bindingActive,
		CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	e.seedBinding(binding)

	scope := e.scopeAt(org, worker)
	_, err := e.callAs(e.actor, scope, opSelect, selectInput{
		Scope:      wireScope{InstallationID: e.install, OrganizationID: org, WorkerID: worker},
		BindingIDs: []contract.ID{binding.ID}, Permission: permRead, MinimumFreshness: e.clock.Now(),
	})
	f := decodeFault(err)
	if f == nil || f.Code != contract.CodePrerequisiteMissing {
		t.Fatalf("select with a stale brain: got err=%v, want prerequisite_missing", err)
	}

	// A minimum_freshness at or before the brain's own freshness succeeds.
	payload, err := e.callAs(e.actor, scope, opSelect, selectInput{
		Scope:      wireScope{InstallationID: e.install, OrganizationID: org, WorkerID: worker},
		BindingIDs: []contract.ID{binding.ID}, Permission: permRead, MinimumFreshness: brain.FreshnessAt,
	})
	if err != nil {
		t.Fatalf("select within the freshness bound: %v", err)
	}
	var out selectOutput
	e.decodePayload(payload, &out)
	if len(out.Bindings) != 1 {
		t.Fatalf("select within the freshness bound: got %+v", out)
	}
}
