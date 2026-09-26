package packaging

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// layoutFlags binds the installation layout inputs shared by install,
// uninstall, audit and inspect. Every value comes from an explicit flag;
// like the rest of this package, the driver reads no environment variable of
// its own to resolve a layout.
type layoutFlags struct {
	distribution string
	goos         string
	home         string
	stateDir     string
	dataHome     string
	configHome   string
	distRoot     string
	appsDir      string
}

func (l *layoutFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&l.distribution, "distribution", "", "controller or desktop (required)")
	fs.StringVar(&l.goos, "os", "", "darwin or linux (required)")
	fs.StringVar(&l.home, "home", "", "the operator's home directory (required)")
	fs.StringVar(&l.stateDir, "state-dir", "", "the installation state directory (required)")
	fs.StringVar(&l.dataHome, "data-home", "", "linux XDG data home override")
	fs.StringVar(&l.configHome, "config-home", "", "linux XDG config home override, controller only")
	fs.StringVar(&l.distRoot, "dist-root", "", "distribution directory override")
	fs.StringVar(&l.appsDir, "applications-dir", "", "desktop applications/launcher directory override")
}

func (l layoutFlags) resolve() (Layout, error) {
	switch l.distribution {
	case DistributionController:
		return NewLayout(LayoutInput{OS: l.goos, Home: l.home, DataHome: l.dataHome, ConfigHome: l.configHome, DistRoot: l.distRoot, StateDir: l.stateDir})
	case DistributionDesktop:
		return NewDesktopLayout(DesktopLayoutInput{OS: l.goos, Home: l.home, DataHome: l.dataHome, ApplicationsDir: l.appsDir, DistRoot: l.distRoot, StateDir: l.stateDir})
	default:
		return Layout{}, errf(CodeInvalidInput, "--distribution must be %s or %s", DistributionController, DistributionDesktop)
	}
}

// serviceSpecDoc is the JSON shape of one entry in a controller install's
// --services file: ServiceSpec with its own snake_case tags. Manager comes
// from the resolved layout, not the file, so a service specification cannot
// disagree with the layout it is installed into.
type serviceSpecDoc struct {
	Role             string            `json:"role"`
	Label            string            `json:"label"`
	Description      string            `json:"description"`
	Executable       string            `json:"executable"`
	Arguments        []string          `json:"arguments,omitempty"`
	Owns             string            `json:"owns"`
	WorkingDirectory string            `json:"working_directory,omitempty"`
	Environment      map[string]string `json:"environment,omitempty"`
	LogDirectory     string            `json:"log_directory,omitempty"`
}

func readServiceSpecs(path, manager string) ([]ServiceSpec, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errWrap(CodeNotFound, "the service specification could not be read", err)
	}
	var docs []serviceSpecDoc
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&docs); err != nil {
		return nil, errWrap(CodeInvalidInput, "the service specification is not valid for its schema", err)
	}
	specs := make([]ServiceSpec, 0, len(docs))
	for _, d := range docs {
		specs = append(specs, ServiceSpec{
			Manager: manager, Role: d.Role, Label: d.Label, Description: d.Description,
			Executable: d.Executable, Arguments: d.Arguments, Owns: d.Owns,
			WorkingDirectory: d.WorkingDirectory, Environment: d.Environment, LogDirectory: d.LogDirectory,
		})
	}
	return specs, nil
}

// buildServiceManager resolves the driver's --service-manager flag into a
// real ServiceManager. "none" returns a nil manager, valid only for a plan
// that asks nothing of one (a desktop plan, or a controller plan applied
// with --service-manager none for a filesystem-only rehearsal). The default
// tool paths are DefaultLaunchctl and DefaultSystemctl, the same absolute,
// non-PATH-resolved paths servicemanager.go always used; --launchctl and
// --systemctl are explicit operator overrides, most useful for pointing the
// driver at a recording stand-in while testing or auditing this tool.
func buildServiceManager(kind, goos, launchctlPath, systemctlPath string, uid int, env []string) (ServiceManager, error) {
	if kind == "" || kind == "auto" {
		switch goos {
		case "darwin":
			kind = "launchd"
		case "linux":
			kind = "systemd"
		default:
			return nil, errf(CodeCapabilityUnsupported, "--service-manager auto needs --os darwin or linux")
		}
	}
	switch kind {
	case "launchd":
		if launchctlPath == "" {
			launchctlPath = DefaultLaunchctl
		}
		if uid < 0 {
			uid = os.Getuid()
		}
		return LaunchdManager{Launchctl: launchctlPath, UID: uid, Runner: ExecRunner{Env: env}}, nil
	case "systemd":
		if systemctlPath == "" {
			systemctlPath = DefaultSystemctl
		}
		return SystemdUserManager{Systemctl: systemctlPath, Runner: ExecRunner{Env: env}}, nil
	case "none":
		return nil, nil
	default:
		return nil, errf(CodeInvalidInput, "--service-manager must be launchd, systemd, none or auto")
	}
}

// A direct Mac mutation must join the bootstrap's release-channel lock before
// inspecting or planning installed state. Apply takes the separate install
// lock inside this critical section, so the only lock order is bootstrap then
// install. Dry runs and Linux plans do not modify that Mac release channel.
func lockDirectMacApply(goos, home string, apply bool) (func(), error) {
	if !apply || goos != "darwin" {
		return func() {}, nil
	}
	fence, err := NewFileMacSequenceWatermark(home)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return fence.Lock(ctx)
}

// cliInstall plans, and with --apply performs, a first install or an
// upgrade of one distribution. It refuses to proceed on an unverified
// signature unless the caller explicitly accepts that with --allow-unsigned,
// so a tampered manifest, a tampered artifact it lists, a tampered secure
// helper declaration or a tampered profile claim is refused here, before any
// service is touched: an invalid signature stops it outright, and a valid
// signature over a manifest whose tree no longer matches (a tampered binary)
// is caught by VerifyTree inside PlanInstallation/PlanDesktopInstallation,
// both called before Apply ever runs.
func cliInstall(args []string, stdout io.Writer) error {
	fs := newFlagSet("install")
	var lf layoutFlags
	lf.register(fs)
	source := fs.String("source", "", "the staged, manifest-carrying distribution tree to install (required)")
	hostArch := fs.String("host-arch", "", "the installing host's architecture, e.g. arm64 or amd64 (required)")
	servicesPath := fs.String("services", "", "controller only: a JSON array of service specifications")
	sigPath := fs.String("sig", "", "defaults to manifest.sig.json next to the manifest")
	var trustedPaths stringListFlag
	fs.Var(&trustedPaths, "trusted", "a PEM PKIX Ed25519 public key trusted to sign the release; repeatable")
	allowUnsigned := fs.Bool("allow-unsigned", false, "install without a verified signature; unsafe, local use only")
	serviceManagerKind := fs.String("service-manager", "auto", "launchd, systemd, none or auto (controller only)")
	launchctlPath := fs.String("launchctl", "", "overrides the default launchctl path")
	systemctlPath := fs.String("systemctl", "", "overrides the default systemctl path")
	uid := fs.Int("uid", -1, "the GUI domain user id for launchd; defaults to the running process's uid")
	var env stringListFlag
	fs.Var(&env, "env", "NAME=VALUE passed to the service manager tool's subprocess; repeatable")
	apply := fs.Bool("apply", false, "apply the plan; omit for a dry run that changes nothing")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *source == "" || *hostArch == "" {
		return errf(CodeInvalidInput, "install: --source and --host-arch are required")
	}
	if !*allowUnsigned && len(trustedPaths) == 0 {
		return errf(CodeInvalidInput, "install: a signature must be verified before install; pass --trusted (and optionally --sig), or --allow-unsigned to install without one")
	}
	unlock, err := lockDirectMacApply(lf.goos, lf.home, *apply)
	if err != nil {
		return err
	}
	defer unlock()
	absSource, err := filepath.Abs(*source)
	if err != nil {
		return errWrap(CodeInvalidInput, "the source tree could not be resolved", err)
	}
	manifestRaw, err := os.ReadFile(filepath.Join(absSource, ManifestFileName))
	if err != nil {
		return errWrap(CodeNotFound, "the source tree has no manifest", err)
	}
	if !*allowUnsigned {
		sigFile := *sigPath
		if sigFile == "" {
			sigFile = filepath.Join(absSource, SignatureFileName)
		}
		sigRaw, err := os.ReadFile(sigFile)
		if err != nil {
			return errWrap(CodeNotFound, "the release signature could not be read", err)
		}
		sig, err := DecodeSignature(sigRaw)
		if err != nil {
			return err
		}
		trusted := make([]ed25519.PublicKey, 0, len(trustedPaths))
		for _, p := range trustedPaths {
			pub, err := loadEd25519PublicKey(p)
			if err != nil {
				return err
			}
			trusted = append(trusted, pub)
		}
		if err := VerifySignature(manifestRaw, sig, trusted); err != nil {
			return err
		}
	}
	m, err := Decode(manifestRaw)
	if err != nil {
		return err
	}
	layout, err := lf.resolve()
	if err != nil {
		return err
	}
	installed, err := Inspect(layout)
	if err != nil {
		return err
	}
	var plan Plan
	switch lf.distribution {
	case DistributionController:
		if *servicesPath == "" {
			return errf(CodeInvalidInput, "install: --services is required for a controller distribution")
		}
		specs, err := readServiceSpecs(*servicesPath, layout.Manager)
		if err != nil {
			return err
		}
		plan, err = PlanInstallation(InstallInput{Layout: layout, Manifest: m, SourceRoot: absSource, HostArch: *hostArch, Services: specs, Installed: installed})
		if err != nil {
			return err
		}
	case DistributionDesktop:
		plan, err = PlanDesktopInstallation(DesktopInstallInput{Layout: layout, Manifest: m, SourceRoot: absSource, HostArch: *hostArch, Installed: installed})
		if err != nil {
			return err
		}
	default:
		return errf(CodeInvalidInput, "--distribution must be %s or %s", DistributionController, DistributionDesktop)
	}
	result := map[string]any{
		"kind": plan.Kind, "version": plan.Version, "previous_version": plan.PreviousVersion,
		"prepare_steps": len(plan.Prepare), "steps": len(plan.Steps), "notices": plan.Notices, "applied": false,
	}
	if *apply {
		mgr, err := buildServiceManager(*serviceManagerKind, lf.goos, *launchctlPath, *systemctlPath, *uid, env)
		if err != nil {
			return err
		}
		if err := Apply(context.Background(), plan, mgr); err != nil {
			return err
		}
		result["applied"] = true
	}
	return writeJSONDoc(stdout, result)
}

// cliUninstall plans, and with --apply performs, removal of one installed
// distribution. State is preserved unless --remove-state repeats the exact
// state directory in --confirm-state-dir, exactly as PlanUninstallation
// requires.
func cliUninstall(args []string, stdout io.Writer) error {
	fs := newFlagSet("uninstall")
	var lf layoutFlags
	lf.register(fs)
	labelsCSV := fs.String("labels", "", "controller only: comma-separated service labels to remove")
	removeState := fs.Bool("remove-state", false, "remove the state directory; needs --confirm-state-dir")
	confirmStateDir := fs.String("confirm-state-dir", "", "must repeat the exact state directory to remove it")
	serviceManagerKind := fs.String("service-manager", "auto", "launchd, systemd, none or auto (controller only)")
	launchctlPath := fs.String("launchctl", "", "overrides the default launchctl path")
	systemctlPath := fs.String("systemctl", "", "overrides the default systemctl path")
	uid := fs.Int("uid", -1, "the GUI domain user id for launchd; defaults to the running process's uid")
	var env stringListFlag
	fs.Var(&env, "env", "NAME=VALUE passed to the service manager tool's subprocess; repeatable")
	apply := fs.Bool("apply", false, "apply the plan; omit for a dry run that changes nothing")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	unlock, err := lockDirectMacApply(lf.goos, lf.home, *apply)
	if err != nil {
		return err
	}
	defer unlock()
	layout, err := lf.resolve()
	if err != nil {
		return err
	}
	var plan Plan
	switch lf.distribution {
	case DistributionController:
		var labels []string
		if *labelsCSV != "" {
			labels = strings.Split(*labelsCSV, ",")
		}
		plan, err = PlanUninstallation(UninstallInput{Layout: layout, Labels: labels, RemoveState: *removeState, ConfirmStateDir: *confirmStateDir})
		if err != nil {
			return err
		}
	case DistributionDesktop:
		m, merr := readActiveManifest(layout)
		if merr != nil {
			return merr
		}
		plan, err = PlanDesktopUninstallation(layout, m)
		if err != nil {
			return err
		}
	default:
		return errf(CodeInvalidInput, "--distribution must be %s or %s", DistributionController, DistributionDesktop)
	}
	result := map[string]any{"kind": plan.Kind, "removes_state": plan.RemovesState, "steps": len(plan.Steps), "notices": plan.Notices, "applied": false}
	if *apply {
		mgr, err := buildServiceManager(*serviceManagerKind, lf.goos, *launchctlPath, *systemctlPath, *uid, env)
		if err != nil {
			return err
		}
		if err := Apply(context.Background(), plan, mgr); err != nil {
			return err
		}
		result["applied"] = true
	}
	return writeJSONDoc(stdout, result)
}

// readActiveManifest reads and decodes the manifest of the active release in
// l: the same lookup AuditInstalled and AuditDesktopInstalled already use,
// needed here because PlanDesktopUninstallation takes the installed
// manifest directly (it names the launcher path to remove) rather than
// re-deriving it from the layout itself.
func readActiveManifest(l Layout) (Manifest, error) {
	inst, err := Inspect(l)
	if err != nil {
		return Manifest{}, err
	}
	if inst.Current == "" {
		return Manifest{}, errf(CodeNotFound, "no release is installed in this layout")
	}
	raw, err := readSmallFile(filepath.Join(l.VersionDir(inst.Current), ManifestFileName))
	if err != nil {
		return Manifest{}, errWrap(CodeVerificationFailed, "the active release has no manifest", err)
	}
	return Decode(raw)
}

// cliService loads, unloads or restarts one launcher directly, without a
// plan. "restart" is unload followed by load through the same manager, the
// same two idempotent calls an upgrade already makes; this command exists so
// an operator (or a test) can drive that outside of an install or upgrade,
// for example after editing a launcher by hand.
func cliService(args []string, stdout io.Writer) error {
	fs := newFlagSet("service")
	verb := fs.String("verb", "", "load, unload or restart (required)")
	label := fs.String("label", "", "the launcher label (required)")
	unitPath := fs.String("unit", "", "the launcher file path (required)")
	goos := fs.String("os", "", "darwin or linux (required)")
	kind := fs.String("service-manager", "auto", "launchd, systemd or auto")
	launchctlPath := fs.String("launchctl", "", "overrides the default launchctl path")
	systemctlPath := fs.String("systemctl", "", "overrides the default systemctl path")
	uid := fs.Int("uid", -1, "the GUI domain user id for launchd; defaults to the running process's uid")
	var env stringListFlag
	fs.Var(&env, "env", "NAME=VALUE passed to the service manager tool's subprocess; repeatable")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *label == "" || *unitPath == "" {
		return errf(CodeInvalidInput, "service: --label and --unit are required")
	}
	mgr, err := buildServiceManager(*kind, *goos, *launchctlPath, *systemctlPath, *uid, env)
	if err != nil {
		return err
	}
	if mgr == nil {
		return errf(CodeInvalidInput, "service: --service-manager none cannot act")
	}
	absUnit, err := filepath.Abs(*unitPath)
	if err != nil {
		return errWrap(CodeInvalidInput, "the unit path could not be resolved", err)
	}
	var steps []string
	do := func(verb string) error {
		if err := mgr.Do(context.Background(), ServiceAction{Verb: verb, Label: *label, UnitPath: absUnit}); err != nil {
			return err
		}
		steps = append(steps, verb)
		return nil
	}
	switch *verb {
	case VerbLoad, VerbUnload:
		if err := do(*verb); err != nil {
			return err
		}
	case "restart":
		if err := do(VerbUnload); err != nil {
			return err
		}
		if err := do(VerbLoad); err != nil {
			return err
		}
	default:
		return errf(CodeInvalidInput, "service: --verb must be load, unload or restart")
	}
	return writeJSONDoc(stdout, map[string]any{"label": *label, "steps": steps})
}

// cliAudit checks an installed release against its manifest: it is
// AuditInstalled or AuditDesktopInstalled behind a flag parser. A finding
// (loose permissions, a tampered file, a missing launcher) surfaces through
// the driver's normal verification_failed error path, findings included.
func cliAudit(args []string, stdout io.Writer) error {
	fs := newFlagSet("audit")
	var lf layoutFlags
	lf.register(fs)
	labelsCSV := fs.String("labels", "", "controller only: comma-separated service labels to check")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	layout, err := lf.resolve()
	if err != nil {
		return err
	}
	switch lf.distribution {
	case DistributionController:
		var labels []string
		if *labelsCSV != "" {
			labels = strings.Split(*labelsCSV, ",")
		}
		if err := AuditInstalled(layout, labels); err != nil {
			return err
		}
	case DistributionDesktop:
		if err := AuditDesktopInstalled(layout); err != nil {
			return err
		}
	default:
		return errf(CodeInvalidInput, "--distribution must be %s or %s", DistributionController, DistributionDesktop)
	}
	return writeJSONDoc(stdout, map[string]any{"ok": true})
}

// cliInspect lists the releases installed in a layout: it is Inspect behind
// a flag parser.
func cliInspect(args []string, stdout io.Writer) error {
	fs := newFlagSet("inspect")
	var lf layoutFlags
	lf.register(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	layout, err := lf.resolve()
	if err != nil {
		return err
	}
	inst, err := Inspect(layout)
	if err != nil {
		return err
	}
	return writeJSONDoc(stdout, map[string]any{"current": inst.Current, "versions": inst.Versions})
}
