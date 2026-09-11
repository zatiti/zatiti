package connections

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Behavioral tests for the local IO seam: challenge begin/cancel/complete
// across Prepare/Perform/Finish, consent URL assembly from trusted metadata,
// helper receipt verification and the expiry and pin-revalidation fences.

// prepareOnly commits just the Prepare phase of a local IO operation: the
// admission transaction stands, Perform and Finish never run.
func (e *testEnv) prepareOnly(op string, in any) contract.IOPlan {
	e.t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		e.t.Fatalf("marshal %s: %v", op, err)
	}
	var plan contract.IOPlan
	err = e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		p, perr := e.svc.Prepare(e.ctx, unit, contract.Invocation{Operation: op, Version: 1, Input: raw})
		plan = p
		return perr
	})
	if err != nil {
		e.t.Fatalf("Prepare(%s): %v", op, err)
	}
	return plan
}

// finishPlan commits just the Finish phase with the supplied result.
func (e *testEnv) finishPlan(plan contract.IOPlan, result contract.IOResult) error {
	e.t.Helper()
	return e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		_, ferr := e.svc.Finish(e.ctx, unit, plan, result)
		return ferr
	})
}

// statusOf reads one challenge through the public status operation.
func (e *testEnv) statusOf(id contract.ID) wireChallenge {
	e.t.Helper()
	return e.challengeOf(e.mustOK("connection.setup.status", setupStatusIn{Scope: e.scope, ChallengeID: id}))
}

// driveBeginFaulted drives one begin to a faulted disposition and returns the
// faulted payload plus the fault, for tests that also need the challenge ID
// the failed plan carried.
func (e *testEnv) driveBeginFaulted(t *testing.T, in beginInput) (*contract.Fault, contract.IOPlan) {
	t.Helper()
	run, err := e.ioAs(e.scope, e.actor, "connection.setup.begin", in)
	if err != nil {
		t.Fatalf("begin errored instead of faulting: %v", err)
	}
	if run.Payload.Error == nil {
		t.Fatalf("begin succeeded, want faulted disposition: %+v", run.Payload)
	}
	return run.Payload.Error, run.Plan
}

func TestBeginBrowserChallengeAssemblesConsent(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)
	env.seedBrowserCredential(conn.CredentialRef)
	run := env.beginChallenge(conn, methodBrowser)

	plan := planChallenge(run.Plan)
	if plan.State != challengePending || plan.Version != 1 {
		t.Fatalf("prepared channel carries %+v, want pending v1", plan)
	}
	if planPrivate(run.Plan).CredentialRef != conn.CredentialRef {
		t.Fatalf("prepared private channel lost the credential reference")
	}

	ch := env.challengeOf(run.Payload)
	if ch.State != challengeExternalActionRequired || ch.Version != 2 {
		t.Fatalf("committed challenge %+v, want external_action_required v2", ch)
	}
	if !ch.ExpiresAt.Equal(env.clock.Now().Add(challengeExpiry)) {
		t.Fatalf("expiry %s, want begin instant + %s", ch.ExpiresAt, challengeExpiry)
	}
	parsed, err := url.Parse(ch.ConsentURL)
	if err != nil {
		t.Fatalf("consent URL %q does not parse: %v", ch.ConsentURL, err)
	}
	if parsed.Scheme != "https" || !strings.HasPrefix(parsed.Host, "auth.example.test") {
		t.Fatalf("consent URL %q does not come from the stored https authorize_url", ch.ConsentURL)
	}
	q := parsed.Query()
	if q.Get("response_type") != "code" || q.Get("client_id") != "client-example-public" {
		t.Fatalf("consent query %v lacks the provider authorization parameters", q)
	}
	if q.Get("redirect_uri") != "https://connect.example.test/callback" {
		t.Fatalf("consent redirect_uri %q does not match the stored metadata", q.Get("redirect_uri"))
	}
	if q.Get("state") != string(ch.ID) {
		t.Fatalf("consent state %q is not the challenge identity %s", q.Get("state"), ch.ID)
	}

	// The exact emissions of the two committed phases.
	var begun, awaiting bool
	for _, k := range env.kindsOf() {
		switch k {
		case "connections.challenge.begun":
			begun = true
		case "connections.challenge.awaiting_external_action":
			awaiting = true
		}
	}
	if !begun || !awaiting {
		t.Fatalf("begin emissions incomplete among %v", env.kindsOf())
	}
}

func TestBeginStoreReferenceNeedsNoExternalWork(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)
	ch := env.challengeOf(env.beginChallenge(conn, methodStoreReference).Payload)

	if ch.State != challengeExternalActionRequired || ch.ConsentURL != "" {
		t.Fatalf("store_reference challenge %+v carries unexpected external work", ch)
	}
	// Begin touched no peer ports and read no secrets: the trusted helper
	// does that later, outside model-visible data.
	for _, op := range []string{"_execution.job.create", "_configuration.stage"} {
		if calls := env.ports.callsOf(op); len(calls) != 0 {
			t.Fatalf("begin made %d %s peer calls", len(calls), op)
		}
	}
	if gets := env.secrets.getsOf(); len(gets) != 0 {
		t.Fatalf("begin read secret references %v", gets)
	}
}

func TestBeginSerializesLiveChallenges(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)
	env.seedBrowserCredential(conn.CredentialRef)
	first := env.challengeOf(env.beginChallenge(conn, methodStoreReference).Payload)

	_ = env.expectIOFault("connection.setup.begin", beginInput{
		Scope: env.scope, ConnectionID: conn.ID, ExpectedVersion: conn.Version, Method: methodBrowser,
	}, contract.CodeConflict)

	// Cancelling the live challenge frees the connection for a new begin.
	env.mustIO("connection.setup.cancel", cancelInput{
		Scope: env.scope, ChallengeID: first.ID, ExpectedVersion: first.Version,
	})
	second := env.challengeOf(env.beginChallenge(conn, methodBrowser).Payload)
	if second.ID == first.ID || second.State != challengeExternalActionRequired {
		t.Fatalf("re-begin produced %+v", second)
	}
}

func TestBeginRefusesStaleVersionAndArchived(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)
	_ = env.expectIOFault("connection.setup.begin", beginInput{
		Scope: env.scope, ConnectionID: conn.ID, ExpectedVersion: 99, Method: methodStoreReference,
	}, contract.CodeStaleVersion)

	archived := env.seedConnection(nil)
	env.activate(archiveDef(archived.ID, 1, archived))
	f := env.expectIOFault("connection.setup.begin", beginInput{
		Scope: env.scope, ConnectionID: archived.ID, ExpectedVersion: 1, Method: methodStoreReference,
	}, contract.CodePrerequisiteMissing)
	if !strings.Contains(f.Message, "archived") {
		t.Fatalf("refusal message %q does not name the archived lifecycle", f.Message)
	}
}

func TestBeginBrowserUnusableMetadataRefuses(t *testing.T) {
	env := newEnv(t)
	cases := []struct {
		name     string
		material string
	}{
		{"absent-credential", ""},
		{"malformed-json", `{"client_id": `},
		{"missing-fields", `{"client_id": "client-example-public"}`},
		{"http-authorize-url", `{"client_id": "client-example-public",` +
			` "authorize_url": "http://auth.example.test/authorize",` +
			` "redirect_uri": "https://connect.example.test/callback"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := env.seedConnection(nil)
			if tc.material != "" {
				env.secrets.seed(t, conn.CredentialRef, []byte(tc.material))
			}
			f, plan := env.driveBeginFaulted(t, beginInput{
				Scope: env.scope, ConnectionID: conn.ID, ExpectedVersion: 1, Method: methodBrowser,
			})
			if f.Code != contract.CodePrerequisiteMissing {
				t.Fatalf("fault %s (%s), want prerequisite_missing", f.Code, f.Message)
			}
			if !strings.Contains(f.Message, string(conn.CredentialRef)) {
				t.Fatalf("refusal message %q does not name the credential reference", f.Message)
			}
			// The failed challenge is terminal and honestly reported: no
			// consent link was invented and no success was recorded.
			ch := env.statusOf(plan.ID)
			if ch.State != challengeFailed {
				t.Fatalf("status reports %q, want failed", ch.State)
			}
			var failedEmitted bool
			for _, k := range env.kindsOf() {
				if k == "connections.challenge.failed" {
					failedEmitted = true
				}
			}
			if !failedEmitted {
				t.Fatalf("no connections.challenge.failed event among %v", env.kindsOf())
			}
			// A failed challenge is terminal, so the connection admits a new
			// begin once the metadata is usable.
			env.seedBrowserCredential(conn.CredentialRef)
			recovered := env.challengeOf(env.beginChallenge(conn, methodBrowser).Payload)
			if recovered.State != challengeExternalActionRequired {
				t.Fatalf("recovery begin produced %+v", recovered)
			}
		})
	}
}

func TestCancelHappyAndIdempotentReplay(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)
	ch := env.challengeOf(env.beginChallenge(conn, methodStoreReference).Payload)

	in := cancelInput{Scope: env.scope, ChallengeID: ch.ID, ExpectedVersion: ch.Version}
	cancelled := env.challengeOf(env.mustIO("connection.setup.cancel", in).Payload)
	if cancelled.State != challengeCancelled || cancelled.Version != ch.Version+1 {
		t.Fatalf("cancel produced %+v, want cancelled at v%d", cancelled, ch.Version+1)
	}
	count := func() int {
		n := 0
		for _, k := range env.kindsOf() {
			if k == "connections.challenge.cancelled" {
				n++
			}
		}
		return n
	}
	if count() != 1 {
		t.Fatalf("cancel emitted %d cancellation events, want 1", count())
	}

	// The identical replay returns the terminal challenge unchanged and
	// emits nothing further.
	replayed := env.challengeOf(env.mustIO("connection.setup.cancel", in).Payload)
	if replayed.State != challengeCancelled || replayed.Version != cancelled.Version {
		t.Fatalf("replay produced %+v, want the standing terminal row", replayed)
	}
	if count() != 1 {
		t.Fatalf("replay emitted another cancellation event (total %d)", count())
	}
}

func TestCancelByOtherPrincipalRefused(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)
	ch := env.challengeOf(env.beginChallenge(conn, methodStoreReference).Payload)
	_, err := env.ioAs(env.scope, env.other, "connection.setup.cancel", cancelInput{
		Scope: env.scope, ChallengeID: ch.ID, ExpectedVersion: ch.Version,
	})
	var f *contract.Fault
	if err == nil {
		t.Fatalf("another principal cancelled a challenge it did not begin")
	}
	if !errors.As(err, &f) || f.Code != contract.CodePermissionDenied {
		t.Fatalf("refusal %v, want permission_denied", err)
	}
	if !strings.Contains(f.Message, "initiating principal") {
		t.Fatalf("refusal message %q does not name the principal binding", f.Message)
	}
	// The challenge is untouched and still live.
	if now := env.statusOf(ch.ID); now.State != challengeExternalActionRequired {
		t.Fatalf("refused cancel left the challenge %q", now.State)
	}
}

func TestCancelCompletedRefusesConflict(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)
	env.seedBrowserCredential(conn.CredentialRef)
	env.secrets.seed(t, helperReceiptKeyRef, helperReceiptKeyMaterial)
	ch := env.challengeOf(env.beginChallenge(conn, methodBrowser).Payload)
	receipt := mintReceipt(helperReceiptKeyMaterial, helperPayload{
		ChallengeID: ch.ID, CredentialRef: conn.CredentialRef, AccountIdentity: conn.AccountIdentity,
	})
	env.mustIO("connection.setup.complete", completeInput{
		Scope: env.scope, ChallengeID: ch.ID, ExpectedVersion: ch.Version, HelperRef: receipt,
	})
	_ = env.expectIOFault("connection.setup.cancel", cancelInput{
		Scope: env.scope, ChallengeID: ch.ID, ExpectedVersion: ch.Version + 1,
	}, contract.CodeConflict)
}

func TestCompleteHappyPathRecordsOpaqueReceipt(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)
	env.seedBrowserCredential(conn.CredentialRef)
	env.secrets.seed(t, helperReceiptKeyRef, helperReceiptKeyMaterial)
	ch := env.challengeOf(env.beginChallenge(conn, methodBrowser).Payload)
	receipt := mintReceipt(helperReceiptKeyMaterial, helperPayload{
		ChallengeID: ch.ID, CredentialRef: conn.CredentialRef, AccountIdentity: conn.AccountIdentity,
	})

	run := env.mustIO("connection.setup.complete", completeInput{
		Scope: env.scope, ChallengeID: ch.ID, ExpectedVersion: ch.Version, HelperRef: receipt,
	})
	done := env.challengeOf(run.Payload)
	if done.State != challengeCompleted || done.Version != ch.Version+1 {
		t.Fatalf("complete produced %+v, want completed at v%d", done, ch.Version+1)
	}
	if done.HelperRef != receipt {
		t.Fatalf("helper reference %q does not equal the opaque receipt", done.HelperRef)
	}
	assertNoSecretBytes(t, run.Payload.Data, string(helperReceiptKeyMaterial))
	var completedEmitted bool
	for _, k := range env.kindsOf() {
		if k == "connections.challenge.completed" {
			completedEmitted = true
		}
	}
	if !completedEmitted {
		t.Fatalf("no connections.challenge.completed event among %v", env.kindsOf())
	}

	// Completion is consent evidence, never a validated probe: the
	// connection stays unverified until an observation records success.
	payload := env.mustOK("connection.get", connGetIn{Scope: env.scope, ID: conn.ID})
	var out resourceOut
	env.decode(payload.Data, &out)
	if out.Resource.ValidationState != connStateUnverified {
		t.Fatalf("completion moved validation state to %q", out.Resource.ValidationState)
	}
}

func TestCompleteRefusesWrongStatesAndVersions(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)

	t.Run("pending-challenge-has-no-external-action", func(t *testing.T) {
		// Commit only the begin Prepare phase: the challenge stands at
		// pending with no external action recorded.
		plan := env.prepareOnly("connection.setup.begin", beginInput{
			Scope: env.scope, ConnectionID: conn.ID, ExpectedVersion: 1, Method: methodStoreReference,
		})
		_ = env.expectIOFault("connection.setup.complete", completeInput{
			Scope: env.scope, ChallengeID: plan.ID, ExpectedVersion: 1,
			HelperRef: helperReceiptPrefix + "whatever",
		}, contract.CodeInvalidInput)
	})

	t.Run("wrong-challenge-version-refused", func(t *testing.T) {
		conn2 := env.seedConnection(nil)
		ch := env.challengeOf(env.beginChallenge(conn2, methodStoreReference).Payload)
		_ = env.expectIOFault("connection.setup.complete", completeInput{
			Scope: env.scope, ChallengeID: ch.ID, ExpectedVersion: ch.Version - 1,
			HelperRef: helperReceiptPrefix + "whatever",
		}, contract.CodeStaleVersion)
	})

	t.Run("cancelled-challenge-cannot-complete", func(t *testing.T) {
		conn3 := env.seedConnection(nil)
		ch := env.challengeOf(env.beginChallenge(conn3, methodStoreReference).Payload)
		env.mustIO("connection.setup.cancel", cancelInput{
			Scope: env.scope, ChallengeID: ch.ID, ExpectedVersion: ch.Version,
		})
		_ = env.expectIOFault("connection.setup.complete", completeInput{
			Scope: env.scope, ChallengeID: ch.ID, ExpectedVersion: ch.Version + 1,
			HelperRef: helperReceiptPrefix + "whatever",
		}, contract.CodePrerequisiteMissing)
	})
}

func TestExpiryDerivedStatusAndCompleteRefusal(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)
	ch := env.challengeOf(env.beginChallenge(conn, methodStoreReference).Payload)

	env.clock.Advance(challengeExpiry + time.Minute)
	// Status derives the honest expired view even though no writer has
	// persisted the transition yet.
	derived := env.statusOf(ch.ID)
	if derived.State != challengeExpired {
		t.Fatalf("status reports %q after expiry, want expired", derived.State)
	}
	// An expired challenge cannot complete.
	_ = env.expectIOFault("connection.setup.complete", completeInput{
		Scope: env.scope, ChallengeID: ch.ID, ExpectedVersion: ch.Version,
		HelperRef: helperReceiptPrefix + "whatever",
	}, contract.CodePrerequisiteMissing)
}

func TestFinishRefusesMovedChallenge(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)
	live := env.challengeOf(env.beginChallenge(conn, methodStoreReference).Payload)

	// Prepare a cancel pinning the challenge at its current version.
	stalePlan := env.prepareOnly("connection.setup.cancel", cancelInput{
		Scope: env.scope, ChallengeID: live.ID, ExpectedVersion: live.Version,
	})
	// The challenge moves: a real cancel commits the terminal transition.
	env.mustIO("connection.setup.cancel", cancelInput{
		Scope: env.scope, ChallengeID: live.ID, ExpectedVersion: live.Version,
	})
	// Finishing the stale plan refuses: the pin no longer matches.
	err := env.finishPlan(stalePlan, contract.IOResult{})
	var f *contract.Fault
	if err == nil {
		t.Fatalf("stale plan finished successfully")
	}
	if !errors.As(err, &f) || f.Code != contract.CodeStaleVersion {
		t.Fatalf("stale finish %v, want stale_version", err)
	}
	// The committed terminal state stands; the refused finish changed
	// nothing.
	if now := env.statusOf(live.ID); now.State != challengeCancelled || now.Version != live.Version+1 {
		t.Fatalf("refused finish disturbed the challenge: %+v", now)
	}
}
