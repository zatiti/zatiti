package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func macReportFixture(t *testing.T, arch string) qualificationReport {
	t.Helper()
	const revision, module = "revision", "module-digest"
	digest := func(c byte) string { return strings.Repeat(string(c), 64) }
	host := "11111111-1111-1111-1111-111111111111"
	if arch == "arm64" {
		host = "22222222-2222-2222-2222-222222222222"
	}
	values := map[string]any{
		"schema": macHostSchema, "version": "1.0.0", "release_sequence": 1,
		"arch": arch, "host_run_id": host, "os_build": "24G207", "developer_tools_absent": true,
		"team_id": "ABCDEFGHIJ", "application_cert_sha256": digest('a'), "installer_cert_sha256": digest('b'),
		"release_descriptor_sha256": digest('c'), "delivery_sha256": digest('d'),
		"script_sha256": digest('e'), "bootstrap_zip_sha256": digest('f'),
		"installer_sha256": digest('1'), "controller_sha256": digest('2'),
		"desktop_sha256": digest('3'), "helper_sha256": digest('4'),
	}
	if arch == "arm64" {
		values["installer_sha256"] = digest('5')
		values["controller_sha256"] = digest('6')
		values["desktop_sha256"] = digest('7')
		values["helper_sha256"] = digest('8')
	}
	caseEvidence := map[string]any{}
	for _, key := range []string{"host_run_id", "delivery_sha256", "installer_sha256", "controller_sha256", "desktop_sha256", "helper_sha256", "bootstrap_zip_sha256", "script_sha256"} {
		caseEvidence[key] = values[key]
	}
	cases := []map[string]any{}
	for _, id := range append(append([]string{}, requiredMacReleaseCases...), macCleanHostCase) {
		evidence := map[string]any{"observed_run": "fixture"}
		if id == macCleanHostCase {
			evidence = caseEvidence
		}
		cases = append(cases, map[string]any{
			"case": id, "status": "passed", "versions": map[string]string{"platform": "darwin/" + arch, "source_revision": revision, "module_root_go_mod": module},
			"expected": []string{"release behavior"}, "observed": []string{"validator fixture"}, "evidence": evidence,
		})
	}
	raw, err := json.Marshal(map[string]any{
		"versions":    map[string]string{"platform": "darwin/" + arch, "source_revision": revision, "module_root_go_mod": module},
		"mac_release": values, "cases": cases,
		"gates": []map[string]string{{"gate": "QUALIFICATION", "status": "passed_cases_only"}, {"gate": "Z21", "status": "passed_cases_only"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var report qualificationReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	return report
}

func TestNativeQualificationMatrixBindsTwoDistinctHostsAndOneRelease(t *testing.T) {
	t.Parallel()
	amd, arm := macReportFixture(t, "amd64"), macReportFixture(t, "arm64")
	check := func(a, b qualificationReport) macQualificationMatrix {
		return evaluateMacQualificationMatrix(a, b, "revision", "module-digest")
	}
	if got := check(amd, arm); !got.Qualified {
		t.Fatalf("valid pair rejected: %v", got.Blocking)
	}
	arm.MacRelease.InstallerSHA256 = amd.MacRelease.InstallerSHA256 // per-arch hashes need not be different
	if got := check(amd, arm); got.Qualified {
		t.Fatal("case linkage should reject changed artifact digest")
	}
	arm = macReportFixture(t, "arm64")
	arm.MacRelease.DeliverySHA256 = strings.Repeat("9", 64)
	if got := check(amd, arm); got.Qualified {
		t.Fatal("different signed releases were accepted")
	}
	arm = macReportFixture(t, "arm64")
	arm.MacRelease.HostRunID = amd.MacRelease.HostRunID
	if got := check(amd, arm); got.Qualified {
		t.Fatal("one host run was reused for both architectures")
	}
}

func TestNativeQualificationMatrixRejectsMissingSkippedAndWrongArchitectureCases(t *testing.T) {
	t.Parallel()
	amd, arm := macReportFixture(t, "amd64"), macReportFixture(t, "arm64")
	check := func(a, b qualificationReport) bool {
		return evaluateMacQualificationMatrix(a, b, "revision", "module-digest").Qualified
	}
	if !check(amd, arm) {
		t.Fatal("fixture should qualify")
	}
	arm.Cases = arm.Cases[:len(arm.Cases)-1]
	if check(amd, arm) {
		t.Fatal("missing clean-host first-chat case was accepted")
	}
	arm = macReportFixture(t, "arm64")
	arm.Cases[len(arm.Cases)-1].Status = "not_run"
	if check(amd, arm) {
		t.Fatal("skipped clean-host case was accepted")
	}
	arm = macReportFixture(t, "arm64")
	arm.Cases[len(arm.Cases)-1].Versions["platform"] = "darwin/amd64"
	if check(amd, arm) {
		t.Fatal("other-architecture case was accepted")
	}
	arm = macReportFixture(t, "arm64")
	arm.Cases[len(arm.Cases)-1].Evidence["delivery_sha256"] = json.RawMessage(`"wrong"`)
	if check(amd, arm) {
		t.Fatal("case linked to different delivery was accepted")
	}
	arm = macReportFixture(t, "arm64")
	arm.MacRelease.DeveloperToolsAbsent = false
	if check(amd, arm) {
		t.Fatal("developer-tools host was accepted as clean")
	}
}

func TestQualMatrixCommandRequiresBothReportsAndRetainsBlockingVerdict(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, _, err := fileSHA256(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{}
	for _, arch := range []string{"amd64", "arm64"} {
		report := macReportFixture(t, arch)
		report.Versions["module_root_go_mod"] = digest
		for i := range report.Cases {
			report.Cases[i].Versions["module_root_go_mod"] = digest
		}
		raw, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, arch+".json")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		paths[arch] = path
	}
	out := filepath.Join(dir, "verdict.json")
	args := []string{"qualmatrix", "-amd64-report", paths["amd64"], "-arm64-report", paths["arm64"], "-root", root, "-revision", "revision", "-out", out}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), args, &stdout, &stderr); code != 0 {
		t.Fatalf("valid pair failed: %s", stderr.String())
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal("missing retained verdict: ", err)
	}
	if err := os.Remove(paths["arm64"]); err != nil {
		t.Fatal(err)
	}
	if code := run(context.Background(), args, &stdout, &stderr); code != 5 {
		t.Fatalf("missing native report exit %d, want prerequisite_missing", code)
	}
}
