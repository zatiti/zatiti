package effects

import (
	"context"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// operation.list: keyset-paginated operation queries. One page reads at most
// limit+1 rows, truncates, and mints an HMAC-bound keyset cursor on
// (created_at, id); a mismatched, tampered or foreign cursor is invalid_input
// and an expired one is cursor_expired. The descendants and needs_you filter
// fields are declared in the wire contract but refused here until an owner
// provides the traversal they require.

const (
	defaultListLimit = int64(50)
	maxListLimit     = int64(200)
)

// listFilter is the normalized filter this operation applies. It is also the
// cursor-binding form: every page must bind the identical normalized filter.
type listFilter struct {
	State          string      `json:"state,omitempty"`
	Key            string      `json:"key,omitempty"`
	ParentID       contract.ID `json:"parent_id,omitempty"`
	WorkerID       contract.ID `json:"worker_id,omitempty"`
	TaskID         contract.ID `json:"task_id,omitempty"`
	OrganizationID contract.ID `json:"organization_id,omitempty"`
}

// operationStates are the eleven logical operation states (R10-003); the
// state filter validates against exactly this set.
var operationStates = map[string]bool{
	opStatePrepared:             true,
	opStateAwaitingReview:       true,
	opStateReady:                true,
	opStateExecuting:            true,
	opStateAwaitingConfirmation: true,
	opStateOutcomeUnknown:       true,
	opStateSucceeded:            true,
	opStateFailed:               true,
	opStateDenied:               true,
	opStateExpired:              true,
	opStateCancelled:            true,
}

// normalizeListFilter validates and flattens the wire filter into its
// normalized form.
func normalizeListFilter(f *operationFilter) (listFilter, error) {
	out := listFilter{}
	if f == nil {
		return out, nil
	}
	if f.Descendants != nil {
		return out, invalidInput("filter field %q is not supported for operation.list", "descendants")
	}
	if f.NeedsYou != nil {
		return out, invalidInput("filter field %q is not supported for operation.list", "needs_you")
	}
	if f.State != nil {
		if !operationStates[*f.State] {
			return out, invalidInput("filter state %q is not an operation state", *f.State)
		}
		out.State = *f.State
	}
	if f.Key != nil {
		out.Key = *f.Key
	}
	if f.ParentID != nil {
		out.ParentID = *f.ParentID
	}
	if f.WorkerID != nil {
		out.WorkerID = *f.WorkerID
	}
	if f.TaskID != nil {
		out.TaskID = *f.TaskID
	}
	if f.OrganizationID != nil {
		out.OrganizationID = *f.OrganizationID
	}
	return out, nil
}

// listWhere compiles the normalized filter into its WHERE conjunction and
// bind arguments, always scoped to the caller's installation.
func listWhere(install contract.ID, filter listFilter) (string, []any) {
	conds := []string{"installation_id = ?"}
	args := []any{string(install)}
	if filter.State != "" {
		conds = append(conds, "state = ?")
		args = append(args, filter.State)
	}
	if filter.Key != "" {
		conds = append(conds, "source_key = ?")
		args = append(args, filter.Key)
	}
	if filter.ParentID != "" {
		conds = append(conds, "linked_operation_id = ?")
		args = append(args, string(filter.ParentID))
	}
	if filter.WorkerID != "" {
		conds = append(conds, "worker_id = ?")
		args = append(args, string(filter.WorkerID))
	}
	if filter.TaskID != "" {
		conds = append(conds, "task_id = ?")
		args = append(args, string(filter.TaskID))
	}
	if filter.OrganizationID != "" {
		conds = append(conds, "organization_id = ?")
		args = append(args, string(filter.OrganizationID))
	}
	return strings.Join(conds, " AND "), args
}

// operation.list returns one keyset page of operations. A missing cursor
// starts the first page; the outcome cursor continues after the last emitted
// row.
func (s *Service) handleList(ctx context.Context, unit contract.Unit, in listOperationsInput) (contract.Outcome[operationListOutput], error) {
	if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[operationListOutput]{}, err
	}
	filter, err := normalizeListFilter(in.Filter)
	if err != nil {
		return contract.Outcome[operationListOutput]{}, err
	}
	limit := defaultListLimit
	if in.Limit != nil {
		limit = *in.Limit
		if limit < 1 || limit > maxListLimit {
			return contract.Outcome[operationListOutput]{}, invalidInput(
				"limit %d must be between 1 and %d", limit, maxListLimit)
		}
	}
	var afterCreated time.Time
	var afterID contract.ID
	if in.Cursor != nil && *in.Cursor != "" {
		created, id, cerr := s.readCursor(opList, unit.Scope(), filter, *in.Cursor)
		if cerr != nil {
			return contract.Outcome[operationListOutput]{}, cerr
		}
		afterCreated = created
		afterID = id
	}
	where, args := listWhere(unit.Scope().InstallationID, filter)
	// Fetch one extra row to learn whether a following page exists.
	query := `SELECT ` + operationColumns + ` FROM effects_operations
		WHERE ` + where + ` AND (created_at > ? OR (created_at = ? AND id > ?))
		ORDER BY created_at, id LIMIT ?`
	pageArgs := append(args, formatStamp(afterCreated), formatStamp(afterCreated), string(afterID), limit+1)
	rows, err := unit.QueryContext(ctx, query, pageArgs...)
	if err != nil {
		return contract.Outcome[operationListOutput]{}, err
	}
	defer func() { _ = rows.Close() }()
	var page []*operationRow
	for rows.Next() {
		o, err := scanOperation(rows.Scan)
		if err != nil {
			return contract.Outcome[operationListOutput]{}, err
		}
		page = append(page, o)
	}
	if err := rows.Err(); err != nil {
		return contract.Outcome[operationListOutput]{}, err
	}
	hasMore := int64(len(page)) > limit
	if hasMore {
		page = page[:limit]
	}
	items := make([]wireOperation, 0, len(page))
	for _, o := range page {
		wire, err := renderOperation(ctx, unit, o)
		if err != nil {
			return contract.Outcome[operationListOutput]{}, err
		}
		items = append(items, wire)
	}
	outcome := contract.Outcome[operationListOutput]{
		Status: contract.StatusCompleted,
		Data:   operationListOutput{Items: items},
	}
	if hasMore && len(page) > 0 {
		last := page[len(page)-1]
		cursor, err := s.mintCursor(opList, unit.Scope(), filter, last.CreatedAt, last.ID, s.now())
		if err != nil {
			return contract.Outcome[operationListOutput]{}, err
		}
		outcome.NextCursor = &cursor
	}
	return outcome, nil
}
