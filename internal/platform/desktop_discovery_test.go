package platform

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func discoveryFor(state string) DesktopDiscovery {
	return DesktopDiscovery{Schema: DesktopDiscoverySchema, ProtocolVersion: 1, SocketPath: filepath.Join(state, "zatiti.sock")}
}

func TestDesktopDiscoveryBeforeAndAfterBootstrap(t *testing.T) {
	p, state := openDiscoveryForTest(t)
	if _, err := ReadDesktopDiscovery(state); Code(err) != contract.CodeNotFound {
		t.Fatalf("missing record: %v", err)
	}
	pre := discoveryFor(state)
	if err := p.PublishDesktopDiscovery(context.Background(), pre); err != nil {
		t.Fatal(err)
	}
	got, err := ReadDesktopDiscovery(state)
	if err != nil || got != pre {
		t.Fatalf("pre-bootstrap read: %+v, %v", got, err)
	}
	post := pre
	post.InstallationID = contract.NewID()
	post.KeychainService = "com.zatiti.zatiti.v1.1234567890abcdef"
	post.KeychainAccount = "installation/owner"
	if err := p.PublishDesktopDiscovery(context.Background(), post); err != nil {
		t.Fatal(err)
	}
	got, err = ReadDesktopDiscovery(state)
	if err != nil || got != post {
		t.Fatalf("initialized read: %+v, %v", got, err)
	}
	raw, err := os.ReadFile(filepath.Join(state, desktopDiscoveryFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "Bearer ") || len(raw) > desktopDiscoveryMaxBytes {
		t.Fatal("secret or oversized discovery")
	}
	info, err := os.Stat(filepath.Join(state, desktopDiscoveryFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: %v %v", info, err)
	}
}

func TestDesktopDiscoveryRejectsBadPublishedRecords(t *testing.T) {
	p, state := openDiscoveryForTest(t)
	d := discoveryFor(state)
	bad := []DesktopDiscovery{
		{Schema: "future", ProtocolVersion: 1, SocketPath: d.SocketPath},
		{Schema: DesktopDiscoverySchema, ProtocolVersion: 2, SocketPath: d.SocketPath},
		{Schema: DesktopDiscoverySchema, ProtocolVersion: 1, SocketPath: "relative.sock"},
		{Schema: DesktopDiscoverySchema, ProtocolVersion: 1, SocketPath: filepath.Join(state, "..", "elsewhere.sock")},
		{Schema: DesktopDiscoverySchema, ProtocolVersion: 1, SocketPath: strings.Repeat("x", 105)},
	}
	for _, candidate := range bad {
		if err := p.PublishDesktopDiscovery(context.Background(), candidate); err == nil {
			t.Fatalf("accepted %+v", candidate)
		}
	}
	partial := d
	partial.InstallationID = contract.NewID()
	if err := p.PublishDesktopDiscovery(context.Background(), partial); err == nil {
		t.Fatal("accepted partial locator")
	}
	if _, err := ReadDesktopDiscovery(state); Code(err) != contract.CodeNotFound {
		t.Fatalf("bad write changed record: %v", err)
	}
}

func TestDesktopDiscoveryRefusesCorruptionAndUnsafeFilesystem(t *testing.T) {
	p, state := openDiscoveryForTest(t)
	d := discoveryFor(state)
	path := filepath.Join(state, desktopDiscoveryFile)
	cases := []string{
		`{"schema":"zatiti.desktop-discovery/v1","protocol_version":1,"socket_path":"/tmp/a","evil":1}`,
		`{"schema":"zatiti.desktop-discovery/v1","schema":"zatiti.desktop-discovery/v1","protocol_version":1,"socket_path":"/tmp/a"}`,
		`{"schema":"zatiti.desktop-discovery/v1","protocol_version":1,"socket_path":"/tmp/a","installation_id":"abc"}`,
		`{"schema":"zatiti.desktop-discovery/v2","protocol_version":1,"socket_path":"/tmp/a"}`,
		`{"schema":"zatiti.desktop-discovery/v1","protocol_version":1.0,"socket_path":"/tmp/a"}`,
		`{"schema":"zatiti.desktop-discovery/v1","protocol_version":1,"socket_path":"/tmp/a"} garbage`,
		strings.Repeat(" ", desktopDiscoveryMaxBytes+1),
		string([]byte{0xff}),
	}
	for _, raw := range cases {
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadDesktopDiscovery(state); err == nil {
			t.Fatalf("accepted corrupt record %.64q", raw)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(state, "target"), path); err != nil {
		t.Fatal(err)
	}
	if err := p.PublishDesktopDiscovery(context.Background(), d); err == nil {
		t.Fatal("published over symlink")
	}
	if _, err := ReadDesktopDiscovery(state); err == nil {
		t.Fatal("read through symlink")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := p.PublishDesktopDiscovery(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDesktopDiscovery(state); err == nil {
		t.Fatal("accepted permissive file")
	}
	if err := p.PublishDesktopDiscovery(context.Background(), d); err == nil {
		t.Fatal("replaced permissive file")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDesktopDiscovery(state); err == nil {
		t.Fatal("accepted permissive parent")
	}
	if err := p.PublishDesktopDiscovery(context.Background(), d); err == nil {
		t.Fatal("published in permissive parent")
	}
}

func TestDesktopDiscoveryInterruptedReplacementLeavesOldRecord(t *testing.T) {
	p, state := openDiscoveryForTest(t)
	pre := discoveryFor(state)
	if err := p.PublishDesktopDiscovery(context.Background(), pre); err != nil {
		t.Fatal(err)
	}
	post := pre
	post.InstallationID = contract.NewID()
	post.KeychainService = "service"
	post.KeychainAccount = "account"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.PublishDesktopDiscovery(ctx, post); err == nil {
		t.Fatal("cancelled publication succeeded")
	}
	got, err := ReadDesktopDiscovery(state)
	if err != nil || got != pre {
		t.Fatalf("old record was not retained: %+v, %v", got, err)
	}
}

type fakeDesktopInfo struct {
	os.FileInfo
	sys any
}

func (f fakeDesktopInfo) Sys() any { return f.sys }
func TestDesktopDiscoveryOwnerCheck(t *testing.T) {
	_, state := openDiscoveryForTest(t)
	info, err := os.Stat(state)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("owner stat unavailable")
	}
	clone := *stat
	clone.Uid = uint32(os.Getuid() + 1)
	if desktopOwned(fakeDesktopInfo{info, &clone}) {
		t.Fatal("accepted a different owner")
	}
	if err := validateDesktopFileInfo(fakeDesktopInfo{info, &clone}); err == nil {
		t.Fatal("accepted wrong-owner file")
	}
}

func openDiscoveryForTest(t *testing.T) (*Platform, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "zdc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	state := filepath.Join(dir, "state")
	p, err := Open(Config{StateDir: state, CredentialBackend: backendHeadless, MasterKeyRef: writeKeyFile(t, testMasterKey(t))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p, p.StateDir()
}
