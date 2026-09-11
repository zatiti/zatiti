package messaging

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Conversation operations: pinned bootstrap, versioned membership changes,
// listing under the membership fence, and cursor binding.

func TestBootstrapPersonalChief(t *testing.T) {
	env := newEnv(t)
	id := env.bootstrap()
	row := env.readConversation(id)
	if row.Key != bootstrapKey || row.Title != bootstrapTitle || !row.Pinned || row.Kind != kindDirect {
		t.Fatalf("personal-chief conversation drifted: %+v", row)
	}
	if !containsID(mustIDList(row.ParticipantIDsJSON), env.owner) ||
		!containsID(mustIDList(row.ParticipantIDsJSON), env.chief) {
		t.Fatalf("bootstrap participants %s missing owner or chief", row.ParticipantIDsJSON)
	}
	// Idempotent replay returns the same conversation, never a second chief.
	again := env.bootstrap()
	if again != id {
		t.Fatalf("bootstrap replay returned %s, want %s", again, id)
	}
	if row.Version != 1 {
		t.Fatalf("bootstrap replay changed version to %d", row.Version)
	}
}

func TestBootstrapRefusals(t *testing.T) {
	env := newEnv(t)
	// Owner and chief must differ.
	_ = env.expectFault("_messaging.bootstrap", map[string]any{
		"scope": env.scope, "owner_id": env.owner, "chief_id": env.owner,
	}, contract.CodeInvalidInput)
	// Both identities are required.
	_ = env.expectFault("_messaging.bootstrap", map[string]any{
		"scope": env.scope, "owner_id": env.owner, "chief_id": "",
	}, contract.CodeInvalidInput)
	// A replay with different participants is a surfaced conflict.
	env.bootstrap()
	_ = env.expectFault("_messaging.bootstrap", map[string]any{
		"scope": env.scope, "owner_id": env.owner, "chief_id": env.third,
	}, contract.CodeConflict)
}

func TestBootstrapEvent(t *testing.T) {
	env := newEnv(t)
	id := env.bootstrap()
	events := env.events()
	found := false
	for _, ev := range events {
		if ev.Kind == eventConversationBootstrapped && ev.ResourceID == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("bootstrap event missing: %+v", events)
	}
}

func TestConversationCreate(t *testing.T) {
	env := newEnv(t)
	id := env.createGroup(env.owner, env.chief, env.worker)
	row := env.readConversation(id)
	if row.Kind != kindGroup || row.Version != 1 {
		t.Fatalf("group conversation drifted: %+v", row)
	}
	if len(mustIDList(row.ParticipantIDsJSON)) != 3 {
		t.Fatalf("group participants %s, want three", row.ParticipantIDsJSON)
	}
	// Duplicate participants are deduplicated, not rejected.
	dup := env.createGroup(env.owner, env.chief, env.chief)
	if len(mustIDList(env.readConversation(dup).ParticipantIDsJSON)) != 2 {
		t.Fatalf("duplicate participants were not deduplicated")
	}
}

func TestConversationCreateRefusals(t *testing.T) {
	env := newEnv(t)
	cases := []struct {
		name string
		in   map[string]any
		code string
	}{
		{"unknown kind", map[string]any{"scope": env.scope, "kind": "circle",
			"participant_ids": []contract.ID{env.owner, env.chief}, "title": "x"}, contract.CodeInvalidInput},
		{"empty participants", map[string]any{"scope": env.scope, "kind": kindGroup,
			"participant_ids": []contract.ID{}, "title": "x"}, contract.CodeInvalidInput},
		{"direct with three", map[string]any{"scope": env.scope, "kind": kindDirect,
			"participant_ids": []contract.ID{env.owner, env.chief, env.worker}, "title": "x"}, contract.CodeInvalidInput},
	}
	for _, tc := range cases {
		_ = env.expectFault("conversation.create", tc.in, tc.code)
	}
	// The caller must participate in a conversation it creates.
	env.asThird()
	_ = env.expectFault("conversation.create", map[string]any{
		"scope": env.scope, "kind": kindDirect,
		"participant_ids": []contract.ID{env.owner, env.chief}, "title": "x",
	}, contract.CodePermissionDenied)
}

func TestConversationGetFences(t *testing.T) {
	env := newEnv(t)
	id := env.createGroup(env.owner, env.chief)

	// Absent conversation is not_found.
	_ = env.expectFault("conversation.get", map[string]any{
		"scope": env.scope, "id": env.ids.New(),
	}, contract.CodeNotFound)

	// A caller outside the membership is permission_denied: group membership
	// is conversation state, never organization membership.
	env.asThird()
	_ = env.expectFault("conversation.get", map[string]any{
		"scope": env.scope, "id": id,
	}, contract.CodePermissionDenied)

	// A stored organization is not disclosed across an asserted foreign one.
	env.asOwner()
	org := env.createGroup(env.owner, env.chief)
	foreign := env.ids.New()
	env.execSQL(`UPDATE messaging_conversations SET organization_id = ?, scope_json = ?
		WHERE id = ?`, string(foreign),
		string(mustJSON(wireScope{InstallationID: env.install, OrganizationID: foreign})), string(org))
	// The stored organization answers an exact query; a different asserted
	// organization is refused rather than silently ignored.
	_ = env.expectFault("conversation.get", map[string]any{
		"scope": wireScope{InstallationID: env.install, OrganizationID: env.ids.New()}, "id": org,
	}, contract.CodePermissionDenied)
	// The exact stored organization still resolves for a participant.
	env.mustOK("conversation.get", map[string]any{
		"scope": wireScope{InstallationID: env.install, OrganizationID: foreign}, "id": org,
	})
}

func TestConversationUpdate(t *testing.T) {
	env := newEnv(t)
	id := env.createGroup(env.owner, env.chief)
	title := "renamed"
	payload := env.mustOK("conversation.update", map[string]any{
		"scope": env.scope, "id": id, "expected_version": int64(1),
		"title": title,
	})
	var out struct {
		Resource wireConversation `json:"resource"`
	}
	env.decode(payload.Data, &out)
	if out.Resource.Title != title || out.Resource.Version != 2 {
		t.Fatalf("update drifted: %+v", out.Resource)
	}
	// Stale version is refused.
	_ = env.expectFault("conversation.update", map[string]any{
		"scope": env.scope, "id": id, "expected_version": int64(1), "title": "stale",
	}, contract.CodeStaleVersion)
	// A caller that would drop itself from membership is refused.
	env.asChief()
	_ = env.expectFault("conversation.update", map[string]any{
		"scope": env.scope, "id": id, "expected_version": int64(2),
		"participant_ids": []contract.ID{env.owner, env.worker},
	}, contract.CodePermissionDenied)
	// A non-participant caller is refused outright.
	env.asThird()
	_ = env.expectFault("conversation.update", map[string]any{
		"scope": env.scope, "id": id, "expected_version": int64(2), "title": "x",
	}, contract.CodePermissionDenied)
}

func TestConversationUpdateStaysPinned(t *testing.T) {
	env := newEnv(t)
	id := env.bootstrap()
	unpin := false
	_ = env.expectFault("conversation.update", map[string]any{
		"scope": env.scope, "id": id, "expected_version": int64(1), "pinned": unpin,
	}, contract.CodeConflict)
	if !env.readConversation(id).Pinned {
		t.Fatalf("personal-chief conversation was unpinned")
	}
}

func TestConversationUpdateEvent(t *testing.T) {
	env := newEnv(t)
	id := env.createGroup(env.owner, env.chief)
	env.mustOK("conversation.update", map[string]any{
		"scope": env.scope, "id": id, "expected_version": int64(1), "title": "v2",
	})
	found := false
	for _, ev := range env.events() {
		if ev.Kind == eventConversationUpdated && ev.ResourceID == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("updated event missing: %+v", env.events())
	}
}

func TestConversationListMembershipFence(t *testing.T) {
	env := newEnv(t)
	first := env.createGroup(env.owner, env.chief)
	second := env.createGroup(env.owner, env.worker)

	// The owner sees both conversations.
	items, _ := env.listConversationsPage(map[string]any{"scope": env.scope})
	if len(items) != 2 {
		t.Fatalf("owner list %d items, want 2", len(items))
	}
	// The worker sees only its own conversation.
	env.asWorker()
	items, _ = env.listConversationsPage(map[string]any{"scope": env.scope})
	if len(items) != 1 || items[0].ID != second {
		t.Fatalf("worker list drifted: %+v", items)
	}
	// The chief sees only the first.
	env.asChief()
	items, _ = env.listConversationsPage(map[string]any{"scope": env.scope})
	if len(items) != 1 || items[0].ID != first {
		t.Fatalf("chief list drifted: %+v", items)
	}
	// An outsider sees nothing.
	env.asThird()
	items, _ = env.listConversationsPage(map[string]any{"scope": env.scope})
	if len(items) != 0 {
		t.Fatalf("outsider list %d items, want 0", len(items))
	}
}

func TestConversationListFilters(t *testing.T) {
	env := newEnv(t)
	boot := env.bootstrap()
	env.createGroup(env.owner, env.chief)
	payload := env.mustOK("conversation.create", map[string]any{
		"scope": wireScope{InstallationID: env.install, WorkerID: env.worker},
		"kind":  kindDirect, "participant_ids": []contract.ID{env.owner, env.worker},
		"title": "worker chat",
	})
	var out struct {
		Resource wireConversation `json:"resource"`
	}
	env.decode(payload.Data, &out)

	items, _ := env.listConversationsPage(map[string]any{
		"scope": env.scope, "filter": map[string]any{"key": bootstrapKey},
	})
	if len(items) != 1 || items[0].ID != boot {
		t.Fatalf("key filter drifted: %+v", items)
	}
	items, _ = env.listConversationsPage(map[string]any{
		"scope": env.scope, "filter": map[string]any{"worker_id": env.worker},
	})
	if len(items) != 1 || items[0].ID != out.Resource.ID {
		t.Fatalf("worker filter drifted: %+v", items)
	}
	// Unsupported conversation filters refuse instead of being ignored.
	for _, filter := range []map[string]any{
		{"state": "admitted"}, {"parent_id": env.ids.New()},
		{"descendants": true},
	} {
		_ = env.expectFault("conversation.list", map[string]any{
			"scope": env.scope, "filter": filter,
		}, contract.CodeInvalidInput)
	}
}

func TestConversationListNeedsYou(t *testing.T) {
	env := newEnv(t)
	env.asChief()
	conv := env.createGroup(env.owner, env.chief)
	env.asOwner()
	taskID := env.ids.New()
	env.sendToConversation(conv, env.ids.New(), "decide this", nil, []contract.ID{taskID})

	// The chief has an unacknowledged meaningful task-bearing message.
	env.asChief()
	items, _ := env.listConversationsPage(map[string]any{
		"scope": env.scope, "filter": map[string]any{"needs_you": true},
	})
	if len(items) != 1 || items[0].ID != conv {
		t.Fatalf("needs_you drifted: %+v", items)
	}
	// An owner without a decision gathers nothing.
	env.asOwner()
	items, _ = env.listConversationsPage(map[string]any{
		"scope": env.scope, "filter": map[string]any{"needs_you": true},
	})
	if len(items) != 0 {
		t.Fatalf("sender needs_you %d items, want 0", len(items))
	}
	// After the chief acknowledges, the decision leaves needs_you.
	env.asChief()
	ackResource(env, conv, taskID)
	items, _ = env.listConversationsPage(map[string]any{
		"scope": env.scope, "filter": map[string]any{"needs_you": true},
	})
	if len(items) != 0 {
		t.Fatalf("acknowledged decision stays in needs_you: %+v", items)
	}
	items, _ = env.listConversationsPage(map[string]any{"scope": env.scope})
	if len(items) != 1 {
		t.Fatalf("acknowledged chat left the listing: %+v", items)
	}
}

// ackResource acknowledges the one message carrying a task reference into a
// conversation, as its recipient.
func ackResource(env *testEnv, conv, taskID contract.ID) {
	env.t.Helper()
	items, _ := env.listMailboxPage(env.actor.PrincipalID, map[string]any{
		"filter": map[string]any{"task_id": taskID},
	})
	if len(items) != 1 {
		env.t.Fatalf("task mailbox listing %d items, want 1", len(items))
	}
	env.ack(items[0].ID, env.actor.PrincipalID, int64(items[0].Version))
}

func TestConversationListQuietTaskRefs(t *testing.T) {
	env := newEnv(t)
	env.asChief()
	conv := env.createGroup(env.owner, env.chief, env.worker)
	env.asWorker()
	// A quiet worker coordination message carries a task reference but is
	// never meaningful: routine coordination does not reorder chats or
	// surface human decisions.
	env.sendToConversation(conv, env.ids.New(), "progress note", nil, []contract.ID{env.ids.New()})
	if row := env.readConversation(conv); row.LastMeaningfulEvent != "" {
		t.Fatalf("quiet message reordered the chat: %s", row.LastMeaningfulEvent)
	}
	env.asChief()
	items, _ := env.listConversationsPage(map[string]any{
		"scope": env.scope, "filter": map[string]any{"needs_you": true},
	})
	if len(items) != 0 {
		t.Fatalf("quiet task-bearing message entered needs_you: %+v", items)
	}
	// An explicit reporting binding whose destinations name this
	// conversation is what makes a worker report meaningful.
	env.asOwner()
	conv = env.createGroup(env.owner, env.worker)
	env.asWorker()
	env.ports.addReportingBinding("b-1", string(env.worker), string(conv))
	env.sendToConversation(conv, env.ids.New(), "the report", nil, []contract.ID{env.ids.New()})
	if row := env.readConversation(conv); row.LastMeaningfulEvent == "" {
		t.Fatalf("bound report never surfaced in the chat")
	}
}

func TestConversationListCursorBinding(t *testing.T) {
	env := newEnv(t)
	env.createGroup(env.owner, env.chief)
	env.createGroup(env.owner, env.chief)
	env.createGroup(env.owner, env.chief)

	page1, next := env.listConversationsPage(map[string]any{"scope": env.scope, "limit": int64(2)})
	if len(page1) != 2 || next == "" {
		t.Fatalf("first page items %d next %q", len(page1), next)
	}
	// The first page minted the cursor against exactly one evidence snapshot.
	if calls := env.ports.callsOf("_evidence.snapshot"); len(calls) != 1 {
		t.Fatalf("evidence snapshot calls %d, want 1", len(calls))
	}
	page2, next2 := env.listConversationsPage(map[string]any{
		"scope": env.scope, "limit": int64(2), "cursor": next,
	})
	if len(page2) != 1 || next2 != "" {
		t.Fatalf("second page items %d next %q", len(page2), next2)
	}
	if calls := env.ports.callsOf("_evidence.snapshot"); len(calls) != 1 {
		t.Fatalf("paged listing re-fetched the evidence checkpoint")
	}

	// A cursor minted for one query fails against another.
	f := env.expectFault("conversation.list", map[string]any{
		"scope": env.scope, "limit": int64(2), "cursor": next,
		"filter": map[string]any{"key": "wake"},
	}, contract.CodeCursorExpired)
	if !strings.Contains(string(f.Details), "snapshot_required") {
		t.Fatalf("cursor_expired details %s missing snapshot_required", f.Details)
	}
	// A forged signature is expired, never replayed.
	_ = env.expectFault("conversation.list", map[string]any{
		"scope": env.scope, "limit": int64(2), "cursor": `{"offset":2,"sig":"deadbeef"}`,
	}, contract.CodeCursorExpired)
	// A lapsed cursor restarts from a fresh snapshot.
	f = env.expectFault("conversation.list", map[string]any{
		"scope": env.scope, "limit": int64(2), "cursor": expireCursor(env, next),
	}, contract.CodeCursorExpired)
	if !strings.Contains(string(f.Details), "snapshot_required") {
		t.Fatalf("lapsed cursor details %s missing snapshot_required", f.Details)
	}
}

// expireCursor re-mints the given cursor payload with a lapsed expiry under
// the exact signature, simulating a stored cursor crossing its TTL.
func expireCursor(env *testEnv, cursor string) string {
	env.t.Helper()
	var out string
	err := env.db.Write(env.ctx, env.actor, env.scope.toContract(), func(unit contract.Unit) error {
		var p cursorPayload
		if err := decodeJSON("cursor", cursor, &p); err != nil {
			return err
		}
		p.Expires = env.clock.Now().Add(-cursorTTL).Format(timeLayout)
		p.Sig = env.svc.cursorSignature(unit,
			cursorFingerprint("conversation.list", "", "", "", "", boolText(false)),
			p.Snapshot, p.Expires)
		raw, err := json.Marshal(p)
		if err != nil {
			return err
		}
		out = string(raw)
		return nil
	})
	if err != nil {
		env.t.Fatalf("re-sign cursor: %v", err)
	}
	return out
}
