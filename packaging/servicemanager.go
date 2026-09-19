package packaging

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"strconv"
)

// Runner runs one service manager command and reports only whether it
// succeeded. Implementations must not pass the caller's environment through.
type Runner interface {
	Run(ctx context.Context, name string, args []string) error
}

// ExecRunner runs commands with a fixed, minimal environment. Command output
// is discarded, not captured: it never reaches an error message or a log.
type ExecRunner struct {
	// Env is the complete environment of the child, for example the
	// XDG_RUNTIME_DIR a user systemd connection needs. Nothing is
	// inherited from the calling process.
	Env []string
}

// Run implements Runner.
func (r ExecRunner) Run(ctx context.Context, name string, args []string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	// A non-nil, possibly empty slice: a nil Env would inherit the caller's.
	cmd.Env = append([]string{}, r.Env...)
	return cmd.Run()
}

// Default service manager tool paths. They are absolute so that a PATH entry
// cannot substitute another program.
const (
	DefaultLaunchctl = "/bin/launchctl"
	DefaultSystemctl = "/usr/bin/systemctl"
)

// LaunchdManager drives launchctl in the per-user GUI domain.
//
// The launchctl subcommands used here (print, bootstrap, bootout) are the
// documented modern interface. Their behavior against a real launchd is not
// proven by this package's tests, which substitute the Runner.
type LaunchdManager struct {
	Launchctl string
	UID       int
	Runner    Runner
}

// Do implements ServiceManager.
func (m LaunchdManager) Do(ctx context.Context, a ServiceAction) error {
	if m.Runner == nil || m.Launchctl == "" || m.UID < 0 {
		return errf(CodePrerequisiteMissing, "the launchd manager needs a runner, the launchctl path and the user id")
	}
	domain := "gui/" + strconv.Itoa(m.UID)
	loaded := m.Runner.Run(ctx, m.Launchctl, []string{"print", domain + "/" + a.Label}) == nil
	switch a.Verb {
	case VerbLoad:
		if loaded {
			return nil
		}
		return managerErr(a, m.Runner.Run(ctx, m.Launchctl, []string{"bootstrap", domain, a.UnitPath}))
	case VerbUnload:
		if !loaded {
			return nil
		}
		return managerErr(a, m.Runner.Run(ctx, m.Launchctl, []string{"bootout", domain + "/" + a.Label}))
	}
	return errf(CodeInvalidInput, "unknown service verb")
}

// SystemdUserManager drives systemctl --user. As with LaunchdManager, the
// commands are the documented interface and unproven here against a real
// systemd.
type SystemdUserManager struct {
	Systemctl string
	Runner    Runner
}

// Do implements ServiceManager.
func (m SystemdUserManager) Do(ctx context.Context, a ServiceAction) error {
	if m.Runner == nil || m.Systemctl == "" {
		return errf(CodePrerequisiteMissing, "the systemd manager needs a runner and the systemctl path")
	}
	unit := a.Label + ".service"
	switch a.Verb {
	case VerbLoad:
		if err := m.Runner.Run(ctx, m.Systemctl, []string{"--user", "daemon-reload"}); err != nil {
			return managerErr(a, err)
		}
		return managerErr(a, m.Runner.Run(ctx, m.Systemctl, []string{"--user", "enable", "--now", unit}))
	case VerbUnload:
		// systemctl fails on a unit it has never seen; an absent
		// launcher file means there is nothing to unload.
		if _, err := os.Lstat(a.UnitPath); errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return managerErr(a, m.Runner.Run(ctx, m.Systemctl, []string{"--user", "disable", "--now", unit}))
	}
	return errf(CodeInvalidInput, "unknown service verb")
}

func managerErr(a ServiceAction, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return errWrap(CodePrerequisiteMissing, "the service manager tool is not installed", err)
	}
	return errWrap(CodeInternalError, "the service manager refused to "+a.Verb+" "+a.Label, err)
}
