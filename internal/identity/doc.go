// Package identity owns principals, grants, authentication metadata,
// credential references and immediate revocation for one installation.
//
// The service implements contract.Module with Name "identity" and
// contract.Authenticator. Public operations administer principals, grants and
// credentials under current authority; internal operations expose bootstrap,
// authority resolution, promotion, restriction and compiler candidate
// validation to their exact caller allowlists. Raw credential bytes never
// enter this package through JSON: provision and bootstrap resolve a
// helper-provisioned opaque store reference through the injected secret store
// and persist only the SHA-256 digest of the custodied bytes. Tokens are
// high-entropy platform-custodied secrets, so the unpinned digest is a
// suitable one-way verifier; presented tokens are zeroed after use and never
// logged.
//
// Revocation is immediate and immutable: principal.revoke, grant.revoke and
// credential.revoke flip the entity's revoked flag and append an
// identity_revocations record that is never updated or removed, so a restore
// of an older database backup cannot resurrect authority the installation
// revoked. Authentication rechecks revocation and expiry on every request;
// principal kind is immutable and revoked entities cannot be un-revoked.
package identity
