//go:build linux

package platform

import (
	"testing"
)

func TestParseMountinfo(t *testing.T) {
	table := []byte(`22 28 0:21 / /sys rw,nosuid,nodev,noexec,relatime shared:7 - sysfs sysfs rw
24 28 0:23 / /dev rw,nosuid,relatime - devtmpfs udev rw,size=4096k
36 28 259:2 / /mnt/data rw,relatime - ext4 /dev/nvme0n1p2 rw
38 36 0:55 / /mnt/data/share rw - nfs4 server:/share rw
40 28 259:3 / /mnt/with\040space rw - xfs /dev/sdb1 rw
`)

	entries := parseMountinfo(table)
	if len(entries) != 5 {
		t.Fatalf("parsed %d entries, want 5", len(entries))
	}
	if entries[3].fstype != "nfs4" || entries[3].mountPoint != "/mnt/data/share" {
		t.Fatalf("nfs entry = %+v", entries[3])
	}
	if entries[4].mountPoint != "/mnt/with space" {
		t.Fatalf("escaped mount point = %q", entries[4].mountPoint)
	}
}

func TestMountinfoLocalErrLongestPrefixWins(t *testing.T) {
	entries := []mountEntry{
		{mountPoint: "/", fstype: "ext4"},
		{mountPoint: "/mnt", fstype: "nfs4"},
		{mountPoint: "/mnt/data", fstype: "ext4"},
	}

	// A path under the local submount is governed by ext4.
	if err := mountinfoLocalErr(entries, "/mnt/data/state"); err != nil {
		t.Fatalf("state under local submount refused: %v", err)
	}
	// A path under the network mount itself is refused.
	err := mountinfoLocalErr(entries, "/mnt/state")
	wantCode(t, err, contractCodeInvalidInput)
	// The mount itself is covered.
	if err := mountinfoLocalErr(entries, "/mnt/data"); err != nil {
		t.Fatalf("mount root refused: %v", err)
	}
	// Root mount covers everything.
	if err := mountinfoLocalErr([]mountEntry{{mountPoint: "/", fstype: "ext4"}}, "/anywhere"); err != nil {
		t.Fatalf("root mount refused: %v", err)
	}
	// No matching mount is undeterminable, which is fail-closed.
	err = mountinfoLocalErr(nil, "/mnt/data")
	wantCode(t, err, contractCodeControllerUnavailable)
}

func TestNetworkFilesystemDenylist(t *testing.T) {
	for _, fs := range []string{"nfs", "nfs4", "cifs", "9p", "ceph", "fuse.sshfs", "lustre", "glusterfs"} {
		if !networkFilesystem(fs) {
			t.Fatalf("%s not treated as a network filesystem", fs)
		}
	}
	for _, fs := range []string{"ext4", "xfs", "btrfs", "zfs", "apfs", "tmpfs", "f2fs"} {
		if networkFilesystem(fs) {
			t.Fatalf("%s wrongly denied", fs)
		}
	}
}

func TestCheckFilesystemLocalAcceptsTempDir(t *testing.T) {
	if err := checkFilesystemLocal(t.TempDir()); err != nil {
		t.Fatalf("local temp filesystem refused: %v", err)
	}
}

func TestLinuxRequiresExplicitBackend(t *testing.T) {
	if defaultBackendIsKeychain {
		t.Fatal("linux must not default to the keychain backend")
	}
	_, err := Open(Config{StateDir: t.TempDir(), MasterKeyRef: writeKeyFile(t, testMasterKey(t))})
	wantCode(t, err, contractCodeInvalidInput)
	if msg := errMessage(err); msg != "credential backend must be configured explicitly on this platform; use headless or keychain" {
		t.Fatalf("unexpected default-backend message: %q", msg)
	}
}
