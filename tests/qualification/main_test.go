package qualification_test

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
)

// Qualification runs the real binary, the pinned MCP client, the landed
// adapters against controlled simulators and the toolchain probes the
// desktop and release cases need. Every named case leaves one evidence
// record on disk whether it passed, failed or could not run; TestMain
// assembles those records into the release report after the run, so a
// failing or not-run case is reported, never silently absent.
//
// The evidence directory is ZATITI_QUALIFICATION_EVIDENCE when set, else a
// fresh directory under the system temp root; its path is printed at the
// end of the run.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := openEvidence(); err != nil {
		fmt.Fprintf(os.Stderr, "qualification: evidence directory: %v\n", err)
		os.Exit(1)
	}
	startShared()
	code := m.Run()
	stopShared()
	report, err := writeReleaseReport()
	if err != nil {
		fmt.Fprintf(os.Stderr, "qualification: release report: %v\n", err)
		if code == 0 {
			code = 1
		}
	} else {
		fmt.Fprintf(os.Stderr, "qualification: evidence in %s (release report %s)\n", evidenceDir, report)
	}
	os.Exit(code)
}
