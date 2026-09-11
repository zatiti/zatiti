package scheduling

import (
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Public list plumbing shared by schedule.list and responsibility.list:
// structured exact-match filter checking, HMAC keyset continuation and the
// limit+1 page fetch that mints the next cursor when the page was full.

// defaultListLimit is the page size when the caller does not bound it.
const defaultListLimit = int64(50)

// itemsOut is the common list result envelope.
type itemsOut[T any] struct {
	Items []T `json:"items"`
}

// scopeContains reports whether the request scope covers a resource scope:
// a dimension the request leaves broad covers every value; a dimension the
// request narrows must match exactly.
func scopeContains(req, have contract.Scope) bool {
	if have.OrganizationID != "" && req.OrganizationID != "" && req.OrganizationID != have.OrganizationID {
		return false
	}
	if have.ProjectID != "" && req.ProjectID != "" && req.ProjectID != have.ProjectID {
		return false
	}
	if have.WorkerID != "" && req.WorkerID != "" && req.WorkerID != have.WorkerID {
		return false
	}
	if have.TaskID != "" && req.TaskID != "" && req.TaskID != have.TaskID {
		return false
	}
	return true
}

// keysetOf validates the cursor and returns the SQL keyset fragment and
// arguments bound to it.
func (s *Service) keysetOf(op string, unit contract.Unit, filter any, raw *string) (string, []any, error) {
	if raw == nil || *raw == "" {
		return "", nil, nil
	}
	created, lastID, err := s.readCursor(op, unit.Scope(), filter, *raw)
	if err != nil {
		return "", nil, err
	}
	cond, args := keysetCond(created, lastID)
	return cond, args, nil
}

// unsupportedFilters reports whether any filter outside the allowed names is
// present on a list request.
func unsupportedFilters(f *listFilter, allowed map[string]bool) bool {
	if f == nil {
		return false
	}
	for name, present := range map[string]bool{
		"state":           f.State != nil,
		"key":             f.Key != nil,
		"parent_id":       f.ParentID != nil,
		"worker_id":       f.WorkerID != nil,
		"task_id":         f.TaskID != nil,
		"organization_id": f.OrganizationID != nil,
		"descendants":     f.Descendants != nil,
		"needs_you":       f.NeedsYou != nil,
	} {
		if present && !allowed[name] {
			return true
		}
	}
	return false
}

// pageSchedules truncates a limit+1 schedule fetch and mints the next cursor
// when the page was full.
func (s *Service) pageSchedules(op string, unit contract.Unit, filter any, limit int64, rows []scheduleRow, now time.Time) ([]scheduleRow, *string, error) {
	var next *string
	if int64(len(rows)) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		cursor, err := s.mintCursor(op, unit.Scope(), filter, last.CreatedAt, last.ID, now)
		if err != nil {
			return nil, nil, err
		}
		next = &cursor
	}
	return rows, next, nil
}

// pageResponsibilities is pageSchedules for responsibility rows.
func (s *Service) pageResponsibilities(op string, unit contract.Unit, filter any, limit int64, rows []responsibilityRow, now time.Time) ([]responsibilityRow, *string, error) {
	var next *string
	if int64(len(rows)) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		cursor, err := s.mintCursor(op, unit.Scope(), filter, last.CreatedAt, last.ID, now)
		if err != nil {
			return nil, nil, err
		}
		next = &cursor
	}
	return rows, next, nil
}

// listWhere compiles the shared list conditions: rows must live in the unit
// scope's installation and the request filters apply as exact matches.
func listWhere(unit contract.Unit) (conds []string, args []any) {
	conds = append(conds, `installation_id = ?`)
	args = append(args, unit.Scope().InstallationID)
	return conds, args
}
