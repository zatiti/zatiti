package reviews

import (
	"context"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Read operations. Every public read authenticates the caller against
// current authority; identity is resolved from credentials, never from a
// claimed profile. Cursors bind the acting principal as well as the query,
// so a cursor minted for one principal is worthless to another.

// defaultListLimit is the page size when the caller sends no limit.
const defaultListLimit = int64(50)

// listOut is the common list result envelope.
type listOut[T any] struct {
	Items []T `json:"items"`
}

// authenticate is the admission gate for public reviews operations: the
// caller must be a registered, unrevoked principal inside the request
// scope. Returns the acting principal for convenience.
func (s *Service) authenticate(ctx context.Context, unit contract.Unit, scope contract.Scope) (contract.ID, error) {
	actor := unit.Actor()
	if actor.PrincipalID == "" {
		return "", permissionDenied("authentication is required")
	}
	auth, err := s.loadAuthority(ctx, unit, actor.PrincipalID, scope)
	if err != nil {
		return "", err
	}
	if auth.Principal.Revoked {
		return "", permissionDenied("caller principal is revoked")
	}
	return actor.PrincipalID, nil
}

// get implements review.get: exact identity and version, or not_found
// without cross-scope disclosure.
func (s *Service) get(ctx context.Context, unit contract.Unit, in wireGetInput) (contract.Payload, error) {
	if err := checkGate(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	if _, err := s.authenticate(ctx, unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	review, err := fetchReviewByID(ctx, unit, unit.Scope().InstallationID, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if review == nil {
		return contract.Payload{}, notFound("review %s not found", in.ID)
	}
	w, err := review.wire()
	if err != nil {
		return contract.Payload{}, err
	}
	return completed(resourceOut[wireReview]{Resource: w})
}

// cursorFilter is the full cursor binding for review.list: the wire filter
// plus the acting principal, so both a filter change and a principal
// change invalidate outstanding cursors.
type cursorFilter struct {
	Filter    *wireListFilter `json:"filter,omitempty"`
	Principal contract.ID     `json:"principal"`
}

// list implements review.list. Filters apply before pagination; the keyset
// is (created_at, id) and a full page mints the next cursor.
func (s *Service) list(ctx context.Context, unit contract.Unit, in wireListInput) (contract.Payload, error) {
	if err := checkGate(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	actor, err := s.authenticate(ctx, unit, in.Scope)
	if err != nil {
		return contract.Payload{}, err
	}
	filter := in.Filter
	if filter != nil && (filter.Key != nil || filter.ParentID != nil || filter.WorkerID != nil ||
		filter.TaskID != nil || filter.OrganizationID != nil || filter.Descendants != nil) {
		return contract.Payload{}, invalidInput("review.list supports only state and needs_you filters")
	}
	if filter != nil && filter.State != nil {
		switch *filter.State {
		case statePending, stateApproved, stateRejected, stateExpired, stateInvalidated:
		default:
			return contract.Payload{}, invalidInput("state filter must be one of pending, approved, rejected, expired, invalidated")
		}
	}
	install := unit.Scope().InstallationID
	binding := cursorFilter{Filter: filter, Principal: actor}

	where := "installation_id = ?"
	args := []any{string(install)}
	if filter != nil {
		if filter.State != nil {
			where += " AND state = ?"
			args = append(args, *filter.State)
		}
		if filter.NeedsYou != nil {
			// needs_you resolves from the reviewer snapshot and the
			// delegation ledger, both keyed by identifier; the acting
			// principal comes from the authenticated caller, never from
			// the request body.
			clause := ` AND (EXISTS (
				SELECT 1 FROM reviews_reviewers rr
				WHERE rr.review_id = reviews_requests.id AND rr.principal_id = ?)
				OR EXISTS (
				SELECT 1 FROM reviews_delegations rd
				WHERE rd.review_id = reviews_requests.id AND rd.delegatee_id = ?))`
			if !*filter.NeedsYou {
				clause = " AND NOT (" + clause[5:] + ")"
			}
			where += clause
			args = append(args, string(actor), string(actor))
		}
	}
	limit := defaultListLimit
	if in.Limit != nil {
		limit = *in.Limit
	}
	var keyset string
	if in.Cursor != nil {
		created, lastID, err := s.readCursor(opList, unit.Scope(), binding, *in.Cursor)
		if err != nil {
			return contract.Payload{}, err
		}
		keyset = " AND (created_at > ? OR (created_at = ? AND id > ?))"
		args = append(args, formatStamp(created), formatStamp(created), string(lastID))
	}
	args = append(args, limit+1)
	rows, err := unit.QueryContext(ctx, `
		SELECT id, version, scope_json, action_digest, preview_json, requirement_json,
		       proposer_id, state, decision_id, created_at, updated_at
		FROM reviews_requests
		WHERE `+where+keyset+`
		ORDER BY created_at, id
		LIMIT ?`, args...)
	if err != nil {
		return contract.Payload{}, fmt.Errorf("reviews: list reviews: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var rowsOut []reviewRow
	for rows.Next() {
		r, err := scanReview(rows.Scan)
		if err != nil {
			return contract.Payload{}, err
		}
		rowsOut = append(rowsOut, r)
	}
	if err := rows.Err(); err != nil {
		return contract.Payload{}, fmt.Errorf("reviews: iterate reviews: %w", err)
	}
	items := make([]wireReview, 0, len(rowsOut))
	for _, r := range rowsOut {
		w, err := r.wire()
		if err != nil {
			return contract.Payload{}, err
		}
		items = append(items, w)
	}
	var next *string
	if int64(len(rowsOut)) > limit {
		rowsOut = rowsOut[:limit]
		items = items[:limit]
		last := rowsOut[len(rowsOut)-1]
		cursor, err := s.mintCursor(opList, unit.Scope(), binding, last.CreatedAt, last.ID, s.deps.Clock.Now())
		if err != nil {
			return contract.Payload{}, err
		}
		next = &cursor
	}
	return listResult(listOut[wireReview]{Items: items}, next)
}
