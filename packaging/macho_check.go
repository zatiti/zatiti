package packaging

import (
	"bytes"
	"debug/macho"
	"errors"
	"io"
	"os"
)

// A controller component remains a single native executable. Desktop
// bundles may mix thin and universal objects, but every Mach-O must contain
// the component's declared architecture.
const maxMacExecutableBytes int64 = 512 << 20

func targetCPU(arch string) (macho.Cpu, error) {
	switch arch {
	case "amd64":
		return macho.CpuAmd64, nil
	case "arm64":
		return macho.CpuArm64, nil
	default:
		return 0, errf(CodeCapabilityUnsupported, "Mac target architecture is unsupported")
	}
}

func verifyThinMachO(path, arch string) error {
	want, err := targetCPU(arch)
	if err != nil {
		return err
	}
	fat, err := macho.OpenFat(path)
	if err == nil {
		_ = fat.Close()
		return errf(CodeVerificationFailed, "Mac controller executable is universal; this release requires one native architecture")
	}
	if !errors.Is(err, macho.ErrNotFat) {
		return errWrap(CodeVerificationFailed, "Mac executable is not a valid Mach-O file", err)
	}
	f, err := macho.Open(path)
	if err != nil {
		return errWrap(CodeVerificationFailed, "Mac executable is not a valid thin Mach-O file", err)
	}
	defer func() { _ = f.Close() }()
	if f.Cpu != want || f.Type != macho.TypeExec {
		return errf(CodeVerificationFailed, "Mac executable CPU or file type differs from its manifest target")
	}
	return nil
}

// inspectArchivedMachO streams one regular archive entry. Non-Mach-O assets
// are drained without staging; Mach-O objects are bounded and staged so the
// standard parser can validate thin and fat slice structures through ReaderAt.
func inspectArchivedMachO(r io.Reader, size int64, arch string, executable bool) (bool, error) {
	if size < 0 {
		return false, errf(CodeVerificationFailed, "Mac bundle entry has invalid size")
	}
	var prefix [4]byte
	n, err := io.ReadFull(r, prefix[:])
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return false, errWrap(CodeVerificationFailed, "Mac bundle entry could not be read", err)
	}
	if !isMachOMagic(prefix[:n]) {
		if executable {
			return false, errf(CodeVerificationFailed, "Mac desktop executable is not Mach-O")
		}
		if _, err := io.Copy(io.Discard, r); err != nil {
			return false, errWrap(CodeVerificationFailed, "Mac bundle entry could not be read", err)
		}
		return false, nil
	}
	if size <= 0 || size > maxMacExecutableBytes {
		return true, errf(CodeVerificationFailed, "Mac Mach-O object exceeds the inspection limit")
	}
	f, err := os.CreateTemp("", "zatiti-macho-*")
	if err != nil {
		return true, errWrap(CodeInternalError, "Mac object could not be staged for inspection", err)
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.Write(prefix[:n]); err != nil {
		_ = f.Close()
		return true, errWrap(CodeInternalError, "Mac object staging failed", err)
	}
	if _, err := io.CopyN(f, r, size-int64(n)); err != nil {
		_ = f.Close()
		return true, errWrap(CodeVerificationFailed, "Mac object could not be fully read", err)
	}
	if err := f.Close(); err != nil {
		return true, errWrap(CodeInternalError, "Mac object staging failed", err)
	}
	return true, verifyDesktopMachO(f.Name(), arch, executable)
}

func isMachOMagic(p []byte) bool {
	if len(p) != 4 {
		return false
	}
	switch {
	case bytes.Equal(p, []byte{0xfe, 0xed, 0xfa, 0xce}), bytes.Equal(p, []byte{0xce, 0xfa, 0xed, 0xfe}),
		bytes.Equal(p, []byte{0xfe, 0xed, 0xfa, 0xcf}), bytes.Equal(p, []byte{0xcf, 0xfa, 0xed, 0xfe}),
		bytes.Equal(p, []byte{0xca, 0xfe, 0xba, 0xbe}), bytes.Equal(p, []byte{0xbe, 0xba, 0xfe, 0xca}),
		bytes.Equal(p, []byte{0xca, 0xfe, 0xba, 0xbf}), bytes.Equal(p, []byte{0xbf, 0xba, 0xfe, 0xca}):
		return true
	}
	return false
}

func verifyDesktopMachO(path, arch string, executable bool) error {
	want, err := targetCPU(arch)
	if err != nil {
		return err
	}
	fat, err := macho.OpenFat(path)
	if err == nil {
		defer func() { _ = fat.Close() }()
		found := false
		for _, slice := range fat.Arches {
			if slice.Cpu != macho.CpuAmd64 && slice.Cpu != macho.CpuArm64 {
				return errf(CodeVerificationFailed, "Mac universal object has an unsupported architecture")
			}
			if slice.File == nil || slice.File.Cpu != slice.Cpu {
				return errf(CodeVerificationFailed, "Mac universal object has an invalid architecture")
			}
			if executable && slice.Type != macho.TypeExec {
				return errf(CodeVerificationFailed, "Mac desktop runner has a non-executable slice")
			}
			if slice.Cpu == want {
				found = true
			}
		}
		if !found {
			return errf(CodeVerificationFailed, "Mac universal object lacks its target architecture")
		}
		return nil
	}
	if !errors.Is(err, macho.ErrNotFat) {
		return errWrap(CodeVerificationFailed, "Mac bundle object is not a valid universal Mach-O", err)
	}
	f, err := macho.Open(path)
	if err != nil {
		return errWrap(CodeVerificationFailed, "Mac bundle object is not a valid thin Mach-O", err)
	}
	defer func() { _ = f.Close() }()
	if f.Cpu != want {
		return errf(CodeVerificationFailed, "Mac bundle object lacks its target architecture")
	}
	if executable && f.Type != macho.TypeExec {
		return errf(CodeVerificationFailed, "Mac desktop runner is not executable Mach-O")
	}
	return nil
}
