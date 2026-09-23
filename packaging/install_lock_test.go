package packaging

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type failOneLoad struct {
	inner  *recordingManager
	failed bool
}

var errStartNew = errors.New("start new failed")
var errRestoreOld = errors.New("restore old failed")

type failBothLoads struct{ loads int }

func (f *failBothLoads) Do(_ context.Context, action ServiceAction) error {
	if action.Verb != VerbLoad {
		return nil
	}
	f.loads++
	if f.loads == 1 {
		return errStartNew
	}
	return errRestoreOld
}

func (f *failOneLoad) Do(ctx context.Context, action ServiceAction) error {
	if action.Verb == VerbLoad && !f.failed {
		f.failed = true
		return errors.New("synthetic service start failure")
	}
	return f.inner.Do(ctx, action)
}

func TestRepeatInstallVerifiesInstalledBytesAndDoesNotTouchService(t *testing.T) {
	in := install(t, "darwin", "1.0.0")
	p, _ := planFor(t, in.layout, "1.0.0")
	if p.Kind != PlanNoop || len(p.Prepare)+len(p.Before)+len(p.Steps)+len(p.After) != 0 {
		t.Fatalf("repeat install did not produce an empty plan: %+v", p)
	}
	prior := len(in.manager.calls)
	if err := Apply(context.Background(), p, in.manager); err != nil {
		t.Fatal(err)
	}
	if len(in.manager.calls) != prior {
		t.Fatal("repeat install touched the service")
	}
	writeFile(t, filepath.Join(in.layout.VersionDir("1.0.0"), "bin", "zatiti"), []byte("tampered"), 0o755)
	if err := Apply(context.Background(), p, in.manager); err == nil {
		t.Fatal("stale no-op accepted tampered release")
	}
	in.wantState(t)
}

func TestRepeatInstallRejectsDifferentSourceAndLauncher(t *testing.T) {
	in := install(t, "darwin", "1.0.0")
	f := newFixture(t, "1.0.0", "darwin")
	writeFile(t, filepath.Join(f.root, "bin", "zatiti"), []byte("different signed binary"), 0o755)
	m := f.build(t)
	inst, err := Inspect(in.layout)
	if err != nil {
		t.Fatal(err)
	}
	_, err = PlanInstallation(InstallInput{Layout: in.layout, Manifest: m, SourceRoot: f.root,
		HostArch: "arm64", Services: []ServiceSpec{controllerSpec(in.layout, m)}, Installed: inst})
	wantCode(t, err, CodeConflict)
	writeFile(t, filepath.Join(in.layout.UnitDir, unitFileName(in.layout.Manager, ControllerLabel)), []byte("changed launcher"), 0o644)
	_, err = PlanInstallation(InstallInput{Layout: in.layout, Manifest: m, SourceRoot: f.root,
		HostArch: "arm64", Services: []ServiceSpec{controllerSpec(in.layout, m)}, Installed: inst})
	wantCode(t, err, CodeConflict)
}

func TestInstallationLockRejectsConcurrentWriterAndUnsafeFile(t *testing.T) {
	l := testLayout(t, "darwin")
	unlock, err := lockInstallation(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = lockInstallation(ctx, l)
	wantCode(t, err, CodeConflict)
	unlock()
	unlock, err = lockInstallation(context.Background(), l)
	if err != nil {
		t.Fatalf("lock was not released: %v", err)
	}
	unlock()
	path := filepath.Join(l.Home, ".zatiti-install.lock")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(l.Home, "other"), path); err != nil {
		t.Fatal(err)
	}
	_, err = lockInstallation(context.Background(), l)
	wantCode(t, err, CodeConflict)
}

func TestUpgradeRestoresPreviousReleaseAfterServiceStartFailure(t *testing.T) {
	in := install(t, "darwin", "1.0.0")
	oldLauncher := filepath.Join(in.layout.UnitDir, unitFileName(in.layout.Manager, ControllerLabel))
	wantLauncher, err := os.ReadFile(oldLauncher)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := planFor(t, in.layout, "1.1.0")
	if err := Apply(context.Background(), p, &failOneLoad{inner: in.manager}); err == nil {
		t.Fatal("upgrade unexpectedly succeeded")
	}
	inst, err := Inspect(in.layout)
	if err != nil || inst.Current != "1.0.0" {
		t.Fatalf("old release was not restored: %+v, %v", inst, err)
	}
	gotLauncher, err := os.ReadFile(oldLauncher)
	if err != nil || string(gotLauncher) != string(wantLauncher) {
		t.Fatal("old launcher was not restored")
	}
	if err := AuditInstalled(in.layout, []string{ControllerLabel}); err != nil {
		t.Fatal(err)
	}
	in.wantState(t)
}

func TestUpgradeReportsOriginalAndRecoveryFailures(t *testing.T) {
	in := install(t, "darwin", "1.0.0")
	p, _ := planFor(t, in.layout, "1.1.0")
	err := Apply(context.Background(), p, &failBothLoads{})
	if Code(err) != CodeInternalError || !errors.Is(err, errStartNew) || !errors.Is(err, errRestoreOld) {
		t.Fatalf("both failure causes must survive: %v", err)
	}
	inst, inspectErr := Inspect(in.layout)
	if inspectErr != nil || inst.Current != "1.0.0" {
		t.Fatalf("previous release link: %+v, %v", inst, inspectErr)
	}
	in.wantState(t)
}
