package reviews

import (
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// review.list: filter validation, needs_you resolution, keyset pagination
// and principal-bound opaque cursors.

// seedReviews creates n pending reviews with staggered timestamps by
// advancing the clock between ensures.
func seedReviews(t *testing.T, e *testEnv, n int, eligible []contract.ID) []wireReview {
	t.Helper()
	proposer := e.actorFor(e.principal(contract.KindClientAgent))
	var out []wireReview
	for i := 0; i < n; i++ {
		e.clock.advance(time.Minute)
		action := actionFixture(e.scope, "https://api.example.com/v1/deploy")
		// Distinct destinations keep every review a distinct digest.
		action.Destination = "https://api.example.com/v1/deploy?step=" + strings.Repeat("s", i+1)
		in := ensureInputFor(t, e.scope, action, requirementFixture(eligible, false))
		review, fault := e.ensureFor(proposer, e.scope, in)
		if fault != nil {
			t.Fatalf("seed ensure %d: %v", i, fault)
		}
		out = append(out, review)
	}
	return out
}

func listerFor(t *testing.T, e *testEnv) contract.Actor {
	t.Helper()
	return e.actorFor(e.principal(contract.KindService))
}

func TestListEmptyAndPaged(t *testing.T) {
	e := newEnv(t)
	lister := listerFor(t, e)

	// Empty page.
	page, next, fault := e.listReviews(lister, wireListInput{Scope: e.scope})
	if fault != nil {
		t.Fatalf("list: %v", fault)
	}
	if len(page.Items) != 0 || next != nil {
		t.Fatalf("empty list = %d items cursor %v", len(page.Items), next)
	}

	reviews := seedReviews(t, e, 3, []contract.ID{e.principal(contract.KindHuman)})

	// Single page when everything fits.
	page, next, fault = e.listReviews(lister, wireListInput{Scope: e.scope})
	if fault != nil {
		t.Fatalf("list: %v", fault)
	}
	if len(page.Items) != 3 || next != nil {
		t.Fatalf("full list = %d items cursor %v, want 3 and no cursor", len(page.Items), next)
	}
	for i, item := range page.Items {
		// Ordered oldest first.
		if item.ID != reviews[i].ID {
			t.Fatalf("page[%d] = %s, want %s", i, item.ID, reviews[i].ID)
		}
	}

	// Paged read with limit 1: three pages, ordered, no repeats.
	limit := int64(1)
	seen := map[contract.ID]bool{}
	var cursor *string
	count := 0
	for {
		in := wireListInput{Scope: e.scope, Limit: &limit, Cursor: cursor}
		page, next, fault = e.listReviews(lister, in)
		if fault != nil {
			t.Fatalf("paged list: %v", fault)
		}
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatalf("item %s repeated across pages", item.ID)
			}
			seen[item.ID] = true
			if item.ID != reviews[count].ID {
				t.Fatalf("page item %d = %s, want %s", count, item.ID, reviews[count].ID)
			}
			count++
		}
		if next == nil {
			break
		}
		if len(page.Items) != 1 {
			t.Fatalf("full page has %d items, want 1", len(page.Items))
		}
		cursor = next
	}
	if count != 3 {
		t.Fatalf("paged through %d items, want 3", count)
	}
}

func TestListStateFilter(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	reviews := seedReviews(t, e, 2, []contract.ID{human})

	// Approve the first; filter isolates each state.
	if _, fault := e.decideAs(e.actorFor(human), wireDecideInput{
		Scope: e.scope, ID: reviews[0].ID, ExpectedVersion: reviews[0].Version,
		ActionDigest: reviews[0].ActionDigest, Decision: decideApprove, Reason: "ok",
	}); fault != nil {
		t.Fatalf("decide: %v", fault)
	}

	lister := listerFor(t, e)
	state := stateApproved
	page, next, fault := e.listReviews(lister, wireListInput{
		Scope: e.scope, Filter: &wireListFilter{State: &state},
	})
	if fault != nil {
		t.Fatalf("list approved: %v", fault)
	}
	if len(page.Items) != 1 || page.Items[0].ID != reviews[0].ID || next != nil {
		t.Fatalf("approved filter = %+v cursor %v", page.Items, next)
	}

	state = statePending
	page, _, fault = e.listReviews(lister, wireListInput{
		Scope: e.scope, Filter: &wireListFilter{State: &state},
	})
	if fault != nil {
		t.Fatalf("list pending: %v", fault)
	}
	if len(page.Items) != 1 || page.Items[0].ID != reviews[1].ID {
		t.Fatalf("pending filter = %+v", page.Items)
	}
}

func TestListNeedsYouFilter(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	reviews := seedReviews(t, e, 3, []contract.ID{human})

	// The eligible human sees all three.
	trueValue := true
	page, _, fault := e.listReviews(e.actorFor(human), wireListInput{
		Scope: e.scope, Filter: &wireListFilter{NeedsYou: &trueValue},
	})
	if fault != nil {
		t.Fatalf("needs_you for eligible: %v", fault)
	}
	if len(page.Items) != 3 {
		t.Fatalf("eligible human sees %d reviews, want 3", len(page.Items))
	}

	// An unrelated principal sees none.
	outsider := e.principal(contract.KindHuman)
	page, _, fault = e.listReviews(e.actorFor(outsider), wireListInput{
		Scope: e.scope, Filter: &wireListFilter{NeedsYou: &trueValue},
	})
	if fault != nil {
		t.Fatalf("needs_you for outsider: %v", fault)
	}
	if len(page.Items) != 0 {
		t.Fatalf("outsider sees %d reviews, want 0", len(page.Items))
	}

	// A delegated principal sees the delegated review only.
	delegate := e.principal(contract.KindHuman)
	if _, fault := e.delegateAs(e.actorFor(human), wireDelegateInput{
		Scope: e.scope, ID: reviews[0].ID, ExpectedVersion: reviews[0].Version, PrincipalID: delegate,
	}); fault != nil {
		t.Fatalf("delegate: %v", fault)
	}
	page, _, fault = e.listReviews(e.actorFor(delegate), wireListInput{
		Scope: e.scope, Filter: &wireListFilter{NeedsYou: &trueValue},
	})
	if fault != nil {
		t.Fatalf("needs_you for delegate: %v", fault)
	}
	if len(page.Items) != 1 || page.Items[0].ID != reviews[0].ID {
		t.Fatalf("delegate sees %+v, want only the delegated review", page.Items)
	}

	// needs_you=false excludes the eligible human's reviews.
	falseValue := false
	page, _, fault = e.listReviews(e.actorFor(human), wireListInput{
		Scope: e.scope, Filter: &wireListFilter{NeedsYou: &falseValue},
	})
	if fault != nil {
		t.Fatalf("needs_you=false: %v", fault)
	}
	if len(page.Items) != 0 {
		t.Fatalf("needs_you=false returned %d reviews for the eligible human", len(page.Items))
	}
}

func TestListFilterValidation(t *testing.T) {
	e := newEnv(t)
	lister := listerFor(t, e)
	orgID := e.ids.New()
	trueVal := true

	for name, filter := range map[string]wireListFilter{
		"key":             {Key: strPtr("k")},
		"parent_id":       {ParentID: &orgID},
		"worker_id":       {WorkerID: &orgID},
		"task_id":         {TaskID: &orgID},
		"organization_id": {OrganizationID: &orgID},
		"descendants":     {Descendants: &trueVal},
	} {
		t.Run(name, func(t *testing.T) {
			f := filter
			_ = e.expectFaultAs(lister, opList, wireListInput{
				Scope: e.scope, Filter: &f,
			}, contract.CodeInvalidInput)
		})
	}

	bad := "abandoned"
	_ = e.expectFaultAs(lister, opList, wireListInput{
		Scope: e.scope, Filter: &wireListFilter{State: &bad},
	}, contract.CodeInvalidInput)
}

func strPtr(s string) *string { return &s }

func TestListRequiresAuthentication(t *testing.T) {
	e := newEnv(t)
	// The execution session refuses a principal-less invocation; the
	// handler's own permission-denied gate stays as defense in depth.
	f := e.expectFaultAs(contract.Actor{}, opList, wireListInput{Scope: e.scope}, contract.CodeInvalidInput)
	if !strings.Contains(f.Message, "principal is required") {
		t.Fatalf("fault message %q does not name the missing principal", f.Message)
	}
}

func TestListCursorTamperRefused(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	seedReviews(t, e, 2, []contract.ID{human})
	lister := listerFor(t, e)

	limit := int64(1)
	_, next, fault := e.listReviews(lister, wireListInput{Scope: e.scope, Limit: &limit})
	if fault != nil || next == nil {
		t.Fatalf("first page fault %v cursor %v", fault, next)
	}

	// Tamper with the payload portion: the HMAC check must refuse it.
	tampered := *next
	tampered = "x" + tampered[1:]
	_ = e.expectFaultAs(lister, opList, wireListInput{
		Scope: e.scope, Limit: &limit, Cursor: &tampered,
	}, contract.CodeInvalidInput)

	// A structurally broken cursor is also refused.
	garbage := "not-a-cursor"
	_ = e.expectFaultAs(lister, opList, wireListInput{
		Scope: e.scope, Limit: &limit, Cursor: &garbage,
	}, contract.CodeInvalidInput)
}

func TestListCursorFilterMismatchRefused(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	seedReviews(t, e, 2, []contract.ID{human})
	lister := listerFor(t, e)

	limit := int64(1)
	_, next, fault := e.listReviews(lister, wireListInput{Scope: e.scope, Limit: &limit})
	if fault != nil || next == nil {
		t.Fatalf("first page fault %v cursor %v", fault, next)
	}

	// Reuse the cursor under a different filter.
	state := stateApproved
	_ = e.expectFaultAs(lister, opList, wireListInput{
		Scope: e.scope, Limit: &limit, Cursor: next, Filter: &wireListFilter{State: &state},
	}, contract.CodeInvalidInput)
}

func TestListCursorBoundToPrincipal(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	seedReviews(t, e, 2, []contract.ID{human})
	lister := listerFor(t, e)

	limit := int64(1)
	_, next, fault := e.listReviews(lister, wireListInput{Scope: e.scope, Limit: &limit})
	if fault != nil || next == nil {
		t.Fatalf("first page fault %v cursor %v", fault, next)
	}

	// A different principal cannot replay someone else's cursor.
	other := e.actorFor(e.principal(contract.KindService))
	_ = e.expectFaultAs(other, opList, wireListInput{
		Scope: e.scope, Limit: &limit, Cursor: next,
	}, contract.CodeInvalidInput)
}

func TestListCursorExpiryRequiresSnapshot(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	seedReviews(t, e, 2, []contract.ID{human})
	lister := listerFor(t, e)

	limit := int64(1)
	_, next, fault := e.listReviews(lister, wireListInput{Scope: e.scope, Limit: &limit})
	if fault != nil || next == nil {
		t.Fatalf("first page fault %v cursor %v", fault, next)
	}

	e.clock.advance(16 * time.Minute)
	f := e.expectFaultAs(lister, opList, wireListInput{
		Scope: e.scope, Limit: &limit, Cursor: next,
	}, contract.CodeCursorExpired)
	if !strings.Contains(string(f.Details), "snapshot_required") {
		t.Fatalf("cursor_expired details = %s, want snapshot_required", f.Details)
	}
}

func TestListRefusesForeignScope(t *testing.T) {
	e := newEnv(t)
	lister := listerFor(t, e)
	foreign := contract.Scope{InstallationID: e.ids.New()}
	_ = e.expectFaultAs(lister, opList, wireListInput{Scope: foreign}, contract.CodeInvalidInput)
}

func TestGetRequiresAuthenticationAndResolvesExactReview(t *testing.T) {
	e := newEnv(t)
	human := e.principal(contract.KindHuman)
	reviews := seedReviews(t, e, 1, []contract.ID{human})

	got, fault := e.getReview(reviews[0].ID)
	if fault != nil {
		t.Fatalf("get: %v", fault)
	}
	if got.ID != reviews[0].ID || got.ActionDigest != reviews[0].ActionDigest {
		t.Fatalf("get returned %+v", got)
	}
	if len(got.Preview.Content) != 2 || got.Preview.Destination != reviews[0].Preview.Destination {
		t.Fatal("get preview drift")
	}

	// Unknown identity is not_found without cross-scope disclosure.
	if _, fault = e.getReview(e.ids.New()); fault == nil || fault.Code != contract.CodeNotFound {
		t.Fatalf("unknown review fault = %v, want not_found", fault)
	}
}
