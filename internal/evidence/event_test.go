package evidence

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Authorized event reads, scope/expiry handling and secret redaction — the
// remaining local proving focus items.

func TestEventGetReturnsRedactedEvent(t *testing.T) {
	env := newEnv(t)
	resourceID := env.ids.New()
	data := json.RawMessage(`{"token":"super-secret-value","note":"keep me"}`)
	emitted := env.emitEventWithData(env.scope, "widget.thing.created", resourceID, data)

	got, err := env.getEvent(env.scope, emitted.ID)
	if err != nil {
		t.Fatalf("event.get: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(got.Data, &body); err != nil {
		t.Fatalf("decode redacted data: %v", err)
	}
	if body["token"] != redactedPlaceholder {
		t.Fatalf("token = %v, want redacted", body["token"])
	}
	if body["note"] != "keep me" {
		t.Fatalf("note = %v, want preserved", body["note"])
	}
}

func TestEventGetUnknownIsNotFound(t *testing.T) {
	env := newEnv(t)
	_ = env.expectFault(opEventGet, eventGetInput{Scope: env.scope, ID: env.ids.New()}, contract.CodeNotFound)
}

// No private events through cursor position / scope isolation: an event
// stamped with a narrower scope than the caller requests is not visible, and
// a caller who does not narrow at all sees every event of the installation.
func TestEventGetScopeIsolation(t *testing.T) {
	env := newEnv(t)
	org := env.ids.New()
	otherOrg := env.ids.New()
	resourceID := env.ids.New()
	scoped := env.scope
	scoped.OrganizationID = org
	emitted := env.emitEvent(scoped, "widget.thing.created", resourceID)

	if _, err := env.getEvent(scoped, emitted.ID); err != nil {
		t.Fatalf("same-org get failed: %v", err)
	}
	if _, err := env.getEvent(env.scope, emitted.ID); err != nil {
		t.Fatalf("installation-only get failed: %v", err)
	}
	foreign := env.scope
	foreign.OrganizationID = otherOrg
	if _, err := env.getEvent(foreign, emitted.ID); err == nil {
		t.Fatal("foreign-org get succeeded, want not_found")
	} else if f := faultFrom(err); f == nil || f.Code != contract.CodeNotFound {
		t.Fatalf("foreign-org get error %v, want not_found", err)
	}
}

func TestEventGetRejectsForeignInstallationScope(t *testing.T) {
	env := newEnv(t)
	foreign := contract.Scope{InstallationID: env.ids.New()}
	_ = env.expectFault(opEventGet, eventGetInput{Scope: foreign, ID: env.ids.New()}, contract.CodePermissionDenied)
}

func TestEventListPaginatesInSequenceOrder(t *testing.T) {
	env := newEnv(t)
	var ids []contract.ID
	for i := 0; i < 5; i++ {
		id := env.ids.New()
		env.emitEvent(env.scope, "widget.thing.created", id)
		ids = append(ids, id)
	}

	limit := int64(2)
	var collected []contract.Event
	var cursor *string
	for i := 0; i < 10; i++ {
		items, next := env.listEvents(env.scope, cursor, &limit, nil)
		collected = append(collected, items...)
		if next == nil {
			break
		}
		cursor = next
	}
	if len(collected) != len(ids) {
		t.Fatalf("collected %d events across pages, want %d", len(collected), len(ids))
	}
	for i, ev := range collected {
		if ev.ResourceID != ids[i] {
			t.Fatalf("event %d resource id %s, want %s (out of order)", i, ev.ResourceID, ids[i])
		}
		if i > 0 && ev.Sequence <= collected[i-1].Sequence {
			t.Fatalf("event %d sequence %d did not increase from %d", i, ev.Sequence, collected[i-1].Sequence)
		}
	}
}

func TestEventListFiltersByWorker(t *testing.T) {
	env := newEnv(t)
	workerA := env.ids.New()
	workerB := env.ids.New()
	scopeA := env.scope
	scopeA.WorkerID = workerA
	scopeB := env.scope
	scopeB.WorkerID = workerB

	idA := env.ids.New()
	idB := env.ids.New()
	env.emitEvent(scopeA, "worker.task.assigned", idA)
	env.emitEvent(scopeB, "worker.task.assigned", idB)

	items, _ := env.listEvents(env.scope, nil, nil, &eventFilterInput{WorkerID: &workerA})
	if len(items) != 1 || items[0].ResourceID != idA {
		t.Fatalf("worker filter returned %+v, want exactly the workerA event", items)
	}
}

func TestEventListRejectsUnsupportedFilterFields(t *testing.T) {
	env := newEnv(t)
	state := "anything"
	_ = env.expectFault(opEventList, eventListInput{
		Scope: env.scope, Filter: &eventFilterInput{State: &state},
	}, contract.CodeInvalidInput)

	yes := true
	_ = env.expectFault(opEventList, eventListInput{
		Scope: env.scope, Filter: &eventFilterInput{Descendants: &yes},
	}, contract.CodeInvalidInput)
}

func TestEventListRejectsBadLimit(t *testing.T) {
	env := newEnv(t)
	zero := int64(0)
	over := int64(501)
	_ = env.expectFault(opEventList, eventListInput{Scope: env.scope, Limit: &zero}, contract.CodeInvalidInput)
	_ = env.expectFault(opEventList, eventListInput{Scope: env.scope, Limit: &over}, contract.CodeInvalidInput)
}

func TestEventListRejectsForeignInstallationScope(t *testing.T) {
	env := newEnv(t)
	foreign := contract.Scope{InstallationID: env.ids.New()}
	_ = env.expectFault(opEventList, eventListInput{Scope: foreign}, contract.CodePermissionDenied)
}

// Event replay cannot silently omit a gap: an expired cursor is a named
// cursor_expired fault carrying snapshot_required, never a silently
// truncated page.
func TestEventListCursorExpiresAndDemandsSnapshot(t *testing.T) {
	env := newEnv(t)
	limit := int64(1)
	env.emitEvent(env.scope, "widget.thing.created", env.ids.New())
	env.emitEvent(env.scope, "widget.thing.created", env.ids.New())
	_, next := env.listEvents(env.scope, nil, &limit, nil)
	if next == nil {
		t.Fatal("expected a next cursor after a truncated first page")
	}

	env.clock.Advance(eventCursorTTL + time.Minute)
	f := env.expectFault(opEventList, eventListInput{Scope: env.scope, Cursor: next, Limit: &limit}, contract.CodeCursorExpired)
	var details struct {
		SnapshotRequired bool `json:"snapshot_required"`
	}
	if err := json.Unmarshal(f.Details, &details); err != nil {
		t.Fatalf("decode fault details: %v", err)
	}
	if !details.SnapshotRequired {
		t.Fatal("cursor_expired fault did not carry snapshot_required=true")
	}
}

func TestEventListCursorTamperedIsRejected(t *testing.T) {
	env := newEnv(t)
	limit := int64(1)
	env.emitEvent(env.scope, "widget.thing.created", env.ids.New())
	env.emitEvent(env.scope, "widget.thing.created", env.ids.New())
	_, next := env.listEvents(env.scope, nil, &limit, nil)
	if next == nil {
		t.Fatal("expected a next cursor")
	}
	tampered := *next + "x"
	_ = env.expectFault(opEventList, eventListInput{Scope: env.scope, Cursor: &tampered, Limit: &limit}, contract.CodeInvalidInput)
}

func TestEventListCursorForeignScopeIsRejected(t *testing.T) {
	env := newEnv(t)
	limit := int64(1)
	env.emitEvent(env.scope, "widget.thing.created", env.ids.New())
	env.emitEvent(env.scope, "widget.thing.created", env.ids.New())
	_, next := env.listEvents(env.scope, nil, &limit, nil)
	if next == nil {
		t.Fatal("expected a next cursor")
	}
	otherOrg := env.scope
	otherOrg.OrganizationID = env.ids.New()
	_ = env.expectFault(opEventList, eventListInput{Scope: otherOrg, Cursor: next, Limit: &limit}, contract.CodeInvalidInput)
}

func TestSnapshotReturnsCurrentCheckpoint(t *testing.T) {
	env := newEnv(t)
	zero := env.mustOK(opSnapshot, snapshotInput{Scope: env.scope})
	var out snapshotOutput
	env.decode(zero.Data, &out)
	if out.LastSequence != 0 {
		t.Fatalf("initial checkpoint %d, want 0", out.LastSequence)
	}

	last := env.emitEvent(env.scope, "widget.thing.created", env.ids.New())
	after := env.mustOK(opSnapshot, snapshotInput{Scope: env.scope})
	env.decode(after.Data, &out)
	if out.LastSequence != last.Sequence {
		t.Fatalf("checkpoint %d, want %d", out.LastSequence, last.Sequence)
	}
}

func TestSnapshotIsScopedByOrganization(t *testing.T) {
	env := newEnv(t)
	org := env.ids.New()
	scoped := env.scope
	scoped.OrganizationID = org
	env.emitEvent(scoped, "widget.thing.created", env.ids.New())

	payload := env.mustOK(opSnapshot, snapshotInput{Scope: scoped})
	var out snapshotOutput
	env.decode(payload.Data, &out)
	if out.LastSequence == 0 {
		t.Fatal("org-scoped checkpoint is 0 after an event in that org")
	}

	otherOrg := env.scope
	otherOrg.OrganizationID = env.ids.New()
	payload = env.mustOK(opSnapshot, snapshotInput{Scope: otherOrg})
	env.decode(payload.Data, &out)
	if out.LastSequence != 0 {
		t.Fatalf("foreign-org checkpoint %d, want 0", out.LastSequence)
	}
}

func TestSnapshotRejectsForeignInstallationScope(t *testing.T) {
	env := newEnv(t)
	foreign := contract.Scope{InstallationID: env.ids.New()}
	_ = env.expectFault(opSnapshot, snapshotInput{Scope: foreign}, contract.CodePermissionDenied)
}
