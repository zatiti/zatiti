package messaging

import (
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Internal peer operations: admission fences and the pending inbox.

func TestAdmitWireFences(t *testing.T) {
	env := newEnv(t)
	env.asWorker()

	// A worker coordination message with no conversation placement is quiet
	// coordination: it delivers admitted without reordering any chat.
	quiet := env.admit(env.admitInput(env.ids.New(), env.worker, []contract.ID{env.chief}, "coord", ""))
	if quiet.State != messageStateAdmitted {
		t.Fatalf("admitted quiet message state %q, want admitted", quiet.State)
	}

	// The wire document is untrusted: a committed state is refused,
	// admission mints its own transition.
	bad := env.admitInput(env.ids.New(), env.worker, []contract.ID{env.chief}, "x", "")
	bad.State = messageStateAdmitted
	_ = env.expectFault("_messaging.admit", map[string]any{"message": bad}, contract.CodeInvalidInput)

	// Version must be 1: admission is the first transition.
	bad = env.admitInput(env.ids.New(), env.worker, []contract.ID{env.chief}, "x", "")
	bad.Version = 2
	_ = env.expectFault("_messaging.admit", map[string]any{"message": bad}, contract.CodeInvalidInput)

	// Identity is required on both ends.
	bad = env.admitInput("", env.worker, []contract.ID{env.chief}, "x", "")
	_ = env.expectFault("_messaging.admit", map[string]any{"message": bad}, contract.CodeInvalidInput)
	bad = env.admitInput(env.ids.New(), "", []contract.ID{env.chief}, "x", "")
	_ = env.expectFault("_messaging.admit", map[string]any{"message": bad}, contract.CodeInvalidInput)
}

func TestAdmitWorkerAsSelfFence(t *testing.T) {
	env := newEnv(t)
	env.asWorker()

	// A worker-kind caller can only admit mail as itself: claiming another
	// sender is permission_denied, never a delivered row.
	msg := env.admitInput(env.ids.New(), env.chief, []contract.ID{env.worker}, "impersonation", "")
	_ = env.expectFault("_messaging.admit", map[string]any{"message": msg}, contract.CodePermissionDenied)

	// Admitting as itself delivers.
	delivered := env.admit(env.admitInput(env.ids.New(), env.worker, []contract.ID{env.chief}, "self", ""))
	if delivered.SenderID != env.worker {
		t.Fatalf("admitted sender %q, want the calling worker", delivered.SenderID)
	}
}

func TestAdmitDedup(t *testing.T) {
	env := newEnv(t)
	env.asWorker()
	taskID := env.ids.New()
	msg := env.admitInput(env.ids.New(), env.worker, []contract.ID{env.chief}, "report", "")
	msg.TaskIDs = []contract.ID{taskID}

	// Redelivery of identical content is idempotent: same id, same version,
	// never a second inbox row.
	first := env.admit(msg)
	second := env.admit(msg)
	if first.ID != second.ID || first.Version != second.Version {
		t.Fatalf("redelivery changed identity %s/%d -> %s/%d",
			first.ID, first.Version, second.ID, second.Version)
	}

	// Redelivery with changed content is a surfaced conflict.
	msg.Body = "changed"
	_ = env.expectFault("_messaging.admit", map[string]any{"message": msg}, contract.CodeSubmissionConflict)
}

func TestPendingWorkerFence(t *testing.T) {
	env := newEnv(t)
	env.asWorker()

	// worker_id and limit are required wire fields.
	_ = env.expectFault("_messaging.pending", map[string]any{}, contract.CodeInvalidInput)

	// A worker-kind caller reads only its own inbox: claiming another
	// worker's pending mail is permission_denied.
	_ = env.expectFault("_messaging.pending", map[string]any{
		"worker_id": env.chief, "limit": int64(50),
	}, contract.CodePermissionDenied)
}

func TestPendingOldestFirst(t *testing.T) {
	env := newEnv(t)
	env.asOwner()
	first := env.admit(env.admitInput(env.ids.New(), env.owner, []contract.ID{env.worker}, "one", "")).ID
	env.clock.advance(1 * time.Second)
	second := env.admit(env.admitInput(env.ids.New(), env.owner, []contract.ID{env.worker}, "two", "")).ID
	env.clock.advance(1 * time.Second)
	env.admit(env.admitInput(env.ids.New(), env.owner, []contract.ID{env.worker}, "three", ""))

	// The pending inbox reads oldest-first for safe-boundary injection:
	// a limited page carries the earliest rows.
	items := env.pendingItems(env.worker, 2)
	if len(items) != 2 || items[0].ID != first || items[1].ID != second {
		t.Fatalf("pending order %s,%s want %s,%s", items[0].ID, items[1].ID, first, second)
	}

	// Acknowledged mail leaves the pending set.
	env.asWorker()
	env.ack(first, env.worker, 1)
	items = env.pendingItems(env.worker, 10)
	if len(items) != 2 || items[0].ID != second {
		t.Fatalf("acknowledged mail stayed pending: %+v", items)
	}
}

func TestPendingLimitCap(t *testing.T) {
	env := newEnv(t)
	env.asOwner()
	// 101 admitted rows bound one worker's pending set.
	for i := 0; i < 101; i++ {
		env.admit(env.admitInput(env.ids.New(), env.owner, []contract.ID{env.worker}, "row", ""))
	}
	// The wire contract caps a page at 100: the schema maximum reads the
	// earliest 100 rows, never the whole set.
	items := env.pendingItems(env.worker, 100)
	if len(items) != 100 {
		t.Fatalf("pending rows %d, want the capped 100", len(items))
	}
	// A limit above the cap is refused at the wire, never silently widened.
	_ = env.expectFault("_messaging.pending", map[string]any{
		"worker_id": env.worker, "limit": int64(500),
	}, contract.CodeInvalidInput)
}
