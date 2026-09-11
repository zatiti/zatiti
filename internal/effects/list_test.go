package effects

import (
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Lists cover the keyset pagination, cursor integrity and the pending view
// the controller polls.

func TestListPagesByKeyset(t *testing.T) {
	env := newEnv(t)
	first := env.staged()
	second := env.staged()
	third := env.staged()

	limit := int64(2)
	items, cursor := env.listOps(env.scope, nil, &limit, nil)
	if len(items) != 2 || cursor == nil {
		t.Fatalf("first page: %d items cursor %v, want 2 items and a cursor", len(items), cursor)
	}
	if items[0].ID != first.ID || items[1].ID != second.ID {
		t.Fatalf("first page %v, want [%s %s]", ids(items), first.ID, second.ID)
	}
	items, cursor = env.listOps(env.scope, cursor, &limit, nil)
	if len(items) != 1 || cursor != nil {
		t.Fatalf("second page: %d items cursor %v, want 1 item and no cursor", len(items), cursor)
	}
	if items[0].ID != third.ID {
		t.Fatalf("second page item %s, want %s", items[0].ID, third.ID)
	}
}

func TestListFiltersByState(t *testing.T) {
	env := newEnv(t)
	env.staged()
	o := env.staged()
	env.admitOp(o.ID, o.Version)

	ready := opStateReady
	items, _ := env.listOps(env.scope, nil, nil, &operationFilter{State: &ready})
	if len(items) != 1 || items[0].ID != o.ID {
		t.Fatalf("ready filter returned %v, want only %s", ids(items), o.ID)
	}
	prepared := opStatePrepared
	items, _ = env.listOps(env.scope, nil, nil, &operationFilter{State: &prepared})
	if len(items) != 1 {
		t.Fatalf("prepared filter returned %d items, want 1", len(items))
	}
}

func TestListFiltersBySourceKey(t *testing.T) {
	env := newEnv(t)
	source := env.ids.New()
	env.prepareOp(env.scope, env.action(), source)
	env.staged()

	key := string(source)
	items, _ := env.listOps(env.scope, nil, nil, &operationFilter{Key: &key})
	if len(items) != 1 {
		t.Fatalf("key filter returned %d items, want 1", len(items))
	}
}

func TestListFiltersByLinkedParent(t *testing.T) {
	env := newEnv(t)
	o, d := env.dispatched()
	o = env.recordOp(o.ID, d.AttemptID, d.Generation, env.observation(dispSucceeded))
	compensation := env.linkedProposeOp(opCompensationPropose, env.scope, o.ID, o.Version, env.action())

	parent := o.ID
	items, _ := env.listOps(env.scope, nil, nil, &operationFilter{ParentID: &parent})
	if len(items) != 1 || items[0].ID != compensation.ID {
		t.Fatalf("parent filter returned %v, want the compensation", ids(items))
	}
}

func TestListRejectsForeignCursor(t *testing.T) {
	env := newEnv(t)
	env.staged()
	env.staged()
	limit := int64(1)
	_, cursor := env.listOps(env.scope, nil, &limit, nil)

	other := newEnv(t)
	other.staged()
	_ = other.expectFault(opList, listOperationsInput{
		Scope: unitScope(other.scope), Cursor: cursor, Limit: &limit,
	}, contract.CodeInvalidInput)
}

func TestListRejectsFilterMismatchedCursor(t *testing.T) {
	env := newEnv(t)
	env.staged()
	env.staged()
	limit := int64(1)
	prepared := opStatePrepared
	_, cursor := env.listOps(env.scope, nil, &limit, &operationFilter{State: &prepared})
	ready := opStateReady
	_ = env.expectFault(opList, listOperationsInput{
		Scope: unitScope(env.scope), Cursor: cursor, Limit: &limit,
		Filter: &operationFilter{State: &ready},
	}, contract.CodeInvalidInput)
}

func TestListRejectsMalformedCursor(t *testing.T) {
	env := newEnv(t)
	env.staged()
	garbage := "not-a-cursor"
	_ = env.expectFault(opList, listOperationsInput{
		Scope: unitScope(env.scope), Cursor: &garbage,
	}, contract.CodeInvalidInput)
}

func TestListCursorExpires(t *testing.T) {
	env := newEnv(t)
	env.staged()
	env.staged()
	limit := int64(1)
	_, cursor := env.listOps(env.scope, nil, &limit, nil)
	env.clock.Advance(cursorTTL + time.Minute)
	_ = env.expectFault(opList, listOperationsInput{
		Scope: unitScope(env.scope), Cursor: cursor, Limit: &limit,
	}, contract.CodeCursorExpired)
}

func TestListCursorSurvivesClockPrecision(t *testing.T) {
	// A cursor minted and consumed within the same timestamp window must not
	// skip rows sharing a created_at stamp: the keyset tiebreaks on id.
	env := newEnv(t)
	for i := 0; i < 3; i++ {
		env.staged()
	}
	limit := int64(1)
	var all []wireOperation
	var cursor *string
	for {
		items, next := env.listOps(env.scope, cursor, &limit, nil)
		all = append(all, items...)
		if next == nil {
			break
		}
		cursor = next
	}
	if len(all) != 3 {
		t.Fatalf("walked %d items over pages, want 3", len(all))
	}
	seen := map[contract.ID]bool{}
	for _, o := range all {
		if seen[o.ID] {
			t.Fatalf("pagination revisited %s", o.ID)
		}
		seen[o.ID] = true
	}
}

func TestPendingListsActionableOperations(t *testing.T) {
	env := newEnv(t)
	staged := env.staged()

	accepted := env.staged()
	accepted = env.admitOp(accepted.ID, accepted.Version)
	d := env.claimOp(accepted.ID, accepted.AttemptIDs[0], env.generation())
	accepted = env.recordOp(accepted.ID, d.AttemptID, d.Generation, env.observation(dispAccepted))

	unknown := env.staged()
	unknown = env.admitOp(unknown.ID, unknown.Version)
	d2 := env.claimOp(unknown.ID, unknown.AttemptIDs[0], env.generation())
	unknown = env.recordOp(unknown.ID, d2.AttemptID, d2.Generation, env.observation(dispUnknown))

	// A concluded operation is not actionable.
	done := env.staged()
	done = env.admitOp(done.ID, done.Version)
	d3 := env.claimOp(done.ID, done.AttemptIDs[0], env.generation())
	env.recordOp(done.ID, d3.AttemptID, d3.Generation, env.observation(dispSucceeded))

	pending := env.pendingOp(100)
	if len(pending) != 3 {
		t.Fatalf("pending listed %d operations, want 3: %v", len(pending), ids(pending))
	}
	wantStates := []string{opStatePrepared, opStateAwaitingConfirmation, opStateOutcomeUnknown}
	for i, o := range pending {
		if o.State != wantStates[i] {
			t.Fatalf("pending[%d] is %s (%s), want %s", i, o.ID, o.State, wantStates[i])
		}
	}
	if pending[0].ID != staged.ID {
		t.Fatalf("pending[0] is %s, want the earliest submission %s", pending[0].ID, staged.ID)
	}
}

func ids(ops []wireOperation) []contract.ID {
	out := make([]contract.ID, 0, len(ops))
	for _, o := range ops {
		out = append(out, o.ID)
	}
	return out
}
