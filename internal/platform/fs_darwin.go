//go:build darwin

package platform

import (
	"syscall"
)

// mntLocal mirrors MNT_LOCAL from sys/mount.h: the filesystem is a local
// mount rather than a network one. The syscall package does not export it.
const mntLocal = 0x00001000

// platformSupported reports whether this Go platform is a supported Zatiti
// host. Windows support is not claimed by the product contract.
func platformSupported() error { return nil }

// defaultBackendIsKeychain: macOS ships with the Keychain available, so an
// empty credential backend defaults to it.
const defaultBackendIsKeychain = true

// checkFilesystemLocal refuses a state directory on a network filesystem:
// OS file locks and rename durability there are not trustworthy enough for
// exclusive installation ownership.
func checkFilesystemLocal(dir string) error {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return errWrap(contractCodeControllerUnavailable, "state directory filesystem cannot be inspected", err)
	}
	return localFlagsErr(uint64(st.Flags))
}

// localFlagsErr is the mount-flag decision, split out for direct testing.
func localFlagsErr(flags uint64) error {
	if flags&mntLocal == 0 {
		return errf(contractCodeInvalidInput, "state directory must not reside on a network filesystem")
	}
	return nil
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
