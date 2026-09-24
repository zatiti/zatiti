package packaging

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// MacAssetSource opens one URL from an already verified delivery record.
// Production uses HTTPSMacAssetSource; tests can provide bounded local bytes.
type MacAssetSource interface {
	Open(context.Context, MacDeliveryAsset) (io.ReadCloser, error)
}

// MacInstallVerifier must prove the signed installer package's Developer ID,
// notarization, and binding to the two verified component archives. Revision 7
// has not frozen the package payload layout or signing identity, so this
// capability has no fabricated default implementation.
type MacInstallVerifier interface {
	Verify(context.Context, MacReleaseDescriptor, MacDownloadPlan, MacStagedAssets) error
}

// MacInstallRunner invokes only the verified package. It must preserve user
// state and supply bounded, fixed install arguments; no arbitrary command is
// accepted from the delivery record.
type MacInstallRunner interface {
	Installed(context.Context, MacReleaseDescriptor, string) (bool, error)
	Install(context.Context, string) error
}

// MacSequenceWatermark is protected local monotonic release state. The caller
// must use an owner-only durable implementation; a nil store fails closed.
type MacAcceptedRelease struct {
	Sequence       int64
	DeliverySHA256 string
}
type MacSequenceWatermark interface {
	Lock(context.Context) (func(), error)
	Load(context.Context) (MacAcceptedRelease, error)
	Advance(context.Context, MacAcceptedRelease) error
}

type MacStagedAssets struct{ Controller, Desktop, Installer string }

type MacBootstrapInput struct {
	Delivery    []byte
	Signature   MacDeliverySignature
	TrustedKeys []ed25519.PublicKey
	Now         time.Time
	Source      MacAssetSource
	Verifier    MacInstallVerifier
	Runner      MacInstallRunner
	Watermark   MacSequenceWatermark
	// NativeArch is for test seams. Production passes DetectNativeMacArch.
	NativeArch func(context.Context) (string, error)
}

// RunMacBootstrap verifies metadata before opening any URL, then downloads
// exactly the native architecture's three signed assets into a private temp
// directory. Nothing activates until every digest and the installer's
// separately supplied native verification succeeds. Every exit cleans staging.
func RunMacBootstrap(ctx context.Context, in MacBootstrapInput) error {
	if in.Source == nil || in.Verifier == nil || in.Runner == nil || in.Watermark == nil || in.NativeArch == nil {
		return errf(CodePrerequisiteMissing, "Mac bootstrap trust, installer or watermark capability is unavailable")
	}
	unlock, err := in.Watermark.Lock(ctx)
	if err != nil {
		return errWrap(CodeConflict, "Mac bootstrap lock is unavailable", err)
	}
	if unlock == nil {
		return errf(CodeConflict, "Mac bootstrap lock is unavailable")
	}
	defer unlock()
	accepted, err := in.Watermark.Load(ctx)
	if err != nil {
		return errWrap(CodePrerequisiteMissing, "Mac release watermark is unavailable", err)
	}
	if accepted.Sequence < 0 || (accepted.Sequence == 0 && accepted.DeliverySHA256 != "") || (accepted.Sequence > 0 && !digestPattern.MatchString(accepted.DeliverySHA256)) {
		return errf(CodeVerificationFailed, "Mac release watermark is invalid")
	}
	// Verify both signature layers and time before choosing/download anything.
	d, err := VerifyMacDelivery(in.Delivery, in.Signature, in.TrustedKeys, in.Now, 0)
	if err != nil {
		return err
	}
	digestBytes := sha256.Sum256(in.Delivery)
	candidate := MacAcceptedRelease{Sequence: d.ReleaseSequence, DeliverySHA256: hex.EncodeToString(digestBytes[:])}
	arch, err := in.NativeArch(ctx)
	if err != nil {
		return err
	}
	plan, err := SelectMacDownloadPlan(d, arch)
	if err != nil {
		return err
	}
	if d.ReleaseSequence < accepted.Sequence {
		return errf(CodeVerificationFailed, "Mac delivery is older than the accepted release")
	}
	// A previous attempt may have installed successfully and failed only while
	// saving its watermark. Inspect before downloading or invoking again.
	if d.ReleaseSequence > accepted.Sequence {
		installed, inspectErr := in.Runner.Installed(ctx, d.Release, arch)
		if inspectErr != nil {
			return errWrap(CodePrerequisiteMissing, "installed Mac release cannot be inspected", inspectErr)
		}
		if installed {
			return in.Watermark.Advance(ctx, candidate)
		}
	}
	if d.ReleaseSequence == accepted.Sequence {
		if candidate.DeliverySHA256 != accepted.DeliverySHA256 {
			return errf(CodeVerificationFailed, "Mac delivery sequence was already accepted with different signed metadata")
		}
		installed, err := in.Runner.Installed(ctx, d.Release, arch)
		if err != nil {
			return errWrap(CodePrerequisiteMissing, "installed Mac release cannot be inspected", err)
		}
		if installed {
			return nil
		}
		return errf(CodeVerificationFailed, "Mac delivery sequence was already accepted but installation is incomplete")
	}
	dir, err := os.MkdirTemp("", "zatiti-mac-stage-*")
	if err != nil {
		return errWrap(CodePrerequisiteMissing, "private Mac staging is unavailable", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := os.Chmod(dir, 0o700); err != nil {
		return errWrap(CodePrerequisiteMissing, "private Mac staging cannot be secured", err)
	}
	staged := MacStagedAssets{}
	if staged.Controller, err = stageMacAsset(ctx, dir, plan.Controller, in.Source); err != nil {
		return err
	}
	if staged.Desktop, err = stageMacAsset(ctx, dir, plan.Desktop, in.Source); err != nil {
		return err
	}
	if staged.Installer, err = stageMacAsset(ctx, dir, plan.Installer, in.Source); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := in.Verifier.Verify(ctx, d.Release, plan, staged); err != nil {
		return errWrap(CodeVerificationFailed, "Mac installer or component signature verification failed", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := in.Runner.Install(ctx, staged.Installer); err != nil {
		return errWrap(CodePrerequisiteMissing, "Mac installer failed; inspect installation before retry", err)
	}
	installed, err := in.Runner.Installed(ctx, d.Release, arch)
	if err != nil {
		return errWrap(CodePrerequisiteMissing, "installed Mac release cannot be inspected", err)
	}
	if !installed {
		return errf(CodeVerificationFailed, "Mac installer did not produce the signed release")
	}
	if err := in.Watermark.Advance(ctx, candidate); err != nil {
		return errWrap(CodePrerequisiteMissing, "Mac release watermark could not be saved; inspect installation before retry", err)
	}
	return nil
}

func stageMacAsset(ctx context.Context, dir string, a MacDeliveryAsset, source MacAssetSource) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := validateDeliveryURL(a.URL, a.Filename); err != nil {
		return "", err
	}
	reader, err := source.Open(ctx, a)
	if err != nil {
		return "", errWrap(CodePrerequisiteMissing, "Mac asset download failed", err)
	}
	if reader == nil {
		return "", errf(CodePrerequisiteMissing, "Mac asset download returned no bytes")
	}
	defer func() { _ = reader.Close() }()
	path := filepath.Join(dir, a.Filename)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", errWrap(CodePrerequisiteMissing, "Mac asset staging failed", err)
	}
	h := sha256.New()
	n, copyErr := io.CopyN(io.MultiWriter(f, h), reader, a.Size+1)
	if errors.Is(copyErr, io.EOF) {
		copyErr = nil
	}
	if copyErr == nil && n == a.Size {
		copyErr = f.Sync()
	}
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil || n != a.Size || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return "", errf(CodeVerificationFailed, "Mac asset length or digest differs from signed delivery")
	}
	return path, nil
}

// HTTPSMacAssetSource uses platform TLS roots and refuses redirects entirely;
// this is stricter than the release rule's cross-host redirect prohibition.
type HTTPSMacAssetSource struct{ client *http.Client }

func NewHTTPSMacAssetSource() *HTTPSMacAssetSource {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	return &HTTPSMacAssetSource{client: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (s *HTTPSMacAssetSource) Open(ctx context.Context, a MacDeliveryAsset) (io.ReadCloser, error) {
	if s == nil || s.client == nil {
		return nil, errf(CodePrerequisiteMissing, "HTTPS downloader is unavailable")
	}
	if err := validateDeliveryURL(a.URL, a.Filename); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return nil, errf(CodeInvalidInput, "Mac asset URL is invalid")
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, errWrap(CodePrerequisiteMissing, "Mac asset HTTPS request failed", err)
	}
	if resp.StatusCode != http.StatusOK || resp.Request.URL.Host != req.URL.Host || resp.Request.URL.Scheme != "https" || resp.Header.Get("Content-Encoding") != "" {
		_ = resp.Body.Close()
		return nil, errf(CodeVerificationFailed, "Mac asset HTTPS response is unsafe")
	}
	if resp.ContentLength >= 0 && resp.ContentLength != a.Size {
		_ = resp.Body.Close()
		return nil, errf(CodeVerificationFailed, "Mac asset HTTPS length differs from signed delivery")
	}
	return resp.Body, nil
}

// DetectNativeMacArch asks the hardware arm64 capability first, which remains
// true when an Apple Silicon shell is translated through Rosetta.
func DetectNativeMacArch(ctx context.Context) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", errf(CodeCapabilityUnsupported, "native Mac architecture detection requires macOS")
	}
	return detectNativeMacArch(ctx, queryMacSysctl)
}
func detectNativeMacArch(ctx context.Context, query func(context.Context, string) (string, error)) (string, error) {
	arm, err := query(ctx, "hw.optional.arm64")
	if err == nil {
		switch strings.TrimSpace(arm) {
		case "1":
			return "arm64", nil
		case "0": /* consult machine below */
		default:
			return "", errf(CodeVerificationFailed, "Mac hardware architecture response is invalid")
		}
	}
	machine, err := query(ctx, "hw.machine")
	if err != nil {
		return "", errWrap(CodePrerequisiteMissing, "Mac hardware architecture is unavailable", err)
	}
	switch strings.TrimSpace(machine) {
	case "arm64":
		return "arm64", nil
	case "x86_64":
		return "amd64", nil
	default:
		return "", errf(CodeCapabilityUnsupported, "Mac hardware architecture is unsupported")
	}
}
func queryMacSysctl(ctx context.Context, name string) (string, error) {
	child, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(child, "/usr/sbin/sysctl", "-n", name).Output()
	if err != nil {
		return "", err
	}
	if len(out) > 128 {
		return "", errf(CodeVerificationFailed, "Mac hardware architecture response is oversized")
	}
	return string(out), nil
}
