package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/controller"
	"github.com/zatiti/zatiti/internal/platform"
)

// KNOWN BLOCKER (documented for whoever runs this file): every test in
// this file that calls openInstallation/serve (which assemble the full
// 16-module registry) is blocked by a pre-existing, unrelated defect on
// this tree, reported separately (not introduced by P24, not fixable from
// cmd/zatiti): several modules' embedded $defs (internal/policy,
// internal/accounting, internal/installation, internal/messaging) have
// drifted from the revision-3 shared shapes other modules already carry
// (missing Responsibility.last_cycle_id, Operation.callback_route/
// attempts, Artifact.purpose/source_operation_id,
// Conversation.caller_unread_count/caller_last_read_marker), and
// internal/registry's embedded catalog.json disagrees with
// docs/implementation/operations.json on task.start's scope_required.
// Both are tracked outside this card. Once fixed, the tests below need no
// change to pass -- they were verified locally against a temporary,
// reverted patch of the affected files before being committed here.

// buildResponsesProfile constructs a valid, self-bound zatiti.responses/v1
// adapter profile: every field internal/adapters/responses/profile.go's
// loadProfile requires, with capability_evidence.profile_digest computed
// exactly as profileDigestWithoutCapabilityEvidence does (canonical JSON of
// the document with capability_evidence deleted, SHA-256) -- the same
// two-pass construction internal/adapters/responses' own test helper
// (unexported, package responses, not importable here) uses.
func buildResponsesProfile(t *testing.T, endpoint string) []byte {
	t.Helper()
	doc := map[string]any{
		"schema":             "zatiti.responses/v1",
		"endpoint":           endpoint,
		"model":              "gpt-test",
		"connection_id":      string(contract.NewID()),
		"max_input_tokens":   8192,
		"max_output_tokens":  2048,
		"max_response_bytes": 1 << 20,
		"timeout_seconds":    30,
		"currency":           "USD",
		"input_rate":         map[string]any{"numerator_micro_units": 1, "denominator_units": 1, "unit": "input_token"},
		"output_rate":        map[string]any{"numerator_micro_units": 2, "denominator_units": 1, "unit": "output_token"},
		"enforcement": map[string]any{
			"cost":                  "enforced",
			"disclosure":            "enforced",
			"maximum_cost":          map[string]any{"currency": "USD", "micro_units": 1000000},
			"provider_destinations": []string{endpoint},
			"classifications":       []string{"internal"},
			"evidence":              capabilityEvidenceStub(t),
		},
		"capability_evidence": capabilityEvidenceStub(t),
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	digest := profileSelfDigest(t, raw)
	doc["capability_evidence"].(map[string]any)["profile_digest"] = string(digest)
	final, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal profile (final): %v", err)
	}
	return final
}

func capabilityEvidenceStub(t *testing.T) map[string]any {
	t.Helper()
	return map[string]any{
		"artifact":          map[string]any{"id": string(contract.NewID()), "digest": syntheticDigest(t, "artifact")},
		"adapter_version":   "test-1",
		"source_revision":   "test-1",
		"protocol_revision": "openai-responses/v1",
		"profile_digest":    syntheticDigest(t, "placeholder"), // overwritten by buildResponsesProfile's second pass
		"qualified_at":      time.Now().UTC().Format(time.RFC3339),
		"capabilities":      []string{},
		"limitations":       []string{},
	}
}

func syntheticDigest(t *testing.T, seed string) contract.Digest {
	t.Helper()
	sum := sha256.Sum256([]byte(seed))
	return contract.Digest(hex.EncodeToString(sum[:]))
}

// profileSelfDigest mirrors internal/adapters/responses/profile.go's
// profileDigestWithoutCapabilityEvidence: canonical JSON of raw with its
// top-level capability_evidence field removed, SHA-256 hex.
func profileSelfDigest(t *testing.T, raw json.RawMessage) contract.Digest {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decoding profile for digest: %v", err)
	}
	delete(doc, "capability_evidence")
	stripped, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal stripped profile: %v", err)
	}
	canon, err := contract.Canonicalize(stripped)
	if err != nil {
		t.Fatalf("canonicalize stripped profile: %v", err)
	}
	return contract.Hash(canon)
}

// TestServeAcceptsAValidResponsesProfile is half of P24's required test 1:
// the production binary (serve, through the real loadAdapters/
// adapterConstructors path this card wires -- cmd/zatiti/adapters.go)
// accepts a valid, self-bound zatiti.responses/v1 profile placed at
// <state-dir>/adapters/responses.json and registers the real
// internal/adapters/responses.New-constructed adapter, never guessing or
// silently skipping it. The profile's endpoint need not be reachable here:
// construction validates the profile document itself (schema, self-binding
// digest, rate units, enforceable bounds, destination containment) without
// making a physical call -- only Invoke would need a live endpoint, and
// this test's concern is acceptance, not a live model step.
func TestServeAcceptsAValidResponsesProfile(t *testing.T) {
	cfg := serveConfig(t)
	if err := os.MkdirAll(cfg.adaptersDir(), 0o700); err != nil {
		t.Fatalf("adapters dir: %v", err)
	}
	profile := buildResponsesProfile(t, "https://api.example.invalid/v1/responses")
	if err := os.WriteFile(filepath.Join(cfg.adaptersDir(), "responses.json"), profile, 0o600); err != nil {
		t.Fatalf("writing responses.json: %v", err)
	}

	h := openTestInstallation(t, cfg)
	bootstrapInstallation(t, h)
	h.close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done, logs := serveInBackground(t, ctx, cfg)
	waitFor(t, 15*time.Second, "the controller to run", func() bool {
		select {
		case err := <-done:
			t.Fatalf("serve exited: %v (logs:\n%s)", err, logs.String())
		default:
		}
		return strings.Contains(logs.String(), "controller running")
	})
	if !strings.Contains(logs.String(), `"adapter registered"`) || !strings.Contains(logs.String(), "adapter=responses") {
		t.Fatalf("the responses adapter was never registered from its valid profile (logs:\n%s)", logs.String())
	}
	if strings.Contains(logs.String(), "adapter package has not landed") {
		t.Fatalf("responses is still reported unimplemented (logs:\n%s)", logs.String())
	}
	if !strings.Contains(logs.String(), "readiness=chat_ready") && !strings.Contains(logs.String(), "readiness=task_ready") {
		t.Fatalf("readiness never reached chat_ready with a valid responses adapter registered (logs:\n%s)", logs.String())
	}
	cancel()
	if err := awaitExit(t, done, "context cancellation", logs); err != nil {
		t.Fatalf("serve = %v on shutdown", err)
	}
}

// TestServeAttachesTheRealVerifierNotAStub is the other half of P24's
// required test 1: the collaborator superviseController actually attaches
// as Collaborators.Verifier is internal/execution.NewVerifier's real
// construction -- proven not by a type check alone (a type check would
// pass for a real construction that had been silently gutted) but by
// exercising genuinely independent behavior: an artifact_digest check
// against a real blob this test stages itself, through the exact same
// contract.BlobStore the running server uses (platform.Open needs no
// module registry -- see this file's KNOWN BLOCKER note; it is
// independent of the registry defect and opens safely alongside a running
// serve, exactly as this card's connection-setup helper already relies on
// -- internal/platform/platform.go:88-91). A stub verifier would either
// reject everything or accept everything regardless of content; the real
// one must pass a correct digest and fail an incorrect one.
func TestServeAttachesTheRealVerifierNotAStub(t *testing.T) {
	cfg := serveConfig(t)
	h := openTestInstallation(t, cfg)
	bootstrapInstallation(t, h)
	h.close()

	attached := make(chan controller.Collaborators, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logs := &lockedBuffer{}
	log := newLogger(logs, slog.LevelInfo)
	listening := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runServe(ctx, cfg, log, serveOptions{
			pollInterval: 20 * time.Millisecond,
			listening:    func() { close(listening) },
			afterAttach:  func(c controller.Collaborators) { attached <- c },
		})
	}()
	select {
	case <-listening:
	case err := <-done:
		t.Fatalf("serve ended before listening: %v", err)
	case <-time.After(startupBudget):
		t.Fatal("serve did not start listening")
	}

	var collab controller.Collaborators
	select {
	case collab = <-attached:
	case err := <-done:
		t.Fatalf("serve exited before attaching collaborators: %v (logs:\n%s)", err, logs.String())
	case <-time.After(startupBudget):
		t.Fatal("controller never attached its collaborators")
	}
	if collab.Verifier == nil {
		t.Fatal("Collaborators.Verifier is nil; no trusted verifier was attached")
	}
	if collab.Operator == nil {
		t.Fatal("Collaborators.Operator is nil; no worker operator was attached")
	}
	if collab.Jobs == nil {
		t.Fatal("Collaborators.Jobs is nil; no local job runners were attached")
	}

	// Independently stage real content through the same platform blob
	// store the server uses, entirely outside the server process.
	plat, err := platform.Open(platform.Config{StateDir: cfg.StateDir, CredentialBackend: cfg.CredentialBackend, MasterKeyRef: cfg.MasterKeyRef})
	if err != nil {
		t.Fatalf("opening the platform blob store: %v", err)
	}
	defer func() { _ = plat.Close() }()
	content := []byte("p24 real-verifier proof " + time.Now().String())
	stagingRef, digest, _, err := plat.Blobs().Stage(context.Background(), bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatalf("staging: %v", err)
	}
	if err := plat.Blobs().Publish(context.Background(), stagingRef, digest); err != nil {
		t.Fatalf("publish: %v", err)
	}

	jobID, taskID, attemptID := contract.NewID(), contract.NewID(), contract.NewID()
	codeDigest := syntheticDigest(t, "verifier-code")
	requestDoc := func(expectedDigest contract.Digest) []byte {
		req := map[string]any{
			"schema": "zatiti.verification-request/v1", "job_id": jobID, "task_id": taskID, "attempt_id": attemptID,
			"scope":             map[string]any{"installation_id": string(contract.NewID())},
			"acceptance_digest": syntheticDigest(t, "acceptance"),
			"profile": map[string]any{
				"schema": "zatiti.verifier-profile/v1", "kind": "artifact_contract", "id": "p24-verifier-core", "version": "1.0.0",
				"code_digest": codeDigest, "supported_checks": []string{"digest"}, "max_bytes": 1048576, "timeout_seconds": 60,
				"capability_evidence": capabilityEvidenceStub(t),
			},
			"sealed_inputs": []any{}, "outputs": []any{},
			"expected_observations": []any{map[string]any{
				"check_id": "out-digest", "kind": "artifact_digest", "expected": "pass", "expected_digest": expectedDigest,
			}},
			"deadline": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		}
		raw, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("marshal verification request: %v", err)
		}
		return raw
	}

	passResult, err := collab.Verifier.Verify(context.Background(), contract.Verification{Request: requestDoc(digest)})
	if err != nil {
		t.Fatalf("Verify (correct digest): %v", err)
	}
	var passDoc struct {
		Status      string `json:"status"`
		Independent bool   `json:"independent"`
	}
	if err := json.Unmarshal(passResult.Document, &passDoc); err != nil {
		t.Fatalf("decoding verification result: %v", err)
	}
	if passDoc.Status != "passed" {
		t.Fatalf("Verify against the real staged digest = %q, want passed (document: %s)", passDoc.Status, passResult.Document)
	}
	if !passDoc.Independent {
		t.Fatalf("verification result does not claim independence: %s", passResult.Document)
	}

	wrongDigest := syntheticDigest(t, "definitely not the staged content")
	failResult, err := collab.Verifier.Verify(context.Background(), contract.Verification{Request: requestDoc(wrongDigest)})
	if err != nil {
		t.Fatalf("Verify (wrong digest): %v", err)
	}
	var failDoc struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(failResult.Document, &failDoc); err != nil {
		t.Fatalf("decoding verification result: %v", err)
	}
	if failDoc.Status == "passed" {
		t.Fatal("Verify passed against a digest that does not match the staged content; this is a stub or broken verifier, not a real one")
	}

	cancel()
	if err := awaitExit(t, done, "context cancellation", logs); err != nil {
		t.Fatalf("serve = %v on shutdown", err)
	}
}
