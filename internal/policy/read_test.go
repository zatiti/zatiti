package policy

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Behavioral tests for read-side operations: keyset pagination with
// authenticated cursors, cursor binding and expiry fences, list filter
// validation and request scoping of gets.

// listPage runs one list op and returns the raw items plus the next cursor.
func (e *testEnv) listPage(op string, in listInput) ([]json.RawMessage, *string) {
	e.t.Helper()
	payload := e.mustOK(op, in)
	var body struct {
		Items []json.RawMessage `json:"items"`
	}
	e.decode(payload.Data, &body)
	return body.Items, payload.NextCursor
}

// policyIDsOf decodes listed policy items into their ids.
func (e *testEnv) policyIDsOf(items []json.RawMessage) []contract.ID {
	e.t.Helper()
	out := make([]contract.ID, 0, len(items))
	for _, raw := range items {
		var p wirePolicy
		e.decode(raw, &p)
		out = append(out, p.ID)
	}
	return out
}

func idOf(id contract.ID) *contract.ID { return &id }
func strOf(s string) *string           { return &s }
func limitOf(n int64) *int64           { return &n }

func TestListPagination(t *testing.T) {
	e := newEnv(t)

	want := map[contract.ID]bool{}
	for i := 0; i < 3; i++ {
		row := e.createPolicy(e.scope)
		want[row.ID] = true
	}

	page1, next := e.listPage(opPolicyList, listInput{Scope: e.scope, Limit: limitOf(2)})
	if len(page1) != 2 || next == nil || *next == "" {
		t.Fatalf("first page: %d items next %v", len(page1), next)
	}
	first := e.policyIDsOf(page1)
	if !want[first[0]] || !want[first[1]] || first[0] == first[1] {
		t.Fatalf("first page ids: %v", first)
	}

	page2, next2 := e.listPage(opPolicyList, listInput{Scope: e.scope, Limit: limitOf(2), Cursor: next})
	if len(page2) != 1 || next2 != nil {
		t.Fatalf("second page: %d items next %v", len(page2), next2)
	}
	second := e.policyIDsOf(page2)
	if !want[second[0]] {
		t.Fatalf("second page id %s outside created set", second[0])
	}
	if second[0] == first[0] || second[0] == first[1] {
		t.Fatalf("second page repeats first page: %v then %v", first, second)
	}
}

func TestCursorFences(t *testing.T) {
	e := newEnv(t)

	// Policies homed in one organization so a filtered page yields rows.
	orgA := e.ids.New()
	scopeA := e.scope
	scopeA.OrganizationID = orgA
	for i := 0; i < 2; i++ {
		e.createPolicy(scopeA)
	}

	filtered := listFilter{OrganizationID: idOf(orgA)}
	_, raw := e.listPage(opPolicyList, listInput{
		Scope: e.scope, Limit: limitOf(1), Filter: &filtered,
	})
	if raw == nil || *raw == "" {
		t.Fatalf("filtered page produced no cursor")
	}
	cursor := *raw

	// A structurally broken cursor is malformed.
	f := e.expectFault(opPolicyList, listInput{
		Scope: e.scope, Limit: limitOf(1), Cursor: strOf("garbage"),
	}, contract.CodeInvalidInput)
	if !contains(f.Message, "cursor is malformed") {
		t.Fatalf("garbage cursor: %s", f.Message)
	}

	// A flipped payload byte fails the MAC check.
	parts := cursor[:len(cursor)-1] + "A"
	if parts == cursor {
		parts = cursor[:len(cursor)-2] + "AA"
	}
	f = e.expectFault(opPolicyList, listInput{
		Scope: e.scope, Limit: limitOf(1), Cursor: strOf(parts),
	}, contract.CodeInvalidInput)
	if !contains(f.Message, "cursor is malformed") {
		t.Fatalf("tampered cursor: %s", f.Message)
	}

	// A cursor minted by one query is refused by another.
	f = e.expectFault(opRuleList, listInput{
		Scope: e.scope, Limit: limitOf(1), Cursor: strOf(cursor),
	}, contract.CodeInvalidInput)
	if !contains(f.Message, "cursor does not belong to this query") {
		t.Fatalf("cross-op cursor: %s", f.Message)
	}

	// A cursor is bound to the filter it was minted for.
	f = e.expectFault(opPolicyList, listInput{
		Scope: e.scope, Limit: limitOf(1), Cursor: strOf(cursor),
	}, contract.CodeInvalidInput)
	if !contains(f.Message, "cursor does not match the current filter") {
		t.Fatalf("filter-mismatch cursor: %s", f.Message)
	}

	// An expired cursor asks for a fresh snapshot.
	e.clock.Advance(16 * time.Minute)
	f = e.expectFault(opPolicyList, listInput{
		Scope: e.scope, Limit: limitOf(1), Filter: &filtered, Cursor: strOf(cursor),
	}, contract.CodeCursorExpired)
	if !contains(string(f.Details), "snapshot_required") {
		t.Fatalf("expired cursor details: %s", f.Details)
	}

	// A qualification state filter must name a known state.
	f = e.expectFault(opAutonomyQualList, listInput{
		Scope:  e.scope,
		Filter: &listFilter{State: strOf("vanished")},
	}, contract.CodeInvalidInput)
	if !contains(f.Message, `qualification state filter "vanished" is not a known state`) {
		t.Fatalf("unknown state filter: %s", f.Message)
	}

	// policy.list refuses every filter other than organization_id.
	f = e.expectFault(opPolicyList, listInput{
		Scope:  e.scope,
		Filter: &listFilter{WorkerID: idOf(e.ids.New())},
	}, contract.CodeInvalidInput)
	if !contains(f.Message, "supports only the organization_id filter") {
		t.Fatalf("unsupported filter: %s", f.Message)
	}
}

func TestGetScoping(t *testing.T) {
	e := newEnv(t)

	orgA := e.ids.New()
	orgB := e.ids.New()
	scopeA := e.scope
	scopeA.OrganizationID = orgA
	scopeB := e.scope
	scopeB.OrganizationID = orgB

	// A policy homed in organization B is invisible to organization A.
	policyB := e.createPolicy(scopeB)
	f := e.expectFault(opPolicyGet, policyGetInput{Scope: scopeA, ID: policyB.ID}, contract.CodePermissionDenied)
	if !contains(f.Message, "is outside the request scope") {
		t.Fatalf("policy scoping: %s", f.Message)
	}
	f = e.expectFault(opPolicyGet, policyGetInput{Scope: scopeA, ID: e.ids.New()}, contract.CodeNotFound)
	if !contains(f.Message, "is unknown in this installation") {
		t.Fatalf("unknown policy: %s", f.Message)
	}
	if got := e.getPolicy(scopeB, policyB.ID); got.ID != policyB.ID {
		t.Fatalf("org B read: %+v", got)
	}

	// The same fence holds for promotion rules.
	ruleB := e.createRule(scopeB, "deploy.render", e.ceiling)
	f = e.expectFault(opRuleGet, ruleGetInput{Scope: scopeA, ID: ruleB.ID}, contract.CodePermissionDenied)
	if !contains(f.Message, "is outside the request scope") {
		t.Fatalf("rule scoping: %s", f.Message)
	}

	// And for qualifications.
	w := e.ids.New()
	e.bindWorker(w, "model-w")
	payload := e.mustOKAs(e.actor, scopeB, opAutonomyPropose, autonomyProposeInput{
		Scope: scopeB, WorkerID: w, Rule: ruleRef(ruleB), EvidenceIDs: []contract.ID{},
	})
	var body qualificationBody
	e.decode(payload.Data, &body)
	f = e.expectFault(opAutonomyQualGet, qualificationGetInput{Scope: scopeA, ID: body.Resource.ID},
		contract.CodePermissionDenied)
	if !contains(f.Message, "is outside the request scope") {
		t.Fatalf("qualification scoping: %s", f.Message)
	}
	if got := e.getQualification(scopeB, body.Resource.ID); got.ID != body.Resource.ID {
		t.Fatalf("org B qualification read: %+v", got)
	}
}
