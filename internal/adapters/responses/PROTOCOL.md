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

## Request: the preparatory call (conversation first)

Before the model step, `POST {conversations}` with body
`{"metadata": {"zatiti_operation_id": ..., "zatiti_attempt_id": ...}}`,
where `{conversations}` is the profile endpoint with its final path segment
`responses` replaced by `conversations` (`https://api.openai.com/v1/conversations`).
The profile's `enforcement.provider_destinations` must cover it; an
origin-wide grant (`https://api.openai.com`) is the simplest.

Documented reply (`ConversationResource`): `{id, object:"conversation",
created_at, metadata}`. The `id` is the client-known handle: it is staged
in the model step's request record before the step is sent, reported as
`Observation.provider_reference`, and returned as
`ModelOutput.continuation_reference` in evidence.

Why: `POST /responses` has no idempotency key. If the connection drops
after the step's bytes leave, the client holds nothing that can find the
call unless it already holds the conversation id. This is the documented
difference on which the provider was chosen.

The step's translation (context to items, tools) is validated before the
preparatory call is sent, so a refusal never follows a sent request.

If the preparatory call fails in any way (transport error, non-2xx, or an
undecodable reply), the model step is not sent: the observation is
`not_sent` with `request_sent: "no"`, `error_code: prepare_*`, the
preparatory request record as this attempt's `request_context`, and its
response staged as `provider_response`. Nothing is billed and nothing is
journaled upstream that a later step would reuse.

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

1. **Two physical calls per Invoke.** The frozen contract says
   "PhysicalCallEvidence records exactly one physical network request per
   claimed attempt. No adapter adds preflight ... inside that request." The
   only documented way to make a dropped `POST /responses` reconcilable is
   a conversation created beforehand, which is a second request. The
   adapter does it (`adapter.go`, `Invoke`, the `prep != nil` block) and
   records it honestly: both request records are staged with purpose
   `context`, both responses with purpose `provider_response`, and the
   single `physical_call` describes the model step. Revision-3 item.
2. **The handle is journaled only as far as the adapter can reach.** The
   contract's "journal before dispatch" is satisfied by staging the record
   that names the conversation id before the step is sent; the controller
   learns the id when Invoke returns. A process crash between the two calls
   leaves an orphaned conversation upstream (harmless: nothing is journaled
   under it and nothing billed); a crash after the step is sent loses the
   handle and the attempt is permanently unknown, because the API documents
   no way to list conversations or find one by metadata.
3. **Reconciliation cannot prove non-execution.** Documented above; the
   contract already says a not-found "cannot establish non-execution", so
   `unknown` is retained, but it means a lost step with no items can never
   be released by this adapter.
4. **Usage is unavailable on reconcile.** `ConversationItemList` items carry
   no usage and no response id, so a reconciled success is billed
   `unknown` with the worst case outstanding. `GET /responses/{id}` would
   give usage but needs the response id, which a lost response never
   delivered.
5. **Cache and tier pricing.** `ResponsesProfile` has `input_rate` and
   `output_rate` only. Documented usage distinguishes cached and
   cache-written input tokens with their own prices, and service tiers are
   priced differently. Handled by disabling caching and pinning
   `service_tier: default`; any deviation reported by the provider makes
   the amount `unknown`. Revision-3 item: `cached_input_rate`,
   `cache_write_rate`.
6. **Tool proposals** (`interpret.go`, the `tool_proposal_mapping_unspecified`
   flag): unchanged from the base adapter; `function_call` items are
   decoded but no typed `ModelToolProposal` can be built without a
   tool-to-operation mapping.
7. **`max_output_tokens` below 16** is refused (`capability_unsupported`)
   because the API cannot enforce a smaller ceiling.

## Qualification seam (not performed)

Live qualification against `https://api.openai.com` needs the founder's
credential and is a separate step (tests/qualification). Its record must
pin `adapter_version`, `source_revision`, this protocol revision, the
profile digest and, if the byte bound held across the qualification
prompts, the capability string `input_token_bound:utf8_bytes`. Nothing in
this package fakes that step: a profile without it simply cannot claim an
enforced hard cap.

Every request and response shape above is exercised in `openai_test.go`
with fixtures shaped by the OpenAPI document. A fixture is not a claim that
the live endpoint behaves that way.
