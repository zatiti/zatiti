package controller

import (
	"context"
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

const fenceReason = "controller generation advanced; leases of earlier generations are fenced"

// call runs one internal operation under the controller's service identity
// and the installation scope. The version is pinned: an owner that no longer
// serves it refuses with capability_unsupported instead of being guessed at.
func (c *Controller) call(ctx context.Context, sess *session, operation string, in, out any) error {
	input, err := json.Marshal(in)
	if err != nil {
		return internalFault("input of %s cannot be encoded", operation)
	}
	payload, err := c.app.Internal(ctx, sess.actor, sess.scope, contract.Invocation{
		Operation: operation,
		Version:   operationVersion,
		Input:     input,
	})
	if err != nil {
		return err
	}
	if payload.Status == contract.StatusFailed {
		if payload.Error != nil {
			return payload.Error
		}
		return internalFault("%s failed without a fault", operation)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(payload.Data, out); err != nil {
		return internalFault("%s returned data the controller cannot decode", operation)
	}
	return nil
}

// start resolves everything one lifetime is bound to, fences the previous
// generation and settles what the previous process left outside a
// transaction. Nothing is admitted before it returns.
func (c *Controller) start(ctx context.Context) (*session, error) {
	c.mu.Lock()
	identity := c.deps.Identity
	c.mu.Unlock()
	if identity.PrincipalID == "" {
		return nil, prerequisiteMissing(
			"no provisioned service identity is attached; the controller cannot make internal calls")
	}
	if !c.own.Held() {
		return nil, unavailable("installation ownership is not held; another controller owns this state directory")
	}
	events, err := c.db.Events(ctx, 0, 1)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 || events[0].Scope.InstallationID == "" {
		return nil, prerequisiteMissing("installation is not initialized; there is nothing to schedule")
	}
	generation, err := c.db.Generation(ctx)
	if err != nil {
		return nil, err
	}
	if generation < 1 {
		return nil, prerequisiteMissing("controller generation was not started before the controller was constructed")
	}
	journal, err := openJournal(c.cfg.StateDir)
	if err != nil {
		return nil, err
	}
	sess := &session{
		actor:      identity,
		scope:      contract.Scope{InstallationID: events[0].Scope.InstallationID},
		generation: generation,
		journal:    journal,
	}
	c.count(func(s *Status) { s.Generation = generation })

	// Fence first: no earlier generation may keep a governed lease while
	// this one admits. A failed fence fails the start closed.
	var fenced attemptIDsOutput
	err = c.write(func() error {
		return c.call(ctx, sess, "_execution.fence", fenceInput{Generation: generation, Reason: fenceReason}, &fenced)
	})
	if err != nil {
		_ = journal.close()
		return nil, err
	}
	if len(fenced.AttemptIDs) > 0 {
		c.log.Info("fenced attempts of earlier generations", "count", len(fenced.AttemptIDs), "generation", generation)
	}

	c.settle(ctx, sess, true)
	if err := journal.compact(); err != nil {
		c.note(err)
	}
	return sess, nil
}

// superseded reports whether a newer generation now owns the installation.
func (c *Controller) superseded(ctx context.Context, sess *session) (bool, error) {
	current, err := c.db.Generation(ctx)
	if err != nil {
		return false, err
	}
	return current != sess.generation, nil
}
