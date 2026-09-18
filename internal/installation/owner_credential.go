package installation

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// The owner credential. Identity stores the SHA-256 of the custodied bytes
// and the server authenticates the SHA-256 of the verbatim Authorization
// header value, so the custodied bytes are the complete header value a
// client presents: the Bearer scheme and 32 random bytes as unpadded
// base64url (token68). Raw random bytes are not a legal header value and
// could never authenticate. A local contract.CredentialSource therefore
// returns Secrets.Get(store reference) unchanged.
const (
	ownerCredentialScheme  = "Bearer "
	ownerCredentialEntropy = 32
)

// mintOwnerCredential returns a fresh owner credential in its custodied,
// header-legal form. The caller zeroes the returned buffer after custody.
func mintOwnerCredential() ([]byte, error) {
	token := make([]byte, ownerCredentialEntropy)
	if _, err := rand.Read(token); err != nil {
		return nil, err
	}
	secret := make([]byte, len(ownerCredentialScheme)+base64.RawURLEncoding.EncodedLen(len(token)))
	copy(secret, ownerCredentialScheme)
	base64.RawURLEncoding.Encode(secret[len(ownerCredentialScheme):], token)
	for i := range token {
		token[i] = 0
	}
	return secret, nil
}

// OwnerCredential is the non-secret metadata of the bootstrap owner's
// credential: who it authenticates and the opaque secret-store reference it
// is custodied under. It never carries the secret.
type OwnerCredential struct {
	InstallationID contract.ID
	OwnerID        contract.ID
	CredentialID   contract.ID
	StoreRef       string
}

// OwnerCredential reports the completed bootstrap's owner credential
// metadata. It is a Go API for local entrypoint assembly only, which opens
// the read snapshot and uses the reference to provision the local operator's
// credential profile (a contract.CredentialSource over the same secure
// store). No operation, result, event or log exposes it: installation.init
// returns metadata only. Before a completed bootstrap it refuses with
// prerequisite_missing.
func (s *Service) OwnerCredential(ctx context.Context, reader contract.Reader) (OwnerCredential, error) {
	var out OwnerCredential
	var installationID, ownerID, credentialID string
	err := reader.QueryRowContext(ctx, `
		SELECT i.installation_id, i.owner_id, i.credential_id, i.store_ref
		FROM installation_bootstrap_intents i
		JOIN installation_state s ON s.id = i.installation_id
		WHERE i.state = 'completed'`).
		Scan(&installationID, &ownerID, &credentialID, &out.StoreRef)
	if errors.Is(err, sql.ErrNoRows) {
		return OwnerCredential{}, prerequisiteMissing("this installation has no completed bootstrap")
	}
	if err != nil {
		return OwnerCredential{}, fmt.Errorf("installation: load owner credential metadata: %w", err)
	}
	if out.StoreRef == "" {
		return OwnerCredential{}, prerequisiteMissing("the completed bootstrap recorded no owner credential reference")
	}
	out.InstallationID = contract.ID(installationID)
	out.OwnerID = contract.ID(ownerID)
	out.CredentialID = contract.ID(credentialID)
	return out, nil
}

// completeBootstrapIntent marks the intent completed and records the opaque
// store reference of the owner credential it custodied.
func completeBootstrapIntent(ctx context.Context, unit contract.Unit, id contract.ID, storeRef string, now time.Time) error {
	res, err := unit.ExecContext(ctx, `
		UPDATE installation_bootstrap_intents
		SET state = 'completed', store_ref = ?, updated_at = ?
		WHERE id = ?`, storeRef, formatStamp(now), string(id))
	if err != nil {
		return fmt.Errorf("installation: complete bootstrap intent %s: %w", id, err)
	}
	return expectOneRow(res, "bootstrap intent", id)
}
