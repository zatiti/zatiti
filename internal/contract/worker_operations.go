package contract

// WorkerVisibleOperations is the frozen worker-visible local operation
// allowlist (revision 21). contract.WorkerOperator refuses any operation
// outside it, and identity grants a registered worker principal exactly
// these capabilities -- one list, declared once, so the executor's
// allowlist and the worker's standing grant can never drift apart. The
// rationale for each family is documented beside ExecuteWorker in
// internal/application/worker.go.
var workerVisibleOperations = []string{
	"configuration.draft.create",
	"configuration.draft.discard",
	"configuration.draft.get",
	"configuration.draft.list",
	"configuration.draft.update",
	"configuration.plan",
	"configuration.plan.get",
	"configuration.plan.list",
	"configuration.revision.get",
	"configuration.revision.list",
	"task.accept",
	"task.assign",
	"task.cancel",
	"task.create",
	"task.delegate",
	"task.dependencies",
	"task.get",
	"task.list",
	"task.retry",
	"task.start",
	"task.update",
	"responsibility.archive",
	"responsibility.create",
	"responsibility.get",
	"responsibility.list",
	"responsibility.pause",
	"responsibility.resume",
	"responsibility.update",
	"memory.inspect",
	"memory.list",
	"memory.promote",
	"memory.recall",
	"memory.remember",
	"memory.retract",
	"mailbox.ack",
	"mailbox.list",
	"mailbox.send",
	"conversation.create",
	"conversation.get",
	"conversation.list",
	"conversation.message.list",
	"conversation.message.send",
	"conversation.update",
	"artifact.get",
	"artifact.list",
	"artifact.read",
	"artifact.upload.begin",
	"artifact.upload.cancel",
	"artifact.upload.chunk",
	"artifact.upload.finish",
}

// WorkerVisibleOperations returns a fresh copy of the allowlist in its
// declared order.
func WorkerVisibleOperations() []string {
	return append([]string(nil), workerVisibleOperations...)
}

// WorkerVisibleOperationSet returns the allowlist as a fresh lookup set.
func WorkerVisibleOperationSet() map[string]bool {
	set := make(map[string]bool, len(workerVisibleOperations))
	for _, op := range workerVisibleOperations {
		set[op] = true
	}
	return set
}
