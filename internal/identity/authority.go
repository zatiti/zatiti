package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Effective authority model.
//
// A grant is effective when it is not revoked, not expired, and every grant
// on its parent chain is likewise not revoked and not expired: revoking a
// delegator kills the delegations beneath it. A principal's allow envelope is
// the union of its effective allow grants; effective deny grants subtract
// capabilities from both admission and delegation; active restrictions block
// named capabilities entirely. Authority is evaluated from current stored
// state only, never from a proposed policy or a profile name.

const capWildcard = "*"

// envelope is the union of a principal's effective allow grants, minus
// explicit denials and restrictions. It is both the admission check and the
// ceiling that delegated definitions must fit inside.
type envelope struct {
	orgs           map[string]bool
	orgWildcard    bool
	projects       map[string]bool
	projWildcard   bool
	workers        map[string]bool
	workerWildcard bool
	tasks          map[string]bool
	taskWildcard   bool
	caps           map[string]bool
	capWildcard    bool
	dests          map[string]bool
	destWildcard   bool
	denied         map[string]bool
	deniedWildcard bool
	expires        *time.Time // earliest expiry over contributing grants; nil = unbounded
}

func newEnvelope() *envelope {
	return &envelope{
		orgs:     map[string]bool{},
		projects: map[string]bool{},
		workers:  map[string]bool{},
		tasks:    map[string]bool{},
		caps:     map[string]bool{},
		dests:    map[string]bool{},
		denied:   map[string]bool{},
	}
}

// add merges one effective allow grant into the envelope.
func (e *envelope) add(g grantRow) {
	if g.Scope.OrganizationID == "" {
		e.orgWildcard = true
	} else {
		e.orgs[string(g.Scope.OrganizationID)] = true
	}
	if g.Scope.ProjectID == "" {
		e.projWildcard = true
	} else {
		e.projects[string(g.Scope.ProjectID)] = true
	}
	if g.Scope.WorkerID == "" {
		e.workerWildcard = true
	} else {
		e.workers[string(g.Scope.WorkerID)] = true
	}
	if g.Scope.TaskID == "" {
		e.taskWildcard = true
	} else {
		e.tasks[string(g.Scope.TaskID)] = true
	}
	for _, c := range g.Capabilities {
		if c == capWildcard {
			e.capWildcard = true
			continue
		}
		e.caps[c] = true
	}
	if len(g.Destinations) == 0 {
		e.destWildcard = true
	}
	for _, d := range g.Destinations {
		e.dests[d] = true
	}
	if g.ExpiresAt != nil {
		if e.expires == nil || g.ExpiresAt.Before(*e.expires) {
			t := *g.ExpiresAt
			e.expires = &t
		}
	}
}

// deny merges one effective deny grant covering the request scope.
func (e *envelope) deny(g grantRow) {
	for _, c := range g.Capabilities {
		if c == capWildcard {
			e.deniedWildcard = true
			continue
		}
		e.denied[c] = true
	}
}

// restrict marks one capability as blocked by an active restriction.
func (e *envelope) restrict(capability string) {
	e.denied[capability] = true
}

// coversScope reports whether the scope is inside the envelope. A wildcard
// or unconstrained envelope dimension covers everything; a set envelope
// dimension requires an exact match, so an org-constrained envelope does not
// authorize installation-wide requests.
func (e *envelope) coversScope(s contract.Scope) bool {
	return dimCover(e.orgs, e.orgWildcard, s.OrganizationID) &&
		dimCover(e.projects, e.projWildcard, s.ProjectID) &&
		dimCover(e.workers, e.workerWildcard, s.WorkerID) &&
		dimCover(e.tasks, e.taskWildcard, s.TaskID)
}

func dimCover(values map[string]bool, wildcard bool, want contract.ID) bool {
	if wildcard {
		return true
	}
	if want == "" {
		return false
	}
	return values[string(want)]
}

// hasCap reports whether the envelope carries the capability and nothing
// denies it.
func (e *envelope) hasCap(capability string) bool {
	if e.deniedWildcard || e.denied[capability] {
		return false
	}
	return e.capWildcard || e.caps[capability]
}

// allowsCaps reports whether a child grant's capability set fits the
// envelope. Explicit denials always win.
func (e *envelope) allowsCaps(caps []string) bool {
	for _, c := range caps {
		if c == capWildcard {
			if !e.capWildcard || e.deniedWildcard {
				return false
			}
			continue
		}
		if e.denied[c] {
			return false
		}
		if !e.capWildcard && !e.caps[c] {
			return false
		}
	}
	return true
}

// allowsDests reports whether a child grant's destinations fit. An empty
// destination list is unconstrained and requires an unconstrained envelope.
func (e *envelope) allowsDests(dests []string) bool {
	if len(dests) == 0 {
		return e.destWildcard
	}
	for _, d := range dests {
		if !e.destWildcard && !e.dests[d] {
			return false
		}
	}
	return true
}

// allowsExpiry reports whether a child grant's deadline fits the envelope's
// earliest expiry. An unbounded child requires an unbounded envelope:
// delegation only narrows deadlines.
func (e *envelope) allowsExpiry(t *time.Time) bool {
	if t == nil {
		return e.expires == nil
	}
	return e.expires == nil || !t.After(*e.expires)
}

// expired reports whether the grant stopped being effective at or before now.
func (g grantRow) expired(now time.Time) bool {
	return g.ExpiresAt != nil && !now.Before(*g.ExpiresAt)
}

// deniesCap reports whether this deny grant blocks the capability.
func (g grantRow) deniesCap(capability string) bool {
	if !g.Denied {
		return false
	}
	for _, c := range g.Capabilities {
		if c == capWildcard || c == capability {
			return true
		}
	}
	return false
}

// deniesRequest reports whether this deny grant covers the request scope. A
// deny grant's scope dimension matches when it is unset (wildcard) or equal
// to the request dimension; an unset request dimension under a set deny
// dimension is still covered, so targeted denials cannot be dodged by
// omitting scope.
func (g grantRow) deniesRequest(capability string, requestScope contract.Scope) bool {
	if !g.deniesCap(capability) {
		return false
	}
	return dimDeny(g.Scope.InstallationID, requestScope.InstallationID) &&
		dimDeny(g.Scope.OrganizationID, requestScope.OrganizationID) &&
		dimDeny(g.Scope.ProjectID, requestScope.ProjectID) &&
		dimDeny(g.Scope.WorkerID, requestScope.WorkerID) &&
		dimDeny(g.Scope.TaskID, requestScope.TaskID)
}

func dimDeny(have, want contract.ID) bool {
	return have == "" || want == "" || have == want
}

// effectiveGrants computes the non-revoked, unexpired grants for a principal
// whose whole parent chain is alive. The parent chain crosses principals — a
// delegation's parent grant is held by the delegator — so the chain index
// covers every live grant in the installation. Broken parent chains (missing,
// revoked or expired ancestors) fail closed.
func (s *Service) effectiveGrants(ctx context.Context, unit contract.Unit, principalID contract.ID, now time.Time) ([]grantRow, error) {
	rows, err := unit.QueryContext(ctx, `
		SELECT id, version, principal_id, scope_json, capabilities_json, destinations_json,
		       denied, expires_at, parent_grant_id, revoked, created_at, updated_at
		FROM identity_grants
		WHERE installation_id = ? AND revoked = 0
		ORDER BY id`, unit.Scope().InstallationID)
	if err != nil {
		return nil, fmt.Errorf("identity: list grants for authority: %w", err)
	}
	defer func() { _ = rows.Close() }()

	byID := map[contract.ID]grantRow{}
	var order []contract.ID
	for rows.Next() {
		r, err := scanGrant(rows.Scan)
		if err != nil {
			return nil, err
		}
		byID[r.ID] = r
		order = append(order, r.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("identity: iterate grants: %w", err)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })

	var effective []grantRow
	for _, id := range order {
		g := byID[id]
		if g.PrincipalID != principalID {
			continue
		}
		if !grantChainAlive(byID, g, now, 0) {
			continue
		}
		effective = append(effective, g)
	}
	return effective, nil
}

// grantChainAlive walks the parent chain of g checking every ancestor is
// present, unrevoked and unexpired. depth guards against corrupted cycles.
func grantChainAlive(byID map[contract.ID]grantRow, g grantRow, now time.Time, depth int) bool {
	if depth > 64 {
		return false
	}
	if g.ExpiresAt != nil && !now.Before(*g.ExpiresAt) {
		return false
	}
	if g.ParentGrantID == nil {
		return true
	}
	parent, ok := byID[*g.ParentGrantID]
	if !ok {
		return false
	}
	return grantChainAlive(byID, parent, now, depth+1)
}

// authorize is the admission gate for every public operation. It loads the
// caller's current state, computes the effective envelope, and checks the
// request scope, capability, explicit denials and active restrictions. The
// returned envelope is the ceiling delegated definitions must fit inside.
func (s *Service) authorize(ctx context.Context, unit contract.Unit, capability string, requestScope contract.Scope) (*envelope, error) {
	if requestScope.InstallationID != unit.Scope().InstallationID {
		return nil, invalidInput("request scope installation %s does not match the transaction scope", requestScope.InstallationID)
	}
	actor := unit.Actor()
	if actor.PrincipalID == "" {
		return nil, permissionDenied("authentication is required")
	}
	caller, found, err := s.loadPrincipal(ctx, unit, actor.PrincipalID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, permissionDenied("caller principal is not registered in this installation")
	}
	if caller.Revoked {
		return nil, permissionDenied("caller principal is revoked")
	}
	now := s.deps.Clock.Now()

	grants, err := s.effectiveGrants(ctx, unit, actor.PrincipalID, now)
	if err != nil {
		return nil, err
	}
	env := newEnvelope()
	for _, g := range grants {
		if g.Denied {
			if !g.expired(now) && g.deniesRequest(capability, requestScope) {
				env.deny(g)
			}
			continue
		}
		env.add(g)
	}
	if !env.coversScope(requestScope) {
		return nil, permissionDenied("request scope escapes the caller's granted envelope")
	}
	if !env.hasCap(capability) {
		return nil, permissionDenied("missing capability %q", capability)
	}
	restricted, err := s.activeRestrictions(ctx, unit, actor.PrincipalID)
	if err != nil {
		return nil, err
	}
	for _, c := range restricted {
		env.restrict(c)
	}
	if !env.hasCap(capability) {
		return nil, permissionDenied("a restriction blocks capability %q", capability)
	}
	return env, nil
}

// activeRestrictions lists the capability names currently restricted for a
// principal in this installation.
func (s *Service) activeRestrictions(ctx context.Context, unit contract.Unit, principalID contract.ID) ([]string, error) {
	rows, err := unit.QueryContext(ctx, `
		SELECT capability FROM identity_restrictions
		WHERE principal_id = ? AND installation_id = ? AND active = 1
		ORDER BY capability`, principalID, unit.Scope().InstallationID)
	if err != nil {
		return nil, fmt.Errorf("identity: list restrictions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, fmt.Errorf("identity: scan restriction: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("identity: iterate restrictions: %w", err)
	}
	return out, nil
}

// loadPrincipal fetches one principal by id within the transaction scope.
// found is false when the id is unknown in this installation.
func (s *Service) loadPrincipal(ctx context.Context, unit contract.Unit, id contract.ID) (principalRow, bool, error) {
	row := unit.QueryRowContext(ctx, `
		SELECT id, version, kind, name, scope_json, revoked, created_at, updated_at
		FROM identity_principals
		WHERE id = ? AND installation_id = ?`, id, unit.Scope().InstallationID)
	p, err := scanPrincipal(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return principalRow{}, false, nil
	}
	if err != nil {
		return principalRow{}, false, fmt.Errorf("identity: load principal: %w", err)
	}
	return p, true, nil
}
