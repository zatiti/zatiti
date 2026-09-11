package policy

import (
	"context"

	"github.com/zatiti/zatiti/internal/contract"
)

// Public read operations: exact resolution by identity and filtered,
// cursor-paginated lists. Reads never mutate; every handler confines itself
// to the request scope so no cross-scope data can leak.

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
	return " AND (created_at > ? OR (created_at = ? AND id > ?))",
		[]any{formatStamp(created), formatStamp(created), string(lastID)}, nil
}

// pageOf truncates a limit+1 fetch and mints the next cursor when the page
// was full.
func (s *Service) pageOf(op string, unit contract.Unit, filter any, limit int64, rows []policyRow) ([]policyRow, *string, error) {
	var next *string
	if int64(len(rows)) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		cursor, err := s.mintCursor(op, unit.Scope(), filter, last.CreatedAt, last.ID, s.deps.Clock.Now())
		if err != nil {
			return nil, nil, err
		}
		next = &cursor
	}
	return rows, next, nil
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

// policyGet resolves one standing policy by identity.
func (s *Service) policyGet(ctx context.Context, unit contract.Unit, in policyGetInput) (contract.Payload, error) {
	if err := requireMatchingInstallation(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	row, found, err := s.loadPolicyRow(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("policy %s is unknown in this installation", in.ID)
	}
	if !scopeContains(in.Scope, row.Scope) {
		return contract.Payload{}, permissionDenied("policy %s is outside the request scope", in.ID)
	}
	return completed(policyBody{Resource: row.wire()})
}

// policyList pages standing policies; only the organization_id filter
// applies to the resource.
func (s *Service) policyList(ctx context.Context, unit contract.Unit, in listInput) (contract.Payload, error) {
	if err := requireMatchingInstallation(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	if unsupportedFilters(in.Filter, map[string]bool{"organization_id": true}) {
		return contract.Payload{}, invalidInput("policy.list supports only the organization_id filter")
	}
	org := ""
	if in.Filter != nil && in.Filter.OrganizationID != nil {
		org = string(*in.Filter.OrganizationID)
	}
	limit := defaultListLimit
	if in.Limit != nil {
		limit = *in.Limit
	}
	keyset, keysetArgs, err := s.keysetOf(opPolicyList, unit, in.Filter, in.Cursor)
	if err != nil {
		return contract.Payload{}, err
	}
	rows, err := s.listPolicies(ctx, unit, org, keyset, keysetArgs, limit+1)
	if err != nil {
		return contract.Payload{}, err
	}
	rows, next, err := s.pageOf(opPolicyList, unit, in.Filter, limit, rows)
	if err != nil {
		return contract.Payload{}, err
	}
	items := make([]wirePolicy, 0, len(rows))
	for _, r := range rows {
		items = append(items, r.wire())
	}
	return listResult(itemsOut[wirePolicy]{Items: items}, next)
}

// ruleGet resolves one promotion rule by identity.
func (s *Service) ruleGet(ctx context.Context, unit contract.Unit, in ruleGetInput) (contract.Payload, error) {
	if err := requireMatchingInstallation(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	row, found, err := s.loadRuleRow(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("promotion rule %s is unknown in this installation", in.ID)
	}
	if !scopeContains(in.Scope, row.Scope) {
		return contract.Payload{}, permissionDenied("promotion rule %s is outside the request scope", in.ID)
	}
	return completed(ruleBody{Resource: row.wire()})
}

// ruleList pages promotion rules; only the organization_id filter applies to
// the resource.
func (s *Service) ruleList(ctx context.Context, unit contract.Unit, in listInput) (contract.Payload, error) {
	if err := requireMatchingInstallation(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	if unsupportedFilters(in.Filter, map[string]bool{"organization_id": true}) {
		return contract.Payload{}, invalidInput("autonomy.rule.list supports only the organization_id filter")
	}
	org := ""
	if in.Filter != nil && in.Filter.OrganizationID != nil {
		org = string(*in.Filter.OrganizationID)
	}
	limit := defaultListLimit
	if in.Limit != nil {
		limit = *in.Limit
	}
	keyset, keysetArgs, err := s.keysetOf(opRuleList, unit, in.Filter, in.Cursor)
	if err != nil {
		return contract.Payload{}, err
	}
	rows, err := s.listRules(ctx, unit, org, keyset, keysetArgs, limit+1)
	if err != nil {
		return contract.Payload{}, err
	}
	rows, next, err := s.pageRules(opRuleList, unit, in.Filter, limit, rows)
	if err != nil {
		return contract.Payload{}, err
	}
	items := make([]wirePromotionRule, 0, len(rows))
	for _, r := range rows {
		items = append(items, r.wire())
	}
	return listResult(itemsOut[wirePromotionRule]{Items: items}, next)
}

// pageRules is pageOf for promotion-rule rows.
func (s *Service) pageRules(op string, unit contract.Unit, filter any, limit int64, rows []promotionRuleRow) ([]promotionRuleRow, *string, error) {
	var next *string
	if int64(len(rows)) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		cursor, err := s.mintCursor(op, unit.Scope(), filter, last.CreatedAt, last.ID, s.deps.Clock.Now())
		if err != nil {
			return nil, nil, err
		}
		next = &cursor
	}
	return rows, next, nil
}

// qualificationGet resolves one qualification by identity under the request
// scope.
func (s *Service) qualificationGet(ctx context.Context, unit contract.Unit, in qualificationGetInput) (contract.Payload, error) {
	if err := requireMatchingInstallation(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	row, found, err := s.loadQualificationRow(ctx, unit, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if !found {
		return contract.Payload{}, notFound("qualification %s is unknown in this installation", in.ID)
	}
	if !scopeContains(in.Scope, row.Scope) {
		return contract.Payload{}, permissionDenied("qualification %s is outside the request scope", in.ID)
	}
	return completed(qualificationBody{Resource: row.wire()})
}

// qualificationList pages qualifications with structured exact-match
// filters: state, worker_id and organization_id.
func (s *Service) qualificationList(ctx context.Context, unit contract.Unit, in listInput) (contract.Payload, error) {
	if err := requireMatchingInstallation(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	if unsupportedFilters(in.Filter, map[string]bool{"state": true, "worker_id": true, "organization_id": true}) {
		return contract.Payload{}, invalidInput(
			"autonomy.qualification.list supports only the state, worker_id and organization_id filters")
	}
	state, worker, org := "", "", ""
	if in.Filter != nil {
		if in.Filter.State != nil {
			state = *in.Filter.State
			switch state {
			case stateProposed, stateQualified, stateRejected, stateRestricted, stateExpired:
			default:
				return contract.Payload{}, invalidInput("qualification state filter %q is not a known state", state)
			}
		}
		if in.Filter.WorkerID != nil {
			worker = string(*in.Filter.WorkerID)
		}
		if in.Filter.OrganizationID != nil {
			org = string(*in.Filter.OrganizationID)
		}
	}
	limit := defaultListLimit
	if in.Limit != nil {
		limit = *in.Limit
	}
	keyset, keysetArgs, err := s.keysetOf(opAutonomyQualList, unit, in.Filter, in.Cursor)
	if err != nil {
		return contract.Payload{}, err
	}
	rows, err := s.listQualifications(ctx, unit, state, worker, org, keyset, keysetArgs, limit+1)
	if err != nil {
		return contract.Payload{}, err
	}
	rows, next, err := s.pageQualifications(opAutonomyQualList, unit, in.Filter, limit, rows)
	if err != nil {
		return contract.Payload{}, err
	}
	items := make([]wireQualification, 0, len(rows))
	for _, r := range rows {
		items = append(items, r.wire())
	}
	return listResult(itemsOut[wireQualification]{Items: items}, next)
}

// pageQualifications is pageOf for qualification rows.
func (s *Service) pageQualifications(op string, unit contract.Unit, filter any, limit int64, rows []qualificationRow) ([]qualificationRow, *string, error) {
	var next *string
	if int64(len(rows)) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		cursor, err := s.mintCursor(op, unit.Scope(), filter, last.CreatedAt, last.ID, s.deps.Clock.Now())
		if err != nil {
			return nil, nil, err
		}
		next = &cursor
	}
	return rows, next, nil
}
