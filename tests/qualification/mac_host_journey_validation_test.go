package qualification_test

import (
	"strings"
	"testing"
)

func TestMacHostEvidenceRejectsUnlinkedStages(t *testing.T) {
	h := macReleaseHost{Schema: macReleaseHostSchema, Version: "1.0.0", ReleaseSequence: 1, Arch: "amd64", HostRunID: "12345678-1234-4234-8234-123456789abc", OSBuild: "23A344", DeveloperToolsAbsent: true, TeamID: "ABCDEFGHIJ"}
	if err := validateMacHostIdentity(h, "amd64"); err == nil {
		t.Fatal("missing final signed hashes accepted")
	}
	digest := strings.Repeat("a", 64)
	h.ApplicationCertSHA256, h.InstallerCertSHA256, h.ReleaseDescriptorSHA256, h.DeliverySHA256 = digest, digest, digest, digest
	h.ScriptSHA256, h.BootstrapZIPSHA256, h.InstallerSHA256 = digest, digest, digest
	h.ControllerSHA256, h.DesktopSHA256, h.HelperSHA256 = digest, digest, digest
	if err := validateMacHostIdentity(h, "arm64"); err == nil {
		t.Fatal("wrong native architecture accepted")
	}
	if err := validateMacHostIdentity(h, "amd64"); err != nil {
		t.Fatalf("well-formed identity: %v", err)
	}
	a := macInstalledAudit{RunID: h.HostRunID, DeliverySHA256: digest, InstallerSHA256: digest, InboxControllerSHA256: digest, InboxDesktopSHA256: digest, ActiveControllerSourceSHA256: digest, ActiveDesktopSourceSHA256: digest, ActiveHelperSHA256: digest, FixedCurrentUserInstaller: true, InboxAndPayloadAudited: true, ActiveTreesAudited: true, LauncherAndKeychainReady: true}
	a.ActiveHelperSHA256 = strings.Repeat("b", 64)
	if err := validateMacInstalledAudit(h, a); err == nil {
		t.Fatal("different active helper accepted")
	}
	if err := validateMacFirstChatAudit(h.HostRunID, macFirstChatAudit{RunID: h.HostRunID}); err == nil {
		t.Fatal("no provider readback accepted")
	}
	if err := validateMacSerenityAudit(h.HostRunID, macSerenityAudit{RunID: h.HostRunID, PinnedProtocol: "v1"}); err == nil {
		t.Fatal("partial Serenity guarantees accepted")
	}
}
