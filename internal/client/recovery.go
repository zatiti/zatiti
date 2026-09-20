package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// defaultPollAttempts bounds Poll when the caller supplies no MaxAttempts:
// enough to observe a slow-completing accepted job without ever polling
// unboundedly.
const defaultPollAttempts = 30

// PollOptions bounds one Poll call. MaxAttempts caps the number of Call
// invocations a non-positive value defaults to defaultPollAttempts. Interval
// is the wait between attempts while the last result stayed accepted; zero
// polls again immediately, bounded only by MaxAttempts and ctx. The caller's
// context deadline or cancellation always ends Poll immediately regardless
// of these bounds.
type PollOptions struct {
	MaxAttempts int
	Interval    time.Duration
}

// Poll repeatedly calls a read-only query operation until its result is no
// longer accepted, the bounded attempts in opts run out, or ctx ends. req
// must be a query: Poll refuses a keyed request outright, sending nothing,
// because resending a mutation to "check on it" is never safe. Recovering a
// mutation's own disposition is Lookup's job; a caller that wants to poll an
// already-accepted job to completion does so through an ordinary query
// operation naming the accepted reference (for example job.get with the
// accepted job_id), which Poll simply repeats until it stops being
// accepted. A completed or failed result is returned immediately exactly as
// Call would return it, including a failed result's fault as Poll's own
// error; Poll adds no retry beyond Call's own bounded connection-retry
// policy and never resends anything to make an accepted job progress.
func (c *Client) Poll(ctx context.Context, operation string, req contract.Request, opts PollOptions) (contract.Result, error) {
	if req.SubmissionKey != "" {
		return contract.Result{}, fmt.Errorf("%w: Poll accepts only a read-only query (no submission key); a mutation must never be resent by polling", ErrInvalidRequest)
	}
	attempts := opts.MaxAttempts
	if attempts <= 0 {
		attempts = defaultPollAttempts
	}
	var last contract.Result
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return last, err
		}
		res, err := c.Call(ctx, operation, req)
		if err != nil {
			return res, err
		}
		last = res
		if res.Status != contract.StatusAccepted {
			return res, nil
		}
		if attempt == attempts {
			break
		}
		if opts.Interval > 0 {
			if err := sleepBackoff(ctx, opts.Interval); err != nil {
				return last, err
			}
		}
	}
	return last, fmt.Errorf("%w: %d attempts, last status %q", ErrPollExhausted, attempts, last.Status)
}

// Lookup performs the frozen command.get recovery query directly: given the
// original submission's operation, scope and submission key, it returns the
// retained disposition of a command that already committed, without
// resending the mutation and without needing the original request bytes —
// which a caller recovering after a restart (a fresh CLI process, a
// reconnecting desktop or MCP session) may no longer have. found is false
// when no command holds that identity: the mutation never committed, and a
// fresh identical submission under the SAME key is licensed, never a new
// one. Lookup is itself a query: it carries no submission key of its own,
// mints no durable identity and performs no replay of its own beyond
// ordinary connection establishment.
func (c *Client) Lookup(ctx context.Context, operation string, scope json.RawMessage, submissionKey string) (result contract.Result, found bool, err error) {
	if err := validateOperationID(operation); err != nil {
		return contract.Result{}, false, err
	}
	if err := validateSubmissionKey(submissionKey); err != nil {
		return contract.Result{}, false, err
	}
	if trimmed := strings.TrimSpace(string(scope)); !strings.HasPrefix(trimmed, "{") {
		return contract.Result{}, false, fmt.Errorf("%w: scope must be a JSON object", ErrInvalidRequest)
	}
	if err := contract.DecodeStrict(scope, new(json.RawMessage)); err != nil {
		return contract.Result{}, false, fmt.Errorf("%w: scope is not strict JSON: %v", ErrInvalidRequest, err)
	}
	input, err := json.Marshal(struct {
		Scope json.RawMessage `json:"scope"`
	}{scope})
	if err != nil {
		return contract.Result{}, false, fmt.Errorf("%w: scope cannot be encoded: %v", ErrInvalidRequest, err)
	}
	return c.lookupCommand(ctx, submission{operation: operation, key: submissionKey, input: input})
}
