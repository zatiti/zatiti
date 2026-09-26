package packaging

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// installed drives a real first install into temp directories.
type installed struct {
	layout  Layout
	manager *recordingManager
	state   string // a file the controller would have written
}

func planFor(t *testing.T, l Layout, version string) (Plan, fixture) {
	t.Helper()
	f := newFixture(t, version, l.OS)
	m := f.build(t)
	inst, err := Inspect(l)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	plan, err := PlanInstallation(InstallInput{
		Layout: l, Manifest: m, SourceRoot: f.root, HostArch: "arm64",
		Services: []ServiceSpec{controllerSpec(l, m)}, Installed: inst,
	})
	if err != nil {
		t.Fatalf("PlanInstallation %s: %v", version, err)
	}
	return plan, f
}

func install(t *testing.T, goos, version string) installed {
	t.Helper()
	l := testLayout(t, goos)
	in := installed{layout: l, manager: &recordingManager{layout: l}, state: filepath.Join(l.StateDir, "zatiti.db")}
	writeFile(t, in.state, []byte("durable state"), 0o600)
	plan, _ := planFor(t, l, version)
	if plan.Kind != PlanInstall {
		t.Fatalf("first plan kind is %s", plan.Kind)
	}
	if err := Apply(context.Background(), plan, in.manager); err != nil {
		t.Fatalf("Apply install: %v", err)
	}
	return in
}

func (in installed) wantState(t *testing.T) {
	t.Helper()
	raw, err := os.ReadFile(in.state)
	if err != nil || string(raw) != "durable state" {
		t.Fatalf("state was not preserved: %q, %v", raw, err)
	}
	info, err := os.Stat(in.state)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("state permissions changed: %v, %v", info, err)
	}
}

func TestInstallUpgradeUninstallPreserveState(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			t.Parallel()
			in := install(t, goos, "1.0.0")
			l := in.layout
			in.wantState(t)
			if err := AuditInstalled(l, []string{ControllerLabel}); err != nil {
				t.Fatalf("AuditInstalled after install: %v", err)
			}
			raw, err := os.ReadFile(filepath.Join(l.Current, "bin", "zatiti"))
			if err != nil || string(raw) != "synthetic controller 1.0.0" {
				t.Fatalf("active controller is %q, %v", raw, err)
			}

			// Upgrade: the release is published while the service still
			// runs, the service is unloaded before the link moves, and
			// loaded again after.
			plan, _ := planFor(t, l, "1.1.0")
			if plan.Kind != PlanUpgrade || plan.PreviousVersion != "1.0.0" {
				t.Fatalf("second plan is %s from %q", plan.Kind, plan.PreviousVersion)
			}
			in.manager.calls = nil
			if err := Apply(context.Background(), plan, in.manager); err != nil {
				t.Fatalf("Apply upgrade: %v", err)
			}
			wantCalls := []string{
				"unload " + ControllerLabel + " @versions/1.0.0",
				"load " + ControllerLabel + " @versions/1.1.0",
			}
			if !reflect.DeepEqual(in.manager.calls, wantCalls) {
				t.Fatalf("service actions:\n got %q\nwant %q", in.manager.calls, wantCalls)
			}
			in.wantState(t)
			got, err := Inspect(l)
			if err != nil || got.Current != "1.1.0" || !reflect.DeepEqual(got.Versions, []string{"1.0.0", "1.1.0"}) {
				t.Fatalf("after upgrade: %+v, %v", got, err)
			}
			if err := AuditInstalled(l, []string{ControllerLabel}); err != nil {
				t.Fatalf("AuditInstalled after upgrade: %v", err)
			}

			// Uninstall without a state request preserves state.
			un, err := PlanUninstallation(UninstallInput{Layout: l, Labels: []string{ControllerLabel}})
			if err != nil {
				t.Fatal(err)
			}
			if un.RemovesState {
				t.Fatal("a plain uninstall plans to remove state")
			}
			in.manager.calls = nil
			if err := Apply(context.Background(), un, in.manager); err != nil {
				t.Fatalf("Apply uninstall: %v", err)
			}
			if !reflect.DeepEqual(in.manager.calls, []string{"unload " + ControllerLabel + " @versions/1.1.0"}) {
				t.Fatalf("uninstall service actions: %q", in.manager.calls)
			}
			for _, gone := range []string{l.DistRoot, filepath.Join(l.UnitDir, unitFileName(l.Manager, ControllerLabel))} {
				if _, err := os.Lstat(gone); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("%s survived uninstall: %v", filepath.Base(gone), err)
				}
			}
			in.wantState(t)

			// A second uninstall is a no-op, and a reinstall finds the
			// state unchanged.
			if err := Apply(context.Background(), un, in.manager); err != nil {
				t.Fatalf("repeated uninstall: %v", err)
			}
			plan, _ = planFor(t, l, "1.1.0")
			if err := Apply(context.Background(), plan, in.manager); err != nil {
				t.Fatalf("reinstall: %v", err)
			}
			in.wantState(t)
		})
	}
}

func TestStateRemovalNeedsExactConfirmation(t *testing.T) {
	t.Parallel()
	in := install(t, "linux", "1.0.0")
	l := in.layout
	for name, input := range map[string]UninstallInput{
		"request without confirmation": {Layout: l, RemoveState: true},
		"wrong directory":              {Layout: l, RemoveState: true, ConfirmStateDir: l.Home},
		"confirmation without request": {Layout: l, ConfirmStateDir: l.StateDir},
		"label is a path":              {Layout: l, Labels: []string{"../../x"}},
	} {
		_, err := PlanUninstallation(input)
		if Code(err) != CodeInvalidInput {
			t.Fatalf("%s: got %v", name, err)
		}
	}
	in.wantState(t)

	plan, err := PlanUninstallation(UninstallInput{Layout: l, Labels: []string{ControllerLabel}, RemoveState: true, ConfirmStateDir: l.StateDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(context.Background(), plan, in.manager); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Lstat(l.StateDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("confirmed state removal left the state directory: %v", err)
	}
	if _, err := os.Lstat(l.Home); err != nil {
		t.Fatalf("the home directory must survive: %v", err)
	}
}

func TestPlanRefusals(t *testing.T) {
	t.Parallel()
	in := install(t, "linux", "1.1.0")
	l := in.layout
	build := func(version string, mutate func(*InstallInput, fixture)) error {
		f := newFixture(t, version, "linux")
		m := f.build(t)
		inst, err := Inspect(l)
		if err != nil {
			t.Fatal(err)
		}
		input := InstallInput{Layout: l, Manifest: m, SourceRoot: f.root, HostArch: "arm64", Services: []ServiceSpec{controllerSpec(l, m)}, Installed: inst}
		if mutate != nil {
			mutate(&input, f)
		}
		_, err = PlanInstallation(input)
		return err
	}
	if err := build("1.1.0", nil); err != nil {
		t.Fatalf("matching repeat install: %v", err)
	}
	wantCode(t, build("1.0.9", nil), CodeCapabilityUnsupported)
	wantCode(t, build("1.2.0-rc.1", func(i *InstallInput, _ fixture) { i.HostArch = "amd64" }), CodeCapabilityUnsupported)
	wantCode(t, build("1.2.0", func(i *InstallInput, _ fixture) { i.Services = nil }), CodeInvalidInput)
	wantCode(t, build("1.2.0", func(i *InstallInput, _ fixture) { i.Services[0].Owns = l.Home + "/elsewhere" }), CodeInvalidInput)
	wantCode(t, build("1.2.0", func(i *InstallInput, _ fixture) { i.Services[0].Executable = "/usr/local/bin/zatiti" }), CodeInvalidInput)
	wantCode(t, build("1.2.0", func(i *InstallInput, _ fixture) { i.Services[0].LogDirectory = l.DistRoot + "/logs" }), CodeConflict)
	wantCode(t, build("1.2.0", func(i *InstallInput, _ fixture) { i.Services[0].Manager = ManagerLaunchd }), CodeInvalidInput)
	wantCode(t, build("1.2.0", func(_ *InstallInput, f fixture) {
		writeFile(t, filepath.Join(f.root, "bin", "zatiti"), []byte("swapped after the manifest"), 0o755)
	}), CodeVerificationFailed)
	serenityElsewhere := func(i *InstallInput, _ fixture) {
		i.Services = append(i.Services, ServiceSpec{
			Manager: l.Manager, Role: RoleSerenity, Label: SerenityLabel, Description: "Serenity",
			Executable: "/usr/local/bin/serenity", Owns: l.Home + "/brains/main",
		})
	}
	wantCode(t, build("1.2.0", serenityElsewhere), CodeInvalidInput)
	in.wantState(t)
}

func TestSerenityServiceRunsThePinnedRuntime(t *testing.T) {
	t.Parallel()
	l := testLayout(t, "linux")
	f := newFixture(t, "1.0.0", "linux")
	m := f.build(t)
	serenity := ServiceSpec{
		Manager: l.Manager, Role: RoleSerenity, Label: SerenityLabel, Description: "Serenity",
		Executable: filepath.Join(l.Current, "serenity", "serenity"), Owns: filepath.Join(l.Home, "brains", "main"),
	}
	plan, err := PlanInstallation(InstallInput{Layout: l, Manifest: m, SourceRoot: f.root, HostArch: "arm64", Services: []ServiceSpec{controllerSpec(l, m), serenity}})
	if err != nil {
		t.Fatalf("PlanInstallation: %v", err)
	}
	mgr := &recordingManager{layout: l}
	if err := Apply(context.Background(), plan, mgr); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(mgr.calls) != 2 {
		t.Fatalf("service actions: %q", mgr.calls)
	}
	if err := AuditInstalled(l, []string{ControllerLabel, SerenityLabel}); err != nil {
		t.Fatalf("AuditInstalled: %v", err)
	}
}

func TestApplyRefusals(t *testing.T) {
	t.Parallel()

	t.Run("no service manager", func(t *testing.T) {
		t.Parallel()
		l := testLayout(t, "linux")
		plan, _ := planFor(t, l, "1.0.0")
		wantCode(t, Apply(context.Background(), plan, nil), CodePrerequisiteMissing)
		if _, err := os.Lstat(l.DistRoot); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("a refused plan wrote to disk")
		}
	})

	t.Run("stale plan", func(t *testing.T) {
		t.Parallel()
		l := testLayout(t, "linux")
		stale, _ := planFor(t, l, "1.0.0")
		fresh, _ := planFor(t, l, "1.0.0")
		mgr := &recordingManager{layout: l}
		if err := Apply(context.Background(), fresh, mgr); err != nil {
			t.Fatal(err)
		}
		wantCode(t, Apply(context.Background(), stale, mgr), CodeConflict)
		if err := AuditInstalled(l, []string{ControllerLabel}); err != nil {
			t.Fatalf("a refused stale plan damaged the installation: %v", err)
		}
	})

	t.Run("source changes after planning", func(t *testing.T) {
		t.Parallel()
		in := install(t, "linux", "1.0.0")
		plan, f := planFor(t, in.layout, "1.1.0")
		writeFile(t, filepath.Join(f.root, "bin", "zatiti"), []byte("synthetic controller 6.6.6"), 0o755)
		in.manager.calls = nil
		wantCode(t, Apply(context.Background(), plan, in.manager), CodeVerificationFailed)
		if len(in.manager.calls) != 0 {
			t.Fatalf("services were touched before the release was published: %q", in.manager.calls)
		}
		got, err := Inspect(in.layout)
		if err != nil || got.Current != "1.0.0" || len(got.Versions) != 1 {
			t.Fatalf("a failed upgrade changed the installation: %+v, %v", got, err)
		}
		// The same plan succeeds once the source is whole again.
		writeFile(t, filepath.Join(f.root, "bin", "zatiti"), []byte("synthetic controller 1.1.0"), 0o755)
		if err := Apply(context.Background(), plan, in.manager); err != nil {
			t.Fatalf("retry after repair: %v", err)
		}
	})

	t.Run("failure while services are down reloads them", func(t *testing.T) {
		t.Parallel()
		in := install(t, "linux", "1.0.0")
		plan, _ := planFor(t, in.layout, "1.1.0")
		// A non-empty directory where the link staging name goes makes the
		// link swap fail after the services were unloaded.
		writeFile(t, filepath.Join(in.layout.Current+".next", "blocker"), []byte("x"), 0o644)
		in.manager.calls = nil
		if err := Apply(context.Background(), plan, in.manager); err == nil {
			t.Fatal("Apply succeeded with a blocked link swap")
		}
		want := []string{"unload " + ControllerLabel + " @versions/1.0.0", "load " + ControllerLabel + " @versions/1.0.0"}
		if !reflect.DeepEqual(in.manager.calls, want) {
			t.Fatalf("service actions:\n got %q\nwant %q", in.manager.calls, want)
		}
		in.wantState(t)
	})

	t.Run("symlinked distribution directory", func(t *testing.T) {
		t.Parallel()
		l := testLayout(t, "linux")
		plan, _ := planFor(t, l, "1.0.0")
		victim := filepath.Join(l.Home, "victim")
		if err := os.MkdirAll(victim, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(l.DistRoot), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(victim, l.DistRoot); err != nil {
			t.Fatal(err)
		}
		wantCode(t, Apply(context.Background(), plan, &recordingManager{layout: l}), CodeConflict)
		entries, err := os.ReadDir(victim)
		if err != nil || len(entries) != 0 {
			t.Fatalf("the install wrote through a symlink: %v, %v", entries, err)
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		t.Parallel()
		l := testLayout(t, "linux")
		plan, _ := planFor(t, l, "1.0.0")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := Apply(ctx, plan, &recordingManager{layout: l}); !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	})
}

// A plan is plain data. Apply must not trust one that reaches outside the
// layout, whoever built it.
func TestApplyRejectsHandBuiltPlansThatEscapeTheLayout(t *testing.T) {
	t.Parallel()
	in := install(t, "linux", "1.0.0")
	l := in.layout
	outside := filepath.Join(l.Home, "documents")
	writeFile(t, filepath.Join(outside, "thesis.txt"), []byte("irreplaceable"), 0o644)
	tests := map[string]Plan{
		"remove tree outside":             {Kind: PlanUninstall, Layout: l, Steps: []Step{{Action: ActionRemoveTree, Path: outside}}},
		"remove state without the flag":   {Kind: PlanUninstall, Layout: l, Steps: []Step{{Action: ActionRemoveTree, Path: l.StateDir}}},
		"remove state from an upgrade":    {Kind: PlanUpgrade, Layout: l, PreviousVersion: "1.0.0", RemovesState: true, Steps: []Step{{Action: ActionRemoveTree, Path: l.StateDir}}},
		"remove below state with flag":    {Kind: PlanUninstall, Layout: l, RemovesState: true, Steps: []Step{{Action: ActionRemoveTree, Path: filepath.Dir(l.StateDir)}}},
		"write into state":                {Kind: PlanInstall, Layout: l, Steps: []Step{{Action: ActionWriteFile, Path: in.state, Mode: 0o644}}},
		"write below the launcher folder": {Kind: PlanInstall, Layout: l, Steps: []Step{{Action: ActionWriteFile, Path: filepath.Join(l.UnitDir, "sub", "x.service"), Mode: 0o644}}},
		"copy out of state":               {Kind: PlanInstall, Layout: l, Steps: []Step{{Action: ActionCopyFile, Path: filepath.Join(l.DistRoot, "leak"), Source: in.state, Mode: 0o644}}},
		"unclean path":                    {Kind: PlanInstall, Layout: l, Steps: []Step{{Action: ActionWriteFile, Path: l.DistRoot + "/../documents/x", Mode: 0o644}}},
		"link to an arbitrary target":     {Kind: PlanInstall, Layout: l, Steps: []Step{{Action: ActionSwapLink, Path: l.Current, Target: outside}}},
		"world writable file":             {Kind: PlanInstall, Layout: l, Steps: []Step{{Action: ActionWriteFile, Path: filepath.Join(l.DistRoot, "x"), Mode: 0o666}}},
		"setuid file":                     {Kind: PlanInstall, Layout: l, Steps: []Step{{Action: ActionWriteFile, Path: filepath.Join(l.DistRoot, "x"), Mode: 0o755 | os.ModeSetuid}}},
		"unknown action":                  {Kind: PlanInstall, Layout: l, Steps: []Step{{Action: "exec", Path: filepath.Join(l.DistRoot, "x")}}},
		"unknown kind":                    {Kind: "repair", Layout: l},
		"launcher outside the folder":     {Kind: PlanUninstall, Layout: l, Before: []ServiceAction{{Verb: VerbUnload, Label: ControllerLabel, UnitPath: filepath.Join(outside, "x.service")}}},
		"unknown verb":                    {Kind: PlanUninstall, Layout: l, Before: []ServiceAction{{Verb: "mask", Label: ControllerLabel, UnitPath: filepath.Join(l.UnitDir, ControllerLabel+".service")}}},
	}
	for name, plan := range tests {
		t.Run(name, func(t *testing.T) {
			wantCode(t, Apply(context.Background(), plan, in.manager), CodeInvalidInput)
		})
	}
	in.wantState(t)
	if raw, err := os.ReadFile(filepath.Join(outside, "thesis.txt")); err != nil || string(raw) != "irreplaceable" {
		t.Fatalf("a file outside the layout was touched: %q, %v", raw, err)
	}
}

func TestAuditInstalledFindsLoosePermissionsAndTampering(t *testing.T) {
	t.Parallel()
	in := install(t, "darwin", "1.0.0")
	l := in.layout
	active := l.VersionDir("1.0.0")
	if err := os.Chmod(filepath.Join(active, "bin"), 0o777); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(active, "LICENSE"), []byte("All rights reserved."), 0o644)
	if err := os.Symlink("/", filepath.Join(l.DistRoot, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(l.UnitDir, ControllerLabel+".plist"), 0o666); err != nil {
		t.Fatal(err)
	}
	perr := wantFault(t, AuditInstalled(l, []string{ControllerLabel, SerenityLabel}), CodeVerificationFailed)
	wantFinding(t, perr, "versions/1.0.0/bin", "writable")
	wantFinding(t, perr, "LICENSE", "digest")
	wantFinding(t, perr, "escape", "symlink")
	wantFinding(t, perr, ControllerLabel+".plist", "writable")
	wantFinding(t, perr, SerenityLabel+".plist", "missing")

	wantCode(t, AuditInstalled(testLayout(t, "darwin"), nil), CodeNotFound)
}

func TestLayout(t *testing.T) {
	t.Parallel()
	l, err := NewLayout(LayoutInput{OS: "linux", Home: "/home/operator", StateDir: "/home/operator/.local/state/zatiti", DataHome: "/data/operator", ConfigHome: "/etc/operator"})
	if err != nil {
		t.Fatal(err)
	}
	if l.DistRoot != "/data/operator/zatiti-dist" || l.UnitDir != "/etc/operator/systemd/user" || l.Manager != ManagerSystemdUser {
		t.Fatalf("XDG overrides ignored: %+v", l)
	}
	d, err := NewLayout(LayoutInput{OS: "darwin", Home: "/Users/operator", StateDir: "/Users/operator/Library/Application Support/Zatiti"})
	if err != nil {
		t.Fatal(err)
	}
	if d.UnitDir != "/Users/operator/Library/LaunchAgents" || d.Manager != ManagerLaunchd {
		t.Fatalf("darwin layout: %+v", d)
	}
	tests := []struct {
		name string
		in   LayoutInput
		code string
	}{
		{"windows", LayoutInput{OS: "windows", Home: "/home/operator", StateDir: "/home/operator/state"}, CodeCapabilityUnsupported},
		{"relative home", LayoutInput{OS: "linux", Home: "operator", StateDir: "/home/operator/state"}, CodeInvalidInput},
		{"unclean state", LayoutInput{OS: "linux", Home: "/home/operator", StateDir: "/home/operator/../state"}, CodeInvalidInput},
		{"state is home", LayoutInput{OS: "linux", Home: "/home/operator", StateDir: "/home/operator"}, CodeInvalidInput},
		{"state is an ancestor of home", LayoutInput{OS: "linux", Home: "/home/operator/nested", StateDir: "/home/operator"}, CodeInvalidInput},
		{"state is shallow", LayoutInput{OS: "linux", Home: "/home/operator", StateDir: "/srv"}, CodeInvalidInput},
		{"distribution inside state", LayoutInput{OS: "linux", Home: "/home/operator", StateDir: "/home/operator/.local/share"}, CodeConflict},
		{"state inside distribution", LayoutInput{OS: "linux", Home: "/home/operator", StateDir: "/home/operator/.local/share/zatiti-dist/state"}, CodeConflict},
		{"launchers inside state", LayoutInput{OS: "linux", Home: "/home/operator", StateDir: "/home/operator/.config"}, CodeConflict},
		{"distribution override inside launchers", LayoutInput{OS: "darwin", Home: "/Users/operator", StateDir: "/Users/operator/state", DistRoot: "/Users/operator/Library/LaunchAgents/dist"}, CodeConflict},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewLayout(tc.in)
			wantCode(t, err, tc.code)
		})
	}
}

func TestCompareVersions(t *testing.T) {
	t.Parallel()
	ordered := []string{"0.9.9", "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.10", "1.2.0", "10.0.0", "99999999999999999999999.0.0"}
	for i := range ordered {
		for j := range ordered {
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := compareVersions(ordered[i], ordered[j]); got != want {
				t.Fatalf("compareVersions(%s, %s) = %d, want %d", ordered[i], ordered[j], got, want)
			}
		}
	}
}

func TestInspectRejectsAForeignActiveLink(t *testing.T) {
	t.Parallel()
	in := install(t, "linux", "1.0.0")
	l := in.layout
	if err := os.Remove(l.Current); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(l.StateDir, l.Current); err != nil {
		t.Fatal(err)
	}
	_, err := Inspect(l)
	wantCode(t, err, CodeConflict)
	if err := os.Remove(l.Current); err != nil {
		t.Fatal(err)
	}
	writeFile(t, l.Current, []byte("not a link"), 0o644)
	_, err = Inspect(l)
	wantCode(t, err, CodeConflict)
}
