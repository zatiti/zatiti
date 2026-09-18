package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/zatiti/zatiti/internal/contract"
)

const adapterEvidenceSchema = "zatiti.controller.adapter-return/v1"

// perform is the one physical invocation of one claimed attempt. It runs
// outside every transaction. Whatever the adapter returns — an observation,
// an error, a panic — becomes exactly one journaled observation, then one
// record. There is no path from here back to Invoke.
func (c *Controller) perform(workCtx context.Context, sess *session, e entry, d contract.Dispatch, adapter contract.Adapter) {
	defer c.workers.Done()
	defer c.free()
	defer c.release(e.ID)

	callCtx := workCtx
	if !d.Deadline.IsZero() {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(workCtx, d.Deadline.Sub(c.now()))
		defer cancel()
	}
	c.count(func(s *Status) { s.Invocations++ })
	obs := c.observe(callCtx, adapter, d, e.Bound)

	e.Observation = &obs
	e.Phase = phaseObserved
	if !c.journal(sess, e) {
		// Abandoned or displaced: the claimed marker stays the last durable
		// phase and the next generation records the outcome as unknown.
		return
	}
	c.settleEffect(workCtx, sess, e, false)
}

// observe invokes the adapter once and returns a schema-valid observation.
// After a claim, an error or a malformed return cannot prove the provider
// did nothing, so both are kept as unknown; the controller never reports a
// cancellation or a failure the adapter did not establish.
func (c *Controller) observe(ctx context.Context, adapter contract.Adapter, d contract.Dispatch, bound *wireMoney) (obs contract.Observation) {
	defer func() {
		if recover() != nil {
			obs = c.unestablished(bound, "adapter_panic", "the adapter panicked during the call")
		}
	}()
	got, err := adapter.Invoke(ctx, d)
	if err != nil {
		code := "adapter_error"
		var f *contract.Fault
		if errors.As(err, &f) && f != nil {
			code = f.Code
		}
		return c.unestablished(bound, code, "the adapter returned an error instead of an observation")
	}
	switch got.Disposition {
	case contract.DispositionSucceeded, contract.DispositionFailed, contract.DispositionAccepted,
		contract.DispositionUnknown, contract.DispositionNotSent:
	default:
		return c.unestablished(bound, "invalid_disposition", "the adapter returned an unsupported disposition")
	}
	if !jsonObject(got.Evidence) {
		got.Evidence = adapterReturn("missing_evidence", "the adapter returned no structured evidence")
	}
	if !validUsage(got.Usage) {
		// An absent usage report is unknown advisory billing, never free.
		got.Usage = c.synthesize(contract.DispositionUnknown, bound, "", "unknown").Usage
	}
	return got
}

// unestablished is the observation for a call whose result the adapter did
// not establish.
func (c *Controller) unestablished(bound *wireMoney, code, reason string) contract.Observation {
	obs := c.synthesize(contract.DispositionUnknown, bound, reason, "unknown")
	obs.Evidence = adapterReturn(code, reason)
	return obs
}

func adapterReturn(code, reason string) json.RawMessage {
	raw, _ := json.Marshal(struct {
		Schema string `json:"schema"`
		Code   string `json:"code"`
		Reason string `json:"reason"`
	}{adapterEvidenceSchema, code, reason})
	return raw
}

func jsonObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 1 && trimmed[0] == '{' && json.Valid(trimmed)
}

// validUsage checks a usage report against $defs/Usage.
func validUsage(raw json.RawMessage) bool {
	if !jsonObject(raw) {
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return false
	}
	for _, name := range []string{"currency", "spent", "reserved", "estimated", "unknown", "advisory"} {
		if _, ok := fields[name]; !ok {
			return false
		}
	}
	if len(fields) != 6 {
		return false
	}
	var u wireUsage
	if err := json.Unmarshal(raw, &u); err != nil {
		return false
	}
	return validCurrency(u.Currency) && u.Spent >= 0 && u.Reserved >= 0 && u.Estimated >= 0 && u.Unknown >= 0
}
