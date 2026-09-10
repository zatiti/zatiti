package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Grant operations. A new or updated grant must fit inside the caller's
// current effective envelope and, when delegated, inside the effective parent
// grant: authority can only narrow as it flows. A caller can never create a
// grant for themselves, cannot delegate from a grant they do not effectively
// hold, and cannot reassign or re-parent an existing grant.

func (s *Service) grantCreate(ctx context.Context, unit contract.Unit, in grantCreateInput) (contract.Payload, error) {
	env, caller, err := s.authorizeCaller(ctx, unit, opGrantCreate, in.Scope)
	if err != nil {
		return contract.Payload{}, err
	}
	d := in.Definition
	if d.Scope.InstallationID != unit.Scope().InstallationID {
		return contract.Payload{}, invalidInput("grant scope installation does not match the transaction scope")
	}
	if d.PrincipalID == caller.ID {
		return contract.Payload{}, permissionDenied("a principal cannot grant itself authority")
	}
	grantee, found, err := s.loadPrincipal(ctx, unit, d.PrincipalID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("grantee principal %s is unknown in this installation", d.PrincipalID)
	}
	if grantee.Revoked {
		return contract.Payload{}, invalidInput("cannot grant to a revoked principal")
	}
	if err := s.checkCeiling(ctx, unit, env, caller, d); err != nil {
		return contract.Payload{}, err
	}
	now := s.deps.Clock.Now()
	g := grantRow{
		ID:            contract.ID(s.deps.IDs.New()),
		Version:       1,
		PrincipalID:   d.PrincipalID,
		Scope:         d.Scope,
		Capabilities:  d.Capabilities,
		Destinations:  d.Destinations,
		Denied:        d.Denied,
		ExpiresAt:     d.ExpiresAt,
		ParentGrantID: d.ParentGrantID,
		Revoked:       false,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.insertGrant(ctx, unit, g); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventGrantCreated, g.ID, 1); err != nil {
		return contract.Payload{}, err
	}
	return completed(resourceOut[grantOut]{Resource: g.wire()})
}

// authorizeCaller is authorize plus the loaded caller row, for operations
// that compare against the caller's identity.
func (s *Service) authorizeCaller(ctx context.Context, unit contract.Unit, capability string, requestScope contract.Scope) (*envelope, principalRow, error) {
	env, err := s.authorize(ctx, unit, capability, requestScope)
	if err != nil {
		return nil, principalRow{}, err
	}
	caller, found, err := s.loadPrincipal(ctx, unit, unit.Actor().PrincipalID)
	if err != nil {
		return nil, principalRow{}, err
	}
	if !found {
		return nil, principalRow{}, permissionDenied("caller principal is not registered in this installation")
	}
	return env, caller, nil
}

// checkCeiling enforces every delegation bound on a proposed grant
// definition: the definition fits the caller's current envelope and, when a
// parent grant is named, the parent is an effective grant of the caller and
// the definition fits inside it.
func (s *Service) checkCeiling(ctx context.Context, unit contract.Unit, env *envelope, caller principalRow, d grantDefinition) error {
	if !env.coversScope(d.Scope) {
		return permissionDenied("the grant's scope escapes the caller's granted envelope")
	}
	if !env.allowsCaps(d.Capabilities) {
		return permissionDenied("the grant's capabilities escape the caller's granted envelope")
	}
	if !env.allowsDests(d.Destinations) {
		return permissionDenied("the grant's destinations escape the caller's granted envelope")
	}
	if !env.allowsExpiry(d.ExpiresAt) {
		return permissionDenied("the grant's expiry escapes the caller's granted envelope")
	}
	if d.ParentGrantID == nil {
		return nil
	}
	parent, found, err := s.loadGrant(ctx, unit, *d.ParentGrantID)
	if err != nil {
		return err
	}
	if !found {
		return notFound("parent grant %s is unknown in this installation", *d.ParentGrantID)
	}
	if parent.PrincipalID != caller.ID {
		return permissionDenied("a grant can only delegate from a grant of the caller")
	}
	if parent.Denied {
		return permissionDenied("cannot delegate from a deny grant")
	}
	effective, err := s.effectiveGrants(ctx, unit, caller.ID, s.deps.Clock.Now())
	if err != nil {
		return err
	}
	var live bool
	for _, g := range effective {
		if g.ID == parent.ID {
			live = true
			break
		}
	}
	if !live {
		return permissionDenied("the parent grant is revoked or expired; its delegations are dead")
	}
	parentEnv := newEnvelope()
	parentEnv.add(parent)
	if !parentEnv.coversScope(d.Scope) {
		return permissionDenied("the grant's scope escapes its parent grant")
	}
	if !parentEnv.allowsCaps(d.Capabilities) {
		return permissionDenied("the grant's capabilities escape its parent grant")
	}
	if !parentEnv.allowsDests(d.Destinations) {
		return permissionDenied("the grant's destinations escape its parent grant")
	}
	if !parentEnv.allowsExpiry(d.ExpiresAt) {
		return permissionDenied("the grant's expiry escapes its parent grant")
	}
	return nil
}

func (s *Service) insertGrant(ctx context.Context, unit contract.Unit, g grantRow) error {
	scopeJSON, err := encodeScope(g.Scope)
	if err != nil {
		return err
	}
	capsJSON, err := encodeStrings(g.Capabilities)
	if err != nil {
		return err
	}
	destsJSON, err := encodeStrings(g.Destinations)
	if err != nil {
		return err
	}
	var expires any
	if g.ExpiresAt != nil {
		expires = formatStamp(*g.ExpiresAt)
	}
	var parent any
	if g.ParentGrantID != nil {
		parent = string(*g.ParentGrantID)
	}
	_, err = unit.ExecContext(ctx, `
		INSERT INTO identity_grants
			(id, version, principal_id, installation_id, organization_id, project_id,
			 worker_id, task_id, scope_json, capabilities_json, destinations_json,
			 denied, expires_at, parent_grant_id, revoked, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(g.ID), g.Version, string(g.PrincipalID), string(g.Scope.InstallationID),
		string(g.Scope.OrganizationID), string(g.Scope.ProjectID),
		string(g.Scope.WorkerID), string(g.Scope.TaskID),
		scopeJSON, capsJSON, destsJSON, boolInt(g.Denied), expires, parent,
		boolInt(g.Revoked), formatStamp(g.CreatedAt), formatStamp(g.UpdatedAt))
	if err != nil {
		return fmt.Errorf("identity: insert grant: %w", err)
	}
	return nil
}

func (s *Service) grantGet(ctx context.Context, unit contract.Unit, in grantGetInput) (contract.Payload, error) {
	if _, err := s.authorize(ctx, unit, opGrantGet, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	g, found, err := s.loadGrant(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("grant %s is unknown in this installation", in.ID)
	}
	return completed(resourceOut[grantOut]{Resource: g.wire()})
}

func (s *Service) grantList(ctx context.Context, unit contract.Unit, in listInput) (contract.Payload, error) {
	if _, err := s.authorize(ctx, unit, opGrantList, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	filter := in.Filter
	if filter != nil && (filter.Key != nil || filter.Descendants != nil || filter.NeedsYou != nil) {
		return contract.Payload{}, invalidInput("grant.list supports only state, parent_id, organization_id, worker_id and task_id filters")
	}
	if filter != nil && filter.State != nil {
		switch *filter.State {
		case "active", "revoked":
		default:
			return contract.Payload{}, invalidInput("grant.list state filter must be \"active\" or \"revoked\"")
		}
	}
	where := "installation_id = ?"
	args := []any{string(unit.Scope().InstallationID)}
	if filter != nil {
		if filter.State != nil {
			if *filter.State == "revoked" {
				where += " AND revoked = 1"
			} else {
				where += " AND revoked = 0"
			}
		}
		if filter.ParentID != nil {
			where += " AND parent_grant_id = ?"
			args = append(args, string(*filter.ParentID))
		}
		if filter.OrganizationID != nil {
			where += " AND organization_id = ?"
			args = append(args, string(*filter.OrganizationID))
		}
		if filter.WorkerID != nil {
			where += " AND worker_id = ?"
			args = append(args, string(*filter.WorkerID))
		}
		if filter.TaskID != nil {
			where += " AND task_id = ?"
			args = append(args, string(*filter.TaskID))
		}
	}
	limit := defaultListLimit
	if in.Limit != nil {
		limit = *in.Limit
	}
	var keyset string
	if in.Cursor != nil {
		created, lastID, err := s.readCursor(opGrantList, unit.Scope(), filter, *in.Cursor)
		if err != nil {
			return contract.Payload{}, err
		}
		keyset = " AND (created_at > ? OR (created_at = ? AND id > ?))"
		args = append(args, formatStamp(created), formatStamp(created), string(lastID))
	}
	args = append(args, limit+1)
	rows, err := unit.QueryContext(ctx, `
		SELECT id, version, principal_id, scope_json, capabilities_json, destinations_json,
		       denied, expires_at, parent_grant_id, revoked, created_at, updated_at
		FROM identity_grants
		WHERE `+where+keyset+`
		ORDER BY created_at, id
		LIMIT ?`, args...)
	if err != nil {
		return contract.Payload{}, fmt.Errorf("identity: list grants: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var rowsOut []grantRow
	for rows.Next() {
		r, err := scanGrant(rows.Scan)
		if err != nil {
			return contract.Payload{}, err
		}
		rowsOut = append(rowsOut, r)
	}
	if err := rows.Err(); err != nil {
		return contract.Payload{}, fmt.Errorf("identity: iterate grants: %w", err)
	}
	items := make([]grantOut, 0, len(rowsOut))
	for _, r := range rowsOut {
		items = append(items, r.wire())
	}
	var next *string
	if int64(len(rowsOut)) > limit {
		rowsOut = rowsOut[:limit]
		items = items[:limit]
		last := rowsOut[len(rowsOut)-1]
		cursor, err := s.mintCursor(opGrantList, unit.Scope(), filter, last.CreatedAt, last.ID, s.deps.Clock.Now())
		if err != nil {
			return contract.Payload{}, err
		}
		next = &cursor
	}
	return listResult(itemsOut[grantOut]{Items: items}, next)
}

func (s *Service) grantUpdate(ctx context.Context, unit contract.Unit, in grantUpdateInput) (contract.Payload, error) {
	env, caller, err := s.authorizeCaller(ctx, unit, opGrantUpdate, in.Scope)
	if err != nil {
		return contract.Payload{}, err
	}
	g, found, err := s.loadGrant(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("grant %s is unknown in this installation", in.ID)
	}
	if g.Revoked {
		return contract.Payload{}, conflict("grant %s is revoked and cannot be modified", in.ID)
	}
	if in.ExpectedVersion != contract.Version(g.Version) {
		return contract.Payload{}, staleVersion(
			"grant %s is at version %d, not the expected %d", in.ID, g.Version, in.ExpectedVersion)
	}
	d := in.Definition
	if d.PrincipalID != g.PrincipalID {
		return contract.Payload{}, invalidInput("a grant cannot be reassigned to another principal")
	}
	if !sameRef(d.ParentGrantID, g.ParentGrantID) {
		return contract.Payload{}, invalidInput("parent_grant_id is immutable; revoke and re-grant instead")
	}
	if d.Scope.InstallationID != unit.Scope().InstallationID {
		return contract.Payload{}, invalidInput("grant scope installation does not match the transaction scope")
	}
	// The ceiling is evaluated against the caller's CURRENT envelope: a
	// delegator whose own authority has since been revoked or narrowed can no
	// longer widen the delegations beneath it.
	if err := s.checkCeiling(ctx, unit, env, caller, d); err != nil {
		return contract.Payload{}, err
	}
	g.Scope = d.Scope
	g.Capabilities = d.Capabilities
	g.Destinations = d.Destinations
	g.Denied = d.Denied
	g.ExpiresAt = d.ExpiresAt
	g.Version++
	g.UpdatedAt = s.deps.Clock.Now()
	if err := s.updateGrant(ctx, unit, g); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventGrantUpdated, g.ID, contract.Version(g.Version)); err != nil {
		return contract.Payload{}, err
	}
	return completed(resourceOut[grantOut]{Resource: g.wire()})
}

func (s *Service) updateGrant(ctx context.Context, unit contract.Unit, g grantRow) error {
	scopeJSON, err := encodeScope(g.Scope)
	if err != nil {
		return err
	}
	capsJSON, err := encodeStrings(g.Capabilities)
	if err != nil {
		return err
	}
	destsJSON, err := encodeStrings(g.Destinations)
	if err != nil {
		return err
	}
	var expires any
	if g.ExpiresAt != nil {
		expires = formatStamp(*g.ExpiresAt)
	}
	res, err := unit.ExecContext(ctx, `
		UPDATE identity_grants
		SET version = ?, organization_id = ?, project_id = ?, worker_id = ?, task_id = ?,
		    scope_json = ?, capabilities_json = ?, destinations_json = ?, denied = ?,
		    expires_at = ?, updated_at = ?
		WHERE id = ? AND installation_id = ? AND version = ? AND revoked = 0`,
		g.Version, string(g.Scope.OrganizationID), string(g.Scope.ProjectID),
		string(g.Scope.WorkerID), string(g.Scope.TaskID),
		scopeJSON, capsJSON, destsJSON, boolInt(g.Denied), expires,
		formatStamp(g.UpdatedAt), string(g.ID), string(g.Scope.InstallationID), g.Version-1)
	if err != nil {
		return fmt.Errorf("identity: update grant: %w", err)
	}
	return expectOneRow(res, "grant", g.ID)
}

func (s *Service) grantRevoke(ctx context.Context, unit contract.Unit, in grantRevokeInput) (contract.Payload, error) {
	if _, err := s.authorize(ctx, unit, opGrantRevoke, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	g, found, err := s.loadGrant(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("grant %s is unknown in this installation", in.ID)
	}
	if g.Revoked {
		// Idempotent. Every delegation beneath this grant is already dead:
		// effectiveness requires an intact parent chain.
		return completed(resourceOut[grantOut]{Resource: g.wire()})
	}
	if in.ExpectedVersion != contract.Version(g.Version) {
		return contract.Payload{}, staleVersion(
			"grant %s is at version %d, not the expected %d", in.ID, g.Version, in.ExpectedVersion)
	}
	g.Revoked = true
	g.Version++
	g.UpdatedAt = s.deps.Clock.Now()
	if err := s.updateGrantRevoked(ctx, unit, g); err != nil {
		return contract.Payload{}, err
	}
	if err := s.appendRevocation(ctx, unit, "grant", g.ID, ""); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventGrantRevoked, g.ID, contract.Version(g.Version)); err != nil {
		return contract.Payload{}, err
	}
	return completed(resourceOut[grantOut]{Resource: g.wire()})
}

func (s *Service) updateGrantRevoked(ctx context.Context, unit contract.Unit, g grantRow) error {
	res, err := unit.ExecContext(ctx, `
		UPDATE identity_grants
		SET version = ?, revoked = ?, updated_at = ?
		WHERE id = ? AND installation_id = ? AND version = ? AND revoked = 0`,
		g.Version, boolInt(g.Revoked), formatStamp(g.UpdatedAt),
		string(g.ID), string(g.Scope.InstallationID), g.Version-1)
	if err != nil {
		return fmt.Errorf("identity: revoke grant: %w", err)
	}
	return expectOneRow(res, "grant", g.ID)
}

// loadGrant fetches one grant by id within the transaction scope.
func (s *Service) loadGrant(ctx context.Context, unit contract.Unit, id contract.ID) (grantRow, bool, error) {
	row := unit.QueryRowContext(ctx, `
		SELECT id, version, principal_id, scope_json, capabilities_json, destinations_json,
		       denied, expires_at, parent_grant_id, revoked, created_at, updated_at
		FROM identity_grants
		WHERE id = ? AND installation_id = ?`, id, unit.Scope().InstallationID)
	g, err := scanGrant(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return grantRow{}, false, nil
	}
	if err != nil {
		return grantRow{}, false, fmt.Errorf("identity: load grant: %w", err)
	}
	return g, true, nil
}

// loadCredential fetches one credential by id within the transaction scope.
func (s *Service) loadCredential(ctx context.Context, unit contract.Unit, id contract.ID) (credentialRow, bool, error) {
	row := unit.QueryRowContext(ctx, `
		SELECT id, version, principal_id, store_ref, expires_at, revoked, created_at, updated_at
		FROM identity_credentials
		WHERE id = ? AND installation_id = ?`, id, unit.Scope().InstallationID)
	c, err := scanCredential(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return credentialRow{}, false, nil
	}
	if err != nil {
		return credentialRow{}, false, fmt.Errorf("identity: load credential: %w", err)
	}
	return c, true, nil
}

// sameRef compares two optional grant references.
func sameRef(a, b *contract.ID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
