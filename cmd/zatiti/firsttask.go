package main

import (
	"github.com/spf13/cobra"

	"github.com/zatiti/zatiti/internal/installation"
)

// firstTaskLong states the three facts a person cannot infer from the
// refusals alone and that stranded the first real attempt: the evidence
// artifact, the currency sentinel, and the completeness of the definition.
const firstTaskLong = `Create a bounded, durable task. The definition is a complete JSON document;
every field in the example below is required and the refusals name exactly
which one is missing or wrong.

Before the first task on a fresh installation:

  1. The verifier profile's capability_evidence.artifact must name an artifact
     that exists, published at INSTALLATION scope (scope with installation_id
     only) through artifact upload begin/chunk/finish. A reference nobody holds
     is refused artifact_fault ("not resolvable in scope").
  2. Until a budget is configured, the accounting currency is the sentinel XXX
     and only a task with limits.currency "XXX" and spend_micro_units 0 is
     admitted; USD or a positive spend is refused budget_unavailable.
  3. owner_id is your own principal id (principal list, kind "human") and
     worker_id an existing worker (worker list; the chief exists after init).

The example is the exact sequence the tests execute against a real controller;
replace the UPPER_CASE placeholders from each command's output.`

// annotateFirstTask attaches the worked first-task sequence to the generated
// `task create` command. The generated tree is left untouched otherwise: no
// operation is added, no flag changes, no refusal is weakened.
func annotateFirstTask(root *cobra.Command) {
	task := childCommand(root, "task")
	if task == nil {
		return
	}
	create := childCommand(task, "create")
	if create == nil {
		return
	}
	create.Long = firstTaskLong
	create.Example = installation.FirstTaskSequence
}

func childCommand(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}
