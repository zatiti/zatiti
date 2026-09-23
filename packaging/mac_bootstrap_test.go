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
	"path/filepath"
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
}

func (v *testMacVerifier) Verify(_ context.Context, _ MacReleaseDescriptor, _ MacDownloadPlan, s MacStagedAssets) error {
	v.called++
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
}

func (r *testMacRunner) Installed(context.Context, MacReleaseDescriptor, string) (bool, error) {
	return r.installed, nil
}
func (r *testMacRunner) Install(_ context.Context, p string) error {
	r.installs++
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
}

func (w *testMacWatermark) Lock(context.Context) (func(), error) { return func() {}, nil }
func (w *testMacWatermark) Load(context.Context) (MacAcceptedRelease, error) {
	return MacAcceptedRelease{Sequence: w.n, DeliverySHA256: w.digest}, nil
}
func (w *testMacWatermark) Advance(_ context.Context, a MacAcceptedRelease) error {
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
	in := MacBootstrapInput{Delivery: raw, Signature: sig, TrustedKeys: []ed25519.PublicKey{pub}, Now: now, Source: source, Verifier: verifier, Runner: runner, Watermark: watermark, NativeArch: func(context.Context) (string, error) { return "arm64", nil }}
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
