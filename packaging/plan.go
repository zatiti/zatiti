package packaging

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Plan kinds.
const (
	PlanInstall   = "install"
	PlanUpgrade   = "upgrade"
	PlanUninstall = "uninstall"
)

// Step actions.
const (
	ActionEnsureDir  = "ensure_dir"
	ActionResetDir   = "reset_dir"
	ActionCopyFile   = "copy_file"
	ActionWriteFile  = "write_file"
	ActionPublishDir = "publish_dir"
	ActionSwapLink   = "swap_link"
	ActionRemoveFile = "remove_file"
	ActionRemoveTree = "remove_tree"
	// ActionExtractBundle unpacks a verified bundle archive (Source, with
	// SHA256 and Size) into Path, checks the executable named by Content,
	// and publishes the result at Target.
	ActionExtractBundle = "extract_bundle"
)

// Service verbs. A ServiceManager must make both idempotent: loading a
// loaded service and unloading an absent one succeed.
const (
	VerbLoad   = "load"
	VerbUnload = "unload"
)

// Step is one filesystem change. Apply re-validates every step against the
// plan's layout, so a hand-built plan cannot reach outside it.
type Step struct {
	Action string
	Path   string
	// Source, SHA256 and Size describe a copy_file source.
	Source string
	SHA256 string
	Size   int64
	Mode   fs.FileMode
	// Target is the final directory for publish_dir and the relative link
	// target for swap_link.
	Target  string
	Content []byte
}

// ServiceAction asks the service manager to load or unload one launcher.
type ServiceAction struct {
	Verb     string
	Label    string
	UnitPath string
}

// Plan is an ordered, inspectable description of an install, upgrade or
// uninstall. Building a plan changes nothing on disk.
type Plan struct {
	Kind            string
	Layout          Layout
	Version         string
	PreviousVersion string
	// Prepare runs while the services are still up: it stages and publishes
	// the release directory without touching the active release.
	Prepare []Step
	Before  []ServiceAction
	// Steps run while the services are down: the active link, the
	// launchers, and every removal.
	Steps []Step
	After []ServiceAction
	// RemovesState is true only for an uninstall whose operator confirmed
	// the exact state directory.
	RemovesState bool
	Notices      []string
}

// Installed is what Inspect finds in a layout.
type Installed struct {
	Current  string
	Versions []string
}

// Inspect reads the installed releases. A missing distribution directory is
// an empty installation, not an error.
func Inspect(l Layout) (Installed, error) {
	if err := l.validate(); err != nil {
		return Installed{}, err
	}
	var inst Installed
	entries, err := os.ReadDir(l.Versions)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Installed{}, errWrap(CodeInternalError, "the installed releases could not be listed", err)
	}
	for _, e := range entries {
		if e.IsDir() && versionPattern.MatchString(e.Name()) {
			inst.Versions = append(inst.Versions, e.Name())
		}
	}
	sort.Slice(inst.Versions, func(i, j int) bool { return compareVersions(inst.Versions[i], inst.Versions[j]) < 0 })
	target, err := os.Readlink(l.Current)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return inst, nil
	case err != nil:
		return Installed{}, errf(CodeConflict, "the active release link exists but is not a link this package manages")
	}
	version := strings.TrimPrefix(filepath.ToSlash(target), dirNameVersions+"/")
	if filepath.ToSlash(target) != dirNameVersions+"/"+version || !versionPattern.MatchString(version) {
		return Installed{}, errf(CodeConflict, "the active release link points outside the managed releases")
	}
	inst.Current = version
	return inst, nil
}

// InstallInput is everything an install or upgrade plan depends on.
type InstallInput struct {
	Layout   Layout
	Manifest Manifest
	// SourceRoot is the unpacked distribution tree. The caller verifies the
	// manifest signature before planning; the planner verifies the tree
	// against the manifest.
	SourceRoot string
	// HostArch is the architecture of the installing host.
	HostArch  string
	Services  []ServiceSpec
	Installed Installed
}

// PlanInstallation plans a first install or an upgrade, whichever the
// installed state calls for. It verifies the source tree first, so a plan
// exists only for a distribution that matches its manifest. The plan never
// touches the state directory: an upgrade publishes the release next to the
// old one while the services still run, then unloads them, moves the active
// link, rewrites the launchers and loads them again.
func PlanInstallation(in InstallInput) (Plan, error) {
	l, m := in.Layout, in.Manifest
	if err := l.validate(); err != nil {
		return Plan{}, err
	}
	if l.Manager == ManagerNone {
		return Plan{}, errf(CodeInvalidInput, "the controller distribution installs into a controller layout, not a desktop layout")
	}
	if err := VerifyTree(m, in.SourceRoot); err != nil {
		return Plan{}, err
	}
	if m.Distribution != DistributionController {
		return Plan{}, errf(CodeCapabilityUnsupported, "this plan installs the controller distribution; the desktop bundle is installed separately")
	}
	if m.Target.OS != l.OS || m.Target.Arch != in.HostArch {
		return Plan{}, errf(CodeCapabilityUnsupported, "the release targets %s/%s, not this host", m.Target.OS, m.Target.Arch)
	}
	if pathWithin(in.SourceRoot, l.DistRoot) || pathWithin(in.SourceRoot, l.StateDir) {
		return Plan{}, errf(CodeInvalidInput, "the source tree must sit outside the distribution and state directories")
	}
	units, err := planUnits(l, m, in.Services)
	if err != nil {
		return Plan{}, err
	}
	plan, err := planRelease(l, m, in.SourceRoot, in.Installed)
	if err != nil {
		return Plan{}, err
	}
	plan.Prepare = append(plan.Prepare, Step{Action: ActionEnsureDir, Path: l.UnitDir, Mode: 0o755})
	plan.Steps = append(plan.Steps, Step{Action: ActionSwapLink, Path: l.Current, Target: filepath.Join(dirNameVersions, m.Version)})
	for _, u := range units {
		unitPath := filepath.Join(l.UnitDir, u.FileName)
		if plan.Kind == PlanUpgrade {
			plan.Before = append(plan.Before, ServiceAction{Verb: VerbUnload, Label: u.Label, UnitPath: unitPath})
		}
		plan.Steps = append(plan.Steps, Step{Action: ActionWriteFile, Path: unitPath, Content: u.Content, Mode: u.Mode})
		plan.After = append(plan.After, ServiceAction{Verb: VerbLoad, Label: u.Label, UnitPath: unitPath})
	}
	plan.Notices = append(plan.Notices, "The state directory is not read or written by this plan.")
	if plan.Kind == PlanUpgrade {
		plan.Notices = append(plan.Notices, "Release "+plan.PreviousVersion+" stays on disk next to the new release.")
	}
	if l.Manager == ManagerSystemdUser {
		plan.Notices = append(plan.Notices, "A user systemd service stops at logout unless the operator enables lingering for the account.")
	}
	return plan, nil
}

// planRelease is the part of an install or upgrade plan both distributions
// share: decide install versus upgrade, then stage every artifact and the
// manifest into a partial directory and publish it as the release
// directory. The active link is left to the caller.
func planRelease(l Layout, m Manifest, sourceRoot string, installed Installed) (Plan, error) {
	plan := Plan{Kind: PlanInstall, Layout: l, Version: m.Version, PreviousVersion: installed.Current}
	if prev := installed.Current; prev != "" {
		switch c := compareVersions(m.Version, prev); {
		case c == 0:
			return Plan{}, errf(CodeConflict, "release %s is already the active release", m.Version)
		case c < 0:
			return Plan{}, errf(CodeCapabilityUnsupported, "release %s is older than the active release; state written by a later release cannot be downgraded, restore a backup instead", m.Version)
		}
		plan.Kind = PlanUpgrade
	}
	partial := filepath.Join(l.Versions, "."+m.Version+".partial")
	final := l.VersionDir(m.Version)
	plan.Prepare = append(plan.Prepare,
		Step{Action: ActionEnsureDir, Path: l.DistRoot, Mode: 0o755},
		Step{Action: ActionEnsureDir, Path: l.Versions, Mode: 0o755},
		// A stale partial or an unpublished copy of this release is left
		// over from an interrupted run. Neither is the active release.
		Step{Action: ActionRemoveTree, Path: final},
		Step{Action: ActionResetDir, Path: partial, Mode: 0o755},
	)
	dirs := map[string]bool{}
	for _, a := range m.Artifacts {
		dest := filepath.Join(partial, filepath.FromSlash(a.Path))
		for dir := filepath.Dir(dest); dir != partial && !dirs[dir]; dir = filepath.Dir(dir) {
			dirs[dir] = true
		}
	}
	for _, dir := range sortedKeys(dirs) {
		plan.Prepare = append(plan.Prepare, Step{Action: ActionEnsureDir, Path: dir, Mode: 0o755})
	}
	for _, a := range m.Artifacts {
		mode, err := parseMode(a.Mode)
		if err != nil {
			return Plan{}, errf(CodeInvalidInput, "artifact %s mode must be a four-digit octal permission", a.Path)
		}
		plan.Prepare = append(plan.Prepare, Step{
			Action: ActionCopyFile,
			Path:   filepath.Join(partial, filepath.FromSlash(a.Path)),
			Source: filepath.Join(sourceRoot, filepath.FromSlash(a.Path)),
			SHA256: a.SHA256, Size: a.Size, Mode: mode,
		})
	}
	manifestBytes, err := Encode(m)
	if err != nil {
		return Plan{}, err
	}
	plan.Prepare = append(plan.Prepare,
		Step{Action: ActionWriteFile, Path: filepath.Join(partial, ManifestFileName), Content: manifestBytes, Mode: 0o644},
		Step{Action: ActionPublishDir, Path: partial, Target: final},
	)
	return plan, nil
}

// planUnits renders the launchers and binds them to this layout: the
// controller launcher runs the binary behind the active link and owns the
// state directory, and a Serenity launcher runs the pinned runtime.
func planUnits(l Layout, m Manifest, specs []ServiceSpec) ([]Unit, error) {
	if err := ValidateServices(specs); err != nil {
		return nil, err
	}
	controllers := 0
	units := make([]Unit, 0, len(specs))
	for _, s := range specs {
		if s.Manager != l.Manager {
			return nil, errf(CodeInvalidInput, "service %s does not use the layout's service manager", s.Label)
		}
		switch s.Role {
		case RoleController:
			controllers++
			if s.Executable != l.ControllerExecutable(m) || s.Owns != l.StateDir {
				return nil, errf(CodeInvalidInput, "the controller service must run the installed controller binary and own the state directory")
			}
		case RoleSerenity:
			if s.Executable != filepath.Join(l.Current, filepath.FromSlash(m.Serenity.Runtime)) {
				return nil, errf(CodeInvalidInput, "a Serenity service must run the pinned Serenity runtime from the installed release")
			}
		}
		if pathWithin(s.Owns, l.DistRoot) || pathWithin(l.DistRoot, s.Owns) {
			return nil, errf(CodeConflict, "service %s would write inside the distribution directory", s.Label)
		}
		if s.LogDirectory != "" && pathWithin(s.LogDirectory, l.DistRoot) {
			return nil, errf(CodeConflict, "service %s would log inside the distribution directory", s.Label)
		}
		u, err := Render(s)
		if err != nil {
			return nil, err
		}
		units = append(units, u)
	}
	if controllers != 1 {
		return nil, errf(CodeInvalidInput, "an installation needs exactly one controller service")
	}
	return units, nil
}

// UninstallInput selects what an uninstall removes.
type UninstallInput struct {
	Layout Layout
	// Labels are the service labels whose launchers are removed.
	Labels []string
	// RemoveState requests removal of the state directory. It takes effect
	// only when ConfirmStateDir repeats the exact state directory path.
	RemoveState     bool
	ConfirmStateDir string
}

// PlanUninstallation plans removal of the launchers and the distribution.
// State is preserved unless the operator both requests its removal and
// confirms the exact directory.
func PlanUninstallation(in UninstallInput) (Plan, error) {
	l := in.Layout
	if err := l.validate(); err != nil {
		return Plan{}, err
	}
	switch {
	case in.RemoveState && in.ConfirmStateDir != l.StateDir:
		return Plan{}, errf(CodeInvalidInput, "state removal must be confirmed with the exact state directory")
	case !in.RemoveState && in.ConfirmStateDir != "":
		return Plan{}, errf(CodeInvalidInput, "a state directory confirmation was given without a state removal request")
	}
	if l.Manager == ManagerNone {
		return Plan{}, errf(CodeInvalidInput, "the controller distribution uninstalls from a controller layout, not a desktop layout")
	}
	plan := Plan{Kind: PlanUninstall, Layout: l, RemovesState: in.RemoveState}
	seen := map[string]bool{}
	for _, label := range in.Labels {
		if !launchdLabelPattern.MatchString(label) || len(label) > 100 || seen[label] {
			return Plan{}, errf(CodeInvalidInput, "service labels must be unique reverse-DNS names")
		}
		seen[label] = true
		unitPath := filepath.Join(l.UnitDir, unitFileName(l.Manager, label))
		plan.Before = append(plan.Before, ServiceAction{Verb: VerbUnload, Label: label, UnitPath: unitPath})
		plan.Steps = append(plan.Steps, Step{Action: ActionRemoveFile, Path: unitPath})
	}
	plan.Steps = append(plan.Steps,
		Step{Action: ActionRemoveFile, Path: l.Current},
		Step{Action: ActionRemoveTree, Path: l.DistRoot},
	)
	if in.RemoveState {
		plan.Steps = append(plan.Steps, Step{Action: ActionRemoveTree, Path: l.StateDir})
		plan.Notices = append(plan.Notices,
			"The state directory is removed. This cannot be undone without a verified backup.",
			"Keychain entries and a master key file live outside the state directory and are not removed.",
		)
	} else {
		plan.Notices = append(plan.Notices, "The state directory is preserved. A later install of the same layout finds it unchanged.")
	}
	return plan, nil
}

func unitFileName(manager, label string) string {
	if manager == ManagerSystemdUser {
		return label + ".service"
	}
	return label + ".plist"
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// compareVersions orders two strings that match versionPattern by semantic
// version precedence.
func compareVersions(a, b string) int {
	coreA, preA, _ := strings.Cut(a, "-")
	coreB, preB, _ := strings.Cut(b, "-")
	pa, pb := strings.Split(coreA, "."), strings.Split(coreB, ".")
	for i := 0; i < 3 && i < len(pa) && i < len(pb); i++ {
		if c := compareNumeric(pa[i], pb[i]); c != 0 {
			return c
		}
	}
	switch {
	case preA == preB:
		return 0
	case preA == "":
		return 1 // a release outranks its own pre-releases
	case preB == "":
		return -1
	}
	ia, ib := strings.Split(preA, "."), strings.Split(preB, ".")
	for i := 0; i < len(ia) && i < len(ib); i++ {
		na, errA := strconv.ParseUint(ia[i], 10, 64)
		nb, errB := strconv.ParseUint(ib[i], 10, 64)
		var c int
		switch {
		case errA == nil && errB == nil:
			c = compareUint(na, nb)
		case errA == nil:
			c = -1 // numeric identifiers rank below alphanumeric ones
		case errB == nil:
			c = 1
		default:
			c = strings.Compare(ia[i], ib[i])
		}
		if c != 0 {
			return c
		}
	}
	return compareUint(uint64(len(ia)), uint64(len(ib)))
}

// compareNumeric compares two decimal strings without leading zeros of any
// length, so an oversized component cannot overflow.
func compareNumeric(a, b string) int {
	if len(a) != len(b) {
		return compareUint(uint64(len(a)), uint64(len(b)))
	}
	return strings.Compare(a, b)
}

func compareUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
