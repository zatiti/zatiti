package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Internal operations for their exact caller allowlists: bootstrap (one
// exclusive installation-initializing transaction), authority (effective
// authority resolution), promote (policy, bounded by a ceiling grant),
// restrict (immediate demotion), and activate/validate (compiler candidates;
// identity owns no draftable kind so they perform no live changes).

func (s *Service) activate(ctx context.Context, unit contract.Unit, in candidateInput) (contract.Payload, error) {
	// Identity owns no compiler-draftable kind: activation of any candidate
	// changes nothing in this domain, so the version list is empty.
	return completed(versionsOut{Versions: []refOut{}})
}

func (s *Service) validate(ctx context.Context, unit contract.Unit, in candidateInput) (contract.Payload, error) {
	// Identity owns no compiler-draftable kind: every candidate change is
	// outside this domain, so the validation is empty and carries no
	// diagnostics, requirements or dependencies.
	return completed(validationOut{
		Diagnostics:  []diagnosticOut{},
		Requirements: []requirementOut{},
		Dependencies: []refOut{},
	})
}

func (s *Service) bootstrap(ctx context.Context, unit contract.Unit, in bootstrapInput) (contract.Payload, error) {
	// Bootstrap runs before any principal can authenticate: no authorize
	// call. The registry's caller allowlist gates this operation to the
	// installation module alone.
	if in.InstallationID != unit.Scope().InstallationID {
		return contract.Payload{}, invalidInput("bootstrap installation does not match the transaction scope")
	}
	var existing int64
	err := unit.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM identity_principals WHERE installation_id = ?`,
		unit.Scope().InstallationID).Scan(&existing)
	if err != nil {
		return contract.Payload{}, fmt.Errorf("identity: check bootstrap state: %w", err)
	}
	if existing > 0 {
		return contract.Payload{}, conflict("this installation is already bootstrapped; bootstrap runs exactly once")
	}
	if err := s.assertNameUnique(ctx, unit, in.Name); err != nil {
		return contract.Payload{}, err
	}
	var dupOwner, dupCred int64
	if err := unit.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM identity_principals WHERE id = ?`, string(in.OwnerID)).Scan(&dupOwner); err != nil {
		return contract.Payload{}, fmt.Errorf("identity: check owner id: %w", err)
	}
	if err := unit.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM identity_credentials WHERE id = ?`, string(in.CredentialID)).Scan(&dupCred); err != nil {
		return contract.Payload{}, fmt.Errorf("identity: check credential id: %w", err)
	}
	if dupOwner > 0 || dupCred > 0 {
		return contract.Payload{}, conflict("bootstrap identity references already exist")
	}
	digest, err := s.resolveTokenDigest(ctx, in.StoreRef)
	if err != nil {
		return contract.Payload{}, err
	}
	now := s.deps.Clock.Now()
	owner := principalRow{
		ID:        in.OwnerID,
		Version:   1,
		Kind:      "human",
		Name:      in.Name,
		Scope:     contract.Scope{InstallationID: in.InstallationID},
		Revoked:   false,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.insertPrincipal(ctx, unit, owner); err != nil {
		return contract.Payload{}, err
	}
	cred := credentialRow{
		ID:          in.CredentialID,
		Version:     1,
		PrincipalID: owner.ID,
		StoreRef:    in.StoreRef,
		ExpiresAt:   nil,
		Revoked:     false,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.insertCredential(ctx, unit, cred, digest); err != nil {
		return contract.Payload{}, err
	}
	// The owner is the administrative trust root established under OS-level
	// installation access: bootstrap grants it the installation-wide wildcard
	// in the same exclusive transaction, so the human owner can administer
	// the installation from here on. Every later grant is bounded by current
	// authority; this is the only grant created outside authorize.
	root := grantRow{
		ID:            contract.ID(s.deps.IDs.New()),
		Version:       1,
		PrincipalID:   owner.ID,
		Scope:         owner.Scope,
		Capabilities:  []string{capWildcard},
		Destinations:  []string{},
		Denied:        false,
		ExpiresAt:     nil,
		ParentGrantID: nil,
		Revoked:       false,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.insertGrant(ctx, unit, root); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventPrincipalCreated, owner.ID, 1); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventGrantCreated, root.ID, 1); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventCredProvisioned, cred.ID, 1); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventBootstrap, owner.ID, 1); err != nil {
		return contract.Payload{}, err
	}
	return completed(resourceOut[principalOut]{Resource: owner.wire()})
}

func (s *Service) authority(ctx context.Context, unit contract.Unit, in authorityInput) (contract.Payload, error) {
	if _, err := s.authorize(ctx, unit, opAuthority, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	p, found, err := s.loadPrincipal(ctx, unit, in.PrincipalID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("principal %s is unknown in this installation", in.PrincipalID)
	}
	now := s.deps.Clock.Now()
	grants, err := s.effectiveGrants(ctx, unit, in.PrincipalID, now)
	if err != nil {
		return contract.Payload{}, err
	}
	relevant := make([]grantOut, 0, len(grants))
	for _, g := range grants {
		if grantCoversScope(g, in.Scope) {
			relevant = append(relevant, g.wire())
		}
	}
	restrictions, err := s.activeRestrictions(ctx, unit, in.PrincipalID)
	if err != nil {
		return contract.Payload{}, err
	}
	if restrictions == nil {
		restrictions = []string{}
	}
	return completed(resourceOut[authorityOut]{Resource: authorityOut{
		Principal:    p.wire(),
		Grants:       relevant,
		Restrictions: restrictions,
	}})
}

// grantCoversScope reports whether one grant reaches the scope: allow grants
// use exact-cover semantics; deny grants match like denials do (an unset deny
// dimension is a wildcard), so a targeted denial is visible at the scope it
// constrains.
func grantCoversScope(g grantRow, s contract.Scope) bool {
	if g.Denied {
		return dimDeny(g.Scope.InstallationID, s.InstallationID) &&
			dimDeny(g.Scope.OrganizationID, s.OrganizationID) &&
			dimDeny(g.Scope.ProjectID, s.ProjectID) &&
			dimDeny(g.Scope.WorkerID, s.WorkerID) &&
			dimDeny(g.Scope.TaskID, s.TaskID)
	}
	env := newEnvelope()
	env.add(g)
	return env.coversScope(s)
}

func (s *Service) promote(ctx context.Context, unit contract.Unit, in promoteInput) (contract.Payload, error) {
	if _, err := s.authorize(ctx, unit, opPromote, unit.Scope()); err != nil {
		return contract.Payload{}, err
	}
	target, found, err := s.loadPrincipal(ctx, unit, in.PrincipalID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("principal %s is unknown in this installation", in.PrincipalID)
	}
	if target.Revoked {
		return contract.Payload{}, invalidInput("cannot promote a revoked principal")
	}
	q := in.Qualification
	if q.WorkerID != in.PrincipalID {
		return contract.Payload{}, invalidInput("the qualification does not belong to the principal being promoted")
	}
	if q.State != "qualified" {
		return contract.Payload{}, verificationFailed("promotion requires a qualified qualification; this one is %q", q.State)
	}
	var replayed int64
	err = unit.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM identity_promotions
		WHERE qualification_id = ? AND qualification_version = ?`,
		string(q.ID), q.Version).Scan(&replayed)
	if err != nil {
		return contract.Payload{}, fmt.Errorf("identity: check promotion replay: %w", err)
	}
	if replayed > 0 {
		return contract.Payload{}, conflict("qualification %s v%d has already been activated", q.ID, q.Version)
	}
	ceiling, found, err := s.loadGrant(ctx, unit, in.CeilingGrantID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("ceiling grant %s is unknown in this installation", in.CeilingGrantID)
	}
	now := s.deps.Clock.Now()
	if ceiling.Denied || ceiling.Revoked || ceiling.expired(now) {
		return contract.Payload{}, permissionDenied("the ceiling grant is not effective")
	}
	alive, err := s.chainAliveDB(ctx, unit, ceiling, now)
	if err != nil {
		return contract.Payload{}, err
	}
	if !alive {
		return contract.Payload{}, permissionDenied("the ceiling grant's parent chain is broken; its delegations are dead")
	}
	ceilingEnv := newEnvelope()
	ceilingEnv.add(ceiling)
	if !ceilingEnv.coversScope(ceiling.Scope) {
		return contract.Payload{}, permissionDenied("internal error: ceiling scope does not cover itself")
	}
	if !ceilingEnv.allowsCaps([]string{q.Capability}) {
		return contract.Payload{}, permissionDenied("the ceiling grant does not carry the qualified capability")
	}
	if !ceilingEnv.allowsDests(q.Destinations) {
		return contract.Payload{}, permissionDenied("the qualified destinations escape the ceiling grant")
	}
	g := grantRow{
		ID:            contract.ID(s.deps.IDs.New()),
		Version:       1,
		PrincipalID:   in.PrincipalID,
		Scope:         ceiling.Scope,
		Capabilities:  []string{q.Capability},
		Destinations:  q.Destinations,
		Denied:        false,
		ExpiresAt:     nil,
		ParentGrantID: &ceiling.ID,
		Revoked:       false,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.insertGrant(ctx, unit, g); err != nil {
		return contract.Payload{}, err
	}
	_, err = unit.ExecContext(ctx, `
		INSERT INTO identity_promotions
			(qualification_id, qualification_version, grant_id, ceiling_id, rule_id, rule_version, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		string(q.ID), q.Version, string(g.ID), string(ceiling.ID),
		string(q.Rule.ID), q.Rule.Version, formatStamp(now))
	if err != nil {
		return contract.Payload{}, fmt.Errorf("identity: record promotion: %w", err)
	}
	if err := emitTransition(ctx, unit, eventPromotion, g.ID, 1); err != nil {
		return contract.Payload{}, err
	}
	return completed(resourceOut[grantOut]{Resource: g.wire()})
}

// chainAliveDB walks a grant's parent chain against the database, checking
// every ancestor exists, is not revoked and is not expired. A cycle fails
// closed.
func (s *Service) chainAliveDB(ctx context.Context, unit contract.Unit, g grantRow, now time.Time) (bool, error) {
	seen := map[contract.ID]bool{g.ID: true}
	cur := g
	for cur.ParentGrantID != nil {
		if seen[*cur.ParentGrantID] {
			return false, nil
		}
		seen[*cur.ParentGrantID] = true
		parent, found, err := s.loadGrant(ctx, unit, *cur.ParentGrantID)
		if err != nil {
			return false, err
		}
		if !found || parent.Revoked || parent.expired(now) {
			return false, nil
		}
		cur = parent
	}
	return true, nil
}

func (s *Service) restrict(ctx context.Context, unit contract.Unit, in restrictInput) (contract.Payload, error) {
	if _, err := s.authorize(ctx, unit, opRestrict, unit.Scope()); err != nil {
		return contract.Payload{}, err
	}
	if in.Capability == "" {
		return contract.Payload{}, invalidInput("capability is required")
	}
	p, found, err := s.loadPrincipal(ctx, unit, in.PrincipalID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("principal %s is unknown in this installation", in.PrincipalID)
	}
	_ = p
	row := unit.QueryRowContext(ctx, `
		SELECT id, version, principal_id, capability, reason, active, created_at
		FROM identity_restrictions
		WHERE principal_id = ? AND installation_id = ? AND capability = ?
		ORDER BY created_at DESC
		LIMIT 1`, in.PrincipalID, unit.Scope().InstallationID, in.Capability)
	existing, err := scanRestriction(row.Scan)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return contract.Payload{}, fmt.Errorf("identity: load restriction: %w", err)
	}
	var out dispositionOut
	switch {
	case err == nil && existing.Active:
		// Idempotent: the restriction already stands.
		out = dispositionOut{ID: existing.ID, Version: contract.Version(existing.Version), State: "applied"}
	case err == nil:
		existing.Active = true
		existing.Version++
		if err := s.updateRestriction(ctx, unit, existing); err != nil {
			return contract.Payload{}, err
		}
		out = dispositionOut{ID: existing.ID, Version: contract.Version(existing.Version), State: "applied"}
		if err := emitTransition(ctx, unit, eventRestriction, existing.ID, contract.Version(existing.Version)); err != nil {
			return contract.Payload{}, err
		}
	default:
		r := restrictionRow{
			ID:          contract.ID(s.deps.IDs.New()),
			Version:     1,
			PrincipalID: in.PrincipalID,
			Capability:  in.Capability,
			Reason:      in.Reason,
			Active:      true,
			CreatedAt:   s.deps.Clock.Now(),
		}
		if err := s.insertRestriction(ctx, unit, r); err != nil {
			return contract.Payload{}, err
		}
		out = dispositionOut{ID: r.ID, Version: 1, State: "applied"}
		if err := emitTransition(ctx, unit, eventRestriction, r.ID, 1); err != nil {
			return contract.Payload{}, err
		}
	}
	return completed(resourceOut[dispositionOut]{Resource: out})
}

func (s *Service) insertRestriction(ctx context.Context, unit contract.Unit, r restrictionRow) error {
	_, err := unit.ExecContext(ctx, `
		INSERT INTO identity_restrictions
			(id, version, principal_id, installation_id, capability, reason, active, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		string(r.ID), r.Version, string(r.PrincipalID), string(unit.Scope().InstallationID),
		r.Capability, r.Reason, boolInt(r.Active), formatStamp(r.CreatedAt))
	if err != nil {
		return fmt.Errorf("identity: insert restriction: %w", err)
	}
	return nil
}

func (s *Service) updateRestriction(ctx context.Context, unit contract.Unit, r restrictionRow) error {
	res, err := unit.ExecContext(ctx, `
		UPDATE identity_restrictions
		SET version = ?, reason = ?, active = ?
		WHERE id = ? AND installation_id = ? AND version = ?`,
		r.Version, r.Reason, boolInt(r.Active),
		string(r.ID), string(unit.Scope().InstallationID), r.Version-1)
	if err != nil {
		return fmt.Errorf("identity: update restriction: %w", err)
	}
	return expectOneRow(res, "restriction", r.ID)
}
