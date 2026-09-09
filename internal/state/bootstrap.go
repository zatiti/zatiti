// internal/state/bootstrap.go
//
// T1.3: `zatiti init`. Refuses any already-initialized installation
// (RFC §5). Owner principal provisioning is delegated to the identity
// root via the PrincipalProvisioner port; state persists only the
// public principal reference and never touches credential material.

package state

import (
	"context"
	"fmt"
)

// PrincipalProvisioner is the identity-owned port state calls during
// bootstrap. Returns a public principal reference only.
type PrincipalProvisioner interface {
	ProvisionOwner(ctx context.Context) (PrincipalRef, error)
}

// PrincipalRef is the public, persistable identity of a principal.
type PrincipalRef struct {
	ID        string
	PublicKey []byte
	CreatedAt int64 // unix seconds
}

// Bootstrap initializes a fresh installation. The only repair it
// permits is retrying a crashed bootstrap whose installation row was
// never committed — migrations and the ledger are individually
// transactional, and the installation row is the commit point.
func Bootstrap(ctx context.Context, s *Store, provisioner PrincipalProvisioner) error {
	// Apply schema migrations first (idempotent, individually recorded).
	if err := Migrate(ctx, s.DB()); err != nil {
		return fmt.Errorf("state: migrate: %w", err)
	}

	// Guard against concurrent bootstrap in-process; cross-process is
	// already excluded by the installation lock in Open.
	s.mu.Lock()
	defer s.mu.Unlock()

	var existing int
	if err := s.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM installation WHERE id = 1`).Scan(&existing); err != nil {
		return fmt.Errorf("state: probe installation: %w", err)
	}
	if existing != 0 {
		return ErrAlreadyInitialized
	}

	// Provision owner principal through the identity port.
	ref, err := provisioner.ProvisionOwner(ctx)
	if err != nil {
		return fmt.Errorf("state: provision owner: %w", err)
	}

	tx, err := s.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("state: begin bootstrap tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck — commit path below

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO installation (id, generation, schema_version, created_at)
		VALUES (1, 1, ?, strftime('%s','now'))`, len(migrations)); err != nil {
		return fmt.Errorf("state: insert installation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO principals (id, kind, public_key, created_at)
		VALUES (?, 'owner', ?, ?)`, ref.ID, ref.PublicKey, ref.CreatedAt); err != nil {
		return fmt.Errorf("state: insert owner principal: %w", err)
	}
	orgID := newID()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO organizations (id, name, created_by, created_at)
		VALUES (?, 'root', ?, strftime('%s','now'))`, orgID, ref.ID); err != nil {
		return fmt.Errorf("state: insert root organization: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memberships (principal_id, org_id, role)
		VALUES (?, ?, 'owner')`, ref.ID, orgID); err != nil {
		return fmt.Errorf("state: insert owner membership: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("state: commit bootstrap: %w", err)
	}
	s.generation = 1
	return nil
}
