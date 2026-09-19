package packaging

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testRevision         = "0123456789abcdef0123456789abcdef01234567"
	testSerenityRevision = "fedcba9876543210fedcba9876543210fedcba98"
	testSerenitySource   = "example.invalid/serenity"
)

// fixture is a staged distribution tree built from synthetic files. The
// Serenity pin is synthetic: it proves the pin's shape, not any real
// Serenity release.
type fixture struct {
	root  string
	input BuildInput
}

func writeFile(t *testing.T, path string, content []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func repositoryLicense(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "LICENSE"))
	if err != nil {
		t.Fatalf("reading the repository license: %v", err)
	}
	return raw
}

func sbomSPDX(version string) []byte {
	return []byte(`{"spdxVersion":"SPDX-2.3","packages":[` +
		`{"name":"` + ProjectComponent + `","versionInfo":"` + version + `"},` +
		`{"name":"` + testSerenitySource + `","versionInfo":"1.4.0"}]}`)
}

func newFixture(t *testing.T, version, goos string) fixture {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "bin", "zatiti"), []byte("synthetic controller "+version), 0o755)
	writeFile(t, filepath.Join(root, "serenity", "serenity"), []byte("synthetic serenity runtime"), 0o755)
	writeFile(t, filepath.Join(root, "serenity", "read-facade"), []byte("synthetic read facade"), 0o755)
	writeFile(t, filepath.Join(root, "LICENSE"), repositoryLicense(t), 0o644)
	writeFile(t, filepath.Join(root, "licenses", "serenity.NOTICE"), []byte("synthetic notice"), 0o644)
	writeFile(t, filepath.Join(root, "sbom.spdx.json"), sbomSPDX(version), 0o644)
	writeFile(t, filepath.Join(root, "evidence", "serenity.json"), []byte(`{"synthetic":true}`), 0o644)

	helper := SecureHelper{Kind: HelperHeadlessMasterKey}
	if goos == "darwin" {
		helper = SecureHelper{Kind: HelperOSKeychain, Path: "/usr/bin/security"}
	}
	return fixture{root: root, input: BuildInput{
		Root:           root,
		Distribution:   DistributionController,
		Version:        version,
		Target:         Target{OS: goos, Arch: "arm64"},
		SourceRevision: testRevision,
		Toolchain:      "go1.26.2",
		Kinds: map[string]string{
			"bin/zatiti":               KindControllerBinary,
			"serenity/serenity":        KindSerenityRuntime,
			"serenity/read-facade":     KindSerenityReadFacade,
			"LICENSE":                  KindLicense,
			"licenses/serenity.NOTICE": KindNotice,
			"sbom.spdx.json":           KindSBOM,
			"evidence/serenity.json":   KindAttestationEvidence,
		},
		Licenses: []LicenseNotice{
			{Component: ProjectComponent, Version: version, SPDX: ProjectLicense, Notice: LicensePath},
			{Component: testSerenitySource, Version: "1.4.0", SPDX: "MIT", Notice: "licenses/serenity.NOTICE"},
		},
		SBOM: SBOMRef{Format: SBOMFormatSPDX, Path: "sbom.spdx.json"},
		Serenity: SerenityPin{
			Source: testSerenitySource, Version: "1.4.0", Revision: testSerenityRevision,
			License: "MIT", InterfaceVersion: "v1",
			Runtime: "serenity/serenity", ReadFacade: "serenity/read-facade",
			Evidence: "evidence/serenity.json",
		},
		SecureHelper: helper,
	}}
}

func (f fixture) build(t *testing.T) Manifest {
	t.Helper()
	m, err := Build(f.input)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return m
}

func wantFault(t *testing.T, err error, code string) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("got nil error, want %s", code)
	}
	var perr *Error
	if !errors.As(err, &perr) {
		t.Fatalf("got %T (%v), want *Error with %s", err, err, code)
	}
	if perr.Code != code {
		t.Fatalf("got code %s (%s), want %s", perr.Code, perr.Message, code)
	}
	return perr
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	_ = wantFault(t, err, code)
}

func wantFinding(t *testing.T, perr *Error, path, problemPart string) {
	t.Helper()
	for _, f := range perr.Findings {
		if f.Path == path && strings.Contains(f.Problem, problemPart) {
			return
		}
	}
	t.Fatalf("no finding for %s containing %q in %+v", path, problemPart, perr.Findings)
}

// recordingManager records service actions and what the active release link
// pointed at when each one ran.
type recordingManager struct {
	layout Layout
	calls  []string
	fail   map[string]error
}

func (r *recordingManager) Do(_ context.Context, a ServiceAction) error {
	target, _ := os.Readlink(r.layout.Current)
	call := a.Verb + " " + a.Label + " @" + filepath.ToSlash(target)
	r.calls = append(r.calls, call)
	return r.fail[a.Verb]
}

func testLayout(t *testing.T, goos string) Layout {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home", "operator")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	l, err := NewLayout(LayoutInput{OS: goos, Home: home, StateDir: filepath.Join(home, "zatiti-state")})
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	return l
}

// controllerSpec is the controller launcher for a test layout. Temporary
// directories are deep enough that <state dir>/zatiti.sock exceeds the
// controller's 104-byte socket bound, so the spec names a short socket path
// explicitly, as an operator with a deep state directory must.
func controllerSpec(l Layout, m Manifest) ServiceSpec {
	return ServiceSpec{
		Manager: l.Manager, Role: RoleController, Label: ControllerLabel,
		Description: "Zatiti controller",
		Executable:  l.ControllerExecutable(m),
		Arguments:   []string{"serve", "--state-dir", l.StateDir, "--socket", "/tmp/zatiti-test.sock"},
		Owns:        l.StateDir,
	}
}

func encodeRawSignature(priv ed25519.PrivateKey, digest string) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(digest)))
}
