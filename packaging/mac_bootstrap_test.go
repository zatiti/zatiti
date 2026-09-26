package packaging

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type testMacSource struct {
	data   map[string][]byte
	opened []string
}

func (s *testMacSource) Open(_ context.Context, a MacDeliveryAsset) (io.ReadCloser, error) {
	s.opened = append(s.opened, a.URL)
	b, ok := s.data[a.URL]
	if !ok {
		return nil, errors.New("missing fixture")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

type testMacVerifier struct {
	called int
	fail   bool
	staged MacStagedAssets
	events *[]string
}

func (v *testMacVerifier) Verify(_ context.Context, _ MacReleaseDescriptor, _ MacDownloadPlan, s MacStagedAssets) error {
	v.called++
	recordMacBootstrapEvent(v.events, "verify")
	v.staged = s
	if v.fail {
		return errors.New("unverified package")
	}
	for _, p := range []string{s.Controller, s.Desktop, s.Installer} {
		info, err := os.Stat(p)
		if err != nil || info.Mode().Perm() != 0o600 {
			return errors.New("bad staged asset")
		}
		dir, err := os.Stat(filepath.Dir(p))
		if err != nil || dir.Mode().Perm() != 0o700 {
			return errors.New("bad staging directory")
		}
	}
	return nil
}

type testMacRunner struct {
	installed   bool
	installs    int
	fail        bool
	packagePath string
	events      *[]string
}

type testMacInbox struct {
	called     int
	fail       bool
	tamperPath bool
	events     *[]string
}

func (v *testMacInbox) VerifyInstalled(_ context.Context, home string, _ MacReleaseDescriptor, _ MacDownloadPlan, b MacPkgBinding) (MacStagedAssets, error) {
	v.called++
	recordMacBootstrapEvent(v.events, "inbox")
	if v.fail {
		return MacStagedAssets{}, errors.New("installed inbox tampered")
	}
	dir, err := MacPkgInboxPath(home, b)
	if err != nil {
		return MacStagedAssets{}, err
	}
	if v.tamperPath {
		dir = filepath.Dir(dir)
	}
	return MacStagedAssets{Controller: filepath.Join(dir, "controller.tar.gz"), Desktop: filepath.Join(dir, "desktop.tar.gz")}, nil
}

type testMacMasterKey struct {
	called int
	fail   bool
	events *[]string
}

func (p *testMacMasterKey) Provision(context.Context, string) error {
	p.called++
	recordMacBootstrapEvent(p.events, "provision")
	if p.fail {
		return errors.New("locked Keychain")
	}
	return nil
}

type testMacActivator struct {
	active                  bool
	activates, audits       int
	failActivate, failAudit bool
	failAuditAt             int
	events                  *[]string
}

func (a *testMacActivator) Activate(context.Context, MacReleaseDescriptor, MacDownloadPlan, MacStagedAssets) error {
	a.activates++
	recordMacBootstrapEvent(a.events, "activate")
	if a.failActivate {
		return errors.New("Apply failed")
	}
	a.active = true
	return nil
}
func (a *testMacActivator) Audit(context.Context, MacReleaseDescriptor, MacDownloadPlan) (bool, error) {
	a.audits++
	recordMacBootstrapEvent(a.events, "audit")
	if a.failAudit || a.failAuditAt == a.audits {
		return false, errors.New("active tree corrupt")
	}
	return a.active, nil
}

func (r *testMacRunner) Installed(context.Context, MacReleaseDescriptor, string) (bool, error) {
	return r.installed, nil
}
func (r *testMacRunner) Install(_ context.Context, p string) error {
	r.installs++
	recordMacBootstrapEvent(r.events, "install")
	r.packagePath = p
	if r.fail {
		return errors.New("installer failed")
	}
	r.installed = true
	return nil
}

type testMacWatermark struct {
	n      int64
	digest string
	fail   bool
	events *[]string
}

func (w *testMacWatermark) Lock(context.Context) (func(), error) { return func() {}, nil }
func (w *testMacWatermark) Load(context.Context) (MacAcceptedRelease, error) {
	return MacAcceptedRelease{Sequence: w.n, DeliverySHA256: w.digest}, nil
}
func (w *testMacWatermark) Advance(_ context.Context, a MacAcceptedRelease) error {
	recordMacBootstrapEvent(w.events, "advance")
	if w.fail {
		return errors.New("write failed")
	}
	if a.Sequence <= w.n {
		return errors.New("not monotonic")
	}
	w.n = a.Sequence
	w.digest = a.DeliverySHA256
	return nil
}

func recordMacBootstrapEvent(events *[]string, name string) {
	if events != nil {
		*events = append(*events, name)
	}
}

func bootstrapFixture(t *testing.T) (MacBootstrapInput, *testMacSource, *testMacVerifier, *testMacRunner, *testMacWatermark) {
	t.Helper()
	d, _, _, pub, priv, now := deliveryFixture(t)
	source := &testMacSource{data: map[string][]byte{}}
	for i := range d.Assets {
		payload := []byte("synthetic " + d.Assets[i].Arch + " " + d.Assets[i].Distribution)
		hash := sha256.Sum256(payload)
		d.Assets[i].Size = int64(len(payload))
		d.Assets[i].SHA256 = hex.EncodeToString(hash[:])
		source.data[d.Assets[i].URL] = payload
	}
	raw, err := EncodeMacDelivery(d)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := SignMacDelivery(raw, priv)
	if err != nil {
		t.Fatal(err)
	}
	verifier := &testMacVerifier{}
	runner := &testMacRunner{}
	watermark := &testMacWatermark{n: 11, digest: strings.Repeat("0", 64)}
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	in := MacBootstrapInput{Home: current.HomeDir, StateDir: filepath.Join(current.HomeDir, "Library", "Application Support", "zatiti"), Delivery: raw, Signature: sig, TrustedKeys: []ed25519.PublicKey{pub}, Now: now, Source: source, Verifier: verifier, Runner: runner, InboxVerifier: &testMacInbox{}, MasterKey: &testMacMasterKey{}, Activator: &testMacActivator{}, Watermark: watermark, NativeArch: func(context.Context) (string, error) { return "arm64", nil }}
	return in, source, verifier, runner, watermark
}

func TestMacBootstrapVerifiedOnlyNativeAssetsAndSafeRerun(t *testing.T) {
	in, source, verifier, runner, watermark := bootstrapFixture(t)
	if err := RunMacBootstrap(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if len(source.opened) != 3 || verifier.called != 1 || runner.installs != 1 || watermark.n != 12 {
		t.Fatalf("steps: %d %d %d %d", len(source.opened), verifier.called, runner.installs, watermark.n)
	}
	for _, u := range source.opened {
		if !strings.Contains(u, "-arm64-") {
			t.Fatalf("downloaded wrong arch: %s", u)
		}
	}
	if _, err := os.Stat(runner.packagePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging was not cleaned: %v", err)
	}
	if err := RunMacBootstrap(context.Background(), in); err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if len(source.opened) != 3 || runner.installs != 1 {
		t.Fatal("rerun downloaded or reinstalled")
	}
}

func TestMacBootstrapFailsClosedBeforeActivation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*MacBootstrapInput, *testMacSource, *testMacVerifier, *testMacRunner, *testMacWatermark)
	}{
		{"bad signature", func(in *MacBootstrapInput, _ *testMacSource, _ *testMacVerifier, _ *testMacRunner, _ *testMacWatermark) {
			in.Signature.SHA256 = strings.Repeat("0", 64)
		}},
		{"wrong trust key", func(in *MacBootstrapInput, _ *testMacSource, _ *testMacVerifier, _ *testMacRunner, _ *testMacWatermark) {
			in.TrustedKeys = nil
		}},
		{"downgrade", func(_ *MacBootstrapInput, _ *testMacSource, _ *testMacVerifier, _ *testMacRunner, w *testMacWatermark) {
			w.n = 13
		}},
		{"short download", func(_ *MacBootstrapInput, s *testMacSource, _ *testMacVerifier, _ *testMacRunner, _ *testMacWatermark) {
			for u := range s.data {
				if strings.Contains(u, "arm64-controller") {
					s.data[u] = []byte("short")
				}
			}
		}},
		{"oversize download", func(_ *MacBootstrapInput, s *testMacSource, _ *testMacVerifier, _ *testMacRunner, _ *testMacWatermark) {
			for u, b := range s.data {
				if strings.Contains(u, "arm64-controller") {
					s.data[u] = append(b, 'x')
				}
			}
		}},
		{"hash mismatch", func(_ *MacBootstrapInput, s *testMacSource, _ *testMacVerifier, _ *testMacRunner, _ *testMacWatermark) {
			for u, b := range s.data {
				if strings.Contains(u, "arm64-controller") {
					copy(b, []byte("tampered"))
				}
			}
		}},
		{"package verifier", func(_ *MacBootstrapInput, _ *testMacSource, v *testMacVerifier, _ *testMacRunner, _ *testMacWatermark) {
			v.fail = true
		}},
		{"missing verifier", func(in *MacBootstrapInput, _ *testMacSource, _ *testMacVerifier, _ *testMacRunner, _ *testMacWatermark) {
			in.Verifier = nil
		}},
		{"installer failure", func(_ *MacBootstrapInput, _ *testMacSource, _ *testMacVerifier, r *testMacRunner, _ *testMacWatermark) {
			r.fail = true
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, s, v, r, w := bootstrapFixture(t)
			tc.mutate(&in, s, v, r, w)
			err := RunMacBootstrap(context.Background(), in)
			if err == nil {
				t.Fatal("unsafe bootstrap succeeded")
			}
			if w.n != 11 && tc.name != "downgrade" {
				t.Fatal("watermark advanced on failure")
			}
			if tc.name != "installer failure" && r.installs != 0 {
				t.Fatal("installer ran before verification")
			}
			if r.packagePath != "" {
				if _, e := os.Stat(r.packagePath); !errors.Is(e, os.ErrNotExist) {
					t.Fatal("staging survived failure")
				}
			}
		})
	}
}

func TestMacBootstrapRecoversAfterWatermarkWriteFailure(t *testing.T) {
	in, s, _, r, w := bootstrapFixture(t)
	w.fail = true
	if err := RunMacBootstrap(context.Background(), in); err == nil {
		t.Fatal("watermark failure hidden")
	}
	if !r.installed || r.installs != 1 {
		t.Fatal("fixture did not install before watermark failure")
	}
	opened := len(s.opened)
	w.fail = false
	if err := RunMacBootstrap(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if r.installs != 1 || len(s.opened) != opened || w.n != 12 {
		t.Fatal("rerun did not reconcile installed release")
	}
}

func TestDetectNativeMacArchUnderTranslatedShell(t *testing.T) {
	query := func(_ context.Context, name string) (string, error) {
		if name == "hw.optional.arm64" {
			return "1\n", nil
		}
		return "x86_64\n", nil
	}
	arch, err := detectNativeMacArch(context.Background(), query)
	if err != nil || arch != "arm64" {
		t.Fatalf("Rosetta selection: %q %v", arch, err)
	}
	intel := func(_ context.Context, name string) (string, error) {
		if name == "hw.optional.arm64" {
			return "0\n", nil
		}
		return "x86_64\n", nil
	}
	arch, err = detectNativeMacArch(context.Background(), intel)
	if err != nil || arch != "amd64" {
		t.Fatalf("Intel selection: %q %v", arch, err)
	}
	if _, err := detectNativeMacArch(context.Background(), func(context.Context, string) (string, error) { return "bogus", nil }); err == nil {
		t.Fatal("accepted unknown sysctl response")
	}
}

func TestMacBootstrapRejectsExpiredRecordBeforeNetwork(t *testing.T) {
	in, s, _, r, _ := bootstrapFixture(t)
	in.Now = in.Now.Add(40 * 24 * time.Hour)
	if err := RunMacBootstrap(context.Background(), in); err == nil {
		t.Fatal("expired record accepted")
	}
	if len(s.opened) != 0 || r.installs != 0 {
		t.Fatal("expired metadata caused a side effect")
	}
}

func TestMacBootstrapSameSequenceRequiresExactAcceptedDigest(t *testing.T) {
	in, source, _, runner, watermark := bootstrapFixture(t)
	watermark.n = 12
	watermark.digest = strings.Repeat("f", 64)
	runner.installed = true
	if err := RunMacBootstrap(context.Background(), in); Code(err) != CodeVerificationFailed {
		t.Fatalf("same-sequence different record: %v", err)
	}
	if len(source.opened) != 0 || runner.installs != 0 {
		t.Fatal("different signed record caused side effect")
	}
}

func TestMacBootstrapRev10PhaseOrderAndReceiptIsNotActivation(t *testing.T) {
	in, source, verifier, runner, fence := bootstrapFixture(t)
	runner.installed = true // a receipt or inert inbox must not advance the fence
	var events []string
	verifier.events, runner.events, fence.events = &events, &events, &events
	in.InboxVerifier.(*testMacInbox).events = &events
	in.MasterKey.(*testMacMasterKey).events = &events
	in.Activator.(*testMacActivator).events = &events
	if err := RunMacBootstrap(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	want := []string{"audit", "verify", "install", "inbox", "provision", "activate", "audit", "advance"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("phase order: %v", events)
	}
	if len(source.opened) != 3 || fence.n != 12 {
		t.Fatal("delivery or fence omitted")
	}
}

func TestMacBootstrapRev10FailsClosedAtEachActivationBoundary(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*MacBootstrapInput)
	}{
		{"missing inbox verifier", func(in *MacBootstrapInput) { in.InboxVerifier = nil }},
		{"missing master key", func(in *MacBootstrapInput) { in.MasterKey = nil }},
		{"missing activator", func(in *MacBootstrapInput) { in.Activator = nil }},
		{"wrong home", func(in *MacBootstrapInput) { in.Home = filepath.Join(in.Home, "elsewhere") }},
		{"wrong state", func(in *MacBootstrapInput) { in.StateDir = filepath.Join(in.Home, "state") }},
		{"inbox tamper", func(in *MacBootstrapInput) { in.InboxVerifier.(*testMacInbox).fail = true }},
		{"inbox path swap", func(in *MacBootstrapInput) { in.InboxVerifier.(*testMacInbox).tamperPath = true }},
		{"master key locked", func(in *MacBootstrapInput) { in.MasterKey.(*testMacMasterKey).fail = true }},
		{"Apply failure", func(in *MacBootstrapInput) { in.Activator.(*testMacActivator).failActivate = true }},
		{"active audit failure", func(in *MacBootstrapInput) { in.Activator.(*testMacActivator).failAuditAt = 2 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in, source, _, runner, fence := bootstrapFixture(t)
			tc.mutate(&in)
			if err := RunMacBootstrap(context.Background(), in); err == nil {
				t.Fatal("unsafe activation succeeded")
			}
			if fence.n != 11 {
				t.Fatal("failure advanced release fence")
			}
			if tc.name == "wrong home" || tc.name == "wrong state" || strings.HasPrefix(tc.name, "missing") {
				if len(source.opened) != 0 || runner.installs != 0 {
					t.Fatal("invalid inputs caused download or install")
				}
			}
			if tc.name == "inbox tamper" || tc.name == "inbox path swap" || tc.name == "master key locked" {
				if in.Activator.(*testMacActivator).activates != 0 {
					t.Fatal("activation preceded trusted prerequisites")
				}
			}
		})
	}
}

func TestMacBootstrapRev10SameSequenceAuditsWithoutReceipt(t *testing.T) {
	in, source, _, runner, fence := bootstrapFixture(t)
	sum := sha256.Sum256(in.Delivery)
	fence.n = 12
	fence.digest = hex.EncodeToString(sum[:])
	runner.installed = false
	activator := in.Activator.(*testMacActivator)
	activator.active = true
	if err := RunMacBootstrap(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if len(source.opened) != 0 || runner.installs != 0 || in.MasterKey.(*testMacMasterKey).called != 1 || activator.audits != 1 {
		t.Fatal("same-sequence audit or provision omitted")
	}
	activator.active = false
	runner.installed = true
	if err := RunMacBootstrap(context.Background(), in); Code(err) != CodeVerificationFailed {
		t.Fatalf("inert receipt accepted: %v", err)
	}
	if len(source.opened) != 0 || runner.installs != 0 {
		t.Fatal("same-sequence rerun attempted reinstall")
	}
}

func TestMacBootstrapRev10RecoveryRejectsUncertainActiveTree(t *testing.T) {
	in, source, _, runner, fence := bootstrapFixture(t)
	fence.fail = true
	if err := RunMacBootstrap(context.Background(), in); err == nil {
		t.Fatal("lost fence publication hidden")
	}
	opened := len(source.opened)
	fence.fail = false
	in.Activator.(*testMacActivator).failAudit = true
	if err := RunMacBootstrap(context.Background(), in); Code(err) != CodeVerificationFailed {
		t.Fatalf("uncertain recovery succeeded: %v", err)
	}
	if fence.n != 11 || len(source.opened) != opened || runner.installs != 1 {
		t.Fatal("uncertain recovery downloaded, installed, or advanced")
	}
}

func TestMacBootstrapRev10RejectsSymlinkedStateAncestor(t *testing.T) {
	home := canonicalTempDir(t)
	library := filepath.Join(home, "Library")
	if err := os.Mkdir(library, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(library, "Application Support")); err != nil {
		t.Fatal(err)
	}
	if err := validateMacStatePath(home); Code(err) != CodeInvalidInput {
		t.Fatalf("symlinked state parent: %v", err)
	}
}
