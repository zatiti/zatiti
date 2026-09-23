package packaging

import (
	"debug/macho"
	"errors"
	"io"
	"os"
)

// A single target gets a single native executable. Universal binaries are
// refused here so an arm64/amd64 manifest cannot hide a second architecture.
// This checks Mach-O structure and CPU, not Apple code signing or runtime use.
const maxMacExecutableBytes int64 = 512 << 20

func verifyThinMachO(path, arch string) error {
	fat, err := macho.OpenFat(path)
	if err == nil {
		_ = fat.Close()
		return errf(CodeVerificationFailed, "Mac executable is universal; this release requires one native architecture per component")
	}
	if !errors.Is(err, macho.ErrNotFat) {
		return errWrap(CodeVerificationFailed, "Mac executable is not a valid Mach-O file", err)
	}
	f, err := macho.Open(path)
	if err != nil {
		return errWrap(CodeVerificationFailed, "Mac executable is not a valid thin Mach-O file", err)
	}
	defer func() { _ = f.Close() }()
	want := macho.CpuAmd64
	if arch == "arm64" {
		want = macho.CpuArm64
	} else if arch != "amd64" {
		return errf(CodeCapabilityUnsupported, "Mac target architecture is unsupported")
	}
	if f.Cpu != want || f.Type != macho.TypeExec {
		return errf(CodeVerificationFailed, "Mac executable CPU or file type differs from its manifest target")
	}
	return nil
}

// verifyArchivedMachO copies only the declared executable to a protected
// temporary file so debug/macho can inspect it as a ReaderAt. The bound
// prevents an archive from exhausting memory or scratch space.
func verifyArchivedMachO(r io.Reader, size int64, arch string) error {
	if size <= 0 || size > maxMacExecutableBytes {
		return errf(CodeVerificationFailed, "Mac executable exceeds the inspection limit")
	}
	f, err := os.CreateTemp("", "zatiti-macho-*")
	if err != nil {
		return errWrap(CodeInternalError, "Mac executable could not be staged for inspection", err)
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := io.CopyN(f, r, size); err != nil {
		_ = f.Close()
		return errWrap(CodeVerificationFailed, "Mac executable could not be fully read", err)
	}
	if err := f.Close(); err != nil {
		return errWrap(CodeInternalError, "Mac executable staging failed", err)
	}
	return verifyThinMachO(f.Name(), arch)
}
