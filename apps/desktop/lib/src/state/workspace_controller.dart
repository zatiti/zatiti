// The workspace view-state controller. It owns no business rules: every
// displayed fact comes from a source snapshot, and an approval, a pause or a
// sent message shows as done only after the source acknowledges it.

import 'package:flutter/foundation.dart';

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

class WorkspaceController extends ChangeNotifier {
  WorkspaceController(this.source, {DateTime Function()? clock})
    : _clock = clock ?? (() => DateTime.now().toUtc());

  final WorkspaceSource source;
  final DateTime Function() _clock;

  // ---- connection -------------------------------------------------------

  ConnectionPhase connection = ConnectionPhase.connecting;

  /// Why the workspace is offline, in words for the person.
  String? connectionMessage;

  WorkspaceSnapshot snapshot = WorkspaceSnapshot.empty;

  bool get isOnline => connection == ConnectionPhase.online;

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
  final Map<ConversationId, String> _drafts = <ConversationId, String>{};

  final List<OutgoingMessage> _outgoing = <OutgoingMessage>[];
  final Map<String, PendingSubmission> _outgoingSubmissions =
      <String, PendingSubmission>{};
  final Map<ReviewId, _ReviewOverlay> _reviewOverlays =
      <ReviewId, _ReviewOverlay>{};
  final Map<RoutineId, _RoutineOverlay> _routineOverlays =
      <RoutineId, _RoutineOverlay>{};
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
    }
    if (selectedGroup == null &&
        (selectedWorker == null || next.worker(selectedWorker!) == null)) {
      final roots = next.childrenOf(null);
      selectedWorker = roots.isEmpty ? null : roots.first.id;
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
  }

  void selectGroup(ConversationId id) {
    if (snapshot.conversation(id)?.kind != ConversationKind.group) return;
    selectedGroup = id;
    selectedWorker = null;
    _changed();
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
    prerequisites: snapshot.prerequisites,
    workspaceName: snapshot.workspaceName,
  );

  Future<void> _reloadQuietly() async {
    try {
      _adopt(await source.loadSnapshot());
      _changed();
    } on SourceUnavailable catch (e) {
      _goOffline(e.message);
    } on AcknowledgmentUnknown {
      // Keep the acknowledged overlay; the next poll refreshes the record.
    } on SourceRefusal {
      // Same: the acknowledgment stands on its own.
    }
  }

  // ---- conversation and composer --------------------------------------------

  ConversationEntry? get selectedConversation {
    final group = selectedGroup;
    if (group != null) return snapshot.conversation(group);
    final worker = selectedWorker == null
        ? null
        : snapshot.worker(selectedWorker!);
    final id = worker?.conversationId;
    return id == null ? null : snapshot.conversation(id);
  }

  String draftFor(ConversationId id) => _drafts[id] ?? '';

  /// Keeps a per-conversation draft. Drafts are local text, never sent.
  void setDraft(ConversationId id, String text) {
    if (text.isEmpty) {
      _drafts.remove(id);
    } else {
      _drafts[id] = text;
    }
  }

  List<OutgoingMessage> outgoingFor(ConversationId id) => [
    for (final m in _outgoing)
      if (m.conversationId == id) m,
  ];

  /// Sends the conversation's draft. Offline, the message is kept and shown
  /// as unsent; it is never queued for automatic delivery.
  Future<void> sendDraft(ConversationId id) async {
    final body = draftFor(id).trim();
    if (body.isEmpty) return;
    _drafts.remove(id);
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

  List<MemoryEntry> memoryFor(WorkerId w) => [
    for (final m in snapshot.memory)
      if (m.workerId == w) m,
  ];

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
}
