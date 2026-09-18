// Typed view states. Each production state from the design of record is a
// value here, with its presentation rules in one place.

import 'snapshot.dart';

/// The connection to the controller.
enum ConnectionPhase {
  /// First snapshot not loaded yet.
  connecting,
  online,

  /// The cached view is shown and labeled; nothing claims to be current.
  offline,

  /// Fetching a fresh snapshot, then events. Drafts stay unsent.
  reconnecting,
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

enum RoutinePhase { active, submitting, acknowledgmentUnknown, paused }

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
