package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

const (
	macHostSchema      = "zatiti.ci.mac_release_host/v1"
	macMatrixSchema    = "zatiti.ci.mac_qualification_matrix/v1"
	macCleanHostCase   = "QUALIFICATION.macos_install_to_first_chat"
	maxHostReportBytes = 8 << 20
)

var macDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)
var macHostID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var macTeamID = regexp.MustCompile(`^[A-Z0-9]{10}$`)

// macReleaseHost is produced only by the native qualification owner after
// testing final signed bytes on a clean machine. A report without it blocks.
// The delivery digest binds the complete six-asset signed release; selected
// installer/controller/desktop/helper digests are deliberately per-arch.
type macReleaseHost struct {
	Schema                  string `json:"schema"`
	Version                 string `json:"version"`
	ReleaseSequence         int64  `json:"release_sequence"`
	Arch                    string `json:"arch"`
	HostRunID               string `json:"host_run_id"`
	OSBuild                 string `json:"os_build"`
	DeveloperToolsAbsent    bool   `json:"developer_tools_absent"`
	TeamID                  string `json:"team_id"`
	ApplicationCertSHA256   string `json:"application_cert_sha256"`
	InstallerCertSHA256     string `json:"installer_cert_sha256"`
	ReleaseDescriptorSHA256 string `json:"release_descriptor_sha256"`
	DeliverySHA256          string `json:"delivery_sha256"`
	ScriptSHA256            string `json:"script_sha256"`
	BootstrapZIPSHA256      string `json:"bootstrap_zip_sha256"`
	InstallerSHA256         string `json:"installer_sha256"`
	ControllerSHA256        string `json:"controller_sha256"`
	DesktopSHA256           string `json:"desktop_sha256"`
	HelperSHA256            string `json:"helper_sha256"`
}

type macQualificationMatrix struct {
	Schema         string            `json:"schema"`
	Qualified      bool              `json:"qualified"`
	Revision       string            `json:"revision"`
	GoModSHA256    string            `json:"go_mod_sha256"`
	DeliverySHA256 string            `json:"delivery_sha256,omitempty"`
	HostRunIDs     map[string]string `json:"host_run_ids"`
	Blocking       []string          `json:"blocking"`
}

func readMacHostReport(path string) (qualificationReport, error) {
	var report qualificationReport
	f, err := os.Open(path)
	if err != nil {
		return report, faultf(codePrerequisiteMissing, "native qualification report is unavailable: %v", err)
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, maxHostReportBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > maxHostReportBytes {
		return report, faultf(codeInvalidInput, "native qualification report is unreadable or exceeds 8 MiB")
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		return report, faultf(codeInvalidInput, "native qualification report is malformed JSON: %v", err)
	}
	return report, nil
}

func evaluateMacQualificationMatrix(amd, arm qualificationReport, revision, moduleDigest string) macQualificationMatrix {
	v := macQualificationMatrix{Schema: macMatrixSchema, Revision: revision, GoModSHA256: moduleDigest, HostRunIDs: map[string]string{}, Blocking: []string{}}
	reports := []struct {
		arch   string
		report qualificationReport
	}{{"amd64", amd}, {"arm64", arm}}
	for _, item := range reports {
		h := item.report.MacRelease
		prefix := item.arch + ": "
		base := evaluateQualificationEvidenceForPlatform(item.report, revision, moduleDigest, []string{"QUALIFICATION", "Z21"}, append(append([]string{}, requiredMacReleaseCases...), macCleanHostCase), "darwin/"+item.arch)
		for _, reason := range base.Blocking {
			v.Blocking = append(v.Blocking, prefix+reason)
		}
		if h.Schema != macHostSchema || h.Arch != item.arch || !versionPattern.MatchString("v"+h.Version) || h.ReleaseSequence <= 0 || !macHostID.MatchString(h.HostRunID) || h.OSBuild == "" || len(h.OSBuild) > 128 || !h.DeveloperToolsAbsent || !macTeamID.MatchString(h.TeamID) {
			v.Blocking = append(v.Blocking, prefix+"missing or invalid native clean-host release identity")
		}
		for name, digest := range map[string]string{
			"application certificate": h.ApplicationCertSHA256, "installer certificate": h.InstallerCertSHA256,
			"release descriptor": h.ReleaseDescriptorSHA256, "delivery": h.DeliverySHA256,
			"script": h.ScriptSHA256, "bootstrap ZIP": h.BootstrapZIPSHA256,
			"installer": h.InstallerSHA256, "controller": h.ControllerSHA256,
			"desktop": h.DesktopSHA256, "helper": h.HelperSHA256,
		} {
			if !macDigest.MatchString(digest) {
				v.Blocking = append(v.Blocking, prefix+name+" digest is absent or invalid")
			}
		}
		v.HostRunIDs[item.arch] = h.HostRunID
		// The clean-host case must identify the same host run and final bytes as
		// the report header, so a legacy first-chat case cannot be relabeled.
		for _, c := range item.report.Cases {
			if c.Case != macCleanHostCase {
				continue
			}
			for key, want := range map[string]string{
				"host_run_id": h.HostRunID, "delivery_sha256": h.DeliverySHA256,
				"installer_sha256": h.InstallerSHA256, "controller_sha256": h.ControllerSHA256,
				"desktop_sha256": h.DesktopSHA256, "helper_sha256": h.HelperSHA256,
				"bootstrap_zip_sha256": h.BootstrapZIPSHA256, "script_sha256": h.ScriptSHA256,
			} {
				var got string
				if err := json.Unmarshal(c.Evidence[key], &got); err != nil || got != want {
					v.Blocking = append(v.Blocking, prefix+fmt.Sprintf("clean-host case %s does not match release evidence", key))
				}
			}
		}
	}
	a, b := amd.MacRelease, arm.MacRelease
	if a.HostRunID != "" && a.HostRunID == b.HostRunID {
		v.Blocking = append(v.Blocking, "native reports reuse one host run ID")
	}
	if a.Version != b.Version || a.ReleaseSequence != b.ReleaseSequence || a.ReleaseDescriptorSHA256 != b.ReleaseDescriptorSHA256 || a.DeliverySHA256 != b.DeliverySHA256 || a.ScriptSHA256 != b.ScriptSHA256 || a.BootstrapZIPSHA256 != b.BootstrapZIPSHA256 || a.TeamID != b.TeamID || a.ApplicationCertSHA256 != b.ApplicationCertSHA256 || a.InstallerCertSHA256 != b.InstallerCertSHA256 {
		v.Blocking = append(v.Blocking, "native reports do not refer to the same final signed release")
	}
	if macDigest.MatchString(a.DeliverySHA256) && a.DeliverySHA256 == b.DeliverySHA256 {
		v.DeliverySHA256 = a.DeliverySHA256
	}
	v.Qualified = len(v.Blocking) == 0
	return v
}

func runQualMatrix(_ context.Context, args []string, stdout, stderr io.Writer) error {
	fs := newFlags("qualmatrix", stderr)
	amdPath := fs.String("amd64-report", "", "native Intel qualification release-report.json")
	armPath := fs.String("arm64-report", "", "native Apple Silicon qualification release-report.json")
	root := fs.String("root", ".", "repository checkout")
	revision := fs.String("revision", os.Getenv("GITHUB_SHA"), "checked-out source revision")
	out := fs.String("out", "", "retained matrix verdict")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *amdPath == "" || *armPath == "" || *revision == "" || *out == "" {
		return faultf(codeInvalidInput, "both reports, revision and output are required")
	}
	amd, err := readMacHostReport(*amdPath)
	if err != nil {
		return err
	}
	arm, err := readMacHostReport(*armPath)
	if err != nil {
		return err
	}
	digest, _, err := fileSHA256(under(*root, "go.mod"))
	if err != nil {
		return faultf(codePrerequisiteMissing, "hashing go.mod: %v", err)
	}
	v := evaluateMacQualificationMatrix(amd, arm, *revision, digest)
	if err := writeJSON(*out, v); err != nil {
		return err
	}
	for _, reason := range v.Blocking {
		say(stdout, "%s", reason)
	}
	if !v.Qualified {
		return faultf(codeVerificationFailed, "Mac native qualification matrix is incomplete: %s", strings.Join(v.Blocking, "; "))
	}
	say(stdout, "Mac native qualification matrix matches final release %s on both architectures", v.DeliverySHA256)
	return nil
}
