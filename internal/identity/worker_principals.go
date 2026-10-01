package identity

import (
	"context"
	"encoding/json"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Worker principals (revision 21). contract.WorkerOperator resolves a
// worker's own actor through identity, so every configured worker needs a
// live principal of kind worker whose id is its worker id. Identity creates
// that principal itself -- from a configuration candidate it is activating,
// or from _identity.worker.sync for the bootstrap chief and for backfill --
// and never from a caller-described grant: the only standing authority a
// worker principal ever receives here is workerStandingGrant, the frozen
// worker-visible allowlist.
//
// Monotonic by design, like every other revocation in this package:
//   - an existing worker principal is never re-granted, so an owner who
//     revoked or narrowed its grant keeps it that way across every sync;
//   - a revoked worker principal is never reactivated;
//   - re-sync only moves the principal's recorded organization to the
//     worker's current one.

// workerPrincipalNamePrefix makes the reserved principal name of a worker
// unique per worker id, so a worker's display name never collides with a
// human's and name uniqueness can never block registration by accident.
const workerPrincipalNamePrefix = "worker:"

// workerPrincipalInput mirrors $defs/WorkerPrincipal.
type workerPrincipalInput struct {
	WorkerID       contract.ID `json:"worker_id"`
	OrganizationID contract.ID `json:"organization_id"`
	Active         bool        `json:"active"`
}

type workerSyncInput struct {
	Workers []workerPrincipalInput `json:"workers"`
}

// candidateWorkerChange is the subset of a configuration Change identity
// reads for a worker: its identity, action and, for create/update, the
// organization the worker belongs to.
type candidateWorkerChange struct {
	Kind       string          `json:"kind"`
	Action     string          `json:"action"`
	ID         contract.ID     `json:"id"`
	Definition json.RawMessage `json:"definition"`
}

type candidateWorkerDefinition struct {
	OrganizationID contract.ID `json:"organization_id"`
}

// workerSync implements _identity.worker.sync.
func (s *Service) workerSync(ctx context.Context, unit contract.Unit, in workerSyncInput) (contract.Payload, error) {
	versions := []refOut{}
	for _, w := range in.Workers {
		ref, changed, err := s.applyWorkerPrincipal(ctx, unit, w)
		if err != nil {
			return contract.Payload{}, err
		}
		if changed {
			versions = append(versions, ref)
		}
	}
	return completed(versionsOut{Versions: versions})
}

// activateWorkerChanges applies the worker changes of a configuration
// candidate being activated in the compiler's transaction.
func (s *Service) activateWorkerChanges(ctx context.Context, unit contract.Unit, c candidate) ([]refOut, error) {
	versions := []refOut{}
	for _, raw := range c.Changes {
		var ch candidateWorkerChange
		if err := json.Unmarshal(raw, &ch); err != nil {
			return nil, invalidInput("candidate change does not decode: %v", err)
		}
		if ch.Kind != "worker" {
			continue
		}
		w := workerPrincipalInput{WorkerID: ch.ID}
		switch ch.Action {
		case "create", "update":
			var def candidateWorkerDefinition
			if err := json.Unmarshal(ch.Definition, &def); err != nil {
				return nil, invalidInput("worker %s definition does not decode: %v", ch.ID, err)
			}
			w.OrganizationID = def.OrganizationID
			w.Active = true
		case "archive", "delete":
			w.Active = false
		default:
			return nil, invalidInput("worker %s change has unknown action %q", ch.ID, ch.Action)
		}
		ref, changed, err := s.applyWorkerPrincipal(ctx, unit, w)
		if err != nil {
			return nil, err
		}
		if changed {
			versions = append(versions, ref)
		}
	}
	return versions, nil
}

// applyWorkerPrincipal registers, moves or retires one worker principal.
func (s *Service) applyWorkerPrincipal(ctx context.Context, unit contract.Unit, w workerPrincipalInput) (refOut, bool, error) {
	if w.WorkerID == "" {
		return refOut{}, false, invalidInput("worker principal requires a worker id")
	}
	if w.Active && w.OrganizationID == "" {
		return refOut{}, false, invalidInput("active worker %s requires an organization", w.WorkerID)
	}
	p, found, err := s.loadPrincipal(ctx, unit, w.WorkerID)
	if err != nil {
		return refOut{}, false, err
	}
	if found && p.Kind != contract.KindWorker {
		return refOut{}, false, conflict("principal %s exists and is not a worker", w.WorkerID)
	}
	switch {
	case !found && !w.Active:
		return refOut{}, false, nil
	case !found:
		return s.registerWorkerPrincipal(ctx, unit, w)
	case p.Revoked:
		// Never reactivate: a revoked worker principal stays revoked.
		return refOut{}, false, nil
	case !w.Active:
		return s.retireWorkerPrincipal(ctx, unit, p)
	case p.Scope.OrganizationID != w.OrganizationID:
		return s.moveWorkerPrincipal(ctx, unit, p, w.OrganizationID)
	default:
		return refOut{}, false, nil
	}
}

func (s *Service) registerWorkerPrincipal(ctx context.Context, unit contract.Unit, w workerPrincipalInput) (refOut, bool, error) {
	install := unit.Scope().InstallationID
	scope := contract.Scope{InstallationID: install, OrganizationID: w.OrganizationID}
	name := workerPrincipalNamePrefix + string(w.WorkerID)
	if err := s.assertNameUnique(ctx, unit, name); err != nil {
		return refOut{}, false, err
	}
	now := s.deps.Clock.Now()
	p := principalRow{
		ID: w.WorkerID, Version: 1, Kind: contract.KindWorker, Name: name, Scope: scope,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.insertPrincipal(ctx, unit, p); err != nil {
		return refOut{}, false, err
	}
	// The grant is installation-scoped, not organization-scoped: a worker's
	// conversations and message turns (the owner's chat with the chief, for
	// one) are commonly installation-scoped, and an organization-scoped
	// allow grant never covers a request that omits the organization. What a
	// worker may reach is still narrowed on every call by WorkerOperator's
	// intersection with the admitting task/source envelope, by policy's
	// worker-binding and task-scope fences, and by each owner's own rules
	// (conversation membership, for one).
	g := workerStandingGrant(contract.ID(s.deps.IDs.New()), p.ID, contract.Scope{InstallationID: install}, now)
	if err := s.insertGrant(ctx, unit, g); err != nil {
		return refOut{}, false, err
	}
	if err := emitTransition(ctx, unit, eventPrincipalCreated, p.ID, 1); err != nil {
		return refOut{}, false, err
	}
	if err := emitTransition(ctx, unit, eventGrantCreated, g.ID, 1); err != nil {
		return refOut{}, false, err
	}
	return refOut{ID: p.ID, Version: 1}, true, nil
}

func (s *Service) retireWorkerPrincipal(ctx context.Context, unit contract.Unit, p principalRow) (refOut, bool, error) {
	p.Revoked = true
	p.Version++
	p.UpdatedAt = s.deps.Clock.Now()
	if err := s.updatePrincipal(ctx, unit, p); err != nil {
		return refOut{}, false, err
	}
	if err := s.appendRevocation(ctx, unit, "principal", p.ID, "worker archived or deleted"); err != nil {
		return refOut{}, false, err
	}
	if err := emitTransition(ctx, unit, eventPrincipalRevoked, p.ID, contract.Version(p.Version)); err != nil {
		return refOut{}, false, err
	}
	return refOut{ID: p.ID, Version: contract.Version(p.Version)}, true, nil
}

// moveWorkerPrincipal follows worker.move: the principal's recorded
// organization moves with the worker. Its grants are left exactly as they
// are -- the standing grant is installation-scoped, and any grant the owner
// attached is the owner's to change.
func (s *Service) moveWorkerPrincipal(ctx context.Context, unit contract.Unit, p principalRow, org contract.ID) (refOut, bool, error) {
	p.Scope.OrganizationID = org
	p.Version++
	p.UpdatedAt = s.deps.Clock.Now()
	if err := s.updatePrincipal(ctx, unit, p); err != nil {
		return refOut{}, false, err
	}
	if err := emitTransition(ctx, unit, eventPrincipalUpdated, p.ID, contract.Version(p.Version)); err != nil {
		return refOut{}, false, err
	}
	return refOut{ID: p.ID, Version: contract.Version(p.Version)}, true, nil
}

// workerStandingGrant is the one grant identity ever issues to a worker
// principal: the frozen worker-visible allowlist, installation-scoped,
// allow, with no destination list (external destinations still require an
// explicit tool binding for a worker), no expiry and no parent.
func workerStandingGrant(id, principal contract.ID, scope contract.Scope, now time.Time) grantRow {
	return grantRow{
		ID: id, Version: 1, PrincipalID: principal, Scope: scope,
		Capabilities: workerStandingCapabilities(), Destinations: []string{},
		CreatedAt: now, UpdatedAt: now,
	}
}

// workerDisclosureCapability is messaging's delivery capability
// (internal/messaging, disclosureCapability): conversation.message.send and
// mailbox.send both ask policy for it before any body is disclosed, so a
// worker allowed to send on the allowlist needs it to send at all.
const workerDisclosureCapability = "messaging.disclosure.deliver"

// workerStandingCapabilities is the standing grant's exact capability set:
// the worker-visible allowlist plus message delivery.
func workerStandingCapabilities() []string {
	return append(contract.WorkerVisibleOperations(), workerDisclosureCapability)
}
