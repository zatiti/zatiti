package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenRejectsInvalidConfiguration(t *testing.T) {
	keyRef := writeKeyFile(t, testMasterKey(t))

	cases := []struct {
		name string
		cfg  Config
		code string
	}{
		{"missing state dir", Config{MasterKeyRef: keyRef}, contractCodeInvalidInput},
		{"relative state dir", Config{StateDir: "relative/state", MasterKeyRef: keyRef}, contractCodeInvalidInput},
		{"negative max bytes", Config{StateDir: t.TempDir(), MasterKeyRef: keyRef, MaxArtifactBytes: -1}, contractCodeInvalidInput},
		{"unknown backend", Config{StateDir: t.TempDir(), CredentialBackend: "ssh-agent", MasterKeyRef: keyRef}, contractCodeInvalidInput},
		{"missing master key ref", Config{StateDir: t.TempDir(), CredentialBackend: backendHeadless}, contractCodeInvalidInput},
		{"unknown master key scheme", Config{StateDir: t.TempDir(), MasterKeyRef: "vault://x"}, contractCodeInvalidInput},
		{"empty file master ref", Config{StateDir: t.TempDir(), MasterKeyRef: "file:"}, contractCodeInvalidInput},
		{"empty secret master ref", Config{StateDir: t.TempDir(), MasterKeyRef: "secret:"}, contractCodeInvalidInput},
		{"headless store cannot hold its own key", Config{StateDir: t.TempDir(), CredentialBackend: backendHeadless, MasterKeyRef: "secret:master"}, contractCodeInvalidInput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Open(tc.cfg)
			wantCode(t, err, tc.code)
		})
	}
}

func TestOpenRequiresKeyMaterialOfExactly32Bytes(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	cases := []struct {
		name string
		data []byte
	}{
		{"too short", []byte("short")},
		{"31 bytes", make([]byte, 31)},
		{"33 bytes", make([]byte, 33)},
		{"invalid hex", []byte(strings.Repeat("zz", 32))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, "key-"+strings.ReplaceAll(tc.name, " ", "-"))
			if err := os.WriteFile(path, tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Open(Config{StateDir: state, CredentialBackend: backendHeadless, MasterKeyRef: "file:" + path})
			wantCode(t, err, contractCodeInvalidInput)
		})
	}
}

func TestOpenAcceptsRawHexAndBase64KeyMaterial(t *testing.T) {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(0xA0 + i)
	}
	hexKey := []byte(strings.Repeat("ab", 32))
	b64Key := []byte("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")

	for i, data := range [][]byte{raw, hexKey, b64Key} {
		name := []string{"raw", "hex", "base64"}[i]
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "k")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			p, err := Open(Config{StateDir: filepath.Join(dir, "state"), CredentialBackend: backendHeadless, MasterKeyRef: "file:" + path})
			if err != nil {
				t.Fatalf("Open with %s key material: %v", name, err)
			}
			_ = p.Close()
		})
	}
}

func TestOpenRefusesUnprotectedOrMissingKeyFiles(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")

	t.Run("group and world readable key is refused", func(t *testing.T) {
		path := filepath.Join(dir, "loose.key")
		if err := os.WriteFile(path, make([]byte, 32), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Open(Config{StateDir: state, CredentialBackend: backendHeadless, MasterKeyRef: "file:" + path})
		wantCode(t, err, contractCodePrerequisiteMissing)
	})

	t.Run("missing key is a named prerequisite failure", func(t *testing.T) {
		_, err := Open(Config{StateDir: state, CredentialBackend: backendHeadless, MasterKeyRef: "file:" + filepath.Join(dir, "absent.key")})
		wantCode(t, err, contractCodePrerequisiteMissing)
	})

	t.Run("symlinked key file is refused without traversal", func(t *testing.T) {
		real := filepath.Join(dir, "real.key")
		if err := os.WriteFile(real, make([]byte, 32), 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "link.key")
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		_, err := Open(Config{StateDir: state, CredentialBackend: backendHeadless, MasterKeyRef: "file:" + link})
		wantCode(t, err, contractCodePrerequisiteMissing)
	})
}

func TestOpenPreparesPrivateStateLayout(t *testing.T) {
	_, state := openForTest(t)

	if got := modeOf(t, state); got != 0o700 {
		t.Fatalf("state root mode is %o, want 700", got)
	}
	for _, sub := range []string{
		dirNameSecrets,
		filepath.Join(dirNameSecrets, dirNameSecretVals),
		dirNameBlobs,
		filepath.Join(dirNameBlobs, dirNameStaged),
		filepath.Join(dirNameBlobs, dirNameObjects),
	} {
		if got := modeOf(t, filepath.Join(state, sub)); got != 0o700 {
			t.Fatalf("%s mode is %o, want 700", sub, got)
		}
	}
	if got := modeOf(t, filepath.Join(state, fileInstanceID)); got != 0o600 {
		t.Fatalf("instance.id mode is %o, want 600", got)
	}
}

func TestInstanceIDIsStableAndPrivate(t *testing.T) {
	_, state := openForTest(t)
	idPath := filepath.Join(state, fileInstanceID)
	first, err := os.ReadFile(idPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 32 || !isHex(string(first), 32) {
		t.Fatalf("instance id is not 32 hex characters: %q", first)
	}
	keyRef := writeKeyFile(t, testMasterKey(t))

	// Reopening the same installation reuses the identity.
	p2, err := Open(Config{StateDir: state, CredentialBackend: backendHeadless, MasterKeyRef: keyRef})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	_ = p2.Close()
	again, err := os.ReadFile(idPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(first) {
		t.Fatalf("instance id changed across reopen: %s -> %s", first, again)
	}
}

func TestInstanceIDCorruptionIsRefused(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	keyRef := writeKeyFile(t, testMasterKey(t))
	if _, err := Open(Config{StateDir: state, CredentialBackend: backendHeadless, MasterKeyRef: keyRef}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, fileInstanceID), []byte("not-an-id"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(Config{StateDir: state, CredentialBackend: backendHeadless, MasterKeyRef: keyRef})
	wantCode(t, err, contractCodeControllerUnavailable)
}

func TestCloseIsIdempotentAndBlocksFurtherUse(t *testing.T) {
	p, _ := openForTest(t)
	own, err := p.Acquire(ctx())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = own.Close() }()

	if err := p.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close must be a no-op: %v", err)
	}
	if !own.Held() {
		t.Fatal("Platform.Close must not release installation ownership")
	}
	_, err = p.Acquire(ctx())
	wantCode(t, err, contractCodeControllerUnavailable)
	if msg := errMessage(err); !strings.Contains(msg, "closed") {
		t.Fatalf("unexpected closed-platform message: %q", msg)
	}
}

func TestAcquireReportsHeldAndQuietLost(t *testing.T) {
	p, _ := openForTest(t)
	own, err := p.Acquire(ctx())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = own.Close() }()
	if !own.Held() {
		t.Fatal("Held() is false right after Acquire")
	}
	select {
	case <-own.Lost():
		t.Fatal("Lost() fired while the lock is held")
	default:
	}
}

func TestStateDirAccessorMatchesConfiguration(t *testing.T) {
	p, state := openForTest(t)
	// Open canonicalizes through pre-existing parent symlinks (macOS aliases
	// /var to /private/var); the accessor reports the canonical location.
	want, err := filepath.EvalSymlinks(state)
	if err != nil {
		t.Fatal(err)
	}
	if p.StateDir() != want {
		t.Fatalf("StateDir() = %s, want %s", p.StateDir(), want)
	}
	if p.Secrets() == nil || p.Blobs() == nil {
		t.Fatal("Secrets() or Blobs() returned nil")
	}
}

func TestOpenRefusesSymlinkedStateComponent(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	keyRef := writeKeyFile(t, testMasterKey(t))
	// The state root itself must not be a symlink: Open canonicalizes
	// pre-existing ancestors only (macOS aliases /var), never the leaf.
	_, err := Open(Config{StateDir: link, CredentialBackend: backendHeadless, MasterKeyRef: keyRef})
	wantCode(t, err, contractCodeInvalidInput)
}

func TestOpenRejectsNonDirectoryStateComponent(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	keyRef := writeKeyFile(t, testMasterKey(t))
	if err := os.MkdirAll(filepath.Join(state, dirNameBlobs), 0o700); err != nil {
		t.Fatal(err)
	}
	// Put a regular file where the staged directory must live.
	if err := os.WriteFile(filepath.Join(state, dirNameBlobs, dirNameStaged), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(Config{StateDir: state, CredentialBackend: backendHeadless, MasterKeyRef: keyRef})
	wantCode(t, err, contractCodeInvalidInput)
}

func TestOpenRejectsFilePlantedAsStateRoot(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	keyRef := writeKeyFile(t, testMasterKey(t))
	if err := os.WriteFile(state, []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(Config{StateDir: state, CredentialBackend: backendHeadless, MasterKeyRef: keyRef})
	wantCode(t, err, contractCodeInvalidInput)
}
