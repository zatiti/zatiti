package serenity

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func newFromProfile(raw json.RawMessage) error {
	_, err := New(contract.AdapterDependencies{}, raw)
	return err
}

// editRaw edits a JSON document as a generic map without decaying its
// integers through float64, so a test breaks exactly the field it names.
func editRaw(t *testing.T, raw json.RawMessage, mutate func(map[string]any)) json.RawMessage {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("decode document: %v", err)
	}
	mutate(m)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("encode document: %v", err)
	}
	return out
}

func TestTruthfulProfileLoads(t *testing.T) {
	profile, err := loadProfile(profileJSON(t, nil))
	if err != nil {
		t.Fatalf("loadProfile: %v", err)
	}
	if got := profile.Brains[testBrainID].Endpoint; got != testEndpoint {
		t.Fatalf("brain endpoint = %q, want %q", got, testEndpoint)
	}
	if profile.Digest == "" || profile.Digest != profile.CapabilityEvidence.ProfileDigest {
		t.Fatalf("profile digest %q does not equal bound evidence digest %q", profile.Digest, profile.CapabilityEvidence.ProfileDigest)
	}
}

func TestAdvisoryEnforcementIsNotAnOverclaim(t *testing.T) {
	raw := profileJSON(t, func(w *wireSerenityProfile) {
		w.Enforcement.Cost = "advisory"
		w.Enforcement.Disclosure = "advisory"
	})
	if err := newFromProfile(raw); err != nil {
		t.Fatalf("advisory enforcement claims no upstream guarantee and must load: %v", err)
	}
}

// TestNewNeedsNoDependency proves the adapter reaches nothing outside
// itself: it constructs and serves with every dependency absent.
func TestNewNeedsNoDependency(t *testing.T) {
	a, err := New(contract.AdapterDependencies{}, profileJSON(t, nil))
	if err != nil {
		t.Fatalf("New with no dependencies: %v", err)
	}
	d := testDispatch(t, rememberAction(testBrainID))
	_, err = a.Invoke(context.Background(), d)
	requireFault(t, err, contract.CodeCapabilityUnsupported)
	_, err = a.Reconcile(context.Background(), d)
	requireFault(t, err, contract.CodeCapabilityUnsupported)
}

func TestProfileSchemaViolationsRejected(t *testing.T) {
	cases := map[string]func(map[string]any){
		"unknown field":     func(m map[string]any) { m["endpoint_override"] = "http://127.0.0.1:1/mcp" },
		"wrong schema":      func(m map[string]any) { m["schema"] = "zatiti.serenity/v1" },
		"missing freshness": func(m map[string]any) { delete(m, "freshness") },
		"zero timeout":      func(m map[string]any) { m["timeout_seconds"] = 0 },
		"unknown operation": func(m map[string]any) { m["supported_operations"] = []string{"synthesize"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			requireFault(t, newFromProfile(editRaw(t, profileJSON(t, nil), mutate)), contract.CodeInvalidInput)
		})
	}
}

func TestProfileDigestMustBindProfile(t *testing.T) {
	// Editing the profile after its evidence was issued breaks the binding.
	raw := editRaw(t, profileJSON(t, nil), func(m map[string]any) { m["timeout_seconds"] = 31 })
	f := mustFault(t, newFromProfile(raw), contract.CodeInvalidInput)
	if !strings.Contains(f.Message, "does not bind this exact profile") {
		t.Fatalf("fault does not name the digest binding: %s", f.Message)
	}
}

func TestProfileMustNameThePinnedSource(t *testing.T) {
	cases := map[string]func(*wireSerenityProfile){
		"commit":            func(w *wireSerenityProfile) { w.Commit = "a27c43e5a5eff306e25420a0fdb7aad6a083f85a" },
		"version":           func(w *wireSerenityProfile) { w.Version = "v0.1.1" },
		"source_revision":   func(w *wireSerenityProfile) { w.CapabilityEvidence.SourceRevision = "main" },
		"protocol_revision": func(w *wireSerenityProfile) { w.CapabilityEvidence.ProtocolRevision = "memory_verbs/2" },
		"adapter_version":   func(w *wireSerenityProfile) { w.CapabilityEvidence.AdapterVersion = "zatiti-serenity-adapter/0" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := mustFault(t, newFromProfile(profileJSON(t, mutate)), contract.CodeCapabilityUnsupported)
			if !strings.Contains(f.Message, name) {
				t.Fatalf("fault does not name the unpinned field %q: %s", name, f.Message)
			}
		})
	}
}

func TestProfileOverclaimsRejected(t *testing.T) {
	cases := []struct {
		field, gap string
		mutate     func(*wireSerenityProfile)
	}{
		{"enforcement.cost", gapCostBound, func(w *wireSerenityProfile) { w.Enforcement.Cost = "enforced" }},
		{"enforcement.disclosure", gapDisclosureDestinations, func(w *wireSerenityProfile) { w.Enforcement.Disclosure = "enforced" }},
		{"command_status_lookup.mode", gapCommandStatusLookup, func(w *wireSerenityProfile) { w.CommandStatusLookup.Mode = "authoritative" }},
		{"command_status_lookup.mode", gapCommandStatusLookup, func(w *wireSerenityProfile) { w.CommandStatusLookup.Mode = "non_authoritative" }},
		{"command_status_lookup.command_identity_supported", gapCommandIdentity, func(w *wireSerenityProfile) { w.CommandStatusLookup.CommandIdentitySupported = true }},
		{"command_status_lookup.retention_seconds", gapIdempotentReplay, func(w *wireSerenityProfile) { w.CommandStatusLookup.RetentionSeconds = 3600 }},
		{"freshness.source_revision_supported", gapSourceRevision, func(w *wireSerenityProfile) { w.Freshness.SourceRevisionSupported = true }},
		{"freshness.index_revision_supported", gapIndexRevision, func(w *wireSerenityProfile) { w.Freshness.IndexRevisionSupported = true }},
		{"freshness.minimum_freshness_enforceable", gapMinimumFreshness, func(w *wireSerenityProfile) { w.Freshness.MinimumFreshnessEnforceable = true }},
		{"freshness.read_facade", gapReadFacade, func(w *wireSerenityProfile) { w.Freshness.ReadFacade = "qualified_local" }},
		{"backup_revision_protocol.mode", gapRevisionExport, func(w *wireSerenityProfile) { w.BackupRevisionProtocol.Mode = "qualified_pinned_revision" }},
		{"backup_revision_protocol.immutable_revision_export", gapRevisionExport, func(w *wireSerenityProfile) { w.BackupRevisionProtocol.ImmutableRevisionExport = true }},
		{"backup_revision_protocol.restore_supported", gapRestore, func(w *wireSerenityProfile) { w.BackupRevisionProtocol.RestoreSupported = true }},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			f := mustFault(t, newFromProfile(profileJSON(t, tc.mutate)), contract.CodeCapabilityUnsupported)
			if !strings.Contains(f.Message, tc.field) || !strings.Contains(f.Message, tc.gap) {
				t.Fatalf("fault must name field %q and gap %q: %s", tc.field, tc.gap, f.Message)
			}
		})
	}
}

func TestProfileCannotEnableAnyOperation(t *testing.T) {
	for kind := range allKinds(testBrainID) {
		t.Run(kind, func(t *testing.T) {
			raw := profileJSON(t, func(w *wireSerenityProfile) { w.SupportedOperations = []string{kind} })
			f := mustFault(t, newFromProfile(raw), contract.CodeCapabilityUnsupported)
			if !strings.Contains(f.Message, kind) || !strings.Contains(f.Message, gapSingleRequestCallPath) {
				t.Fatalf("fault must name kind %q and the blocking gap: %s", kind, f.Message)
			}
		})
	}
}

func TestProfileCannotListQualifiedCapabilities(t *testing.T) {
	raw := profileJSON(t, func(w *wireSerenityProfile) { w.CapabilityEvidence.Capabilities = []string{"recall"} })
	requireFault(t, newFromProfile(raw), contract.CodeCapabilityUnsupported)
}

func TestBrainMappingsValidated(t *testing.T) {
	second := wireBrainMapping{BrainID: testOtherBrainID, Endpoint: "http://127.0.0.1:54218/mcp",
		RootRef: "brain:org-1", WriterOwner: testWriterOwner, Classification: "internal"}
	cases := map[string]func(*wireSerenityProfile){
		"duplicate brain": func(w *wireSerenityProfile) {
			dup := second
			dup.BrainID = testBrainID
			w.BrainMappings = append(w.BrainMappings, dup)
		},
		"shared endpoint": func(w *wireSerenityProfile) {
			dup := second
			dup.Endpoint = testEndpoint
			w.BrainMappings = append(w.BrainMappings, dup)
		},
		"wrong route": func(w *wireSerenityProfile) { w.BrainMappings[0].Endpoint = "http://127.0.0.1:54217/memory" },
		"bad scheme":  func(w *wireSerenityProfile) { w.BrainMappings[0].Endpoint = "file:///mcp" },
		"no host":     func(w *wireSerenityProfile) { w.BrainMappings[0].Endpoint = "http:///mcp" },
		"query":       func(w *wireSerenityProfile) { w.BrainMappings[0].Endpoint = testEndpoint + "?brain=other" },
		"not a URL":   func(w *wireSerenityProfile) { w.BrainMappings[0].Endpoint = "http://[::1" },
		"user and pass": func(w *wireSerenityProfile) {
			w.BrainMappings[0].Endpoint = "http://svc:" + testToken + "@127.0.0.1:54217/mcp"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := mustFault(t, newFromProfile(profileJSON(t, mutate)), contract.CodeInvalidInput)
			if strings.Contains(f.Message, testToken) {
				t.Fatalf("fault leaks the credential embedded in the endpoint: %s", f.Message)
			}
		})
	}

	// Two brains on two endpoints is the supported shape.
	raw := profileJSON(t, func(w *wireSerenityProfile) { w.BrainMappings = append(w.BrainMappings, second) })
	if err := newFromProfile(raw); err != nil {
		t.Fatalf("two brains on distinct endpoints must load: %v", err)
	}
}

func TestHostedBrainMappingIsRecognizedButNotDispatched(t *testing.T) {
	hosted := wireBrainMapping{BrainID: testBrainID, HostedProjectID: "ABCDEFGHIJKLMNOP", ConnectionID: contract.ID("33333333-3333-4333-8333-333333333333"),
		Endpoint: "https://serenity.sire.run/mcp", Classification: "internal"}
	raw := profileJSON(t, func(w *wireSerenityProfile) { w.BrainMappings = []wireBrainMapping{hosted} })
	a, probes := newTestAdapter(t, raw)
	profile := a.(*Adapter).profile
	if got := profile.Brains[testBrainID]; got.HostedProjectID != hosted.HostedProjectID || got.ConnectionID != hosted.ConnectionID {
		t.Fatalf("hosted identity was not retained: %+v", got)
	}
	d := testDispatch(t, recallAction(testBrainID))
	requireFault(t, func() error { _, err := a.Invoke(context.Background(), d); return err }(), contract.CodeCapabilityUnsupported)
	if probes.total() != 0 {
		t.Fatalf("unsupported hosted call made %d outward calls", probes.total())
	}
}

func TestHostedMappingRejectsMismatchedAndDuplicateIdentities(t *testing.T) {
	first := wireBrainMapping{BrainID: testBrainID, HostedProjectID: "ABCDEFGHIJKLMNOP", ConnectionID: contract.ID("33333333-3333-4333-8333-333333333333"),
		Endpoint: "https://serenity.sire.run/mcp", Classification: "internal"}
	second := wireBrainMapping{BrainID: testOtherBrainID, HostedProjectID: "QRSTUVWXYZabcdef", ConnectionID: contract.ID("44444444-4444-4444-8444-444444444444"),
		Endpoint: first.Endpoint, Classification: "restricted"}
	if err := newFromProfile(profileJSON(t, func(w *wireSerenityProfile) { w.BrainMappings = []wireBrainMapping{first, second} })); err != nil {
		t.Fatalf("distinct hosted mappings rejected: %v", err)
	}
	cases := map[string]func(*wireSerenityProfile){
		"same local brain": func(w *wireSerenityProfile) {
			dup := second
			dup.BrainID = first.BrainID
			w.BrainMappings = []wireBrainMapping{first, dup}
		},
		"same hosted project": func(w *wireSerenityProfile) {
			dup := second
			dup.HostedProjectID = first.HostedProjectID
			w.BrainMappings = []wireBrainMapping{first, dup}
		},
		"same connection grant": func(w *wireSerenityProfile) {
			dup := second
			dup.ConnectionID = first.ConnectionID
			w.BrainMappings = []wireBrainMapping{first, dup}
		},
		"hosted and local same brain": func(w *wireSerenityProfile) {
			w.BrainMappings = []wireBrainMapping{first, {BrainID: first.BrainID, Endpoint: testEndpoint, RootRef: "brain:other", WriterOwner: testWriterOwner, Classification: "internal"}}
		},
		"wrong hosted endpoint": func(w *wireSerenityProfile) {
			dup := first
			dup.Endpoint = "https://elsewhere.example/mcp"
			w.BrainMappings = []wireBrainMapping{dup}
		},
		"local fields on hosted mapping": func(w *wireSerenityProfile) {
			dup := first
			dup.RootRef = "brain:other"
			w.BrainMappings = []wireBrainMapping{dup}
		},
		"uuid-shaped hosted project": func(w *wireSerenityProfile) {
			dup := first
			dup.HostedProjectID = string(testBrainID)
			w.BrainMappings = []wireBrainMapping{dup}
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			if err := newFromProfile(profileJSON(t, edit)); err == nil {
				t.Fatal("mismatched mapping accepted")
			}
		})
	}
}
