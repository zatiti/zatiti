package packaging

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMacPkgNativeProductMetadataRejectsAlternateDomainsChoicesAndScripts(t *testing.T) {
	home, release, plan, _, _ := macPkgBindingFixture(t)
	pkg := buildMacPayloadFixture(t, home, release.Version, plan.Arch)
	x, err := openMacPkgXAR(pkg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = x.Close() }()
	distribution, err := x.readSmallMember("Distribution")
	if err != nil {
		t.Fatal(err)
	}
	component, err := x.readSmallMember("component.pkg/PackageInfo")
	if err != nil {
		t.Fatal(err)
	}
	if err := inspectMacPkgMetadata(x, release.Version, plan.Arch); err != nil {
		t.Fatal(err)
	}
	badDistribution := map[string]func(string) string{
		"anywhere domain": func(s string) string {
			return strings.Replace(s, `enable_anywhere="false"`, `enable_anywhere="true"`, 1)
		},
		"local system domain": func(s string) string {
			return strings.Replace(s, `enable_localSystem="false"`, `enable_localSystem="true"`, 1)
		},
		"missing home domain": func(s string) string {
			return strings.Replace(s, `enable_currentUserHome="true"`, `enable_currentUserHome="false"`, 1)
		},
		"script required": func(s string) string {
			return strings.Replace(s, `require-scripts="false"`, `require-scripts="true"`, 1)
		},
		"script element": func(s string) string {
			return strings.Replace(s, "</installer-gui-script>", `<script>system.run("echo unsafe")</script></installer-gui-script>`, 1)
		},
		"second choice": func(s string) string {
			return strings.Replace(s, "</installer-gui-script>", `<choice id="second" title="second"/></installer-gui-script>`, 1)
		},
		"other component": func(s string) string { return strings.Replace(s, "#component.pkg", "#evil.pkg", 1) },
		"root auth": func(s string) string {
			return strings.Replace(s, `<pkg-ref id="`+macPkgComponentID+`" version=`, `<pkg-ref auth="root" id="`+macPkgComponentID+`" version=`, 1)
		},
		"wrong architecture": func(s string) string {
			return strings.Replace(s, `hostArchitectures="arm64"`, `hostArchitectures="x86_64"`, 1)
		},
		"XML directive": func(s string) string {
			return "<!DOCTYPE installer-gui-script [<!ENTITY x SYSTEM \"file:///etc/passwd\">]>" + s
		},
	}
	for name, mutate := range badDistribution {
		t.Run(name, func(t *testing.T) {
			changed := []byte(mutate(string(distribution)))
			if bytes.Equal(changed, distribution) {
				t.Fatal("mutation did not apply")
			}
			if err := verifyMacPkgDistribution(changed, release.Version, plan.Arch); err == nil {
				t.Fatal("accepted unsafe Distribution")
			}
		})
	}
	badComponent := map[string]func(string) string{
		"scripts": func(s string) string {
			return strings.Replace(s, "</pkg-info>", "<scripts><postinstall file=\"postinstall\"/></scripts></pkg-info>", 1)
		},
		"alternate location": func(s string) string {
			return strings.Replace(s, `install-location="/"`, `install-location="/Applications"`, 1)
		},
		"wrong ID": func(s string) string { return strings.Replace(s, macPkgComponentID, "com.example.other", 1) },
		"restart": func(s string) string {
			return strings.Replace(s, `postinstall-action="none"`, `postinstall-action="restart"`, 1)
		},
		"extra file count": func(s string) string { return strings.Replace(s, `numberOfFiles="10"`, `numberOfFiles="11"`, 1) },
	}
	for name, mutate := range badComponent {
		t.Run(name, func(t *testing.T) {
			changed := []byte(mutate(string(component)))
			if bytes.Equal(changed, component) {
				t.Fatal("mutation did not apply")
			}
			if err := verifyMacPkgComponentInfo(changed, release.Version); err == nil {
				t.Fatal("accepted unsafe PackageInfo")
			}
		})
	}
	if err := inspectMacPkgPayload(context.Background(), pkg, release, plan); err != nil {
		t.Fatal(err)
	}
}

func TestBuildUnsignedMacPkgCandidateHasExactInertPayload(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Mac package production requires macOS")
	}
	_, release, plan, _, inbox := macPkgBindingFixture(t)
	output := filepath.Join(t.TempDir(), plan.Installer.Filename)
	size, digest, err := BuildUnsignedMacPkgCandidate(context.Background(), release, plan, filepath.Join(inbox, "controller.tar.gz"), filepath.Join(inbox, "desktop.tar.gz"), output)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if int64(len(raw)) != size || digest != hex.EncodeToString(sum[:]) {
		t.Fatal("candidate measurement differs from output")
	}
	if err := inspectMacPkgPayload(context.Background(), output, release, plan); err != nil {
		t.Fatal(err)
	}
	if _, _, err := BuildUnsignedMacPkgCandidate(context.Background(), release, plan, filepath.Join(inbox, "controller.tar.gz"), filepath.Join(inbox, "desktop.tar.gz"), output); err == nil {
		t.Fatal("overwrote existing candidate")
	}
}
