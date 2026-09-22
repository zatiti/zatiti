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

/// A [PendingSubmission] whose acknowledgment carries data the next step of
/// a multi-step flow (draft → plan → review → apply; create → start) needs,
/// for example the identity `configuration.plan` binds to. [result] is set
/// once [WorkspaceSource.submit] or [WorkspaceSource.resolve] establishes the
/// mutation actually committed — never guessed, never set on a refusal or an
/// unresolved unknown acknowledgment.
abstract class ResourceSubmission<T> implements PendingSubmission {
  T? get result;
}

/// What `organization.create`/`worker.create`/`responsibility.create` staged:
/// the still-inactive draft, and the definition's own identity (real only
/// once the draft is applied).
class DraftedResource {
  const DraftedResource({
    required this.draftId,
    required this.draftVersion,
    required this.resourceId,
    this.conversationTargetId,
  });

  /// `configuration.plan`'s `draft_id`.
  final String draftId;

  /// `configuration.plan`'s `expected_version`.
  final int draftVersion;

  /// The organization/worker/responsibility id this draft will activate.
  final String resourceId;

  /// The worker id a direct conversation should open with once this draft
  /// is applied: the new chief for an organization, the new worker itself,
  /// or null for a responsibility (nothing to talk to).
  final String? conversationTargetId;
}

/// What `configuration.plan` sealed: the candidate `configuration.apply`
/// commits, plus what a person must see before approving it.
class PlanOutcome {
  const PlanOutcome({
    required this.planId,
    required this.baseRevision,
    required this.candidateDigest,
    required this.diagnostics,
    required this.pendingRequirements,
  });

  /// `configuration.apply`'s `plan_id`.
  final String planId;

  /// `configuration.apply`'s `base_revision`.
  final int baseRevision;

  /// `configuration.apply`'s `candidate_digest`.
  final String candidateDigest;

  /// One line per diagnostic, worst severity first.
  final List<String> diagnostics;

  /// Authority or decision requirements standing in the way of `apply`, in
  /// words. Empty means the plan is clean.
  final List<String> pendingRequirements;

  bool get isClean => diagnostics.isEmpty && pendingRequirements.isEmpty;
}

/// What `task.create`/`.start`/`.delegate`/`.accept` returns: enough to show
/// the task without a full reload, and to bind the next call's
/// `expected_version`.
class TaskOutcome {
  const TaskOutcome({
    required this.id,
    required this.version,
    required this.state,
  });

  final String id;
  final int version;
  final String state;
}

/// What `conversation.create` returns: enough to select the new chat.
class ConversationOutcome {
  const ConversationOutcome({required this.id, required this.version});
  final String id;
  final int version;
}

/// What `memory.retract` returns: the `Job` identity and its status at
/// acknowledgment. Retraction is asynchronous — "acknowledged" here means
/// the controller accepted the retraction request, never that the claim is
/// already gone; [jobStatus] is the job's own state in words (`Queued`,
/// `In progress`, `Done`, …), read directly from the controller's `Job`,
/// never a locally invented phase.
class MemoryRetractOutcome {
  const MemoryRetractOutcome({required this.jobId, required this.jobStatus});
  final String jobId;
  final String jobStatus;
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

/// The controller does not serve an operation this client needs, at the
/// version it needs. A named state, never a guessed call.
class SourceUnsupported implements Exception {
  const SourceUnsupported(this.message);
  final String message;
  @override
  String toString() => 'SourceUnsupported: $message';
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

  /// Reads the full authorized message history for [conversation], oldest
  /// first: the real reply history, never a fabricated local response and
  /// never limited to what this window happens to remember. Called lazily,
  /// only for a conversation the person has actually opened.
  Future<List<ChatMessage>> loadMessages(ConversationId conversation);

  /// Freezes a decision bound to the exact version and digest of [review].
  PendingSubmission prepareDecision(ReviewEntry review, DecisionChoice choice);

  /// Freezes one message, including its message identity.
  PendingSubmission prepareMessage(ConversationId conversation, String body);

  /// Freezes a pause bound to the routine's current version.
  PendingSubmission preparePause(RoutineEntry routine);

  /// Freezes a retraction of [claim], bound to the exact claim version this
  /// view saw. Only offered when [MemoryEntry.canRetract] is true.
  ResourceSubmission<MemoryRetractOutcome> prepareMemoryRetract(
    MemoryEntry claim,
    String reason,
  );

  /// Looks up a retraction job's current status. Never a background poll:
  /// called only when the person asks to check again.
  Future<String> checkMemoryJob(String jobId);

  // ---- organization/worker/group/task/responsibility creation ------------
  //
  // Every mutation below follows the same prepare/submit/resolve contract as
  // the ones above: nothing is active or sent until [submit] is called, a
  // retry after [AcknowledgmentUnknown] reuses the same frozen submission,
  // and [ResourceSubmission.result] is set only once the controller's own
  // acknowledgment (direct or through [resolve]) establishes it.

  /// Stages an organization and its chief as one draft. Not active until the
  /// returned draft is planned and applied.
  ResourceSubmission<DraftedResource> prepareCreateOrganization({
    required String key,
    required String name,
    String? parentOrganizationId,
    required String chiefKey,
    required String chiefName,
    required String chiefPurpose,
    required String chiefInstructions,
  });

  /// Stages a worker under an existing organization as one draft.
  ResourceSubmission<DraftedResource> prepareCreateWorker({
    required String organizationId,
    required String key,
    required String name,
    required String purpose,
    required String instructions,
  });

  /// Stages an ongoing responsibility for a worker as one draft. Decided
  /// manually: see `api/acceptance.dart`. [verifier] must be one
  /// [loadTrustedVerifiers] actually returned.
  ResourceSubmission<DraftedResource> prepareCreateResponsibility({
    required String workerId,
    required String outcome,
    required List<String> triggers,
    required int minIntervalSeconds,
    required VerifierIdentity verifier,
    required DateTime rootDeadline,
    String currency = 'XXX',
  });

  /// Seals a draft into a candidate a person can review before applying.
  ResourceSubmission<PlanOutcome> preparePlan({
    required String draftId,
    required int expectedVersion,
  });

  /// Activates a plan's candidate. Only after this is the staged
  /// organization/worker/responsibility real and visible elsewhere.
  ResourceSubmission<PlanOutcome> prepareApplyPlan(PlanOutcome plan);

  /// Opens a direct conversation with a newly created worker so the person
  /// can talk to it. [humanPrincipalId] is derived from an existing direct
  /// conversation's own participants, never guessed (see
  /// `live_source.dart`'s class doc on why this client has no
  /// `identity.current`).
  ResourceSubmission<ConversationOutcome> prepareOpenDirectConversation({
    required String humanPrincipalId,
    required String workerId,
    required String title,
  });

  /// Creates a group conversation with the given participants. Immediate:
  /// group chats carry no org/grant/memory permissions, so nothing is
  /// staged through the compiler.
  ResourceSubmission<ConversationOutcome> prepareCreateGroup({
    required String title,
    required List<String> participantIds,
  });

  /// Adds a participant to an existing conversation (a real membership
  /// operation, `conversation.update`), never a locally invented list.
  ResourceSubmission<ConversationOutcome> prepareAddParticipant({
    required ConversationEntry conversation,
    required int expectedVersion,
    required String newParticipantId,
  });

  /// Creates a bounded task, decided manually by an eligible human (see
  /// `api/acceptance.dart`). Not started until [prepareStartTask]. [verifier]
  /// must be one [loadTrustedVerifiers] actually returned.
  ResourceSubmission<TaskOutcome> prepareCreateTask({
    required String ownerId,
    required String workerId,
    required String outcome,
    required List<String> requiredOutputs,
    required VerifierIdentity verifier,
    required DateTime rootDeadline,
    String currency = 'XXX',
  });

  /// Transitions an eligible draft/ready task to ready and enqueues its run.
  ResourceSubmission<TaskOutcome> prepareStartTask({
    required String id,
    required int expectedVersion,
  });

  /// Delegates a bounded child task from an existing (parent) task to
  /// another worker.
  ResourceSubmission<TaskOutcome> prepareDelegateTask({
    required String parentId,
    required int parentExpectedVersion,
    required String ownerId,
    required String childWorkerId,
    required String outcome,
    required List<String> requiredOutputs,
    required VerifierIdentity verifier,
    required DateTime rootDeadline,
    String currency = 'XXX',
  });

  /// The eligible human's own decision on a manually-decided task: this is
  /// the human review the task's acceptance contract requires.
  ResourceSubmission<TaskOutcome> prepareAcceptTask({
    required String id,
    required int expectedVersion,
    required bool accept,
    String reason = '',
  });

  /// The verifier identities this installation actually has, so a task or
  /// responsibility's acceptance contract can name a real one.
  Future<List<VerifierIdentity>> loadTrustedVerifiers();

  /// The real result artifacts a task has produced so far, for eligible
  /// human review before a manual decision.
  Future<List<TaskArtifactEntry>> loadTaskArtifacts(String taskId);

  /// Reads one task artifact's exact bytes, the same way a review's exact
  /// content is read.
  Future<ReviewContentPart> readTaskArtifact(TaskArtifactEntry artifact);

  /// Sends [submission] once. Completes only on controller acknowledgment.
  /// Throws [SourceUnavailable], [AcknowledgmentUnknown] or [SourceRefusal].
  Future<void> submit(PendingSubmission submission);

  /// Looks up an unknown acknowledgment. Throws [SourceUnavailable] or
  /// [AcknowledgmentUnknown] when the lookup itself gets no answer.
  Future<Resolution> resolve(PendingSubmission submission);
}
