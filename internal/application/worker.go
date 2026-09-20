// Worker proposal authorization: contract.WorkerOperator (revision 3,
// P00-003). A worker-authored local proposal invokes an ordinary public
// operation under the worker's own authenticated actor, scope-intersected
// with the task/source authorization envelope named by the request. This
// file owns exactly that seam; every other public operation still reaches
// the ordinary dispatcher through Invoke, unchanged.
package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/zatiti/zatiti/internal/contract"
)

// workerVisibleOperations is the explicit local model-visible operation
// allowlist P00-003 requires. A worker-authored proposal may name only
// these public operations, regardless of what its own grants or policy
// would otherwise permit: an operation absent here is refused before its
// descriptor ever reaches dispatch, whatever its own authorization would
// have decided. Grant/policy changes, credentials, internal bookkeeping and
// human review are never reachable through WorkerOperator merely because
// they exist in the public catalog.
//
// This table is owned entirely by internal/application. P00-003 names four
// categories and deliberately leaves exact catalog membership to the
// implementer (it is not part of the frozen contract schemas), so changing
// it is a local decision that never touches a sibling package:
//
//   - authorized configuration authoring/inspection: propose and inspect
//     configuration drafts, plans and revisions. Never configuration.apply
//     or configuration.rollback.plan, which activate a definition under
//     prior authority, and never an organization/team/project/worker/
//     binding/execution_profile/policy/schedule/autonomy_rule resource
//     operation, each of which stages a change to another principal's
//     standing configuration.
//   - task/delegation/responsibility actions: the tasks and scheduling
//     (responsibility) families a worker uses to run and hand off its own
//     work. task.accept's own handler still refuses a worker's self-accept
//     of a manual-acceptance task; the allowlist governs which operations
//     are reachable at all, not each operation's own business rule.
//   - approved memory/messaging operations: the operational memory actions
//     (never memory.binding.*, which configures which brains bind to a
//     scope -- that is configuration, not an operational memory action)
//     and the messaging families.
//   - output publication: uploading and reading the artifacts a task
//     produces or consumes. Never artifact.export, which is a bulk/
//     administrative operation closer to backup custody.
var workerVisibleOperations = map[string]bool{
	// authorized configuration authoring/inspection
	"configuration.draft.create":  true,
	"configuration.draft.discard": true,
	"configuration.draft.get":     true,
	"configuration.draft.list":    true,
	"configuration.draft.update":  true,
	"configuration.plan":          true,
	"configuration.plan.get":      true,
	"configuration.plan.list":     true,
	"configuration.revision.get":  true,
	"configuration.revision.list": true,

	// task/delegation/responsibility actions
	"task.accept":            true,
	"task.assign":            true,
	"task.cancel":            true,
	"task.create":            true,
	"task.delegate":          true,
	"task.dependencies":      true,
	"task.get":               true,
	"task.list":              true,
	"task.retry":             true,
	"task.start":             true,
	"task.update":            true,
	"responsibility.archive": true,
	"responsibility.create":  true,
	"responsibility.get":     true,
	"responsibility.list":    true,
	"responsibility.pause":   true,
	"responsibility.resume":  true,
	"responsibility.update":  true,

	// approved memory/messaging operations
	"memory.inspect":            true,
	"memory.list":               true,
	"memory.promote":            true,
	"memory.recall":             true,
	"memory.remember":           true,
	"memory.retract":            true,
	"mailbox.ack":               true,
	"mailbox.list":              true,
	"mailbox.send":              true,
	"conversation.create":       true,
	"conversation.get":          true,
	"conversation.list":         true,
	"conversation.message.list": true,
	"conversation.message.send": true,
	"conversation.update":       true,

	// output publication
	"artifact.get":           true,
	"artifact.list":          true,
	"artifact.read":          true,
	"artifact.upload.begin":  true,
	"artifact.upload.cancel": true,
	"artifact.upload.chunk":  true,
	"artifact.upload.finish": true,
}

// workerActorChainSeed marks the dispatch chain WorkerOperator opens to
// resolve a worker's actor through identity. It must never collide with a
// real operation id: dispatchNested's recursion guard compares chain
// entries against the operation it is about to call ("_identity.authority"
// here), and a colliding seed would misfire that guard as a false
// recursive-loop refusal.
const workerActorChainSeed = "_application.worker_actor_resolution"

// ExecuteWorker implements contract.WorkerOperator: a worker-authored local
// proposal invokes an ordinary public operation under the worker's own
// authenticated actor, scope-intersected with the task/source authorization
// envelope named by request.Scope. It is a controller-only capability --
// entrypoint assembly is expected to inject it only into trusted runtime
// composition (execution, controller), exactly as contract.DatabaseBackup
// and contract.RestoreCoordinator are supplied to exactly one owner
// elsewhere in this contract. Nothing here authenticates a "caller"
// parameter because that Go-level injection boundary is the control; the
// frozen WorkerRequest type itself carries no Actor field for the same
// reason -- it is "a trusted-runtime-composition value, not a JSON wire
// document in its own right."
//
// Every check below runs before the ordinary dispatcher (a.Invoke) does:
// the operation must be public, on the explicit worker-visible allowlist
// above, and version-compatible; its own declared scope must never name an
// organization, project, worker or task that conflicts with request.Scope;
// and WorkerID is resolved into a current, unrevoked worker actor through
// identity's authority view -- never accepted as a caller-supplied Actor.
// Once those hold, ExecuteWorker re-enters the exact a.Invoke path a
// CLI/MCP caller would for Operation/Version/Input: the same schema
// validation, the same fresh authority/policy recheck, the same
// submission-replay and transaction machinery already proven by
// Z04/Z10/Z16/Z21. A pause, revocation or policy change recorded after the
// proposal was authored and before this call is observed there, live,
// because a.Invoke performs those reads fresh on every dispatch; nothing in
// ExecuteWorker caches a decision across proposals, and the controller's
// own administrative identity is never substituted to force a denied call
// through -- the dispatched actor is always the resolved worker's own.
func (a *Application) ExecuteWorker(ctx context.Context, request contract.WorkerRequest) (contract.Result, error) {
	if err := a.entryGuard(); err != nil {
		return contract.Result{}, err
	}
	switch {
	case request.TurnID == "" || request.ProposalID == "":
		return contract.Result{}, invalidFault("worker request requires a turn and proposal identity")
	case request.WorkerID == "":
		return contract.Result{}, invalidFault("worker request requires a worker identity")
	case request.Operation == "":
		return contract.Result{}, invalidFault("worker request requires a target operation")
	case request.Scope.InstallationID == "":
		return contract.Result{}, invalidFault("worker request requires an installation scope")
	}
	// WorkerID is an asserted match against the caller's own persisted
	// turn/worker mapping, never a free choice of principal: a request
	// whose own scope names a different worker than the acting principal
	// is already internally inconsistent and is refused before anything
	// else runs.
	if request.Scope.WorkerID != "" && request.Scope.WorkerID != request.WorkerID {
		return contract.Result{}, permissionFault(
			"worker request scope names a different worker than the proposal's own actor")
	}

	installation, err := a.installationID(ctx)
	if err != nil {
		return contract.Result{}, err
	}
	if installation == "" {
		return contract.Result{}, permissionFault("installation is not initialized")
	}
	if request.Scope.InstallationID != installation {
		return contract.Result{}, permissionFault(
			"worker request targets installation %s outside this controller", request.Scope.InstallationID)
	}

	// Allowlist and visibility: refused before the operation is ever
	// resolved for real dispatch, and before identity is ever consulted, so
	// a disallowed target never depends on whether WorkerID happens to name
	// a live principal. Internal operations -- the controller's own
	// bookkeeping -- are categorically excluded regardless of the
	// allowlist's contents.
	desc, _, err := a.reg.Lookup(request.Operation, 0)
	if err != nil {
		return contract.Result{}, notFoundFault("unknown operation %q", request.Operation)
	}
	if desc.Visibility != contract.VisibilityPublic || !workerVisibleOperations[desc.ID] {
		return contract.Result{}, permissionFault(
			"operation %q is not on the worker-visible operation allowlist", request.Operation)
	}
	if request.Version != 0 && int64(request.Version) != desc.Version {
		return contract.Result{}, faultErr(&contract.Fault{
			Code:    contract.CodeCapabilityUnsupported,
			Message: "operation " + desc.ID + " version " + itoa64(int64(request.Version)) + " is not supported",
		})
	}

	// Scope intersection: the operation's own declared scope may leave a
	// dimension unset (it simply is not scoped along that axis), but where
	// both the operation and the envelope name a dimension, they must
	// agree. This blocks a proposal from naming an organization, project,
	// worker or task other than the one that admitted it; the owning
	// domain's own authorization remains responsible for everything a
	// dimension-omitting operation might still reach, exactly as it is for
	// any other authenticated caller.
	derived, err := deriveScope(desc, request.Input, installation)
	if err != nil {
		return contract.Result{}, err
	}
	if !scopeWithin(derived, request.Scope) {
		return contract.Result{}, permissionFault(
			"operation %s scope is outside the worker's authorized scope", desc.ID)
	}

	// Actor resolution through identity -- never a caller-supplied Actor.
	actor, err := a.resolveWorkerActor(ctx, request.WorkerID, installation)
	if err != nil {
		return contract.Result{}, err
	}

	// Deterministic submission identity derived from turn/proposal
	// identity: a crash after this proposal's command commits, followed by
	// exec redelivering the identical (turn, proposal), reaches the
	// ordinary submission-replay machinery inside a.Invoke and returns the
	// original command result -- including a refusal -- instead of
	// executing the domain mutation a second time.
	submissionKey := workerSubmissionKey(request.TurnID, request.ProposalID)

	return a.Invoke(ctx, actor, desc.ID, contract.Request{
		Schema:        contract.SchemaRequest,
		SubmissionKey: submissionKey,
		Input:         request.Input,
	})
}

// resolveWorkerActor resolves workerID into a current worker actor through
// identity's authority view: it must name a live, unrevoked principal of
// kind "worker". The Actor is constructed here and immediately revalidated
// through the same revalidateAuthority every ordinary dispatch already
// uses, so WorkerOperator's notion of "the worker's own actor" never comes
// from caller-supplied data -- only from identity's own current record, at
// the moment of this call.
func (a *Application) resolveWorkerActor(ctx context.Context, workerID contract.ID, installation contract.ID) (contract.Actor, error) {
	actor := contract.Actor{PrincipalID: workerID, Kind: contract.KindWorker}
	scope := contract.Scope{InstallationID: installation}
	err := a.db.Read(ctx, a.systemActor, scope, func(u contract.Unit) error {
		wctx := withState(ctx, &dispatchState{chain: []string{workerActorChainSeed}, unit: u})
		return a.revalidateAuthority(wctx, u, actor, scope)
	})
	if err != nil {
		return contract.Actor{}, err
	}
	return actor, nil
}

// scopeWithin reports whether derived (an operation's own declared scope)
// conflicts with envelope (the task/source authorization envelope that
// admitted a worker's proposal). A dimension either side leaves unset
// places no restriction on that dimension; a dimension both sides set must
// agree exactly.
func scopeWithin(derived, envelope contract.Scope) bool {
	agrees := func(want, got contract.ID) bool {
		return want == "" || got == "" || want == got
	}
	return agrees(envelope.OrganizationID, derived.OrganizationID) &&
		agrees(envelope.ProjectID, derived.ProjectID) &&
		agrees(envelope.WorkerID, derived.WorkerID) &&
		agrees(envelope.TaskID, derived.TaskID)
}

// workerSubmissionKey derives the deterministic wire submission identity
// for one worker proposal from its turn/proposal identity alone -- never
// from a caller-supplied string. The exact same (turn_id, proposal_id)
// redelivered after a crash always produces the exact same key, so the
// ordinary submission-replay machinery (commandBegin/commandFinish)
// recovers the original disposition instead of re-executing.
func workerSubmissionKey(turnID contract.ID, proposalID string) string {
	sum := sha256.Sum256([]byte("worker-proposal/" + string(turnID) + "/" + proposalID))
	return "wp-" + hex.EncodeToString(sum[:])
}

// Compile-time proof that *Application satisfies the frozen capability.
var _ contract.WorkerOperator = (*Application)(nil)
