// internal/app/mcp.go
//
// `zatiti mcp serve`: MCP stdio with proof-of-possession sessions.
// Acquires installation ownership like `serve` (identical guarantees:
// one controller, generation advance, exclusive lock), then serves the
// protocol until stdin closes or the context cancels.

package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"zatiti/internal/authz"
	"zatiti/internal/controller"
	"zatiti/internal/identity"
	"zatiti/internal/state"
	"zatiti/internal/transport"
)

// mcpLogger writes diagnostics to stderr: stdout is protocol-only.
func mcpLogger() *log.Logger { return log.New(os.Stderr, "", log.LstdFlags|log.LUTC) }

// sha256Sum is sha256.Sum256, named for the call site above.
func sha256Sum(b []byte) [32]byte { return sha256.Sum256(b) }

func (a *App) runMCPServe(ctx context.Context, args []string) (int, error) {
	opts, err := a.cli.ParseServe(args)
	if err != nil {
		return ExitInputError, err
	}
	ctrl, err := controller.Start(ctx, opts.DataDir, mcpLogger())
	if err != nil {
		if errors.Is(err, state.ErrLocked) {
			return ExitUnavailable, fmt.Errorf("another controller owns this installation")
		}
		if errors.Is(err, state.ErrNotInitialized) {
			return ExitStale, fmt.Errorf("%w: run `zatiti init` first", err)
		}
		return ExitFailure, err
	}

	srv := transport.NewMCPServer(a.registry, transport.Stdin(), transport.Stdout())
	srv.SetAuthenticator(mcpAuthenticator(ctrl, opts.DataDir))
	srv.SetSessionLookup(func(ctx context.Context, sid string) (string, error) {
		return state.SessionPrincipal(ctx, ctrl.Store().DB(), sid)
	})
	srv.SetExecutor(a.mcpExecutor(ctrl))

	err = srv.Serve(ctx)
	runErr := ctrl.Run(ctx)
	switch {
	case err != nil && ctx.Err() != nil:
		return ExitOK, nil
	case err != nil:
		return ExitUnavailable, fmt.Errorf("mcp: %w", err)
	case runErr != nil:
		return ExitFailure, runErr
	}
	return ExitOK, nil
}

// mcpAuthenticator verifies a signed nonce and mints the session row.
func mcpAuthenticator(ctrl *controller.Controller, dataDir string) func(string, []byte, []byte) (string, error) {
	return func(principal string, nonce, sig []byte) (string, error) {
		ctx := context.Background()
		var pub []byte
		err := ctrl.Store().DB().QueryRowContext(ctx,
			`SELECT public_key FROM principals WHERE id = ?`, principal).Scan(&pub)
		if err != nil {
			return "", fmt.Errorf("unknown principal")
		}
		if err := identity.VerifySignature(pub, nonce, sig); err != nil {
			return "", err
		}
		nonceB64 := base64.StdEncoding.EncodeToString(nonce)
		sum := sha256Sum([]byte(principal + ":" + nonceB64))
		sid := base64.StdEncoding.EncodeToString(sum[:16])
		if err := state.CreateSession(ctx, ctrl.Store().DB(), sid, principal); err != nil {
			return "", err
		}
		return sid, nil
	}
}

// mcpExecutor runs a registered operation as the session's principal
// through the same authorization and audit path as the CLI. Inputs are
// validated against the registry's field declarations; the scaffold
// accepts only declared fields and required-field enforcement.
func (a *App) mcpExecutor(ctrl *controller.Controller) func(context.Context, string, string, map[string]any) (string, error) {
	return func(ctx context.Context, principalID, opName string, args map[string]any) (string, error) {
		op, ok := a.registry.Lookup(opName)
		if !ok {
			return "", fmt.Errorf("unknown operation %q", opName)
		}
		db := ctrl.Store().DB()
		// Reject undeclared fields: MCP input is not a freeform channel.
		declared := map[string]bool{}
		for _, f := range op.Fields {
			declared[f.Name] = true
			if f.Required {
				if _, present := args[f.Name]; !present {
					return "", fmt.Errorf("missing required field %q", f.Name)
				}
			}
		}
		for k := range args {
			if !declared[k] {
				return "", fmt.Errorf("undeclared field %q", k)
			}
		}
		payload, err := json.Marshal(args)
		if err != nil {
			return "", err
		}
		orgID, err := state.RootOrgID(ctx, db)
		if err != nil {
			return "", err
		}
		az := authz.New(db)
		dec, err := az.Decide(ctx, principalID, orgID, op)
		if err != nil {
			return "", err
		}
		if !dec.Allow {
			_ = state.RecordAudit(ctx, db, state.AuditEntry{
				ActorID: principalID, OrgID: orgID, Op: opName,
				Payload: payload, Result: "denied", Detail: dec.Reason,
			})
			return "", fmt.Errorf("denied: %s", dec.Reason)
		}
		switch opName {
		case "organization.list":
			orgs, err := state.ListOrganizations(ctx, db)
			if err != nil {
				return "", err
			}
			var lines []string
			for _, o := range orgs {
				lines = append(lines, fmt.Sprintf("%s\t%s\t%d", o.ID, o.Name, o.CreatedAt))
			}
			return strings.Join(lines, "\n"), nil
		case "organization.create":
			name, _ := args["name"].(string)
			id := state.NewID()
			if err := state.CreateOrganization(ctx, db, id, name, principalID, state.AuditEntry{
				ActorID: principalID, OrgID: orgID, Op: opName,
				Payload: payload, Result: "ok",
			}); err != nil {
				return "", err
			}
			return fmt.Sprintf("created organization %s (%s)", name, id), nil
		default:
			return "", fmt.Errorf("operation %q not executable over MCP yet", opName)
		}
	}
}
