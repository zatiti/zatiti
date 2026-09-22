// Typed view states. Each production state from the design of record is a
// value here, with its presentation rules in one place.

import 'snapshot.dart';
import 'workspace_source.dart' show PlanOutcome;

/// The connection to the controller.
enum ConnectionPhase {
  /// First snapshot not loaded yet.
  connecting,
  online,

  /// The cached view is shown and labeled; nothing claims to be current.
  offline,

  /// Fetching a fresh snapshot, then events. Drafts stay unsent.
  reconnecting,

  /// The controller answered but does not serve an operation this client
  /// needs. Nothing is guessed; the message names what is missing.
  unsupported,
}

/// Where one review stands, combining the controller's record with what this
/// client has submitted and not yet heard back about.
enum ReviewPhase {
  awaitingDecision,
  submitting,
  acknowledgmentUnknown,
  approved,
  declined,
  deliveryAccepted,
  delivered,
  outcomeUnknown,
  effectFailed,
  stale,
  expired,
  invalidated,
  prerequisiteMissing;

  /// Whether approve and decline may be activated, connection permitting.
  bool get allowsDecision => this == ReviewPhase.awaitingDecision;

  /// Whether the review still needs the person: it counts toward Needs you
  /// and lights the amber indicator.
  bool get needsYou => switch (this) {
    ReviewPhase.awaitingDecision ||
    ReviewPhase.submitting ||
    ReviewPhase.acknowledgmentUnknown ||
    ReviewPhase.stale ||
    ReviewPhase.prerequisiteMissing => true,
    _ => false,
  };

  /// The status line. Wording follows the design's state table.
  String get label => switch (this) {
    ReviewPhase.awaitingDecision => 'Your decision',
    ReviewPhase.submitting => 'Sending your decision…',
    ReviewPhase.acknowledgmentUnknown => 'Checking whether this was received',
    ReviewPhase.approved => 'Approved · delivery not confirmed',
    ReviewPhase.declined => 'Declined · nothing will be sent',
    ReviewPhase.deliveryAccepted => 'Request accepted · outcome not confirmed',
    ReviewPhase.delivered => 'Done · outcome confirmed',
    ReviewPhase.outcomeUnknown =>
      'Outcome unknown · reservation kept while this is reconciled',
    ReviewPhase.effectFailed => 'Approved · the action failed',
    ReviewPhase.stale => 'This request changed · review the new version',
    ReviewPhase.expired => 'Expired · no decision was recorded',
    ReviewPhase.invalidated => 'Withdrawn · no decision is needed',
    ReviewPhase.prerequisiteMissing => 'Something is missing before you decide',
  };
}

/// The phase a bare controller record implies, before any local submission.
ReviewPhase phaseOfRecord(ReviewEntry review, DateTime now) {
  switch (review.state) {
    case ReviewRecordState.pending:
      return review.expiresAt.isAfter(now)
          ? ReviewPhase.awaitingDecision
          : ReviewPhase.expired;
    case ReviewRecordState.rejected:
      return ReviewPhase.declined;
    case ReviewRecordState.expired:
      return ReviewPhase.expired;
    case ReviewRecordState.invalidated:
      return ReviewPhase.invalidated;
    case ReviewRecordState.approved:
      return switch (review.effect) {
        EffectState.notStarted || EffectState.executing => ReviewPhase.approved,
        EffectState.deliveryAccepted => ReviewPhase.deliveryAccepted,
        EffectState.outcomeUnknown => ReviewPhase.outcomeUnknown,
        EffectState.succeeded => ReviewPhase.delivered,
        EffectState.failed => ReviewPhase.effectFailed,
      };
  }
}

/// A review as every surface shows it: the card, the Needs you list, the Work
/// tab and the dialog read this one object.
class DecisionView {
  const DecisionView({
    required this.review,
    required this.phase,
    required this.proposerName,
    required this.ancestry,
    required this.canDecide,
    this.blockedReason,
    this.note,
  });

  final ReviewEntry review;
  final ReviewPhase phase;
  final String proposerName;
  final String ancestry;

  /// True only when the phase allows a decision, the controller is reachable
  /// and the exact content can be shown.
  final bool canDecide;

  /// Why [canDecide] is false, in words for the person.
  final String? blockedReason;

  /// An extra line from the last attempt, for example "not received".
  final String? note;
}

/// One row of the organization conversation tree.
class TreeNodeView {
  const TreeNodeView({
    required this.worker,
    required this.depth,
    required this.hasChildren,
    required this.expanded,
    required this.selected,
    required this.pinned,
    required this.showsDecisionIndicator,
    required this.decisionsBelow,
  });

  final WorkerEntry worker;
  final int depth;
  final bool hasChildren;
  final bool expanded;
  final bool selected;
  final bool pinned;

  /// Amber dot: this worker has a pending decision, or the branch is
  /// collapsed and something beneath it does.
  final bool showsDecisionIndicator;

  /// Pending decisions in hidden descendants, for the accessible label.
  final int decisionsBelow;

  /// The full announcement for assistive technology.
  String get semanticLabel {
    final parts = <String>[
      worker.name,
      worker.role,
      if (worker.ancestry.isNotEmpty) 'in ${worker.ancestry}',
      if (showsDecisionIndicator)
        decisionsBelow > 0 ? 'decisions waiting below' : 'needs your decision',
    ];
    return parts.join(', ');
  }
}

enum OutgoingPhase {
  sending,

  /// Kept locally and visibly unsent. Never sent without the person asking.
  unsentDraft,
  acknowledgmentUnknown,
  refused,
}

/// A message the person wrote that the controller has not acknowledged.
class OutgoingMessage {
  OutgoingMessage({
    required this.localId,
    required this.conversationId,
    required this.body,
    required this.phase,
    this.note,
  });

  final String localId;
  final ConversationId conversationId;
  final String body;
  OutgoingPhase phase;
  String? note;

  String get label => switch (phase) {
    OutgoingPhase.sending => 'Sending…',
    OutgoingPhase.unsentDraft => 'Unsent draft · not delivered',
    OutgoingPhase.acknowledgmentUnknown => 'Checking whether this was received',
    OutgoingPhase.refused => 'Not sent',
  };
}

/// What a conversation's status line shows about the turn in progress, all
/// derived from real controller state (delivery phase, message timestamps,
/// an open review, a workspace prerequisite) — never a fabricated local
/// response. There is no controller-side "turn state" projection this
/// client can read (the catalog exposes none), so this is assembled from
/// what public operations actually return:
///
/// - A `clarify` or final `reply` decision carries no separate wire state
///   of its own: both simply become an ordinary incoming message, and this
///   client renders them exactly like any other message, not as a special
///   card.
/// - [reviewWaiting] and [blockedSetup] reuse the same review/prerequisite
///   records the decision cards and Needs you list already read.
enum TurnStatus {
  /// This window's own message is being sent; not yet acknowledged.
  acknowledging,

  /// Bytes may have reached the controller; nobody knows yet.
  acknowledgmentUnknown,

  /// Kept locally, not delivered, because of a connection failure.
  unsent,

  /// The controller refused this window's own message.
  refused,

  /// Delivered, and nothing newer has arrived from the worker since. A
  /// plain fact about timestamps, never a claim about what the worker is
  /// doing internally.
  waitingForReply,

  /// A decision this worker (or a worker beneath it) proposed is open.
  reviewWaiting,

  /// A workspace-wide prerequisite (not initialized, paused, maintenance)
  /// blocks new work everywhere, this conversation included.
  blockedSetup;

  String get label => switch (this) {
    TurnStatus.acknowledging => 'Sending…',
    TurnStatus.acknowledgmentUnknown => 'Checking whether this was received',
    TurnStatus.unsent => 'Unsent · not delivered',
    TurnStatus.refused => 'Not sent',
    TurnStatus.waitingForReply => 'Waiting for a reply',
    TurnStatus.reviewWaiting => 'A decision is open',
    TurnStatus.blockedSetup => 'Setup is needed before this can continue',
  };
}

enum RoutinePhase { active, submitting, acknowledgmentUnknown, paused }

/// Which draft/plan/review/apply flow a dialog is driving. Each kind has at
/// most one flow in progress at a time (one modal dialog at a time).
enum DraftFlowKind { organization, worker, responsibility }

/// Where a draft/plan/review/apply flow (new organization, new worker, new
/// responsibility) stands. Nothing named by [DraftFlowView.createdId] is
/// real until [applied].
enum DraftFlowPhase {
  /// The dialog is open; nothing has been sent.
  composing,

  /// Staging the draft (the `organization.create`/`worker.create`/
  /// `responsibility.create` call).
  staging,
  stagingUnknown,

  /// Sealing the candidate (`configuration.plan`).
  planning,
  planningUnknown,

  /// Planned and clean: a person can review and apply it.
  ready,

  /// Planned, but the compiler found something that must be resolved
  /// elsewhere first (an authority or decision requirement, a diagnostic).
  blocked,

  /// Activating the plan (`configuration.apply`).
  applying,
  applyingUnknown,

  /// Active. [DraftFlowView.createdId] now names a real resource.
  applied,

  /// A step was refused. [DraftFlowView.note] explains.
  failed;

  bool get isBusy => switch (this) {
    staging || planning || applying => true,
    _ => false,
  };

  bool get needsCheck => switch (this) {
    stagingUnknown || planningUnknown || applyingUnknown => true,
    _ => false,
  };
}

/// The draft/plan/review/apply flow as a dialog renders it: one object, one
/// place, so the "New organization"/"New worker"/"New responsibility"
/// dialogs share the same review step.
class DraftFlowView {
  const DraftFlowView({
    this.phase = DraftFlowPhase.composing,
    this.plan,
    this.note,
    this.createdId,
  });

  final DraftFlowPhase phase;
  final PlanOutcome? plan;
  final String? note;

  /// The organization/worker/responsibility id, once [phase] is [applied].
  final String? createdId;
}

/// Where a bounded task's own creation/start/delegation stands.
enum TaskFlowPhase {
  composing,
  creating,
  creatingUnknown,
  created,
  starting,
  startingUnknown,
  started,
  failed,
}

class TaskFlowView {
  const TaskFlowView({this.phase = TaskFlowPhase.composing, this.note});
  final TaskFlowPhase phase;
  final String? note;

  bool get isBusy => switch (phase) {
    TaskFlowPhase.creating || TaskFlowPhase.starting => true,
    _ => false,
  };
}

class RoutineView {
  const RoutineView({
    required this.routine,
    required this.phase,
    required this.canPause,
    this.note,
  });

  final RoutineEntry routine;
  final RoutinePhase phase;
  final bool canPause;
  final String? note;

  String get label => switch (phase) {
    RoutinePhase.active => 'Active',
    RoutinePhase.submitting => 'Pausing…',
    RoutinePhase.acknowledgmentUnknown => 'Checking whether this was received',
    RoutinePhase.paused => 'Paused',
  };
}

/// Where a claim's own retraction stands, before the acknowledgment rule
/// lets this view claim anything happened. Mirrors [RoutinePhase]'s shape.
enum ClaimActionPhase { submitting, acknowledgmentUnknown, requested }

/// A memory claim as the Memory tab renders it: the controller's own record,
/// plus whatever this window has submitted toward retracting it.
class MemoryClaimView {
  const MemoryClaimView({required this.claim, this.phase, this.note});

  final MemoryEntry claim;
  final ClaimActionPhase? phase;

  /// The last known retraction job status, once one was requested.
  final String? note;

  String get statusLabel => switch (phase) {
    ClaimActionPhase.submitting => 'Retracting…',
    ClaimActionPhase.acknowledgmentUnknown =>
      'Checking whether this was received',
    ClaimActionPhase.requested => note ?? 'Retraction requested',
    null => claim.active ? 'Active' : 'Retracted',
  };

  /// True only when nothing is in flight, the claim is still active, and the
  /// binding it came through actually grants `retract`.
  bool get canRetract => phase == null && claim.active && claim.canRetract;
}
