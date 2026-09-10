package platform

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

func TestZ01DuplicateControllerRefusedInProcess(t *testing.T) {
	p1, _ := openForTest(t)
	own1, err := p1.Acquire(ctx())
	if err != nil {
		t.Fatalf("first controller Acquire: %v", err)
	}
	defer func() { _ = own1.Close() }()

	p2, _ := openForTestWithKey(t, writeKeyFile(t, testMasterKey(t)))
	p2.stateDir = p1.stateDir // same installation
	own2, err := p2.Acquire(ctx())
	if err == nil {
		_ = own2.Close()
		t.Fatal("second controller was admitted to a served installation")
	}
	wantCode(t, err, contractCodeControllerUnavailable)
	if msg := errMessage(err); msg != "installation is already served by another controller" {
		t.Fatalf("unexpected refusal message: %q", msg)
	}
	if own1.Held() != true {
		t.Fatal("first controller lost Held() although it never released")
	}
	select {
	case <-own1.Lost():
		t.Fatal("first controller was marked lost by a refused contender")
	default:
	}
}

// TestZ01DuplicateControllerRefusedAcrossProcesses proves exclusion between
// two real processes: a re-executed test binary cannot take the lock while
// this process holds it.
func TestZ01DuplicateControllerRefusedAcrossProcesses(t *testing.T) {
	keyRef := writeKeyFile(t, testMasterKey(t))
	p, state := openForTestWithKey(t, keyRef)
	own, err := p.Acquire(ctx())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = own.Close() }()

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^$", "-test.v=false")
	cmd.Env = append(os.Environ(),
		childEnvVar+"=duplicate-controller",
		"ZATITI_TEST_STATE="+state,
		"ZATITI_TEST_KEY="+keyRef,
	)
	out, err := cmd.CombinedOutput()
	exitErr := &exec.ExitError{}
	if !asExitError(err, &exitErr) || exitErr.ExitCode() != 44 {
		t.Fatalf("second process was not refused with exit 44 (err=%v, out=%s)", err, out)
	}
	if !own.Held() {
		t.Fatal("holder lost ownership while a contender was refused")
	}
}

func asExitError(err error, target **exec.ExitError) bool {
	if err == nil {
		return false
	}
	if e, ok := err.(*exec.ExitError); ok {
		*target = e
		return true
	}
	return false
}

func TestAcquireRefusedWhenLockPathIsSymlink(t *testing.T) {
	_, state := openForTest(t)
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(state, fileLock)); err != nil {
		t.Fatal(err)
	}
	keyRef := writeKeyFile(t, testMasterKey(t))
	p, err := Open(Config{StateDir: state, CredentialBackend: backendHeadless, MasterKeyRef: keyRef})
	if err != nil {
		t.Fatal(err)
	}
	p.lockWatchInterval = 25 * time.Millisecond
	_, err = p.Acquire(ctx())
	wantCode(t, err, contractCodeControllerUnavailable)
}

func TestAcquireRefusedWhenLockPathIsDirectory(t *testing.T) {
	_, state := openForTest(t)
	if err := os.Mkdir(filepath.Join(state, fileLock), 0o700); err != nil {
		t.Fatal(err)
	}
	keyRef := writeKeyFile(t, testMasterKey(t))
	p, err := Open(Config{StateDir: state, CredentialBackend: backendHeadless, MasterKeyRef: keyRef})
	if err != nil {
		t.Fatal(err)
	}
	p.lockWatchInterval = 25 * time.Millisecond
	_, err = p.Acquire(ctx())
	wantCode(t, err, contractCodeControllerUnavailable)
}

func TestLockFileIsPrivate(t *testing.T) {
	p, state := openForTest(t)
	own, err := p.Acquire(ctx())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = own.Close() }()
	if got := modeOf(t, filepath.Join(state, fileLock)); got != 0o600 {
		t.Fatalf("controller.lock mode is %o, want 600", got)
	}
}

func TestOwnershipLostWhenLockFileRemoved(t *testing.T) {
	_, state := openForTest(t)
	own, err := acquireFromState(t, state)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = own.Close() }()

	if err := os.Remove(filepath.Join(state, fileLock)); err != nil {
		t.Fatal(err)
	}
	waitForLost(t, own)
	if own.Held() {
		t.Fatal("Held() is true after the lock file was removed")
	}
}

func TestOwnershipLostWhenLockFileReplaced(t *testing.T) {
	_, state := openForTest(t)
	own, err := acquireFromState(t, state)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = own.Close() }()

	lockPath := filepath.Join(state, fileLock)
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	replacement, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_ = replacement.Close()

	waitForLost(t, own)
	if own.Held() {
		t.Fatal("Held() is true after the lock file was replaced")
	}
}

func TestOwnershipLostWhenFlockIsTakenOver(t *testing.T) {
	// Force the loss at the descriptor level. An external process cannot
	// actually steal a held flock (its LOCK_EX fails while ours stands), so
	// the faithful simulation of losing the OS lock is releasing it behind
	// the controller's back; the watchdog's fresh-description probe then
	// succeeds, which is proof of lost ownership.
	_, state := openForTest(t)
	own, err := acquireFromState(t, state)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = own.Close() }()

	o, ok := own.(*installationLock)
	if !ok {
		t.Fatalf("acquireFromState returned %T, want *installationLock", own)
	}
	if err := syscall.Flock(int(o.f.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatalf("descriptor-level unlock: %v", err)
	}

	waitForLost(t, own)
	if own.Held() {
		t.Fatal("Held() is true after the flock was released behind the controller's back")
	}
}

func TestOwnershipCloseIsIdempotentAndReleases(t *testing.T) {
	_, state := openForTest(t)
	own, err := acquireFromState(t, state)
	if err != nil {
		t.Fatal(err)
	}
	if err := own.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := own.Close(); err != nil {
		t.Fatalf("second Close must be a no-op: %v", err)
	}
	if own.Held() {
		t.Fatal("Held() is true after release")
	}
	select {
	case <-own.Lost():
	default:
		t.Fatal("Lost() must close on explicit release")
	}

	// The lock must be re-acquirable after release.
	p2, err := Open(Config{StateDir: state, CredentialBackend: backendHeadless, MasterKeyRef: writeKeyFile(t, testMasterKey(t))})
	if err != nil {
		t.Fatal(err)
	}
	p2.lockWatchInterval = 25 * time.Millisecond
	own2, err := p2.Acquire(ctx())
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	_ = own2.Close()
	_ = p2.Close()
}

func TestConcurrentAcquireHasExactlyOneWinner(t *testing.T) {
	const contenders = 8
	keyRef := writeKeyFile(t, testMasterKey(t))
	_, state := openForTestWithKey(t, keyRef)

	platforms := make([]*Platform, contenders)
	for i := range platforms {
		p, err := Open(Config{StateDir: state, CredentialBackend: backendHeadless, MasterKeyRef: keyRef})
		if err != nil {
			t.Fatalf("contender %d Open: %v", i, err)
		}
		p.lockWatchInterval = 25 * time.Millisecond
		platforms[i] = p
	}
	defer func() {
		for _, p := range platforms {
			_ = p.Close()
		}
	}()

	var wins int64
	var wg sync.WaitGroup
	for _, p := range platforms {
		p := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			own, err := p.Acquire(ctx())
			if err == nil {
				// Hold briefly, then release so cleanup cannot deadlock.
				time.Sleep(10 * time.Millisecond)
				_ = own.Close()
				atomic.AddInt64(&wins, 1)
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("exactly one contender must win, got %d", wins)
	}
}

func TestPlatformCloseKeepsLockWatchdogAlive(t *testing.T) {
	_, state := openForTest(t)
	own, err := acquireFromState(t, state)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = own.Close() }()

	p, err := Open(Config{StateDir: state, CredentialBackend: backendHeadless, MasterKeyRef: writeKeyFile(t, testMasterKey(t))})
	if err != nil {
		t.Fatal(err)
	}
	_ = p.Close()

	// Platform closure must not end ownership; the watchdog keeps watching.
	select {
	case <-own.Lost():
		t.Fatal("ownership was lost by Platform.Close")
	case <-time.After(100 * time.Millisecond):
	}
	if !own.Held() {
		t.Fatal("Held() is false after Platform.Close")
	}
}

// acquireFromState opens a platform over an existing state directory and
// takes its lock, for tests that manipulate the state tree in place.
func acquireFromState(t *testing.T, state string) (contract.Ownership, error) {
	t.Helper()
	p, err := Open(Config{StateDir: state, CredentialBackend: backendHeadless, MasterKeyRef: writeKeyFile(t, testMasterKey(t))})
	if err != nil {
		return nil, err
	}
	p.lockWatchInterval = 25 * time.Millisecond
	t.Cleanup(func() { _ = p.Close() })
	return p.Acquire(context.Background())
}
