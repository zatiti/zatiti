// internal/identity/keyutil.go
//
// Key generation, encoding, and public-key marshaling helpers.
// Confined to identity: these never serialize private material outside
// the keystore path.

package identity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

// generateP256 creates a new P-256 keypair.
func generateP256() (*ecdsa.PrivateKey, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("identity: generate key: %w", err)
	}
	return priv, nil
}

// encodePEM serializes a private for keystore storage.
func encodePEM(priv *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("identity: marshal key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}

// marshalPublic returns the PKIX wire encoding of a public key.
func marshalPublic(priv *ecdsa.PrivateKey) ([]byte, error) {
	pub, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("identity: marshal pubkey: %w", err)
	}
	return pub, nil
}
