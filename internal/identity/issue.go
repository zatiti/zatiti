// internal/identity/issue.go
//
// Credential issuance (T3.x). The owner (authenticated by keystore
// possession) generates a P-256 keypair for a pending principal. The
// private half is custodied in the keystore at 0600 pending out-of-band
// delivery; the public half is returned for binding into state.
// Issuance never touches the owner's own key.

package identity

import (
	"context"
	"fmt"
)

// IssuedCredential is the result of issuance: the public wire key to
// bind in state, and the keystore name under which the private half
// is custodied.
type IssuedCredential struct {
	PrincipalID string
	PublicKey   []byte // PKIX
	KeystoreKey string // keystore filename stem for the private half
}

// IssueFor generates and custodies a credential for principalID.
// Idempotence: re-issuance for an already-issued principal is refused
// at the state layer (public_key no longer pending); here, a keystore
// collision is also refused — two live private keys for one principal
// would be an untracked authority grant.
func (p *Provisioner) IssueFor(ctx context.Context, principalID string) (IssuedCredential, error) {
	select {
	case <-ctx.Done():
		return IssuedCredential{}, ctx.Err()
	default:
	}

	name := "principal-" + principalID
	if p.keystore.exists(name) {
		return IssuedCredential{}, fmt.Errorf(
			"identity: credential for %s already custodied (refusing overwrite)", principalID)
	}

	priv, err := generateP256()
	if err != nil {
		return IssuedCredential{}, err
	}
	raw, err := encodePEM(priv)
	if err != nil {
		return IssuedCredential{}, err
	}
	if err := p.keystore.storeKey(name, raw); err != nil {
		return IssuedCredential{}, err
	}
	pub, err := marshalPublic(priv)
	if err != nil {
		return IssuedCredential{}, err
	}
	return IssuedCredential{
		PrincipalID: principalID,
		PublicKey:   pub,
		KeystoreKey: name,
	}, nil
}

// exported for IssueFor's existence check
var _ = (*Keystore)(nil) // see keystore.go additions: exists, storeKey
