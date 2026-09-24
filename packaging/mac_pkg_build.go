package packaging

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

// BuildUnsignedMacPkgCandidate assembles only an inert, unsigned producer
// candidate. Signing, notarization, final delivery hash and clean-host install
// are separate gates; this function is never an end-user installer.
func BuildUnsignedMacPkgCandidate(ctx context.Context, release MacReleaseDescriptor, plan MacDownloadPlan, controller, desktop, output string) (int64, string, error) {
	if runtime.GOOS != "darwin" {
		return 0, "", errf(CodeCapabilityUnsupported, "Mac package production requires macOS")
	}
	b, err := NewMacPkgBinding(release, plan)
	if err != nil {
		return 0, "", err
	}
	if !cleanAbs(output) || filepath.Base(output) != plan.Installer.Filename || !cleanAbs(controller) || !cleanAbs(desktop) {
		return 0, "", errf(CodeInvalidInput, "Mac package producer paths are invalid")
	}
	if err := verifyInboxDigest(controller, plan.Controller.Size, plan.Controller.SHA256); err != nil {
		return 0, "", err
	}
	if err := verifyInboxDigest(desktop, plan.Desktop.Size, plan.Desktop.SHA256); err != nil {
		return 0, "", err
	}
	stage, err := os.MkdirTemp(filepath.Dir(output), ".zatiti-pkg-*")
	if err != nil {
		return 0, "", errWrap(CodePrerequisiteMissing, "Mac package producer staging is unavailable", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := os.Chmod(stage, 0o700); err != nil {
		return 0, "", errWrap(CodePrerequisiteMissing, "Mac package producer staging cannot be secured", err)
	}
	root := filepath.Join(stage, "root")
	inbox := filepath.Join(root, "Library", "Application Support", "zatiti-installer", "inbox", strconv.FormatInt(b.ReleaseSequence, 10), b.Arch)
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		return 0, "", errWrap(CodePrerequisiteMissing, "Mac package producer inbox cannot be created", err)
	}
	bindingRaw, err := EncodeMacPkgBinding(b)
	if err != nil {
		return 0, "", err
	}
	if err := os.WriteFile(filepath.Join(inbox, "binding.json"), bindingRaw, 0o600); err != nil {
		return 0, "", errWrap(CodePrerequisiteMissing, "Mac package producer binding cannot be staged", err)
	}
	for _, asset := range []struct {
		src, name string
		meta      MacDeliveryAsset
	}{{controller, "controller.tar.gz", plan.Controller}, {desktop, "desktop.tar.gz", plan.Desktop}} {
		if err := copyMacPkgArchive(asset.src, filepath.Join(inbox, asset.name), asset.meta); err != nil {
			return 0, "", err
		}
	}
	dist, err := macPkgDistributionXML(release.Version, plan.Arch)
	if err != nil {
		return 0, "", err
	}
	distPath := filepath.Join(stage, "Distribution.xml")
	if err := os.WriteFile(distPath, dist, 0o600); err != nil {
		return 0, "", errWrap(CodePrerequisiteMissing, "Mac package Distribution cannot be staged", err)
	}
	component := filepath.Join(stage, "component.pkg")
	product := filepath.Join(stage, "product.pkg")
	bounded, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	for _, args := range [][]string{
		{"/usr/bin/pkgbuild", "--root", root, "--identifier", macPkgComponentID, "--version", release.Version, "--install-location", "/", "--ownership", "preserve", component},
		{"/usr/bin/productbuild", "--distribution", distPath, "--package-path", stage, product},
	} {
		cmd := exec.CommandContext(bounded, args[0], args[1:]...)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		if err := cmd.Run(); err != nil {
			return 0, "", errWrap(CodePrerequisiteMissing, "Mac package producer system tool failed", err)
		}
	}
	if err := os.Chmod(product, 0o600); err != nil {
		return 0, "", errWrap(CodePrerequisiteMissing, "Mac package candidate cannot be secured", err)
	}
	if err := inspectMacPkgPayload(bounded, product, release, plan); err != nil {
		return 0, "", err
	}
	if err := verifyInboxDigest(controller, plan.Controller.Size, plan.Controller.SHA256); err != nil {
		return 0, "", err
	}
	if err := verifyInboxDigest(desktop, plan.Desktop.Size, plan.Desktop.SHA256); err != nil {
		return 0, "", err
	}
	f, err := os.Open(product)
	if err != nil {
		return 0, "", errWrap(CodePrerequisiteMissing, "Mac package candidate cannot be measured", err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	size, err := io.Copy(h, io.LimitReader(f, maxMacAssetBytes+1))
	if err != nil || size <= 0 || size > maxMacAssetBytes {
		return 0, "", errf(CodeVerificationFailed, "Mac package candidate exceeds release bounds")
	}
	if err := os.Link(product, output); err != nil {
		return 0, "", errWrap(CodeConflict, "Mac package candidate output already exists or cannot be published", err)
	}
	return size, hex.EncodeToString(h.Sum(nil)), nil
}

func copyMacPkgArchive(src, dest string, meta MacDeliveryAsset) error {
	in, err := os.Open(src)
	if err != nil {
		return errWrap(CodeVerificationFailed, "Mac package input archive cannot be opened", err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errWrap(CodePrerequisiteMissing, "Mac package archive cannot be staged", err)
	}
	_, copyErr := io.CopyN(out, in, meta.Size)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		return errf(CodeVerificationFailed, "Mac package input archive could not be copied")
	}
	return verifyInboxDigest(dest, meta.Size, meta.SHA256)
}
