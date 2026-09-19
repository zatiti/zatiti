package packaging

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// SignatureSchema identifies the detached manifest signature document.
const SignatureSchema = "zatiti.release_signature/v1"

const signatureAlgorithm = "ed25519"

// Signature is a detached Ed25519 signature over the exact bytes of a
// manifest file. This package holds no key: the release workflow owner
// supplies the signer, and an installer supplies the keys it trusts.
type Signature struct {
	Schema         string `json:"schema"`
	Algorithm      string `json:"algorithm"`
	KeyID          string `json:"key_id"`
	ManifestSHA256 string `json:"manifest_sha256"`
	Signature      string `json:"signature"`
}

// KeyID is the lowercase hexadecimal SHA-256 of the raw public key.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])
}

// signedMessage binds the signature to this schema so that a signature over
// a manifest digest cannot be replayed as a signature over anything else.
func signedMessage(manifestSHA256 string) []byte {
	return []byte(SignatureSchema + "\n" + manifestSHA256 + "\n")
}

// Sign signs manifestBytes, which must decode as a valid manifest. The signer
// may be backed by hardware; only its Ed25519 public half is read here.
func Sign(manifestBytes []byte, signer crypto.Signer) (Signature, error) {
	if _, err := Decode(manifestBytes); err != nil {
		return Signature{}, err
	}
	if signer == nil {
		return Signature{}, errf(CodePrerequisiteMissing, "no release signer is configured")
	}
	pub, ok := signer.Public().(ed25519.PublicKey)
	if !ok || len(pub) != ed25519.PublicKeySize {
		return Signature{}, errf(CodeCapabilityUnsupported, "the release signer must hold an Ed25519 key")
	}
	sum := sha256.Sum256(manifestBytes)
	digest := hex.EncodeToString(sum[:])
	raw, err := signer.Sign(rand.Reader, signedMessage(digest), crypto.Hash(0))
	if err != nil {
		return Signature{}, errWrap(CodeInternalError, "the release signer refused to sign", err)
	}
	if !ed25519.Verify(pub, signedMessage(digest), raw) {
		return Signature{}, errf(CodeInternalError, "the release signer produced a signature that does not verify")
	}
	return Signature{
		Schema:         SignatureSchema,
		Algorithm:      signatureAlgorithm,
		KeyID:          KeyID(pub),
		ManifestSHA256: digest,
		Signature:      base64.StdEncoding.EncodeToString(raw),
	}, nil
}

// VerifySignature checks sig over manifestBytes against the trusted keys. An
// empty trust set is a missing prerequisite, never an implicit pass.
func VerifySignature(manifestBytes []byte, sig Signature, trusted []ed25519.PublicKey) error {
	if len(trusted) == 0 {
		return errf(CodePrerequisiteMissing, "no trusted release key is configured")
	}
	if sig.Schema != SignatureSchema {
		return errf(CodeInvalidInput, "signature schema must be %s", SignatureSchema)
	}
	if sig.Algorithm != signatureAlgorithm {
		return errf(CodeCapabilityUnsupported, "signature algorithm %s is not supported", printable(sig.Algorithm))
	}
	sum := sha256.Sum256(manifestBytes)
	if sig.ManifestSHA256 != hex.EncodeToString(sum[:]) {
		return errf(CodeVerificationFailed, "the signature covers a different manifest")
	}
	raw, err := base64.StdEncoding.DecodeString(sig.Signature)
	if err != nil || len(raw) != ed25519.SignatureSize {
		return errf(CodeInvalidInput, "the signature is not a base64 Ed25519 signature")
	}
	for _, pub := range trusted {
		if len(pub) != ed25519.PublicKeySize || KeyID(pub) != sig.KeyID {
			continue
		}
		if ed25519.Verify(pub, signedMessage(sig.ManifestSHA256), raw) {
			return nil
		}
		return errf(CodeVerificationFailed, "the manifest signature does not verify")
	}
	return errf(CodeVerificationFailed, "the manifest is signed by a key that is not trusted")
}

// EncodeSignature returns the canonical JSON of sig.
func EncodeSignature(sig Signature) ([]byte, error) { return canonicalJSON(sig) }

// DecodeSignature strictly parses a signature document.
func DecodeSignature(data []byte) (Signature, error) {
	var sig Signature
	if err := strictDecode(data, &sig); err != nil {
		return Signature{}, err
	}
	return sig, nil
}
