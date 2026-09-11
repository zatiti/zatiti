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

// Acceptance tests for the connections-local proving focus:
// credential challenge expiry/cancel/replay, helper forgery, raw-secret
// rejection, same-account rotation vs substitution, and the bound consent
// redirect. Stale validation, bound destinations and plugin discovery are
// proved in internal_test.go and tools_test.go.

// TestZ01CredentialCustody is the Z01/Z13 local property: credential setup
// handles opaque references only. Raw secret material planted in the store
// surfaces in no payload and no event; the helper receipt is an opaque,
// signed envelope; the challenge is bound to its initiating principal, which
// alone may cancel or complete it.
func TestZ01CredentialCustody(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)
	env.secrets.seed(t, conn.CredentialRef, []byte(markedSecret))
	env.secrets.seed(t, helperReceiptKeyRef, helperReceiptKeyMaterial)

	run := env.beginChallenge(conn, methodStoreReference)
	ch := env.challengeOf(run.Payload)
	receipt := mintReceipt(helperReceiptKeyMaterial, helperPayload{
		ChallengeID: ch.ID, CredentialRef: conn.CredentialRef, AccountIdentity: conn.AccountIdentity,
	})
	completed := env.mustIO("connection.setup.complete", completeInput{
		Scope: env.scope, ChallengeID: ch.ID, ExpectedVersion: ch.Version, HelperRef: receipt,
	})
	status := env.mustOK("connection.setup.status", setupStatusIn{Scope: env.scope, ChallengeID: ch.ID})
	got := env.mustOK("connection.get", connGetIn{Scope: env.scope, ID: conn.ID})
	listed := env.mustOK("connection.list", connListIn{Scope: env.scope})

	// No payload surface carries the raw secret or the receipt key.
	for _, surface := range []struct {
		name string
		raw  json.RawMessage
	}{
		{"begin", run.Payload.Data}, {"complete", completed.Payload.Data},
		{"status", status.Data}, {"connection.get", got.Data}, {"connection.list", listed.Data},
	} {
		assertNoSecretBytes(t, surface.raw, markedSecret, string(helperReceiptKeyMaterial))
	}
	// No event surface carries them either.
	for _, ev := range env.events(0) {
		assertNoSecretBytes(t, ev.Data, markedSecret, string(helperReceiptKeyMaterial))
	}
	// The stored material was read only during verification, never returned.
	var credentialRead, keyRead bool
	for _, ref := range env.secrets.getsOf() {
		if ref == conn.CredentialRef {
			credentialRead = true
		}
		if ref == helperReceiptKeyRef {
			keyRead = true
		}
	}
	if !credentialRead || !keyRead {
		t.Fatalf("verification read credential=%v key=%v, want both", credentialRead, keyRead)
	}

	// The challenge binds to its initiating principal: another principal can
	// neither complete nor cancel it, and no tool argument changes that.
	for _, attempt := range []struct {
		op string
		in any
	}{
		{"connection.setup.complete", completeInput{
			Scope: env.scope, ChallengeID: ch.ID, ExpectedVersion: ch.Version, HelperRef: receipt,
		}},
		{"connection.setup.cancel", cancelInput{
			Scope: env.scope, ChallengeID: ch.ID, ExpectedVersion: ch.Version,
		}},
	} {
		run, err := env.ioAs(env.scope, env.other, attempt.op, attempt.in)
		var f *contract.Fault
		if err != nil {
			if !errors.As(err, &f) {
				t.Fatalf("%s refusal %v is not a fault", attempt.op, err)
			}
		} else {
			f = run.Payload.Error
		}
		if f == nil {
			t.Fatalf("%s succeeded for a principal that did not begin the challenge", attempt.op)
		}
		if f.Code != contract.CodePermissionDenied {
			t.Fatalf("%s refusal %s (%s), want permission_denied", attempt.op, f.Code, f.Message)
		}
	}
}

// TestChallengeExpiryCancelReplay is the expiry local property: a challenge
// whose expiry passed reports expired honestly, can no longer complete, and
// a late cancel records the expired terminal state — never a false
// cancellation — which frees the connection for a fresh begin.
func TestChallengeExpiryCancelReplay(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)
	env.seedBrowserCredential(conn.CredentialRef)
	env.secrets.seed(t, helperReceiptKeyRef, helperReceiptKeyMaterial)
	ch := env.challengeOf(env.beginChallenge(conn, methodBrowser).Payload)

	env.clock.Advance(challengeExpiry + time.Minute)

	// Status derives the honest expired view before any writer commits.
	if derived := env.statusOf(ch.ID); derived.State != challengeExpired {
		t.Fatalf("status reports %q after expiry, want expired", derived.State)
	}
	// An expired challenge cannot complete.
	f := env.expectIOFault("connection.setup.complete", completeInput{
		Scope: env.scope, ChallengeID: ch.ID, ExpectedVersion: ch.Version,
		HelperRef: helperReceiptPrefix + "late",
	}, contract.CodePrerequisiteMissing)
	if !strings.Contains(f.Message, "expired") {
		t.Fatalf("refusal message %q does not name the expiry", f.Message)
	}

	// A late cancel commits the honest expired terminal state.
	in := cancelInput{Scope: env.scope, ChallengeID: ch.ID, ExpectedVersion: ch.Version}
	expired := env.challengeOf(env.mustIO("connection.setup.cancel", in).Payload)
	if expired.State != challengeExpired {
		t.Fatalf("late cancel recorded %q, want expired", expired.State)
	}
	count := func(kind string) int {
		n := 0
		for _, k := range env.kindsOf() {
			if k == kind {
				n++
			}
		}
		return n
	}
	if count("connections.challenge.expired") != 1 || count("connections.challenge.cancelled") != 0 {
		t.Fatalf("late cancel emitted expired=%d cancelled=%d, want 1/0",
			count("connections.challenge.expired"), count("connections.challenge.cancelled"))
	}

	// The replay returns the standing terminal row and emits nothing.
	replayed := env.challengeOf(env.mustIO("connection.setup.cancel", in).Payload)
	if replayed.State != challengeExpired || replayed.Version != expired.Version {
		t.Fatalf("replay produced %+v, want the standing expired row", replayed)
	}
	if count("connections.challenge.expired") != 1 {
		t.Fatalf("replay emitted another expiry event (total %d)", count("connections.challenge.expired"))
	}

	// The terminal state frees the connection: a fresh begin succeeds.
	again := env.challengeOf(env.beginChallenge(conn, methodBrowser).Payload)
	if again.ID == ch.ID || again.State != challengeExternalActionRequired {
		t.Fatalf("post-expiry begin produced %+v", again)
	}
}

// TestHelperForgeryRefuses is the helper-forgery local property: every way
// to forge or misdirect the opaque receipt refuses as verification_failed,
// and only the exactly-signed receipt bound to this challenge, this account
// and a held credential reference passes.
func TestHelperForgeryRefuses(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)
	env.seedBrowserCredential(conn.CredentialRef)
	env.secrets.seed(t, helperReceiptKeyRef, helperReceiptKeyMaterial)

	// The genuine receipt passes first.
	ch := env.challengeOf(env.beginChallenge(conn, methodBrowser).Payload)
	genuine := mintReceipt(helperReceiptKeyMaterial, helperPayload{
		ChallengeID: ch.ID, CredentialRef: conn.CredentialRef, AccountIdentity: conn.AccountIdentity,
	})
	env.mustIO("connection.setup.complete", completeInput{
		Scope: env.scope, ChallengeID: ch.ID, ExpectedVersion: ch.Version, HelperRef: genuine,
	})

	// Forged variants: each gets a fresh challenge (a refused completion
	// marks the previous one failed, which is terminal).
	tampered := func(receipt string) string {
		rest := strings.TrimPrefix(receipt, helperReceiptPrefix)
		body, sig, _ := strings.Cut(rest, ".")
		flipped := byte('A')
		if body[len(body)-1] == 'A' {
			flipped = 'B'
		}
		return helperReceiptPrefix + body[:len(body)-1] + string(flipped) + "." + sig
	}
	variants := []struct {
		name    string
		receipt func(challengeID contract.ID) string
		wantMsg string
	}{
		{"wrong-signing-key", func(id contract.ID) string {
			return mintReceipt([]byte("forged-key-material-0000"), helperPayload{
				ChallengeID: id, CredentialRef: conn.CredentialRef, AccountIdentity: conn.AccountIdentity,
			})
		}, "does not verify"},
		{"tampered-payload", func(id contract.ID) string {
			return tampered(mintReceipt(helperReceiptKeyMaterial, helperPayload{
				ChallengeID: id, CredentialRef: conn.CredentialRef, AccountIdentity: conn.AccountIdentity,
			}))
		}, "does not verify"},
		{"wrong-challenge-binding", func(contract.ID) string {
			return mintReceipt(helperReceiptKeyMaterial, helperPayload{
				ChallengeID: env.ids.New(), CredentialRef: conn.CredentialRef,
				AccountIdentity: conn.AccountIdentity,
			})
		}, "is bound to challenge"},
		{"wrong-account-binding", func(id contract.ID) string {
			return mintReceipt(helperReceiptKeyMaterial, helperPayload{
				ChallengeID: id, CredentialRef: conn.CredentialRef,
				AccountIdentity: "acct-impersonated",
			})
		}, "substitution is refused"},
		{"unheld-credential-reference", func(id contract.ID) string {
			return mintReceipt(helperReceiptKeyMaterial, helperPayload{
				ChallengeID: id, CredentialRef: "connections/credentials/ghost",
				AccountIdentity: conn.AccountIdentity,
			})
		}, "does not hold"},
	}
	for _, tc := range variants {
		t.Run(tc.name, func(t *testing.T) {
			live := env.challengeOf(env.beginChallenge(conn, methodBrowser).Payload)
			f := env.expectIOFault("connection.setup.complete", completeInput{
				Scope: env.scope, ChallengeID: live.ID, ExpectedVersion: live.Version,
				HelperRef: tc.receipt(live.ID),
			}, contract.CodeVerificationFailed)
			if !strings.Contains(f.Message, tc.wantMsg) {
				t.Fatalf("refusal message %q does not name the forgery defect (%s)", f.Message, tc.wantMsg)
			}
			// The refused completion left the challenge failed: no invented
			// success.
			if now := env.statusOf(live.ID); now.State != challengeFailed {
				t.Fatalf("refused completion left the challenge %q", now.State)
			}
		})
	}
}

// TestRawSecretRejection is the raw-secret local property: completion
// accepts the opaque helper receipt only. A pasted OAuth code, token or free
// text refuses before any account check runs.
func TestRawSecretRejection(t *testing.T) {
	env := newEnv(t)
	for _, tc := range []struct {
		name   string
		helper string
	}{
		{"pasted-oauth-code", "AQB.8ZzZ0dExampleCode"},
		{"pasted-token", "sk-live-synthetic-token-0001"},
		{"free-text", "please approve this connection"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A fresh connection per variant: the refusal happens in
			// Prepare, so the challenge never transitions and stays live.
			conn := env.seedConnection(nil)
			ch := env.challengeOf(env.beginChallenge(conn, methodStoreReference).Payload)
			f := env.expectIOFault("connection.setup.complete", completeInput{
				Scope: env.scope, ChallengeID: ch.ID, ExpectedVersion: ch.Version, HelperRef: tc.helper,
			}, contract.CodeVerificationFailed)
			if !strings.Contains(f.Message, "opaque helper receipt only") {
				t.Fatalf("refusal message %q does not state the opaque-receipt rule", f.Message)
			}
			// The refusal left the challenge untouched and live: it was
			// never completed, and no failure was recorded either.
			if now := env.statusOf(ch.ID); now.State != challengeExternalActionRequired {
				t.Fatalf("refused completion left the challenge %q", now.State)
			}
		})
	}
}

// TestRotationVsSubstitution is the rotation/substitution local property:
// same-account rotation admits a governed probe job carrying the original
// input under the connections owner; account substitution never rides a
// rotation and demands review; probe evidence naming a different account is
// refused outright.
func TestRotationVsSubstitution(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)

	payload := env.mustOK("connection.rotate", connRotateIn{
		Scope: env.scope, ID: conn.ID, ExpectedVersion: 1,
		StoreRef: "connections/credentials/rotated-a",
	})
	var out jobOut
	env.decode(payload.Data, &out)
	if out.Resource.Owner != "connections" || out.Resource.Operation != "connection.rotate" {
		t.Fatalf("admitted job carries owner=%q operation=%q", out.Resource.Owner, out.Resource.Operation)
	}
	if out.Resource.State != "pending" {
		t.Fatalf("admitted job state %q, want pending", out.Resource.State)
	}
	calls := env.ports.callsOf("_execution.job.create")
	if len(calls) != 1 {
		t.Fatalf("rotation made %d job-creation calls, want 1", len(calls))
	}
	var stored struct {
		Owner     string          `json:"owner"`
		Operation string          `json:"operation"`
		Input     json.RawMessage `json:"input"`
		SourceID  contract.ID     `json:"source_id"`
	}
	env.decode(calls[0].Input, &stored)
	if stored.Owner != "connections" || stored.Operation != "connection.rotate" || stored.SourceID == "" {
		t.Fatalf("stored job envelope %+v", stored)
	}
	var replay connRotateIn
	env.decode(stored.Input, &replay)
	if replay.ID != conn.ID || replay.StoreRef != "connections/credentials/rotated-a" || replay.ExpectedVersion != 1 {
		t.Fatalf("stored job input lost the original request: %+v", replay)
	}

	t.Run("job-port-failure-surfaces-honestly", func(t *testing.T) {
		env.ports.failOn("_execution.job.create", &contract.Fault{
			Code: contract.CodeBudgetUnavailable, Message: "synthetic job budget refusal",
		})
		_ = env.expectFault("connection.rotate", connRotateIn{
			Scope: env.scope, ID: conn.ID, ExpectedVersion: 1,
			StoreRef: "connections/credentials/rotated-b",
		}, contract.CodeBudgetUnavailable)
		env.ports.failOn("_execution.job.create", nil)
	})

	t.Run("revoked-connection-refuses-rotation", func(t *testing.T) {
		revoked := env.seedConnection(nil)
		env.mustOK("connection.revoke", connRevokeIn{Scope: env.scope, ID: revoked.ID, ExpectedVersion: 1})
		_ = env.expectFault("connection.rotate", connRotateIn{
			Scope: env.scope, ID: revoked.ID, ExpectedVersion: 2,
			StoreRef: "connections/credentials/revoked-a",
		}, contract.CodePrerequisiteMissing)
	})

	t.Run("stale-pin-refused", func(t *testing.T) {
		_ = env.expectFault("connection.rotate", connRotateIn{
			Scope: env.scope, ID: conn.ID, ExpectedVersion: 99,
			StoreRef: "connections/credentials/rotated-c",
		}, contract.CodeStaleVersion)
	})

	t.Run("substitution-cannot-ride-rotation", func(t *testing.T) {
		// The rotation schema has no account field: a request carrying one
		// refuses at the schema boundary.
		in := map[string]any{
			"scope":            env.scope,
			"id":               conn.ID,
			"expected_version": 1,
			"store_ref":        "connections/credentials/rotated-d",
			"account_identity": "acct-impersonated",
		}
		f := env.expectFault("connection.rotate", in, contract.CodeInvalidInput)
		if !strings.Contains(f.Message, "schema") {
			t.Fatalf("refusal message %q does not name the schema refusal", f.Message)
		}
	})

	t.Run("probe-evidence-naming-another-account-refused", func(t *testing.T) {
		payload := env.mustOK("connection.get", connGetIn{Scope: env.scope, ID: conn.ID})
		var live resourceOut
		env.decode(payload.Data, &live)
		evidence := mustRaw(map[string]any{
			"account_identity": "acct-impersonated",
			"allowed_scopes":   conn.AllowedScopes,
		})
		_ = env.expectFault("_connections.validation.record", validationRecordIn{
			ConnectionID: conn.ID, ExpectedVersion: live.Resource.Version,
			Observation: wireObservation{
				Disposition: obsSucceeded, Evidence: evidence,
				Usage: &wireUsage{Currency: "USD", Advisory: true},
			},
		}, contract.CodeVerificationFailed)
		if live.Resource.ValidationState == connStateValid {
			t.Fatalf("a probe for another account validated the connection")
		}
	})
}

// TestBoundConsentRedirectPinned is the bound-redirect local property: the
// consent redirect comes only from the stored credential metadata and the
// state parameter is the challenge identity; the caller cannot inject either.
func TestBoundConsentRedirectPinned(t *testing.T) {
	env := newEnv(t)
	alpha := env.seedConnection(func(w *wireConnection) {
		w.CredentialRef = "connections/credentials/alpha"
	})
	beta := env.seedConnection(func(w *wireConnection) {
		w.CredentialRef = "connections/credentials/beta"
	})
	env.secrets.seed(t, alpha.CredentialRef, []byte(`{
		"client_id": "client-alpha", "authorize_url": "https://auth.example.test/authorize",
		"redirect_uri": "https://connect.example.test/callback/alpha"}`))
	env.secrets.seed(t, beta.CredentialRef, []byte(`{
		"client_id": "client-beta", "authorize_url": "https://auth.example.test/authorize",
		"redirect_uri": "https://connect.example.test/callback/beta"}`))

	a := env.challengeOf(env.beginChallenge(alpha, methodBrowser).Payload)
	b := env.challengeOf(env.beginChallenge(beta, methodBrowser).Payload)
	if got := parseConsent(t, a.ConsentURL).Query().Get("redirect_uri"); got != "https://connect.example.test/callback/alpha" {
		t.Fatalf("alpha consent redirect %q is not the stored metadata", got)
	}
	if got := parseConsent(t, b.ConsentURL).Query().Get("redirect_uri"); got != "https://connect.example.test/callback/beta" {
		t.Fatalf("beta consent redirect %q is not the stored metadata", got)
	}
	if state := parseConsent(t, a.ConsentURL).Query().Get("state"); state != string(a.ID) {
		t.Fatalf("consent state %q is not the challenge identity", state)
	}

	t.Run("caller-cannot-inject-redirect-or-state", func(t *testing.T) {
		_ = env.expectIOFault("connection.setup.begin", map[string]any{
			"scope": env.scope, "connection_id": alpha.ID, "expected_version": 1,
			"method": methodBrowser, "redirect_uri": "https://evil.example.test/cb",
		}, contract.CodeInvalidInput)
		_ = env.expectIOFault("connection.setup.begin", map[string]any{
			"scope": env.scope, "connection_id": alpha.ID, "expected_version": 1,
			"method": methodBrowser, "state": "chosen-by-caller",
		}, contract.CodeInvalidInput)
		// No consent surface ever carries the forged host.
		for _, ev := range env.events(0) {
			assertNoSecretBytes(t, ev.Data, "evil.example.test")
		}
	})
}

// parseConsent parses a consent URL or fails the test.
func parseConsent(t *testing.T, consent string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(consent)
	if err != nil {
		t.Fatalf("consent URL %q does not parse: %v", consent, err)
	}
	return parsed
}
