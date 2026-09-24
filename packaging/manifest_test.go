package packaging

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLicenseDigestMatchesRepository(t *testing.T) {
	t.Parallel()
	sum := sha256.Sum256(repositoryLicense(t))
	if got := hex.EncodeToString(sum[:]); got != ApacheLicenseSHA256 {
		t.Fatalf("repository LICENSE digest is %s, the package pins %s", got, ApacheLicenseSHA256)
	}
}

func TestBuildIsDeterministicAndRoundTrips(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "1.2.3", "linux")
	m := f.build(t)

	first, err := Encode(m)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	second, err := Encode(f.build(t))
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("two builds of the same tree encode differently")
	}
	decoded, err := Decode(first)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	again, err := Encode(decoded)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !bytes.Equal(first, again) {
		t.Fatal("decode then encode changed the bytes")
	}

	controller := sha256.Sum256([]byte("synthetic controller 1.2.3"))
	var found bool
	for _, a := range m.Artifacts {
		if a.Path == "bin/zatiti" {
			found = true
			if a.SHA256 != hex.EncodeToString(controller[:]) || a.Size != 26 || a.Mode != "0755" {
				t.Fatalf("controller artifact measured wrongly: %+v", a)
			}
		}
	}
	if !found {
		t.Fatal("controller artifact is missing")
	}
	if m.Protocols.MCP != "2025-11-25" || m.Protocols.OperationAPI != "v1" {
		t.Fatalf("protocols are not the frozen ones: %+v", m.Protocols)
	}
	if err := VerifyTree(m, f.root); err != nil {
		t.Fatalf("VerifyTree on the tree just built: %v", err)
	}
}

func TestBuildWithoutSerenityPinIsPrerequisiteMissing(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "1.2.3", "linux")
	f.input.Serenity = SerenityPin{}
	_, err := Build(f.input)
	perr := wantFault(t, err, CodePrerequisiteMissing)
	if !strings.Contains(perr.Message, "Serenity") {
		t.Fatalf("message does not name the missing prerequisite: %s", perr.Message)
	}
}

func TestBuildRejectsUndistributableTrees(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(t *testing.T, f *fixture)
		code   string
	}{
		{"symlink in tree", func(t *testing.T, f *fixture) {
			if err := os.Symlink("zatiti", filepath.Join(f.root, "bin", "alias")); err != nil {
				t.Fatal(err)
			}
		}, CodeVerificationFailed},
		{"unclassified file", func(t *testing.T, f *fixture) {
			writeFile(t, filepath.Join(f.root, "stray.txt"), []byte("x"), 0o644)
		}, CodeInvalidInput},
		{"declared file missing", func(_ *testing.T, f *fixture) {
			f.input.Kinds["bin/ghost"] = KindDocumentation
		}, CodeNotFound},
		{"group writable binary", func(t *testing.T, f *fixture) {
			if err := os.Chmod(filepath.Join(f.root, "bin", "zatiti"), 0o775); err != nil {
				t.Fatal(err)
			}
		}, CodeInvalidInput},
		{"controller not executable", func(t *testing.T, f *fixture) {
			if err := os.Chmod(filepath.Join(f.root, "bin", "zatiti"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, CodeInvalidInput},
		{"modified license", func(t *testing.T, f *fixture) {
			writeFile(t, filepath.Join(f.root, "LICENSE"), append(repositoryLicense(t), '\n'), 0o644)
		}, CodeVerificationFailed},
		{"relative root", func(_ *testing.T, f *fixture) { f.input.Root = "relative" }, CodeInvalidInput},
		{"sbom omits a licensed component", func(t *testing.T, f *fixture) {
			writeFile(t, filepath.Join(f.root, "sbom.spdx.json"),
				[]byte(`{"spdxVersion":"SPDX-2.3","packages":[{"name":"`+ProjectComponent+`","versionInfo":"1.2.3"}]}`), 0o644)
		}, CodeVerificationFailed},
		{"sbom lists an unlicensed component", func(t *testing.T, f *fixture) {
			writeFile(t, filepath.Join(f.root, "sbom.spdx.json"),
				[]byte(`{"spdxVersion":"SPDX-2.3","packages":[{"name":"`+ProjectComponent+`","versionInfo":"1.2.3"},{"name":"`+testSerenitySource+`","versionInfo":"1.4.0"},{"name":"example.invalid/extra","versionInfo":"0.1.0"}]}`), 0o644)
		}, CodeVerificationFailed},
		{"sbom version disagrees", func(t *testing.T, f *fixture) {
			writeFile(t, filepath.Join(f.root, "sbom.spdx.json"), sbomSPDX("9.9.9"), 0o644)
		}, CodeVerificationFailed},
		{"sbom is not the declared format", func(_ *testing.T, f *fixture) {
			f.input.SBOM.Format = SBOMFormatCycloneDX
		}, CodeInvalidInput},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, "1.2.3", "linux")
			tc.mutate(t, &f)
			_, err := Build(f.input)
			wantCode(t, err, tc.code)
		})
	}
}

func TestBuildAcceptsCycloneDX(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "1.2.3", "linux")
	writeFile(t, filepath.Join(f.root, "sbom.spdx.json"), []byte(`{"bomFormat":"CycloneDX","specVersion":"1.5",`+
		`"metadata":{"component":{"name":"`+ProjectComponent+`","version":"1.2.3"}},`+
		`"components":[{"name":"`+testSerenitySource+`","version":"1.4.0"}]}`), 0o644)
	f.input.SBOM.Format = SBOMFormatCycloneDX
	f.build(t)
}

func TestValidateRejections(t *testing.T) {
	t.Parallel()
	base := newFixture(t, "1.2.3", "darwin").build(t)
	clone := func() Manifest {
		m := base
		m.Artifacts = append([]Artifact{}, base.Artifacts...)
		m.Licenses = append([]LicenseNotice{}, base.Licenses...)
		return m
	}
	tests := []struct {
		name   string
		mutate func(m *Manifest)
		code   string
	}{
		{"schema", func(m *Manifest) { m.Schema = "zatiti.release_manifest/v2" }, CodeInvalidInput},
		{"unknown distribution", func(m *Manifest) { m.Distribution = "server" }, CodeInvalidInput},
		{"desktop section on the controller", func(m *Manifest) { m.Desktop = &DesktopClient{} }, CodeInvalidInput},
		{"desktop bundle in the controller tree", func(m *Manifest) { m.Artifacts[0].Kind = KindDesktopBundle }, CodeInvalidInput},
		{"contract revision", func(m *Manifest) { m.ContractRevision = 1 }, CodeCapabilityUnsupported},
		{"version with build metadata", func(m *Manifest) { m.Version = "1.2.3+build" }, CodeInvalidInput},
		{"version as path", func(m *Manifest) { m.Version = "../1.2.3" }, CodeInvalidInput},
		{"windows target", func(m *Manifest) { m.Target.OS = "windows" }, CodeCapabilityUnsupported},
		{"short revision", func(m *Manifest) { m.SourceRevision = "abc123" }, CodeInvalidInput},
		{"toolchain", func(m *Manifest) { m.Toolchain = "gccgo" }, CodeInvalidInput},
		{"other mcp protocol", func(m *Manifest) { m.Protocols.MCP = "2026-07-28" }, CodeCapabilityUnsupported},
		{"parent traversal", func(m *Manifest) { m.Artifacts[0].Path = "../escape" }, CodeInvalidInput},
		{"absolute path", func(m *Manifest) { m.Artifacts[0].Path = "/etc/passwd" }, CodeInvalidInput},
		{"unsorted artifacts", func(m *Manifest) { m.Artifacts[0], m.Artifacts[1] = m.Artifacts[1], m.Artifacts[0] }, CodeInvalidInput},
		{"reserved manifest path", func(m *Manifest) {
			m.Artifacts = append(m.Artifacts, Artifact{Path: ManifestFileName, Kind: KindDocumentation, SHA256: m.Artifacts[0].SHA256, Mode: "0644"})
		}, CodeInvalidInput},
		{"uppercase digest", func(m *Manifest) { m.Artifacts[0].SHA256 = strings.ToUpper(m.Artifacts[0].SHA256) }, CodeInvalidInput},
		{"setuid mode", func(m *Manifest) { m.Artifacts[0].Mode = "4755" }, CodeInvalidInput},
		{"two controllers", func(m *Manifest) {
			for i := range m.Artifacts {
				if m.Artifacts[i].Kind == KindSerenityRuntime {
					m.Artifacts[i].Kind = KindControllerBinary
				}
			}
		}, CodeInvalidInput},
		{"project license entry missing", func(m *Manifest) { m.Licenses = m.Licenses[1:] }, CodeInvalidInput},
		{"project license entry for another version", func(m *Manifest) { m.Licenses[0].Version = "1.0.0" }, CodeInvalidInput},
		{"notice outside tree", func(m *Manifest) { m.Licenses[0].Notice = "missing.NOTICE" }, CodeInvalidInput},
		{"profile without evidence", func(m *Manifest) {
			m.Profiles = []Profile{{Adapter: "responses", Name: "default", Version: "1", Evidence: "bin/zatiti"}}
		}, CodeInvalidInput},
		{"notarization claim without evidence", func(m *Manifest) {
			m.Attestations = []Attestation{{Kind: AttestationNotarization, Subject: "bin/zatiti", Evidence: "missing.json"}}
		}, CodeInvalidInput},
		{"attestation evidences itself", func(m *Manifest) {
			m.Attestations = []Attestation{{Kind: AttestationQualification, Subject: "evidence/serenity.json", Evidence: "evidence/serenity.json"}}
		}, CodeInvalidInput},
		{"unknown attestation kind", func(m *Manifest) {
			m.Attestations = []Attestation{{Kind: "published", Subject: "bin/zatiti", Evidence: "evidence/serenity.json"}}
		}, CodeInvalidInput},
		{"keychain helper with relative path", func(m *Manifest) { m.SecureHelper.Path = "security" }, CodeInvalidInput},
		{"headless helper with a path", func(m *Manifest) {
			m.SecureHelper = SecureHelper{Kind: HelperHeadlessMasterKey, Path: "/keys/master"}
		}, CodeInvalidInput},
		{"partial serenity pin", func(m *Manifest) { m.Serenity.Revision = "" }, CodeInvalidInput},
		{"serenity runtime is not a runtime", func(m *Manifest) { m.Serenity.Runtime = "bin/zatiti" }, CodeInvalidInput},
		{"serenity without evidence", func(m *Manifest) { m.Serenity.Evidence = "LICENSE" }, CodeInvalidInput},
		{"serenity license entry disagrees", func(m *Manifest) { m.Serenity.License = "Apache-2.0" }, CodeInvalidInput},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := clone()
			tc.mutate(&m)
			wantCode(t, m.Validate(), tc.code)
			_, err := Encode(m)
			wantCode(t, err, tc.code)
		})
	}
}

func TestKeychainHelperIsDarwinOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "1.2.3", "linux")
	f.input.SecureHelper = SecureHelper{Kind: HelperOSKeychain, Path: "/usr/bin/security"}
	_, err := Build(f.input)
	wantCode(t, err, CodeCapabilityUnsupported)
}

func TestAttestationWithEvidenceIsAccepted(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "1.2.3", "darwin")
	f.input.Attestations = append(f.input.Attestations, Attestation{Kind: AttestationQualification, Subject: "serenity/serenity", Evidence: "evidence/serenity.json"})
	f.input.Profiles = []Profile{{Adapter: "github", Name: "default", Version: "1", Evidence: "evidence/serenity.json"}}
	f.build(t)
}

func TestDecodeIsStrict(t *testing.T) {
	t.Parallel()
	valid, err := Encode(newFixture(t, "1.2.3", "linux").build(t))
	if err != nil {
		t.Fatal(err)
	}
	text := string(valid)
	tests := []struct {
		name string
		data string
	}{
		{"unknown field", strings.Replace(text, `"schema":`, `"published":true,"schema":`, 1)},
		{"duplicate key", strings.Replace(text, `"schema":`, `"version":"0.0.1","schema":`, 1)},
		{"nested duplicate key", strings.Replace(text, `"arch":`, `"os":"linux","arch":`, 1)},
		{"trailing data", text + "{}"},
		{"not json", "schema: nope"},
		{"oversized", text + strings.Repeat(" ", maxManifestBytes)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Decode([]byte(tc.data))
			wantCode(t, err, CodeInvalidInput)
		})
	}
}

func TestVerifyTreeReportsEveryDefect(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "1.2.3", "linux")
	m := f.build(t)

	writeFile(t, filepath.Join(f.root, "bin", "zatiti"), []byte("tampered controller bytes!"), 0o755) // same size
	if err := os.Chmod(filepath.Join(f.root, "serenity", "serenity"), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.root, "licenses", "serenity.NOTICE")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.root, "bin", "implant"), []byte("x"), 0o755)
	if err := os.Symlink("/", filepath.Join(f.root, "escape")); err != nil {
		t.Fatal(err)
	}
	// The manifest and signature files are part of the tree by name only.
	writeFile(t, filepath.Join(f.root, ManifestFileName), []byte("{}"), 0o644)

	perr := wantFault(t, VerifyTree(m, f.root), CodeVerificationFailed)
	wantFinding(t, perr, "bin/zatiti", "digest")
	wantFinding(t, perr, "serenity/serenity", "permissions")
	wantFinding(t, perr, "licenses/serenity.NOTICE", "missing")
	wantFinding(t, perr, "bin/implant", "not listed")
	wantFinding(t, perr, "escape", "symlink")
	if len(perr.Findings) != 5 {
		t.Fatalf("got %d findings, want 5: %+v", len(perr.Findings), perr.Findings)
	}
	if strings.Contains(perr.Error(), f.root) {
		t.Fatal("the error message leaks an absolute path")
	}
}

func TestErrorsNeverRenderTheirCause(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "absent")
	_, err := Build(BuildInput{Root: missing})
	perr := wantFault(t, err, CodeNotFound)
	if strings.Contains(perr.Error(), missing) {
		t.Fatalf("message leaks the path: %s", perr.Error())
	}
	if perr.Unwrap() == nil {
		t.Fatal("the cause must stay reachable for programmatic inspection")
	}
	if Code(nil) != "" || Code(os.ErrNotExist) != CodeInternalError {
		t.Fatal("Code maps nil or foreign errors wrongly")
	}
}
