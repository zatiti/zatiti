// The workspace view-state controller. It owns no business rules: every
// displayed fact comes from a source snapshot, and an approval, a pause or a
// sent message shows as done only after the source acknowledges it.

import 'dart:async';

import 'package:flutter/foundation.dart';

import '../app/local_store.dart';
import 'snapshot.dart';
import 'view_state.dart';
import 'workspace_source.dart';

class _ReviewOverlay {
  ReviewPhase? phase;
  PendingSubmission? pending;
  DecisionChoice? choice;
  String? note;
}

class _RoutineOverlay {
  RoutinePhase? phase;
  PendingSubmission? pending;
  String? note;
}

/// One claim's retraction in progress, keyed by [ClaimId]. Mirrors
/// [_RoutineOverlay]'s shape; [jobStatus] holds the last known `Job.label`
/// once the retraction was acknowledged.
class _MemoryOverlay {
  ClaimActionPhase? phase;
  ResourceSubmission<MemoryRetractOutcome>? pending;
  String? jobId;
  String? jobStatus;
}

/// One draft/plan/review/apply flow in progress: organization, worker or
/// responsibility creation. Exactly one of [stagePending]/[planPending]/
/// [applyPending] is ever set at a time, and it is the same object across a
/// retry — never re-prepared, so a retry never mints a second submission
/// key for the same intended mutation.
class _DraftFlowOverlay {
  DraftFlowPhase phase = DraftFlowPhase.composing;
  ResourceSubmission<DraftedResource>? stagePending;
  ResourceSubmission<PlanOutcome>? planPending;
  ResourceSubmission<PlanOutcome>? applyPending;
  DraftedResource? draft;
  PlanOutcome? plan;
  String? note;
}

/// One task creation flow in progress: `task.create` then `task.start`.
class _TaskFlowOverlay {
  TaskFlowPhase phase = TaskFlowPhase.composing;
  ResourceSubmission<TaskOutcome>? createPending;
  ResourceSubmission<TaskOutcome>? startPending;
  TaskOutcome? created;
  String? note;
}

/// One single-step mutation in progress: a new group chat, or a membership
/// change to an existing one. Mirrors [_RoutineOverlay]'s shape.
class _GroupFlowOverlay {
  bool submitting = false;
  bool unknown = false;
  ResourceSubmission<ConversationOutcome>? pending;
  String? note;
}

class WorkspaceController extends ChangeNotifier {
  /// [localStore]/[installationId] persist the selected conversation and a
  /// bounded authorized-identity cache to ordinary OS-protected local
  /// storage (never secure storage — see `local_store.dart`'s header).
  /// [initialLocal] is that state, already read once by the caller before
  /// this controller exists (so construction stays synchronous); pass
  /// [LocalState.empty] when there is nothing to restore, or none of
  /// [localStore]/[installationId] were supplied.
  ///
  /// [initialDrafts]/[persistDrafts] are the frozen contract's own seam for
  /// unsent drafts (AGENTS.md: "offline drafts... live in operating-system
  /// secure storage", the same as the credential): this controller owns the
  /// in-memory draft map either way, but never reads or writes secure
  /// storage itself, so the caller supplies the already-read blob and a
  /// write-back callback rather than a concrete `CredentialStore`.
  WorkspaceController(
    this.source, {
    DateTime Function()? clock,
    LocalStore? localStore,
    String? installationId,
    LocalState initialLocal = LocalState.empty,
    Map<String, String> initialDrafts = const {},
    Future<void> Function(Map<String, String> drafts)? persistDrafts,
  }) : _clock = clock ?? (() => DateTime.now().toUtc()),
       _localStore = localStore,
       _installationId = installationId,
       _local = initialLocal,
       _restoreWorkerId = initialLocal.selectedWorkerId,
       _restoreGroupId = initialLocal.selectedGroupId,
       _persistDrafts = persistDrafts,
       _drafts = {
         for (final entry in initialDrafts.entries)
           ConversationId(entry.key): entry.value,
       };

  final WorkspaceSource source;
  final DateTime Function() _clock;
  final LocalStore? _localStore;
  final String? _installationId;
  LocalState _local;
  final Future<void> Function(Map<String, String> drafts)? _persistDrafts;

  /// A selection to restore on the very first snapshot, consumed once.
  /// Null immediately once that first snapshot has been adopted, whether or
  /// not the id it named turned out to still exist.
  String? _restoreWorkerId;
  String? _restoreGroupId;

  /// The full authorized history for a conversation the person has opened,
  /// fetched lazily via `WorkspaceSource.loadMessages`. Never persisted:
  /// re-fetched fresh from the controller every time it is needed, so it
  /// cannot itself become the "leaked cached history" account/scope switch
  /// must avoid.
  final Map<ConversationId, List<ChatMessage>> _messageCache = {};
  final Map<ConversationId, String> _messageNotice = {};
  final Set<ConversationId> _loadingMessages = {};

  /// The moment this window's own last message to a conversation was
  /// acknowledged, kept only until a newer message (from either side)
  /// arrives. Drives the "waiting for a reply" status line — literally true
  /// from delivery and arrival timestamps, never a claim about what a
  /// worker is doing internally.
  final Map<ConversationId, DateTime> _awaitingReplySince = {};

  // ---- connection -------------------------------------------------------

  ConnectionPhase connection = ConnectionPhase.connecting;

  /// Why the workspace is offline, in words for the person.
  String? connectionMessage;

  WorkspaceSnapshot snapshot = WorkspaceSnapshot.empty;

  bool get isOnline => connection == ConnectionPhase.online;
  bool get canCompose => !snapshot.installedMac || snapshot.installedChiefReady;
  String? get installedReadinessIssue => snapshot.installedMac
      ? snapshot.installedChiefIssue ??
            (snapshot.installedChiefReady
                ? null
                : 'The personal-chief chat is not ready.')
      : null;

  /// True while the view may be out of date and must say so.
  bool get showsSavedView =>
      connection == ConnectionPhase.offline ||
      connection == ConnectionPhase.reconnecting ||
      connection == ConnectionPhase.unsupported;

  // ---- interface state, each independent of the others --------------------

  WorkerId? selectedWorker;
  ConversationId? selectedGroup;
  final Set<WorkerId> _collapsed = <WorkerId>{};
  WorkerId? filterRoot;
  bool detailsOpen = false;
  DetailsTab detailsTab = DetailsTab.work;

  /// Unsent per-conversation draft text. Seeded from [initialDrafts] and
  /// written back through [_persistDrafts] on every change; never persisted
  /// by this controller directly (see the constructor doc).
  final Map<ConversationId, String> _drafts;

  final List<OutgoingMessage> _outgoing = <OutgoingMessage>[];
  final Map<String, PendingSubmission> _outgoingSubmissions =
      <String, PendingSubmission>{};
  final Map<ReviewId, _ReviewOverlay> _reviewOverlays =
      <ReviewId, _ReviewOverlay>{};
  final Map<RoutineId, _RoutineOverlay> _routineOverlays =
      <RoutineId, _RoutineOverlay>{};
  final Map<ClaimId, _MemoryOverlay> _memoryOverlays =
      <ClaimId, _MemoryOverlay>{};
  final Map<DraftFlowKind, _DraftFlowOverlay> _draftFlows =
      <DraftFlowKind, _DraftFlowOverlay>{};
  _TaskFlowOverlay? _taskFlow;
  _GroupFlowOverlay? _groupFlow;
  int _localIds = 0;
  bool _disposed = false;

  @override
  void dispose() {
    _disposed = true;
    super.dispose();
  }

  void _changed() {
    if (!_disposed) notifyListeners();
  }

  // ---- loading ------------------------------------------------------------

  /// Loads the first snapshot.
  Future<void> start() => _load(ConnectionPhase.connecting);

  /// Fetches a fresh snapshot after being offline. Unsent drafts stay unsent:
  /// reconnecting never sends anything.
  Future<void> reconnect() => _load(ConnectionPhase.reconnecting);

  /// Replays events and reloads when something changed.
  Future<void> poll() async {
    if (!isOnline) return;
    try {
      if (await source.hasChanges()) await _load(ConnectionPhase.online);
    } on SourceUnavailable catch (e) {
      _goOffline(e.message);
    } on AcknowledgmentUnknown {
      // A lost answer to a query changes nothing; the next poll asks again.
    } on SourceUnsupported catch (e) {
      connection = ConnectionPhase.unsupported;
      connectionMessage = e.message;
      _changed();
    }
  }

  Future<void> _load(ConnectionPhase during) async {
    connection = during;
    _changed();
    try {
      final next = await source.loadSnapshot();
      _adopt(next);
      connection = ConnectionPhase.online;
      connectionMessage = null;
      await _persistLocal();
      final id = _rawSelectedConversation()?.id;
      if (id != null) await _ensureMessages(id, refresh: true);
    } on SourceUnavailable catch (e) {
      _goOffline(e.message);
      return;
    } on AcknowledgmentUnknown catch (e) {
      _goOffline(e.message);
      return;
    } on SourceUnsupported catch (e) {
      connection = ConnectionPhase.unsupported;
      connectionMessage = e.message;
    } on SourceRefusal catch (e) {
      connection = ConnectionPhase.offline;
      connectionMessage = e.message;
    }
    _changed();
  }

  void _goOffline(String message) {
    connection = ConnectionPhase.offline;
    connectionMessage = message;
    _changed();
  }

  void _adopt(WorkspaceSnapshot next) {
    final first = snapshot.workers.isEmpty;
    snapshot = next;
    if (first) {
      // Start with nested branches collapsed, as the design does, and the
      // root chief selected.
      for (final w in next.workers) {
        if (w.parentId != null && next.childrenOf(w.id).isNotEmpty) {
          _collapsed.add(w.id);
        }
      }
      // A returning launch restores the conversation last open, not always
      // the root chief (R16-003). Consumed once, whether or not the
      // restored id still names something real.
      final restoreGroup = _restoreGroupId;
      final restoreWorker = _restoreWorkerId;
      _restoreGroupId = null;
      _restoreWorkerId = null;
      if ((!next.installedMac || next.installedChiefWorkerId != null) &&
          restoreGroup != null &&
          next.conversation(ConversationId(restoreGroup))?.kind ==
              ConversationKind.group) {
        selectedGroup = ConversationId(restoreGroup);
      } else if ((!next.installedMac || next.installedChiefWorkerId != null) &&
          restoreWorker != null &&
          next.worker(WorkerId(restoreWorker)) != null) {
        selectedWorker = WorkerId(restoreWorker);
      }
    }
    if (next.installedMac && next.installedChiefWorkerId == null) {
      selectedGroup = null;
      selectedWorker = null;
    }
    if (selectedGroup == null &&
        (selectedWorker == null || next.worker(selectedWorker!) == null)) {
      if (next.installedMac) {
        final chief = next.installedChiefWorkerId;
        selectedWorker = chief == null ? null : WorkerId(chief);
      } else {
        final roots = next.childrenOf(null);
        selectedWorker = roots.isEmpty ? null : roots.first.id;
      }
    }
    // An overlay that only echoed an acknowledgment retires once the
    // controller's own record says the same.
    _reviewOverlays.removeWhere((id, overlay) {
      final settled =
          overlay.phase == ReviewPhase.approved ||
          overlay.phase == ReviewPhase.declined;
      final record = next.review(id);
      return settled &&
          record != null &&
          record.state != ReviewRecordState.pending;
    });
    _routineOverlays.removeWhere((id, overlay) {
      if (overlay.phase != RoutinePhase.paused) return false;
      for (final r in next.routines) {
        if (r.id == id) return r.paused;
      }
      return true;
    });
    // A retraction overlay retires once the controller's own record agrees
    // the claim is no longer active, or the claim has left this view
    // entirely (for example, its brain became unlisted).
    _memoryOverlays.removeWhere((id, overlay) {
      if (overlay.phase != ClaimActionPhase.requested) return false;
      for (final m in next.memory) {
        if (m.id == id) return !m.active;
      }
      return true;
    });
  }

  // ---- organization conversation tree --------------------------------------

  bool isExpanded(WorkerId id) => !_collapsed.contains(id);

  /// Expands or collapses a branch. The selected conversation is untouched.
  void toggleExpanded(WorkerId id) {
    if (!_collapsed.remove(id)) _collapsed.add(id);
    _changed();
  }

  void setExpanded(WorkerId id, {required bool expanded}) {
    final changed = expanded ? _collapsed.remove(id) : _collapsed.add(id);
    if (changed) _changed();
  }

  /// Opens a worker's conversation and reveals it in the tree.
  void selectWorker(WorkerId id) {
    if (snapshot.worker(id) == null) return;
    selectedWorker = id;
    selectedGroup = null;
    snapshot.ancestorsOf(id).forEach(_collapsed.remove);
    _changed();
    unawaited(_persistLocal());
    final conversationId = _rawSelectedConversation()?.id;
    if (conversationId != null) unawaited(_ensureMessages(conversationId));
  }

  void selectGroup(ConversationId id) {
    if (snapshot.conversation(id)?.kind != ConversationKind.group) return;
    selectedGroup = id;
    selectedWorker = null;
    _changed();
    unawaited(_persistLocal());
    unawaited(_ensureMessages(id));
  }

  /// Limits the tree to one organization chief's branch; null shows all.
  void setFilter(WorkerId? root) {
    filterRoot = root;
    _changed();
  }

  List<WorkerEntry> get filterOptions => [
    for (final w in snapshot.workers)
      if (w.isOrganizationChief && w.parentId != null) w,
  ];

  List<ConversationEntry> get groups => [
    for (final c in snapshot.conversations)
      if (c.kind == ConversationKind.group) c,
  ];

  /// Visible rows in stable organization order.
  List<TreeNodeView> get tree {
    final pendingBy = <WorkerId, int>{};
    for (final d in needsYou) {
      final p = d.review.proposerId;
      if (p != null) pendingBy[p] = (pendingBy[p] ?? 0) + 1;
    }
    Set<WorkerId>? visible;
    final root = filterRoot;
    if (root != null && snapshot.worker(root) != null) {
      // The branch and its descendants, plus ancestors for orientation.
      visible = {...snapshot.subtree(root), ...snapshot.ancestorsOf(root)};
    }

    final rows = <TreeNodeView>[];
    void visit(WorkerEntry w, int depth) {
      if (visible != null && !visible.contains(w.id)) return;
      final children = [
        for (final c in snapshot.childrenOf(w.id))
          if (visible == null || visible.contains(c.id)) c,
      ];
      final expanded = isExpanded(w.id);
      var below = 0;
      if (!expanded) {
        for (final id in snapshot.subtree(w.id)) {
          if (id != w.id) below += pendingBy[id] ?? 0;
        }
      }
      rows.add(
        TreeNodeView(
          worker: w,
          depth: depth,
          hasChildren: children.isNotEmpty,
          expanded: expanded,
          selected: selectedGroup == null && selectedWorker == w.id,
          pinned: w.parentId == null,
          showsDecisionIndicator: (pendingBy[w.id] ?? 0) > 0 || below > 0,
          decisionsBelow: below,
        ),
      );
      if (expanded) {
        for (final c in children) {
          visit(c, depth + 1);
        }
      }
    }

    for (final r in snapshot.childrenOf(null)) {
      visit(r, 0);
    }
    return rows;
  }

  // ---- decisions ------------------------------------------------------------

  ReviewPhase phaseOf(ReviewId id) {
    final record = snapshot.review(id);
    final overlay = _reviewOverlays[id]?.phase;
    if (overlay != null) return overlay;
    if (record == null) return ReviewPhase.invalidated;
    return phaseOfRecord(record, _clock());
  }

  DecisionView _viewOf(ReviewEntry review) {
    final phase = phaseOf(review.id);
    final proposer = review.proposerId == null
        ? null
        : snapshot.worker(review.proposerId!);
    String? blocked;
    if (!phase.allowsDecision) {
      blocked = phase.label;
    } else if (!isOnline) {
      blocked =
          'Offline. Reconnect to check the current request before deciding.';
    } else if (!review.contentShowable) {
      blocked =
          'The exact content cannot be shown here, so it cannot be '
          'approved here.';
    }
    return DecisionView(
      review: review,
      phase: phase,
      proposerName: proposer?.name ?? 'A worker',
      ancestry: proposer?.ancestry ?? '',
      canDecide: blocked == null,
      blockedReason: blocked,
      note: _reviewOverlays[review.id]?.note,
    );
  }

  DecisionView? decision(ReviewId id) {
    final review = snapshot.review(id);
    return review == null ? null : _viewOf(review);
  }

  /// Every review that still needs the person, across the whole tree. The
  /// sidebar count, the amber indicators, the cards and the Work tab all read
  /// this list.
  List<DecisionView> get needsYou => [
    for (final r in snapshot.reviews)
      if (phaseOf(r.id).needsYou) _viewOf(r),
  ];

  int get needsYouCount => needsYou.length;

  /// Reviews shown in [worker]'s conversation: its own and those of every
  /// worker beneath it.
  List<DecisionView> decisionsFor(WorkerId worker) {
    final scope = snapshot.subtree(worker);
    return [
      for (final r in snapshot.reviews)
        if (r.proposerId != null && scope.contains(r.proposerId)) _viewOf(r),
    ];
  }

  /// Records a decision on the exact version and digest the person saw.
  ///
  /// Returns without doing anything when a decision is not allowed right
  /// now: offline, already submitting, unknown acknowledgment, stale or
  /// expired. The phase changes to approved or declined only after the
  /// source acknowledges.
  Future<void> decide(
    ReviewId id,
    DecisionChoice choice, {
    required int seenVersion,
    required String seenDigest,
  }) async {
    final view = decision(id);
    if (view == null || !view.canDecide) return;
    final review = view.review;
    final overlay = _reviewOverlays.putIfAbsent(id, _ReviewOverlay.new);

    if (review.version != seenVersion || review.actionDigest != seenDigest) {
      overlay
        ..phase = ReviewPhase.stale
        ..pending = null
        ..note = 'The request changed while you were reading it.';
      _changed();
      return;
    }
    if (!review.expiresAt.isAfter(_clock())) {
      overlay
        ..phase = ReviewPhase.expired
        ..pending = null;
      _changed();
      return;
    }

    // A retry of the same choice after a provable non-delivery keeps the
    // original submission identity. A different choice is a different
    // intended mutation.
    if (overlay.pending == null || overlay.choice != choice) {
      overlay
        ..pending = source.prepareDecision(review, choice)
        ..choice = choice;
    }
    overlay
      ..phase = ReviewPhase.submitting
      ..note = null;
    _changed();
    await _submitDecision(id, overlay);
  }

  Future<void> _submitDecision(ReviewId id, _ReviewOverlay overlay) async {
    try {
      await source.submit(overlay.pending!);
      _acknowledged(overlay);
      _changed();
      await _reloadQuietly();
    } on SourceUnavailable catch (e) {
      // Nothing was sent. The person may try again; the identity is kept.
      overlay
        ..phase = null
        ..note = 'Not sent. Nothing changed.';
      _goOffline(e.message);
    } on AcknowledgmentUnknown {
      overlay.phase = ReviewPhase.acknowledgmentUnknown;
      _changed();
      await checkDecision(id);
    } on SourceRefusal catch (e) {
      _refused(overlay, e);
      _changed();
    }
  }

  void _acknowledged(_ReviewOverlay overlay) {
    overlay
      ..phase = overlay.choice == DecisionChoice.approve
          ? ReviewPhase.approved
          : ReviewPhase.declined
      ..pending = null
      ..note = null;
  }

  void _refused(_ReviewOverlay overlay, SourceRefusal refusal) {
    overlay
      ..pending = null
      ..note = refusal.message
      ..phase = switch (refusal.kind) {
        RefusalKind.stale => ReviewPhase.stale,
        RefusalKind.expired => ReviewPhase.expired,
        RefusalKind.prerequisiteMissing => ReviewPhase.prerequisiteMissing,
        RefusalKind.other => ReviewPhase.prerequisiteMissing,
      };
  }

  /// Looks up a decision whose acknowledgment is unknown. Never resends.
  Future<void> checkDecision(ReviewId id) async {
    final overlay = _reviewOverlays[id];
    final pending = overlay?.pending;
    if (overlay == null ||
        pending == null ||
        overlay.phase != ReviewPhase.acknowledgmentUnknown) {
      return;
    }
    try {
      switch (await source.resolve(pending)) {
        case ResolvedAcknowledged():
          _acknowledged(overlay);
          _changed();
          await _reloadQuietly();
        case ResolvedRefused(:final refusal):
          _refused(overlay, refusal);
          _changed();
        case ResolvedNotReceived():
          // Proven not received. Back to awaiting an explicit decision; the
          // same submission is reused if the person repeats the choice.
          overlay
            ..phase = null
            ..note = 'Your decision was not received. Nothing changed.';
          _changed();
      }
    } on SourceUnavailable catch (e) {
      _goOffline(e.message);
    } on AcknowledgmentUnknown {
      overlay.note =
          'Still checking. Decisions stay locked until this is known.';
      _changed();
    }
  }

  /// Fetches the current preview of a stale review. The person must then make
  /// a new exact decision; nothing carries over.
  Future<void> refreshReview(ReviewId id) async {
    if (!isOnline) return;
    try {
      final fresh = await source.refreshReview(id);
      snapshot = _withReview(fresh);
      _reviewOverlays.remove(id);
      _changed();
    } on SourceUnavailable catch (e) {
      _goOffline(e.message);
    } on AcknowledgmentUnknown {
      // The lookup got no answer; the review stays stale and disabled.
    } on SourceRefusal catch (e) {
      _reviewOverlays.putIfAbsent(id, _ReviewOverlay.new).note = e.message;
      _changed();
    }
  }

  WorkspaceSnapshot _withReview(ReviewEntry fresh) => WorkspaceSnapshot(
    takenAt: snapshot.takenAt,
    workers: snapshot.workers,
    conversations: snapshot.conversations,
    reviews: [for (final r in snapshot.reviews) r.id == fresh.id ? fresh : r],
    proposals: snapshot.proposals,
    tasks: snapshot.tasks,
    routines: snapshot.routines,
    files: snapshot.files,
    memory: snapshot.memory,
    access: snapshot.access,
    spending: snapshot.spending,
    principals: snapshot.principals,
    prerequisites: snapshot.prerequisites,
    unresolvedOperations: snapshot.unresolvedOperations,
    workspaceName: snapshot.workspaceName,
  );

  Future<void> _reloadQuietly() async {
    try {
      _adopt(await source.loadSnapshot());
      _changed();
      await _persistLocal();
      final id = _rawSelectedConversation()?.id;
      if (id != null) await _ensureMessages(id, refresh: true);
    } on SourceUnavailable catch (e) {
      _goOffline(e.message);
    } on AcknowledgmentUnknown {
      // Keep the acknowledged overlay; the next poll refreshes the record.
    } on SourceRefusal {
      // Same: the acknowledgment stands on its own.
    }
  }

  // ---- conversation and composer --------------------------------------------

  /// The selected conversation's identity and shape from the current
  /// snapshot, before any fetched-message overlay. Used to decide what to
  /// load and persist without recursing into [selectedConversation] itself.
  ConversationEntry? _rawSelectedConversation() {
    final group = selectedGroup;
    if (group != null) return snapshot.conversation(group);
    final worker = selectedWorker == null
        ? null
        : snapshot.worker(selectedWorker!);
    final id = worker?.conversationId;
    return id == null ? null : snapshot.conversation(id);
  }

  /// The selected conversation as the person sees it: the snapshot entry
  /// with its fetched message history (or load notice) overlaid. The
  /// snapshot itself never carries every conversation's full history (see
  /// `live_source.dart`'s `_conversation`); this is where it is joined back
  /// in, only for the one conversation actually open.
  ConversationEntry? get selectedConversation {
    final base = _rawSelectedConversation();
    if (base == null) return null;
    final cached = _messageCache[base.id];
    final notice = _messageNotice[base.id];
    if (cached == null && notice == null) return base;
    return base.withMessages(cached ?? base.messages, historyNotice: notice);
  }

  /// True while this window is waiting to hear back after its own last
  /// message to [id] was delivered, with nothing newer from either side
  /// since. A plain fact about timestamps, never a claim about what a
  /// worker is doing.
  bool isAwaitingReply(ConversationId id) =>
      _awaitingReplySince.containsKey(id);

  /// Fetches [id]'s full authorized history. Skips the fetch when already
  /// cached and [refresh] is false. Failures are recorded as a per-
  /// conversation notice rather than surfaced as a workspace-wide error:
  /// this must cost the open conversation, not everything else on screen.
  Future<void> _ensureMessages(
    ConversationId id, {
    bool refresh = false,
  }) async {
    if (!refresh &&
        (_messageCache.containsKey(id) || _loadingMessages.contains(id))) {
      return;
    }
    if (_loadingMessages.contains(id)) return;
    _loadingMessages.add(id);
    try {
      final messages = await source.loadMessages(id);
      final previousLatest = _messageCache[id]?.lastOrNull?.at;
      _messageCache[id] = messages;
      _messageNotice.remove(id);
      final latest = messages.lastOrNull;
      if (latest != null &&
          (previousLatest == null || latest.at.isAfter(previousLatest))) {
        // Something newer arrived. A reply from the worker settles "waiting
        // for a reply"; a message this window itself just sent starts it.
        if (latest.fromUser) {
          _awaitingReplySince[id] = latest.at;
        } else {
          _awaitingReplySince.remove(id);
        }
      }
    } on SourceUnavailable catch (e) {
      _messageNotice[id] = e.message;
    } on SourceRefusal catch (e) {
      _messageNotice[id] = e.message;
    } on AcknowledgmentUnknown {
      // A lost answer to a query changes nothing; the next attempt asks
      // again. The existing cache (if any) stands.
    } finally {
      _loadingMessages.remove(id);
      _changed();
    }
  }

  /// Writes the current selection and a bounded, non-secret cache of known
  /// identities to ordinary local storage (never message content — see
  /// `local_store.dart`). Read-modify-write against the same file
  /// `LiveWorkspaceSource` persists its event cursor to.
  Future<void> _persistLocal() async {
    final store = _localStore;
    final installationId = _installationId;
    if (store == null || installationId == null) return;
    try {
      final current = await store.read(installationId);
      // Organization names are not their own snapshot list; the tree
      // already carries one (name, id) per organization on every worker
      // that belongs to it, so the last segment of each worker's own path
      // is that organization's real name, not a guess.
      final organizations = <String, String>{};
      for (final w in snapshot.workers) {
        final name = w.organizationPath.lastOrNull;
        if (name != null) organizations[w.organizationId.value] = name;
      }
      _local = current.copyWith(
        installationId: installationId,
        selectedWorkerId: selectedGroup != null ? null : selectedWorker?.value,
        selectedGroupId: selectedGroup?.value,
        workers: [
          for (final w in snapshot.workers)
            CachedIdentity(id: w.id.value, name: w.name),
        ],
        organizations: [
          for (final e in organizations.entries)
            CachedIdentity(id: e.key, name: e.value),
        ],
        principals: [
          for (final p in snapshot.principals)
            CachedIdentity(id: p.id, name: p.name),
        ],
      );
      await store.write(_local);
    } on Object {
      // Best-effort: local UI persistence never becomes a workspace error.
    }
  }

  /// Clears every locally held trace of the current account: the fetched-
  /// message cache, the persisted selection, the bounded identity cache and
  /// any unsent drafts. Called before any explicit credential change, since
  /// this client has no `identity.current` operation to confirm whether
  /// that change is still the same principal — the safe assumption is that
  /// it might not be, and showing another account's cached workspace even
  /// briefly is worse than an extra reload.
  Future<void> resetForCredentialChange() async {
    _messageCache.clear();
    _messageNotice.clear();
    _loadingMessages.clear();
    _awaitingReplySince.clear();
    _drafts.clear();
    selectedWorker = null;
    selectedGroup = null;
    snapshot = WorkspaceSnapshot.empty;
    _local = LocalState.empty;
    await _localStore?.clear();
    await _persistDraftsNow();
    _changed();
  }

  String draftFor(ConversationId id) => _drafts[id] ?? '';

  /// Keeps a per-conversation draft. Drafts are local text, never sent.
  /// Written back to secure storage (see the constructor doc) so an unsent
  /// draft survives close/reopen exactly like a delivered message does.
  void setDraft(ConversationId id, String text) {
    if (text.isEmpty) {
      _drafts.remove(id);
    } else {
      _drafts[id] = text;
    }
    unawaited(_persistDraftsNow());
  }

  /// Best-effort write-back of every current draft. A failure here costs
  /// only "the draft might not survive a restart", never the in-memory
  /// state this session already shows.
  Future<void> _persistDraftsNow() async {
    final persist = _persistDrafts;
    if (persist == null) return;
    try {
      await persist({for (final e in _drafts.entries) e.key.value: e.value});
    } on Object {
      // Best-effort, as documented above.
    }
  }

  List<OutgoingMessage> outgoingFor(ConversationId id) => [
    for (final m in _outgoing)
      if (m.conversationId == id) m,
  ];

  /// The status line for [conversation]'s turn in progress, or null when
  /// there is nothing to say. Checked in the order a person would actually
  /// notice them: this window's own unsettled message first (acknowledged
  /// takes priority over stale "waiting" from an earlier message), then
  /// whatever would stop a reply from ever arriving (an open decision or a
  /// workspace-wide prerequisite), then simply "delivered, nothing back
  /// yet". [worker] is null for a group conversation, which has no single
  /// proposer to check reviews against.
  TurnStatus? turnStatusFor(ConversationId conversation, {WorkerId? worker}) {
    final outgoing = outgoingFor(conversation);
    if (outgoing.isNotEmpty) {
      return switch (outgoing.first.phase) {
        OutgoingPhase.sending => TurnStatus.acknowledging,
        OutgoingPhase.acknowledgmentUnknown => TurnStatus.acknowledgmentUnknown,
        OutgoingPhase.unsentDraft => TurnStatus.unsent,
        OutgoingPhase.refused => TurnStatus.refused,
      };
    }
    if (worker != null && decisionsFor(worker).any((d) => d.phase.needsYou)) {
      return TurnStatus.reviewWaiting;
    }
    if (prerequisitesFor(null).isNotEmpty) return TurnStatus.blockedSetup;
    if (isAwaitingReply(conversation)) return TurnStatus.waitingForReply;
    return null;
  }

  /// Sends the conversation's draft. Offline, the message is kept and shown
  /// as unsent; it is never queued for automatic delivery.
  Future<void> sendDraft(ConversationId id) async {
    if (!canCompose) return;
    final body = draftFor(id).trim();
    if (body.isEmpty) return;
    _drafts.remove(id);
    unawaited(_persistDraftsNow());
    final message = OutgoingMessage(
      localId: 'local-${++_localIds}',
      conversationId: id,
      body: body,
      phase: isOnline ? OutgoingPhase.sending : OutgoingPhase.unsentDraft,
    );
    _outgoing.add(message);
    _outgoingSubmissions[message.localId] = source.prepareMessage(id, body);
    _changed();
    if (message.phase == OutgoingPhase.sending) await _submitMessage(message);
  }

  /// Sends one unsent message because the person asked to.
  Future<void> sendUnsent(String localId) async {
    if (!canCompose) return;
    final message = _outgoingById(localId);
    if (message == null || !isOnline) return;
    if (message.phase != OutgoingPhase.unsentDraft) return;
    message
      ..phase = OutgoingPhase.sending
      ..note = null;
    _changed();
    await _submitMessage(message);
  }

  /// Puts an unsent message back into the composer.
  void discardUnsent(String localId) {
    final message = _outgoingById(localId);
    if (message == null) return;
    if (message.phase != OutgoingPhase.unsentDraft &&
        message.phase != OutgoingPhase.refused) {
      return;
    }
    _outgoing.remove(message);
    _outgoingSubmissions.remove(localId);
    _changed();
  }

  OutgoingMessage? _outgoingById(String localId) {
    for (final m in _outgoing) {
      if (m.localId == localId) return m;
    }
    return null;
  }

  Future<void> _submitMessage(OutgoingMessage message) async {
    final pending = _outgoingSubmissions[message.localId]!;
    try {
      await source.submit(pending);
      _delivered(message);
      await _reloadQuietly();
    } on SourceUnavailable catch (e) {
      message
        ..phase = OutgoingPhase.unsentDraft
        ..note = null;
      _goOffline(e.message);
    } on AcknowledgmentUnknown {
      message.phase = OutgoingPhase.acknowledgmentUnknown;
      _changed();
      await checkMessage(message.localId);
    } on SourceRefusal catch (e) {
      message
        ..phase = OutgoingPhase.refused
        ..note = e.message;
      _changed();
    }
  }

  void _delivered(OutgoingMessage message) {
    _outgoing.remove(message);
    _outgoingSubmissions.remove(message.localId);
    _changed();
  }

  /// Looks up a message whose acknowledgment is unknown. Never resends.
  Future<void> checkMessage(String localId) async {
    final message = _outgoingById(localId);
    final pending = _outgoingSubmissions[localId];
    if (message == null || pending == null) return;
    if (message.phase != OutgoingPhase.acknowledgmentUnknown) return;
    try {
      switch (await source.resolve(pending)) {
        case ResolvedAcknowledged():
          _delivered(message);
          await _reloadQuietly();
        case ResolvedRefused(:final refusal):
          message
            ..phase = OutgoingPhase.refused
            ..note = refusal.message;
          _changed();
        case ResolvedNotReceived():
          message
            ..phase = OutgoingPhase.unsentDraft
            ..note = 'Not received.';
          _changed();
      }
    } on SourceUnavailable catch (e) {
      _goOffline(e.message);
    } on AcknowledgmentUnknown {
      message.note = 'Still checking.';
      _changed();
    }
  }

  // ---- details panel ----------------------------------------------------------

  void openDetails([DetailsTab? tab]) {
    detailsOpen = true;
    if (tab != null) detailsTab = tab;
    _changed();
  }

  void closeDetails() {
    detailsOpen = false;
    _changed();
  }

  void setDetailsTab(DetailsTab tab) {
    detailsTab = tab;
    _changed();
  }

  Iterable<T> _inBranch<T>(
    WorkerId worker,
    Iterable<T> items,
    WorkerId Function(T) owner,
  ) {
    final scope = snapshot.subtree(worker);
    return items.where((i) => scope.contains(owner(i)));
  }

  List<TaskEntry> tasksFor(WorkerId w) =>
      _inBranch(w, snapshot.tasks, (t) => t.workerId).toList();

  List<FileEntry> filesFor(WorkerId w) =>
      _inBranch(w, snapshot.files, (f) => f.workerId).toList();

  List<MemoryClaimView> memoryFor(WorkerId w) => [
    for (final m in snapshot.memory)
      if (m.workerId == w) _memoryClaimView(m),
  ];

  MemoryClaimView _memoryClaimView(MemoryEntry claim) {
    final overlay = _memoryOverlays[claim.id];
    return MemoryClaimView(
      claim: claim,
      phase: overlay?.phase,
      note: overlay?.jobStatus,
    );
  }

  List<AccessEntry> accessFor(WorkerId w) => [
    for (final a in snapshot.access)
      if (a.workerId == w) a,
  ];

  SpendingEntry? spendingFor(WorkerId w) {
    for (final s in snapshot.spending) {
      if (s.workerId == w) return s;
    }
    return null;
  }

  /// Capability-specific autonomy evidence for one worker (R16-009): never a
  /// single trust score.
  List<AutonomyEntry> autonomyFor(WorkerId w) => [
    for (final a in snapshot.autonomy)
      if (a.workerId == w) a,
  ];

  /// Recovery obligations any of this worker's still-live runs carry.
  List<RecoveryEntry> recoveryFor(WorkerId w) => [
    for (final r in snapshot.recovery)
      if (r.workerId == w) r,
  ];

  List<ProposalEntry> proposalsFor(WorkerId w) => [
    for (final p in snapshot.proposals)
      if (p.workerId == w) p,
  ];

  List<PrerequisiteNotice> prerequisitesFor(DetailsTab? tab) => [
    for (final p in snapshot.prerequisites)
      if (p.tab == tab) p,
  ];

  List<RoutineView> routinesFor(WorkerId w) => [
    for (final r in _inBranch(w, snapshot.routines, (r) => r.workerId))
      _routineView(r),
  ];

  RoutineView _routineView(RoutineEntry routine) {
    final overlay = _routineOverlays[routine.id];
    final phase =
        overlay?.phase ??
        (routine.paused ? RoutinePhase.paused : RoutinePhase.active);
    return RoutineView(
      routine: routine,
      phase: phase,
      canPause: phase == RoutinePhase.active && isOnline,
      note: overlay?.note,
    );
  }

  /// Pauses a responsibility. It shows as paused only after acknowledgment.
  Future<void> pauseRoutine(RoutineId id) async {
    RoutineEntry? routine;
    for (final r in snapshot.routines) {
      if (r.id == id) routine = r;
    }
    if (routine == null || !_routineView(routine).canPause) return;
    final overlay = _routineOverlays.putIfAbsent(id, _RoutineOverlay.new);
    overlay
      ..pending ??= source.preparePause(routine)
      ..phase = RoutinePhase.submitting
      ..note = null;
    _changed();
    try {
      await source.submit(overlay.pending!);
      overlay
        ..phase = RoutinePhase.paused
        ..pending = null;
      _changed();
      await _reloadQuietly();
    } on SourceUnavailable catch (e) {
      overlay
        ..phase = null
        ..note = 'Not sent. The responsibility is still active.';
      _goOffline(e.message);
    } on AcknowledgmentUnknown {
      overlay.phase = RoutinePhase.acknowledgmentUnknown;
      _changed();
      await checkPause(id);
    } on SourceRefusal catch (e) {
      overlay
        ..phase = null
        ..pending = null
        ..note = e.message;
      _changed();
    }
  }

  /// Looks up a pause whose acknowledgment is unknown. Never resends.
  Future<void> checkPause(RoutineId id) async {
    final overlay = _routineOverlays[id];
    final pending = overlay?.pending;
    if (overlay == null ||
        pending == null ||
        overlay.phase != RoutinePhase.acknowledgmentUnknown) {
      return;
    }
    try {
      switch (await source.resolve(pending)) {
        case ResolvedAcknowledged():
          overlay
            ..phase = RoutinePhase.paused
            ..pending = null;
          _changed();
          await _reloadQuietly();
        case ResolvedRefused(:final refusal):
          overlay
            ..phase = null
            ..pending = null
            ..note = refusal.message;
          _changed();
        case ResolvedNotReceived():
          overlay
            ..phase = null
            ..note = 'The pause was not received. It is still active.';
          _changed();
      }
    } on SourceUnavailable catch (e) {
      _goOffline(e.message);
    } on AcknowledgmentUnknown {
      overlay.note = 'Still checking.';
      _changed();
    }
  }

  // ---- memory: authorized claims, source/freshness and retraction --------

  /// Retracts a memory claim. Shown as done only once the controller
  /// acknowledges the retraction *request* — retraction itself is a `Job`,
  /// so "acknowledged" means accepted, not yet necessarily applied; the
  /// acknowledgment rule applies to the job's own state exactly as it does
  /// to a review or a pause.
  Future<void> retractClaim(ClaimId id, String reason) async {
    MemoryEntry? claim;
    for (final m in snapshot.memory) {
      if (m.id == id) claim = m;
    }
    if (claim == null || !_memoryClaimView(claim).canRetract) return;
    final overlay = _memoryOverlays.putIfAbsent(id, _MemoryOverlay.new);
    overlay
      ..pending ??= source.prepareMemoryRetract(claim, reason)
      ..phase = ClaimActionPhase.submitting
      ..jobStatus = null;
    _changed();
    try {
      await source.submit(overlay.pending!);
      final result = overlay.pending!.result;
      overlay
        ..phase = ClaimActionPhase.requested
        ..jobId = result?.jobId
        ..jobStatus = result?.jobStatus
        ..pending = null;
      _changed();
      await _reloadQuietly();
    } on SourceUnavailable catch (e) {
      overlay
        ..phase = null
        ..jobStatus = 'Not sent. The claim is still active.';
      _goOffline(e.message);
    } on AcknowledgmentUnknown {
      overlay.phase = ClaimActionPhase.acknowledgmentUnknown;
      _changed();
      await checkMemoryRetract(id);
    } on SourceRefusal catch (e) {
      overlay
        ..phase = null
        ..pending = null
        ..jobStatus = e.message;
      _changed();
    }
  }

  /// Looks up a retraction request whose acknowledgment is unknown. Never
  /// resends.
  Future<void> checkMemoryRetract(ClaimId id) async {
    final overlay = _memoryOverlays[id];
    final pending = overlay?.pending;
    if (overlay == null ||
        pending == null ||
        overlay.phase != ClaimActionPhase.acknowledgmentUnknown) {
      return;
    }
    try {
      switch (await source.resolve(pending)) {
        case ResolvedAcknowledged():
          final result = pending.result;
          overlay
            ..phase = ClaimActionPhase.requested
            ..jobId = result?.jobId
            ..jobStatus = result?.jobStatus
            ..pending = null;
          _changed();
          await _reloadQuietly();
        case ResolvedRefused(:final refusal):
          overlay
            ..phase = null
            ..pending = null
            ..jobStatus = refusal.message;
          _changed();
        case ResolvedNotReceived():
          overlay
            ..phase = null
            ..jobStatus =
                'The retraction was not received. The claim is still active.';
          _changed();
      }
    } on SourceUnavailable catch (e) {
      _goOffline(e.message);
    } on AcknowledgmentUnknown {
      overlay.jobStatus = 'Still checking.';
      _changed();
    }
  }

  /// Re-checks a retraction job already known to have been accepted,
  /// refreshing its status in words. Never a background poll: called only
  /// when the person asks.
  Future<void> checkMemoryJobStatus(ClaimId id) async {
    final overlay = _memoryOverlays[id];
    final jobId = overlay?.jobId;
    if (overlay == null || jobId == null) return;
    try {
      overlay.jobStatus = await source.checkMemoryJob(jobId);
      _changed();
      await _reloadQuietly();
    } on SourceUnavailable catch (e) {
      _goOffline(e.message);
    } on AcknowledgmentUnknown {
      // Nothing new yet; keep the last known status.
    }
  }

  // ---- organization/worker/group/task/responsibility creation -----------

  /// The signed-in human's own principal id, derived from an existing
  /// direct conversation's real participants — this client has no
  /// `identity.current` operation (see `live_source.dart`'s class doc), so
  /// it never guesses which principal is "me"; it reads which participant
  /// of an already-known direct chat is not a worker. Null only before the
  /// first snapshot has loaded.
  String? get humanPrincipalId {
    final workerIds = {for (final w in snapshot.workers) w.id.value};
    for (final c in snapshot.conversations) {
      if (c.kind != ConversationKind.direct) continue;
      for (final id in c.participantIds) {
        if (!workerIds.contains(id)) return id;
      }
    }
    return null;
  }

  /// Setup/prerequisite cards specific to one worker: no execution profile,
  /// no configured budget, a credential that needs action.
  List<PrerequisiteNotice> prerequisitesForWorker(WorkerId id) => [
    for (final p in snapshot.prerequisites)
      if (p.workerId == id) p,
  ];

  /// Real external operations no review names, kept visible rather than
  /// dropped while their disposition is unresolved.
  List<UnresolvedOperationEntry> unresolvedOperationsFor(WorkerId worker) {
    final scope = snapshot.subtree(worker);
    return [
      for (final op in snapshot.unresolvedOperations)
        if (op.workerId != null && scope.contains(op.workerId)) op,
    ];
  }

  // -- draft/plan/review/apply: new organization, new worker, new
  // responsibility --

  DraftFlowView draftFlow(DraftFlowKind kind) {
    final o = _draftFlows[kind];
    if (o == null) return const DraftFlowView();
    return DraftFlowView(
      phase: o.phase,
      plan: o.plan,
      note: o.note,
      createdId: o.draft?.resourceId,
    );
  }

  void cancelDraftFlow(DraftFlowKind kind) {
    _draftFlows.remove(kind);
    _changed();
  }

  Future<void> createOrganization({
    required String key,
    required String name,
    String? parentOrganizationId,
    required String chiefKey,
    required String chiefName,
    required String chiefPurpose,
    required String chiefInstructions,
  }) => _startDraftFlow(
    DraftFlowKind.organization,
    () => source.prepareCreateOrganization(
      key: key,
      name: name,
      parentOrganizationId: parentOrganizationId,
      chiefKey: chiefKey,
      chiefName: chiefName,
      chiefPurpose: chiefPurpose,
      chiefInstructions: chiefInstructions,
    ),
  );

  Future<void> createWorker({
    required String organizationId,
    required String key,
    required String name,
    required String purpose,
    required String instructions,
  }) => _startDraftFlow(
    DraftFlowKind.worker,
    () => source.prepareCreateWorker(
      organizationId: organizationId,
      key: key,
      name: name,
      purpose: purpose,
      instructions: instructions,
    ),
  );

  Future<void> createResponsibility({
    required String workerId,
    required String outcome,
    required List<String> triggers,
    required int minIntervalSeconds,
    required VerifierIdentity verifier,
    String currency = 'XXX',
  }) => _startDraftFlow(
    DraftFlowKind.responsibility,
    () => source.prepareCreateResponsibility(
      workerId: workerId,
      outcome: outcome,
      triggers: triggers,
      minIntervalSeconds: minIntervalSeconds,
      verifier: verifier,
      rootDeadline: _clock().add(const Duration(hours: 24)),
      currency: currency,
    ),
  );

  Future<void> _startDraftFlow(
    DraftFlowKind kind,
    ResourceSubmission<DraftedResource> Function() prepare,
  ) async {
    final flow = _DraftFlowOverlay()
      ..stagePending = prepare()
      ..phase = DraftFlowPhase.staging;
    _draftFlows[kind] = flow;
    _changed();
    await _submitStage(kind, flow);
  }

  Future<void> _submitStage(DraftFlowKind kind, _DraftFlowOverlay flow) async {
    try {
      await source.submit(flow.stagePending!);
      flow
        ..draft = flow.stagePending!.result
        ..stagePending = null;
      await _startPlan(kind, flow);
    } on SourceUnavailable catch (e) {
      flow
        ..phase = DraftFlowPhase.failed
        ..note = 'Not sent. ${e.message}';
      _changed();
    } on AcknowledgmentUnknown {
      flow.phase = DraftFlowPhase.stagingUnknown;
      _changed();
    } on SourceRefusal catch (e) {
      flow
        ..phase = DraftFlowPhase.failed
        ..note = e.message
        ..stagePending = null;
      _changed();
    }
  }

  Future<void> _startPlan(DraftFlowKind kind, _DraftFlowOverlay flow) async {
    final draft = flow.draft!;
    flow
      ..planPending = source.preparePlan(
        draftId: draft.draftId,
        expectedVersion: draft.draftVersion,
      )
      ..phase = DraftFlowPhase.planning
      ..note = null;
    _changed();
    await _submitPlan(kind, flow);
  }

  Future<void> _submitPlan(DraftFlowKind kind, _DraftFlowOverlay flow) async {
    try {
      await source.submit(flow.planPending!);
      final plan = flow.planPending!.result!;
      flow
        ..plan = plan
        ..planPending = null
        ..phase = plan.isClean ? DraftFlowPhase.ready : DraftFlowPhase.blocked;
      _changed();
    } on SourceUnavailable catch (e) {
      flow
        ..phase = DraftFlowPhase.failed
        ..note = 'Not sent. ${e.message}';
      _changed();
    } on AcknowledgmentUnknown {
      flow.phase = DraftFlowPhase.planningUnknown;
      _changed();
    } on SourceRefusal catch (e) {
      flow
        ..phase = DraftFlowPhase.failed
        ..note = e.message
        ..planPending = null;
      _changed();
    }
  }

  /// The person's explicit apply, after reviewing the plan. Only allowed
  /// once the plan is [DraftFlowPhase.ready]: clean, nothing outstanding.
  Future<void> applyDraftFlow(DraftFlowKind kind) async {
    final flow = _draftFlows[kind];
    if (flow == null || flow.phase != DraftFlowPhase.ready) return;
    flow
      ..applyPending = source.prepareApplyPlan(flow.plan!)
      ..phase = DraftFlowPhase.applying
      ..note = null;
    _changed();
    await _submitApply(kind, flow);
  }

  Future<void> _submitApply(DraftFlowKind kind, _DraftFlowOverlay flow) async {
    try {
      await source.submit(flow.applyPending!);
      flow
        ..applyPending = null
        ..phase = DraftFlowPhase.applied;
      _changed();
      await _afterDraftApplied(kind, flow);
    } on SourceUnavailable catch (e) {
      flow
        ..phase = DraftFlowPhase.failed
        ..note = 'Not sent. ${e.message}';
      _changed();
    } on AcknowledgmentUnknown {
      flow.phase = DraftFlowPhase.applyingUnknown;
      _changed();
    } on SourceRefusal catch (e) {
      flow
        ..phase = DraftFlowPhase.failed
        ..note = e.message
        ..applyPending = null;
      _changed();
    }
  }

  /// Reloads the snapshot now that a draft is active, and — for a new
  /// organization or worker — opens the direct conversation the design of
  /// record expects. The new chief/worker is otherwise unreachable from
  /// this app: the compiler never stages a conversation (it is not one of
  /// the `Change` kinds), so this flow drives the real membership operation
  /// itself once the worker actually exists.
  Future<void> _afterDraftApplied(
    DraftFlowKind kind,
    _DraftFlowOverlay flow,
  ) async {
    await _reloadQuietly();
    final targetWorkerId = flow.draft?.conversationTargetId;
    final human = humanPrincipalId;
    if (targetWorkerId == null || human == null) return;
    final worker = snapshot.worker(WorkerId(targetWorkerId));
    if (worker == null || worker.conversationId != null) return;
    try {
      final submission = source.prepareOpenDirectConversation(
        humanPrincipalId: human,
        workerId: targetWorkerId,
        title: worker.name,
      );
      await source.submit(submission);
      await _reloadQuietly();
      selectWorker(worker.id);
    } on SourceUnavailable {
      // Best-effort: the organization/worker is real either way. The
      // conversation can be opened again from the tree once reachable.
    } on AcknowledgmentUnknown {
      // Same: a later poll/snapshot shows the conversation if it landed.
    } on SourceRefusal {
      // Same.
    }
  }

  /// Looks up whichever step's acknowledgment is unknown. Never resends.
  Future<void> checkDraftFlow(DraftFlowKind kind) async {
    final flow = _draftFlows[kind];
    if (flow == null) return;
    try {
      switch (flow.phase) {
        case DraftFlowPhase.stagingUnknown:
          await _resolveStage(kind, flow);
        case DraftFlowPhase.planningUnknown:
          await _resolvePlan(kind, flow);
        case DraftFlowPhase.applyingUnknown:
          await _resolveApply(kind, flow);
        default:
          return;
      }
    } on SourceUnavailable catch (e) {
      _goOffline(e.message);
    }
  }

  Future<void> _resolveStage(DraftFlowKind kind, _DraftFlowOverlay flow) async {
    try {
      switch (await source.resolve(flow.stagePending!)) {
        case ResolvedAcknowledged():
          flow
            ..draft = flow.stagePending!.result
            ..stagePending = null;
          await _startPlan(kind, flow);
        case ResolvedRefused(:final refusal):
          flow
            ..phase = DraftFlowPhase.failed
            ..note = refusal.message
            ..stagePending = null;
          _changed();
        case ResolvedNotReceived():
          flow
            ..phase = DraftFlowPhase.failed
            ..note = 'Not received. Nothing was created; try again.';
          _changed();
      }
    } on AcknowledgmentUnknown {
      flow.note = 'Still checking.';
      _changed();
    }
  }

  Future<void> _resolvePlan(DraftFlowKind kind, _DraftFlowOverlay flow) async {
    try {
      switch (await source.resolve(flow.planPending!)) {
        case ResolvedAcknowledged():
          final plan = flow.planPending!.result!;
          flow
            ..plan = plan
            ..planPending = null
            ..phase = plan.isClean
                ? DraftFlowPhase.ready
                : DraftFlowPhase.blocked;
          _changed();
        case ResolvedRefused(:final refusal):
          flow
            ..phase = DraftFlowPhase.failed
            ..note = refusal.message
            ..planPending = null;
          _changed();
        case ResolvedNotReceived():
          flow
            ..phase = DraftFlowPhase.failed
            ..note = 'Not received. The draft was not planned; try again.';
          _changed();
      }
    } on AcknowledgmentUnknown {
      flow.note = 'Still checking.';
      _changed();
    }
  }

  Future<void> _resolveApply(DraftFlowKind kind, _DraftFlowOverlay flow) async {
    try {
      switch (await source.resolve(flow.applyPending!)) {
        case ResolvedAcknowledged():
          flow
            ..applyPending = null
            ..phase = DraftFlowPhase.applied;
          _changed();
          await _afterDraftApplied(kind, flow);
        case ResolvedRefused(:final refusal):
          flow
            ..phase = DraftFlowPhase.failed
            ..note = refusal.message
            ..applyPending = null;
          _changed();
        case ResolvedNotReceived():
          flow
            ..phase = DraftFlowPhase.failed
            ..note = 'Not received. Nothing was activated; try again.';
          _changed();
      }
    } on AcknowledgmentUnknown {
      flow.note = 'Still checking.';
      _changed();
    }
  }

  /// Resends whichever step provably sent nothing (or was proven not
  /// received). The same frozen submission is reused; nothing is
  /// re-prepared.
  Future<void> retryDraftFlow(DraftFlowKind kind) async {
    final flow = _draftFlows[kind];
    if (flow == null) return;
    if (flow.applyPending != null) {
      flow
        ..phase = DraftFlowPhase.applying
        ..note = null;
      _changed();
      await _submitApply(kind, flow);
    } else if (flow.planPending != null) {
      flow
        ..phase = DraftFlowPhase.planning
        ..note = null;
      _changed();
      await _submitPlan(kind, flow);
    } else if (flow.stagePending != null) {
      flow
        ..phase = DraftFlowPhase.staging
        ..note = null;
      _changed();
      await _submitStage(kind, flow);
    }
  }

  // -- group chats: an immediate conversation.create/update, no compiler --

  bool get groupFlowBusy => _groupFlow?.submitting ?? false;
  bool get groupFlowUnknown => _groupFlow?.unknown ?? false;
  String? get groupFlowNote => _groupFlow?.note;

  void cancelGroupFlow() {
    _groupFlow = null;
    _changed();
  }

  /// Creates a group chat with the given participants (the signed-in human
  /// is added automatically). Immediate: group chats carry no org/grant/
  /// memory permission, so nothing is staged through the compiler.
  Future<void> createGroup({
    required String title,
    required List<String> workerParticipantIds,
  }) async {
    final human = humanPrincipalId;
    if (human == null) return;
    final overlay = _groupFlow = _GroupFlowOverlay()
      ..pending = source.prepareCreateGroup(
        title: title,
        participantIds: {human, ...workerParticipantIds}.toList(),
      )
      ..submitting = true;
    _changed();
    await _submitGroup(overlay, onDone: selectGroup);
  }

  /// Adds an existing worker to a group conversation: a real membership
  /// operation (`conversation.update`), never a locally invented list.
  Future<void> addParticipantToGroup(
    ConversationId group,
    String workerId,
  ) async {
    final conversation = snapshot.conversation(group);
    if (conversation == null) return;
    final overlay = _groupFlow = _GroupFlowOverlay()
      ..pending = source.prepareAddParticipant(
        conversation: conversation,
        expectedVersion: conversation.version,
        newParticipantId: workerId,
      )
      ..submitting = true;
    _changed();
    await _submitGroup(overlay, onDone: (_) {});
  }

  Future<void> _submitGroup(
    _GroupFlowOverlay overlay, {
    required void Function(ConversationId) onDone,
  }) async {
    try {
      await source.submit(overlay.pending!);
      final outcome = overlay.pending!.result;
      overlay
        ..submitting = false
        ..pending = null;
      _changed();
      await _reloadQuietly();
      if (outcome != null) onDone(ConversationId(outcome.id));
    } on SourceUnavailable catch (e) {
      overlay
        ..submitting = false
        ..note = 'Not sent. ${e.message}';
      _goOffline(e.message);
    } on AcknowledgmentUnknown {
      overlay
        ..submitting = false
        ..unknown = true;
      _changed();
    } on SourceRefusal catch (e) {
      overlay
        ..submitting = false
        ..pending = null
        ..note = e.message;
      _changed();
    }
  }

  /// Looks up an unknown acknowledgment. Never resends.
  Future<void> checkGroupFlow() async {
    final overlay = _groupFlow;
    final pending = overlay?.pending;
    if (overlay == null || pending == null || !overlay.unknown) return;
    try {
      switch (await source.resolve(pending)) {
        case ResolvedAcknowledged():
          final outcome = pending.result;
          overlay
            ..unknown = false
            ..pending = null;
          _changed();
          await _reloadQuietly();
          if (outcome != null) selectGroup(ConversationId(outcome.id));
        case ResolvedRefused(:final refusal):
          overlay
            ..unknown = false
            ..pending = null
            ..note = refusal.message;
          _changed();
        case ResolvedNotReceived():
          overlay
            ..unknown = false
            ..note = 'Not received. Try again.';
          _changed();
      }
    } on SourceUnavailable catch (e) {
      _goOffline(e.message);
    } on AcknowledgmentUnknown {
      overlay.note = 'Still checking.';
      _changed();
    }
  }

  // -- bounded tasks: create, then start; or delegate a child task --

  TaskFlowView get taskFlow {
    final o = _taskFlow;
    if (o == null) return const TaskFlowView();
    return TaskFlowView(phase: o.phase, note: o.note);
  }

  void cancelTaskFlow() {
    _taskFlow = null;
    _changed();
  }

  Future<void> createTask({
    required String workerId,
    required String outcome,
    required List<String> requiredOutputs,
    required VerifierIdentity verifier,
    String currency = 'XXX',
  }) async {
    final owner = humanPrincipalId;
    if (owner == null) return;
    await _startTaskFlow(
      () => source.prepareCreateTask(
        ownerId: owner,
        workerId: workerId,
        outcome: outcome,
        requiredOutputs: requiredOutputs,
        verifier: verifier,
        rootDeadline: _clock().add(const Duration(hours: 24)),
        currency: currency,
      ),
    );
  }

  Future<void> delegateTask({
    required TaskEntry parent,
    required String childWorkerId,
    required String outcome,
    required List<String> requiredOutputs,
    required VerifierIdentity verifier,
    String currency = 'XXX',
  }) => _startTaskFlow(
    () => source.prepareDelegateTask(
      parentId: parent.id,
      parentExpectedVersion: parent.version,
      ownerId: parent.ownerId,
      childWorkerId: childWorkerId,
      outcome: outcome,
      requiredOutputs: requiredOutputs,
      verifier: verifier,
      rootDeadline: _clock().add(const Duration(hours: 24)),
      currency: currency,
    ),
  );

  Future<void> _startTaskFlow(
    ResourceSubmission<TaskOutcome> Function() prepare,
  ) async {
    final flow = _TaskFlowOverlay()
      ..createPending = prepare()
      ..phase = TaskFlowPhase.creating;
    _taskFlow = flow;
    _changed();
    await _submitTaskCreate(flow);
  }

  Future<void> _submitTaskCreate(_TaskFlowOverlay flow) async {
    try {
      await source.submit(flow.createPending!);
      flow
        ..created = flow.createPending!.result
        ..createPending = null
        ..phase = TaskFlowPhase.created;
      _changed();
      await _startTaskStart(flow);
    } on SourceUnavailable catch (e) {
      flow
        ..phase = TaskFlowPhase.failed
        ..note = 'Not sent. ${e.message}';
      _changed();
    } on AcknowledgmentUnknown {
      flow.phase = TaskFlowPhase.creatingUnknown;
      _changed();
    } on SourceRefusal catch (e) {
      flow
        ..phase = TaskFlowPhase.failed
        ..note = e.message
        ..createPending = null;
      _changed();
    }
  }

  Future<void> _startTaskStart(_TaskFlowOverlay flow) async {
    final created = flow.created!;
    flow
      ..startPending = source.prepareStartTask(
        id: created.id,
        expectedVersion: created.version,
      )
      ..phase = TaskFlowPhase.starting
      ..note = null;
    _changed();
    await _submitTaskStart(flow);
  }

  Future<void> _submitTaskStart(_TaskFlowOverlay flow) async {
    try {
      await source.submit(flow.startPending!);
      flow
        ..startPending = null
        ..phase = TaskFlowPhase.started;
      _changed();
      await _reloadQuietly();
    } on SourceUnavailable catch (e) {
      flow
        ..phase = TaskFlowPhase.failed
        ..note = 'Not sent. ${e.message}';
      _changed();
    } on AcknowledgmentUnknown {
      flow.phase = TaskFlowPhase.startingUnknown;
      _changed();
    } on SourceRefusal catch (e) {
      flow
        ..phase = TaskFlowPhase.failed
        ..note = e.message
        ..startPending = null;
      _changed();
    }
  }

  /// Looks up whichever step's acknowledgment is unknown. Never resends.
  Future<void> checkTaskFlow() async {
    final flow = _taskFlow;
    if (flow == null) return;
    try {
      if (flow.phase == TaskFlowPhase.creatingUnknown) {
        switch (await source.resolve(flow.createPending!)) {
          case ResolvedAcknowledged():
            flow
              ..created = flow.createPending!.result
              ..createPending = null
              ..phase = TaskFlowPhase.created;
            _changed();
            await _startTaskStart(flow);
          case ResolvedRefused(:final refusal):
            flow
              ..phase = TaskFlowPhase.failed
              ..note = refusal.message
              ..createPending = null;
            _changed();
          case ResolvedNotReceived():
            flow
              ..phase = TaskFlowPhase.failed
              ..note = 'Not received. Nothing was created; try again.';
            _changed();
        }
      } else if (flow.phase == TaskFlowPhase.startingUnknown) {
        switch (await source.resolve(flow.startPending!)) {
          case ResolvedAcknowledged():
            flow
              ..startPending = null
              ..phase = TaskFlowPhase.started;
            _changed();
            await _reloadQuietly();
          case ResolvedRefused(:final refusal):
            flow
              ..phase = TaskFlowPhase.failed
              ..note = refusal.message
              ..startPending = null;
            _changed();
          case ResolvedNotReceived():
            flow
              ..phase = TaskFlowPhase.failed
              ..note = 'Not received. The task was not started; try again.';
            _changed();
        }
      }
    } on SourceUnavailable catch (e) {
      _goOffline(e.message);
    } on AcknowledgmentUnknown {
      flow.note = 'Still checking.';
      _changed();
    }
  }

  /// A manually-decided task's own human review: the same "eligible human
  /// review" the acceptance contract requires (`api/acceptance.dart`).
  Future<void> decideTask(TaskEntry task, {required bool accept}) async {
    try {
      final submission = source.prepareAcceptTask(
        id: task.id,
        expectedVersion: task.version,
        accept: accept,
      );
      await source.submit(submission);
      await _reloadQuietly();
    } on SourceUnavailable catch (e) {
      _goOffline(e.message);
    } on AcknowledgmentUnknown {
      // A later snapshot shows the decision if it landed; the person can
      // look again rather than risk a duplicate decision.
    } on SourceRefusal {
      // The task's own state (re-read on the next snapshot) is the record.
    }
  }

  // -- task result artifacts: eligible human review of exact content -----

  Future<List<TaskArtifactEntry>> loadTaskArtifacts(String taskId) =>
      source.loadTaskArtifacts(taskId);

  Future<ReviewContentPart> readTaskArtifact(TaskArtifactEntry artifact) =>
      source.readTaskArtifact(artifact);

  Future<List<VerifierIdentity>> loadTrustedVerifiers() =>
      source.loadTrustedVerifiers();
}
