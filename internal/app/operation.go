// internal/app/operation.go
//
// Generated operation execution: resolve actor → authorize → journal →
// execute. Ordering is fixed: authorization precedes execution, and
// both grants and denials are journaled. The scaffold's actor is the
// owner principal; multi-actor identity arrives with T3.x.

package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"zatiti/internal/authz"
	"zatiti/internal/identity"
	"zatiti/internal/operations"
	"zatiti/internal/state"
)

func (a *App) runOperation(ctx context.Context, name string, args []string) (int, error) {
	op, _ := a.registry.Lookup(name)

	dataDir, rest, err := a.cli.ExtractDataDir(args, name)
	if err != nil {
		return ExitInputError, err
	}
	store, err := state.Open(dataDir)
	if err != nil {
		if errors.Is(err, state.ErrLocked) {
			return ExitUnavailable, err
		}
		return ExitUnavailable, fmt.Errorf("open state: %w", err)
	}
	defer store.Close()
	if _, err := store.Generation(ctx); err != nil {
		if errors.Is(err, state.ErrNotInitialized) {
			return ExitStale, fmt.Errorf("%w: run `zatiti init` first", err)
		}
		return ExitFailure, err
	}

	actorID, err := state.OwnerPrincipalID(ctx, store.DB())
	if err != nil {
		return ExitFailure, fmt.Errorf("resolve actor: %w", err)
	}

	inputs, err := a.cli.ParseOpFields(op, rest)
	if err != nil {
		return ExitInputError, err
	}
	payload, err := json.Marshal(inputs)
	if err != nil {
		return ExitFailure, err
	}

	az := authz.New(store.DB())
	// org-scoped ops carry the target org in inputs["org"]; the scaffold's
	// single-org model defaults to the root organization.
	orgID, err := state.RootOrgID(ctx, store.DB())
	if err != nil {
		return ExitFailure, err
	}
	dec, err := az.Decide(ctx, actorID, orgID, op)
	if err != nil {
		return ExitFailure, err
	}
	if !dec.Allow {
		_ = state.RecordAudit(ctx, store.DB(), state.AuditEntry{
			ActorID: actorID, OrgID: orgID, Op: name,
			Payload: payload, Result: "denied", Detail: dec.Reason,
		})
		return ExitDenied, fmt.Errorf("%w: %s", authz.ErrDenied, dec.Reason)
	}

	switch name {
	case "organization.list":
		return a.execOrgList(ctx, store.DB())
	case "organization.create":
		return a.execOrgCreate(ctx, store.DB(), actorID, orgID, name, payload, inputs)
	case "audit.list":
		return a.execAuditList(ctx, store.DB(), actorID, orgID, inputs)
	case "principal.create":
		return a.execPrincipalCreate(ctx, store.DB(), actorID, orgID, name, payload, inputs)
	case "credential.issue":
		return a.execCredentialIssue(ctx, store, actorID, orgID, name, payload, inputs)
	case "review.submit":
		return a.execReviewSubmit(ctx, store.DB(), actorID, orgID, name, inputs)
	case "review.approve":
		return a.execReviewApprove(ctx, store.DB(), actorID, orgID, name, inputs)
	case "review.execute":
		return a.execReviewExecute(ctx, store.DB(), actorID, inputs)
	case "organization.purge":
		return ExitDenied, errors.New(
			"organization.purge executes only via review.submit → review.approve ×2 → review.execute")
	default:
		return ExitPrereqMissing, fmt.Errorf("operation %q is declared but not implemented", name)
	}
}

func (a *App) execOrgList(ctx context.Context, db *sql.DB) (int, error) {
	orgs, err := state.ListOrganizations(ctx, db)
	if err != nil {
		return ExitFailure, err
	}
	for _, o := range orgs {
		fmt.Fprintf(osStdout, "%s\t%s\t%d\n", o.ID, o.Name, o.CreatedAt)
	}
	return ExitOK, nil
}

func (a *App) execOrgCreate(ctx context.Context, db *sql.DB, actorID, orgID, opName string, payload []byte, inputs map[string]any) (int, error) {
	name, _ := inputs["name"].(string)
	if name == "" {
		return ExitInputError, errors.New("organization.create: name is required")
	}
	id := newAppID()
	err := state.CreateOrganization(ctx, db, id, name, actorID, state.AuditEntry{
		ActorID: actorID, OrgID: orgID, Op: opName,
		Payload: payload, Result: "ok",
	})
	if errors.Is(err, state.ErrConflict) {
		return ExitStale, err
	}
	if err != nil {
		return ExitFailure, err
	}
	fmt.Fprintf(osStdout, "created organization %s (%s)\n", name, id)
	return ExitOK, nil
}

// newAppID: app-level UUID for new entities. Distinct from the state
// and identity copies only in package location; format identical.
func newAppID() string { return state.NewID() }

// execAuditList: reads require auditor-or-better on the target org;
// the generic Decide call already ran with the root org, but audit.list
// may target another org via -org, so it re-checks against the target.
func (a *App) execAuditList(ctx context.Context, db *sql.DB, actorID, defaultOrg string, inputs map[string]any) (int, error) {
	orgName, _ := inputs["org"].(string)
	orgID := defaultOrg
	if orgName != "" {
		id, err := state.OrgIDByName(ctx, db, orgName)
		if errors.Is(err, state.ErrNotFound) {
			return ExitInputError, fmt.Errorf("no such organization %q", orgName)
		}
		if err != nil {
			return ExitFailure, err
		}
		orgID = id
	}
	limit, _ := inputs["limit"].(uint64)

	az := authz.New(db)
	op, _ := a.registry.Lookup("audit.list")
	dec, err := az.Decide(ctx, actorID, orgID, op)
	if err != nil {
		return ExitFailure, err
	}
	if !dec.Allow {
		_ = state.RecordAudit(ctx, db, state.AuditEntry{
			ActorID: actorID, OrgID: orgID, Op: "audit.list",
			Result: "denied", Detail: dec.Reason,
		})
		return ExitDenied, fmt.Errorf("%w: %s", authz.ErrDenied, dec.Reason)
	}

	rows, err := state.ListAudit(ctx, db, orgID, limit)
	if err != nil {
		return ExitFailure, err
	}
	for _, r := range rows {
		fmt.Fprintf(osStdout, "%d\t%d\t%s\t%s\t%s\n",
			r.Seq, r.TS, r.ActorID, r.Result, r.Op)
	}
	return ExitOK, nil
}

func (a *App) execPrincipalCreate(ctx context.Context, db *sql.DB, actorID, orgID, opName string, payload []byte, inputs map[string]any) (int, error) {
	label, _ := inputs["name"].(string)
	role, _ := inputs["role"].(string)
	if label == "" {
		return ExitInputError, errors.New("principal.create: name is required")
	}
	id := state.NewID()
	err := state.CreateOperator(ctx, db, id, label, role, orgID, actorID, state.AuditEntry{
		ActorID: actorID, OrgID: orgID, Op: opName,
		Payload: payload, Result: "ok",
	})
	switch {
	case errors.Is(err, state.ErrBadRole):
		return ExitInputError, err
	case errors.Is(err, state.ErrConflict):
		return ExitStale, err
	case err != nil:
		return ExitFailure, err
	}
	fmt.Fprintf(osStdout, "created operator %s (%s, role %s, no credential yet — T3.x)\n", label, id, role)
	return ExitOK, nil
}

// execCredentialIssue: owner-only (policy override in authz), binds a
// real key over a pending marker, custodies the private half keystore-side.
func (a *App) execCredentialIssue(ctx context.Context, store *state.Store, actorID, orgID, opName string, payload []byte, inputs map[string]any) (int, error) {
	ref, _ := inputs["principal"].(string)
	if ref == "" {
		return ExitInputError, errors.New("credential.issue: -principal is required")
	}
	principalID, pending, err := state.PrincipalIDByRef(ctx, store.DB(), ref)
	if errors.Is(err, state.ErrNotFound) {
		return ExitInputError, err
	}
	if err != nil {
		return ExitFailure, err
	}
	if !pending {
		_ = state.RecordAudit(ctx, store.DB(), state.AuditEntry{
			ActorID: actorID, OrgID: orgID, Op: opName,
			Payload: payload, Result: "denied",
			Detail: "principal already holds a credential",
		})
		return ExitStale, fmt.Errorf("principal %s already holds a credential", principalID)
	}

	provisioner, err := identity.NewProvisioner(filepath.Join(store.Dir(), "keystore"))
	if err != nil {
		return ExitUnavailable, err
	}
	cred, err := provisioner.IssueFor(ctx, principalID)
	if err != nil {
		return ExitFailure, err
	}
	err = state.BindCredential(ctx, store.DB(), principalID, cred.PublicKey, state.AuditEntry{
		ActorID: actorID, OrgID: orgID, Op: opName,
		Payload: payload, Result: "ok",
		Detail: "keystore:" + cred.KeystoreKey,
	})
	if errors.Is(err, state.ErrConflict) {
		// Lost the CAS race; the custodied key must not linger.
		return ExitStale, err
	}
	if err != nil {
		return ExitFailure, err
	}
	fmt.Fprintf(osStdout, "issued credential for %s (custodied at keystore/%s.key — deliver out-of-band)\n",
		principalID, cred.KeystoreKey)
	return ExitOK, nil
}

func (a *App) execReviewSubmit(ctx context.Context, db *sql.DB, actorID, defaultOrg, opName string, inputs map[string]any) (int, error) {
	targetOp, _ := inputs["op"].(string)
	orgName, _ := inputs["org"].(string)

	// Only declared DESTRUCTIVE ops may be proposed.
	declared, ok := a.registry.Lookup(targetOp)
	if !ok || declared.Mutability != operations.Destructive {
		return ExitInputError, fmt.Errorf("op %q is not a declared destructive operation", targetOp)
	}
	if orgName == "" {
		return ExitInputError, errors.New("review.submit: -org is required")
	}
	orgID, err := state.OrgIDByName(ctx, db, orgName)
	if errors.Is(err, state.ErrNotFound) {
		return ExitInputError, err
	}
	if err != nil {
		return ExitFailure, err
	}
	payload, _ := json.Marshal(map[string]string{"op": targetOp, "org": orgName})
	id := state.NewID()
	if err := state.CreateReview(ctx, db, id, orgID, targetOp, actorID, payload, state.AuditEntry{
		ActorID: actorID, OrgID: orgID, Op: opName,
		Payload: payload, Result: "ok", Detail: "proposed " + targetOp,
	}); err != nil {
		return ExitFailure, err
	}
	fmt.Fprintf(osStdout, "review %s opened for %s on %s (needs %d approvals, proposer excluded)\n",
		id, targetOp, orgName, state.QuorumApprovals)
	return ExitOK, nil
}

func (a *App) execReviewApprove(ctx context.Context, db *sql.DB, actorID, defaultOrg, opName string, inputs map[string]any) (int, error) {
	reviewID, _ := inputs["review"].(string)
	if reviewID == "" {
		return ExitInputError, errors.New("review.approve: -review is required")
	}
	err := state.ApproveReview(ctx, db, reviewID, actorID, state.AuditEntry{
		ActorID: actorID, Op: opName,
		Payload: []byte(`{"review":"` + reviewID + `"}`), Result: "ok",
	})
	switch {
	case errors.Is(err, state.ErrNotFound):
		return ExitInputError, err
	case errors.Is(err, state.ErrDenied):
		return ExitDenied, err
	case errors.Is(err, state.ErrConflict):
		return ExitStale, err
	case err != nil:
		return ExitFailure, err
	}
	fmt.Fprintf(osStdout, "approved %s\n", reviewID)
	return ExitOK, nil
}

func (a *App) execReviewExecute(ctx context.Context, db *sql.DB, actorID string, inputs map[string]any) (int, error) {
	reviewID, _ := inputs["review"].(string)
	if reviewID == "" {
		return ExitInputError, errors.New("review.execute: -review is required")
	}
	result, err := state.ExecuteReview(ctx, db, reviewID, actorID,
		func(tx *sql.Tx) (string, error) {
			// Resolve the proposal's payload op → the purge. The scaffold
			// has exactly one destructive op; the switch makes the
			// extension point explicit.
			var op string
			if err := tx.QueryRowContext(ctx,
				`SELECT op FROM reviews WHERE id = ?`, reviewID).Scan(&op); err != nil {
				return "", err
			}
			if op != "organization.purge" {
				return "", fmt.Errorf("no executor for review op %q", op)
			}
			var orgName string
			if err := tx.QueryRowContext(ctx,
				`SELECT json_extract(payload,'$.org') FROM reviews WHERE id = ?`,
				reviewID).Scan(&orgName); err != nil {
				return "", err
			}
			orgID, err := state.OrgIDByNameTx(ctx, tx, orgName)
			if err != nil {
				return "", err
			}
			return state.PurgeOrganization(ctx, tx, orgID)
		})
	switch {
	case errors.Is(err, state.ErrNotReady):
		return ExitStale, err
	case errors.Is(err, state.ErrNotFound):
		return ExitInputError, err
	case errors.Is(err, state.ErrConflict):
		return ExitStale, err
	case err != nil:
		return ExitFailure, err
	}
	fmt.Fprintf(osStdout, "%s (review %s executed)\n", result, reviewID)
	return ExitOK, nil
}
