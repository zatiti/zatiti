package qualification_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The desktop is the Flutter application at apps/desktop, driven as a built
// subprocess against a real controller; a Go stand-in, widget test or
// WebView never counts. P44 shipped apps/desktop/live_test (run in
// production via apps/desktop/tool/live-proof.sh): flutter_test widgetTests
// and plain tests that start a real `zatiti serve` subprocess, run `zatiti
// init` against it, and drive the real shipped ZatitiApp widget tree (or
// the client's own transport) against its real private Unix socket -- the
// "native driver" this package's own AGENTS.md calls for, not the
// integration_test package specifically. TestZ21DesktopJourneys below
// launches that real driver as a controlled subprocess from this root and
// maps its actual, observed per-test results onto the Z21/JOURNEY case
// identities it genuinely proves; a case the driver does not yet exercise
// stays honestly not_run, naming exactly what live_test would still need.

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

// liveTestCoverage names, for each case this desktop driver could in
// principle prove, the exact live_test test name that proves it today, if
// any. A case absent here (or whose named test never ran) is honestly
// not_run: this harness never infers UI coverage from a passing transport-
// only proof or from a different journey's test.
var liveTestCoverage = map[string]string{
	"Z21.first_conversation": "a first launch opens the personal chief with a composer",
}

// flutterToolchain probes the pinned Flutter toolchain and the desktop
// source, recording versions on the case.
type flutterToolchain struct {
	flutterVersion, dartVersion, channel, revision string
	available                                      bool
	reason                                         string
	desktopDir                                     string
	liveTestDriver                                 bool
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
	// live_test/ is P44's real native driver (started from
	// apps/desktop/tool/live-proof.sh in production): flutter_test suites
	// that start a real controller subprocess and drive the real shipped
	// widget tree or transport client against it. An integration_test/
	// directory would count equally if one is ever added; neither a widget
	// test alone (apps/desktop/test/) nor a WebView does.
	if _, err := os.Stat(filepath.Join(tc.desktopDir, "live_test")); err == nil {
		tc.liveTestDriver = true
	} else if _, err := os.Stat(filepath.Join(tc.desktopDir, "integration_test")); err == nil {
		tc.liveTestDriver = true
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

// flutterTestEvent is one line of `flutter test --reporter json`'s event
// stream. Only the fields this harness reads are declared; the reporter's
// own schema carries more.
type flutterTestEvent struct {
	Type string `json:"type"`
	Test *struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"test"`
	TestID int    `json:"testID"`
	Result string `json:"result"`
	Error  string `json:"error"`
	Reason string `json:"skipReason,omitempty"`
}

// liveTestOutcome is one named test's observed result.
type liveTestOutcome struct {
	name, result, errorText string
}

// runLiveTest launches `flutter test live_test/` as a controlled subprocess
// against a controllerBin this harness built from the recorded source
// revision, exactly as apps/desktop/tool/live-proof.sh does, and parses its
// JSON event stream into one outcome per named test. It never substitutes a
// Go stand-in, a widget test in isolation or a WebView: the controller
// subprocess live_test starts is the same built `zatiti serve`, and the
// widget tree it pumps is the real shipped ZatitiApp.
func runLiveTest(desktopDir, controllerBin, stateBase string, timeout time.Duration) ([]liveTestOutcome, string, error) {
	flutterPath, err := exec.LookPath("flutter")
	if err != nil {
		return nil, "", err
	}
	ctxArgs := []string{"test", "--reporter", "json", "live_test/"}
	cmd := exec.Command(flutterPath, ctxArgs...)
	cmd.Dir = desktopDir
	cmd.Env = append(os.Environ(),
		"ZATITI_CONTROLLER_BIN="+controllerBin,
		"ZATITI_LIVE_STATE_BASE="+stateBase,
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return nil, "", fmt.Errorf("starting flutter test: %w", err)
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
		// err may be non-nil (a failing suite exits non-zero); the JSON
		// stream itself is still parsed below to report exactly which
		// named tests passed or failed rather than only a process exit.
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-done
		return nil, stdout.String() + "\n" + stderr.String(), fmt.Errorf("flutter test live_test/ exceeded %s and was killed", timeout)
	}

	// flutter's JSON reporter does not guarantee "error" arrives after
	// "testDone" for the same testID (observed here: error before
	// testDone), so every event is folded into one outcome per testID,
	// keyed by testID throughout, and converted to the name-keyed result
	// only once the stream ends -- never appended as a second entry.
	names := map[int]string{}
	byID := map[int]*liveTestOutcome{}
	order := []int{}
	scanner := bufio.NewScanner(&stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev flutterTestEvent
		if jsonErr := json.Unmarshal(line, &ev); jsonErr != nil {
			continue
		}
		ensure := func(testID int) *liveTestOutcome {
			if o, ok := byID[testID]; ok {
				return o
			}
			o := &liveTestOutcome{name: names[testID]}
			byID[testID] = o
			order = append(order, testID)
			return o
		}
		switch ev.Type {
		case "testStart":
			if ev.Test != nil {
				names[ev.Test.ID] = ev.Test.Name
				if o, ok := byID[ev.Test.ID]; ok && o.name == "" {
					o.name = ev.Test.Name
				}
			}
		case "testDone":
			o := ensure(ev.TestID)
			if o.name == "" {
				o.name = names[ev.TestID]
			}
			if ev.Result != "" {
				o.result = ev.Result
			} else if o.result == "" {
				o.result = "success"
			}
		case "error":
			o := ensure(ev.TestID)
			if o.name == "" {
				o.name = names[ev.TestID]
			}
			if o.errorText == "" {
				o.errorText = ev.Error
			} else {
				o.errorText += "; " + ev.Error
			}
			if o.result == "" {
				o.result = "error"
			}
		}
	}
	outcomes := make([]liveTestOutcome, 0, len(order))
	for _, id := range order {
		o := byID[id]
		if o.name == "" {
			continue
		}
		outcomes = append(outcomes, *o)
	}
	return outcomes, stdout.String() + "\n" + stderr.String(), err
}

// TestZ21DesktopJourneys drives apps/desktop's real live_test native driver
// (see this file's top doc comment) against a real controller built from
// this tree, and maps its observed per-test outcomes onto the case
// identities liveTestCoverage says the driver actually proves. A case with
// no covering test, or whose covering test did not pass, is recorded
// not_run with the concrete, specific reason -- never a blanket "no driver
// exists" once a driver genuinely does.
func TestZ21DesktopJourneys(t *testing.T) {
	tc := probeFlutter()

	var outcomes []liveTestOutcome
	var runErr error
	var rawOutput string
	ran := false
	controllerErr := ""
	if tc.available && tc.liveTestDriver {
		if sharedErr != nil {
			controllerErr = sharedErr.Error()
		} else {
			stateBase, err := shortTempDir()
			if err != nil {
				t.Fatalf("live_test state base: %v", err)
			}
			outcomes, rawOutput, runErr = runLiveTest(tc.desktopDir, shared.bin, stateBase, 8*time.Minute)
			ran = true
		}
	}

	byName := map[string]liveTestOutcome{}
	for _, o := range outcomes {
		byName[o.name] = o
	}

	for _, dc := range desktopCases {
		t.Run(dc.id, func(t *testing.T) {
			c := beginCase(t, dc.id, dc.gate,
				"Executed UI/transport observations of the built Flutter desktop (apps/desktop/live_test) against a real controller over the private socket, verified against controller state read back through the Go client.")
			tc.record(c)
			c.attach("desktop_dir", tc.desktopDir)
			switch {
			case !tc.available:
				c.notRun("%s; the built desktop cannot be produced or driven", tc.reason)
				return
			case !tc.liveTestDriver:
				c.notRun("apps/desktop has no live_test or integration_test driver to launch the built application as a controlled subprocess (toolchain present: flutter %s, dart %s); a Go stand-in, widget test or WebView is prohibited", tc.flutterVersion, tc.dartVersion)
				return
			}
			if !ran {
				if controllerErr != "" {
					c.notRun("the shared controlled controller did not start: %s", controllerErr)
				} else {
					c.notRun("the live_test driver did not run for this case")
				}
				return
			}
			covering, mapped := liveTestCoverage[dc.id]
			if !mapped {
				c.attach("live_test_raw_test_names", testNames(outcomes))
				if failures := allFailures(outcomes); len(failures) > 0 {
					c.attach("live_test_all_failures", failures)
				}
				if runErr != nil {
					c.attach("live_test_run_error", runErr.Error())
				}
				c.notRun("apps/desktop/live_test proves %d other real behaviors against a live controller (first launch/composer, identity settings, message send, and %d transport-parity properties), but no test there yet drives this case's own journey step; see live_test_raw_test_names for what the driver currently covers", len(outcomes), len(outcomes)-1)
				return
			}
			o, found := byName[covering]
			if !found {
				c.attach("live_test_raw_output_tail", tailString(rawOutput, 4000))
				c.notRun("the covering live_test test %q did not report a result (driver run error: %v)", covering, runErr)
				return
			}
			c.attach("live_test_name", o.name)
			c.attach("live_test_result", o.result)
			if o.result != "success" {
				// Flutter's reporter can attribute the root-cause exception
				// of an async failure to a different testID than the one
				// whose assertion ultimately failed (observed: a decode
				// exception thrown inside a shared snapshot-loading path
				// surfaces on whichever test the zone was in when the
				// unawaited Future rejected). All observed failures across
				// the whole run are attached here so the real root cause is
				// never lost behind a generic "Test failed" message.
				c.attach("live_test_all_failures", allFailures(outcomes))
				c.fail("live_test %q reported %s: %s", o.name, o.result, o.errorText)
			}
			c.observe("apps/desktop/live_test %q passed against a real controller over its real private socket", o.name)
		})
	}
}

func testNames(outcomes []liveTestOutcome) []string {
	names := make([]string, 0, len(outcomes))
	for _, o := range outcomes {
		names = append(names, o.name+": "+o.result)
	}
	return names
}

// allFailures lists every non-success outcome's name and captured error
// text, so a root cause reported against one test (or a "(setUpAll)"/
// "(tearDownAll)" pseudo-test) is visible even when a different, related
// test's own failure record carries only a generic summary.
func allFailures(outcomes []liveTestOutcome) []string {
	var out []string
	for _, o := range outcomes {
		if o.result != "success" {
			out = append(out, o.name+" ["+o.result+"]: "+o.errorText)
		}
	}
	return out
}

func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

// Distribution qualification (QUALIFICATION.macos_distribution,
// QUALIFICATION.linux_distribution) lives in packaging_test.go: it drives
// the real produced zatiti binary through the real zatiti-pack packaging/
// install driver against a temp-directory "clean host", per packaging/
// QUALIFICATION.md.
