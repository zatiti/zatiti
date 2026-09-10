package application

import (
	"context"
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// policyExempt lists the public capabilities the dispatcher does not gate on
// _policy.check: the one-time bootstrap and the capability catalog queries.
// Everything else public is gated.
var policyExempt = map[string]bool{
	"installation.init":   true,
	"capabilities.list":   true,
	"capabilities.schema": true,
}

// identityAuthority is the _identity.authority output data.
type identityAuthority struct {
	Resource storedAuthority `json:"resource"`
}

// storedAuthority mirrors $defs/Authority.
type storedAuthority struct {
	Principal    storedPrincipal `json:"principal"`
	Grants       []storedGrant   `json:"grants"`
	Restrictions []string        `json:"restrictions"`
}

// storedPrincipal mirrors $defs/Principal.
type storedPrincipal struct {
	ID      contract.ID    `json:"id"`
	Version int64          `json:"version"`
	Kind    string         `json:"kind"`
	Name    string         `json:"name"`
	Scope   contract.Scope `json:"scope"`
	Revoked bool           `json:"revoked"`
}

// storedGrant mirrors $defs/Grant.
type storedGrant struct {
	ID           contract.ID    `json:"id"`
	Version      int64          `json:"version"`
	PrincipalID  contract.ID    `json:"principal_id"`
	Scope        contract.Scope `json:"scope"`
	Capabilities []string       `json:"capabilities"`
	Destinations []string       `json:"destinations"`
	Denied       bool           `json:"denied"`
}

// revalidateAuthority confirms the actor still exists unrevoked in scope
// with the authenticated kind, through the identity owner's current
// authority view. Authentication resolved the credential once at admission;
// every dispatch revalidates the principal so a revocation denies the very
// next operation.
func (a *Application) revalidateAuthority(ctx context.Context, u contract.Unit, actor contract.Actor, scope contract.Scope) error {
	input, err := json.Marshal(struct {
		PrincipalID contract.ID    `json:"principal_id"`
		Scope       contract.Scope `json:"scope"`
	}{PrincipalID: actor.PrincipalID, Scope: scope})
	if err != nil {
		return internalFault("authority request could not be encoded")
	}
	var out identityAuthority
	if err := a.dispatchCall(ctx, u, "_identity.authority", input, &out); err != nil {
		f := faultOf(err)
		if f.Code == contract.CodeNotFound {
			return permissionFault("principal is not authorized in this scope")
		}
		return err
	}
	switch {
	case out.Resource.Principal.ID != actor.PrincipalID:
		return internalFault("identity authority returned a different principal")
	case out.Resource.Principal.Revoked:
		return permissionFault("principal is revoked")
	case out.Resource.Principal.Kind != actor.Kind:
		return permissionFault("principal kind does not match the authenticated principal")
	}
	return nil
}

// policyResultPayload is the _policy.check output data.
type policyResultPayload struct {
	Resource storedPolicyResult `json:"resource"`
}

// storedPolicyResult mirrors $defs/PolicyResult.
type storedPolicyResult struct {
	Decision     string                  `json:"decision"`
	Reasons      []string                `json:"reasons"`
	Requirements []storedRequirementItem `json:"requirements"`
}

// storedRequirementItem mirrors $defs/DecisionRequirement.
type storedRequirementItem struct {
	ActionDigest       contract.Digest `json:"action_digest"`
	HumanRequired      bool            `json:"human_required"`
	EligiblePrincipals []contract.ID   `json:"eligible_principals"`
	ExpiresAt          *string         `json:"expires_at"`
	SeparateProposer   bool            `json:"separate_proposer"`
}

// policyGate runs the current policy decision for one public capability
// under the transaction's authority. Deny wins, review demands its
// requirement, and an unknown decision fails closed.
func (a *Application) policyGate(ctx context.Context, u contract.Unit, scope contract.Scope, capability string) error {
	if policyExempt[capability] {
		return nil
	}
	input, err := json.Marshal(struct {
		Scope      contract.Scope `json:"scope"`
		Capability string         `json:"capability"`
	}{Scope: scope, Capability: capability})
	if err != nil {
		return internalFault("policy check request could not be encoded")
	}
	var out policyResultPayload
	if err := a.dispatchCall(ctx, u, "_policy.check", input, &out); err != nil {
		return err
	}
	switch out.Resource.Decision {
	case "allow":
		return nil
	case "deny":
		return permissionFault("policy denied %s: %s", capability, joinReasons(out.Resource.Reasons))
	case "review":
		details, dErr := json.Marshal(map[string]any{
			"reasons":      out.Resource.Reasons,
			"requirements": out.Resource.Requirements,
		})
		if dErr != nil {
			details = nil
		}
		return &contract.Fault{
			Code:    contract.CodeReviewRequired,
			Message: "operation requires review: " + joinReasons(out.Resource.Reasons),
			Details: details,
		}
	case "prerequisite_missing":
		return &contract.Fault{
			Code:    contract.CodePrerequisiteMissing,
			Message: "operation prerequisites are missing: " + joinReasons(out.Resource.Reasons),
		}
	default:
		return internalFault("policy returned unknown decision %q", out.Resource.Decision)
	}
}

func joinReasons(reasons []string) string {
	switch len(reasons) {
	case 0:
		return "no reason recorded"
	case 1:
		return reasons[0]
	default:
		out := reasons[0]
		for _, r := range reasons[1:] {
			out += "; " + r
		}
		return out
	}
}

// commandBegin acquires or joins the durable command identity for one public
// mutation. Identical input replays the retained disposition; the evidence
// owner conflicts a changed digest at this atomic boundary, before any
// stale-version validation runs.
func (a *Application) commandBegin(
	ctx context.Context,
	u contract.Unit,
	actor contract.Actor,
	desc contract.Descriptor,
	submissionKey string,
	digest contract.Digest,
) (contract.ID, *contract.Result, error) {
	input, err := json.Marshal(submissionCommandInput{
		PrincipalID:      actor.PrincipalID,
		Operation:        desc.ID,
		OperationVersion: desc.Version,
		SubmissionKey:    submissionKey,
		RequestDigest:    digest,
	})
	if err != nil {
		return "", nil, internalFault("command begin request could not be encoded")
	}
	var out evidenceBeginOutput
	if err := a.dispatchCall(ctx, u, "_evidence.command.begin", input, &out); err != nil {
		return "", nil, err
	}
	if out.Existing == nil {
		if out.CommandID == "" {
			return "", nil, internalFault("command begin returned no command identity")
		}
		return out.CommandID, nil, nil
	}
	cmd := out.Existing
	if cmd.RequestDigest != digest {
		return "", nil, submissionConflictFault(
			"submission key %q is already bound to a different input for %s", submissionKey, desc.ID)
	}
	replay, err := replayDisposition(cmd)
	if err != nil {
		return "", nil, err
	}
	return cmd.ID, &replay, nil
}

// commandFinish persists the complete result envelope in the same
// transaction as the handler's state changes and events. The retained
// envelope replays exactly, including fault message, details, retryability
// and cursor.
func (a *Application) commandFinish(ctx context.Context, u contract.Unit, commandID contract.ID, result contract.Result) error {
	input, err := json.Marshal(struct {
		CommandID contract.ID     `json:"command_id"`
		Result    contract.Result `json:"result"`
	}{CommandID: commandID, Result: result})
	if err != nil {
		return internalFault("command finish request could not be encoded")
	}
	var out struct {
		Resource storedCommand `json:"resource"`
	}
	if err := a.dispatchCall(ctx, u, "_evidence.command.finish", input, &out); err != nil {
		return err
	}
	if out.Resource.Status != result.Status {
		return internalFault(
			"retained command %s status %q disagrees with the finished result %q",
			commandID, out.Resource.Status, result.Status)
	}
	return nil
}

// dispatchCall performs one application-owned internal call inside the
// current transaction. It goes through the same checked dispatcher as owner
// ports calls, under the "application" caller identity the catalog
// allowlists.
func (a *Application) dispatchCall(ctx context.Context, u contract.Unit, operation string, input []byte, out any) error {
	payload, err := a.dispatchNested(ctx, u, applicationCaller, contract.Invocation{
		Operation: operation,
		Version:   0,
		Input:     input,
	})
	if err != nil {
		return err
	}
	if payload.Status == contract.StatusFailed {
		if payload.Error != nil {
			return payload.Error
		}
		return internalFault("internal operation %s failed without a fault", operation)
	}
	if err := contract.DecodeStrict(payload.Data, out); err != nil {
		return internalFault("internal operation %s returned undecodable data: %v", operation, err)
	}
	return nil
}
