package identity

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Principal operations: create, get, list, update, revoke. Kind is immutable,
// revoked principals cannot be un-revoked, and revocation flips the flag,
// appends an immutable revocation record and emits an event in one
// transaction. Every operation runs under the caller's current authority;
// a profile name or kind confers nothing.

// resourceOut wraps the common single-resource result envelope.
type resourceOut[T any] struct {
	Resource T `json:"resource"`
}

// itemsOut is the common list result envelope.
type itemsOut[T any] struct {
	Items []T `json:"items"`
}

// versionsOut is the activate result envelope.
type versionsOut struct {
	Versions []refOut `json:"versions"`
}

const defaultListLimit = int64(50)

func (s *Service) principalCreate(ctx context.Context, unit contract.Unit, in principalCreateInput) (contract.Payload, error) {
	env, err := s.authorize(ctx, unit, opPrincipalCreate, in.Scope)
	if err != nil {
		return contract.Payload{}, err
	}
	d := in.Definition
	if d.Revoked {
		return contract.Payload{}, invalidInput("a principal cannot be created revoked; revoke it after creation so the revocation is recorded")
	}
	if d.Scope.InstallationID != unit.Scope().InstallationID {
		return contract.Payload{}, invalidInput("principal scope installation does not match the transaction scope")
	}
	if !env.coversScope(d.Scope) {
		return contract.Payload{}, permissionDenied("the new principal's scope escapes the caller's granted envelope")
	}
	if err := s.assertNameUnique(ctx, unit, d.Name); err != nil {
		return contract.Payload{}, err
	}
	now := s.deps.Clock.Now()
	p := principalRow{
		ID:        contract.ID(s.deps.IDs.New()),
		Version:   1,
		Kind:      d.Kind,
		Name:      d.Name,
		Scope:     d.Scope,
		Revoked:   false,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.insertPrincipal(ctx, unit, p); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventPrincipalCreated, p.ID, 1); err != nil {
		return contract.Payload{}, err
	}
	return completed(resourceOut[principalOut]{Resource: p.wire()})
}

// assertNameUnique rejects reusing an active principal's name inside one
// installation. A revoked principal's name stays reserved: names are profile
// identifiers, never authority, but reuse would confuse audit trails.
func (s *Service) assertNameUnique(ctx context.Context, unit contract.Unit, name string) error {
	var n int64
	err := unit.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM identity_principals
		WHERE installation_id = ? AND name = ?`, unit.Scope().InstallationID, name).Scan(&n)
	if err != nil {
		return fmt.Errorf("identity: check principal name: %w", err)
	}
	if n > 0 {
		return conflict("a principal with this name already exists in the installation")
	}
	return nil
}

func (s *Service) insertPrincipal(ctx context.Context, unit contract.Unit, p principalRow) error {
	scopeJSON, err := encodeScope(p.Scope)
	if err != nil {
		return err
	}
	_, err = unit.ExecContext(ctx, `
		INSERT INTO identity_principals
			(id, version, kind, name, installation_id, organization_id, project_id,
			 worker_id, task_id, scope_json, revoked, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(p.ID), p.Version, p.Kind, p.Name,
		string(p.Scope.InstallationID), string(p.Scope.OrganizationID),
		string(p.Scope.ProjectID), string(p.Scope.WorkerID), string(p.Scope.TaskID),
		scopeJSON, boolInt(p.Revoked), formatStamp(p.CreatedAt), formatStamp(p.UpdatedAt))
	if err != nil {
		return fmt.Errorf("identity: insert principal: %w", err)
	}
	return nil
}

func (s *Service) principalGet(ctx context.Context, unit contract.Unit, in principalGetInput) (contract.Payload, error) {
	if _, err := s.authorize(ctx, unit, opPrincipalGet, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	p, found, err := s.loadPrincipal(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("principal %s is unknown in this installation", in.ID)
	}
	return completed(resourceOut[principalOut]{Resource: p.wire()})
}

func (s *Service) principalList(ctx context.Context, unit contract.Unit, in listInput) (contract.Payload, error) {
	if _, err := s.authorize(ctx, unit, opPrincipalList, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	filter := in.Filter
	if filter != nil && (filter.ParentID != nil || filter.Descendants != nil || filter.NeedsYou != nil) {
		return contract.Payload{}, invalidInput("principal.list supports only state, key, organization_id, worker_id and task_id filters")
	}
	if filter != nil && filter.State != nil {
		switch *filter.State {
		case "active", "revoked":
		default:
			return contract.Payload{}, invalidInput("principal.list state filter must be \"active\" or \"revoked\"")
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
		if filter.Key != nil {
			where += " AND name = ?"
			args = append(args, *filter.Key)
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
		created, lastID, err := s.readCursor(opPrincipalList, unit.Scope(), filter, *in.Cursor)
		if err != nil {
			return contract.Payload{}, err
		}
		keyset = " AND (created_at > ? OR (created_at = ? AND id > ?))"
		args = append(args, formatStamp(created), formatStamp(created), string(lastID))
	}
	args = append(args, limit+1)
	rows, err := unit.QueryContext(ctx, `
		SELECT id, version, kind, name, scope_json, revoked, created_at, updated_at
		FROM identity_principals
		WHERE `+where+keyset+`
		ORDER BY created_at, id
		LIMIT ?`, args...)
	if err != nil {
		return contract.Payload{}, fmt.Errorf("identity: list principals: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var rowsOut []principalRow
	for rows.Next() {
		r, err := scanPrincipal(rows.Scan)
		if err != nil {
			return contract.Payload{}, err
		}
		rowsOut = append(rowsOut, r)
	}
	if err := rows.Err(); err != nil {
		return contract.Payload{}, fmt.Errorf("identity: iterate principals: %w", err)
	}
	items := make([]principalOut, 0, len(rowsOut))
	for _, r := range rowsOut {
		items = append(items, r.wire())
	}
	var next *string
	if int64(len(rowsOut)) > limit {
		rowsOut = rowsOut[:limit]
		items = items[:limit]
		last := rowsOut[len(rowsOut)-1]
		cursor, err := s.mintCursor(opPrincipalList, unit.Scope(), filter, last.CreatedAt, last.ID, s.deps.Clock.Now())
		if err != nil {
			return contract.Payload{}, err
		}
		next = &cursor
	}
	return listResult(itemsOut[principalOut]{Items: items}, next)
}

func (s *Service) principalUpdate(ctx context.Context, unit contract.Unit, in principalUpdateInput) (contract.Payload, error) {
	env, err := s.authorize(ctx, unit, opPrincipalUpdate, in.Scope)
	if err != nil {
		return contract.Payload{}, err
	}
	p, found, err := s.loadPrincipal(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("principal %s is unknown in this installation", in.ID)
	}
	if in.ExpectedVersion != contract.Version(p.Version) {
		return contract.Payload{}, staleVersion(
			"principal %s is at version %d, not the expected %d", in.ID, p.Version, in.ExpectedVersion)
	}
	d := in.Definition
	if d.Kind != p.Kind {
		return contract.Payload{}, invalidInput("principal kind is immutable")
	}
	if d.Revoked != p.Revoked {
		return contract.Payload{}, conflict("revocation state changes only through principal.revoke; revoked principals cannot be un-revoked")
	}
	if d.Scope.InstallationID != unit.Scope().InstallationID {
		return contract.Payload{}, invalidInput("principal scope installation does not match the transaction scope")
	}
	if !env.coversScope(d.Scope) {
		return contract.Payload{}, permissionDenied("the updated principal's scope escapes the caller's granted envelope")
	}
	if d.Name != p.Name {
		if err := s.assertNameUnique(ctx, unit, d.Name); err != nil {
			return contract.Payload{}, err
		}
	}
	p.Version++
	p.Name = d.Name
	p.Scope = d.Scope
	p.UpdatedAt = s.deps.Clock.Now()
	if err := s.updatePrincipal(ctx, unit, p); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventPrincipalUpdated, p.ID, contract.Version(p.Version)); err != nil {
		return contract.Payload{}, err
	}
	return completed(resourceOut[principalOut]{Resource: p.wire()})
}

func (s *Service) updatePrincipal(ctx context.Context, unit contract.Unit, p principalRow) error {
	scopeJSON, err := encodeScope(p.Scope)
	if err != nil {
		return err
	}
	res, err := unit.ExecContext(ctx, `
		UPDATE identity_principals
		SET version = ?, name = ?, organization_id = ?, project_id = ?, worker_id = ?,
		    task_id = ?, scope_json = ?, revoked = ?, updated_at = ?
		WHERE id = ? AND installation_id = ? AND version = ?`,
		p.Version, p.Name, string(p.Scope.OrganizationID), string(p.Scope.ProjectID),
		string(p.Scope.WorkerID), string(p.Scope.TaskID), scopeJSON, boolInt(p.Revoked), formatStamp(p.UpdatedAt),
		string(p.ID), string(p.Scope.InstallationID), p.Version-1)
	if err != nil {
		return fmt.Errorf("identity: update principal: %w", err)
	}
	return expectOneRow(res, "principal", p.ID)
}

func (s *Service) principalRevoke(ctx context.Context, unit contract.Unit, in principalRevokeInput) (contract.Payload, error) {
	if _, err := s.authorize(ctx, unit, opPrincipalRevoke, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	p, found, err := s.loadPrincipal(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("principal %s is unknown in this installation", in.ID)
	}
	if p.Revoked {
		// Idempotent: revoke of an already-revoked principal returns the
		// current state. Revocation records are never removed, so a restore
		// of an older backup cannot resurrect this principal's authority.
		return completed(resourceOut[principalOut]{Resource: p.wire()})
	}
	if in.ExpectedVersion != contract.Version(p.Version) {
		return contract.Payload{}, staleVersion(
			"principal %s is at version %d, not the expected %d", in.ID, p.Version, in.ExpectedVersion)
	}
	p.Revoked = true
	p.Version++
	p.UpdatedAt = s.deps.Clock.Now()
	if err := s.updatePrincipal(ctx, unit, p); err != nil {
		return contract.Payload{}, err
	}
	if err := s.appendRevocation(ctx, unit, "principal", p.ID, ""); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventPrincipalRevoked, p.ID, contract.Version(p.Version)); err != nil {
		return contract.Payload{}, err
	}
	return completed(resourceOut[principalOut]{Resource: p.wire()})
}

// appendRevocation records an immutable revocation row. The table has no
// update or delete path anywhere in this package.
func (s *Service) appendRevocation(ctx context.Context, unit contract.Unit, entityKind string, entityID contract.ID, reason string) error {
	_, err := unit.ExecContext(ctx, `
		INSERT INTO identity_revocations (id, entity_kind, entity_id, reason, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		s.deps.IDs.New(), entityKind, string(entityID), reason,
		formatStamp(s.deps.Clock.Now()))
	if err != nil {
		return fmt.Errorf("identity: record revocation: %w", err)
	}
	return nil
}

// expectOneRow converts a no-op optimistic update into stale_version so a
// lost race surfaces as the right fault instead of silent success.
func expectOneRow(res sql.Result, entity string, id contract.ID) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("identity: update %s: %w", entity, err)
	}
	if n == 0 {
		return staleVersion("%s %s changed concurrently", entity, id)
	}
	return nil
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
