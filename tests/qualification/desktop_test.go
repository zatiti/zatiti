package qualification_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The desktop is the Flutter application at apps/desktop, driven as a built
// subprocess against a real controller; a Go stand-in, widget test or
// WebView never counts. These cases record the toolchain and source they
// would run against and are not run until apps/desktop ships an
// integration_test driver, which blocks every desktop release claim.

// desktopCases are the Z21 cases and the desktop journey this package owns
// harness identities for.
var desktopCases = []struct{ id, gate string }{
	{"Z21.first_conversation", "Z21"},
	{"Z21.two_child_chiefs", "Z21"},
	{"Z21.delegate_work", "Z21"},
	{"Z21.hierarchy_navigation", "Z21"},
	{"Z21.exact_decision", "Z21"},
	{"Z21.reconnect_no_duplicate", "Z21"},
	{"Z21.close_client", "Z21"},
	{"Z21.groups_and_quiet_coordination", "Z21"},
	{"Z21.capability_and_pause_cards", "Z21"},
	{"JOURNEY.desktop_daily_work", "JOURNEY"},
}

// flutterToolchain probes the pinned Flutter toolchain and the desktop
// source, recording versions on the case.
type flutterToolchain struct {
	flutterVersion, dartVersion, channel, revision string
	available                                      bool
	reason                                         string
	desktopDir                                     string
	integrationDriver                              bool
	pubspecLock                                    string
}

func probeFlutter() flutterToolchain {
	tc := flutterToolchain{}
	root, err := moduleRoot()
	if err != nil {
		tc.reason = "module root: " + err.Error()
		return tc
	}
	tc.desktopDir = filepath.Join(root, "apps", "desktop")
	if _, err := os.Stat(filepath.Join(tc.desktopDir, "pubspec.yaml")); err != nil {
		tc.reason = "apps/desktop is not present: " + err.Error()
		return tc
	}
	tc.pubspecLock = fileDigest(filepath.Join(tc.desktopDir, "pubspec.lock"))
	if _, err := os.Stat(filepath.Join(tc.desktopDir, "integration_test")); err == nil {
		tc.integrationDriver = true
	}
	path, err := exec.LookPath("flutter")
	if err != nil {
		tc.reason = "flutter toolchain is not installed"
		return tc
	}
	out, err := exec.Command(path, "--version", "--machine").Output()
	if err != nil {
		tc.reason = "flutter --version --machine failed: " + err.Error()
		return tc
	}
	var v struct {
		FrameworkVersion  string `json:"frameworkVersion"`
		Channel           string `json:"channel"`
		FrameworkRevision string `json:"frameworkRevision"`
		DartSDKVersion    string `json:"dartSdkVersion"`
	}
	// The probe may print a notice before the JSON document.
	if i := strings.IndexByte(string(out), '{'); i >= 0 {
		out = out[i:]
	}
	if err := json.Unmarshal(out, &v); err != nil {
		tc.reason = "flutter --version --machine is not JSON: " + err.Error()
		return tc
	}
	tc.flutterVersion, tc.dartVersion, tc.channel, tc.revision = v.FrameworkVersion, v.DartSDKVersion, v.Channel, v.FrameworkRevision
	tc.available = true
	return tc
}

func (tc flutterToolchain) record(c *caseRun) {
	c.version("flutter", tc.flutterVersion)
	c.version("dart", tc.dartVersion)
	c.version("flutter_channel", tc.channel)
	c.version("flutter_revision", tc.revision)
	c.version("apps_desktop_pubspec_lock_sha256", tc.pubspecLock)
}

func TestZ21DesktopJourneys(t *testing.T) {
	tc := probeFlutter()
	for _, dc := range desktopCases {
		t.Run(dc.id, func(t *testing.T) {
			c := beginCase(t, dc.id, dc.gate,
				"Executed UI observations of the built Flutter desktop against a real controller over the private socket, verified against controller state read back through the Go client.")
			tc.record(c)
			c.attach("desktop_dir", tc.desktopDir)
			switch {
			case !tc.available:
				c.notRun("%s; the built desktop cannot be produced or driven", tc.reason)
			case !tc.integrationDriver:
				c.notRun("apps/desktop has no integration_test driver to launch the built application as a controlled subprocess (toolchain present: flutter %s, dart %s); a Go stand-in, widget test or WebView is prohibited", tc.flutterVersion, tc.dartVersion)
			default:
				c.notRun("a desktop driver exists but this harness does not yet launch it; it must build from the recorded revision with flutter %s and drive the built application", tc.flutterVersion)
			}
		})
	}
}

// Distribution qualification installs documented release artifacts on a
// clean supported host. No release artifact exists on main to install, so
// both cases are not run; the host they would run on is recorded.
func TestQualificationDistribution(t *testing.T) {
	root, _ := moduleRoot()
	artifacts := []string{}
	if root != "" {
		_ = filepath.WalkDir(filepath.Join(root, "packaging"), func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				artifacts = append(artifacts, strings.TrimPrefix(path, root+string(os.PathSeparator)))
			}
			return nil
		})
	}
	for _, dc := range []struct{ id, goos string }{
		{"QUALIFICATION.macos_distribution", "darwin"},
		{"QUALIFICATION.linux_distribution", "linux"},
	} {
		t.Run(dc.id, func(t *testing.T) {
			c := beginCase(t, dc.id, "QUALIFICATION",
				"Documented installation commands work from actual artifacts on a clean supported host; controller ownership, private socket, secure store, lock, Serenity lifecycle and backup/restore exercised.",
				"Release evidence identifies exact OS/architecture/artifact/source/tool versions; compile success is not platform qualification.")
			c.attach("packaging_tree", artifacts)
			if runtime.GOOS != dc.goos {
				c.notRun("this host is %s/%s, not a %s host", runtime.GOOS, runtime.GOARCH, dc.goos)
			}
			c.notRun("no documented release artifact for %s is built by this tree (packaging holds %d files, none an installable artifact this harness can install on a clean host); the binary this run built from source is not a release artifact", dc.goos, len(artifacts))
		})
	}
}
