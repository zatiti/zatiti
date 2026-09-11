package accounting

import (
	"context"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Effective limits. A budget definition binds by identity: the budget whose
// id equals an installation/organization/project/worker id is the budget of
// that object at its natural level. The effective chain reserves
// installation, ancestor organizations from root down, project, worker and
// root task in stable order; each level's caps narrow the effective limits,
// and per-level positions enforce the shared aggregate caps across all
// children. Unconfigured currency is the XXX sentinel and the zero time is
// the unset root deadline; the shipped defaults refuse paid execution until
// an installation explicitly selects a currency and a finite spend ceiling.

const unconfiguredCurrency = "XXX"

// Shipped defaults: one concurrent attempt per worker, four per
// installation, a 30-minute attempt, 100 model steps, eight children,
// delegation depth three, and a 24-hour root deadline.
const (
	defaultInstallationConcurrency = int64(4)
	defaultWorkerConcurrency       = int64(1)
	defaultWorkerModelSteps        = int64(100)
	defaultWorkerAttemptSeconds    = int64(1800)
	defaultRootChildCount          = int64(8)
	defaultRootDelegationDepth     = int64(3)
	defaultRootDeadline            = 24 * time.Hour
)

// positionKind enumerates the dimensions a position aggregates.
type positionKind string

const (
	posInstallation positionKind = "installation"
	posOrganization positionKind = "organization"
	posProject      positionKind = "project"
	posWorker       positionKind = "worker"
	posRootTask     positionKind = "root_task"
)

// levelCaps are the enforceable caps configured at one chain level. Nil
// fields mean the level declares no cap for that dimension; spend limits are
// never negative, so a present zero spend cap refuses paid work at that
// level.
type levelCaps struct {
	currency        string
	spend           *int64
	concurrency     *int64
	modelSteps      *int64
	childCount      *int64
	delegationDepth *int64
	attemptSeconds  *int64
	rootDeadline    *time.Time
}

// mergeLimits narrows the caps with the limits declared by one wire document
// (a budget, a definition's limits or a caller's claimed root limits). A
// declared non-XXX currency must agree with every other declared currency;
// a mismatch is a caller-visible input failure.
func (c *levelCaps) mergeLimits(l *wireLimits) error {
	if l == nil {
		return nil
	}
	if l.Currency != unconfiguredCurrency {
		if c.currency == "" {
			c.currency = l.Currency
		} else if c.currency != l.Currency {
			return invalidInput("currency mismatch: %s is configured elsewhere, %s was declared", c.currency, l.Currency)
		}
	}
	if l.SpendMicroUnits >= 0 {
		c.spend = minPtr(c.spend, l.SpendMicroUnits)
	}
	if l.Concurrency > 0 {
		c.concurrency = minPtr(c.concurrency, l.Concurrency)
	}
	if l.ModelSteps > 0 {
		c.modelSteps = minPtr(c.modelSteps, l.ModelSteps)
	}
	if l.ChildCount >= 0 {
		c.childCount = minPtr(c.childCount, l.ChildCount)
	}
	if l.DelegationDepth >= 0 {
		c.delegationDepth = minPtr(c.delegationDepth, l.DelegationDepth)
	}
	if l.AttemptSeconds > 0 {
		c.attemptSeconds = minPtr(c.attemptSeconds, l.AttemptSeconds)
	}
	if !l.RootDeadline.IsZero() && (c.rootDeadline == nil || l.RootDeadline.Before(*c.rootDeadline)) {
		deadline := l.RootDeadline
		c.rootDeadline = &deadline
	}
	return nil
}

// mergeCaps narrows the caps with another level's configured caps.
func (c *levelCaps) mergeCaps(o *levelCaps) error {
	if o == nil {
		return nil
	}
	if o.currency != "" {
		if c.currency == "" {
			c.currency = o.currency
		} else if c.currency != o.currency {
			return invalidInput("currency mismatch: %s is configured elsewhere, %s was declared", c.currency, o.currency)
		}
	}
	c.spend = minPtr2(c.spend, o.spend)
	c.concurrency = minPtr2(c.concurrency, o.concurrency)
	c.modelSteps = minPtr2(c.modelSteps, o.modelSteps)
	c.childCount = minPtr2(c.childCount, o.childCount)
	c.delegationDepth = minPtr2(c.delegationDepth, o.delegationDepth)
	c.attemptSeconds = minPtr2(c.attemptSeconds, o.attemptSeconds)
	if o.rootDeadline != nil && (c.rootDeadline == nil || o.rootDeadline.Before(*c.rootDeadline)) {
		c.rootDeadline = o.rootDeadline
	}
	return nil
}

// minPtr narrows a present cap with one declared value.
func minPtr(cur *int64, v int64) *int64 {
	if cur == nil || v < *cur {
		return &v
	}
	return cur
}

// minPtr narrows a present cap with another present cap.
func minPtr2(cur, other *int64) *int64 {
	if other == nil {
		return cur
	}
	return minPtr(cur, *other)
}

// deref returns the value of a present cap, or zero when absent.
func deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// level is one dimension of the reservation order with its configured caps.
type level struct {
	kind positionKind
	ref  contract.ID
	caps levelCaps
}

// budgetRow is one stored budget definition.
type budgetRow struct {
	ID         contract.ID
	Version    int64
	InstallID  contract.ID
	LimitsJSON string
	State      string
}

// loadBudget returns the active budget bound to id, or nil when the
// installation has no active budget for that identity.
func (s *Service) loadBudget(ctx context.Context, unit contract.Unit, install, id contract.ID) (*budgetRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT id, version, limits_json, state FROM accounting_budgets
		WHERE installation_id = ? AND id = ? AND state = 'active'`, string(install), string(id))
	var b budgetRow
	var limitsJSON string
	err := row.Scan(&b.ID, &b.Version, &limitsJSON, &b.State)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b.InstallID = install
	b.LimitsJSON = limitsJSON
	return &b, nil
}

// budgetLimits decodes a stored canonical limits document; a nil budget has
// no limits.
func budgetLimits(b *budgetRow) (*wireLimits, error) {
	if b == nil {
		return nil, nil
	}
	var l wireLimits
	if err := contract.DecodeStrict([]byte(b.LimitsJSON), &l); err != nil {
		return nil, invalidInput("stored budget %s does not decode as limits", b.ID)
	}
	return &l, nil
}

// buildLevels assembles the reservation order from the snapshot and the
// active budgets: installation, ancestor organizations from root down,
// project and worker. Scope dimensions named by the caller must exist in the
// snapshot; a named but absent object is an input failure, never an
// implicit skip. The root-task level is appended by reserve from the
// caller-declared limits.
func (s *Service) buildLevels(ctx context.Context, unit contract.Unit, scope wireScope, snap *snapshotScope) ([]level, error) {
	// Installation: the stored budget, or the shipped installation default
	// when no budget exists. The default refuses paid work until a currency
	// and a finite spend limit are selected; an explicit budget always
	// replaces the default instead of narrowing it.
	inst := level{kind: posInstallation, ref: scope.InstallationID}
	b, err := s.loadBudget(ctx, unit, scope.InstallationID, scope.InstallationID)
	if err != nil {
		return nil, err
	}
	if b != nil {
		l, err := budgetLimits(b)
		if err != nil {
			return nil, err
		}
		if err := inst.caps.mergeLimits(l); err != nil {
			return nil, err
		}
	} else if err := inst.caps.mergeCaps(installationDefaultCaps()); err != nil {
		return nil, err
	}
	levels := make([]level, 0, 8)
	levels = append(levels, inst)

	// Ancestor organizations, root down. The snapshot walks nearest first,
	// so the chain reverses it. A bare installation scope carries no
	// snapshot at all; a named organization must still be reachable.
	var ancestors []snapshotOrg
	if snap != nil {
		ancestors = snap.Ancestors
	}
	if scope.OrganizationID != "" && len(ancestors) == 0 {
		return nil, invalidInput("organization %s does not exist in this installation", scope.OrganizationID)
	}
	for i := len(ancestors) - 1; i >= 0; i-- {
		org := ancestors[i]
		lvl := level{kind: posOrganization, ref: org.ID}
		if err := lvl.caps.mergeLimits(org.Limits); err != nil {
			return nil, err
		}
		b, err := s.loadBudget(ctx, unit, scope.InstallationID, org.ID)
		if err != nil {
			return nil, err
		}
		if l, err := budgetLimits(b); err != nil {
			return nil, err
		} else if err := lvl.caps.mergeLimits(l); err != nil {
			return nil, err
		}
		levels = append(levels, lvl)
	}

	if scope.ProjectID != "" {
		if snap.Project == nil || snap.Project.ID != scope.ProjectID {
			return nil, invalidInput("project %s does not exist in this installation", scope.ProjectID)
		}
		if scope.OrganizationID != "" && snap.Project.OrganizationID != scope.OrganizationID {
			return nil, invalidInput("project %s does not belong to organization %s", scope.ProjectID, scope.OrganizationID)
		}
		lvl := level{kind: posProject, ref: snap.Project.ID}
		if err := lvl.caps.mergeLimits(snap.Project.Limits); err != nil {
			return nil, err
		}
		b, err := s.loadBudget(ctx, unit, scope.InstallationID, snap.Project.ID)
		if err != nil {
			return nil, err
		}
		if l, err := budgetLimits(b); err != nil {
			return nil, err
		} else if err := lvl.caps.mergeLimits(l); err != nil {
			return nil, err
		}
		levels = append(levels, lvl)
	}

	if scope.WorkerID != "" {
		if snap.Worker == nil || snap.Worker.ID != scope.WorkerID {
			return nil, invalidInput("worker %s does not exist in this installation", scope.WorkerID)
		}
		if scope.OrganizationID != "" && snap.Worker.OrganizationID != scope.OrganizationID {
			return nil, invalidInput("worker %s does not belong to organization %s", scope.WorkerID, scope.OrganizationID)
		}
		lvl := level{kind: posWorker, ref: snap.Worker.ID}
		if err := lvl.caps.mergeLimits(snap.Worker.Limits); err != nil {
			return nil, err
		}
		b, err := s.loadBudget(ctx, unit, scope.InstallationID, snap.Worker.ID)
		if err != nil {
			return nil, err
		}
		if l, err := budgetLimits(b); err != nil {
			return nil, err
		} else if err := lvl.caps.mergeLimits(l); err != nil {
			return nil, err
		}
		// The shipped worker defaults bound every field the worker's own
		// sources leave unconfigured; defaults only fill absent caps and
		// never tighten an explicit one.
		fillWorkerDefaults(&lvl.caps)
		levels = append(levels, lvl)
	}
	return levels, nil
}

// rootLevel builds the root-task level from the caller-declared limits. A
// task effect shares its root task's aggregate position with every sibling;
// administrative effects have no task dimension and skip it.
func rootLevel(rootTaskID contract.ID, caller *wireLimits) (level, error) {
	lvl := level{kind: posRootTask, ref: rootTaskID}
	if err := lvl.caps.mergeLimits(caller); err != nil {
		return level{}, err
	}
	return lvl, nil
}

// installationDefaultCaps are the shipped installation defaults: concurrency
// four and a zero spend ceiling that refuses paid execution until the
// installation explicitly selects a currency and a finite spend limit.
func installationDefaultCaps() *levelCaps {
	zero := int64(0)
	concurrency := defaultInstallationConcurrency
	return &levelCaps{
		spend:       &zero,
		concurrency: &concurrency,
	}
}

// fillWorkerDefaults fills the worker concurrency, model-step and
// attempt-seconds caps the worker's own sources leave unconfigured.
func fillWorkerDefaults(c *levelCaps) {
	if c.concurrency == nil {
		v := defaultWorkerConcurrency
		c.concurrency = &v
	}
	if c.modelSteps == nil {
		v := defaultWorkerModelSteps
		c.modelSteps = &v
	}
	if c.attemptSeconds == nil {
		v := defaultWorkerAttemptSeconds
		c.attemptSeconds = &v
	}
}

// fillRootDefaults fills the root-task child, delegation-depth and deadline
// caps the caller and configured budgets leave undeclared. A zero caller
// declaration for these three fields is read as undeclared; a zero spend
// declaration is a real zero and refuses paid work.
func fillRootDefaults(c *levelCaps, now time.Time) {
	if c.childCount == nil {
		v := defaultRootChildCount
		c.childCount = &v
	}
	if c.delegationDepth == nil {
		v := defaultRootDelegationDepth
		c.delegationDepth = &v
	}
	if c.rootDeadline == nil {
		v := now.Add(defaultRootDeadline)
		c.rootDeadline = &v
	}
}

// effectiveLimits intersects every level's caps into the one wire document
// the operations report or seal into a reservation. Absent caps fall back to
// the shipped defaults; the root deadline fallback is the zero time for pure
// limit views and now plus the default deadline for reservations.
func effectiveLimits(levels []level, deadlineFallback time.Time) (*wireLimits, error) {
	var c levelCaps
	for i := range levels {
		if err := c.mergeCaps(&levels[i].caps); err != nil {
			return nil, err
		}
	}
	out := &wireLimits{Currency: unconfiguredCurrency}
	if c.currency != "" {
		out.Currency = c.currency
	}
	out.SpendMicroUnits = deref(c.spend)
	if c.concurrency == nil {
		out.Concurrency = defaultInstallationConcurrency
	} else {
		out.Concurrency = deref(c.concurrency)
	}
	if c.modelSteps == nil {
		out.ModelSteps = defaultWorkerModelSteps
	} else {
		out.ModelSteps = deref(c.modelSteps)
	}
	if c.attemptSeconds == nil {
		out.AttemptSeconds = defaultWorkerAttemptSeconds
	} else {
		out.AttemptSeconds = deref(c.attemptSeconds)
	}
	if c.childCount == nil {
		out.ChildCount = defaultRootChildCount
	} else {
		out.ChildCount = deref(c.childCount)
	}
	if c.delegationDepth == nil {
		out.DelegationDepth = defaultRootDelegationDepth
	} else {
		out.DelegationDepth = deref(c.delegationDepth)
	}
	if c.rootDeadline != nil {
		out.RootDeadline = *c.rootDeadline
	} else {
		out.RootDeadline = deadlineFallback
	}
	return out, nil
}
