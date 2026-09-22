// An in-memory demo source seeded with the design study's synthetic records.
//
// Nothing here talks to a controller, a model or a network. Every record is
// fictional. The label says so and the UI shows it at all times. The design
// study's email batch is not a v1 capability, so the pending decision here is
// a repository action instead.

import 'snapshot.dart';
import 'workspace_source.dart';

const demoSourceLabel = 'Demo data · not connected to a controller';

/// What the demo does with the next submission, so the unhappy paths can be
/// explored and tested without a controller.
enum DemoFault {
  none,
  unavailable,
  unknownThenFound,
  unknownThenNotReceived,
  stale,
}

class _DemoSubmission implements PendingSubmission {
  _DemoSubmission(this.description, this.apply);

  @override
  final String description;
  final void Function() apply;
  bool committed = false;
  int sends = 0;
}

/// The demo's [ResourceSubmission]: [_produce] both mutates the in-memory
/// demo state and returns the decoded result, mirroring how the live
/// source's decode callback runs only once the controller acknowledges.
class _DemoResourceSubmission<T> implements ResourceSubmission<T> {
  _DemoResourceSubmission(this.description, this._produce);

  @override
  final String description;
  final T Function() _produce;
  bool committed = false;
  int sends = 0;

  @override
  T? result;
}

class DemoWorkspaceSource implements WorkspaceSource {
  DemoWorkspaceSource({DateTime Function()? clock})
    : _clock = clock ?? (() => DateTime.now().toUtc());

  final DateTime Function() _clock;

  static const wren = WorkerId('demo-worker-wren');
  static const marketing = WorkerId('demo-worker-marketing');
  static const research = WorkerId('demo-worker-research');
  static const outreach = WorkerId('demo-worker-outreach');
  static const engineering = WorkerId('demo-worker-engineering');
  static const quality = WorkerId('demo-worker-quality');
  static const ledger = WorkerId('demo-worker-ledger');
  static const pullRequestReview = ReviewId('demo-review-pull-request');
  static const reconciliation = RoutineId('demo-routine-reconciliation');
  static const planningGroup = ConversationId('demo-conversation-planning');

  /// Simulates an unreachable controller.
  bool offline = false;

  /// Applied to the next submission, then reset.
  DemoFault nextFault = DemoFault.none;

  /// Awaited before each submission is processed, so a test can hold one in
  /// flight.
  Future<void> Function()? beforeSubmit;

  ReviewRecordState _reviewState = ReviewRecordState.pending;
  int _reviewVersion = 1;
  bool _routinePaused = false;
  bool _memoryRetracted = false;
  bool _dirty = false;
  final Map<ConversationId, List<ChatMessage>> _sent = {};
  int _messageIds = 0;

  // ---- organization/worker/group/task/responsibility creation, in memory

  int _createdIds = 0;
  String _newId(String prefix) => 'demo-$prefix-${++_createdIds}';

  final List<WorkerEntry> _extraWorkers = [];

  /// [w] with its real conversation id attached, found the same way the
  /// live source's `_tree` does: the direct conversation scoped to it.
  /// [_extraWorkers] entries never bake this in at creation, since the
  /// conversation is opened by a separate call after the worker exists.
  WorkerEntry _attachConversation(WorkerEntry w) {
    if (w.conversationId != null) return w;
    for (final c in _extraConversations) {
      if (c.kind == ConversationKind.direct && c.workerId == w.id) {
        return WorkerEntry(
          id: w.id,
          name: w.name,
          role: w.role,
          organizationId: w.organizationId,
          organizationPath: w.organizationPath,
          parentId: w.parentId,
          conversationId: c.id,
          isOrganizationChief: w.isOrganizationChief,
          preview: w.preview,
        );
      }
    }
    return w;
  }

  final List<ConversationEntry> _extraConversations = [];
  final List<TaskEntry> _extraTasks = [];
  final List<RoutineEntry> _extraRoutines = [];
  static const humanPrincipal = 'demo-principal-owner';
  static const _verifier = VerifierIdentity(id: 'demo-verifier', version: 1);

  /// Every submission that reached the pretend controller, for tests.
  final List<String> committed = [];

  @override
  SourceKind get kind => SourceKind.demo;

  @override
  String get label => demoSourceLabel;

  static ConversationId conversationOf(WorkerId w) =>
      ConversationId('demo-conversation-${w.value}');

  void _requireOnline() {
    if (offline) {
      throw const SourceUnavailable('The demo is simulating being offline.');
    }
  }

  @override
  Future<bool> hasChanges() async {
    _requireOnline();
    final was = _dirty;
    _dirty = false;
    return was;
  }

  @override
  Future<ReviewEntry> refreshReview(ReviewId id) async {
    _requireOnline();
    return _review();
  }

  @override
  Future<List<ChatMessage>> loadMessages(ConversationId conversation) async {
    _requireOnline();
    // The demo builds every conversation's full message list fresh inside
    // loadSnapshot (fixed sample text plus anything since sent) and caches
    // it here so this read does not re-run loadSnapshot's own side effects
    // (it clears the dirty flag other callers rely on).
    return _lastMessages[conversation] ?? const [];
  }

  final Map<ConversationId, List<ChatMessage>> _lastMessages = {};

  /// Records each conversation's messages for [loadMessages] to return,
  /// without re-running loadSnapshot's own side effects.
  List<ConversationEntry> _cacheMessages(List<ConversationEntry> entries) {
    for (final c in entries) {
      _lastMessages[c.id] = c.messages;
    }
    return entries;
  }

  @override
  PendingSubmission prepareDecision(ReviewEntry review, DecisionChoice choice) {
    final version = review.version;
    return _DemoSubmission('decide ${review.id.value} ${choice.name}', () {
      if (version != _reviewVersion) {
        throw const SourceRefusal(
          RefusalKind.stale,
          'This request changed after you opened it.',
        );
      }
      if (_reviewState != ReviewRecordState.pending) {
        throw const SourceRefusal(
          RefusalKind.other,
          'A decision was already recorded.',
        );
      }
      _reviewState = choice == DecisionChoice.approve
          ? ReviewRecordState.approved
          : ReviewRecordState.rejected;
    });
  }

  @override
  PendingSubmission prepareMessage(ConversationId conversation, String body) {
    return _DemoSubmission('message to ${conversation.value}', () {
      final now = _clock();
      (_sent[conversation] ??= []).addAll([
        ChatMessage(
          id: 'demo-message-${++_messageIds}',
          fromUser: true,
          senderName: 'You',
          body: body,
          at: now,
        ),
        ChatMessage(
          id: 'demo-message-${++_messageIds}',
          fromUser: false,
          senderName: 'Demo response',
          body:
              'This is demo data. No worker or model received your '
              'message, so there is no real reply.',
          at: now,
        ),
      ]);
    });
  }

  @override
  PendingSubmission preparePause(RoutineEntry routine) =>
      _DemoSubmission('pause ${routine.id.value}', () => _routinePaused = true);

  @override
  ResourceSubmission<MemoryRetractOutcome> prepareMemoryRetract(
    MemoryEntry claim,
    String reason,
  ) => _DemoResourceSubmission('retract ${claim.id.value}', () {
    _memoryRetracted = true;
    return const MemoryRetractOutcome(
      jobId: 'demo-job-retract',
      jobStatus: 'Done',
    );
  });

  @override
  Future<String> checkMemoryJob(String jobId) async => 'Done';

  // ---- organization/worker/group/task/responsibility creation -----------
  //
  // The demo has no compiler: creation happens on the "create" step's own
  // commit, and `preparePlan`/`prepareApplyPlan` are trivial pass-throughs
  // so the same dialog flow the live source drives (stage → plan → review →
  // apply) still exercises every step and every acknowledgment path here.

  @override
  ResourceSubmission<DraftedResource> prepareCreateOrganization({
    required String key,
    required String name,
    String? parentOrganizationId,
    required String chiefKey,
    required String chiefName,
    required String chiefPurpose,
    required String chiefInstructions,
  }) => _DemoResourceSubmission('create organization $key', () {
    final orgId = _newId('org');
    final chiefId = WorkerId(_newId('worker'));
    final parent = parentOrganizationId == null
        ? null
        : WorkerId(parentOrganizationId);
    _extraWorkers.add(
      WorkerEntry(
        id: chiefId,
        name: chiefName,
        role: 'Organization chief',
        organizationId: OrganizationId(orgId),
        organizationPath: const ['Personal', 'New organization'],
        parentId: parent ?? wren,
        isOrganizationChief: true,
      ),
    );
    return DraftedResource(
      draftId: _newId('draft'),
      draftVersion: 1,
      resourceId: orgId,
      conversationTargetId: chiefId.value,
    );
  });

  @override
  ResourceSubmission<DraftedResource> prepareCreateWorker({
    required String organizationId,
    required String key,
    required String name,
    required String purpose,
    required String instructions,
  }) => _DemoResourceSubmission('create worker $key', () {
    final id = WorkerId(_newId('worker'));
    final parent = WorkerId(organizationId);
    _extraWorkers.add(
      WorkerEntry(
        id: id,
        name: name,
        role: purpose,
        organizationId: OrganizationId(organizationId),
        organizationPath: const ['Personal'],
        parentId: parent,
      ),
    );
    return DraftedResource(
      draftId: _newId('draft'),
      draftVersion: 1,
      resourceId: id.value,
      conversationTargetId: id.value,
    );
  });

  @override
  ResourceSubmission<DraftedResource> prepareCreateResponsibility({
    required String workerId,
    required String outcome,
    required List<String> triggers,
    required int minIntervalSeconds,
    required VerifierIdentity verifier,
    required DateTime rootDeadline,
    String currency = 'XXX',
  }) => _DemoResourceSubmission('create responsibility for $workerId', () {
    final id = RoutineId(_newId('routine'));
    _extraRoutines.add(
      RoutineEntry(
        id: id,
        version: 1,
        workerId: WorkerId(workerId),
        title: outcome,
        triggers: triggers,
        signals: const [],
        minIntervalSeconds: minIntervalSeconds,
        paused: false,
      ),
    );
    return DraftedResource(
      draftId: _newId('draft'),
      draftVersion: 1,
      resourceId: id.value,
    );
  });

  @override
  ResourceSubmission<PlanOutcome> preparePlan({
    required String draftId,
    required int expectedVersion,
  }) => _DemoResourceSubmission(
    'plan $draftId',
    () => PlanOutcome(
      planId: _newId('plan'),
      baseRevision: 1,
      candidateDigest: _newId('digest'),
      diagnostics: const [],
      pendingRequirements: const [],
    ),
  );

  @override
  ResourceSubmission<PlanOutcome> prepareApplyPlan(PlanOutcome plan) =>
      _DemoResourceSubmission('apply plan ${plan.planId}', () => plan);

  @override
  ResourceSubmission<ConversationOutcome> prepareOpenDirectConversation({
    required String humanPrincipalId,
    required String workerId,
    required String title,
  }) => _DemoResourceSubmission('open a conversation with $workerId', () {
    final id = ConversationId(_newId('conversation'));
    _extraConversations.add(
      ConversationEntry(
        id: id,
        title: title,
        kind: ConversationKind.direct,
        workerId: WorkerId(workerId),
        messages: const [],
        participantIds: [humanPrincipalId, workerId],
      ),
    );
    return ConversationOutcome(id: id.value, version: 1);
  });

  @override
  ResourceSubmission<ConversationOutcome> prepareCreateGroup({
    required String title,
    required List<String> participantIds,
  }) => _DemoResourceSubmission('create group $title', () {
    final id = ConversationId(_newId('conversation'));
    _extraConversations.add(
      ConversationEntry(
        id: id,
        title: title,
        kind: ConversationKind.group,
        messages: const [],
        participantIds: participantIds,
      ),
    );
    return ConversationOutcome(id: id.value, version: 1);
  });

  @override
  ResourceSubmission<ConversationOutcome> prepareAddParticipant({
    required ConversationEntry conversation,
    required int expectedVersion,
    required String newParticipantId,
  }) => _DemoResourceSubmission(
    'add a participant to ${conversation.id.value}',
    () {
      final updated = ConversationEntry(
        id: conversation.id,
        title: conversation.title,
        kind: conversation.kind,
        workerId: conversation.workerId,
        messages: conversation.messages,
        participantIds: {
          ...conversation.participantIds,
          newParticipantId,
        }.toList(),
        version: conversation.version + 1,
      );
      _extraConversations
        ..removeWhere((c) => c.id == conversation.id)
        ..add(updated);
      return ConversationOutcome(
        id: updated.id.value,
        version: updated.version,
      );
    },
  );

  @override
  ResourceSubmission<TaskOutcome> prepareCreateTask({
    required String ownerId,
    required String workerId,
    required String outcome,
    required List<String> requiredOutputs,
    required VerifierIdentity verifier,
    required DateTime rootDeadline,
    String currency = 'XXX',
  }) => _DemoResourceSubmission('create task for $workerId', () {
    final id = _newId('task');
    _extraTasks.add(
      TaskEntry(
        id: id,
        workerId: WorkerId(workerId),
        title: outcome,
        state: TaskEntryState.waiting,
        detail: 'Not started yet',
        version: 1,
        ownerId: ownerId,
        manualAcceptance: true,
      ),
    );
    return TaskOutcome(id: id, version: 1, state: 'draft');
  });

  @override
  ResourceSubmission<TaskOutcome> prepareStartTask({
    required String id,
    required int expectedVersion,
  }) => _DemoResourceSubmission('start task $id', () {
    final existing = _extraTasks.firstWhere((t) => t.id == id);
    final started = TaskEntry(
      id: existing.id,
      workerId: existing.workerId,
      title: existing.title,
      state: TaskEntryState.inProgress,
      detail: 'Running (demo)',
      version: existing.version + 1,
      ownerId: existing.ownerId,
      manualAcceptance: existing.manualAcceptance,
    );
    _extraTasks
      ..removeWhere((t) => t.id == id)
      ..add(started);
    return TaskOutcome(
      id: started.id,
      version: started.version,
      state: 'ready',
    );
  });

  @override
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
  }) => _DemoResourceSubmission('delegate $parentId to $childWorkerId', () {
    final id = _newId('task');
    _extraTasks.add(
      TaskEntry(
        id: id,
        workerId: WorkerId(childWorkerId),
        title: outcome,
        state: TaskEntryState.waiting,
        detail: 'Delegated (demo) · not started yet',
        version: 1,
        ownerId: ownerId,
        manualAcceptance: true,
      ),
    );
    return TaskOutcome(id: id, version: 1, state: 'draft');
  });

  @override
  ResourceSubmission<TaskOutcome> prepareAcceptTask({
    required String id,
    required int expectedVersion,
    required bool accept,
    String reason = '',
  }) => _DemoResourceSubmission('${accept ? 'accept' : 'reject'} task $id', () {
    final existing = _extraTasks.firstWhere((t) => t.id == id);
    final decided = TaskEntry(
      id: existing.id,
      workerId: existing.workerId,
      title: existing.title,
      state: accept ? TaskEntryState.completed : TaskEntryState.needsChanges,
      detail: accept ? 'Accepted (demo)' : 'Rejected (demo)',
      version: existing.version + 1,
      ownerId: existing.ownerId,
      manualAcceptance: existing.manualAcceptance,
    );
    _extraTasks
      ..removeWhere((t) => t.id == id)
      ..add(decided);
    return TaskOutcome(
      id: decided.id,
      version: decided.version,
      state: accept ? 'succeeded' : 'failed',
    );
  });

  @override
  Future<List<VerifierIdentity>> loadTrustedVerifiers() async {
    _requireOnline();
    return const [_verifier];
  }

  @override
  Future<List<TaskArtifactEntry>> loadTaskArtifacts(String taskId) async {
    _requireOnline();
    return const [];
  }

  @override
  Future<ReviewContentPart> readTaskArtifact(TaskArtifactEntry artifact) async {
    _requireOnline();
    return ReviewContentPart(
      label: artifact.mediaType,
      digest: artifact.digest,
      unavailableReason: 'The demo has no artifact bytes to show.',
    );
  }

  @override
  Future<void> submit(PendingSubmission submission) async {
    _requireOnline();
    await beforeSubmit?.call();
    final fault = nextFault;
    nextFault = DemoFault.none;
    _sends(submission, 1);
    switch (fault) {
      case DemoFault.none:
        _commit(submission);
      case DemoFault.unavailable:
        _sends(submission, -1);
        throw const SourceUnavailable('The demo refused the connection.');
      case DemoFault.unknownThenFound:
        _commit(submission);
        throw const AcknowledgmentUnknown('The demo dropped the answer.');
      case DemoFault.unknownThenNotReceived:
        throw const AcknowledgmentUnknown('The demo dropped the request.');
      case DemoFault.stale:
        _reviewVersion++;
        _dirty = true;
        _commit(submission);
    }
  }

  void _sends(PendingSubmission submission, int delta) {
    switch (submission) {
      case _DemoSubmission s:
        s.sends += delta;
      case _DemoResourceSubmission s:
        s.sends += delta;
      default:
        throw StateError('unknown demo submission ${submission.runtimeType}');
    }
  }

  bool _committedOf(PendingSubmission submission) => switch (submission) {
    _DemoSubmission s => s.committed,
    _DemoResourceSubmission s => s.committed,
    _ => throw StateError('unknown demo submission ${submission.runtimeType}'),
  };

  void _commit(PendingSubmission submission) {
    // Same identity: the original result stands.
    if (_committedOf(submission)) return;
    switch (submission) {
      case _DemoSubmission s:
        s.apply();
        s.committed = true;
      case _DemoResourceSubmission s:
        s.result = s._produce();
        s.committed = true;
      default:
        throw StateError('unknown demo submission ${submission.runtimeType}');
    }
    committed.add(submission.description);
    _dirty = true;
  }

  @override
  Future<Resolution> resolve(PendingSubmission submission) async {
    _requireOnline();
    return _committedOf(submission)
        ? const ResolvedAcknowledged()
        : const ResolvedNotReceived();
  }

  /// Changes the pending request underneath the person, as a worker revising
  /// its proposal would.
  void reviseReview() {
    _reviewVersion++;
    _dirty = true;
  }

  ReviewEntry _review() => ReviewEntry(
    id: pullRequestReview,
    version: _reviewVersion,
    actionDigest:
        '${_reviewVersion.toRadixString(16).padLeft(2, '0')}'
        '${'d3' * 31}',
    state: _reviewState,
    proposerId: quality,
    title: 'Open a pull request',
    consequence:
        'This opens one pull request in example/website. Publishing '
        'changes always asks you first.',
    action: 'Open one pull request from the branch below',
    accountIdentity: 'example-bot (sample connection)',
    destination: 'github.com/example/website · base main',
    costBound: '0.04 USD',
    expiresAt: _clock().add(const Duration(hours: 7)),
    content: [
      ReviewContentPart(
        label: 'Pull request title and description · revision $_reviewVersion',
        digest: 'a1' * 32,
        text:
            'Clarify the first-time setup steps\n\n'
            'Rewrites the getting-started section so the three setup steps '
            'are numbered and each one names the screen it happens on. No '
            'behavior changes. Fictional repository used for this demo.',
      ),
      ReviewContentPart(
        label: 'Changed files · 1 file, 14 lines',
        digest: 'b2' * 32,
        text:
            'docs/getting-started.md\n'
            '- Setup is easy, just follow along.\n'
            '+ 1. Open Settings and choose Connections.\n'
            '+ 2. Add the repository connection.\n'
            '+ 3. Return to the conversation and ask for a review.',
      ),
    ],
    parameters: const {
      'Head branch': 'docs/first-time-setup',
      'Base branch': 'main',
      'Draft': 'no',
    },
    evidence: const [
      'Standing rule: ask before opening or merging a pull request.',
      '10:02 · Branch prepared from main; one file changed.',
      '10:06 · 0.04 USD reserved. Authority and limits are rechecked before '
          'the request is sent.',
      'This review binds the exact title, description, branch and changed '
          'files shown. Any change requires a new decision.',
      'Approving does not change the standing rule. A worker cannot '
          'approve on your behalf.',
    ],
  );

  @override
  Future<WorkspaceSnapshot> loadSnapshot() async {
    _requireOnline();
    _dirty = false;
    final now = _clock();
    final morning = DateTime.utc(now.year, now.month, now.day, 9);

    ConversationEntry direct(
      WorkerId w,
      String title,
      String ask,
      String name,
      String reply,
    ) {
      final id = conversationOf(w);
      return ConversationEntry(
        id: id,
        title: title,
        kind: ConversationKind.direct,
        workerId: w,
        participantIds: [humanPrincipal, w.value],
        messages: [
          if (ask.isNotEmpty)
            ChatMessage(
              id: '${id.value}-1',
              fromUser: true,
              senderName: 'You',
              body: ask,
              at: morning,
            ),
          ChatMessage(
            id: '${id.value}-2',
            fromUser: false,
            senderName: name,
            body: reply,
            at: morning.add(const Duration(minutes: 2)),
          ),
          ...?_sent[id],
        ],
      );
    }

    WorkerEntry worker(
      WorkerId id,
      String name,
      String role,
      List<String> path,
      WorkerId? parent, {
      bool chief = false,
      String preview = '',
    }) => WorkerEntry(
      id: id,
      name: name,
      role: role,
      organizationId: OrganizationId('demo-org-${path.last.toLowerCase()}'),
      organizationPath: path,
      parentId: parent,
      conversationId: conversationOf(id),
      isOrganizationChief: chief,
      preview: preview,
    );

    final pending = _reviewState == ReviewRecordState.pending;
    return WorkspaceSnapshot(
      takenAt: now,
      workspaceName: 'Personal workspace · sample',
      workers: [
        worker(
          wren,
          'Wren',
          'Your chief',
          ['Personal'],
          null,
          chief: true,
          preview: pending
              ? 'One thing needs your attention.'
              : 'You’re all caught up.',
        ),
        worker(
          marketing,
          'Marketing chief',
          'Organization chief',
          ['Personal', 'Marketing'],
          wren,
          chief: true,
          preview: 'Partnerships are taking shape.',
        ),
        worker(
          research,
          'YouTube researcher',
          'Research worker',
          ['Personal', 'Marketing'],
          marketing,
          preview: 'Three partnerships worth a look.',
        ),
        worker(
          outreach,
          'Outreach',
          'Outreach worker',
          ['Personal', 'Marketing'],
          marketing,
          preview: 'Drafting notes for your review.',
        ),
        worker(
          engineering,
          'Engineering chief',
          'Organization chief',
          ['Personal', 'Engineering'],
          wren,
          chief: true,
          preview: 'The website review is underway.',
        ),
        worker(
          quality,
          'Website reviewer',
          'Quality chief',
          ['Personal', 'Engineering', 'Quality'],
          engineering,
          chief: true,
          preview: pending
              ? 'A pull request is ready for your decision.'
              : 'Decision recorded.',
        ),
        worker(
          ledger,
          'Ledger',
          'Operations chief',
          ['Personal', 'Operations'],
          wren,
          chief: true,
          preview: 'Reconciling this week’s expenses.',
        ),
        for (final w in _extraWorkers) _attachConversation(w),
      ],
      conversations: _cacheMessages([
        direct(
          wren,
          'Wren',
          'Morning, Wren. How are things looking?',
          'Wren',
          'A good morning. A little less on your plate.\n\n'
              'The partnership shortlist is ready, and Ledger is working '
              'through this week’s expenses. '
              '${pending ? 'Just one thing needs you.' : 'You’re all caught up on decisions.'}',
        ),
        direct(
          marketing,
          'Marketing chief',
          'How is the workshop coming along?',
          'Marketing chief',
          'The right people are coming together. YouTube researcher found '
              'three potential collaborators, and Outreach is drafting notes '
              'for you to read.',
        ),
        direct(
          research,
          'YouTube researcher',
          'Find a few partners for our September workshop.',
          'YouTube researcher',
          'Three promising places to start. I compared eight candidates '
              'against your audience, location and workshop goals.',
        ),
        direct(
          outreach,
          'Outreach',
          'Draft an introduction for the shortlist.',
          'Outreach',
          'A short draft is in Files. Sending messages is not something '
              'this workspace can do, so the draft is yours to use.',
        ),
        direct(
          engineering,
          'Engineering chief',
          '',
          'Engineering chief',
          'Making the first visit feel easy. The website review is '
              'underway; publishing changes needs a separate decision.',
        ),
        direct(
          quality,
          'Website reviewer',
          'Review the first-time website experience.',
          'Website reviewer',
          'The setup steps were hard to follow, so I prepared a small '
              'documentation fix. You can read the exact pull request before '
              'anything is opened.',
        ),
        direct(
          ledger,
          'Ledger',
          'Keep our expenses organized every Friday.',
          'Ledger',
          'One less thing for Friday. I’m matching receipts to this '
              'week’s transactions and will leave you a summary.',
        ),
        ConversationEntry(
          id: planningGroup,
          title: 'Workshop planning',
          kind: ConversationKind.group,
          participantIds: [humanPrincipal, marketing.value],
          messages: [
            ChatMessage(
              id: 'demo-group-1',
              fromUser: false,
              senderName: 'Marketing chief',
              body:
                  'A shared space for the September workshop. A group is a '
                  'conversation, not an organization or a permission.',
              at: morning,
            ),
            ...?_sent[planningGroup],
          ],
        ),
        ..._extraConversations,
      ]),
      reviews: [_review()],
      proposals: const [
        ProposalEntry(
          id: 'demo-proposal-newsletter',
          workerId: marketing,
          title: 'Proposed: Newsletter writer',
          summary:
              'A staged worker under Marketing. Not active: it has no '
              'conversation, access or schedule until the plan is applied.',
        ),
      ],
      tasks: [
        const TaskEntry(
          id: 'demo-task-reconcile',
          workerId: ledger,
          title: 'Weekly reconciliation',
          state: TaskEntryState.inProgress,
          detail: '18 of 24 receipts matched',
        ),
        const TaskEntry(
          id: 'demo-task-shortlist',
          workerId: research,
          title: 'Partnership shortlist',
          state: TaskEntryState.completed,
          detail: 'Acceptance checks passed · sample evidence',
        ),
        const TaskEntry(
          id: 'demo-task-website',
          workerId: quality,
          title: 'Review the first-time website experience',
          state: TaskEntryState.needsChanges,
          detail: 'Observed: 1 of 3 checks failed · setup steps unclear',
        ),
        ..._extraTasks,
      ],
      routines: [
        RoutineEntry(
          id: reconciliation,
          version: 1,
          workerId: ledger,
          title: 'Weekly reconciliation',
          triggers: const ['receipts.imported', 'week.closed'],
          signals: const ['unmatched receipt count'],
          minIntervalSeconds: 3600,
          paused: _routinePaused,
          lastCycleId: 'demo-cycle-41',
          schedule: const ScheduleLink(
            id: 'demo-schedule-reconciliation',
            timezone: 'America/Los_Angeles',
            expression: '0 9 * * FRI',
            misfire: 'coalesce',
            catchUpSeconds: 3600,
            paused: false,
          ),
        ),
        ..._extraRoutines,
      ],
      files: [
        FileEntry(
          id: 'demo-file-shortlist',
          workerId: research,
          title: 'Partnership shortlist',
          detail: 'Research brief · shared with Marketing and Wren',
          verified: true,
          digest: 'a' * 64,
          classification: 'internal',
          taskId: 'demo-task-shortlist',
          taskTitle: 'Partnership shortlist',
          checks: const ['presence on shortlist.md: expected to pass'],
        ),
        const FileEntry(
          id: 'demo-file-intro',
          workerId: outreach,
          title: 'Introduction draft',
          detail: 'Draft text · not sent anywhere',
          verified: false,
          digest: '',
          classification: 'internal',
        ),
      ],
      memory: [
        MemoryEntry(
          id: const ClaimId('demo-memory-tone'),
          workerId: marketing,
          bindingId: 'demo-memory-binding-marketing',
          brainId: 'demo-brain-marketing',
          claimVersion: 1,
          title: 'Keep introductions short',
          text:
              '“Use a friendly, direct tone. Keep the first note under '
              '100 words.”',
          provenance: const [
            'Scope · Personal › Marketing',
            'Source · your message, September 14',
          ],
          freshness: DateTime.utc(2026, 9, 16),
          active: !_memoryRetracted,
          confidence: 900000,
          canRetract: true,
        ),
      ],
      autonomy: const [
        AutonomyEntry(
          id: 'demo-autonomy-pr',
          workerId: quality,
          capability: 'Open a pull request',
          destinations: ['github.com/example/website'],
          state: 'restricted',
          explanation: 'A human decides every pull request for now.',
        ),
      ],
      access: [
        const AccessEntry(
          id: 'demo-access-read',
          workerId: quality,
          title: 'Can read the website repository',
          detail: 'Read-only, within the Engineering organization’s scope.',
          allowed: true,
        ),
        AccessEntry(
          id: 'demo-access-pr',
          workerId: quality,
          title: 'Asks before opening a pull request',
          detail: 'Opening or merging always needs your decision.',
          allowed: false,
          reviewId: pending ? pullRequestReview : null,
        ),
      ],
      principals: const [
        PrincipalEntry(
          id: 'demo-principal-owner',
          name: 'You',
          kind: 'human',
          revoked: false,
        ),
        PrincipalEntry(
          id: 'demo-principal-controller',
          name: 'controller',
          kind: 'service',
          revoked: false,
        ),
        PrincipalEntry(
          id: 'demo-principal-desktop',
          name: 'This desktop client',
          kind: 'client_agent',
          revoked: false,
        ),
      ],
      spending: const [
        SpendingEntry(
          workerId: quality,
          headline: '4.82 USD spent · 0.04 USD reserved',
          detail: 'No unknown costs in this sample.',
          fraction: 0.241,
          ceiling: '20.00 USD ceiling · 2 concurrent · 40 model steps',
          contextCapture: 'complete',
        ),
      ],
      recovery: const [
        RecoveryEntry(
          id: 'demo-run-recovery',
          workerId: quality,
          label: 'Run demo-run for Review the first-time website experience',
          obligations: ['Confirm the pull-request draft was never sent.'],
        ),
      ],
    );
  }
}
