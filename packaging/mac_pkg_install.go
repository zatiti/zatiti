package packaging

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// runFixedMacPkgInstaller is the final command boundary for a separately
// verified package. It deliberately does not implement MacInstallRunner:
// package payload/signing verification and post-install activation remain
// prerequisites before RunMacBootstrap may be wired to production.
func runFixedMacPkgInstaller(ctx context.Context, pkg string, expected MacDeliveryAsset, runner Runner) error {
	if runtime.GOOS != "darwin" {
		return errf(CodeCapabilityUnsupported, "Mac package installation requires macOS")
	}
	if runner == nil || !cleanAbs(pkg) || expected.Distribution != "installer" || filepath.Base(pkg) != expected.Filename {
		return errf(CodeInvalidInput, "Mac package installer inputs are invalid")
	}
	parent := filepath.Dir(pkg)
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || !acceptedOwned(info) || info.Mode().Perm() != 0o700 {
		return errf(CodeVerificationFailed, "Mac package staging directory is not owner-only")
	}
	// Rehash the exact privately staged file immediately before invocation;
	// this catches changes after the package verifier's earlier inspection.
	if err := verifyInboxDigest(pkg, expected.Size, expected.SHA256); err != nil {
		return err
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if err := runner.Run(bounded, "/usr/sbin/installer", []string{"-pkg", pkg, "-target", "CurrentUserHomeDirectory"}); err != nil {
		return errWrap(CodePrerequisiteMissing, "Mac system installer failed", err)
	}
	return nil
}
