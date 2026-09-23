package packaging

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func newFenceFixture(t *testing.T) *FileMacSequenceWatermark {
	t.Helper()
	w, err := NewFileMacSequenceWatermark(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return w
}
func acceptedFixture(n int64) MacAcceptedRelease {
	return MacAcceptedRelease{Sequence: n, DeliverySHA256: strings.Repeat("a", 64)}
}

func TestMacFileFenceAtomicRoundTripAndMonotonicity(t *testing.T) {
	w := newFenceFixture(t)
	unlock, err := w.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	before, err := w.Load(context.Background())
	if err != nil || before != (MacAcceptedRelease{}) {
		t.Fatalf("missing fence: %+v %v", before, err)
	}
	if err := w.Advance(context.Background(), acceptedFixture(5)); err != nil {
		t.Fatal(err)
	}
	got, err := w.Load(context.Background())
	if err != nil || got != acceptedFixture(5) {
		t.Fatalf("roundtrip: %+v %v", got, err)
	}
	path := filepath.Join(w.dir, macAcceptedFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{\"delivery_sha256\":\""+strings.Repeat("a", 64)+"\",\"release_sequence\":5,\"schema\":\"zatiti.mac_accepted_release/v1\"}\n" {
		t.Fatalf("not canonical: %s", raw)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: %v %v", info, err)
	}
	dirInfo, err := os.Stat(w.dir)
	if err != nil || dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode: %v %v", dirInfo, err)
	}
	if err := w.Advance(context.Background(), acceptedFixture(5)); err != nil {
		t.Fatalf("same exact fence: %v", err)
	}
	if err := w.Advance(context.Background(), acceptedFixture(4)); Code(err) != CodeVerificationFailed {
		t.Fatalf("downgrade: %v", err)
	}
	alternate := acceptedFixture(5)
	alternate.DeliverySHA256 = strings.Repeat("b", 64)
	if err := w.Advance(context.Background(), alternate); Code(err) != CodeVerificationFailed {
		t.Fatalf("alternate digest: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(raw) {
		t.Fatal("rejected write changed accepted record")
	}
}

func TestMacFileFenceRejectsCorruptModeAndSymlink(t *testing.T) {
	w := newFenceFixture(t)
	unlock, err := w.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	path := filepath.Join(w.dir, macAcceptedFile)
	cases := []string{
		"{", "", strings.Repeat(" ", macAcceptedMaxBytes+1),
		`{"schema":"zatiti.mac_accepted_release/v1","release_sequence":2}`,
		`{"schema":"zatiti.mac_accepted_release/v2","release_sequence":2,"delivery_sha256":"` + strings.Repeat("a", 64) + `"}`,
		`{"schema":"zatiti.mac_accepted_release/v1","release_sequence":2,"delivery_sha256":"` + strings.Repeat("a", 64) + `","extra":1}`,
		`{"schema":"zatiti.mac_accepted_release/v1","schema":"zatiti.mac_accepted_release/v1","release_sequence":2,"delivery_sha256":"` + strings.Repeat("a", 64) + `"}`,
		string([]byte{0xff}),
	}
	for _, raw := range cases {
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Load(context.Background()); err == nil {
			t.Fatalf("accepted corrupt fence %.60q", raw)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(w.dir, "target"), path); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Load(context.Background()); err == nil {
		t.Fatal("followed fence symlink")
	}
	if err := w.Advance(context.Background(), acceptedFixture(1)); err == nil {
		t.Fatal("replaced fence symlink")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Load(context.Background()); err == nil {
		t.Fatal("accepted permissive fence")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(w.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Load(context.Background()); err == nil {
		t.Fatal("accepted permissive directory")
	}
}

func TestMacBootstrapLockSerializesAndCancels(t *testing.T) {
	w := newFenceFixture(t)
	first, err := w.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewFileMacSequenceWatermark(filepath.Dir(filepath.Dir(filepath.Dir(w.dir))))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = second.Lock(ctx)
	if err == nil || time.Since(started) < 90*time.Millisecond {
		t.Fatalf("competing lock did not wait: %v", err)
	}
	first()
	unlock, err := second.Lock(context.Background())
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	unlock()
}

type ownerChangedInfo struct {
	os.FileInfo
	stat *syscall.Stat_t
}

func (f ownerChangedInfo) Sys() any { return f.stat }
func TestMacFenceOwnerValidation(t *testing.T) {
	w := newFenceFixture(t)
	unlock, err := w.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	info, err := os.Stat(filepath.Join(w.dir, macBootstrapLockFile))
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("owner metadata unavailable")
	}
	clone := *stat
	clone.Uid = uint32(os.Getuid() + 1)
	if acceptedFileInfo(ownerChangedInfo{info, &clone}) {
		t.Fatal("accepted wrong-owner lock or fence")
	}
}

func TestMacFenceCancelledAdvanceKeepsAcceptedRecord(t *testing.T) {
	w := newFenceFixture(t)
	unlock, err := w.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := w.Advance(context.Background(), acceptedFixture(1)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := w.Advance(ctx, acceptedFixture(2)); err == nil {
		t.Fatal("cancelled advance succeeded")
	}
	got, err := w.Load(context.Background())
	if err != nil || got != acceptedFixture(1) {
		t.Fatalf("old fence lost: %+v %v", got, err)
	}
}

type blockingMacVerifier struct {
	entered chan struct{}
	release chan struct{}
}

func (v *blockingMacVerifier) Verify(ctx context.Context, _ MacReleaseDescriptor, _ MacDownloadPlan, _ MacStagedAssets) error {
	close(v.entered)
	select {
	case <-v.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func TestRunMacBootstrapSerializesCompetingProcesses(t *testing.T) {
	in, source, _, runner, _ := bootstrapFixture(t)
	home := t.TempDir()
	firstFence, err := NewFileMacSequenceWatermark(home)
	if err != nil {
		t.Fatal(err)
	}
	secondFence, err := NewFileMacSequenceWatermark(home)
	if err != nil {
		t.Fatal(err)
	}
	blocker := &blockingMacVerifier{entered: make(chan struct{}), release: make(chan struct{})}
	in.Verifier = blocker
	in.Watermark = firstFence
	done := make(chan error, 1)
	go func() { done <- RunMacBootstrap(context.Background(), in) }()
	select {
	case <-blocker.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first bootstrap did not reach verifier")
	}
	other := in
	other.Watermark = secondFence
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if err := RunMacBootstrap(ctx, other); err == nil {
		t.Fatal("second bootstrap passed held lock")
	}
	close(blocker.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got, err := secondFence.Load(context.Background())
	if err != nil || got.Sequence != 12 {
		t.Fatalf("fence after first install: %+v %v", got, err)
	}
	if runner.installs != 1 || len(source.opened) != 3 {
		t.Fatalf("concurrent activation: installs=%d downloads=%d", runner.installs, len(source.opened))
	}
}

type failOnceFence struct {
	inner  *FileMacSequenceWatermark
	failed bool
}

func (f *failOnceFence) Lock(ctx context.Context) (func(), error) { return f.inner.Lock(ctx) }
func (f *failOnceFence) Load(ctx context.Context) (MacAcceptedRelease, error) {
	return f.inner.Load(ctx)
}
func (f *failOnceFence) Advance(ctx context.Context, a MacAcceptedRelease) error {
	if !f.failed {
		f.failed = true
		return errors.New("simulated crash before publication")
	}
	return f.inner.Advance(ctx, a)
}
func TestRunMacBootstrapRecoversActivationBeforeFence(t *testing.T) {
	in, source, _, runner, _ := bootstrapFixture(t)
	realFence, err := NewFileMacSequenceWatermark(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	in.Watermark = &failOnceFence{inner: realFence}
	if err := RunMacBootstrap(context.Background(), in); err == nil {
		t.Fatal("lost fence write was hidden")
	}
	if !runner.installed || runner.installs != 1 {
		t.Fatal("activation did not precede fence failure")
	}
	got, err := realFence.Load(context.Background())
	if err != nil || got.Sequence != 0 {
		t.Fatalf("fence advanced despite failure: %+v %v", got, err)
	}
	opened := len(source.opened)
	in.Watermark = realFence
	if err := RunMacBootstrap(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	got, err = realFence.Load(context.Background())
	if err != nil || got.Sequence != 12 {
		t.Fatalf("recovery fence: %+v %v", got, err)
	}
	if runner.installs != 1 || len(source.opened) != opened {
		t.Fatal("recovery invoked installer or downloader again")
	}
}

func TestMacBootstrapLockRejectsSymlinkAndPermissiveMode(t *testing.T) {
	w := newFenceFixture(t)
	unlock, err := w.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	path := filepath.Join(w.dir, macBootstrapLockFile)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Lock(context.Background()); err == nil {
		t.Fatal("permissive lock accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(w.dir, "target"), path); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Lock(context.Background()); err == nil {
		t.Fatal("symlink lock accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(w.dir); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(filepath.Dir(w.dir), "elsewhere")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, w.dir); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Lock(context.Background()); err == nil {
		t.Fatal("symlink directory accepted")
	}
}

type cancelOnInstallerSource struct {
	inner  *testMacSource
	cancel context.CancelFunc
}

func (s cancelOnInstallerSource) Open(ctx context.Context, a MacDeliveryAsset) (io.ReadCloser, error) {
	r, err := s.inner.Open(ctx, a)
	if a.Distribution == "installer" {
		s.cancel()
	}
	return r, err
}
func TestRunMacBootstrapCancellationCleansStageBeforeActivation(t *testing.T) {
	in, source, verifier, runner, _ := bootstrapFixture(t)
	fence, err := NewFileMacSequenceWatermark(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in.Watermark = fence
	in.Source = cancelOnInstallerSource{inner: source, cancel: cancel}
	if err := RunMacBootstrap(ctx, in); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if verifier.called != 0 || runner.installs != 0 {
		t.Fatal("cancelled bootstrap activated")
	}
	got, err := fence.Load(context.Background())
	if err != nil || got.Sequence != 0 {
		t.Fatalf("cancelled fence: %+v %v", got, err)
	}
	unlock, err := fence.Lock(context.Background())
	if err != nil {
		t.Fatalf("cancellation leaked lock: %v", err)
	}
	unlock()
}
