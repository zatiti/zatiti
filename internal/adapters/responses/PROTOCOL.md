# Wire protocol pin: OpenAI Responses API

Protocol revision string (the value `capability_evidence.protocol_revision`
must carry to select it): **`openai-openapi/2.3.0@ddface9b`**.

Implementation: `openai.go`. This document pins every documented fact the
implementation relies on, where it was read, and where the documented API
cannot give the adapter what the frozen Zatiti contract demands.

## Sources, as read on 2026-09-19

| What | Where | Revision |
|---|---|---|
| The API's OpenAPI document (request and response schemas, paths, error shapes) | `https://raw.githubusercontent.com/openai/openai-openapi/main/openapi.yaml` (repository `openai/openai-openapi`, branch `main`) | commit `ddface9bd361f5fe37943291d23ee2ca72cbcc2b`, committed 2026-09-19T01:29:08Z; `info.version: 2.3.0`; SHA-256 of the file `d4e842399c2e9e63aca79aa9cb04b766f1cb9e1e98d48300a22e3dddc2add6fb` |
| Error codes guide (HTTP status meanings) | `https://developers.openai.com/api/docs/guides/error-codes` | as rendered 2026-09-19 |
| Data retention, `store`, Zero Data Retention | `https://developers.openai.com/api/docs/guides/your-data` | as rendered 2026-09-19 |
| Conversation state guide | `https://developers.openai.com/api/docs/guides/conversation-state` | as rendered 2026-09-19 |
| Pricing (flat rates, the 272K tier break, cache pricing) | `https://openai.com/api/pricing/` | read by the founder and lead 2026-09-18 and quoted in the pinned profile note; the page refused an automated fetch (HTTP 403) on 2026-09-19, so the quotes below are theirs, not re-verified by this adapter's author |

The `platform.openai.com/docs/api-reference/...` pages redirect (301) to
`developers.openai.com/api/docs/...`; the rendered reference pages are
generated from the same OpenAPI document, which is why the document itself
is the primary pin.

## Request: the model step

`POST {profile.endpoint}` (the profile pins `https://api.openai.com/v1/responses`),
`Authorization: Bearer <credential>` (OpenAPI `securitySchemes.ApiKeyAuth`:
`type: http, scheme: bearer`), `Content-Type: application/json`.

Body fields sent, all from `CreateResponse` / `ResponseProperties` /
`CreateModelResponseProperties`:

| Field | Value | Why |
|---|---|---|
| `model` | `profile.model` | `ResponseProperties.model` |
| `conversation` | the id minted by the preparatory call (see below) | `ConversationParam`: "Items from this conversation are prepended to `input_items` for this response request. Input items and output items from this response are automatically added to this conversation after this response completes." |
| `input` | the persisted context as an item list, in exact order | `InputParam` (array form of `InputItem`) |
| `tools` | one `FunctionTool` per context tool: `{type:"function", name, description, parameters: <input_schema>, strict: false}` | `FunctionTool` requires `type, name, strict, parameters`; `strict` is false because Zatiti tool schemas are not guaranteed to satisfy strict-mode constraints |
| `tool_choice` | `"auto"` when tools are present, absent otherwise | `ToolChoiceOptions` |
| `max_output_tokens` | the action's `max_output_tokens` | "An upper bound for the number of tokens that can be generated for a response, including visible output tokens and reasoning tokens", `minimum: 16` |
| `store` | `true` | "Whether to store the generated model response for later retrieval via API. Defaults to true when omitted." Set explicitly so the reconciliation path does not depend on a default |
| `background` | `false` | "Whether to run the model response in the background." A synchronous response is the only shape this adapter classifies |
| `stream` | `false` | The adapter reads one bounded JSON body; streaming is not qualified |
| `service_tier` | `"default"` | "If set to 'default', then the request will be processed with the standard pricing and performance for the selected model." The profile's two rates are the standard prices; any other tier is priced differently and the response's `service_tier` is checked (see accounting) |
| `prompt_cache_options` | `{"mode":"explicit"}` | `PromptCacheModeEnum`: "With `explicit`, OpenAI does not create an implicit breakpoint ... If there are no explicit breakpoints, the request does not use prompt caching." Caching is disabled on purpose: the profile has no cached-input or cache-write rate (see gaps) |
| `metadata` | `{"zatiti_operation_id": ..., "zatiti_attempt_id": ...}` | `Metadata`: up to 16 string pairs, keys ≤ 64 chars, values ≤ 512 chars. Binds the upstream object to the Zatiti attempt as evidence |

Not sent, deliberately: `previous_response_id` ("Cannot be used in
conjunction with `conversation`"), `instructions` (system and developer
messages travel as input items with the documented precedence),
`truncation` (deprecated; its documented default `disabled` makes an
over-long prompt fail with a 400 instead of silently dropping context),
`include`, `reasoning`, `text`, `temperature`, `top_p`, `stream_options`,
`context_management`, `prompt_cache_key`, `safety_identifier`.

### Context to input items

| Zatiti part | Item sent |
|---|---|
| `text` part in a message with role `system`, `developer`, `user` or `assistant` | `EasyInputMessage` `{type:"message", role, content:[{type:"input_text", text}]}`; consecutive text-like parts of one message share one item |
| `memory_excerpt` part | `input_text` content with the excerpt text, in the same message item |
| `artifact` part with a `text/*` media type | `input_text` content with the artifact bytes (loaded and digest-verified by the adapter core) |
| `artifact` part with any other media type | refused: `capability_unsupported` (no image or file mapping is qualified) |
| `tool_call` part | `FunctionToolCall` `{type:"function_call", call_id: proposal.id, name: <context tool name for proposal.tool>, arguments: <proposal.input as a JSON string>}` |
| `tool_result` part | `FunctionCallOutputItemParam` `{type:"function_call_output", call_id: proposal_id, output: <artifact bytes as a string>}` |
| a message with role `tool` carrying text parts | refused: tool results are carried only as `function_call_output` items |

Continuation (`ResponsesParameters.continuation_reference`) is refused:
every step sends the complete persisted context, so nothing server-side may
be prepended to it. The conversation minted for a step is never reused for
another step for the same reason.

## Request: prepare_session (revision 3 -- its own admitted, journaled effect)

Revision 3 (P00-009) splits conversation creation from the model call into
two separately admitted, journaled single-call effects instead of chaining
both inside one `Invoke` (see "Where the frozen contract cannot be
honoured" below, gap 1, formerly a documented deviation this build
accepted -- it no longer needs to). `ResponsesParameters` is a
`kind`-discriminated `oneOf`: `prepare_session` (only `schema`/`kind`, no
`context_artifact`, no `session_handle`) and `model_step` (everything else,
`session_handle` required). Each `Adapter.Invoke` performs exactly one
physical call, matching the dispatched kind.

`prepare_session`: `POST {conversations}` with body
`{"metadata": {"zatiti_operation_id": ..., "zatiti_attempt_id": ...}}`,
where `{conversations}` is the profile endpoint with its final path segment
`responses` replaced by `conversations` (`https://api.openai.com/v1/conversations`).
The profile's `enforcement.provider_destinations` must cover it; an
origin-wide grant (`https://api.openai.com`) is the simplest.

Documented reply (`ConversationResource`): `{id, object:"conversation",
created_at, metadata}`. The `id` is the client-known session handle:
reported as `Observation.provider_reference` and
`ResponsesEvidence.session_handle` on this attempt's own evidence document,
which carries no model output (no `response_id`, `output` or
`output_artifacts` -- "no model-visible content and no context_artifact").
A later `model_step` action names this handle explicitly
(`ResponsesParameters.session_handle`); this adapter never mints or reuses
a handle without it being passed back in.

Why a conversation at all: `POST /responses` has no idempotency key. If the
connection drops after the step's bytes leave, the client holds nothing
that can find the call unless it already holds the conversation id first.
This is the documented difference on which the provider was chosen, and
splitting session creation out as its own effect is exactly what lets the
controller persist that id *before* the model call is ever attempted
(P00-009: "Persist the session handle before the model call").

Outcome classification (`interpretPrepareSession`, `interpret.go`) mirrors
the model step's: a decoded 2xx is `succeeded`/`authoritative_success`; a
2xx this protocol cannot decode into a handle, or a 5xx, is `unknown` (bytes
may have created a session the reply cannot confirm); any other status is
`failed`/`authoritative_failure`; a transport failure is classified exactly
as `classifyNetworkError` would for a model step (dial/DNS failure only is
`not_sent`). Session creation is never billed by this pinned protocol, so
usage is `no_charge` on every disposition, including a failure. Unlike the
pre-split adapter, a rejected or undecodable conversation-create response
is now this attempt's own honest outcome -- never framed as "the model step
was never sent", because there is no model step in the same dispatch to
frame it against.

If session creation may have succeeded but the reply was lost (`unknown`),
the caller must not create a second, unlinked conversation by dispatching
another `prepare_session` for the same turn: `Adapter.Reconcile` refuses a
`prepare_session` dispatch outright (`capability_unsupported`, no call
made) because the API documents no way to list conversations or find one
by metadata -- there is no authoritative lookup this adapter can perform on
its own initiative. Recovering from a lost `prepare_session` acknowledgment
is an execution/controller decision (a fresh, deliberately re-admitted
`prepare_session` effect), never something this adapter retries itself.

Once a `prepare_session` effect has minted a handle, the persisted
`model_step` action names it directly
(`ResponsesParameters.session_handle`, required, non-empty) and the model
step above ("Request: the model step") sends it as `conversation`. The
model step is now a single physical call with no preparatory request
inside its own `Invoke` -- the request body fields are unchanged from the
pre-split adapter; only the handle's origin changed: it is a caller-
supplied input this attempt names, never one it mints for itself.

## Response: the model step

Documented `Response` object, fields read:

| Field | Use |
|---|---|
| `object` | must be `"response"`, else the body is undecodable and the outcome stays unknown |
| `id` | `response_id` in evidence and `physical_call.provider_reference`; `provider_usage_reference` |
| `status` | `completed`, `incomplete` → the step completed (charged, outputs final); `failed`, `cancelled` → authoritative failure; `in_progress`, `queued` → `accepted`, never promoted |
| `error` | `{code, message}` on a failed response → `error_code`, `error_message` |
| `incomplete_details.reason` | `max_output_tokens` → `finish_reason: length_limit`; `content_filter` → `refused`; `max_messages`, `steered` → `interrupted` |
| `output[]` | `message` items: each `output_text` content is one staged `model_text`; `refusal` content becomes `ModelOutput.refusal`. `function_call` items `{call_id, name, arguments}` are tool calls (see gaps). Other item types (`reasoning`, built-in tool calls) carry nothing this contract records |
| `usage` | `input_tokens`, `output_tokens` are the observed tokens; `input_tokens_details.cached_tokens` and `cache_write_tokens` are checked (see accounting) |
| `service_tier` | must be `"default"` for the profile's rates to apply |
| `conversation.id` | echoed as `continuation_reference` |

Finish reason with tool calls present is always `tool_calls`.

Error bodies (`ErrorResponse`): `{"error": {"type", "message", "param",
"code"}}`. On a non-2xx the adapter records `error_code: http_<status>` and
the sanitized `message`.

Status classification is by HTTP class, as the adapter core defines: 4xx
is an authoritative failure of this attempt; 5xx (the documented
`server_error`, `server_is_overloaded` 503) stays unknown because it
cannot rule out that the step ran; 429 (`rate_limit_exceeded`,
`slow_down`, credit exhaustion) is a 4xx and therefore failed. `Retry-After`
is not acted on: this adapter never retries.

## Reconcile: the documented authoritative lookup

This is a `model_step` reconciliation only. `Adapter.Reconcile` refuses a
`prepare_session` dispatch before any call (`capability_unsupported`): see
"Request: prepare_session" above -- there is no documented way to resolve a
lost conversation-create by lookup, only by dispatching a brand new,
deliberately re-admitted `prepare_session` effect.

`GET {conversations}/{handle}/items?limit=100&order=asc`, where `handle`
is `Dispatch.provider_key` (the conversation id the original attempt
reported). Documented reply (`ConversationItemList`): `{object:"list",
data:[...], has_more, first_id, last_id}`; item shapes are the same as
`Response.output`.

Interpretation:

- Any item present → the step completed ("Input items and output items
  from this response are automatically added to this conversation after
  this response completes"). Disposition `succeeded`, confirmation
  `authoritative_success`; assistant `message` texts are staged as
  `model_text`, `function_call` items are tool calls. Usage is **not**
  available from this lookup, so billing is `unknown` with the admitted
  worst case outstanding. `has_more: true` is flagged
  `reconcile_items_truncated`.
- No items → `unknown` with `error_code: reconcile_unresolved`. An empty
  list is consistent with a step still running or with one that never ran;
  the documentation gives no way to tell them apart, and nothing here can
  ever prove non-execution.
- Any non-2xx (a 404 included), transport failure, truncated or
  undecodable body → `unknown`; the lookup resolved nothing.

Retention: "the `/v1/conversations/items` endpoint ... application state
... 'Until deleted'" and "Conversation objects and items in them are not
subject to the 30 day TTL", so the handle does not expire under the
documented defaults.

### Zero Data Retention: a documented account constraint

Documented: under Zero Data Retention "the `store` parameter will always be
treated as `false`, even if the request attempts to set the value to
`true`." With `store` forced false the response is not retained and the
reconciliation path above has nothing to read. **ZDR must not be enabled
on the account this profile uses.** The adapter cannot detect ZDR from a
response; it is an operator obligation recorded here and in the
qualification record.

## Accounting

- Observed spend = `ceil(input_tokens × input_rate) + ceil(output_tokens ×
  output_rate)` from the profile's two rates, only when the response
  reports `usage`, `service_tier` is `"default"` (or absent) and no cache
  activity is reported. Otherwise the tokens are recorded exactly and the
  amount is reported `unknown` with `error_code: usage_unpriceable`.
- Reasoning tokens are inside `output_tokens` and inside the
  `max_output_tokens` ceiling ("including visible output tokens and
  reasoning tokens"), so the output side of the worst case is enforced by
  the provider.
- **Input bound at 272,000 tokens.** The founder-pinned pricing note quotes
  the published page: "Prompts with >272K input tokens are priced at 2x
  input and 1.5x output for the full request." A single flat rate under-
  reserves above that, and the frozen contract forbids a hard cap a profile
  cannot enforce. `validateProfile` therefore refuses a profile whose
  `max_input_tokens` exceeds 272,000 (`openaiFlatTierMaxInputTokens`).
  Lifting it means implementing the tier in the rate logic with its own
  qualification test.
- **Input token bound is a qualification claim, not a documented fact.**
  The API documents no way to cap input tokens per request. The protocol
  offers one bound — at most one billed input token per UTF-8 byte of
  model-visible text, plus 8 tokens per item, 8 per tool, 128 per tool
  call and 32 fixed — and claims it only when
  `capability_evidence.capabilities` contains `input_token_bound:utf8_bytes`,
  which live qualification records after comparing reported `input_tokens`
  against the bound. Without that string the protocol reports no bound, an
  `enforced` cost profile refuses to send, and an `advisory` profile sends
  with the profile ceiling priced. At runtime a reported `input_tokens`
  above the admitted bound is flagged `input_token_bound_exceeded`.
- Prompt caching is disabled (`prompt_cache_options.mode: explicit`)
  because the profile schema carries no cached-input or cache-write rate.
  The pinned note quotes cached input at $0.02 per 1M and cache writes at
  1.25× the input rate; neither can be expressed by `input_rate` alone.

## Constants the implementation hardcodes, and why each is allowed

| Constant | Value | Source |
|---|---|---|
| `openaiMinOutputTokens` | 16 | `max_output_tokens.minimum: 16` in the OpenAPI document |
| `openaiFlatTierMaxInputTokens` | 272,000 | pricing page as quoted above |
| `openaiReconcilePageLimit` | 100 | "Limit can range between 1 and 100" |
| `openaiServiceTier` | `default` | "standard pricing" tier |
| conversation id pattern | `^[A-Za-z0-9_-]{1,256}$` | defensive: the id is placed in a URL path; the document does not constrain the format, so anything outside this set is refused rather than escaped |

No account, model, price or endpoint is hardcoded; those come from the
profile.

## Where the documented API cannot give the adapter what the frozen contract demands

1. **RESOLVED by revision 3: two physical calls per Invoke.** The frozen
   contract says "PhysicalCallEvidence records exactly one physical network
   request per claimed attempt. No adapter adds preflight ... inside that
   request." The pre-split adapter created the conversation and sent the
   step inside one `Invoke` -- two physical requests behind a one-call-per-
   attempt contract (audit finding G27). P00-009 splits this into two
   separately admitted, journaled, single-call effects
   (`prepare_session`/`model_step`, `ResponsesParameters`'s
   `kind`-discriminated `oneOf`); each `Adapter.Invoke` now performs exactly
   the one physical call its dispatched kind describes. This entry is kept
   as the historical record of the gap revision 3 closes, not a live one.
2. **RESOLVED by revision 3, and actually improved: the handle is
   journaled before the model call, not just as far as one Invoke could
   reach.** The pre-split adapter could only stage the conversation id
   inside the SAME `Invoke` that also sent the step, so a crash between the
   two internal calls was invisible to the controller (the id existed only
   in adapter-local memory until `Invoke` returned). Splitting them into
   two dispatches lets the controller durably persist
   `prepare_session`'s own `Observation.provider_reference` -- the exact
   P00-009 requirement, "persist the session handle before the model
   call" -- through its own admit/claim/record transaction before a
   `model_step` is ever admitted. A crash between the two effects now
   leaves a durably recorded, successfully created session with no
   `model_step` dispatched against it yet (recoverable: the next attempt
   simply admits `model_step` naming that already-known handle), rather
   than an orphaned conversation the controller never learned about. A
   crash after `prepare_session`'s own response was sent but before its
   acknowledgment reached the controller is `unknown`, exactly like any
   other lost response (see "Request: prepare_session" above); it is never
   silently resolved by minting a second conversation.
3. **NEW gap: `ResponsesEvidence.session_handle` is required with
   `minLength` 1 on every disposition, including one that never minted a
   handle.** A `prepare_session` attempt that fails, times out or never
   left (`failed`/`unknown`/`not_sent`) has no handle to report, and this
   adapter never fabricates one ("never fabricated locally", AGENTS.md).
   Every sibling field this exact contract uses for an honestly-absent
   value -- `response_id`, `provider_reference`, `error_code` -- allows
   `minLength` 0; `session_handle` does not, which reads as an oversight
   in the frozen schema rather than an intended asymmetry. This build
   reports an empty string on those dispositions (the honest value) and
   accepts that the resulting evidence document does not itself validate
   against the frozen `ResponsesEvidence` schema on that narrow path; see
   `decodePrepareSessionEvidenceLenient` in `testhelpers_test.go` and the
   P13 PR, which reports this gap rather than inventing a seam around it
   (a coordinated revision-4 fix would relax `minLength` to 0, matching
   every sibling field).
5. **Reconciliation cannot prove non-execution.** Documented above; the
   contract already says a not-found "cannot establish non-execution", so
   `unknown` is retained, but it means a lost step with no items can never
   be released by this adapter.
6. **Usage is unavailable on reconcile.** `ConversationItemList` items carry
   no usage and no response id, so a reconciled success is billed
   `unknown` with the worst case outstanding. `GET /responses/{id}` would
   give usage but needs the response id, which a lost response never
   delivered.
7. **Cache and tier pricing.** `ResponsesProfile` has `input_rate` and
   `output_rate` only. Documented usage distinguishes cached and
   cache-written input tokens with their own prices, and service tiers are
   priced differently. Handled by disabling caching and pinning
   `service_tier: default`; any deviation reported by the provider makes
   the amount `unknown`. Revision-3 item: `cached_input_rate`,
   `cache_write_rate`.
8. **Tool proposals** (`interpret.go`, the `tool_proposal_mapping_unspecified`
   flag): unchanged from the base adapter; `function_call` items are
   decoded but no typed `ModelToolProposal` can be built without a
   tool-to-operation mapping.
9. **`max_output_tokens` below 16** is refused (`capability_unsupported`)
   because the API cannot enforce a smaller ceiling.

## Live qualification (performed 2026-09-19, predates the revision-3 split)

`tests/qualification/responses_live_test.go` on `origin/wave3/responses`
(commit `4aa219b`, banked 2026-09-19, not merged; that branch and P13 are
disjoint worktrees under `tests/qualification`, outside this card's
allowed writes, so it was read but not incorporated as code) ran this
protocol against `https://api.openai.com` with the founder's credential
resolved by reference from the operating system's secure credential store,
at source revision `34d291f` (dirty: the test itself), profile digest
`c91c14ce749b3f7ef807bb7d1b45266e790395e9a51802fcdd6d045d32a0f353`, model
`gpt-5.6-luna`. It predates this card's `prepare_session`/`model_step`
split: it drove the pre-split adapter's single `Invoke` (one conversation
create, then one response create). The two physical calls it observed are
byte-for-byte what `prepare_session` and `model_step` each still send under
the split -- `openaiProtocol.prepare`/`encode` are unchanged by P13 -- so
these facts about the live endpoint remain valid evidence; what changed is
only how Zatiti sequences and journals the two calls, not what either
carries over the wire. Observed:

- One text step: exactly 2 physical requests (`POST /v1/conversations`,
  then `POST /v1/responses`), both bodies unrewindable; `succeeded`,
  HTTP 200, `finish_reason: completed`, model text `"OK"`;
  `usage.input_tokens 22, output_tokens 5, total 27, reasoning_tokens 0`,
  priced at 11 micro-USD by the pinned rates and reported as
  `billing: observed, spent: 11`; `service_tier: "default"`,
  `store: true`, `conversation.id` equal to the minted handle.
- The staged step record named the conversation id before the step was
  sent and carried only `Content-Type`; no staged byte, evidence document,
  usage document or provider reference contained the credential.
- Reconcile by that handle: 1 request, `GET
  /v1/conversations/{id}/items?limit=100&order=asc`, HTTP 200, items
  `[system, user, assistant]`, the assistant `output_text` equal to the
  step's text, `succeeded`. Reconcile of a never-minted id: HTTP 404,
  `unknown`, `error_code: http_404`.
- One function-tool step: 2 requests, `succeeded`, one `function_call`
  item (`call_id`, `name`, `arguments` as a JSON string, `status:
  completed`), `finish_reason: tool_calls`, flagged
  `tool_proposal_mapping_unspecified`; `usage 74/22`, 42 micro-USD.
- Total spend: 53 micro-USD (0.000053 USD) for two model steps and two
  lookups.
- The byte-based input bound held on both steps (22 and 74 reported
  input tokens against far larger byte counts), so
  `input_token_bound:utf8_bytes` may be recorded in the profile's
  `capability_evidence`. Two prompts are thin evidence; the runtime flag
  `input_token_bound_exceeded` stays on so a later violation surfaces.

Where the live endpoint differed from the pinned OpenAPI document:

- The live `Response` object carries fields the pinned `Response` schema
  does not list: `billing` (`{"payer":"developer"}`), `tool_usage`
  (per-built-in-tool token counters), `frequency_penalty`,
  `presence_penalty`, and an echoed `store`. None carries a monetary
  amount: the API reports tokens, never a price, so the pinned rates are
  the only source of an amount. The adapter ignores unknown fields (Go's
  `encoding/json` decode target here, `openaiResponse`, only reads its own
  declared fields).
- Documented `Response` fields absent live: `output_text` (SDK-only by
  its own description), `prompt`, `prompt_cache_diagnostics`.
- Defaults echoed live and not otherwise documented here:
  `reasoning: {effort: "medium", context: "all_turns"}`,
  `prompt_cache_retention: "24h"`, `text.verbosity: "medium"`,
  `truncation: "disabled"`. Reasoning at `medium` bills reasoning tokens
  as output tokens (0 on these prompts); the `max_output_tokens` ceiling
  covers them, so the cost bound is unaffected.
- No disagreement in any field this protocol reads.

**Not re-run for this card.** Re-running it costs real money against the
founder's real credential; P13's own required tests are satisfied by the
package's synthetic-protocol and real-protocol-fixture suites
(`adapter_test.go`, `openai_test.go`), so this banked record is treated as
sufficient evidence for the wire-level facts above, not superseded or
re-verified here. **A fresh live run is still owed** once
`prepare_session`/`model_step` are wired through the real execution path
(P15/P23): the observations above are per-physical-call and unaffected by
the split, but no live run has yet exercised `prepare_session` and
`model_step` as two independently dispatched, independently journaled
effects end to end (crash-between-them recovery, in particular, is
proven only by this package's synthetic-protocol test,
`TestPrepareSessionAndModelStepAreSeparatelyDispatchedSingleCallEffects`,
never against the real endpoint). The record must be re-run when this
protocol revision, the model, or the profile digest changes; a profile
that claims `input_token_bound:utf8_bytes` without a passing live record
for its digest has no basis for it.

Every request and response shape above is exercised in `openai_test.go`
with fixtures shaped by the OpenAPI document. A fixture is not a claim that
the live endpoint behaves that way.

## Stateless gateway profiles (revision 12)

The local protocol revision strings are `openrouter-responses/stateless-v1` and
`experiential-responses/stateless-v1`. They identify the request and response
shapes implemented in `gateways.go`; they are not claims of live endpoint
qualification. The profile's capability evidence remains the qualification
record for a particular model, route and price.

Both gateways receive one `POST /v1/responses` request with the full persisted
context translated into Responses `input` items, its exact selected `model`,
tools translated to function tools, `max_output_tokens`, `stream:false`,
`background:false` and `store:false`. The OpenAI conversation field and
OpenAI-only cache and service-tier fields are removed. Neither gateway uses
`/conversations`, `session_handle`, `previous_response_id`, nor a continuation
lookup. Every invocation makes at most one HTTP request; `SupportsReconcile`
is false. A timeout or malformed response after send remains unknown.

OpenRouter pins `https://openrouter.ai/api/v1/responses`, sends the configured
provider allowlist (`only`), `allow_fallbacks:false` and
`require_parameters:true`. Configured privacy choices become the documented
`provider.data_collection` and `provider.zdr` constraints. A configured price
ceiling becomes `provider.max_price.prompt` and `.completion` in USD per
million tokens. Enforced-cost profiles require those ceilings and configured
per-token profile rates at least as high as the ceilings; returned decimal
`usage.cost` is parsed without floating point and is retained as
`provider_billed`. When provider cost is absent, observed tokens are priced at
the pinned ceiling and recorded as a bounded estimate. `X-OpenRouter-Metadata:
enabled` asks the gateway to include available route metadata; the complete
redacted response is staged, preserving any provider endpoint identity it
returns.

Experiential pins `https://api.experientiallabs.ai/v1/responses`. Each request
carries the documented `gateway.retry` limits of one attempt per route and one
total attempt, `backoff.type:none`, `gateway.routing.allow_fallbacks:false`,
and the configured route ID under `gateway.routing.route_id`. Privacy flags
are passed through the documented OpenRouter-compatible `provider` object.
Because the gateway cost is a platform charge and may be zero for BYOK while
the upstream provider is still billed, this adapter only accepts advisory-cost
Experiential profiles. Inline gateway cost is retained as `gateway_platform`
and does not replace the unknown upstream charge. The adapter treats a
reported ignored safety parameter or `x-gateway-replay-repair` disclosure as
an undecodable outcome; it never retries to obtain a cleaner response.

The primary references read 2026-09-24 are OpenRouter's [Responses create
reference](https://openrouter.ai/docs/api/api-reference/responses/create-responses)
and [provider routing guide](https://openrouter.ai/docs/guides/routing/provider-selection),
and Experiential's [waterfall controls](https://platform.experientiallabs.ai/docs/waterfall),
[OpenAI compatibility](https://platform.experientiallabs.ai/docs/openai-compatibility),
and [data controls](https://platform.experientiallabs.ai/docs/data-controls).
These hosted references can evolve. The implementation therefore makes no
live qualification or release claim by itself.
