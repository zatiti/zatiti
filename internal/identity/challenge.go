// internal/identity/challenge.go
//
// Proof-of-possession authentication for MCP sessions. The challenger
// (controller) mints a nonce; the client signs it; the identity root
// verifies the signature against the principal's bound public key in
// state and the custodied private key in the keystore. Verification is
// dual: state's public key proves the binding, keystore possession
// proves the client path delivered the real key.

package identity

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

// Challenge is a fresh nonce to be signed by the caller.
type Challenge struct {
	Nonce []byte
}

// NewChallenge mints a 32-byte random nonce.
func NewChallenge() (Challenge, error) {
	n := make([]byte, 32)
	if _, err := rand.Read(n); err != nil {
		return Challenge{}, fmt.Errorf("identity: nonce: %w", err)
	}
	return Challenge{Nonce: n}, nil
}

// Digest returns the SHA-256 of the nonce — the bytes actually signed.
func (c Challenge) Digest() []byte {
	h := sha256.Sum256(c.Nonce)
	return h[:]
}

// SignWithCustodied signs the challenge digest with the private key
// custodied under keystore name "principal-<principalID>". Used by the
// in-process test path and by the controller on behalf of out-of-band
// delivered clients in local scaffold mode.
func (p *Provisioner) SignWithCustodied(ctx context.Context, principalID string, c Challenge) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	raw, err := p.keystore.loadKey("principal-" + principalID)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("identity: custodied key is not PEM")
	}
	priv, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("identity: parse custodied key: %w", err)
	}
	sig, err := ecdsa.SignASN1(rand.Reader, priv, c.Digest())
	if err != nil {
		return nil, fmt.Errorf("identity: sign: %w", err)
	}
	return sig, nil
}

// VerifySignature checks sig over the challenge digest against a
// PKIX-encoded public key. Pure function; no I/O.
func VerifySignature(pubPEMPKIX, nonce, sig []byte) error {
	pub, err := x509.ParsePKIXPublicKey(pubPEMPKIX)
	if err != nil {
		return fmt.Errorf("identity: parse public key: %w", err)
	}
	ecPub, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return fmt.Errorf("identity: unexpected public key type %T", pub)
	}
	h := sha256.Sum256(nonce)
	if !ecdsa.VerifyASN1(ecPub, h[:], sig) {
		return fmt.Errorf("identity: signature verification failed")
	}
	return nil
}
