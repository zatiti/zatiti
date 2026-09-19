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
  bool _dirty = false;
  final Map<ConversationId, List<ChatMessage>> _sent = {};
  int _messageIds = 0;

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
  Future<void> submit(PendingSubmission submission) async {
    final s = submission as _DemoSubmission;
    _requireOnline();
    await beforeSubmit?.call();
    final fault = nextFault;
    nextFault = DemoFault.none;
    s.sends++;
    switch (fault) {
      case DemoFault.none:
        _commit(s);
      case DemoFault.unavailable:
        s.sends--;
        throw const SourceUnavailable('The demo refused the connection.');
      case DemoFault.unknownThenFound:
        _commit(s);
        throw const AcknowledgmentUnknown('The demo dropped the answer.');
      case DemoFault.unknownThenNotReceived:
        throw const AcknowledgmentUnknown('The demo dropped the request.');
      case DemoFault.stale:
        _reviewVersion++;
        _dirty = true;
        _commit(s);
    }
  }

  void _commit(_DemoSubmission s) {
    if (s.committed) return; // Same identity: the original result stands.
    s.apply();
    s.committed = true;
    committed.add(s.description);
    _dirty = true;
  }

  @override
  Future<Resolution> resolve(PendingSubmission submission) async {
    _requireOnline();
    return (submission as _DemoSubmission).committed
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
      ],
      conversations: [
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
      ],
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
      tasks: const [
        TaskEntry(
          id: 'demo-task-reconcile',
          workerId: ledger,
          title: 'Weekly reconciliation',
          state: TaskEntryState.inProgress,
          detail: '18 of 24 receipts matched',
        ),
        TaskEntry(
          id: 'demo-task-shortlist',
          workerId: research,
          title: 'Partnership shortlist',
          state: TaskEntryState.completed,
          detail: 'Acceptance checks passed · sample evidence',
        ),
        TaskEntry(
          id: 'demo-task-website',
          workerId: quality,
          title: 'Review the first-time website experience',
          state: TaskEntryState.needsChanges,
          detail: 'Observed: 1 of 3 checks failed · setup steps unclear',
        ),
      ],
      routines: [
        RoutineEntry(
          id: reconciliation,
          version: 1,
          workerId: ledger,
          title: 'Weekly reconciliation',
          schedule: 'Every Friday · 9:00 AM · America/Los_Angeles',
          boundary: 'Reads receipts and prepares a report. Cannot move money.',
          paused: _routinePaused,
        ),
      ],
      files: const [
        FileEntry(
          id: 'demo-file-shortlist',
          workerId: research,
          title: 'Partnership shortlist',
          detail: 'Research brief · shared with Marketing and Wren',
          verified: true,
        ),
        FileEntry(
          id: 'demo-file-intro',
          workerId: outreach,
          title: 'Introduction draft',
          detail: 'Draft text · not sent anywhere',
          verified: false,
        ),
      ],
      memory: const [
        MemoryEntry(
          id: 'demo-memory-tone',
          workerId: marketing,
          title: 'Keep introductions short',
          text:
              '“Use a friendly, direct tone. Keep the first note under '
              '100 words.”',
          provenance: [
            'Scope · Personal › Marketing',
            'Source · your message, September 14',
            'Updated · September 16 · current',
          ],
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
          headline: '4.82 USD spent of 20.00 USD daily limit',
          detail: '0.04 USD reserved · no unknown costs in this sample',
          fraction: 0.241,
        ),
      ],
    );
  }
}
