// internal/state/genlock.go
//
// Controller generation lifecycle (T1.8 support). The generation is
// advanced exactly once per controller acquisition of ownership, so
// anything bound to a previous generation (leases, pending reviews)
// is trivially detectable as stale.

package state

import (
	"context"
	"fmt"
)

// AdvanceGeneration bumps the installation generation by one and
// returns the new value. Caller must hold installation ownership
// (Open) and the installation row must exist (Bootstrap ran).
func (s *Store) AdvanceGeneration(ctx context.Context) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.DB().ExecContext(ctx, `
		UPDATE installation SET generation = generation + 1 WHERE id = 1`)
	if err != nil {
		return 0, fmt.Errorf("state: advance generation: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("state: advance generation: %w", err)
	}
	if n != 1 {
		return 0, ErrNotInitialized
	}
	s.generation = 0 // invalidate cache; Generation() re-reads
	return s.GenerationLocked(ctx)
}

// GenerationLocked is Generation without re-taking the mutex. Use only
// when s.mu is already held.
func (s *Store) GenerationLocked(ctx context.Context) (uint64, error) {
	if s.generation != 0 {
		return s.generation, nil
	}
	var g uint64
	err := s.DB().QueryRowContext(ctx,
		`SELECT generation FROM installation WHERE id = 1`).Scan(&g)
	if err != nil {
		return 0, fmt.Errorf("state: read generation: %w", err)
	}
	s.generation = g
	return g, nil
}
