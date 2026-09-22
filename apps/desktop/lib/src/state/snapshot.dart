// The workspace snapshot: everything the UI shows, as one immutable value
// keyed by controller identities. Sidebar badges, decision cards and detail
// entries all derive from the same snapshot, so they cannot disagree.

/// Immutable identities. Display names are never used as keys.
extension type const WorkerId(String value) {}
extension type const OrganizationId(String value) {}
extension type const ConversationId(String value) {}
extension type const ReviewId(String value) {}
extension type const RoutineId(String value) {}
extension type const ClaimId(String value) {}

/// One worker in the organization tree.
class WorkerEntry {
  const WorkerEntry({
    required this.id,
    required this.name,
    required this.role,
    required this.organizationId,
    required this.organizationPath,
    required this.parentId,
    this.conversationId,
    this.isOrganizationChief = false,
    this.preview = '',
  });

  final WorkerId id;
  final String name;

  /// A short human role, for example "Your chief" or the worker's purpose.
  final String role;

  final OrganizationId organizationId;

  /// Organization names from the root down, for ancestry announcements.
  final List<String> organizationPath;

  /// The worker this one sits beneath in the tree; null for the root chief.
  final WorkerId? parentId;

  final ConversationId? conversationId;
  final bool isOrganizationChief;

  /// The latest meaningful summary line, when the source has one.
  final String preview;

  String get ancestry => organizationPath.join(' › ');
}

enum ConversationKind { direct, group }

class ChatMessage {
  const ChatMessage({
    required this.id,
    required this.fromUser,
    required this.senderName,
    required this.body,
    required this.at,
  });

  final String id;
  final bool fromUser;
  final String senderName;
  final String body;
  final DateTime at;
}

class ConversationEntry {
  const ConversationEntry({
    required this.id,
    required this.title,
    required this.kind,
    required this.messages,
    this.workerId,
    this.historyNotice,
    this.unreadCount = 0,
    this.lastReadMarker,
    this.lastMeaningfulEvent,
    this.participantIds = const [],
    this.version = 1,
  });

  final ConversationId id;
  final String title;
  final ConversationKind kind;

  /// Every principal id the controller lists as a participant. Real
  /// membership, read for `conversation.update`'s replacement list — never
  /// inferred from who has spoken.
  final List<String> participantIds;

  /// The controller's own resource version: what `conversation.update`
  /// binds as `expected_version`.
  final int version;

  /// The worker a direct conversation belongs to. Groups have none and sit
  /// outside the organization tree.
  final WorkerId? workerId;

  /// The full authorized history known so far. For the live source this is
  /// populated lazily (by `conversation.message.list`), not eagerly for
  /// every conversation, so an entry the person has not opened yet may carry
  /// none.
  final List<ChatMessage> messages;

  /// Set when the source cannot supply the full history, with the reason.
  final String? historyNotice;

  /// The controller's own unread count for the calling principal. Never
  /// computed locally from message timestamps: quiet internal coordination
  /// must not manufacture an unread badge the controller itself would not
  /// report.
  final int unreadCount;

  /// The controller's own read-marker timestamp for the calling principal,
  /// when it has one.
  final DateTime? lastReadMarker;

  /// The controller's own ordering signal, excluding routine/quiet activity.
  /// Never used to reorder the sidebar on its own: R16-008 keeps
  /// organization order stable regardless of activity.
  final DateTime? lastMeaningfulEvent;

  /// A copy with [messages] and/or [historyNotice] replaced. Used to overlay
  /// a fetched message cache onto an otherwise-unchanged snapshot entry.
  ConversationEntry withMessages(
    List<ChatMessage> messages, {
    String? historyNotice,
  }) => ConversationEntry(
    id: id,
    title: title,
    kind: kind,
    workerId: workerId,
    messages: messages,
    historyNotice: historyNotice,
    unreadCount: unreadCount,
    lastReadMarker: lastReadMarker,
    lastMeaningfulEvent: lastMeaningfulEvent,
    participantIds: participantIds,
    version: version,
  );
}

/// The controller's view of a review.
enum ReviewRecordState { pending, approved, rejected, expired, invalidated }

/// What is known about the external effect after an approval. An accepted
/// request is not a delivered outcome.
enum EffectState {
  notStarted,
  executing,
  deliveryAccepted,
  outcomeUnknown,
  succeeded,
  failed,
}

/// One piece of the exact content a decision binds to.
class ReviewContentPart {
  const ReviewContentPart({
    required this.label,
    required this.digest,
    this.text,
    this.unavailableReason,
  });

  final String label;
  final String digest;

  /// The exact text, when the content is text and was read intact.
  final String? text;

  /// Why the exact content cannot be shown. A review with unshowable content
  /// cannot be approved from this client.
  final String? unavailableReason;

  bool get isShowable => text != null;
}

class ReviewEntry {
  const ReviewEntry({
    required this.id,
    required this.version,
    required this.actionDigest,
    required this.state,
    required this.title,
    required this.consequence,
    required this.action,
    required this.accountIdentity,
    required this.destination,
    required this.costBound,
    required this.expiresAt,
    required this.content,
    required this.parameters,
    required this.evidence,
    this.proposerId,
    this.effect = EffectState.notStarted,
    this.humanRequired = true,
  });

  final ReviewId id;

  /// The review version a decision must name as `expected_version`.
  final int version;

  /// The sealed action digest a decision binds to.
  final String actionDigest;

  final ReviewRecordState state;

  /// The consequence as a plain label, for example "Open a pull request".
  final String title;

  /// One sentence for the consequence banner.
  final String consequence;

  final String action;
  final String accountIdentity;
  final String destination;
  final String costBound;
  final DateTime expiresAt;
  final List<ReviewContentPart> content;

  /// The action's exact parameters, rendered key by key.
  final Map<String, String> parameters;

  /// Rules and evidence for the collapsed "why this needs you" section.
  final List<String> evidence;

  final WorkerId? proposerId;
  final EffectState effect;
  final bool humanRequired;

  bool get contentShowable => content.every((c) => c.isShowable);
}

/// A staged definition (worker, organization or responsibility) that is not
/// active. It is summarized and never carries an active badge.
class ProposalEntry {
  const ProposalEntry({
    required this.id,
    required this.workerId,
    required this.title,
    required this.summary,
  });

  final String id;

  /// The worker whose conversation shows the proposal.
  final WorkerId workerId;
  final String title;
  final String summary;
}

enum TaskEntryState { inProgress, waiting, needsChanges, completed, cancelled }

class TaskEntry {
  const TaskEntry({
    required this.id,
    required this.workerId,
    required this.title,
    required this.state,
    this.detail = '',
    this.version = 1,
    this.ownerId = '',
    this.manualAcceptance = false,
  });

  final String id;
  final WorkerId workerId;
  final String title;
  final TaskEntryState state;

  /// Observed detail, for example failed checks or a waiting reason. A
  /// worker's own success statement never appears here.
  final String detail;

  /// The controller's own resource version: what `task.start`/`.delegate`/
  /// `.accept` bind as `expected_version`.
  final int version;

  /// The principal this task's outcome is reported to; reused unchanged
  /// when delegating a child task.
  final String ownerId;

  /// True when this task's success can only be established by an eligible
  /// human calling `task.accept` — see `api/acceptance.dart`.
  final bool manualAcceptance;
}

/// One real result artifact a task has produced, named so it can be
/// individually read.
class TaskArtifactEntry {
  const TaskArtifactEntry({
    required this.id,
    required this.digest,
    required this.mediaType,
    required this.sizeBytes,
    required this.createdAt,
  });

  final String id;
  final String digest;
  final String mediaType;
  final int sizeBytes;
  final DateTime createdAt;
}

/// One verifier identity this installation actually has, exactly as
/// `installation.verifier.list` reports it — never one this client invents.
class VerifierIdentity {
  const VerifierIdentity({required this.id, required this.version});
  final String id;
  final int version;
}

/// A real cron-like `Schedule` linked to a responsibility's worker, read from
/// `schedule.list` — kept a distinct object from [RoutineEntry.triggers] so
/// an event trigger string is never rendered as though it were a schedule.
class ScheduleLink {
  const ScheduleLink({
    required this.id,
    required this.timezone,
    required this.expression,
    required this.misfire,
    required this.catchUpSeconds,
    required this.paused,
    this.nextWake,
  });

  final String id;
  final String timezone;
  final String expression;
  final String misfire;
  final int catchUpSeconds;
  final bool paused;
  final DateTime? nextWake;
}

class RoutineEntry {
  const RoutineEntry({
    required this.id,
    required this.version,
    required this.workerId,
    required this.title,
    required this.triggers,
    required this.signals,
    required this.minIntervalSeconds,
    required this.paused,
    this.lastCycleId,
    this.nextRun,
    this.schedule,
  });

  final RoutineId id;
  final int version;
  final WorkerId workerId;
  final String title;

  /// Event strings that admit a new cycle. Never shown as though it were a
  /// schedule description on its own.
  final List<String> triggers;

  /// What this responsibility watches for a trigger to react to.
  final List<String> signals;
  final int minIntervalSeconds;
  final bool paused;

  /// The most recent cycle this responsibility actually ran, when the
  /// controller has recorded one.
  final String? lastCycleId;

  /// This responsibility's own next-wake decision.
  final DateTime? nextRun;

  /// A real cron-like `Schedule` targeting this worker, when one exists.
  /// Null is an honest "no schedule links this worker", never an unresolved
  /// load.
  final ScheduleLink? schedule;
}

class FileEntry {
  const FileEntry({
    required this.id,
    required this.workerId,
    required this.title,
    required this.detail,
    required this.verified,
    required this.digest,
    required this.classification,
    this.taskId,
    this.taskTitle,
    this.checks = const [],
  });

  final String id;
  final WorkerId workerId;
  final String title;
  final String detail;

  /// `true` only for the controller's own `available` state. `false` means
  /// `fault` — an integrity failure or missing bytes, not merely "not yet
  /// checked".
  final bool verified;

  /// The full, immutable content digest — never truncated for display here;
  /// a caller that wants a short label truncates it itself.
  final String digest;
  final String classification;

  /// The task this artifact's provenance names, when the controller recorded
  /// one (`Artifact.scope.task_id`).
  final String? taskId;
  final String? taskTitle;

  /// The task's own sealed checks, as plain labels — the *configured*
  /// verifier contract, never a claim about which of them actually passed
  /// (that fact belongs to the owning task's own state/detail).
  final List<String> checks;
}

class MemoryEntry {
  const MemoryEntry({
    required this.id,
    required this.workerId,
    required this.bindingId,
    required this.brainId,
    required this.claimVersion,
    required this.title,
    required this.text,
    required this.provenance,
    required this.freshness,
    required this.active,
    this.confidence,
    this.canRetract = false,
  });

  final ClaimId id;
  final WorkerId workerId;
  final String bindingId;
  final String brainId;

  /// The claim's own resource version — what `memory.retract` binds as
  /// `claim.version`.
  final int claimVersion;
  final String title;
  final String text;
  final List<String> provenance;
  final DateTime freshness;

  /// `false` means a retraction has already excluded this claim from
  /// recall. The claim itself stays listed — retraction removes it from
  /// active recall, never from this audit view (R15-006).
  final bool active;

  /// 0..1000000 (parts-per-million), when the controller reported one.
  final int? confidence;

  /// Whether the binding this claim came through actually grants `retract`
  /// for the caller — an unsupported action is never offered as a live
  /// button.
  final bool canRetract;
}

/// One capability-specific autonomy record for a worker — never a global
/// trust score (R16-009).
class AutonomyEntry {
  const AutonomyEntry({
    required this.id,
    required this.workerId,
    required this.capability,
    required this.destinations,
    required this.state,
    required this.explanation,
  });

  final String id;
  final WorkerId workerId;
  final String capability;
  final List<String> destinations;

  /// `proposed`, `qualified`, `rejected`, `restricted` or `expired`.
  final String state;
  final String explanation;
}

/// Recovery obligations a non-terminal run still carries, read from
/// `run.recovery` — never inferred from a task's bare state.
class RecoveryEntry {
  const RecoveryEntry({
    required this.id,
    required this.workerId,
    required this.label,
    required this.obligations,
  });

  final String id;
  final WorkerId workerId;
  final String label;
  final List<String> obligations;
}

class AccessEntry {
  const AccessEntry({
    required this.id,
    required this.workerId,
    required this.title,
    required this.detail,
    required this.allowed,
    this.reviewId,
  });

  final String id;
  final WorkerId workerId;
  final String title;
  final String detail;
  final bool allowed;

  /// The pending review this boundary produced, if any.
  final ReviewId? reviewId;
}

/// One identity the controller authenticates: the person using this client,
/// the controller's own service identity, each worker, and any other client
/// application that holds a credential. Installation-wide, not per worker.
class PrincipalEntry {
  const PrincipalEntry({
    required this.id,
    required this.name,
    required this.kind,
    required this.revoked,
  });

  final String id;
  final String name;

  /// The controller's own word for what this identity is.
  final String kind;

  final bool revoked;

  /// A readable name for [kind], or the wire value itself when this build
  /// does not know the kind. A label is never guessed.
  String get kindLabel => switch (kind) {
    'human' => 'Person',
    'client_agent' => 'Client application',
    'worker' => 'Worker',
    'service' => 'Controller service',
    _ => kind,
  };
}

class SpendingEntry {
  const SpendingEntry({
    required this.workerId,
    required this.headline,
    required this.detail,
    this.fraction,
    this.ceiling,
    this.contextCapture,
  });

  final WorkerId workerId;
  final String headline;
  final String detail;

  /// Spent share of the limit, 0..1, when a limit exists.
  final double? fraction;

  /// The worker's own effective spend/step ceiling, in words, when one is
  /// configured — real cost liability, not merely what has been spent so
  /// far.
  final String? ceiling;

  /// `complete`, `partial` or `advisory`: how much of a model step's real
  /// context this worker's execution profile actually captures. Null means
  /// no profile is configured, never a guessed default.
  final String? contextCapture;
}

/// The details-panel tabs.
enum DetailsTab {
  work('Work'),
  routines('Routines'),
  files('Files'),
  memory('Memory'),
  access('Access');

  const DetailsTab(this.label);
  final String label;
}

/// Something the workspace needs before a surface can work: a missing
/// connection, access, limit, or an operation the controller does not offer.
class PrerequisiteNotice {
  const PrerequisiteNotice({
    required this.title,
    required this.message,
    this.tab,
    this.setupPath,
    this.workerId,
  });

  final String title;
  final String message;

  /// The details tab the notice belongs to; null for the whole workspace.
  final DetailsTab? tab;

  /// Where the person goes to fix it.
  final String? setupPath;

  /// The worker this prerequisite blocks, for example a missing provider or
  /// budget; null for a workspace-wide notice (not initialized, paused).
  final WorkerId? workerId;
}

/// An external operation an authorized worker started on its own — no human
/// review required — whose disposition is not yet settled. Never dropped
/// silently: an accepted-but-unconfirmed or outcome-unknown effect stays
/// visible until it resolves, even though nobody needs to decide it.
class UnresolvedOperationEntry {
  const UnresolvedOperationEntry({
    required this.id,
    required this.workerId,
    required this.title,
    required this.state,
    required this.destination,
  });

  final String id;
  final WorkerId? workerId;
  final String title;
  final EffectState state;
  final String destination;

  String get label => switch (state) {
    EffectState.executing => 'Running',
    EffectState.deliveryAccepted => 'Accepted · outcome not confirmed',
    EffectState.outcomeUnknown => 'Outcome unknown · reservation kept',
    EffectState.succeeded => 'Done · outcome confirmed',
    EffectState.failed => 'Failed',
    EffectState.notStarted => 'Not started',
  };
}

class WorkspaceSnapshot {
  const WorkspaceSnapshot({
    required this.takenAt,
    required this.workers,
    required this.conversations,
    required this.reviews,
    this.proposals = const [],
    this.tasks = const [],
    this.routines = const [],
    this.files = const [],
    this.memory = const [],
    this.access = const [],
    this.spending = const [],
    this.autonomy = const [],
    this.recovery = const [],
    this.principals = const [],
    this.prerequisites = const [],
    this.unresolvedOperations = const [],
    this.workspaceName = '',
  });

  static final WorkspaceSnapshot empty = WorkspaceSnapshot(
    takenAt: DateTime.fromMillisecondsSinceEpoch(0, isUtc: true),
    workers: const [],
    conversations: const [],
    reviews: const [],
  );

  final DateTime takenAt;

  /// Workers in stable organization order. Activity never reorders them.
  final List<WorkerEntry> workers;

  final List<ConversationEntry> conversations;
  final List<ReviewEntry> reviews;
  final List<ProposalEntry> proposals;
  final List<TaskEntry> tasks;
  final List<RoutineEntry> routines;
  final List<FileEntry> files;
  final List<MemoryEntry> memory;
  final List<AccessEntry> access;
  final List<SpendingEntry> spending;
  final List<AutonomyEntry> autonomy;
  final List<RecoveryEntry> recovery;

  /// Every identity the controller authenticates, in stable name order.
  final List<PrincipalEntry> principals;

  final List<PrerequisiteNotice> prerequisites;
  final List<UnresolvedOperationEntry> unresolvedOperations;
  final String workspaceName;

  WorkerEntry? worker(WorkerId id) {
    for (final w in workers) {
      if (w.id == id) return w;
    }
    return null;
  }

  ConversationEntry? conversation(ConversationId id) {
    for (final c in conversations) {
      if (c.id == id) return c;
    }
    return null;
  }

  ReviewEntry? review(ReviewId id) {
    for (final r in reviews) {
      if (r.id == id) return r;
    }
    return null;
  }

  List<WorkerEntry> childrenOf(WorkerId? parent) => [
    for (final w in workers)
      if (w.parentId == parent) w,
  ];

  /// [id] and every worker beneath it.
  Set<WorkerId> subtree(WorkerId id) {
    final out = <WorkerId>{id};
    var grew = true;
    while (grew) {
      grew = false;
      for (final w in workers) {
        final p = w.parentId;
        if (p != null && out.contains(p) && out.add(w.id)) grew = true;
      }
    }
    return out;
  }

  /// The chain of workers above [id], nearest first.
  List<WorkerId> ancestorsOf(WorkerId id) {
    final out = <WorkerId>[];
    var current = worker(id)?.parentId;
    while (current != null && !out.contains(current)) {
      out.add(current);
      current = worker(current)?.parentId;
    }
    return out;
  }
}
