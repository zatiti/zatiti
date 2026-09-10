//go:build linux

package platform

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// platformSupported reports whether this Go platform is a supported Zatiti
// host. Windows support is not claimed by the product contract.
func platformSupported() error { return nil }

// defaultBackendIsKeychain: Linux has no default secure store until a Secret
// Service client is qualified, so the backend must be configured explicitly.
const defaultBackendIsKeychain = false

// checkFilesystemLocal refuses a state directory on a network filesystem.
// Linux statfs exposes no local/remote flag, so the filesystem type of the
// longest matching mount point from /proc/self/mountinfo is inspected.
func checkFilesystemLocal(dir string) error {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return errWrap(contractCodeControllerUnavailable, "filesystem type cannot be determined", err)
	}
	if err := mountinfoLocalErr(parseMountinfo(data), dir); err != nil {
		return err
	}
	return nil
}

type mountEntry struct {
	mountPoint string
	fstype     string
}

// parseMountinfo parses the kernel mount table format:
//
//	id parent major:minor root mountpoint options... - fstype source superopts
func parseMountinfo(data []byte) []mountEntry {
	var entries []mountEntry
	for _, line := range strings.Split(string(data), "\n") {
		sep := strings.Index(line, " - ")
		if sep < 0 {
			continue
		}
		left := strings.Fields(line[:sep])
		right := strings.Fields(line[sep+3:])
		if len(left) < 5 || len(right) < 1 {
			continue
		}
		mp, err := unescapeMountPath(left[4])
		if err != nil {
			continue
		}
		entries = append(entries, mountEntry{mountPoint: mp, fstype: right[0]})
	}
	return entries
}

// unescapeMountPath decodes the octal escapes the kernel uses for space,
// tab, backslash and newline in mountinfo paths.
func unescapeMountPath(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		if i+3 >= len(s) {
			return "", errf(contractCodeControllerUnavailable, "filesystem table is malformed")
		}
		v, err := strconv.ParseUint(s[i+1:i+4], 8, 8)
		if err != nil {
			return "", errf(contractCodeControllerUnavailable, "filesystem table is malformed")
		}
		b.WriteByte(byte(v))
		i += 3
	}
	return b.String(), nil
}

// mountinfoLocalErr is the decision over parsed entries, split out for
// direct testing with synthetic tables.
func mountinfoLocalErr(entries []mountEntry, dir string) error {
	best := -1
	bestLen := 0
	for i, e := range entries {
		if !withinMount(dir, e.mountPoint) {
			continue
		}
		if l := len(e.mountPoint); l >= bestLen {
			best, bestLen = i, l
		}
	}
	if best < 0 {
		return errWrap(contractCodeControllerUnavailable, "filesystem type cannot be determined", nil)
	}
	if networkFilesystem(entries[best].fstype) {
		return errf(contractCodeInvalidInput, "state directory must not reside on a network filesystem")
	}
	return nil
}

// withinMount reports whether dir is mount itself or below it.
func withinMount(dir, mount string) bool {
	if mount == "/" {
		return true
	}
	if dir == mount {
		return true
	}
	return strings.HasPrefix(dir, mount+"/")
}

// networkFilesystem names the filesystem types on which exclusive file
// locks and rename durability are not trusted for installation state.
func networkFilesystem(fstype string) bool {
	switch fstype {
	case "nfs", "nfs4", "cifs", "smbfs", "smb2", "afs", "ncpfs", "coda",
		"9p", "ceph", "cephfuse", "fuse.ceph", "fuse.cephfs", "fuse.sshfs",
		"fuse.rclone", "fuse.encfs", "lustre", "glusterfs", "fuse.glusterfs",
		"davfs2", "fuse.davfs2", "acfs", "ocfs2", "gpfs", "panfs", "beegfs":
		return true
	}
	return false
}

// defaultProbeFreeSpace reports available bytes for a non-root user on the
// filesystem holding dir.
func defaultProbeFreeSpace(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	avail := int64(st.Bavail) * int64(st.Bsize)
	if avail < 0 {
		return 0, nil
	}
	return uint64(avail), nil
}
