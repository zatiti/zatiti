// The boundary between the view-state layer and wherever workspace data
// comes from: the real controller client, or the labeled in-memory demo.

import 'snapshot.dart';

enum SourceKind { controller, demo }

enum DecisionChoice { approve, decline }

/// One intended mutation, prepared once. The handle keeps the submission
/// identity, so a retry cannot mint a new one.
abstract class PendingSubmission {
  /// A short description for diagnostics; never a credential.
  String get description;
}

/// Nothing was sent. The same [PendingSubmission] may be submitted again.
class SourceUnavailable implements Exception {
  const SourceUnavailable(this.message);
  final String message;
  @override
  String toString() => 'SourceUnavailable: $message';
}

/// The request may have been received; nobody knows. The submission must be
/// resolved through [WorkspaceSource.resolve] before anything else.
class AcknowledgmentUnknown implements Exception {
  const AcknowledgmentUnknown(this.message);
  final String message;
  @override
  String toString() => 'AcknowledgmentUnknown: $message';
}

enum RefusalKind {
  /// The review changed since this preview was fetched.
  stale,

  /// The review expired.
  expired,

  /// A connection, access or limit is missing.
  prerequisiteMissing,

  /// Any other authoritative refusal.
  other,
}

/// The controller answered and refused. Authoritative: nothing is unknown.
class SourceRefusal implements Exception {
  const SourceRefusal(this.kind, this.message);
  final RefusalKind kind;
  final String message;
  @override
  String toString() => 'SourceRefusal(${kind.name}): $message';
}

/// What a lookup established about an unknown acknowledgment.
sealed class Resolution {
  const Resolution();
}

/// The controller has the command and acknowledged it.
final class ResolvedAcknowledged extends Resolution {
  const ResolvedAcknowledged();
}

/// The controller has the command and refused it.
final class ResolvedRefused extends Resolution {
  const ResolvedRefused(this.refusal);
  final SourceRefusal refusal;
}

/// The command never committed. An explicit retry with the same submission
/// is safe; nothing retries by itself.
final class ResolvedNotReceived extends Resolution {
  const ResolvedNotReceived();
}

abstract interface class WorkspaceSource {
  SourceKind get kind;

  /// Shown in the workspace chrome. A demo source must say it is a demo.
  String get label;

  /// Fetches a fresh authorized snapshot.
  Future<WorkspaceSnapshot> loadSnapshot();

  /// Reports whether anything changed since the last snapshot, replaying
  /// events after the retained cursor. An expired cursor reports true: the
  /// caller fetches a fresh snapshot and never fills the gap by guessing.
  Future<bool> hasChanges();

  /// Fetches the current preview of one review.
  Future<ReviewEntry> refreshReview(ReviewId id);

  /// Freezes a decision bound to the exact version and digest of [review].
  PendingSubmission prepareDecision(ReviewEntry review, DecisionChoice choice);

  /// Freezes one message, including its message identity.
  PendingSubmission prepareMessage(ConversationId conversation, String body);

  /// Freezes a pause bound to the routine's current version.
  PendingSubmission preparePause(RoutineEntry routine);

  /// Sends [submission] once. Completes only on controller acknowledgment.
  /// Throws [SourceUnavailable], [AcknowledgmentUnknown] or [SourceRefusal].
  Future<void> submit(PendingSubmission submission);

  /// Looks up an unknown acknowledgment. Throws [SourceUnavailable] or
  /// [AcknowledgmentUnknown] when the lookup itself gets no answer.
  Future<Resolution> resolve(PendingSubmission submission);
}
