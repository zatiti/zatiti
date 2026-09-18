package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/mcp"
)

// runMCP is `zatiti mcp serve`: the stdio MCP adapter over the common
// authenticated client. stdout carries protocol frames only; diagnostics go
// to stderr. It starts no controller and no scheduler: a missing controller
// surfaces as controller_unavailable in each tool result, exactly as
// internal/mcp maps the client's error. With bootstrap set, the session
// offers only installation.init, needs no credential, and ends itself once
// the bootstrap completes: it can never become an ordinary session.
func runMCP(ctx context.Context, cfg config, in io.Reader, out, diagnostics io.Writer, descriptors []contract.Descriptor, op contract.Operator, bootstrap bool) error {
	if !bootstrap {
		return mcp.Serve(ctx, in, out, diagnostics, op, descriptors)
	}
	var only []contract.Descriptor
	for _, d := range descriptors {
		if d.ID == bootstrapOperation {
			only = append(only, d)
		}
	}
	if len(only) == 0 {
		return fmt.Errorf("catalog exposes no %s operation", bootstrapOperation)
	}
	sessionCtx, end := context.WithCancel(ctx)
	defer end()
	ender := &bootstrapEnder{inner: op, end: end}
	err := mcp.Serve(sessionCtx, in, out, diagnostics, ender, only)
	if ender.completed() && errors.Is(err, context.Canceled) && ctx.Err() == nil {
		_, _ = fmt.Fprintln(diagnostics, "zatiti: bootstrap completed; this session is over")
		return nil
	}
	return err
}

// bootstrapEnder ends the MCP session after the one completed bootstrap it
// exists for.
type bootstrapEnder struct {
	inner contract.Operator
	end   context.CancelFunc

	mu   sync.Mutex
	done bool
}

func (b *bootstrapEnder) Call(ctx context.Context, operation string, req contract.Request) (contract.Result, error) {
	if operation != bootstrapOperation {
		return contract.Result{}, &contract.Fault{Code: contract.CodePermissionDenied, Message: "a bootstrap session serves only " + bootstrapOperation}
	}
	res, err := b.inner.Call(ctx, operation, req)
	if err == nil && res.Status == contract.StatusCompleted {
		b.mu.Lock()
		b.done = true
		b.mu.Unlock()
		b.end()
	}
	return res, err
}

func (b *bootstrapEnder) completed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.done
}
