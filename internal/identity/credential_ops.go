package identity

import (
	"context"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Credential operations. Identity never sees raw credential bytes through
// JSON: provision resolves the helper-written store reference through the
// injected secret store inside this transaction and persists only the SHA-256
// digest of the custodied bytes as a one-way token verifier. A token digest
// that already exists — live or revoked — is a conflict, so a replayed token
// can never resurrect authority after a revoked restore.
//
// Note for the integration owner: resolving the store reference performs
// secret-store access inside the Unit callback, which the contract's Unit
// prose discourages. The alternative (resolving outside the transaction)
// would leave the digest stale relative to the row it guards, so the access
// is kept here, read-only, before any write.

func (s *Service) credentialProvision(ctx context.Context, unit contract.Unit, in credentialProvisionInput) (contract.Payload, error) {
	if _, err := s.authorize(ctx, unit, opCredProvision, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	if in.Scope.InstallationID != unit.Scope().InstallationID {
		return contract.Payload{}, invalidInput("credential scope installation does not match the transaction scope")
	}
	p, found, err := s.loadPrincipal(ctx, unit, in.PrincipalID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("principal %s is unknown in this installation", in.PrincipalID)
	}
	if p.Revoked {
		return contract.Payload{}, invalidInput("cannot provision a credential for a revoked principal")
	}
	digest, err := s.resolveTokenDigest(ctx, in.StoreRef)
	if err != nil {
		return contract.Payload{}, err
	}
	var dup int64
	err = unit.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM identity_credentials WHERE token_digest = ?`, digest).Scan(&dup)
	if err != nil {
		return contract.Payload{}, fmt.Errorf("identity: check token digest: %w", err)
	}
	if dup > 0 {
		return contract.Payload{}, conflict("this credential token is already registered; revoked tokens are never re-registered")
	}
	now := s.deps.Clock.Now()
	c := credentialRow{
		ID:          contract.ID(s.deps.IDs.New()),
		Version:     1,
		PrincipalID: in.PrincipalID,
		StoreRef:    in.StoreRef,
		ExpiresAt:   in.ExpiresAt,
		Revoked:     false,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.insertCredential(ctx, unit, c, digest); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventCredProvisioned, c.ID, 1); err != nil {
		return contract.Payload{}, err
	}
	return completed(resourceOut[credentialOut]{Resource: c.wire()})
}

// resolveTokenDigest fetches the custodied credential bytes under store_ref
// and returns their SHA-256 digest, zeroing the bytes immediately after
// hashing. The secret store must be injected; failures surface as
// prerequisite_missing without echoing the reference into the message beyond
// what the caller already supplied.
func (s *Service) resolveTokenDigest(ctx context.Context, storeRef string) (string, error) {
	if s.deps.Secrets == nil {
		return "", prerequisiteMissing("no secret store is configured; credential material cannot be resolved")
	}
	secret, err := s.deps.Secrets.Get(ctx, storeRef)
	if err != nil {
		return "", prerequisiteMissing("credential material is unavailable for the given store reference")
	}
	if len(secret) == 0 {
		return "", prerequisiteMissing("credential material is empty for the given store reference")
	}
	digest := string(contract.Hash(secret))
	for i := range secret {
		secret[i] = 0
	}
	return digest, nil
}

func (s *Service) insertCredential(ctx context.Context, unit contract.Unit, c credentialRow, digest string) error {
	var expires any
	if c.ExpiresAt != nil {
		expires = formatStamp(*c.ExpiresAt)
	}
	_, err := unit.ExecContext(ctx, `
		INSERT INTO identity_credentials
			(id, version, principal_id, installation_id, store_ref, token_digest,
			 expires_at, revoked, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(c.ID), c.Version, string(c.PrincipalID), string(unit.Scope().InstallationID),
		c.StoreRef, digest, expires, boolInt(c.Revoked),
		formatStamp(c.CreatedAt), formatStamp(c.UpdatedAt))
	if err != nil {
		return fmt.Errorf("identity: insert credential: %w", err)
	}
	return nil
}

func (s *Service) credentialRevoke(ctx context.Context, unit contract.Unit, in credentialRevokeInput) (contract.Payload, error) {
	if _, err := s.authorize(ctx, unit, opCredRevoke, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	c, found, err := s.loadCredential(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("credential %s is unknown in this installation", in.ID)
	}
	if c.Revoked {
		// Idempotent. Authentication already fails for this credential and
		// the revocation record stands.
		return completed(resourceOut[credentialOut]{Resource: c.wire()})
	}
	if in.ExpectedVersion != contract.Version(c.Version) {
		return contract.Payload{}, staleVersion(
			"credential %s is at version %d, not the expected %d", in.ID, c.Version, in.ExpectedVersion)
	}
	c.Revoked = true
	c.Version++
	c.UpdatedAt = s.deps.Clock.Now()
	res, err := unit.ExecContext(ctx, `
		UPDATE identity_credentials
		SET version = ?, revoked = ?, updated_at = ?
		WHERE id = ? AND installation_id = ? AND version = ? AND revoked = 0`,
		c.Version, boolInt(c.Revoked), formatStamp(c.UpdatedAt),
		string(c.ID), string(unit.Scope().InstallationID), c.Version-1)
	if err != nil {
		return contract.Payload{}, fmt.Errorf("identity: revoke credential: %w", err)
	}
	if err := expectOneRow(res, "credential", c.ID); err != nil {
		return contract.Payload{}, err
	}
	if err := s.appendRevocation(ctx, unit, "credential", c.ID, ""); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventCredRevoked, c.ID, contract.Version(c.Version)); err != nil {
		return contract.Payload{}, err
	}
	return completed(resourceOut[credentialOut]{Resource: c.wire()})
}
