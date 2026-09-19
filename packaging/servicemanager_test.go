package packaging

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// scriptedRunner records every command and fails the ones whose first
// argument is listed.
type scriptedRunner struct {
	calls []string
	fail  map[string]error
}

func (r *scriptedRunner) Run(_ context.Context, name string, args []string) error {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	for _, arg := range args {
		if err, ok := r.fail[arg]; ok {
			return err
		}
	}
	return nil
}

func TestLaunchdManagerCommands(t *testing.T) {
	t.Parallel()
	action := ServiceAction{Label: ControllerLabel, UnitPath: "/Users/operator/Library/LaunchAgents/com.zatiti.controller.plist"}
	notLoaded := map[string]error{"print": errors.New("exit status 113")}
	tests := []struct {
		name string
		verb string
		fail map[string]error
		want []string
	}{
		{"load when absent", VerbLoad, notLoaded, []string{
			"/bin/launchctl print gui/501/com.zatiti.controller",
			"/bin/launchctl bootstrap gui/501 " + action.UnitPath,
		}},
		{"load when loaded is a no-op", VerbLoad, nil, []string{"/bin/launchctl print gui/501/com.zatiti.controller"}},
		{"unload when loaded", VerbUnload, nil, []string{
			"/bin/launchctl print gui/501/com.zatiti.controller",
			"/bin/launchctl bootout gui/501/com.zatiti.controller",
		}},
		{"unload when absent is a no-op", VerbUnload, notLoaded, []string{"/bin/launchctl print gui/501/com.zatiti.controller"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := &scriptedRunner{fail: tc.fail}
			a := action
			a.Verb = tc.verb
			if err := (LaunchdManager{Launchctl: DefaultLaunchctl, UID: 501, Runner: r}).Do(context.Background(), a); err != nil {
				t.Fatalf("Do: %v", err)
			}
			if !reflect.DeepEqual(r.calls, tc.want) {
				t.Fatalf("commands:\n got %q\nwant %q", r.calls, tc.want)
			}
		})
	}
}

func TestSystemdManagerCommands(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	present := filepath.Join(dir, "com.zatiti.controller.service")
	writeFile(t, present, []byte("[Unit]\n"), 0o644)

	r := &scriptedRunner{}
	m := SystemdUserManager{Systemctl: DefaultSystemctl, Runner: r}
	for _, a := range []ServiceAction{
		{Verb: VerbLoad, Label: ControllerLabel, UnitPath: present},
		{Verb: VerbUnload, Label: ControllerLabel, UnitPath: present},
		{Verb: VerbUnload, Label: SerenityLabel, UnitPath: filepath.Join(dir, "absent.service")},
	} {
		if err := m.Do(context.Background(), a); err != nil {
			t.Fatalf("Do %s: %v", a.Verb, err)
		}
	}
	want := []string{
		"/usr/bin/systemctl --user daemon-reload",
		"/usr/bin/systemctl --user enable --now com.zatiti.controller.service",
		"/usr/bin/systemctl --user disable --now com.zatiti.controller.service",
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("commands:\n got %q\nwant %q", r.calls, want)
	}
}

func TestManagerFailuresAreCodedAndRedacted(t *testing.T) {
	t.Parallel()
	a := ServiceAction{Verb: VerbLoad, Label: ControllerLabel, UnitPath: "/home/operator/.config/systemd/user/com.zatiti.controller.service"}

	refused := &scriptedRunner{fail: map[string]error{"enable": errors.New("unit output with /home/operator/private detail")}}
	perr := wantFault(t, SystemdUserManager{Systemctl: DefaultSystemctl, Runner: refused}.Do(context.Background(), a), CodeInternalError)
	if strings.Contains(perr.Error(), "/home/operator") {
		t.Fatalf("message leaks the cause: %s", perr.Error())
	}

	missing := &scriptedRunner{fail: map[string]error{"daemon-reload": exec.ErrNotFound}}
	wantCode(t, SystemdUserManager{Systemctl: DefaultSystemctl, Runner: missing}.Do(context.Background(), a), CodePrerequisiteMissing)

	wantCode(t, SystemdUserManager{}.Do(context.Background(), a), CodePrerequisiteMissing)
	wantCode(t, LaunchdManager{Launchctl: DefaultLaunchctl, UID: -1, Runner: refused}.Do(context.Background(), a), CodePrerequisiteMissing)
	a.Verb = "mask"
	wantCode(t, SystemdUserManager{Systemctl: DefaultSystemctl, Runner: refused}.Do(context.Background(), a), CodeInvalidInput)
}

// ExecRunner is the one place this package starts a process. It must not
// hand the caller's environment, where credentials commonly live, to the
// child.
func TestExecRunnerDoesNotInheritTheEnvironment(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh on this host")
	}
	t.Setenv("ZATITI_TEST_LEAK", "synthetic-credential")
	ctx := context.Background()
	leaked := []string{"-c", `test -n "$ZATITI_TEST_LEAK"`}
	if err := (ExecRunner{}).Run(ctx, "/bin/sh", leaked); err == nil {
		t.Fatal("the child saw the caller's environment")
	}
	if err := (ExecRunner{Env: []string{"ZATITI_TEST_LEAK=explicit"}}).Run(ctx, "/bin/sh", leaked); err != nil {
		t.Fatalf("an explicitly passed variable did not reach the child: %v", err)
	}
	err := (ExecRunner{}).Run(ctx, filepath.Join(t.TempDir(), "absent-tool"), nil)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("got %v, want a not-exist error", err)
	}
}
