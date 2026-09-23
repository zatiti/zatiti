package packaging

// Matched Mac release metadata binds the four separately signed controller
// and desktop distributions required for Intel and Apple Silicon. It is a
// description of staged artifacts, not evidence that any real build exists.

import (
	"archive/tar"
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"path/filepath"
	"reflect"
	"sort"
)

const MacReleaseSchema = "zatiti.mac_release/v1"
const MacReleaseSignatureSchema = "zatiti.mac_release_signature/v1"

type MacReleasePart struct {
	Root      string
	Manifest  []byte
	Signature Signature
}

type MacReleaseComponent struct {
	Arch           string    `json:"arch"`
	Distribution   string    `json:"distribution"`
	ManifestSHA256 string    `json:"manifest_sha256"`
	Signature      Signature `json:"signature"`
}

type MacReleaseDescriptor struct {
	Schema           string                `json:"schema"`
	Version          string                `json:"version"`
	SourceRevision   string                `json:"source_revision"`
	ContractRevision int64                 `json:"contract_revision"`
	Protocols        Protocols             `json:"protocols"`
	Components       []MacReleaseComponent `json:"components"`
}

type MacReleaseSignature struct {
	Schema    string `json:"schema"`
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"key_id"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
}

func EncodeMacReleaseSignature(sig MacReleaseSignature) ([]byte, error) {
	return canonicalJSON(sig)
}

func DecodeMacReleaseSignature(raw []byte) (MacReleaseSignature, error) {
	var sig MacReleaseSignature
	if err := strictDecode(raw, &sig); err != nil {
		return MacReleaseSignature{}, err
	}
	return sig, nil
}

func macPartKey(m Manifest) string { return m.Target.Arch + "/" + m.Distribution }

// AssembleMacRelease verifies every supplied signature and complete tree
// before describing it. The trust set is supplied by the caller.
func AssembleMacRelease(parts []MacReleasePart, trusted []ed25519.PublicKey) (MacReleaseDescriptor, error) {
	if len(parts) != 4 {
		return MacReleaseDescriptor{}, errf(CodeInvalidInput, "a Mac release needs four controller and desktop components")
	}
	want := map[string]bool{"amd64/controller": true, "amd64/desktop": true, "arm64/controller": true, "arm64/desktop": true}
	var descriptor MacReleaseDescriptor
	descriptor.Schema = MacReleaseSchema
	var first Manifest
	var controller, desktop map[string]Manifest = map[string]Manifest{}, map[string]Manifest{}
	for i, part := range parts {
		if err := VerifySignature(part.Manifest, part.Signature, trusted); err != nil {
			return MacReleaseDescriptor{}, err
		}
		m, err := Decode(part.Manifest)
		if err != nil {
			return MacReleaseDescriptor{}, err
		}
		key := macPartKey(m)
		if m.Target.OS != "darwin" || !want[key] {
			return MacReleaseDescriptor{}, errf(CodeCapabilityUnsupported, "the Mac release has an unsupported or duplicate component")
		}
		delete(want, key)
		if err := VerifyTree(m, part.Root); err != nil {
			return MacReleaseDescriptor{}, err
		}
		if m.Distribution == DistributionController {
			for _, a := range m.Artifacts {
				if a.Kind == KindControllerBinary {
					if a.Size > maxMacExecutableBytes {
						return MacReleaseDescriptor{}, errf(CodeVerificationFailed, "Mac controller executable exceeds the inspection limit")
					}
					if err := verifyThinMachO(filepath.Join(part.Root, filepath.FromSlash(a.Path)), m.Target.Arch); err != nil {
						return MacReleaseDescriptor{}, err
					}
				}
			}
		}
		if m.Distribution == DistributionDesktop {
			archive := filepath.Join(part.Root, filepath.FromSlash(m.Desktop.Bundle))
			found := false
			gotDigest, gotSize, err := readBundle(archive, func(e bundleEntry, r io.Reader) error {
				if e.path == m.Desktop.Executable && e.kind == tar.TypeReg && e.mode&0o100 != 0 {
					if err := verifyArchivedMachO(r, e.size, m.Target.Arch); err != nil {
						return err
					}
					found = true
					return nil
				}
				_, err := io.Copy(io.Discard, r)
				return err
			})
			if err != nil {
				return MacReleaseDescriptor{}, err
			}
			var bundle Artifact
			for _, a := range m.Artifacts {
				if a.Path == m.Desktop.Bundle {
					bundle = a
					break
				}
			}
			if gotDigest != bundle.SHA256 || gotSize != bundle.Size {
				return MacReleaseDescriptor{}, errf(CodeVerificationFailed, "Mac desktop bundle changed during verification")
			}
			if !found {
				return MacReleaseDescriptor{}, errf(CodeVerificationFailed, "Mac desktop bundle lacks its declared executable")
			}
		}
		if i == 0 {
			first = m
			descriptor.Version, descriptor.SourceRevision = m.Version, m.SourceRevision
			descriptor.ContractRevision, descriptor.Protocols = m.ContractRevision, m.Protocols
		} else if m.Version != first.Version || m.SourceRevision != first.SourceRevision || m.ContractRevision != first.ContractRevision || m.Protocols != first.Protocols || m.Toolchain != first.Toolchain {
			return MacReleaseDescriptor{}, errf(CodeConflict, "Mac release components have incompatible versions or protocols")
		}
		if m.Distribution == DistributionController {
			controller[m.Target.Arch] = m
		} else {
			desktop[m.Target.Arch] = m
		}
		sum := sha256.Sum256(part.Manifest)
		descriptor.Components = append(descriptor.Components, MacReleaseComponent{Arch: m.Target.Arch, Distribution: m.Distribution, ManifestSHA256: hex.EncodeToString(sum[:]), Signature: part.Signature})
	}
	if len(want) != 0 {
		return MacReleaseDescriptor{}, errf(CodeInvalidInput, "Mac release is missing a required component")
	}
	if controller["arm64"].Serenity != controller["amd64"].Serenity || controller["arm64"].SecureHelper != controller["amd64"].SecureHelper || !reflect.DeepEqual(controller["arm64"].Profiles, controller["amd64"].Profiles) || !reflect.DeepEqual(controller["arm64"].Licenses, controller["amd64"].Licenses) {
		return MacReleaseDescriptor{}, errf(CodeConflict, "Mac controller capabilities or notices differ across architectures")
	}
	a, b := desktop["arm64"].Desktop, desktop["amd64"].Desktop
	if a.FlutterVersion != b.FlutterVersion || a.DartVersion != b.DartVersion || !reflect.DeepEqual(a.Plugins, b.Plugins) || !reflect.DeepEqual(a.NativeDependencies, b.NativeDependencies) || !reflect.DeepEqual(desktop["arm64"].Profiles, desktop["amd64"].Profiles) || !reflect.DeepEqual(desktop["arm64"].Licenses, desktop["amd64"].Licenses) {
		return MacReleaseDescriptor{}, errf(CodeConflict, "Mac desktop dependencies or notices differ across architectures")
	}
	sort.Slice(descriptor.Components, func(i, j int) bool {
		a, b := descriptor.Components[i], descriptor.Components[j]
		if a.Arch != b.Arch {
			return a.Arch < b.Arch
		}
		return a.Distribution < b.Distribution
	})
	return descriptor, nil
}

func EncodeMacRelease(d MacReleaseDescriptor) ([]byte, error) {
	if err := validateMacRelease(d); err != nil {
		return nil, err
	}
	return canonicalJSON(d)
}

func DecodeMacRelease(raw []byte) (MacReleaseDescriptor, error) {
	var d MacReleaseDescriptor
	if err := strictDecode(raw, &d); err != nil {
		return MacReleaseDescriptor{}, err
	}
	if err := validateMacRelease(d); err != nil {
		return MacReleaseDescriptor{}, err
	}
	return d, nil
}

func validateMacRelease(d MacReleaseDescriptor) error {
	if d.Schema != MacReleaseSchema {
		return errf(CodeCapabilityUnsupported, "Mac release descriptor version is unsupported")
	}
	if !versionPattern.MatchString(d.Version) || !revisionPattern.MatchString(d.SourceRevision) || d.ContractRevision != ContractRevision || d.Protocols != (Protocols{OperationAPI: OperationAPI, RequestSchema: RequestSchema, ResultSchema: ResultSchema, MCP: MCPProtocolVersion}) || len(d.Components) != 4 {
		return errf(CodeInvalidInput, "Mac release descriptor fields are invalid")
	}
	want := []string{"amd64/controller", "amd64/desktop", "arm64/controller", "arm64/desktop"}
	for i, c := range d.Components {
		if c.Arch+"/"+c.Distribution != want[i] || !digestPattern.MatchString(c.ManifestSHA256) || c.Signature.ManifestSHA256 != c.ManifestSHA256 {
			return errf(CodeInvalidInput, "Mac release component list is invalid")
		}
	}
	return nil
}

// VerifyMacRelease recomputes the descriptor from the four signed trees.
func VerifyMacRelease(d MacReleaseDescriptor, parts []MacReleasePart, trusted []ed25519.PublicKey) error {
	want, err := EncodeMacRelease(d)
	if err != nil {
		return err
	}
	got, err := AssembleMacRelease(parts, trusted)
	if err != nil {
		return err
	}
	actual, err := EncodeMacRelease(got)
	if err != nil {
		return err
	}
	if !bytes.Equal(want, actual) {
		return errf(CodeVerificationFailed, "Mac release descriptor does not match its signed components")
	}
	return nil
}

func macReleaseMessage(digest string) []byte {
	return []byte(MacReleaseSignatureSchema + "\n" + digest + "\n")
}

func SignMacRelease(raw []byte, signer crypto.Signer) (MacReleaseSignature, error) {
	if _, err := DecodeMacRelease(raw); err != nil {
		return MacReleaseSignature{}, err
	}
	if signer == nil {
		return MacReleaseSignature{}, errf(CodePrerequisiteMissing, "no Mac release signer is configured")
	}
	pub, ok := signer.Public().(ed25519.PublicKey)
	if !ok || len(pub) != ed25519.PublicKeySize {
		return MacReleaseSignature{}, errf(CodeCapabilityUnsupported, "Mac release signer must hold an Ed25519 key")
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	sig, err := signer.Sign(rand.Reader, macReleaseMessage(digest), crypto.Hash(0))
	if err != nil {
		return MacReleaseSignature{}, errWrap(CodeInternalError, "Mac release signer refused to sign", err)
	}
	if !ed25519.Verify(pub, macReleaseMessage(digest), sig) {
		return MacReleaseSignature{}, errf(CodeInternalError, "Mac release signer produced an invalid signature")
	}
	return MacReleaseSignature{Schema: MacReleaseSignatureSchema, Algorithm: signatureAlgorithm, KeyID: KeyID(pub), SHA256: digest, Signature: base64.StdEncoding.EncodeToString(sig)}, nil
}

func VerifyMacReleaseSignature(raw []byte, sig MacReleaseSignature, trusted []ed25519.PublicKey) error {
	if _, err := DecodeMacRelease(raw); err != nil {
		return err
	}
	if len(trusted) == 0 {
		return errf(CodePrerequisiteMissing, "no trusted Mac release key is configured")
	}
	if sig.Schema != MacReleaseSignatureSchema || sig.Algorithm != signatureAlgorithm {
		return errf(CodeCapabilityUnsupported, "Mac release signature format is unsupported")
	}
	sum := sha256.Sum256(raw)
	if sig.SHA256 != hex.EncodeToString(sum[:]) {
		return errf(CodeVerificationFailed, "Mac release signature covers different metadata")
	}
	bytes, err := base64.StdEncoding.DecodeString(sig.Signature)
	if err != nil || len(bytes) != ed25519.SignatureSize {
		return errf(CodeInvalidInput, "Mac release signature bytes are invalid")
	}
	for _, pub := range trusted {
		if len(pub) == ed25519.PublicKeySize && KeyID(pub) == sig.KeyID && ed25519.Verify(pub, macReleaseMessage(sig.SHA256), bytes) {
			return nil
		}
	}
	return errf(CodeVerificationFailed, "Mac release metadata is not signed by a trusted key")
}
