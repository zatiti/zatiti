// internal/authz/authz.go
//
// Authorization root. Contract: every mutating operation passes through
// Decide before execution; denials are returned as ErrDenied so callers
// journal them (with RFC §8.4 exit 3) rather than improvise. Fail-closed:
// any missing fact denies. Policy enrichment (capability grants, review
// requirements) arrives with T3.x as additional Decision reasons.

package authz

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"zatiti/internal/operations"
)

// ErrDenied is the sentinel denial. Callers map it to exit 3.
var ErrDenied = errors.New("authz: denied")

// Decision records access was granted or refused.
type Decision struct {
	Allow  bool
	Reason string
}

// Authorizer decides operations against persisted membership.
type Authorizer struct {
	db *sql.DB
}

// New builds an Authorizer over the state handle (read-only use).
func New(db *sql.DB) *Authorizer { return &Authorizer{db: db} }

// roleCapabilities: the scaffold policy table. Destructive operations
// require 'owner' — no role below owner may pass, and the table is the
// only place this is encoded.
// OpOverrides: per-operation role restrictions. Presence in this map
// REPLACES the generic mutability check for that operation. Only
// narrowing entries belong here; a widening change is a reviewed
// code change to this table, never a runtime setting.
var OpOverrides = map[string]map[string]bool{
	"credential.issue": {"owner": true},
}

var roleCapabilities = map[string]map[operations.Mutability]bool{
	"owner":    {operations.ReadOnly: true, operations.Mutating: true, operations.Destructive: true},
	"admin":    {operations.ReadOnly: true, operations.Mutating: true},
	"operator": {operations.ReadOnly: true, operations.Mutating: true},
	"auditor":  {operations.ReadOnly: true},
}

// Decide evaluates actor → org → operation. Every branch denies unless
// positively proven otherwise.
func (a *Authorizer) Decide(ctx context.Context, actorID, orgID string, op *operations.Op) (Decision, error) {
	if actorID == "" || orgID == "" || op == nil {
		return Decision{Allow: false, Reason: "missing actor, org, or operation"}, nil
	}
	role, err := a.memberRole(ctx, actorID, orgID)
	if errors.Is(err, errNotMember) {
		return Decision{Allow: false, Reason: "actor is not a member of the organization"}, nil
	}
	if err != nil {
		return Decision{}, fmt.Errorf("authz: membership lookup: %w", err)
	}
	caps, ok := roleCapabilities[role]
	if !ok {
		return Decision{Allow: false, Reason: fmt.Sprintf("unknown role %q", role)}, nil
	}
	if !caps[op.Mutability] {
		return Decision{Allow: false, Reason: fmt.Sprintf(
			"role %q does not permit %s operations", role, mutabilityName(op.Mutability))}, nil
	}
	if override, ok := OpOverrides[op.Name]; ok {
		if !override[role] {
			return Decision{Allow: false, Reason: fmt.Sprintf(
				"operation %q is restricted; role %q is not permitted", op.Name, role)}, nil
		}
	}
	if op.Mutability == operations.Destructive {
		// Destructive operations are authorized by review quorum, not
		// by direct role grant — even owners cannot execute destructively
		// outside the review path. See state.ExecuteReview.
		return Decision{Allow: false,
			Reason: "destructive operations execute only via review quorum (review.submit → review.approve ×2 → review.execute)"}, nil
	}
	return Decision{Allow: true, Reason: fmt.Sprintf("role %q permits %s", role, mutabilityName(op.Mutability))}, nil
}

var errNotMember = errors.New("authz: not a member")

func (a *Authorizer) memberRole(ctx context.Context, actorID, orgID string) (string, error) {
	var role string
	err := a.db.QueryRowContext(ctx, `
		SELECT role FROM memberships WHERE principal_id = ? AND org_id = ?`,
		actorID, orgID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errNotMember
	}
	if err != nil {
		return "", fmt.Errorf("state: membership query: %w", err)
	}
	return role, nil
}

func mutabilityName(m operations.Mutability) string {
	switch m {
	case operations.ReadOnly:
		return "read-only"
	case operations.Mutating:
		return "mutating"
	default:
		return "destructive"
	}
}
