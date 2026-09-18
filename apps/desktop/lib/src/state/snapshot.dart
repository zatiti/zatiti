// The workspace snapshot: everything the UI shows, as one immutable value
// keyed by controller identities. Sidebar badges, decision cards and detail
// entries all derive from the same snapshot, so they cannot disagree.

/// Immutable identities. Display names are never used as keys.
extension type const WorkerId(String value) {}
extension type const OrganizationId(String value) {}
extension type const ConversationId(String value) {}
extension type const ReviewId(String value) {}
extension type const RoutineId(String value) {}

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
  });

  final ConversationId id;
  final String title;
  final ConversationKind kind;

  /// The worker a direct conversation belongs to. Groups have none and sit
  /// outside the organization tree.
  final WorkerId? workerId;

  final List<ChatMessage> messages;

  /// Set when the source cannot supply the full history, with the reason.
  final String? historyNotice;
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
  });

  final String id;
  final WorkerId workerId;
  final String title;
  final TaskEntryState state;

  /// Observed detail, for example failed checks or a waiting reason. A
  /// worker's own success statement never appears here.
  final String detail;
}

class RoutineEntry {
  const RoutineEntry({
    required this.id,
    required this.version,
    required this.workerId,
    required this.title,
    required this.schedule,
    required this.paused,
    this.boundary = '',
    this.nextRun,
  });

  final RoutineId id;
  final int version;
  final WorkerId workerId;
  final String title;
  final String schedule;
  final bool paused;
  final String boundary;
  final DateTime? nextRun;
}

class FileEntry {
  const FileEntry({
    required this.id,
    required this.workerId,
    required this.title,
    required this.detail,
    required this.verified,
  });

  final String id;
  final WorkerId workerId;
  final String title;
  final String detail;
  final bool verified;
}

class MemoryEntry {
  const MemoryEntry({
    required this.id,
    required this.workerId,
    required this.title,
    required this.text,
    required this.provenance,
  });

  final String id;
  final WorkerId workerId;
  final String title;
  final String text;
  final List<String> provenance;
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

class SpendingEntry {
  const SpendingEntry({
    required this.workerId,
    required this.headline,
    required this.detail,
    this.fraction,
  });

  final WorkerId workerId;
  final String headline;
  final String detail;

  /// Spent share of the limit, 0..1, when a limit exists.
  final double? fraction;
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
  });

  final String title;
  final String message;

  /// The details tab the notice belongs to; null for the whole workspace.
  final DetailsTab? tab;

  /// Where the person goes to fix it.
  final String? setupPath;
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
    this.prerequisites = const [],
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
  final List<PrerequisiteNotice> prerequisites;
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
