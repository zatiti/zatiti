package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Authentication. Identity stores only the SHA-256 digest of custodied
// credential bytes, so the digest is the one-way token verifier: presented
// material is hashed and matched against token_digest, which is UNIQUE across
// the installation database. Every authentication rechecks credential and
// principal revocation and expiry against current stored state; revocation is
// therefore immediate. Presented bytes are zeroed after hashing and never
// logged; every failure is the same generic verification failure so
// authentication cannot be probed for account state.

// Authenticate implements contract.Authenticator. The reader supplies the
// read snapshot the lookup runs in.
func (s *Service) Authenticate(ctx context.Context, reader contract.Reader, credential []byte) (contract.Actor, error) {
	if len(credential) == 0 {
		return contract.Actor{}, verificationFailed("authentication failed")
	}
	digest := string(contract.Hash(credential))
	for i := range credential {
		credential[i] = 0
	}
	return s.authenticateDigest(ctx, reader, digest)
}

// AuthenticateCertificate implements contract.Authenticator for
// certificate-fingerprint authentication. The fingerprint is already a
// SHA-256 hex digest, so it matches token_digest directly.
func (s *Service) AuthenticateCertificate(ctx context.Context, reader contract.Reader, fingerprint contract.Digest) (contract.Actor, error) {
	if fingerprint == "" {
		return contract.Actor{}, verificationFailed("authentication failed")
	}
	return s.authenticateDigest(ctx, reader, string(fingerprint))
}

// authenticateDigest resolves a token digest to an actor. Both the credential
// and its principal must be live, unrevoked and unexpired in the reader's
// installation.
func (s *Service) authenticateDigest(ctx context.Context, reader contract.Reader, digest string) (contract.Actor, error) {
	if reader == nil {
		return contract.Actor{}, verificationFailed("authentication failed")
	}
	var (
		credentialID string
		credRev      int64
		expires      sql.NullString
		principalID  string
		kind         string
		principalRev int64
	)
	// The installation database is single-tenant by construction, so the
	// principal's installation must equal the credential's; contract.Reader
	// carries no scope of its own.
	err := reader.QueryRowContext(ctx, `
		SELECT c.id, c.expires_at, c.revoked, p.id, p.kind, p.revoked
		FROM identity_credentials c
		JOIN identity_principals p ON p.id = c.principal_id
		WHERE c.token_digest = ?
		  AND p.installation_id = c.installation_id`,
		digest).
		Scan(&credentialID, &expires, &credRev, &principalID, &kind, &principalRev)
	if errors.Is(err, sql.ErrNoRows) {
		return contract.Actor{}, verificationFailed("authentication failed")
	}
	if err != nil {
		return contract.Actor{}, fmt.Errorf("identity: authenticate: %w", err)
	}
	if credRev == 1 || principalRev == 1 {
		return contract.Actor{}, verificationFailed("authentication failed")
	}
	if expires.Valid {
		t, err := parseStamp(expires.String)
		if err != nil {
			return contract.Actor{}, fmt.Errorf("identity: authenticate: %w", err)
		}
		if !s.deps.Clock.Now().Before(t) {
			return contract.Actor{}, verificationFailed("authentication failed")
		}
	}
	return contract.Actor{
		PrincipalID:  contract.ID(principalID),
		Kind:         kind,
		CredentialID: contract.ID(credentialID),
	}, nil
}

// Compile-time proof that *Service implements contract.Authenticator.
var _ contract.Authenticator = (*Service)(nil)
