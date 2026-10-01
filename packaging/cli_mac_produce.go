package packaging

// Mac release producer commands. They chain the existing library steps:
// four signed component parts become one signed release descriptor
// (mac-release); each architecture's controller and desktop archives
// become an installer package bound to that descriptor (mac-pkg); the six
// assets become one signed delivery index (mac-delivery); and "apple"
// runs Apple signing, notarization and stapling, failing closed with a
// "not configured" error when its identity or profile is absent.
//
// The Ed25519 keys these commands take are release-metadata keys supplied by
// the caller. This driver mints no release identity (see cliKeygen), and
// nothing here publishes: the outputs are local files for the release owner.

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Output file names written by the producer commands.
const (
	MacReleaseFileName          = "release.json"
	MacReleaseSignatureFileName = "release.sig.json"
	MacDeliveryFileName         = "delivery.json"
	MacDeliverySigFileName      = "delivery.sig.json"
)

func loadTrustedKeys(paths []string) ([]ed25519.PublicKey, error) {
	if len(paths) == 0 {
		return nil, errf(CodeInvalidInput, "at least one --trusted public key is required")
	}
	keys := make([]ed25519.PublicKey, 0, len(paths))
	for _, p := range paths {
		k, err := loadEd25519PublicKey(p)
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, nil
}

func readMacPart(dir string) (MacReleasePart, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return MacReleasePart{}, errWrap(CodeInvalidInput, "a --part directory could not be resolved", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, ManifestFileName))
	if err != nil {
		return MacReleasePart{}, errWrap(CodeNotFound, "a --part directory has no manifest", err)
	}
	sigRaw, err := os.ReadFile(filepath.Join(root, SignatureFileName))
	if err != nil {
		return MacReleasePart{}, errWrap(CodeNotFound, "a --part directory has no manifest signature", err)
	}
	sig, err := DecodeSignature(sigRaw)
	if err != nil {
		return MacReleasePart{}, err
	}
	return MacReleasePart{Root: root, Manifest: raw, Signature: sig}, nil
}

func writeOwnerFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return errWrap(CodeInternalError, "an output file could not be written", err)
	}
	return nil
}

// cliMacRelease verifies four signed component parts and writes the signed
// Mac release descriptor.
func cliMacRelease(args []string, stdout io.Writer) error {
	fs := newFlagSet("mac-release")
	var parts, trusted stringListFlag
	fs.Var(&parts, "part", "a signed component directory with manifest.json and manifest.sig.json (four required)")
	fs.Var(&trusted, "trusted", "a PEM PKIX Ed25519 public key trusted for component signatures (repeatable)")
	keyPath := fs.String("key", "", "the PEM PKCS8 Ed25519 key that signs the release descriptor (required)")
	outDir := fs.String("out-dir", "", "the directory for release.json and release.sig.json (required)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(parts) != 4 || *keyPath == "" || *outDir == "" {
		return errf(CodeInvalidInput, "mac-release: four --part, --key and --out-dir are required")
	}
	keys, err := loadTrustedKeys(trusted)
	if err != nil {
		return err
	}
	var in []MacReleasePart
	for _, p := range parts {
		part, err := readMacPart(p)
		if err != nil {
			return err
		}
		in = append(in, part)
	}
	release, err := AssembleMacRelease(in, keys)
	if err != nil {
		return err
	}
	raw, err := EncodeMacRelease(release)
	if err != nil {
		return err
	}
	signer, err := loadEd25519PrivateKey(*keyPath)
	if err != nil {
		return err
	}
	sig, err := SignMacRelease(raw, signer)
	if err != nil {
		return err
	}
	sigRaw, err := EncodeMacReleaseSignature(sig)
	if err != nil {
		return err
	}
	if err := writeOwnerFile(filepath.Join(*outDir, MacReleaseFileName), raw); err != nil {
		return err
	}
	if err := writeOwnerFile(filepath.Join(*outDir, MacReleaseSignatureFileName), sigRaw); err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	return writeJSONDoc(stdout, map[string]any{"version": release.Version, "release_sha256": hex.EncodeToString(sum[:]), "key_id": sig.KeyID})
}

func measureFile(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", errWrap(CodeNotFound, "an input archive could not be opened", err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, maxMacAssetBytes+1))
	if err != nil {
		return 0, "", errWrap(CodeInternalError, "an input archive could not be read", err)
	}
	if n <= 0 || n > maxMacAssetBytes {
		return 0, "", errf(CodeInvalidInput, "an input archive is empty or exceeds release bounds")
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// macAssetFor names and measures one delivery asset.
func macAssetFor(version, arch, role, path string) (MacDeliveryAsset, error) {
	ext := "tar.gz"
	if role == "installer" {
		ext = "pkg"
	}
	size, sum, err := measureFile(path)
	if err != nil {
		return MacDeliveryAsset{}, err
	}
	return MacDeliveryAsset{Arch: arch, Distribution: role, Filename: "zatiti-" + version + "-darwin-" + arch + "-" + role + "." + ext, Size: size, SHA256: sum}, nil
}

// cliMacPkg builds one architecture's unsigned installer package from its
// controller and desktop delivery archives and prints the three measured
// assets. Signing the package is a separate "apple --step productsign".
func cliMacPkg(args []string, stdout io.Writer) error {
	fs := newFlagSet("mac-pkg")
	releasePath := fs.String("release", "", "the release.json descriptor (required)")
	arch := fs.String("arch", "", "amd64 or arm64 (required)")
	sequence := fs.Int64("sequence", 0, "the positive release sequence (required)")
	controller := fs.String("controller", "", "this architecture's owner-only (0600) controller delivery archive (required)")
	desktop := fs.String("desktop", "", "this architecture's owner-only (0600) desktop delivery archive (required)")
	outDir := fs.String("out-dir", "", "the directory for the installer package (required)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *releasePath == "" || (*arch != "amd64" && *arch != "arm64") || *sequence <= 0 || *controller == "" || *desktop == "" || *outDir == "" {
		return errf(CodeInvalidInput, "mac-pkg: --release, --arch (amd64|arm64), a positive --sequence, --controller, --desktop and --out-dir are required")
	}
	raw, err := os.ReadFile(*releasePath)
	if err != nil {
		return errWrap(CodeNotFound, "the release descriptor could not be read", err)
	}
	release, err := DecodeMacRelease(raw)
	if err != nil {
		return err
	}
	absController, err := filepath.Abs(*controller)
	if err != nil {
		return errWrap(CodeInvalidInput, "the controller archive path could not be resolved", err)
	}
	absDesktop, err := filepath.Abs(*desktop)
	if err != nil {
		return errWrap(CodeInvalidInput, "the desktop archive path could not be resolved", err)
	}
	absOut, err := filepath.Abs(*outDir)
	if err != nil {
		return errWrap(CodeInvalidInput, "the output directory could not be resolved", err)
	}
	c, err := macAssetFor(release.Version, *arch, DistributionController, absController)
	if err != nil {
		return err
	}
	d, err := macAssetFor(release.Version, *arch, DistributionDesktop, absDesktop)
	if err != nil {
		return err
	}
	installer := MacDeliveryAsset{Arch: *arch, Distribution: "installer", Filename: "zatiti-" + release.Version + "-darwin-" + *arch + "-installer.pkg"}
	plan := MacDownloadPlan{Arch: *arch, ReleaseSequence: *sequence, Controller: c, Desktop: d, Installer: installer}
	output := filepath.Join(absOut, installer.Filename)
	size, sum, err := BuildUnsignedMacPkgCandidate(context.Background(), release, plan, absController, absDesktop, output)
	if err != nil {
		return err
	}
	installer.Size, installer.SHA256 = size, sum
	return writeJSONDoc(stdout, map[string]any{"assets": []MacDeliveryAsset{c, d, installer}, "installer": output})
}

// cliMacDelivery writes the signed six-asset delivery index. Each asset's
// size and digest are measured from the final file, so the index can only
// describe bytes that exist.
func cliMacDelivery(args []string, stdout io.Writer) error {
	fs := newFlagSet("mac-delivery")
	var assetFlags stringListFlag
	releasePath := fs.String("release", "", "the release.json descriptor (required)")
	releaseSigPath := fs.String("release-sig", "", "the release.sig.json signature (required)")
	fs.Var(&assetFlags, "asset", "arch/role=path for each of the six final assets, role controller|desktop|installer (six required)")
	baseURL := fs.String("base-url", "", "the https URL directory the assets will be served from (required)")
	sequence := fs.Int64("sequence", 0, "the positive release sequence (required)")
	published := fs.String("published-at", "", "UTC RFC 3339 publication time (default: now)")
	validity := fs.Duration("validity", 14*24*time.Hour, "how long the index stays valid, at most 30 days")
	keyPath := fs.String("key", "", "the PEM PKCS8 Ed25519 key that signs the delivery index (required)")
	outDir := fs.String("out-dir", "", "the directory for delivery.json and delivery.sig.json (required)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *releasePath == "" || *releaseSigPath == "" || len(assetFlags) != 6 || *baseURL == "" || *sequence <= 0 || *keyPath == "" || *outDir == "" {
		return errf(CodeInvalidInput, "mac-delivery: --release, --release-sig, six --asset, --base-url, a positive --sequence, --key and --out-dir are required")
	}
	raw, err := os.ReadFile(*releasePath)
	if err != nil {
		return errWrap(CodeNotFound, "the release descriptor could not be read", err)
	}
	release, err := DecodeMacRelease(raw)
	if err != nil {
		return err
	}
	sigRaw, err := os.ReadFile(*releaseSigPath)
	if err != nil {
		return errWrap(CodeNotFound, "the release signature could not be read", err)
	}
	releaseSig, err := DecodeMacReleaseSignature(sigRaw)
	if err != nil {
		return err
	}
	pubAt := time.Now().UTC().Truncate(time.Second)
	if *published != "" {
		if pubAt, err = parseDeliveryTime(*published); err != nil {
			return err
		}
	}
	byKey := map[string]string{}
	for _, a := range assetFlags {
		k, v, ok := strings.Cut(a, "=")
		if !ok || v == "" || byKey[k] != "" {
			return errf(CodeInvalidInput, "mac-delivery: each --asset is a distinct arch/role=path")
		}
		byKey[k] = v
	}
	base := strings.TrimSuffix(*baseURL, "/")
	d := MacDelivery{Schema: MacDeliverySchema, Release: release, ReleaseSignature: releaseSig, Channel: "stable", ReleaseSequence: *sequence,
		PublishedAt: pubAt.Format(time.RFC3339Nano), ExpiresAt: pubAt.Add(*validity).Format(time.RFC3339Nano)}
	for _, arch := range []string{"amd64", "arm64"} {
		for _, role := range []string{DistributionController, DistributionDesktop, "installer"} {
			path, ok := byKey[arch+"/"+role]
			if !ok {
				return errf(CodeInvalidInput, "mac-delivery: missing --asset %s/%s", arch, role)
			}
			asset, err := macAssetFor(release.Version, arch, role, path)
			if err != nil {
				return err
			}
			asset.URL = base + "/" + asset.Filename
			d.Assets = append(d.Assets, asset)
		}
	}
	deliveryRaw, err := EncodeMacDelivery(d)
	if err != nil {
		return err
	}
	signer, err := loadEd25519PrivateKey(*keyPath)
	if err != nil {
		return err
	}
	sig, err := SignMacDelivery(deliveryRaw, signer)
	if err != nil {
		return err
	}
	deliverySigRaw, err := EncodeMacDeliverySignature(sig)
	if err != nil {
		return err
	}
	if err := writeOwnerFile(filepath.Join(*outDir, MacDeliveryFileName), deliveryRaw); err != nil {
		return err
	}
	if err := writeOwnerFile(filepath.Join(*outDir, MacDeliverySigFileName), deliverySigRaw); err != nil {
		return err
	}
	sum := sha256.Sum256(deliveryRaw)
	return writeJSONDoc(stdout, map[string]any{"delivery_sha256": hex.EncodeToString(sum[:]), "release_sequence": d.ReleaseSequence, "expires_at": d.ExpiresAt, "assets": d.Assets})
}

// appleTools is the runner "apple" uses; tests replace it.
var appleTools AppleToolRunner = ExecAppleTools{}

// cliApple runs one Apple signing step, configured only from the
// ZATITI_APPLE_* environment. "status" reports configuration and, with
// --require, fails unless every step is configured.
func cliApple(args []string, stdout io.Writer) error {
	fs := newFlagSet("apple")
	step := fs.String("step", "", "status, codesign, productsign, notarize or staple (required)")
	require := fs.Bool("require", false, "status: fail unless every step is configured")
	entitlements := fs.String("entitlements", "", "codesign: an optional entitlements plist")
	in := fs.String("in", "", "productsign: the unsigned package")
	out := fs.String("out", "", "productsign: the signed package to write")
	timeout := fs.Duration("timeout", time.Hour, "notarize: how long to wait for Apple's verdict")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	cfg := AppleSigningFromEnv(os.Getenv)
	ctx := context.Background()
	paths := fs.Args()
	switch *step {
	case "status":
		status := cfg.Status()
		if err := writeJSONDoc(stdout, map[string]any{"steps": status}); err != nil {
			return err
		}
		if *require {
			if m := cfg.sortedMissing(); len(m) != 0 {
				return errf(CodePrerequisiteMissing, "Apple signing is not configured: set %s", strings.Join(m, ", "))
			}
		}
		return nil
	case AppleStepCodesign:
		abs, err := absPaths(paths)
		if err != nil {
			return err
		}
		if err := AppleCodesign(ctx, appleTools, cfg, *entitlements, abs...); err != nil {
			return err
		}
		return writeJSONDoc(stdout, map[string]any{"step": *step, "signed": abs})
	case AppleStepProductsign:
		abs, err := absPaths([]string{*in, *out})
		if err != nil {
			return err
		}
		if err := AppleProductsign(ctx, appleTools, cfg, abs[0], abs[1]); err != nil {
			return err
		}
		size, sum, err := measureFile(abs[1])
		if err != nil {
			return err
		}
		return writeJSONDoc(stdout, map[string]any{"step": *step, "package": abs[1], "size": size, "sha256": sum})
	case AppleStepNotarize, AppleStepStaple:
		abs, err := absPaths(paths)
		if err != nil {
			return err
		}
		if len(abs) != 1 {
			return errf(CodeInvalidInput, "apple --step %s takes exactly one path", *step)
		}
		if *step == AppleStepNotarize {
			id, err := AppleNotarize(ctx, appleTools, cfg, abs[0], *timeout)
			if err != nil {
				return err
			}
			return writeJSONDoc(stdout, map[string]any{"step": *step, "path": abs[0], "submission_id": id, "status": "Accepted"})
		}
		if err := AppleStaple(ctx, appleTools, cfg, abs[0]); err != nil {
			return err
		}
		size, sum, err := measureFile(abs[0])
		if err != nil {
			return err
		}
		return writeJSONDoc(stdout, map[string]any{"step": *step, "path": abs[0], "size": size, "sha256": sum})
	default:
		return errf(CodeInvalidInput, "apple: --step must be status, codesign, productsign, notarize or staple")
	}
}

func absPaths(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, errf(CodeInvalidInput, "apple: a path is required")
	}
	out := make([]string, 0, len(in))
	for _, p := range in {
		if p == "" {
			return nil, errf(CodeInvalidInput, "apple: a required path is empty")
		}
		a, err := filepath.Abs(p)
		if err != nil {
			return nil, errWrap(CodeInvalidInput, "a path could not be resolved", err)
		}
		out = append(out, a)
	}
	return out, nil
}
