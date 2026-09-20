package memory

import (
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// TestListAuthorizedClaimsExcludesUnauthorizedBrainAndNeverRecalls proves
// memory.list (P00-017/R15-007): it projects source/freshness/lineage from
// locally cached claims for brains the caller can read, it never lists a
// brain the caller cannot read (a binding scoped to another worker is
// refused, not silently dropped from the page), and it never performs paid
// retrieval -- no peer operation is called at all, unlike memory.recall.
func TestListAuthorizedClaimsExcludesUnauthorizedBrainAndNeverRecalls(t *testing.T) {
	e := newEnv(t)
	org, worker := e.ids.New(), e.ids.New()
	otherWorker := e.ids.New()

	authorizedBrain := e.provisionedBrain(brainKindWorker, org, worker)
	e.seedBrain(authorizedBrain)
	unauthorizedBrain := e.provisionedBrain(brainKindWorker, org, otherWorker)
	e.seedBrain(unauthorizedBrain)

	authorizedBinding := &bindingRow{ID: e.ids.New(), Version: 1, InstallationID: e.install, OrganizationID: org, WorkerID: worker,
		BrainID: authorizedBrain.ID, Permissions: []string{permRead}, Classification: classificationInternal, State: bindingActive,
		CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	e.seedBinding(authorizedBinding)
	// A binding on the unauthorized brain, but scoped to a different worker
	// than the caller -- exactly the shape TestSelectUnauthorizedBrainNeverQueried
	// proves memory.select refuses.
	unauthorizedBinding := &bindingRow{ID: e.ids.New(), Version: 1, InstallationID: e.install, OrganizationID: org, WorkerID: otherWorker,
		BrainID: unauthorizedBrain.ID, Permissions: []string{permRead}, Classification: classificationInternal, State: bindingActive,
		CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	e.seedBinding(unauthorizedBinding)

	curator := e.ids.New()
	authorizedClaim := &claimRow{BrainID: authorizedBrain.ID, ID: e.ids.New(), Version: 1, Text: "authorized fact",
		Sources: []wireArtifactRef{}, Confidence: 900000, Freshness: e.clock.Now(), Active: true,
		CuratorID: curator, RecordedAt: e.clock.Now()}
	e.seedClaim(authorizedClaim)
	unauthorizedClaim := &claimRow{BrainID: unauthorizedBrain.ID, ID: e.ids.New(), Version: 1, Text: "unauthorized fact",
		Sources: []wireArtifactRef{}, Confidence: 900000, Freshness: e.clock.Now(), Active: true, RecordedAt: e.clock.Now()}
	e.seedClaim(unauthorizedClaim)

	scope := e.scopeAt(org, worker)
	wireCallerScope := wireScope{InstallationID: e.install, OrganizationID: org, WorkerID: worker}

	// Naming only the authorized binding lists exactly its claim, with
	// source/freshness/lineage intact, and calls no peer at all.
	e.ports.resetCalls()
	payload, err := e.callAs(e.actor, scope, opList, listInput{Scope: wireCallerScope, BindingIDs: []contract.ID{authorizedBinding.ID}})
	if err != nil {
		t.Fatalf("list with the authorized binding: %v", err)
	}
	var out listOutput
	e.decodePayload(payload, &out)
	if len(out.Items) != 1 || out.Items[0].ID != authorizedClaim.ID {
		t.Fatalf("list result = %+v, want exactly the authorized claim %s", out.Items, authorizedClaim.ID)
	}
	got := out.Items[0]
	if got.BrainID != authorizedBrain.ID || !got.Freshness.Equal(authorizedClaim.Freshness) ||
		got.CuratorID == nil || *got.CuratorID != curator {
		t.Fatalf("listed claim = %+v, want brain/freshness/curator lineage from the cached row", got)
	}
	if calls := e.ports.opsCalled(); len(calls) != 0 {
		t.Fatalf("memory.list called peers %v; it must never perform paid retrieval", calls)
	}

	// Naming the binding on the unauthorized brain is refused outright, not
	// silently excluded from an otherwise-successful page.
	_, err = e.callAs(e.actor, scope, opList, listInput{Scope: wireCallerScope, BindingIDs: []contract.ID{unauthorizedBinding.ID}})
	if f := decodeFault(err); f == nil || f.Code != contract.CodePermissionDenied {
		t.Fatalf("list naming a binding scoped to another worker: got err=%v, want permission_denied", err)
	}

	// Naming both still returns only the authorized brain's claim: an
	// authorized selection never leaks a sibling unauthorized brain's data
	// merely because both ids were named in binding_ids together.
	_, err = e.callAs(e.actor, scope, opList, listInput{
		Scope: wireCallerScope, BindingIDs: []contract.ID{authorizedBinding.ID, unauthorizedBinding.ID},
	})
	if f := decodeFault(err); f == nil || f.Code != contract.CodePermissionDenied {
		t.Fatalf("list naming one authorized and one unauthorized binding: got err=%v, want permission_denied", err)
	}
}

// TestListPaginatesWithinOneBrain proves memory.list's keyset cursor pages
// deterministically across more claims than fit in one page, in recorded
// order, without repeating or skipping a claim.
func TestListPaginatesWithinOneBrain(t *testing.T) {
	e := newEnv(t)
	org, worker := e.ids.New(), e.ids.New()
	brain := e.provisionedBrain(brainKindWorker, org, worker)
	e.seedBrain(brain)
	binding := &bindingRow{ID: e.ids.New(), Version: 1, InstallationID: e.install, OrganizationID: org, WorkerID: worker,
		BrainID: brain.ID, Permissions: []string{permRead}, Classification: classificationInternal, State: bindingActive,
		CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now()}
	e.seedBinding(binding)

	var ids []contract.ID
	for i := 0; i < 3; i++ {
		c := &claimRow{BrainID: brain.ID, ID: e.ids.New(), Version: 1, Text: "fact", Sources: []wireArtifactRef{},
			Confidence: 900000, Freshness: e.clock.Now(), Active: true, RecordedAt: e.clock.Now()}
		e.seedClaim(c)
		ids = append(ids, c.ID)
		e.clock.Advance(time.Second)
	}

	scope := e.scopeAt(org, worker)
	wireCallerScope := wireScope{InstallationID: e.install, OrganizationID: org, WorkerID: worker}
	limit := int64(2)

	var seen []contract.ID
	var cursor string
	for {
		in := listInput{Scope: wireCallerScope, BindingIDs: []contract.ID{binding.ID}, Limit: &limit}
		if cursor != "" {
			in.Cursor = cursor
		}
		payload, err := e.callAs(e.actor, scope, opList, in)
		if err != nil {
			t.Fatalf("list page: %v", err)
		}
		var out listOutput
		e.decodePayload(payload, &out)
		for _, item := range out.Items {
			seen = append(seen, item.ID)
		}
		if payload.NextCursor == nil {
			break
		}
		cursor = *payload.NextCursor
		if len(seen) > len(ids) {
			t.Fatalf("list pagination did not terminate: seen %v", seen)
		}
	}
	if len(seen) != len(ids) {
		t.Fatalf("paginated claim ids = %v, want all of %v exactly once", seen, ids)
	}
	for i, id := range ids {
		if seen[i] != id {
			t.Fatalf("paginated order = %v, want recorded order %v", seen, ids)
		}
	}
}
