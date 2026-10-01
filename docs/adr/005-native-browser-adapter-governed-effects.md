# ADR 005: Native browser adapter importing ferro, with governed effects

## Status
Accepted

## Date
2026-09-28

## Context
Page actions must be governed effects:
- policy review and a digest-bound human review, with approval only in the desktop (decide.go:135 denies non-human principals);
- honest dispositions and reconciliation without resending.
The effects dispatch sends the full governed Action; a not_sent observation on a claimed attempt becomes outcome_unknown (effects handlers.go:1147-1167); and a reconcile dispatch carries a fresh AttemptID (handlers.go:1563-1572). controller/reconcile.go re-admits outcome_unknown operations with backoff (:51-61, :73) and calls Reconcile once per admitted read, never Invoke.
Ferro supplies the snapshot and selector model; its public page and bridge packages ship in v0.2.0.

## Decision
Tools c7 browser-read, c8 browser-navigate and c9 browser-act. internal/adapters/browser imports ferro/page, and the internal/wshub hub imports ferro/bridge.

One Invoke is one in-process contract.BrowserChannel.Exchange, which is one extension command.

Destinations: read and act use parameters.origin; navigate uses origin(url).

Preconditions, in order: digest (Blobs.Open and re-hash), normalized identity, page, same-origin links and form actions, sensitive fields, generation. Identity normalization: tag and role exact; name and text NFC with whitespace collapsed; href and form_action URL-normalized and compared exactly. Target and page from the model are compared after normalization, so the digest plus ref identify the element.

The extension does a CDP hit-test before each click and verifies focus before each key press; key accepts named keys only.

The journal is write-ahead, with an epoch and an eviction watermark, and is indexed by attempt and by operation_id.

Outcome table:
- authoritative refusals before or at execution (origin_denied, no_tab, stale_target, sensitive_field_refused, expired, pairing_changed or controller_draining before hand-out, refused receipt) -> failed with a code;
- uncertainty after hand-out -> unknown with ProviderReference 'browser/v1:<attempt>:<boot_id>:<generation>:<epoch>:<delivered_at_ms>:<deadline_ms>'.

Reconcile parses Dispatch.ProviderKey, never the fresh AttemptID, and reads receipts only:
- completed -> succeeded (failed for a stop);
- refused -> failed;
- received-not-started past deadline plus grace -> failed;
- never_delivered in the same boot -> failed;
- not_found in the same epoch, above the watermark and past deadline plus grace -> failed;
- otherwise unknown.
With an empty ProviderKey (a crash mid-Invoke), Reconcile queries by Dispatch.OperationID and settles only on a single positive record.

Across a controller restart the new boot_id empties the ledger, so never_delivered is unavailable and only journal evidence settles an outcome. Offline or epoch-changed operations stay outcome_unknown under controller backoff. Drain records honest outcomes before ctl.Stop.

Review floor: c8 and c9 are in the default review class with an exact-capability floor, plus worker-scope human_required rules. Approval happens only in the desktop.

Browser-worker isolation: a browser worker holds only c7, c8, c9 and its model tool.

Revalidation: the owner runs `zatiti browser status --revalidate` (connection.validate), because the paired client_agent is fenced away from connection.*; automation is planned in E11.

The browser worker uses group conversations. RunDriver is deferred. The residual deceptive-label risk is documented.

## Consequences
- Every page action has an operation row, review, evidence and an honest outcome, with no resend path.
- Review fatigue is expected (E12 plans standing allows).
- Restart and browser-restart windows can leave operations outcome_unknown indefinitely, and that is visible, never guessed.
- The adapter depends on ferro v0.2.0 being pinned (T1.5, T2.4b; sirerun/ferro is already public).
