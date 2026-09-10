//go:build darwin

package platform

import (
	"testing"
)

func TestLocalFlagsErrDecision(t *testing.T) {
	if err := localFlagsErr(mntLocal); err != nil {
		t.Fatalf("local mount refused: %v", err)
	}
	err := localFlagsErr(0)
	wantCode(t, err, contractCodeInvalidInput)
	if msg := errMessage(err); msg != "state directory must not reside on a network filesystem" {
		t.Fatalf("unexpected network-filesystem message: %q", msg)
	}
	// A mount with the local flag plus other flags set stays local.
	if err := localFlagsErr(mntLocal | 0x1 | 0x4); err != nil {
		t.Fatalf("local mount with extra flags refused: %v", err)
	}
}

func TestDefaultProbeFreeSpaceReportsLocalDisk(t *testing.T) {
	free, err := defaultProbeFreeSpace(t.TempDir())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if free == 0 {
		t.Fatal("probe reported zero free space on a healthy filesystem")
	}
}

func TestDarwinDefaultsToKeychainWhenUnconfigured(t *testing.T) {
	// Empty backend + file: master key opens without touching the keychain.
	p, err := Open(Config{
		StateDir:     t.TempDir(),
		MasterKeyRef: writeKeyFile(t, testMasterKey(t)),
	})
	if err != nil {
		t.Fatalf("default-backend Open: %v", err)
	}
	_ = p.Close()
}

func TestCheckFilesystemLocalAcceptsTempDir(t *testing.T) {
	if err := checkFilesystemLocal(t.TempDir()); err != nil {
		t.Fatalf("local temp filesystem refused: %v", err)
	}
}
