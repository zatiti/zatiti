package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Owner seams the real assembly surfaced. The evidence and policy fakes
// enforce what the real owners enforce (one finish, one replacement of an
// accepted pending disposition; exact review only over a sealed candidate
// digest), so each case fails against a dispatcher that leans on a laxer
// owner.

// requireSameDisposition asserts a replay reached the caller in exactly the
// form of the original: the same envelope and the same error.
func requireSameDisposition(t *testing.T, first contract.Result, firstErr error, replay contract.Result, replayErr error) {
	t.Helper()
	want, _ := json.Marshal(first)
	got, _ := json.Marshal(replay)
	if string(got) != string(want) {
		t.Fatalf("replayed envelope %s, want the original %s", got, want)
	}
	if (firstErr == nil) != (replayErr == nil) {
		t.Fatalf("replay error %v, original error %v", replayErr, firstErr)
	}
	if firstErr == nil {
		return
	}
	wantFault, _ := json.Marshal(faultOf(firstErr))
	gotFault, _ := json.Marshal(faultOf(replayErr))
	if string(gotFault) != string(wantFault) {
		t.Fatalf("replayed fault %s, want the original %s", gotFault, wantFault)
	}
}

// requireFailedPairing asserts the one form a failed disposition takes: the
// fault as the error, and, once a durable command retains it, the failed
// envelope naming that command.
func requireFailedPairing(t *testing.T, res contract.Result, err error, code string, cmd *fakeCommand) {
	t.Helper()
	f := requireFault(t, err, code)
	if cmd == nil {
		t.Fatalf("no retained command for the %s refusal", code)
	}
	if res.Schema != contract.SchemaResult || res.Status != contract.StatusFailed || string(res.CommandID) != cmd.ID {
		t.Fatalf("failed envelope %+v, want a failed result naming retained command %s", res, cmd.ID)
	}
	if res.Error == nil || res.Error.Code != f.Code || res.Error.Message != f.Message {
		t.Fatalf("failed envelope carries %+v, want the returned fault %+v", res.Error, f)
	}
}

func TestRefusedCommandReplaysInTheSameForm(t *testing.T) {
	t.Run("handler refusal", func(t *testing.T) {
		e := newTestEnv(t)
		if _, err := e.invoke(t, e.actor(), "business.claim", "seed", map[string]any{"worker": "w1"}); err != nil {
			t.Fatalf("seed claim: %v", err)
		}
		first, firstErr := e.invoke(t, e.actor(), "business.claim", "dup", map[string]any{"worker": "w1"})
		cmd := e.commandGet(t, "business.claim", "dup")
		requireFailedPairing(t, first, firstErr, contract.CodeConflict, cmd)

		replay, replayErr := e.invoke(t, e.actor(), "business.claim", "dup", map[string]any{"worker": "w1"})
		requireSameDisposition(t, first, firstErr, replay, replayErr)

		e2 := reopenEnv(t, e)
		again, againErr := e2.invoke(t, e2.actor(), "business.claim", "dup", map[string]any{"worker": "w1"})
		requireSameDisposition(t, first, firstErr, again, againErr)
	})

	t.Run("policy refusal", func(t *testing.T) {
		e := newTestEnv(t)
		e.policy.setRule(context.Background(), e.db, e.install, "business.apply", "deny", []string{"not allowed here"})
		input := map[string]any{"name": "denied", "expected_version": 0}
		first, firstErr := e.invoke(t, e.actor(), "business.apply", "deny-key", input)
		requireFailedPairing(t, first, firstErr, contract.CodePermissionDenied, e.commandGet(t, "business.apply", "deny-key"))
		replay, replayErr := e.invoke(t, e.actor(), "business.apply", "deny-key", input)
		requireSameDisposition(t, first, firstErr, replay, replayErr)
	})

	t.Run("local IO perform fault", func(t *testing.T) {
		e := newTestEnv(t)
		e.io.failPerform = true
		first, firstErr := e.invoke(t, e.actor(), "artifact.upload.finish", "io-fault", map[string]any{})
		requireFailedPairing(t, first, firstErr, contract.CodeArtifactFault, e.commandGet(t, "artifact.upload.finish", "io-fault"))
		e.io.failPerform = false
		replay, replayErr := e.invoke(t, e.actor(), "artifact.upload.finish", "io-fault", map[string]any{})
		requireSameDisposition(t, first, firstErr, replay, replayErr)
	})

	t.Run("an unretained refusal has no envelope", func(t *testing.T) {
		e := newTestEnv(t)
		e.policy.setRule(context.Background(), e.db, e.install, "business.apply", "review", []string{"needs a human"})
		res, err := e.invoke(t, e.actor(), "business.apply", "review-key", map[string]any{"name": "r", "expected_version": 0})
		_ = requireFault(t, err, contract.CodeReviewRequired)
		if res.CommandID != "" || res.Status != "" {
			t.Fatalf("an unretained refusal returned envelope %+v, want none (no durable command exists)", res)
		}
		if cmd := e.commandGet(t, "business.apply", "review-key"); cmd != nil {
			t.Fatalf("review_required was retained as %+v; an approved review could never be retried under the same key", cmd)
		}
	})

	t.Run("local IO query fault", func(t *testing.T) {
		e := newTestEnv(t)
		e.io.failPerform = true
		res, err := e.invoke(t, e.actor(), "artifact.read", "", map[string]any{})
		f := requireFault(t, err, contract.CodeArtifactFault)
		if res.Status != contract.StatusFailed || res.CommandID == "" || res.Error == nil || res.Error.Code != f.Code {
			t.Fatalf("failed query envelope %+v, want a failed result carrying the fault", res)
		}
	})
}

func TestLocalIOPendingDisposition(t *testing.T) {
	t.Run("completed replay is the identical retained result", func(t *testing.T) {
		e := newTestEnv(t)
		first, err := e.invoke(t, e.actor(), "artifact.upload.finish", "io-done", map[string]any{})
		if err != nil {
			t.Fatalf("local IO mutation: %v", err)
		}
		cmd := e.commandGet(t, "artifact.upload.finish", "io-done")
		if cmd == nil || cmd.Status != contract.StatusCompleted || cmd.ID != string(first.CommandID) {
			t.Fatalf("retained command %+v, want completed %s (the pending disposition was not replaced)", cmd, first.CommandID)
		}
		e2 := reopenEnv(t, e)
		replay, replayErr := e2.invoke(t, e2.actor(), "artifact.upload.finish", "io-done", map[string]any{})
		requireSameDisposition(t, first, nil, replay, replayErr)
		if _, p, _ := e2.io.calls(); p != 0 {
			t.Fatalf("replay ran Perform %d times", p)
		}
	})

	// The Finish transaction never commits (a crash, or a transient fault):
	// the accepted pending disposition is the honest durable record, a
	// same-key retry joins it without a second Perform, and lookup shows it.
	t.Run("interrupted finish leaves the accepted command", func(t *testing.T) {
		e := newTestEnv(t)
		_, basePerform, _ := e.io.calls()
		e.io.finishFault = &contract.Fault{Code: contract.CodeControllerUnavailable, Message: "writer busy", Retryable: true}
		res, err := e.invoke(t, e.actor(), "artifact.upload.finish", "io-crash", map[string]any{})
		_ = requireFault(t, err, contract.CodeControllerUnavailable)
		if res.Status == contract.StatusFailed {
			t.Fatalf("a transient finish fault returned a failed envelope %+v; nothing durable says failed", res)
		}
		cmd := e.commandGet(t, "artifact.upload.finish", "io-crash")
		if cmd == nil || cmd.Status != contract.StatusAccepted {
			t.Fatalf("retained command %+v, want the accepted pending disposition", cmd)
		}

		e2 := reopenEnv(t, e)
		replay, replayErr := e2.invoke(t, e2.actor(), "artifact.upload.finish", "io-crash", map[string]any{})
		if replayErr != nil {
			t.Fatalf("retry over an interrupted command: %v", replayErr)
		}
		if replay.Status != contract.StatusAccepted || string(replay.CommandID) != cmd.ID {
			t.Fatalf("retry envelope %+v, want accepted command %s", replay, cmd.ID)
		}
		if _, p, _ := e2.io.calls(); p != 0 {
			t.Fatalf("retry over an interrupted command ran Perform %d times", p)
		}
		if _, p, _ := e.io.calls(); p != basePerform+1 {
			t.Fatalf("Perform ran %d times over baseline %d, want exactly 1", p, basePerform)
		}
	})

	// Finish deterministically refuses (a stale version, a revocation): the
	// caller saw that refusal, so the pending disposition is replaced by it
	// and an identical retry replays it instead of reporting accepted.
	t.Run("refused finish replaces the pending disposition", func(t *testing.T) {
		e := newTestEnv(t)
		e.io.finishFault = &contract.Fault{Code: contract.CodeStaleVersion, Message: "upload version moved"}
		first, firstErr := e.invoke(t, e.actor(), "artifact.upload.finish", "io-stale", map[string]any{})
		requireFailedPairing(t, first, firstErr, contract.CodeStaleVersion, e.commandGet(t, "artifact.upload.finish", "io-stale"))
		if cmd := e.commandGet(t, "artifact.upload.finish", "io-stale"); cmd.Status != contract.StatusFailed {
			t.Fatalf("retained command status %q after a refused finish, want failed", cmd.Status)
		}
		e.io.finishFault = nil
		_, basePerform, _ := e.io.calls()
		replay, replayErr := e.invoke(t, e.actor(), "artifact.upload.finish", "io-stale", map[string]any{})
		requireSameDisposition(t, first, firstErr, replay, replayErr)
		if _, p, _ := e.io.calls(); p != basePerform {
			t.Fatal("replay of a refused finish ran Perform again")
		}
	})
}

// configuration.apply is an exact-review operation: its input seals the
// candidate digest, and the gate must hand policy that digest or no approved
// review can ever satisfy the requirement.
func TestPolicyGateBindsSealedCandidate(t *testing.T) {
	ctx := context.Background()
	digestA := strings.Repeat("a", 64)
	digestB := strings.Repeat("b", 64)

	setup := func(t *testing.T) *testEnv {
		e := newTestEnv(t)
		schema := json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{` +
			`"plan_id":{"type":"string"},"candidate_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"}},` +
			`"required":["plan_id","candidate_digest"]}`)
		e.cat.ops["configuration.apply"] = fakeOp{
			desc: contract.Descriptor{ID: "configuration.apply", Version: 1, Owner: "configuration",
				Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, SubmissionKey: true, InputSchema: schema},
			h: func(ctx context.Context, u contract.Unit, inv contract.Invocation) (contract.Payload, error) {
				return payloadJSON(map[string]any{"applied": true})
			},
		}
		e.policy.setRule(ctx, e.db, e.install, "configuration.apply", "review",
			[]string{"capability configuration.apply is a default review class and no narrower standing policy governs it"})
		return e
	}
	apply := func(digest string) map[string]any { return map[string]any{"plan_id": "p1", "candidate_digest": digest} }

	t.Run("unreviewed candidate names its exact digest", func(t *testing.T) {
		e := setup(t)
		_, err := e.invoke(t, e.actor(), "configuration.apply", "apply-1", apply(digestA))
		f := requireFault(t, err, contract.CodeReviewRequired)
		var details struct {
			Requirements []fakeRequirement `json:"requirements"`
		}
		if err := json.Unmarshal(f.Details, &details); err != nil {
			t.Fatalf("review details: %v", err)
		}
		if len(details.Requirements) != 1 || details.Requirements[0].ActionDigest != digestA {
			t.Fatalf("review requirement %+v, want the sealed candidate digest %s", details.Requirements, digestA)
		}
	})

	t.Run("approved exact review admits the same key", func(t *testing.T) {
		e := setup(t)
		_, err := e.invoke(t, e.actor(), "configuration.apply", "apply-1", apply(digestA))
		_ = requireFault(t, err, contract.CodeReviewRequired)
		e.policy.decideReview(ctx, e.db, e.install, digestA, "approve")
		res, err := e.invoke(t, e.actor(), "configuration.apply", "apply-1", apply(digestA))
		if err != nil {
			t.Fatalf("apply under an approved exact review: %v", err)
		}
		if res.Status != contract.StatusCompleted {
			t.Fatalf("apply status %q, want completed", res.Status)
		}
	})

	t.Run("an approval does not cover another candidate", func(t *testing.T) {
		e := setup(t)
		e.policy.decideReview(ctx, e.db, e.install, digestA, "approve")
		_, err := e.invoke(t, e.actor(), "configuration.apply", "apply-2", apply(digestB))
		_ = requireFault(t, err, contract.CodeReviewRequired)
	})

	t.Run("rejected exact review denies", func(t *testing.T) {
		e := setup(t)
		e.policy.decideReview(ctx, e.db, e.install, digestA, "reject")
		_, err := e.invoke(t, e.actor(), "configuration.apply", "apply-3", apply(digestA))
		_ = requireFault(t, err, contract.CodePermissionDenied)
	})

	// An operation that seals no candidate gets no digest: a caller cannot
	// borrow an approved digest, and the gate stays closed on review.
	t.Run("operations without a sealed candidate bind nothing", func(t *testing.T) {
		e := setup(t)
		e.policy.setRule(ctx, e.db, e.install, "business.apply", "review", []string{"needs a human"})
		e.policy.decideReview(ctx, e.db, e.install, digestA, "approve")
		_, err := e.invoke(t, e.actor(), "business.apply", "plain", map[string]any{"name": "n", "expected_version": 0})
		_ = requireFault(t, err, contract.CodeReviewRequired)
		for _, seen := range e.policy.seenCandidates() {
			if strings.HasPrefix(seen, "business.apply=") && seen != "business.apply=" {
				t.Fatalf("gate bound %q for an operation that seals no candidate", seen)
			}
		}
	})
}
