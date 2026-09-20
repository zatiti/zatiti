package memory

import (
	"context"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// memory.binding.list: keyset-paginated binding queries scoped to one
// installation. state/organization_id/worker_id/task_id are supported exact
// filters; key/parent_id/descendants/needs_you have no analogous binding
// field and are refused rather than silently ignored, mirroring the
// effects package's own list refusal precedent.

const (
	defaultListLimit = int64(50)
	maxListLimit     = int64(200)
)

// bindingListFilter is the normalized filter this operation applies. It is
// also the cursor-binding form: every page must bind the identical
// normalized filter.
type bindingListFilter struct {
	State          string      `json:"state,omitempty"`
	OrganizationID contract.ID `json:"organization_id,omitempty"`
	WorkerID       contract.ID `json:"worker_id,omitempty"`
	TaskID         contract.ID `json:"task_id,omitempty"`
}

var bindingStates = map[string]bool{bindingActive: true, bindingArchiving: true, bindingArchived: true}

func normalizeBindingFilter(f *bindingFilter) (bindingListFilter, error) {
	out := bindingListFilter{}
	if f == nil {
		return out, nil
	}
	if f.Key != "" {
		return out, invalidInput("filter field %q is not supported for memory.binding.list", "key")
	}
	if f.ParentID != "" {
		return out, invalidInput("filter field %q is not supported for memory.binding.list", "parent_id")
	}
	if f.Descendants != nil {
		return out, invalidInput("filter field %q is not supported for memory.binding.list", "descendants")
	}
	if f.NeedsYou != nil {
		return out, invalidInput("filter field %q is not supported for memory.binding.list", "needs_you")
	}
	if f.State != "" {
		if !bindingStates[f.State] {
			return out, invalidInput("filter state %q is not a memory binding state", f.State)
		}
		out.State = f.State
	}
	out.OrganizationID, out.WorkerID, out.TaskID = f.OrganizationID, f.WorkerID, f.TaskID
	return out, nil
}

// bindingListWhere compiles the normalized filter into its WHERE
// conjunction and bind arguments, always scoped to the caller's
// installation. Values are always bound as parameters, never interpolated.
func bindingListWhere(install contract.ID, filter bindingListFilter) (string, []any) {
	conds := []string{"installation_id = ?"}
	args := []any{string(install)}
	if filter.State != "" {
		conds = append(conds, "state = ?")
		args = append(args, filter.State)
	}
	if filter.OrganizationID != "" {
		conds = append(conds, "organization_id = ?")
		args = append(args, string(filter.OrganizationID))
	}
	if filter.WorkerID != "" {
		conds = append(conds, "worker_id = ?")
		args = append(args, string(filter.WorkerID))
	}
	if filter.TaskID != "" {
		conds = append(conds, "task_id = ?")
		args = append(args, string(filter.TaskID))
	}
	return strings.Join(conds, " AND "), args
}

// handleBindingList returns one keyset page of memory bindings. A missing
// cursor starts the first page; the outcome cursor continues after the last
// emitted row.
func (s *Service) handleBindingList(ctx context.Context, unit contract.Unit, in bindingListInput) (contract.Outcome[bindingListOutput], error) {
	if err := s.checkScope(unit, in.Scope); err != nil {
		return contract.Outcome[bindingListOutput]{}, err
	}
	filter, err := normalizeBindingFilter(in.Filter)
	if err != nil {
		return contract.Outcome[bindingListOutput]{}, err
	}
	limit := defaultListLimit
	if in.Limit != nil {
		limit = *in.Limit
		if limit < 1 || limit > maxListLimit {
			return contract.Outcome[bindingListOutput]{}, invalidInput("limit %d must be between 1 and %d", limit, maxListLimit)
		}
	}
	var afterCreated time.Time
	var afterID contract.ID
	if in.Cursor != "" {
		created, id, cerr := s.readCursor(opBindingList, unit.Scope(), filter, in.Cursor)
		if cerr != nil {
			return contract.Outcome[bindingListOutput]{}, cerr
		}
		afterCreated, afterID = created, id
	}
	where, args := bindingListWhere(in.Scope.InstallationID, filter)
	// Fetch one extra row to learn whether a following page exists.
	query := `SELECT ` + bindingColumns + ` FROM memory_bindings WHERE ` + where +
		` AND (created_at > ? OR (created_at = ? AND id > ?)) ORDER BY created_at, id LIMIT ?`
	pageArgs := append(args, formatStamp(afterCreated), formatStamp(afterCreated), string(afterID), limit+1)
	rows, err := unit.QueryContext(ctx, query, pageArgs...)
	if err != nil {
		return contract.Outcome[bindingListOutput]{}, err
	}
	defer func() { _ = rows.Close() }()
	var page []*bindingRow
	for rows.Next() {
		b, err := scanBinding(rows.Scan)
		if err != nil {
			return contract.Outcome[bindingListOutput]{}, err
		}
		page = append(page, b)
	}
	if err := rows.Err(); err != nil {
		return contract.Outcome[bindingListOutput]{}, err
	}
	hasMore := int64(len(page)) > limit
	if hasMore {
		page = page[:limit]
	}
	items := make([]wireMemoryBinding, 0, len(page))
	for _, b := range page {
		items = append(items, renderBinding(b))
	}
	outcome := contract.Outcome[bindingListOutput]{Status: contract.StatusCompleted, Data: bindingListOutput{Items: items}}
	if hasMore && len(page) > 0 {
		last := page[len(page)-1]
		cursor, err := s.mintCursor(opBindingList, unit.Scope(), filter, last.CreatedAt, last.ID, s.now())
		if err != nil {
			return contract.Outcome[bindingListOutput]{}, err
		}
		outcome.NextCursor = &cursor
	}
	return outcome, nil
}

// memory.list: authorized scoped claim refs (source, freshness, lineage) for
// brains the caller can access, entirely from the local cache -- never an
// unauthorized brain and never a paid Serenity retrieval (R15-007/P00-017).
// binding_ids is the sole authorization input: every named id is authorized
// for read exactly as memory.recall authorizes it, so an id the caller
// cannot read fails permission_denied/not_found instead of being silently
// dropped from the page.

// listFilter is the cursor-binding form for memory.list: the exact
// authorized brain set a page was computed against, so a cursor minted for
// one binding_ids selection can never continue a different one.
type listFilter struct {
	BrainIDs []contract.ID `json:"brain_ids"`
}

// handleList authorizes every named binding for read, resolves the distinct
// authorized brain set, and returns one keyset page of the latest cached
// version of every claim in those brains.
func (s *Service) handleList(ctx context.Context, unit contract.Unit, in listInput) (contract.Outcome[listOutput], error) {
	if err := s.checkScope(unit, in.Scope); err != nil {
		return contract.Outcome[listOutput]{}, err
	}
	bindings, err := s.selectBindings(ctx, unit, in.Scope, in.BindingIDs, permRead, time.Time{})
	if err != nil {
		return contract.Outcome[listOutput]{}, err
	}
	seen := map[contract.ID]bool{}
	var brainIDs []contract.ID
	for _, b := range bindings {
		if seen[b.BrainID] {
			continue
		}
		seen[b.BrainID] = true
		brainIDs = append(brainIDs, b.BrainID)
	}
	filter := listFilter{BrainIDs: brainIDs}

	limit := defaultListLimit
	if in.Limit != nil {
		limit = *in.Limit
		if limit < 1 || limit > maxListLimit {
			return contract.Outcome[listOutput]{}, invalidInput("limit %d must be between 1 and %d", limit, maxListLimit)
		}
	}
	var afterRecorded time.Time
	var afterID contract.ID
	if in.Cursor != "" {
		recorded, id, cerr := s.readCursor(opList, unit.Scope(), filter, in.Cursor)
		if cerr != nil {
			return contract.Outcome[listOutput]{}, cerr
		}
		afterRecorded, afterID = recorded, id
	}

	// Fetch one extra row to learn whether a following page exists.
	page, err := listClaimsByBrains(ctx, unit, brainIDs, afterRecorded, afterID, limit+1)
	if err != nil {
		return contract.Outcome[listOutput]{}, err
	}
	hasMore := int64(len(page)) > limit
	if hasMore {
		page = page[:limit]
	}
	items := make([]wireClaim, 0, len(page))
	for _, c := range page {
		items = append(items, renderClaim(c))
	}
	outcome := contract.Outcome[listOutput]{Status: contract.StatusCompleted, Data: listOutput{Items: items}}
	if hasMore && len(page) > 0 {
		last := page[len(page)-1]
		cursor, err := s.mintCursor(opList, unit.Scope(), filter, last.RecordedAt, last.ID, s.now())
		if err != nil {
			return contract.Outcome[listOutput]{}, err
		}
		outcome.NextCursor = &cursor
	}
	return outcome, nil
}
