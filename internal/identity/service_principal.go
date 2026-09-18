package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// The controller's scoped service identity. Bootstrap creates it in the same
// exclusive transaction as the human owner ("one initial human owner and
// scoped service identities"), so the trusted local controller has a
// registered, unrevoked service principal to run every application.Internal
// call under from the first tick.
//
// Its standing authority is exactly what the internal dispatch path checks
// against a principal's grants and nothing more. Which internal operations
// the controller may invoke is decided by each descriptor's caller allowlist
// at the Internal entry point, not by a grant; what every Internal call and
// every owner-side liveness recheck resolves through is _identity.authority,
// which admits its caller under that caller's own current authority. The
// service principal therefore holds one unbounded installation-wide allow
// grant carrying _identity.authority: no wildcard, no public operation, no
// destination, no expiry, no delegation. Revocation, restriction and grant
// revocation apply to it exactly as to any other principal, so the owner can
// stop the controller's admission immediately.
//
// The frozen _identity.bootstrap input carries the owner's credential
// reference only, so the controller principal is created without a
// credential: the in-process Internal seam authenticates by assembly trust
// and passes the actor explicitly, never by presented bytes.

// ControllerPrincipalName is the reserved principal name of the controller's
// service identity in every installation. Bootstrap refuses an owner that
// claims it, and name uniqueness refuses any later principal that does.
const ControllerPrincipalName = "controller"

// controllerCapabilities is the complete capability set of the controller's
// standing grant.
var controllerCapabilities = []string{opAuthority}

// createControllerPrincipal inserts the controller's service principal and
// its standing grant inside the bootstrap transaction and records both
// transitions. The caller has already proven the installation holds no
// principal at all, so the reserved name is free by construction.
func (s *Service) createControllerPrincipal(ctx context.Context, unit contract.Unit, installationID contract.ID, now time.Time) error {
	ctrl := principalRow{
		ID:        contract.ID(s.deps.IDs.New()),
		Version:   1,
		Kind:      contract.KindService,
		Name:      ControllerPrincipalName,
		Scope:     contract.Scope{InstallationID: installationID},
		Revoked:   false,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.insertPrincipal(ctx, unit, ctrl); err != nil {
		return err
	}
	standing := grantRow{
		ID:            contract.ID(s.deps.IDs.New()),
		Version:       1,
		PrincipalID:   ctrl.ID,
		Scope:         ctrl.Scope,
		Capabilities:  append([]string(nil), controllerCapabilities...),
		Destinations:  []string{},
		Denied:        false,
		ExpiresAt:     nil,
		ParentGrantID: nil,
		Revoked:       false,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.insertGrant(ctx, unit, standing); err != nil {
		return err
	}
	if err := emitTransition(ctx, unit, eventPrincipalCreated, ctrl.ID, 1); err != nil {
		return err
	}
	return emitTransition(ctx, unit, eventGrantCreated, standing.ID, 1)
}

// ControllerPrincipal reports the bootstrapped controller service principal
// as the actor every application.Internal call runs under. It is a Go API
// for local entrypoint assembly only, which opens the read snapshot and
// attaches the actor to the controller; no operation, result or event
// exposes it as such, and it carries no credential. Before a completed
// bootstrap it refuses with prerequisite_missing; once the owner has revoked
// the principal it refuses with permission_denied, so assembly fails closed
// instead of starting a controller every call would deny.
func (s *Service) ControllerPrincipal(ctx context.Context, reader contract.Reader) (contract.Actor, error) {
	if reader == nil {
		return contract.Actor{}, prerequisiteMissing("a read snapshot is required to resolve the controller principal")
	}
	var (
		id      string
		revoked int64
	)
	// The installation database is single-tenant by construction; the
	// reserved name is unique within it, so the kind and name identify the
	// principal without a scope.
	err := reader.QueryRowContext(ctx, `
		SELECT id, revoked FROM identity_principals
		WHERE kind = ? AND name = ?`, contract.KindService, ControllerPrincipalName).
		Scan(&id, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return contract.Actor{}, prerequisiteMissing("this installation has no completed bootstrap; no controller principal exists")
	}
	if err != nil {
		return contract.Actor{}, fmt.Errorf("identity: load controller principal: %w", err)
	}
	if revoked == 1 {
		return contract.Actor{}, permissionDenied("the controller principal is revoked")
	}
	return contract.Actor{PrincipalID: contract.ID(id), Kind: contract.KindService}, nil
}
