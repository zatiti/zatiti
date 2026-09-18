package controller

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func wantFault(t *testing.T, err error, code string) {
	t.Helper()
	var f *contract.Fault
	if !errors.As(err, &f) || f == nil {
		t.Fatalf("expected %s fault, got %v", code, err)
	}
	if f.Code != code {
		t.Fatalf("expected %s fault, got %s: %s", code, f.Code, f.Message)
	}
}

func obligationKinds(s Status) []string {
	out := []string{}
	for _, o := range s.Obligations {
		out = append(out, o.Kind+":"+o.Fault.Code)
	}
	return out
}

// Dispatch exact counts: every prepared intent becomes exactly one attempt,
// one physical invocation and one physical observation, however many ticks
// follow, and the batch never exceeds installation concurrency.
func TestDispatchInvokesEachAttemptExactlyOnce(t *testing.T) {
	f := newFx(t)
	provider := f.adapter("synthetic")
	var ops []contract.ID
	for i := 0; i < 6; i++ {
		ops = append(ops, f.prepare("synthetic", nil))
	}
	c, sess := f.started()

	// Hold every call open: the first tick may start only as many calls as
	// the installation concurrency allows.
	entered := make(chan struct{}, len(ops))
	release := make(chan struct{})
	provider.reply = func(context.Context, contract.Dispatch) (contract.Observation, error) {
		entered <- struct{}{}
		<-release
		return succeeded(nil), nil
	}
	ctx := context.Background()
	if err := c.tick(ctx, ctx, sess); err != nil {
		t.Fatalf("tick: %v", err)
	}
	for i := 0; i < installationConcurrency; i++ {
		<-entered
	}
	if got := c.Status().InFlight; got != installationConcurrency {
		t.Fatalf("in flight %d, want the concurrency bound %d", got, installationConcurrency)
	}
	if got := f.called("_effects.claim"); got != installationConcurrency {
		t.Fatalf("claimed %d attempts with every slot busy, want %d", got, installationConcurrency)
	}
	close(release)
	c.workers.Wait()
	for i := 0; i < 4; i++ {
		if err := f.pass(c, sess); err != nil {
			t.Fatalf("tick: %v", err)
		}
	}
	if got := provider.calls(); got != len(ops) {
		t.Fatalf("provider invoked %d times for %d operations", got, len(ops))
	}
	seen := map[contract.ID]bool{}
	for _, d := range provider.invoked {
		if seen[d.AttemptID] {
			t.Fatalf("attempt %s was invoked twice", d.AttemptID)
		}
		seen[d.AttemptID] = true
		if d.Generation != sess.generation {
			t.Fatalf("dispatch generation %d, want %d", d.Generation, sess.generation)
		}
	}
	for _, op := range ops {
		if got := f.opState(op); got != "succeeded" {
			t.Fatalf("operation %s is %s, want succeeded", op, got)
		}
		if got := f.attempts(op); got != 1 {
			t.Fatalf("operation %s has %d attempts, want 1", op, got)
		}
		if got := f.observations(op); !reflect.DeepEqual(got, []string{"physical:succeeded"}) {
			t.Fatalf("operation %s observations %v", op, got)
		}
	}
	s := c.Status()
	if s.Invocations != int64(len(ops)) || s.Recorded != int64(len(ops)) || s.InFlight != 0 {
		t.Fatalf("status %+v", s)
	}
	if provider.reconciles() != 0 {
		t.Fatal("the controller must never reconcile without an admitted reconciliation attempt")
	}
}

// Uncertain and unconfirmed operations are listed by the owner but are never
// admitted, claimed, resent or reconciled by the dispatch loop.
func TestDispatchNeverTouchesUncertainOperations(t *testing.T) {
	f := newFx(t)
	provider := f.adapter("synthetic")
	unknown := f.prepare("synthetic", nil)
	accepted := f.prepare("synthetic", nil)
	f.exec(`UPDATE effects_operations SET state = 'outcome_unknown' WHERE id = ?`, string(unknown))
	f.exec(`UPDATE effects_operations SET state = 'awaiting_confirmation' WHERE id = ?`, string(accepted))
	c, sess := f.started()
	for i := 0; i < 3; i++ {
		if err := f.pass(c, sess); err != nil {
			t.Fatalf("tick: %v", err)
		}
	}
	if provider.calls() != 0 || provider.reconciles() != 0 {
		t.Fatalf("provider saw %d invocations and %d reconciles", provider.calls(), provider.reconciles())
	}
	if f.called("_effects.admit") != 0 || f.called("_effects.claim") != 0 {
		t.Fatal("uncertain operations must not be admitted or claimed")
	}
	if f.opState(unknown) != "outcome_unknown" || f.opState(accepted) != "awaiting_confirmation" {
		t.Fatal("uncertainty must be retained")
	}
}

// An admission the owner refuses leaves the operation pending; the loop backs
// off instead of re-admitting on every tick, and a denial creates no attempt.
func TestRefusedAdmissionBacksOffAndDenialCreatesNoAttempt(t *testing.T) {
	f := newFx(t)
	provider := f.adapter("synthetic")
	review := f.prepare("synthetic", nil)
	denied := f.prepare("synthetic", nil)
	f.exec(`UPDATE effects_operations SET admit_mode = 'review' WHERE id = ?`, string(review))
	f.exec(`UPDATE effects_operations SET admit_mode = 'deny' WHERE id = ?`, string(denied))
	c, sess := f.started()
	for i := 0; i < 8; i++ {
		if err := f.pass(c, sess); err != nil {
			t.Fatalf("tick: %v", err)
		}
	}
	// review: ticks 1, 3 and 7 (backoff 2, then 4); denied: once.
	if got := f.called("_effects.admit"); got != 4 {
		t.Fatalf("admit called %d times over 8 ticks, want 4", got)
	}
	if provider.calls() != 0 || f.attempts(review) != 0 || f.attempts(denied) != 0 {
		t.Fatal("a refused or denied admission must not create an attempt or reach the provider")
	}
	if f.opState(denied) != "denied" || f.opState(review) != "prepared" {
		t.Fatalf("states: denied=%s review=%s", f.opState(denied), f.opState(review))
	}
	wantFault(t, c.Status().LastFault, contract.CodeReviewRequired)
	for _, e := range sess.journal.snapshot() {
		if e.open() {
			t.Fatalf("journal entry %s left open in phase %s", e.ID, e.Phase)
		}
	}
}

// What an adapter fails to establish is kept as unknown; the controller
// never converts an adapter error, panic or malformed return into a failure,
// a cancellation or a free success.
func TestAdapterMisbehaviourIsRetainedAsUnknown(t *testing.T) {
	cases := []struct {
		name        string
		reply       func(context.Context, contract.Dispatch) (contract.Observation, error)
		state       string
		disposition string
		code        string
	}{
		{"error", func(context.Context, contract.Dispatch) (contract.Observation, error) {
			return contract.Observation{}, errors.New("dial tcp 192.0.2.1: private detail")
		}, "outcome_unknown", "unknown", "adapter_error"},
		{"fault", func(context.Context, contract.Dispatch) (contract.Observation, error) {
			return contract.Observation{}, &contract.Fault{Code: contract.CodeCapabilityUnsupported, Message: "x"}
		}, "outcome_unknown", "unknown", contract.CodeCapabilityUnsupported},
		{"panic", func(context.Context, contract.Dispatch) (contract.Observation, error) {
			panic("adapter bug")
		}, "outcome_unknown", "unknown", "adapter_panic"},
		{"bad disposition", func(context.Context, contract.Dispatch) (contract.Observation, error) {
			return contract.Observation{Disposition: "cancelled"}, nil
		}, "outcome_unknown", "unknown", "invalid_disposition"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFx(t)
			provider := f.adapter("synthetic")
			provider.reply = tc.reply
			op := f.prepare("synthetic", nil)
			c, sess := f.started()
			for i := 0; i < 3; i++ {
				if err := f.pass(c, sess); err != nil {
					t.Fatalf("tick: %v", err)
				}
			}
			if provider.calls() != 1 {
				t.Fatalf("provider invoked %d times, want exactly 1", provider.calls())
			}
			if got := f.opState(op); got != tc.state {
				t.Fatalf("operation is %s, want %s", got, tc.state)
			}
			evidence := f.queryString(`SELECT evidence FROM effects_observations WHERE operation_id = ?`, string(op))
			var got struct {
				Code   string `json:"code"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal([]byte(evidence), &got); err != nil || got.Code != tc.code {
				t.Fatalf("evidence %s, want code %s", evidence, tc.code)
			}
			if got.Reason == "" || containsAny(evidence, "192.0.2.1", "private detail", "adapter bug") {
				t.Fatalf("evidence leaks adapter detail: %s", evidence)
			}
			usage := f.queryString(`SELECT usage FROM effects_observations WHERE operation_id = ?`, string(op))
			if usage != `{"currency":"USD","spent":0,"reserved":0,"estimated":0,"unknown":5000,"advisory":true}` {
				t.Fatalf("unknown usage must retain the whole bound as unknown: %s", usage)
			}
		})
	}
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if len(n) > 0 && len(s) >= len(n) {
			for i := 0; i+len(n) <= len(s); i++ {
				if s[i:i+len(n)] == n {
					return true
				}
			}
		}
	}
	return false
}

// A success whose usage report is absent is recorded with unknown advisory
// usage, never as free.
func TestAbsentUsageIsNeverFree(t *testing.T) {
	f := newFx(t)
	provider := f.adapter("synthetic")
	provider.reply = func(context.Context, contract.Dispatch) (contract.Observation, error) {
		obs := succeeded(nil)
		obs.Usage = nil
		return obs, nil
	}
	op := f.prepare("synthetic", nil)
	c, sess := f.started()
	if err := f.pass(c, sess); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if got := f.opState(op); got != "succeeded" {
		t.Fatalf("operation is %s", got)
	}
	usage := f.queryString(`SELECT usage FROM effects_observations WHERE operation_id = ?`, string(op))
	if usage != `{"currency":"USD","spent":0,"reserved":0,"estimated":0,"unknown":5000,"advisory":true}` {
		t.Fatalf("usage %s", usage)
	}
}

// A claimed dispatch naming an adapter this controller does not have is never
// invoked. The controller says so; the owner, having consumed the claim,
// keeps the outcome unknown rather than trusting a non-execution claim.
func TestUnregisteredAdapterIsNeverInvoked(t *testing.T) {
	f := newFx(t)
	provider := f.adapter("synthetic")
	op := f.prepare("absent-adapter", nil)
	c, sess := f.started()
	for i := 0; i < 3; i++ {
		if err := f.pass(c, sess); err != nil {
			t.Fatalf("tick: %v", err)
		}
	}
	if provider.calls() != 0 {
		t.Fatal("no adapter may be invoked for an unregistered adapter name")
	}
	if got := f.observations(op); !reflect.DeepEqual(got, []string{"physical:not_sent"}) {
		t.Fatalf("observations %v", got)
	}
	if got := f.opState(op); got != "outcome_unknown" {
		t.Fatalf("operation is %s; a consumed claim cannot be proven unsent", got)
	}
	evidence := f.queryString(`SELECT evidence FROM effects_observations WHERE operation_id = ?`, string(op))
	if !containsAny(evidence, contract.CodeCapabilityUnsupported) || !containsAny(evidence, `"adapter_invoked":"no"`) {
		t.Fatalf("evidence %s", evidence)
	}
	if f.attempts(op) != 1 {
		t.Fatalf("attempts %d", f.attempts(op))
	}
}
