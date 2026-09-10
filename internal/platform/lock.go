package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

const (
	lockFileName             = "controller.lock"
	lockWatchIntervalDefault = time.Second
)

// Acquire takes the exclusive installation lock for this state directory.
// The lock is an OS file lock (flock) held on an open file description for
// the lifetime of the controller: a second controller process on the same
// state directory is refused, and only explicit release drops it. If the
// lock file is replaced or removed underneath a held lock, the watchdog
// closes the Lost channel and Held reports false; admission must stop.
func (p *Platform) Acquire(ctx context.Context) (contract.Ownership, error) {
	if err := p.ensureOpen(); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(p.stateDir, lockFileName)
	f, err := openPrivate(lockPath, os.O_RDWR|os.O_CREATE, filePrivate)
	if err != nil {
		return nil, errWrap(contractCodeControllerUnavailable, "installation lock file cannot be opened", err)
	}
	if err := f.Chmod(filePrivate); err != nil {
		_ = f.Close()
		return nil, errWrap(contractCodeControllerUnavailable, "installation lock file permissions cannot be enforced", err)
	}
	fd := int(f.Fd())
	for {
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EACCES) {
			return nil, errf(contractCodeControllerUnavailable, "installation is already served by another controller")
		}
		return nil, errWrap(contractCodeControllerUnavailable, "installation lock cannot be taken", err)
	}
	o := &installationLock{
		path:      lockPath,
		f:         f,
		held:      true,
		lost:      make(chan struct{}),
		done:      make(chan struct{}),
		watchDone: make(chan struct{}),
		interval:  p.lockWatchInterval,
	}
	go o.watch()
	return o, nil
}

// installationLock is the contract.Ownership implementation over an
// exclusive flock.
type installationLock struct {
	path string
	f    *os.File

	mu        sync.Mutex
	held      bool
	closed    bool
	lostOnce  sync.Once
	lost      chan struct{}
	done      chan struct{}
	watchDone chan struct{}
	interval  time.Duration
}

func (o *installationLock) Held() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.held
}

// Lost closes when ownership is lost or released.
func (o *installationLock) Lost() <-chan struct{} { return o.lost }

// Close releases the lock explicitly. It is idempotent.
func (o *installationLock) Close() error {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return nil
	}
	o.closed = true
	o.held = false
	o.mu.Unlock()
	// Stop the watchdog and wait for it to exit before touching the
	// descriptor: stale() reads the fd concurrently.
	close(o.done)
	<-o.watchDone
	_ = syscall.Flock(int(o.f.Fd()), syscall.LOCK_UN)
	_ = o.f.Close()
	o.lose()
	return nil
}

func (o *installationLock) watch() {
	defer close(o.watchDone)
	t := time.NewTicker(o.interval)
	defer t.Stop()
	for {
		select {
		case <-o.done:
			return
		case <-t.C:
			if o.stale() {
				o.lose()
				return
			}
		}
	}
}

// stale detects replacement or removal of the lock file, and loss of our
// flock: a fresh non-blocking exclusive flock only succeeds if we no longer
// hold the lock.
func (o *installationLock) stale() bool {
	var ours syscall.Stat_t
	if err := syscall.Fstat(int(o.f.Fd()), &ours); err != nil {
		return true
	}
	fi, err := os.Lstat(o.path)
	if err != nil {
		return true // lock file removed
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return true
	}
	other, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	if other.Dev != ours.Dev || other.Ino != ours.Ino {
		return true // replaced: our lock is on a detached inode
	}
	probe, err := openPrivate(o.path, os.O_RDONLY, 0)
	if err != nil {
		return false // transient; not proof of loss
	}
	defer func() { _ = probe.Close() }()
	err = syscall.Flock(int(probe.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		_ = syscall.Flock(int(probe.Fd()), syscall.LOCK_UN)
		return true // a new description acquired it: we lost ownership
	}
	return false
}

// lose marks ownership lost exactly once; the admission fence is the closed
// Lost channel.
func (o *installationLock) lose() {
	o.mu.Lock()
	o.held = false
	o.mu.Unlock()
	o.lostOnce.Do(func() { close(o.lost) })
}
