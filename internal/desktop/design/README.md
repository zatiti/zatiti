# Zatiti desktop design study

Open `index.html` in a browser. No build, server, dependencies, fonts, or external requests are required. All records are synthetic; changes live in memory and reset on reload. This is an interactive visual prototype, not a functioning Zatiti client or release evidence.

The requested production direction is **Flutter for desktop**. HTML/CSS/JS here is a framework-independent design reference. Existing generated briefs specify Fyne; integration must coordinate that change before production implementation. This study changes no generated prompts, contracts, root dependencies, or acceptance criteria.

Screens: [conversation](previews/conversation.png), [exact review](previews/review.png), [details](previews/details.png), [light appearance](previews/light.png).

## Experience

Zatiti should feel like talking to a small, organized team. Start with Wren, the personal chief. Describe the outcome in ordinary language. The team handles coordination; the interface makes results and decisions easy to find.

The default screen has two areas: a nested conversation sidebar and a spacious conversation. One optional details panel contains durable work. The sidebar makes organization structure visible, as explicitly requested:

```text
Wren · Personal chief
  Marketing chief
    YouTube researcher
    Outreach
  Engineering chief
    Website reviewer · Quality chief
  Ledger · Operations chief
```

Each row opens that worker's conversation. Its separate chevron expands or collapses descendants without changing conversations. Keep the root chief pinned. Use stable organization order: background reasoning and routine reports never reorder the tree. Reserve unread state for meaningful messages; reserve amber indicators for actual pending decisions. Collapsed branches retain an indication of decisions below them. The global Needs you entry collects exact pending decisions across the tree.

Group chats appear as separate conversations, outside the organizational tree. A group is not an organization or permission boundary. Do not infer reporting relationships from group participants. Search displays full ancestry to disambiguate names. The organization filter includes descendants and retains ancestor rows for orientation. Production must use organization and worker identities, not display-name matching.

## Visual direction

Neutral charcoal surfaces, system sans-serif typography, soft corners, restrained mint for primary actions, and amber for decisions. Avoid persistent dashboards, technical identifiers, decorative gradients, and large blocks of saturated color. Worker initials are compact navigation aids, never the only indication of organization or status.

| Element | Reference |
| --- | --- |
| Canvas / sidebar / card | `#141515` / `#1a1b1b` / `#202222` |
| Primary text / secondary text | `#eeefeb` / `#a1a7a4` |
| Primary action / decision accent | `#b8d8c8` / `#e4c391` |
| Sidebar | 302 px; 316 px on large windows |
| Conversation measure | 770–810 px including horizontal padding |
| Optional details | 336 px; overlay on smaller windows |
| Spacing | 4, 8, 12, 16, 24, 32 px rhythm |
| Corners | 8 px controls, 12–14 px cards, 18 px dialogs |
| Typography | 12–15 px body/control text; 22–26 px conversational emphasis |

The prototype includes a light appearance. Flutter should also support the operating system preference and text scaling. Validate contrast, enlarged text, keyboard navigation, screen readers, and minimum pointer targets with the real desktop toolkit. Prototype screenshots do not establish accessibility qualification.

## Interactions to explore

1. Expand Marketing or Engineering and open any worker directly. Use the header breadcrumb or All chats filter to inspect organization scope.
2. Open **Review & decide**. Inspect the exact message, all 42 recipients, cost, expiry, and rule. Approve or decline once; the card and Needs you count update together. Approval never implies confirmed delivery.
3. Open **Details**. Inspect work, routines, files, memory, and access. Pause a responsibility or remove a memory preference from active recall.
4. Open the plus beside Zatiti. Create a worker, organization with chief, or group. Review the proposal before the demo creates it; new workers appear beneath their home chief.
5. Use **Connections & skills**, the profile/settings entry, or `Cmd/Ctrl+K` search.
6. Try **First-time setup**, **Try offline**, and **Light appearance** in the prototype toolbar. Offline decisions are disabled; messages are visibly unsent. Reconnection does not automatically deliver those drafts.

The prototype toolbar is a review aid, not production chrome. The idle composer has no fake voice feature. File selection explicitly says no upload occurred. Arbitrary messages produce an explicit demo response rather than pretending to invoke an AI.

## Keep power close, not constantly visible

| User-facing surface | What it preserves |
| --- | --- |
| Conversation | Requests, useful summaries, exact decisions, results; no stream of internal reasoning |
| Work | Durable tasks, owners, current states, acceptance evidence, required results |
| Routines | Ongoing responsibilities, schedule/timezone, independent pause and acknowledgments |
| Files | Outputs discoverable outside scroll; provenance and authorized sharing |
| Memory | Scope, source, freshness, lineage, removal from recall versus historical erasure |
| Access | Concrete capabilities, boundaries, evidence, spend/reservations, exact proposed changes |
| Needs you | Unresolved human decisions across organizations; no chat claim treated as approval |
| Connections & skills | Scoped external capabilities and versioned procedures; no implicit permission grants |

Terminology translates the operation into its consequence. “Review partner introductions” is the main label; operation identity/version/digest belongs in expandable evidence. For decisions, the consequence, destination, actual content, cost boundary, expiry, and applicability are not hidden behind a technical record.

Email sending is inherited from the supplied visual brief as an **illustrative future adapter**. It is not in the frozen qualified v1 capability set. Do not implement or advertise working email from this mockup. The same review component should serve the qualified repository action workflow using its exact controller payload.

## Production state behavior

These are design requirements for implementation; the prototype only simulates a subset.

| State | Presentation and action |
| --- | --- |
| Proposed definition | Summarize the staged worker/org/responsibility; no active badge |
| Awaiting decision | Exact current preview; decision bound to sealed plan/action |
| Submitting | Disable duplicate activation; retain original submission identity |
| Acknowledgment unknown | “Checking whether this was received”; query disposition before retry |
| Created/approved/paused | Show only after controller acknowledgment |
| Delivery accepted | Distinguish accepted request from delivered outcome |
| Outcome unknown | Explicit unresolved status, retained reservation, bounded reconciliation |
| Stale or expired review | Disable decision; fetch new preview and require a new exact decision |
| Offline | Label cached view and unsent drafts; no optimistic approval/pause claims |
| Reconnecting | Snapshot plus events; expired cursors require a fresh authorized snapshot |
| Permission or prerequisite missing | Explain the missing connection, access, or limit and provide the appropriate setup path |
| Task checks fail | Show “Needs changes” with observed failures; a worker's success statement cannot override checks |
| Empty workspace | Personal-chief chat with a ready composer, short examples, and setup only where required |

The current mock does not implement real stale-review races, unknown acknowledgments, replay, secure credential entry, skill installation, model execution, memory reconciliation, or persistence. It does not claim that providers, platforms, or adapters have been qualified. Details for newly created demo workers and engineering conversations are intentionally incomplete fixtures, not backend records.

## Flutter handoff

Use native Flutter widgets for the production client, with a restrained custom theme rather than an embedded browser. Suggested component boundaries are `WorkspaceShell`, `OrganizationConversationTree`, `ConversationView`, `MessageComposer`, `ActionReviewDialog`, `WorkerDetailsPanel`, and `WorkspaceSettings`. Use split layout constraints for wide windows and dismissible overlay navigation/details on narrow windows. Keep the selected conversation, expanded tree state, scroll position, and per-conversation drafts independent.

Use typed view states keyed by immutable identities. Derive badges, cards, and detail entries from the same controller snapshot. Keep conversation prose separate from disposition records. Direct creation controls and conversational proposals must render the same compiler plan objects. Native dialogs need focus restoration, escape behavior, semantic labels, and keyboard traversal. The tree requires accessible disclosure controls, selected states, full ancestry announcements, and keyboard navigation. Pause/stop controls must identify scope and retained unknown effects.

**Integration change request:** replace the Fyne desktop host with Flutter while preserving the controller, policy, execution, evidence, and exact-review semantics. Affected owners/callers: desktop host, `cmd/zatiti-desktop` entrypoint, shared client/transport and credential plumbing, installation/packaging, dependency foundation, desktop integration and platform qualification. The existing `desktop.New(Config, contract.Operator)` Go API cannot directly serve a separate Dart UI without a coordinated boundary decision. Prefer reusing the authenticated local controller transport and versioned wire contracts; design the Flutter credential/transport bridge explicitly. Coordinate generated source changes through the authored spec inputs and renderer. Do not quietly add Dart dependencies to the root Go assignment or treat a WebView wrapper as the agreed Flutter implementation.

## Review limitations

Sample data is deliberately fictional and contains only reserved example addresses. No supplied reference screenshot or personal data is copied into the repository. No credentials are requested. This design adds files only beneath `internal/desktop/design/`; existing work in other directories is untouched.

Browser validation performed September 17, 2026: a local Playwright/Chromium run exercised tree expansion, direct conversations, exact approval and count update, disabled offline approval, unsent drafts retained after reconnection, all details tabs, pause acknowledgement, memory removal, descendant filtering, worker creation under Marketing, organization/chief creation, creation under the new organization, independent group creation, setup, search, dark/light appearance, and a 390 px narrow viewport. The run passed with no page JavaScript errors; the narrow viewport had no horizontal document overflow. Screenshots at 1440 × 1000 were visually inspected. `node --check` also passed. These checks validate this browser study only; Flutter, real controller behavior, accessibility, and platform packaging remain unqualified.
