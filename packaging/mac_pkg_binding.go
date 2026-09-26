package packaging

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"
)

const (
	MacPkgBindingSchema   = "zatiti.mac_pkg_binding/v1"
	MaxMacPkgBindingBytes = 4096
)

// MacPkgBinding contains only acyclic facts: the outer delivery signs the
// package hash, while this package binds the inner descriptor and two inputs.
type MacPkgBinding struct {
	Schema                  string `json:"schema"`
	ReleaseSequence         int64  `json:"release_sequence"`
	Version                 string `json:"version"`
	Arch                    string `json:"arch"`
	ReleaseDescriptorSHA256 string `json:"release_descriptor_sha256"`
	ControllerSHA256        string `json:"controller_sha256"`
	DesktopSHA256           string `json:"desktop_sha256"`
}

func (b MacPkgBinding) validate() error {
	if b.Schema != MacPkgBindingSchema || b.ReleaseSequence <= 0 || !versionPattern.MatchString(b.Version) || (b.Arch != "amd64" && b.Arch != "arm64") || !digestPattern.MatchString(b.ReleaseDescriptorSHA256) || !digestPattern.MatchString(b.ControllerSHA256) || !digestPattern.MatchString(b.DesktopSHA256) {
		return errf(CodeInvalidInput, "Mac package binding has invalid fields or unsupported schema")
	}
	return nil
}

func EncodeMacPkgBinding(b MacPkgBinding) ([]byte, error) {
	if err := b.validate(); err != nil {
		return nil, err
	}
	raw, err := canonicalJSON(b)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxMacPkgBindingBytes {
		return nil, errf(CodeInvalidInput, "Mac package binding exceeds 4096 bytes")
	}
	return raw, nil
}

func DecodeMacPkgBinding(raw []byte) (MacPkgBinding, error) {
	var b MacPkgBinding
	if len(raw) == 0 || len(raw) > MaxMacPkgBindingBytes || !utf8.Valid(raw) {
		return b, errf(CodeInvalidInput, "Mac package binding is empty, oversized or not UTF-8")
	}
	if err := strictDecode(raw, &b); err != nil {
		return b, err
	}
	canonical, err := EncodeMacPkgBinding(b)
	if err != nil {
		return b, err
	}
	if !bytes.Equal(raw, canonical) {
		return b, errf(CodeInvalidInput, "Mac package binding is not canonical JSON")
	}
	return b, nil
}

func NewMacPkgBinding(release MacReleaseDescriptor, plan MacDownloadPlan) (MacPkgBinding, error) {
	raw, err := EncodeMacRelease(release)
	if err != nil {
		return MacPkgBinding{}, err
	}
	if plan.ReleaseSequence <= 0 || plan.Arch != plan.Controller.Arch || plan.Arch != plan.Desktop.Arch || plan.Arch != plan.Installer.Arch || plan.Controller.Distribution != DistributionController || plan.Desktop.Distribution != DistributionDesktop || plan.Installer.Distribution != "installer" || plan.Controller.Size <= 0 || plan.Desktop.Size <= 0 || plan.Controller.Size > maxMacAssetBytes || plan.Desktop.Size > maxMacAssetBytes {
		return MacPkgBinding{}, errf(CodeInvalidInput, "Mac package plan is incomplete")
	}
	stem := "zatiti-" + release.Version + "-darwin-" + plan.Arch + "-"
	if plan.Controller.Filename != stem+"controller.tar.gz" || plan.Desktop.Filename != stem+"desktop.tar.gz" || plan.Installer.Filename != stem+"installer.pkg" {
		return MacPkgBinding{}, errf(CodeInvalidInput, "Mac package plan filenames differ from its release")
	}
	sum := sha256.Sum256(raw)
	b := MacPkgBinding{Schema: MacPkgBindingSchema, ReleaseSequence: plan.ReleaseSequence, Version: release.Version, Arch: plan.Arch, ReleaseDescriptorSHA256: hex.EncodeToString(sum[:]), ControllerSHA256: plan.Controller.SHA256, DesktopSHA256: plan.Desktop.SHA256}
	if _, err := EncodeMacPkgBinding(b); err != nil {
		return MacPkgBinding{}, err
	}
	return b, nil
}

func VerifyMacPkgBinding(raw []byte, release MacReleaseDescriptor, plan MacDownloadPlan) error {
	got, err := DecodeMacPkgBinding(raw)
	if err != nil {
		return err
	}
	want, err := NewMacPkgBinding(release, plan)
	if err != nil {
		return err
	}
	if got != want {
		return errf(CodeVerificationFailed, "Mac package binding differs from the signed release plan")
	}
	return nil
}

// MacPkgInboxPath names the fixed, inert, per-user package landing zone.
func MacPkgInboxPath(home string, b MacPkgBinding) (string, error) {
	if err := b.validate(); err != nil {
		return "", err
	}
	if !filepath.IsAbs(home) || filepath.Clean(home) != home || strings.IndexByte(home, 0) >= 0 {
		return "", errf(CodeInvalidInput, "Mac package home path is unsafe")
	}
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil || resolved != home {
		return "", errf(CodeInvalidInput, "Mac package home path traverses a symlink")
	}
	return filepath.Join(home, "Library", "Application Support", "zatiti-installer", "inbox", strconv.FormatInt(b.ReleaseSequence, 10), b.Arch), nil
}

// VerifyMacPkgInbox checks only staged, inert package bytes; successful
// activation still requires signed-tree verification and Apply.
func VerifyMacPkgInbox(home string, b MacPkgBinding, release MacReleaseDescriptor, plan MacDownloadPlan) (MacStagedAssets, error) {
	var out MacStagedAssets
	if err := b.validate(); err != nil {
		return out, err
	}
	dir, err := MacPkgInboxPath(home, b)
	if err != nil {
		return out, err
	}
	for _, p := range []string{home, filepath.Join(home, "Library"), filepath.Join(home, "Library", "Application Support"), filepath.Join(home, "Library", "Application Support", "zatiti-installer"), filepath.Join(home, "Library", "Application Support", "zatiti-installer", "inbox"), filepath.Dir(dir), dir} {
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() || !acceptedOwned(info) {
			return out, errf(CodeVerificationFailed, "Mac package inbox directory is unsafe")
		}
		if p != home && p != filepath.Join(home, "Library") && p != filepath.Join(home, "Library", "Application Support") && info.Mode().Perm() != 0o700 {
			return out, errf(CodeVerificationFailed, "Mac package inbox directory is not owner-only")
		}
	}
	f, err := os.Open(dir)
	if err != nil {
		return out, errWrap(CodeVerificationFailed, "Mac package inbox cannot be opened", err)
	}
	names, readErr := f.Readdirnames(4)
	closeErr := f.Close()
	if (readErr != nil && !errors.Is(readErr, io.EOF)) || closeErr != nil || len(names) != 3 {
		return out, errf(CodeVerificationFailed, "Mac package inbox must contain exactly three files")
	}
	wantNames := map[string]bool{"binding.json": true, "controller.tar.gz": true, "desktop.tar.gz": true}
	for _, name := range names {
		if !wantNames[name] {
			return out, errf(CodeVerificationFailed, "Mac package inbox has an unexpected entry")
		}
		delete(wantNames, name)
	}
	if len(wantNames) != 0 {
		return out, errf(CodeVerificationFailed, "Mac package inbox is incomplete")
	}
	bindRaw, err := readInboxFile(filepath.Join(dir, "binding.json"), MaxMacPkgBindingBytes)
	if err != nil {
		return out, err
	}
	if err := VerifyMacPkgBinding(bindRaw, release, plan); err != nil {
		return out, err
	}
	if decoded, err := DecodeMacPkgBinding(bindRaw); err != nil || decoded != b {
		return out, errf(CodeVerificationFailed, "Mac package binding changed after verification")
	}
	if err := verifyInboxDigest(filepath.Join(dir, "controller.tar.gz"), plan.Controller.Size, b.ControllerSHA256); err != nil {
		return out, err
	}
	if err := verifyInboxDigest(filepath.Join(dir, "desktop.tar.gz"), plan.Desktop.Size, b.DesktopSHA256); err != nil {
		return out, err
	}
	out.Controller = filepath.Join(dir, "controller.tar.gz")
	out.Desktop = filepath.Join(dir, "desktop.tar.gz")
	return out, nil
}

func readInboxFile(path string, max int) ([]byte, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, errWrap(CodeVerificationFailed, "Mac package inbox file cannot be opened safely", err)
	}
	f := os.NewFile(uintptr(fd), path)
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !acceptedFileInfo(info) || info.Size() <= 0 || info.Size() > int64(max) {
		return nil, errf(CodeVerificationFailed, "Mac package inbox file is unsafe or oversized")
	}
	raw, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil || int64(len(raw)) != info.Size() {
		return nil, errf(CodeVerificationFailed, "Mac package inbox file changed while reading")
	}
	return raw, nil
}

func verifyInboxDigest(path string, size int64, digest string) error {
	if size <= 0 || size > maxMacAssetBytes || !digestPattern.MatchString(digest) {
		return errf(CodeInvalidInput, "Mac package component declaration is invalid")
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return errWrap(CodeVerificationFailed, "Mac package component cannot be opened safely", err)
	}
	f := os.NewFile(uintptr(fd), path)
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !acceptedFileInfo(info) || info.Size() != size {
		return errf(CodeVerificationFailed, "Mac package component mode or size differs from signed delivery")
	}
	h := sha256.New()
	n, err := io.CopyN(h, f, size+1)
	if !errors.Is(err, io.EOF) || n != size || hex.EncodeToString(h.Sum(nil)) != digest {
		return errf(CodeVerificationFailed, "Mac package component differs from signed delivery")
	}
	return nil
}
