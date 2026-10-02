# ADR 007: Execution-pinned tool operation mapping and durable worker replies (contract revision 25)

## Status
Accepted

## Date
2026-09-28

## Context
On zatiti main 9cd94f7 (revision 20), a hosted model can never act. A ModelToolProposal requires operation_id and operation_version, but ContextToolDefinition carries no mapping. The Responses adapter therefore flags every function_call as tool_proposal_mapping_unspecified and emits zero proposals (internal/adapters/responses/interpret.go:177-185). Execution then refuses every zero-proposal observation (internal/execution/interpret.go:170-178).

MCP tools (revision 18) are offered but can never be called, and the planned browser tools c7-c9 would be blocked the same way. A recorded reply is only committed to a preview hub: it never becomes a conversation message (internal/controller/turns.go:495-503; contracts.md:476).

Verification also found several defects:
- A: execution-built contexts list the four sealed decision tools, but tool_contract_versions omits them, so the real adapter refuses every execution-built model_step (context_build.go:749 vs :914-918 and :986-994; contextdoc.go:100-108).
- B: the model-dispatch tool is offered to the model.
- C: a local-effect tool lets the model choose any operation_id (interpret.go:707-716).
- F: no production code sets a turn's conversation_id, so revision 20's reply route is unreachable.
- D: controller turn discovery would admit and auto-acknowledge human recipients.

The contracts already say the mapping must be 'the qualified mapping for that tool' and that sealed tools are execution-local. The connections Tool contract has no operation field, and no local-effect tool exists.

## Decision
Revision 25, after worker-principal revision 22, adds optional, paired operation_id and operation_version fields to ContextToolDefinition.

Execution, and only execution, fills them for every offered tool:
- sealed decision tools map to zatiti.local_decision.<name>@1;
- every adapter-dispatched tool (built-in, MCP, browser) maps to _effects.prepare@1, the governed operation interpretation actually invokes;
- local-effect tools and the model-dispatch tool are not offered;
- tool_contract_versions equals the offered set.

The Responses adapter maps a call by name to the offered definition and copies tool and mapping from it. Unoffered, unmapped or malformed calls get a flag and no proposal.

Execution verifies the proposal's mapping equals the pinned mapping and never executes a model-chosen operation.

On a conversation-linked turn, a reply is prepared as conversation.message.send with a deterministic message_id. The controller drives it through WorkerOperator.ExecuteWorker under the worker's own actor, records it, completes the turn, and only then commits the preview.

Message turns adopt their conversation at admission. Non-worker recipients are never turn-admitted, and messaging's ready scan lists only worker recipients.

Rejected alternatives:
- Model-supplied or adapter-invented operation ids: they violate 'never model-supplied'.
- A per-tool operation field on the connections Tool $def now: it needs S2, a v5 migration and a use case. It is deferred until a local-effect tool contract exists.
- One catalog operation per tool: this would multiply the frozen catalog for no authority gain.
- Execution admitting the reply through _messaging.admit in the observation transaction under the controller identity: the controller identity may authorize bookkeeping only, never a model proposal, and messaging's disclosure policy checks the unit actor.
- Mapping sealed tools directly to conversation.message.send: task-turn replies have no conversation, and the input shapes differ.

## Consequences
Positive outcomes:
- Hosted model tool calls, MCP included, become typed, pinned, re-validated proposals, and plan-v3 browser tools inherit the same path with no further contract change.
- Worker replies persist in conversation history exactly once and remain governed by participant and disclosure policy under the worker's own identity.
- Defects A, B, C, D and F are closed.

Costs:
- Revision 25 must merge after worker-principal revision 22 and before the browser milestone, which follows this revision. The browser specification tasks rebase onto E0T's files.
- internal/controller and internal/messaging change, contrary to plan-v2/v3's 'controller unchanged'.
- Local-effect product tools are unavailable until a pinned local mapping exists. The interpret_test local-tool scenarios are rewritten as refusals.
- A crash between ExecuteWorker and record is resolved by a history check keyed on the deterministic message_id. A narrow race can still yield a second, different reply.

What stays open:
- Controlled hosted chat and governed connection validation pass; installed live-provider qualification remains outstanding.
- Mailbox-only message turns stay unsupported (defect G).
- Text-only model output still refuses the observation.
- Live provider behavior is unqualified until the gated live case runs.
