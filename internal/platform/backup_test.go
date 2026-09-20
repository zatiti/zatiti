package platform

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// copyTree recursively copies src into dst, preserving regular-file
// permissions; used to simulate "the backup bundle's blob objects were
// physically restored onto a clean destination's state directory" without
// depending on any not-yet-implemented bundle format.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("copyTree read %s: %v", src, err)
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		t.Fatalf("copyTree mkdir %s: %v", dst, err)
	}
	for _, e := range entries {
		sp := filepath.Join(src, e.Name())
		dp := filepath.Join(dst, e.Name())
		if e.IsDir() {
			copyTree(t, sp, dp)
			continue
		}
		data, err := os.ReadFile(sp)
		if err != nil {
			t.Fatalf("copyTree read file %s: %v", sp, err)
		}
		if err := os.WriteFile(dp, data, 0o600); err != nil {
			t.Fatalf("copyTree write file %s: %v", dp, err)
		}
	}
}

// TestBackupKeyProvisionedToCleanDestinationOpensBundle proves the exact
// required behavior: exporting the master key material from a source
// installation and explicitly provisioning it (a plain key file) at a
// clean destination -- one that never touched the source's OS keychain or
// headless store -- lets the destination open the source's published
// blobs. This is the "key-transfer mechanics" item 1/2 close: an opaque
// secret-store reference alone would not survive the move; the exported
// raw material does.
func TestBackupKeyProvisionedToCleanDestinationOpensBundle(t *testing.T) {
	srcRoot := t.TempDir()
	srcKeyPath := filepath.Join(srcRoot, "master.key")
	if err := os.WriteFile(srcKeyPath, testMasterKey(t), 0o600); err != nil {
		t.Fatal(err)
	}
	srcCfg := Config{
		StateDir:          filepath.Join(srcRoot, "state"),
		CredentialBackend: backendHeadless,
		MasterKeyRef:      "file:" + srcKeyPath,
	}
	src, err := Open(srcCfg)
	if err != nil {
		t.Fatal(err)
	}
	src.lockWatchInterval = testLockWatchInterval
	data := []byte("bundle contents restored to a clean destination")
	digest := stageAndPublish(t, src, data, 0)
	inv, err := src.Inventory(ctx())
	if err != nil {
		t.Fatal(err)
	}
	if len(inv) != 1 || inv[0].Digest != digest || inv[0].Size != int64(len(data)) {
		t.Fatalf("unexpected inventory: %+v", inv)
	}
	_ = src.Close()

	// Key-transfer mechanics: export raw material from the source's
	// reference, independent of any OS-specific secret store.
	exported, err := ExportMasterKeyMaterial(ctx(), srcCfg)
	if err != nil {
		t.Fatalf("ExportMasterKeyMaterial: %v", err)
	}
	if len(exported) != 32 {
		t.Fatalf("exported key material length = %d, want 32", len(exported))
	}

	// Clean destination: a brand-new state directory that only receives the
	// physically-restored blob objects (simulating a bundle restore) and an
	// explicitly provisioned key file -- never the source's headless store
	// or any reference into it.
	dstRoot := t.TempDir()
	dstState := filepath.Join(dstRoot, "state")
	for _, sub := range []string{dirNameBlobs, filepath.Join(dirNameBlobs, dirNameStaged), filepath.Join(dirNameBlobs, dirNameObjects)} {
		if err := os.MkdirAll(filepath.Join(dstState, sub), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	copyTree(t, filepath.Join(srcCfg.StateDir, dirNameBlobs, dirNameObjects), filepath.Join(dstState, dirNameBlobs, dirNameObjects))
	dstKeyPath := filepath.Join(dstRoot, "provisioned.key")
	if err := os.WriteFile(dstKeyPath, exported, 0o600); err != nil {
		t.Fatal(err)
	}
	zero(exported)

	dst, err := Open(Config{
		StateDir:          dstState,
		CredentialBackend: backendHeadless,
		MasterKeyRef:      "file:" + dstKeyPath,
	})
	if err != nil {
		t.Fatalf("Open clean destination with provisioned key: %v", err)
	}
	defer func() { _ = dst.Close() }()
	rc, err := dst.Blobs().Open(ctx(), digest, 0, 0)
	if err != nil {
		t.Fatalf("clean destination cannot open restored bundle: %v", err)
	}
	got := readAll(t, rc, int64(len(data)))
	if !bytes.Equal(got, data) {
		t.Fatalf("restored bundle content mismatch: got %q want %q", got, data)
	}

	// Missing key: a clean destination that never received the exported
	// material is a named prerequisite failure, not a silent plaintext
	// fallback or a generic error.
	_, err = ExportMasterKeyMaterial(ctx(), Config{MasterKeyRef: "file:" + filepath.Join(dstRoot, "never-provisioned.key")})
	wantCode(t, err, contractCodePrerequisiteMissing)
	_, err = Open(Config{
		StateDir:          filepath.Join(dstRoot, "state2"),
		CredentialBackend: backendHeadless,
		MasterKeyRef:      "file:" + filepath.Join(dstRoot, "never-provisioned.key"),
	})
	wantCode(t, err, contractCodePrerequisiteMissing)
}

// TestBlobImportRefusesCorruptTruncatedOrWrongKeyForeignArtifact proves item
// 3/4's required behavior: a corrupt/truncated artifact or one sealed under
// the wrong (foreign) installation's key cannot replace a live blob, and a
// failed import leaves no trace in the destination's live object store --
// staging for the attempt is isolated and cleaned up, never touching
// whatever (if anything) was already published at that digest.
func TestBlobImportRefusesCorruptTruncatedOrWrongKeyForeignArtifact(t *testing.T) {
	// The foreign (source) installation: a real, different master key from
	// the destination's.
	foreignRoot := t.TempDir()
	foreignKey := make([]byte, 32)
	for i := range foreignKey {
		foreignKey[i] = byte(200 + i)
	}
	foreignKeyPath := filepath.Join(foreignRoot, "master.key")
	if err := os.WriteFile(foreignKeyPath, foreignKey, 0o600); err != nil {
		t.Fatal(err)
	}
	foreign, err := Open(Config{
		StateDir:          filepath.Join(foreignRoot, "state"),
		CredentialBackend: backendHeadless,
		MasterKeyRef:      "file:" + foreignKeyPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	foreign.lockWatchInterval = testLockWatchInterval
	defer func() { _ = foreign.Close() }()
	data := []byte("artifact originating from another installation entirely")
	digest := stageAndPublish(t, foreign, data, 0)
	foreignObjPath := foreign.blobs.objectPath(digest)

	// The destination installation under test.
	dst, dstState := openForTest(t)
	_ = dstState

	openForeign := func(t *testing.T, path string) *os.File {
		t.Helper()
		f, err := OpenForeignBlobFile(path)
		if err != nil {
			t.Fatalf("OpenForeignBlobFile: %v", err)
		}
		t.Cleanup(func() { _ = f.Close() })
		return f
	}

	// Case 1 (control): the correct foreign key and an untampered artifact
	// import cleanly.
	t.Run("correct key imports and matches source bytes", func(t *testing.T) {
		f := openForeign(t, foreignObjPath)
		if err := dst.ImportForeignBlob(ctx(), f, foreignKey, digest); err != nil {
			t.Fatalf("ImportForeignBlob: %v", err)
		}
		rc, err := dst.Blobs().Open(ctx(), digest, 0, 0)
		if err != nil {
			t.Fatalf("Open imported artifact: %v", err)
		}
		got := readAll(t, rc, int64(len(data)))
		if !bytes.Equal(got, data) {
			t.Fatalf("imported content mismatch: got %q want %q", got, data)
		}
	})

	// Case 2: wrong (not the artifact's actual originating installation)
	// key must be refused, and nothing already published must be disturbed.
	t.Run("wrong installation key is refused and live blob is untouched", func(t *testing.T) {
		wrongKey := make([]byte, 32)
		for i := range wrongKey {
			wrongKey[i] = byte(i + 1) // testMasterKey's pattern: a real, different 32-byte key
		}
		otherData := []byte("a second artifact, never imported")
		otherDigest := digestOf(otherData)
		f := openForeign(t, foreignObjPath)
		err := dst.ImportForeignBlob(ctx(), f, wrongKey, otherDigest)
		wantCode(t, err, contractCodeArtifactFaultAlias)
		if _, serr := os.Stat(dst.blobs.objectPath(otherDigest)); !os.IsNotExist(serr) {
			t.Fatal("a wrong-key import reached the live object store")
		}
		// The artifact imported in the control case must be unaffected.
		rc, err := dst.Blobs().Open(ctx(), digest, 0, 0)
		if err != nil {
			t.Fatalf("previously imported artifact was disturbed: %v", err)
		}
		_ = readAll(t, rc, int64(len(data)))
	})

	// Case 3: a truncated foreign artifact must be refused.
	t.Run("truncated foreign artifact is refused", func(t *testing.T) {
		full, err := os.ReadFile(foreignObjPath)
		if err != nil {
			t.Fatal(err)
		}
		truncPath := filepath.Join(t.TempDir(), "truncated.obj")
		if err := os.WriteFile(truncPath, full[:len(full)-10], 0o600); err != nil {
			t.Fatal(err)
		}
		f := openForeign(t, truncPath)
		err = dst.ImportForeignBlob(ctx(), f, foreignKey, digest)
		wantCode(t, err, contractCodeArtifactFaultAlias)
	})

	// Case 4: a corrupted (bit-flipped) foreign artifact must be refused --
	// XOR-flip guarantees an actual change regardless of the original byte
	// (see TestBlobTamperedObjectFailsPublishOverExisting for why a fixed
	// overwrite value is not safe to use here).
	t.Run("corrupted foreign artifact is refused", func(t *testing.T) {
		full, err := os.ReadFile(foreignObjPath)
		if err != nil {
			t.Fatal(err)
		}
		corrupt := append([]byte{}, full...)
		off := chunksStartOffset() + 10
		corrupt[off] ^= 0xFF
		corruptPath := filepath.Join(t.TempDir(), "corrupt.obj")
		if err := os.WriteFile(corruptPath, corrupt, 0o600); err != nil {
			t.Fatal(err)
		}
		f := openForeign(t, corruptPath)
		err = dst.ImportForeignBlob(ctx(), f, foreignKey, digest)
		wantCode(t, err, contractCodeArtifactFaultAlias)
	})

	// Case 5: a symlinked source path is refused by OpenForeignBlobFile
	// itself -- it never follows a symlink to reach the bytes it decrypts.
	t.Run("symlinked source artifact path is refused", func(t *testing.T) {
		linkDir := t.TempDir()
		link := filepath.Join(linkDir, "artifact")
		if err := os.Symlink(foreignObjPath, link); err != nil {
			t.Fatal(err)
		}
		_, err := OpenForeignBlobFile(link)
		if err == nil {
			t.Fatal("OpenForeignBlobFile followed a symlink")
		}
	})
}

// TestBlobRecordCarriesNoSecretMaterial guards BlobRecord -- the platform's
// only backup-manifest-facing type -- against ever growing a field that
// would leak key or path material into a manifest: only digest and size are
// present, exactly the frozen "typed encrypted blob inventory" shape.
func TestBlobRecordCarriesNoSecretMaterial(t *testing.T) {
	rec := BlobRecord{Digest: contract.Digest(strings.Repeat("a", 64)), Size: 42}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 {
		t.Fatalf("BlobRecord has %d JSON fields, want exactly digest and size: %s", len(fields), data)
	}
	if _, ok := fields["digest"]; !ok {
		t.Fatal("BlobRecord is missing digest")
	}
	if _, ok := fields["size"]; !ok {
		t.Fatal("BlobRecord is missing size")
	}
}

// TestExportMasterKeyMaterialErrorsAreRedacted proves the key-transfer
// mechanics never leak raw key bytes into an error message -- the same
// redaction discipline TestErrorsAreRedacted proves for the rest of the
// package, extended to the new export path.
func TestExportMasterKeyMaterialErrorsAreRedacted(t *testing.T) {
	dir := t.TempDir()
	badPath := filepath.Join(dir, "master.key")
	secret := []byte("not-a-valid-32-byte-key-but-secret-looking")
	if err := os.WriteFile(badPath, secret, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ExportMasterKeyMaterial(ctx(), Config{MasterKeyRef: "file:" + badPath})
	if err == nil {
		t.Fatal("expected a decode error for malformed key material")
	}
	msg := errMessage(err)
	if strings.Contains(msg, string(secret)) || strings.Contains(msg, badPath) {
		t.Fatalf("export error leaked secret material or a local path: %q", msg)
	}
}

const testLockWatchInterval = 25_000_000 // 25ms in time.Duration nanoseconds, avoids importing time here
