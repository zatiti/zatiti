package qualification_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"testing"
	"time"
)

const macReleaseHostSchema = "zatiti.ci.mac_release_host/v1"
const macInstallToFirstChatCase = "QUALIFICATION.macos_install_to_first_chat"

// The fields mirror the rev10 CI matrix reader. No value here is a secret or
// raw model text. Producers must derive them from final signed bytes and a
// live host, never from caller-provided success JSON.
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

type macInstalledAudit struct {
	RunID                        string
	DeliverySHA256               string
	InstallerSHA256              string
	InboxControllerSHA256        string
	InboxDesktopSHA256           string
	ActiveControllerSourceSHA256 string
	ActiveDesktopSourceSHA256    string
	ActiveHelperSHA256           string
	FixedCurrentUserInstaller    bool
	InboxAndPayloadAudited       bool
	ActiveTreesAudited           bool
	LauncherAndKeychainReady     bool
}

type macFirstChatAudit struct {
	RunID                   string
	InstallationID          string
	ConversationID          string
	ProviderResponseID      string
	CommittedMessageID      string
	DisplayedMessageID      string
	ReadbackMessageID       string
	ControllerAuthenticated bool
}

type macSerenityAudit struct {
	RunID                        string
	PinnedProtocol               string
	OneCanonicalWriter           bool
	ScopedRecallAndCuration      bool
	LostAcknowledgmentReconciled bool
	CostAndDisclosureBounded     bool
	BackupRevisionRestored       bool
}

// A concrete implementation must run in the native clean-host session. Each
// method is a live observation boundary; the same run ID and final-byte
// digests are checked before any result can enter the release report.
type macHostJourneyDriver interface {
	VerifyFinalSignedRelease(context.Context, string, string) (macReleaseHost, error)
	InstallAndAudit(context.Context, macReleaseHost) (macInstalledAudit, error)
	CaptureAndReadBackFirstChat(context.Context, macReleaseHost, macInstalledAudit) (macFirstChatAudit, error)
	QualifySerenity(context.Context, macReleaseHost, macFirstChatAudit) (macSerenityAudit, error)
}

// No production driver is registered until signed final artifacts, a
// controlled clean account, the provider and the pinned Serenity service
// exist. Tests cannot turn a fixture report or environment value into a pass.
var liveMacHostDriver macHostJourneyDriver

func TestMacInstallToFirstChat(t *testing.T) {
	c := beginCase(t, macInstallToFirstChatCase, "QUALIFICATION",
		"one native clean-host run binds final signed delivery, script, bootstrap ZIP and architecture-specific pkg/components/helper to installed inbox/active trees, authenticated first provider reply and full Serenity guarantees")
	arch, reason := nativeMacCaseArch(runtime.GOOS, runtime.GOARCH, nativeMacHardwareArch)
	if reason != "" {
		c.notRun("%s", reason)
	}
	if liveMacHostDriver == nil {
		c.notRun("native clean-host driver is unavailable: require final signed delivery/bootstrap/pkg, pinned signer, installed AppKit helper, disposable provider account, qualified Serenity and authoritative GUI/controller readback")
	}
	runID, err := newMacHostRunID()
	if err != nil {
		c.fail("host-run identity could not be generated")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	host, err := liveMacHostDriver.VerifyFinalSignedRelease(ctx, arch, runID)
	if err != nil {
		macDriverError(c, "verified final release", err)
	}
	host.HostRunID = runID
	if err := validateMacHostIdentity(host, arch); err != nil {
		c.fail("verified release identity: %v", err)
	}
	installed, err := liveMacHostDriver.InstallAndAudit(ctx, host)
	if err != nil {
		macDriverError(c, "native installation/audit", err)
	}
	if err := validateMacInstalledAudit(host, installed); err != nil {
		c.fail("installed release mismatch: %v", err)
	}
	chat, err := liveMacHostDriver.CaptureAndReadBackFirstChat(ctx, host, installed)
	if err != nil {
		macDriverError(c, "real provider first-chat readback", err)
	}
	if err := validateMacFirstChatAudit(runID, chat); err != nil {
		c.fail("first-chat readback: %v", err)
	}
	serenity, err := liveMacHostDriver.QualifySerenity(ctx, host, chat)
	if err != nil {
		macDriverError(c, "Serenity public-protocol qualification", err)
	}
	if err := validateMacSerenityAudit(runID, serenity); err != nil {
		c.fail("Serenity qualification: %v", err)
	}
	c.attach("host_run_id", runID)
	for key, digest := range map[string]string{"delivery_sha256": host.DeliverySHA256, "installer_sha256": host.InstallerSHA256, "controller_sha256": host.ControllerSHA256, "desktop_sha256": host.DesktopSHA256, "helper_sha256": host.HelperSHA256, "bootstrap_zip_sha256": host.BootstrapZIPSHA256, "script_sha256": host.ScriptSHA256} {
		c.attach(key, digest)
	}
	c.attach("installation_id", chat.InstallationID)
	c.attach("conversation_id", chat.ConversationID)
	c.attach("provider_response_id", chat.ProviderResponseID)
	c.attach("committed_message_id", chat.CommittedMessageID)
	c.attach("serenity_protocol", serenity.PinnedProtocol)
	c.observe("final signed release, installed inbox/active trees, authenticated first-chat readback and Serenity guarantees matched one native host run")
	publishMacRelease(host)
}

var macSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)
var macUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var macVersion = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z]+(\.[0-9A-Za-z]+)*)?$`)
var macRedactedIdentifier = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,256}$`)

type macDriverPrerequisite struct{ name string }

func (e macDriverPrerequisite) Error() string { return e.name }

func macDriverError(c *caseRun, stage string, err error) {
	var missing macDriverPrerequisite
	if errors.As(err, &missing) {
		c.notRun("%s unavailable: %s", stage, missing.name)
	}
	c.fail("%s failed: %v", stage, err)
}

func validateMacHostIdentity(h macReleaseHost, arch string) error {
	if h.Schema != macReleaseHostSchema || h.Arch != arch || !macVersion.MatchString(h.Version) || h.ReleaseSequence <= 0 || !macUUID.MatchString(h.HostRunID) || h.OSBuild == "" || len(h.OSBuild) > 128 || !h.DeveloperToolsAbsent || !plainTeamID(h.TeamID) {
		return errors.New("native signed release identity is incomplete")
	}
	for _, digest := range []string{h.ApplicationCertSHA256, h.InstallerCertSHA256, h.ReleaseDescriptorSHA256, h.DeliverySHA256, h.ScriptSHA256, h.BootstrapZIPSHA256, h.InstallerSHA256, h.ControllerSHA256, h.DesktopSHA256, h.HelperSHA256} {
		if !macSHA256.MatchString(digest) {
			return errors.New("signed release digest is missing or malformed")
		}
	}
	return nil
}

func validateMacInstalledAudit(h macReleaseHost, a macInstalledAudit) error {
	if a.RunID != h.HostRunID || a.DeliverySHA256 != h.DeliverySHA256 || a.InstallerSHA256 != h.InstallerSHA256 || a.InboxControllerSHA256 != h.ControllerSHA256 || a.InboxDesktopSHA256 != h.DesktopSHA256 || a.ActiveControllerSourceSHA256 != h.ControllerSHA256 || a.ActiveDesktopSourceSHA256 != h.DesktopSHA256 || a.ActiveHelperSHA256 != h.HelperSHA256 || !a.FixedCurrentUserInstaller || !a.InboxAndPayloadAudited || !a.ActiveTreesAudited || !a.LauncherAndKeychainReady {
		return errors.New("installed bytes or required native audits do not match verified final release")
	}
	return nil
}

func validateMacFirstChatAudit(runID string, a macFirstChatAudit) error {
	if a.RunID != runID || !macUUID.MatchString(a.InstallationID) || !macUUID.MatchString(a.ConversationID) || !macUUID.MatchString(a.CommittedMessageID) || a.CommittedMessageID != a.ReadbackMessageID || a.CommittedMessageID != a.DisplayedMessageID || !macRedactedIdentifier.MatchString(a.ProviderResponseID) || !a.ControllerAuthenticated {
		return errors.New("authoritative first provider reply was not committed, displayed and read back")
	}
	return nil
}

func validateMacSerenityAudit(runID string, a macSerenityAudit) error {
	if a.RunID != runID || !macRedactedIdentifier.MatchString(a.PinnedProtocol) || !a.OneCanonicalWriter || !a.ScopedRecallAndCuration || !a.LostAcknowledgmentReconciled || !a.CostAndDisclosureBounded || !a.BackupRevisionRestored {
		return errors.New("required Serenity guarantees are missing")
	}
	return nil
}

func newMacHostRunID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	s := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", s[:8], s[8:12], s[12:16], s[16:20], s[20:]), nil
}
