package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/platform"
)

type recordedLocator struct{ ref string }

func (l *recordedLocator) KeychainLocator(_ context.Context, ref string) (string, string, error) {
	l.ref = ref
	return "zatiti-owner-service", "installation/owner", nil
}

// A process can die after installation.init commits but before it publishes
// a profile or discovery file. The next process must recover the exact owner
// credential from the committed StoreRef without minting a replacement.
func TestCommittedOwnerCredentialRecoversAfterPrepublicationCrash(t *testing.T) {
	cfg := serveConfig(t)
	h := openTestInstallation(t, cfg)
	installationID := bootstrapInstallation(t, h)
	owner, err := committedOwnerCredential(context.Background(), h, installationID)
	if err != nil {
		t.Fatal(err)
	}
	if owner.InstallationID != installationID || owner.StoreRef == "" || owner.OwnerID == "" || owner.CredentialID == "" {
		t.Fatalf("committed metadata is incomplete: %+v", owner)
	}
	secret, err := h.secrets.Get(context.Background(), owner.StoreRef)
	if err != nil {
		t.Fatal(err)
	}
	defer zero(secret)
	h.close() // crash boundary: no profile/discovery publication occurred
	if _, err := os.Stat(filepath.Join(cfg.profilesDir(), defaultProfile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owner profile appeared before publication: %v", err)
	}

	restarted := openTestInstallation(t, cfg)
	defer restarted.close()
	profile, recovered, err := recoverOwnerCredential(context.Background(), restarted, installationID)
	if err != nil {
		t.Fatal(err)
	}
	if profile != defaultProfile || recovered != owner {
		t.Fatalf("recovery changed owner identity: profile=%q owner=%+v", profile, recovered)
	}
	got, err := (profileStore{dir: cfg.profilesDir()}).read(defaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	defer zero(got)
	if !bytes.Equal(got, secret) {
		t.Fatal("recovery changed the committed owner credential")
	}
	info, err := os.Stat(filepath.Join(cfg.profilesDir(), defaultProfile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("owner profile protection: %v, %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(cfg.StateDir, "desktop.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("headless custom layout unexpectedly published desktop discovery: %v", err)
	}
}

func TestRestartedServeRecoversProfileWithoutLoggingSecret(t *testing.T) {
	cfg := serveConfig(t)
	h := openTestInstallation(t, cfg)
	installationID := bootstrapInstallation(t, h)
	owner, err := committedOwnerCredential(context.Background(), h, installationID)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := h.secrets.Get(context.Background(), owner.StoreRef)
	if err != nil {
		t.Fatal(err)
	}
	defer zero(secret)
	h.close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done, logs := serveInBackground(t, ctx, cfg)
	waitFor(t, 15*time.Second, "recovered controller", func() bool {
		select {
		case err := <-done:
			t.Fatalf("serve exited: %v", err)
		default:
		}
		return strings.Contains(logs.String(), "controller running")
	})
	if strings.Contains(logs.String(), string(secret)) || strings.Contains(logs.String(), owner.StoreRef) {
		t.Fatal("owner credential or StoreRef appeared in serve diagnostics")
	}
	if strings.Contains(logs.String(), "bootstrap completed") {
		t.Fatal("restart reran bootstrap")
	}
	got, err := (profileStore{dir: cfg.profilesDir()}).read(defaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	defer zero(got)
	if !bytes.Equal(got, secret) {
		t.Fatal("serve used a different owner credential")
	}
	cancel()
	if err := awaitExit(t, done, "context cancellation", logs); err != nil {
		t.Fatal(err)
	}
}

func TestMissingCommittedOwnerItemFailsWithoutReminting(t *testing.T) {
	cfg := serveConfig(t)
	h := openTestInstallation(t, cfg)
	id := bootstrapInstallation(t, h)
	owner, err := committedOwnerCredential(context.Background(), h, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.secrets.Delete(context.Background(), owner.StoreRef); err != nil {
		t.Fatal(err)
	}
	h.close()
	restarted := openTestInstallation(t, cfg)
	defer restarted.close()
	_, err = handOverOwnerCredential(context.Background(), restarted)
	if faultCode(err) != contract.CodePrerequisiteMissing {
		t.Fatalf("missing committed item = %v, want prerequisite_missing", err)
	}
	still, err := committedOwnerCredential(context.Background(), restarted, id)
	if err != nil || still != owner {
		t.Fatalf("metadata changed after failed recovery: %+v, %v", still, err)
	}
	if _, statErr := os.Stat(filepath.Join(cfg.profilesDir(), defaultProfile)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing owner item created a profile: %v", statErr)
	}
}

func TestInstalledMacDiscoveryRequiresDefaultKeychainLayout(t *testing.T) {
	cfg := serveConfig(t)
	if installedMacDiscovery(cfg) {
		t.Fatal("headless custom state enabled desktop discovery")
	}
	if runtime.GOOS != "darwin" {
		return
	}
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	cfg.StateDir = filepath.Join(base, "zatiti")
	cfg.SocketPath = filepath.Join(cfg.StateDir, socketFileName)
	cfg.CredentialBackend = "keychain"
	if !installedMacDiscovery(cfg) {
		t.Fatal("default Mac layout did not enable discovery")
	}
	cfg.SocketPath = filepath.Join(cfg.StateDir, "other.sock")
	if installedMacDiscovery(cfg) {
		t.Fatal("custom socket enabled installed discovery")
	}
	cfg.SocketPath = filepath.Join(cfg.StateDir, socketFileName)
	cfg.CredentialBackend = "headless"
	if installedMacDiscovery(cfg) {
		t.Fatal("headless backend enabled installed discovery")
	}
}

func TestInitializedRestartKeepsLocatorWhenKeychainRecoveryFails(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS discovery layout")
	}
	root, err := ownerDiscoveryTestRoot(t)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	t.Setenv("HOME", root)
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config{StateDir: filepath.Join(base, "zatiti"), Profile: defaultProfile, CredentialBackend: "headless", MasterKeyRef: writeMasterKey(t, root), TickInterval: 50 * time.Millisecond}
	if err := cfg.finalize(); err != nil {
		t.Fatal(err)
	}
	cfg.SocketPath = filepath.Join(cfg.StateDir, socketFileName)
	h := openTestInstallation(t, cfg)
	id := bootstrapInstallation(t, h)
	h.close() // committed before either discovery or profile publication

	restarted := openTestInstallation(t, cfg)
	defer restarted.close()
	prior := platform.DesktopDiscovery{Schema: platform.DesktopDiscoverySchema, ProtocolVersion: 1,
		SocketPath: cfg.SocketPath, InstallationID: id, KeychainService: "prior-service", KeychainAccount: "prior-account"}
	if err := restarted.plat.PublishDesktopDiscovery(context.Background(), prior); err != nil {
		t.Fatal(err)
	}
	// The platform remains headless, simulating unavailable Keychain custody.
	// Only the entrypoint's installed-layout policy is enabled for this run.
	restarted.cfg.CredentialBackend = ""
	if !installedMacDiscovery(restarted.cfg) {
		t.Fatal("test did not select installed Mac discovery")
	}
	logs := &lockedBuffer{}
	err = runServeOnce(context.Background(), cfg, restarted, newLogger(logs, slog.LevelInfo), serveOptions{pollInterval: 10 * time.Millisecond})
	if err == nil {
		t.Fatal("headless Keychain locator unexpectedly succeeded")
	}
	got, readErr := platform.ReadDesktopDiscovery(cfg.StateDir)
	if readErr != nil || got != prior {
		t.Fatalf("initialized locator changed during failed recovery: %+v, %v", got, readErr)
	}
}

func TestOwnerDiscoveryFileContainsOnlyPersistedIdentityAndLocator(t *testing.T) {
	root, err := ownerDiscoveryTestRoot(t)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	cfg := config{StateDir: filepath.Join(root, "state"), Profile: defaultProfile, CredentialBackend: "headless", MasterKeyRef: writeMasterKey(t, root)}
	if err := cfg.finalize(); err != nil {
		t.Fatal(err)
	}
	cfg.SocketPath = filepath.Join(cfg.StateDir, socketFileName)
	h := openTestInstallation(t, cfg)
	defer h.close()
	id := bootstrapInstallation(t, h)
	owner, err := committedOwnerCredential(context.Background(), h, id)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := h.secrets.Get(context.Background(), owner.StoreRef)
	if err != nil {
		t.Fatal(err)
	}
	defer zero(secret)
	locator := &recordedLocator{}
	record, err := ownerDesktopDiscovery(context.Background(), locator, cfg.SocketPath, id, owner)
	if err != nil {
		t.Fatal(err)
	}
	if locator.ref != owner.StoreRef {
		t.Fatal("desktop locator used a different StoreRef")
	}
	if err := h.plat.PublishDesktopDiscovery(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	got, err := platform.ReadDesktopDiscovery(cfg.StateDir)
	if err != nil || got != record || got.InstallationID != id {
		t.Fatalf("desktop discovery identity: %+v, %v", got, err)
	}
	raw, err := os.ReadFile(filepath.Join(cfg.StateDir, "desktop.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, secret) || bytes.Contains(raw, []byte(owner.StoreRef)) {
		t.Fatal("desktop discovery disclosed owner secret or StoreRef")
	}
	if _, err := ownerDesktopDiscovery(context.Background(), locator, cfg.SocketPath, contract.NewID(), owner); faultCode(err) != contract.CodePrerequisiteMissing {
		t.Fatalf("mismatched installation identity: %v", err)
	}
}

func ownerDiscoveryTestRoot(t *testing.T) (string, error) {
	t.Helper()
	base := os.TempDir()
	if info, err := os.Stat("/tmp"); err == nil && info.IsDir() {
		base = "/tmp"
	}
	root, err := os.MkdirTemp(base, "zt")
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		_ = os.RemoveAll(root)
		return "", err
	}
	return resolved, nil
}
