package messaging

import (
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

func TestMessagingHistoryIsChronologicalAndMembershipBound(t *testing.T) {
	env := newEnv(t)
	env.asOwner()
	conversation := env.createGroup(env.owner, env.chief)
	before := env.sendToConversation(conversation, env.ids.New(), "before worker joined", nil, nil).ID
	if _, err := env.call("conversation.update", map[string]any{
		"scope": env.scope, "id": conversation, "expected_version": int64(1),
		"participant_ids": []contract.ID{env.owner, env.chief, env.worker},
	}); err != nil {
		t.Fatalf("add worker participant: %v", err)
	}
	env.clock.advance(time.Second)
	after1 := env.sendToConversation(conversation, env.ids.New(), "first visible", nil, nil).ID
	env.clock.advance(time.Second)
	after2 := env.sendToConversation(conversation, env.ids.New(), "second visible", nil, nil).ID

	scope := env.scope
	scope.WorkerID = env.worker
	payload := env.mustOK("_messaging.history", map[string]any{
		"scope": scope, "conversation_id": conversation, "worker_id": env.worker, "limit": int64(200),
	})
	var out struct {
		Items    []*wireMessage `json:"items"`
		Complete bool           `json:"complete"`
	}
	env.decode(payload.Data, &out)
	if !out.Complete || len(out.Items) != 2 {
		t.Fatalf("history returned complete=%v count=%d, want complete two-row visible interval", out.Complete, len(out.Items))
	}
	if out.Items[0].ID != after1 || out.Items[1].ID != after2 {
		t.Fatalf("history order/contents %s,%s; want %s,%s (pre-join %s must stay hidden)", out.Items[0].ID, out.Items[1].ID, after1, after2, before)
	}
}

func TestMessagingHistorySignalsIncompleteAtBound(t *testing.T) {
	env := newEnv(t)
	env.asOwner()
	conversation := env.createGroup(env.owner, env.worker)
	for i := 0; i < 201; i++ {
		env.sendToConversation(conversation, env.ids.New(), "history row", nil, nil)
	}
	scope := env.scope
	scope.WorkerID = env.worker
	payload := env.mustOK("_messaging.history", map[string]any{
		"scope": scope, "conversation_id": conversation, "worker_id": env.worker, "limit": int64(200),
	})
	var out struct {
		Items    []*wireMessage `json:"items"`
		Complete bool           `json:"complete"`
	}
	env.decode(payload.Data, &out)
	if out.Complete || len(out.Items) != 200 {
		t.Fatalf("bounded history complete=%v count=%d, want incomplete and exactly 200 rows", out.Complete, len(out.Items))
	}
}
