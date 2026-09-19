package packaging

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"testing"
)

// testKey generates a throwaway key. No key is ever stored in the repository.
func testKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func TestSignatureRoundTrip(t *testing.T) {
	t.Parallel()
	manifest, err := Encode(newFixture(t, "1.2.3", "linux").build(t))
	if err != nil {
		t.Fatal(err)
	}
	pub, priv := testKey(t)
	otherPub, _ := testKey(t)

	sig, err := Sign(manifest, priv)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	encoded, err := EncodeSignature(sig)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeSignature(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySignature(manifest, decoded, []ed25519.PublicKey{otherPub, pub}); err != nil {
		t.Fatalf("VerifySignature: %v", err)
	}

	tampered := bytes.Replace(manifest, []byte(`"1.2.3"`), []byte(`"1.2.4"`), 1)
	wantCode(t, VerifySignature(tampered, sig, []ed25519.PublicKey{pub}), CodeVerificationFailed)
	wantCode(t, VerifySignature(manifest, sig, []ed25519.PublicKey{otherPub}), CodeVerificationFailed)
	wantCode(t, VerifySignature(manifest, sig, nil), CodePrerequisiteMissing)

	forged := sig
	forged.KeyID = KeyID(otherPub)
	wantCode(t, VerifySignature(manifest, forged, []ed25519.PublicKey{otherPub}), CodeVerificationFailed)

	// A raw signature over the digest alone must not pass: the signed
	// message is bound to the signature schema.
	raw := sig
	raw.Signature = encodeRawSignature(priv, sig.ManifestSHA256)
	wantCode(t, VerifySignature(manifest, raw, []ed25519.PublicKey{pub}), CodeVerificationFailed)

	bad := sig
	bad.Algorithm = "rsa"
	wantCode(t, VerifySignature(manifest, bad, []ed25519.PublicKey{pub}), CodeCapabilityUnsupported)
	bad = sig
	bad.Signature = "not base64"
	wantCode(t, VerifySignature(manifest, bad, []ed25519.PublicKey{pub}), CodeInvalidInput)
	bad = sig
	bad.Schema = "other/v1"
	wantCode(t, VerifySignature(manifest, bad, []ed25519.PublicKey{pub}), CodeInvalidInput)
}

func TestSignRefusals(t *testing.T) {
	t.Parallel()
	manifest, err := Encode(newFixture(t, "1.2.3", "linux").build(t))
	if err != nil {
		t.Fatal(err)
	}
	_, priv := testKey(t)

	_, err = Sign([]byte(`{"schema":"nope"}`), priv)
	wantCode(t, err, CodeInvalidInput)

	_, err = Sign(manifest, nil)
	wantCode(t, err, CodePrerequisiteMissing)

	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Sign(manifest, ec)
	wantCode(t, err, CodeCapabilityUnsupported)

	_, err = DecodeSignature([]byte(`{"schema":"x","extra":1}`))
	wantCode(t, err, CodeInvalidInput)
}
