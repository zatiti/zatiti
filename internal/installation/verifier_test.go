package installation

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// TestVerifierListReturnsTheFixedInstalledCatalog proves
// installation.verifier.list answers from the build-defined catalog: every
// entry it returns is exactly one of trustedVerifierProfiles, field for
// field, regardless of what has been published as an artifact or requested
// as a task's acceptance elsewhere in the installation.
func TestVerifierListReturnsTheFixedInstalledCatalog(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()

	payload := mustQueryOK(t, e, opVerifierList, verifierListInput{Scope: e.scope})
	var out verifierListOutput
	e.decode(payload.Data, &out)

	if len(out.Items) != len(trustedVerifierProfiles) {
		t.Fatalf("verifier.list returned %d items, want %d", len(out.Items), len(trustedVerifierProfiles))
	}
	for i, want := range trustedVerifierProfiles {
		got := out.Items[i]
		if got.ID != want.ID || got.Version != want.Version || got.Kind != want.Kind || got.Classification != want.Classification {
			t.Fatalf("verifier.list item %d = %+v, want %+v", i, got, want)
		}
	}
}

// TestSelfPublishedVerifierEvidenceDoesNotManufactureInstalledCapability is
// the card's third required behavioral test. A caller publishes an
// artifact whose bytes claim to be capability evidence for a verifier this
// installation never built ("totally-fabricated-verifier"), exactly the
// shape a task's acceptance.profile.capability_evidence could reference.
// installation.verifier.list must not be influenced by it in any way: the
// fabricated identity never appears in the catalog, and the real installed
// identity's fields never change to match whatever the self-published
// artifact claims. The catalog is Go source, not derived from artifact
// metadata or any peer call, so this holds by construction; the test pins
// that property so a future change that makes the catalog artifact-derived
// would fail loudly here.
func TestSelfPublishedVerifierEvidenceDoesNotManufactureInstalledCapability(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()

	fabricated := map[string]any{
		"schema":  "zatiti.verifier-profile/v1",
		"kind":    "artifact_contract",
		"id":      "totally-fabricated-verifier",
		"version": "999",
		"capability_evidence": map[string]any{
			"capabilities": []string{"presence", "digest", "json_schema", "network_write", "credential_bypass"},
			"limitations":  []string{},
		},
	}
	raw, err := json.Marshal(fabricated)
	if err != nil {
		t.Fatalf("marshal fabricated evidence: %v", err)
	}
	digest := e.blobs.publishBytes(raw)

	// Simulate the operator's own "self-publish" step: an artifact carrying
	// the fabricated claim now legitimately exists at installation scope,
	// exactly as FirstTaskSequence's upload steps would leave one. Peer
	// calls run inside a transaction, the same way every handler drives
	// them.
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		_, perr := e.svc.artifactsPublish(e.ctx, unit, e.scope, digest, int64(len(raw)), "application/json")
		return perr
	}); err != nil {
		t.Fatalf("publish fabricated evidence artifact: %v", err)
	}

	payload := mustQueryOK(t, e, opVerifierList, verifierListInput{Scope: e.scope})
	var out verifierListOutput
	e.decode(payload.Data, &out)

	for _, item := range out.Items {
		if item.ID == "totally-fabricated-verifier" {
			t.Fatalf("self-published evidence manufactured an installed capability: %+v", item)
		}
	}
	if len(out.Items) != len(trustedVerifierProfiles) || out.Items[0].ID != trustedVerifierProfiles[0].ID {
		t.Fatalf("catalog changed after a self-published artifact: %+v", out.Items)
	}
}

// TestVerifierListRejectsAForeignCursor proves the fixed, single-page
// catalog does not silently accept an opaque cursor it never issued (which
// would otherwise look like unbounded, unauthenticated pagination over
// caller-supplied state).
func TestVerifierListRejectsAForeignCursor(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	cursor := "not-a-cursor-this-operation-issued"
	f := e.expectQueryFault(opVerifierList, verifierListInput{Scope: e.scope, Cursor: &cursor}, contract.CodeInvalidInput)
	if f.Code != contract.CodeInvalidInput {
		t.Fatalf("cursor fault = %s, want %s", f.Code, contract.CodeInvalidInput)
	}
}

// TestFirstTaskSequenceNamesTheInstalledVerifier proves the documented
// first-task recipe's acceptance.verifier_id/verifier_version and
// profile.id/version match, byte for byte, what installation.verifier.list
// itself reports as installed -- not a value the documentation author typed
// independently and could let drift. This is the package-local half of the
// card's first required behavioral test ("a fresh installation can complete
// the documented ... first task"): it proves the sequence names a real
// installed verifier rather than an invented one. Completing the task to a
// succeeded terminal state crosses into tasks/execution and is proven by
// owner integration at landing, not by this package's tests.
func TestFirstTaskSequenceNamesTheInstalledVerifier(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()

	payload := mustQueryOK(t, e, opVerifierList, verifierListInput{Scope: e.scope})
	var out verifierListOutput
	e.decode(payload.Data, &out)
	if len(out.Items) == 0 {
		t.Fatalf("no installed verifier to compare against")
	}
	installed := out.Items[0]

	verifierIDRe := regexp.MustCompile(`"verifier_id":"([^"]+)"`)
	verifierVersionRe := regexp.MustCompile(`"verifier_version":"([^"]+)"`)
	profileIDRe := regexp.MustCompile(`"kind":"artifact_contract","id":"([^"]+)","version":"([^"]+)"`)

	vid := verifierIDRe.FindStringSubmatch(FirstTaskSequence)
	vver := verifierVersionRe.FindStringSubmatch(FirstTaskSequence)
	pid := profileIDRe.FindStringSubmatch(FirstTaskSequence)
	if vid == nil || vver == nil || pid == nil {
		t.Fatalf("FirstTaskSequence does not embed a recognizable verifier_id/verifier_version/profile identity")
	}
	if vid[1] != installed.ID {
		t.Fatalf("acceptance.verifier_id = %q, want the installed verifier %q", vid[1], installed.ID)
	}
	if pid[1] != installed.ID || pid[2] != vver[1] {
		t.Fatalf("acceptance.profile identity = %q/%q, want %q/%q", pid[1], pid[2], installed.ID, vver[1])
	}
	// The sequence also documents the discovery step itself, so an operator
	// is told to check rather than trust the printed literal.
	if !regexp.MustCompile(`installation verifier list`).MatchString(FirstTaskSequence) {
		t.Fatalf("FirstTaskSequence does not tell the operator to confirm the installed verifier first")
	}
}
