package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
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

// Responses providers are constructed from the durable profile pinned to an
// individual dispatch. A process-global responses.json must not register or
// select a provider; the trusted per-dispatch factory accepts the evidence-free
// v2 draft only for its bounded qualification probe.
func TestResponsesAdapterFactoryAcceptsQualificationDraftOnDemand(t *testing.T) {
	const endpoint = "https://openrouter.ai/api/v1/responses"
	const model = "z-ai/glm-flash-latest"
	connectionID := contract.NewID()
	draft, err := json.Marshal(map[string]any{
		"schema": "zatiti.responses/v2", "endpoint": endpoint, "model": model,
		"connection_id": connectionID, "max_input_tokens": 32768,
		"max_output_tokens": 2048, "max_response_bytes": 65536,
		"timeout_seconds": 30, "currency": "USD",
		"input_rate":  map[string]any{"numerator_micro_units": 500000, "denominator_units": 1000000, "unit": "input_token"},
		"output_rate": map[string]any{"numerator_micro_units": 1500000, "denominator_units": 1000000, "unit": "output_token"},
		"enforcement": map[string]any{
			"cost": "enforced", "disclosure": "enforced",
			"maximum_cost":          map[string]any{"currency": "USD", "micro_units": 20000},
			"provider_destinations": []string{endpoint},
		},
		"provider": "openrouter", "session_mode": "stateless",
		"routing": map[string]any{
			"only": []string{model}, "allow_fallbacks": false, "require_parameters": true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	deps := contract.AdapterDependencies{
		HTTP:  &http.Client{Transport: &http.Transport{Proxy: nil}},
		Clock: adapterFactoryTestClock{},
	}
	adapter, err := responsesAdapterFactory(deps)(draft)
	if err != nil {
		t.Fatalf("constructing the dispatch-pinned Responses adapter: %v", err)
	}
	if adapter.Name() != "responses" {
		t.Fatalf("factory constructed %q, want responses", adapter.Name())
	}
}

type adapterFactoryTestClock struct{}

func (adapterFactoryTestClock) Now() time.Time { return time.Now().UTC() }

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
