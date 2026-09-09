// internal/identity/identity.go
//
// Identity root ownership: keypair generation, keystore lifecycle,
// public-key wire encoding. Credential material is confined to this
// package; state persists only PrincipalRef via the port interface.

package identity

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"zatiti/internal/state"
)

// Provisioner provisions owner principals and satisfies
// state.PrincipalProvisioner. The adapter declaration lives in wire.go;
// the method signature here matches the port exactly.
// PrincipalRef aliases the state-owned port type so *Provisioner
// satisfies state.PrincipalProvisioner (see internal/app/wire.go).
type PrincipalRef = state.PrincipalRef

type Provisioner struct {
	keystore *Keystore
}

// NewProvisioner builds a Provisioner rooted at keystoreDir.
func NewProvisioner(keystoreDir string) (*Provisioner, error) {
	ks, err := OpenKeystore(keystoreDir)
	if err != nil {
		return nil, err
	}
	return &Provisioner{keystore: ks}, nil
}

// ErrKeyCorrupt is returned when keystore contents fail to parse.
// Recovery is operator action (keystore is not self-repairing).
var ErrKeyCorrupt = errors.New("identity: keystore content is corrupt")

// parseKey validates a stored PEM block back into a private key.
func parseKey(raw []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "EC PRIVATE KEY" {
		return nil, ErrKeyCorrupt
	}
	priv, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyCorrupt, err)
	}
	return priv, nil
}

// newUUID is local to identity to avoid importing state (which would
// invert the dependency direction: state depends on identity's port,
// identity must not import state).
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("identity: entropy unavailable: %v", err))
	}
	const hexd = "0123456789abcdef"
	out := make([]byte, 36)
	for i, v := range b {
		switch i {
		case 6:
			v = (v & 0x0f) | 0x40
		case 8:
			v = (v & 0x3f) | 0x80
		}
		out[i*2] = hexd[v>>4]
		out[i*2+1] = hexd[v&0x0f]
		switch i {
		case 3, 5, 7, 9:
			out[i*2+2] = '-'
		}
	}
	return string(out)
}
