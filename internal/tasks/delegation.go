package tasks

import (
	"context"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Delegation narrowing. A child inherits an intersected contract: every
// finite dimension takes the minimum of what the child requests and what
// the parent holds, the deadline is contained within the parent's, scope
// dimensions cannot widen, and the root's budgets stay shared through the
// root task id. Expansion is refused.

// narrowScope checks that the child scope does not widen the parent scope.
// Every dimension present on the child must be present and equal on the
// parent; the installation must match exactly.
func narrowScope(parent, child wireScope) error {
	if child.InstallationID != parent.InstallationID {
		return invalidInput("delegation cannot cross installations")
	}
	if child.OrganizationID != "" && child.OrganizationID != parent.OrganizationID {
		return invalidInput("delegation cannot widen the organization scope")
	}
	if child.ProjectID != "" && child.ProjectID != parent.ProjectID {
		return invalidInput("delegation cannot widen the project data scope")
	}
	if child.WorkerID != "" && child.WorkerID != parent.WorkerID {
		return invalidInput("delegation cannot widen the worker scope")
	}
	return nil
}

// narrowLimits intersects the child's requested limits with the parent's.
// Numeric dimensions take the minimum; a currency mismatch is an expansion
// attempt and is refused; the deadline is contained within the parent's.
func narrowLimits(parent, child wireLimits) (wireLimits, error) {
	if child.Currency != parent.Currency {
		return wireLimits{}, invalidInput("delegation currency %q does not match the parent currency %q",
			child.Currency, parent.Currency)
	}
	if child.SpendMicroUnits > parent.SpendMicroUnits {
		return wireLimits{}, invalidInput("delegation spend limit %d exceeds the parent limit %d",
			child.SpendMicroUnits, parent.SpendMicroUnits)
	}
	narrowed := wireLimits{
		Currency:        child.Currency,
		SpendMicroUnits: child.SpendMicroUnits,
		Concurrency:     min64(child.Concurrency, parent.Concurrency),
		ModelSteps:      min64(child.ModelSteps, parent.ModelSteps),
		ChildCount:      min64(child.ChildCount, parent.ChildCount),
		DelegationDepth: min64(child.DelegationDepth, parent.DelegationDepth),
		AttemptSeconds:  min64(child.AttemptSeconds, parent.AttemptSeconds),
	}
	childDeadline, err := parseDeadline(child.RootDeadline)
	if err != nil {
		return wireLimits{}, err
	}
	parentDeadline, err := parseDeadline(parent.RootDeadline)
	if err != nil {
		return wireLimits{}, err
	}
	if childDeadline.After(parentDeadline) {
		narrowed.RootDeadline = parentDeadline.Format(timeLayout)
	} else {
		narrowed.RootDeadline = childDeadline.Format(timeLayout)
	}
	return narrowed, nil
}

// parseDeadline parses a stored or wire RFC3339 deadline.
func parseDeadline(s string) (time.Time, error) {
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}, invalidInput("root_deadline %q is not a valid RFC3339 timestamp", s)
	}
	return t, nil
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// checkDepthAndCount enforces the finite tree: the parent must still have
// delegation depth and child count available. depth remaining counts the
// edges below the parent; a parent at depth 0 cannot delegate at all.
func checkDepthAndCount(ctx context.Context, unit contract.Unit, parent *taskRow) error {
	parentLimits, err := parent.decodeLimits()
	if err != nil {
		return err
	}
	if parentLimits.DelegationDepth < 1 {
		return invalidInput("delegation depth limit exhausted; the parent cannot delegate")
	}
	if parentLimits.ChildCount < 1 {
		return invalidInput("child count limit exhausted; the parent cannot accept another child")
	}
	count, err := countChildren(ctx, unit, parent.ID)
	if err != nil {
		return err
	}
	if count >= parentLimits.ChildCount {
		return invalidInput("child count limit %d reached for parent %s", parentLimits.ChildCount, parent.ID)
	}
	return nil
}

// narrowedChildLimits returns the limits the child must actually store:
// the requested limits already intersected, with one level of delegation
// depth consumed from the parent's remaining depth.
func narrowedChildLimits(parent *taskRow, requested wireLimits) (wireLimits, error) {
	parentLimits, err := parent.decodeLimits()
	if err != nil {
		return wireLimits{}, err
	}
	narrowed, err := narrowLimits(parentLimits, requested)
	if err != nil {
		return wireLimits{}, err
	}
	// The child holds at most parent depth minus the edge already used.
	narrowed.DelegationDepth = min64(narrowed.DelegationDepth, parentLimits.DelegationDepth-1)
	if narrowed.DelegationDepth < 0 {
		return wireLimits{}, invalidInput("delegation depth limit exhausted; the parent cannot delegate")
	}
	return narrowed, nil
}
