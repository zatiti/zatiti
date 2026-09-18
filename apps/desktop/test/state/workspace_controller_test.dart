import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/state/demo_source.dart';
import 'package:zatiti_desktop/src/state/snapshot.dart';
import 'package:zatiti_desktop/src/state/view_state.dart';
import 'package:zatiti_desktop/src/state/workspace_controller.dart';
import 'package:zatiti_desktop/src/state/workspace_source.dart';

const _review = DemoWorkspaceSource.pullRequestReview;
const _quality = DemoWorkspaceSource.quality;
const _engineering = DemoWorkspaceSource.engineering;
const _wren = DemoWorkspaceSource.wren;

void main() {
  late DemoWorkspaceSource source;
  late WorkspaceController c;
  var now = DateTime.utc(2026, 9, 18, 12);

  setUp(() async {
    now = DateTime.utc(2026, 9, 18, 12);
    source = DemoWorkspaceSource(clock: () => now);
    c = WorkspaceController(source, clock: () => now);
    await c.start();
  });

  Future<void> decide(DecisionChoice choice) {
    final r = c.decision(_review)!.review;
    return c.decide(
      _review,
      choice,
      seenVersion: r.version,
      seenDigest: r.actionDigest,
    );
  }

  TreeNodeView row(WorkerId id) => c.tree.firstWhere((n) => n.worker.id == id);

  group('one snapshot feeds every surface', () {
    test('count, card, Work tab and tree agree', () {
      expect(c.connection, ConnectionPhase.online);
      expect(c.needsYouCount, 1);
      expect(c.needsYou.single.review.id, _review);
      // The card shows in the proposer's conversation and every ancestor's.
      for (final w in [_quality, _engineering, _wren]) {
        expect(c.decisionsFor(w).single.review.id, _review, reason: w.value);
      }
      expect(c.decisionsFor(DemoWorkspaceSource.ledger), isEmpty);
    });

    test('after a decision every surface changes together', () async {
      await decide(DecisionChoice.approve);
      expect(c.needsYouCount, 0);
      expect(c.decisionsFor(_wren).single.phase, ReviewPhase.approved);
      expect(row(_engineering).showsDecisionIndicator, isFalse);
    });

    test('a staged proposal never counts as needing you', () {
      expect(c.proposalsFor(DemoWorkspaceSource.marketing), hasLength(1));
      expect(c.needsYouCount, 1);
      expect(
        row(DemoWorkspaceSource.marketing).showsDecisionIndicator,
        isFalse,
      );
    });
  });

  group('organization conversation tree', () {
    test('rows keep stable organization order', () {
      c.setExpanded(DemoWorkspaceSource.marketing, expanded: true);
      c.setExpanded(_engineering, expanded: true);
      expect(c.tree.map((n) => n.worker.name), [
        'Wren',
        'Marketing chief',
        'YouTube researcher',
        'Outreach',
        'Engineering chief',
        'Website reviewer',
        'Ledger',
      ]);
      expect(row(_wren).pinned, isTrue);
      expect(row(_quality).depth, 2);
    });

    test('expanding or collapsing never changes the selection', () {
      expect(c.selectedWorker, _wren);
      c.toggleExpanded(_engineering);
      expect(c.isExpanded(_engineering), isTrue);
      expect(c.selectedWorker, _wren);
      c.toggleExpanded(_engineering);
      expect(c.selectedWorker, _wren);
    });

    test('a collapsed branch bubbles the decision indicator up', () {
      expect(c.isExpanded(_engineering), isFalse);
      final collapsed = row(_engineering);
      expect(collapsed.showsDecisionIndicator, isTrue);
      expect(collapsed.decisionsBelow, 1);
      expect(collapsed.semanticLabel, contains('decisions waiting below'));

      c.setExpanded(_engineering, expanded: true);
      expect(row(_engineering).showsDecisionIndicator, isFalse);
      expect(row(_quality).showsDecisionIndicator, isTrue);
      expect(row(_quality).semanticLabel, contains('needs your decision'));

      // Collapsing the root bubbles it all the way up.
      c.setExpanded(_wren, expanded: false);
      expect(row(_wren).showsDecisionIndicator, isTrue);
      expect(c.tree, hasLength(1));
    });

    test('selecting a hidden worker reveals it', () {
      c.selectWorker(_quality);
      expect(c.isExpanded(_engineering), isTrue);
      expect(row(_quality).selected, isTrue);
    });

    test('announces full ancestry', () {
      c.selectWorker(_quality);
      expect(
        row(_quality).semanticLabel,
        contains('Personal › Engineering › Quality'),
      );
    });

    test('the filter keeps descendants and ancestors', () {
      c.setFilter(_engineering);
      c.setExpanded(_engineering, expanded: true);
      expect(c.tree.map((n) => n.worker.name), [
        'Wren',
        'Engineering chief',
        'Website reviewer',
      ]);
      c.setFilter(null);
      expect(c.tree.length, greaterThan(3));
    });

    test('groups sit outside the tree', () {
      expect(c.groups.single.title, 'Workshop planning');
      expect(c.tree.any((n) => n.worker.name == 'Workshop planning'), isFalse);
      c.selectGroup(DemoWorkspaceSource.planningGroup);
      expect(c.selectedWorker, isNull);
      expect(c.selectedConversation!.kind, ConversationKind.group);
    });

    test('selection, expansion and drafts are independent', () {
      final wrenChat = DemoWorkspaceSource.conversationOf(_wren);
      c.setDraft(wrenChat, 'half a thought');
      c.selectWorker(DemoWorkspaceSource.ledger);
      c.toggleExpanded(DemoWorkspaceSource.marketing);
      c.selectWorker(_wren);
      expect(c.draftFor(wrenChat), 'half a thought');
      expect(c.isExpanded(DemoWorkspaceSource.marketing), isTrue);
    });
  });

  group('approval changes only after controller acknowledgment', () {
    test('submitting shows submitting, never approved', () async {
      final gate = Completer<void>();
      source.beforeSubmit = () => gate.future;
      final phases = <ReviewPhase>[];
      c.addListener(() => phases.add(c.phaseOf(_review)));

      final done = decide(DecisionChoice.approve);
      await Future<void>.delayed(Duration.zero);
      expect(c.phaseOf(_review), ReviewPhase.submitting);
      expect(c.needsYouCount, 1, reason: 'still unresolved while in flight');
      expect(source.committed, isEmpty);

      gate.complete();
      await done;
      expect(c.phaseOf(_review), ReviewPhase.approved);
      expect(
        phases.indexOf(ReviewPhase.approved),
        greaterThan(phases.indexOf(ReviewPhase.submitting)),
      );
      expect(phases.first, ReviewPhase.submitting);
    });

    test('duplicate activation is disabled while submitting', () async {
      final gate = Completer<void>();
      source.beforeSubmit = () => gate.future;
      final first = decide(DecisionChoice.approve);
      await Future<void>.delayed(Duration.zero);

      expect(c.decision(_review)!.canDecide, isFalse);
      await decide(DecisionChoice.approve);
      await decide(DecisionChoice.decline);

      gate.complete();
      await first;
      expect(source.committed, ['decide ${_review.value} approve']);
    });

    test('approved is not delivered', () async {
      await decide(DecisionChoice.approve);
      expect(c.phaseOf(_review), ReviewPhase.approved);
      expect(c.phaseOf(_review).label, contains('delivery not confirmed'));
    });

    test('decline records a decline', () async {
      await decide(DecisionChoice.decline);
      expect(c.phaseOf(_review), ReviewPhase.declined);
      expect(c.needsYouCount, 0);
    });
  });

  group('acknowledgment unknown', () {
    test('found: the lookup settles it without resending', () async {
      source.nextFault = DemoFault.unknownThenFound;
      final phases = <ReviewPhase>[];
      c.addListener(() => phases.add(c.phaseOf(_review)));
      await decide(DecisionChoice.approve);
      expect(phases, contains(ReviewPhase.acknowledgmentUnknown));
      expect(c.phaseOf(_review), ReviewPhase.approved);
      expect(source.committed, hasLength(1));
    });

    test(
      'not received: back to an explicit decision, nothing resent',
      () async {
        source.nextFault = DemoFault.unknownThenNotReceived;
        await decide(DecisionChoice.approve);
        expect(c.phaseOf(_review), ReviewPhase.awaitingDecision);
        expect(c.decision(_review)!.note, contains('not received'));
        expect(source.committed, isEmpty);

        // The person decides again; only now is anything sent.
        await decide(DecisionChoice.approve);
        expect(c.phaseOf(_review), ReviewPhase.approved);
        expect(source.committed, hasLength(1));
      },
    );

    test('while the lookup cannot answer, decisions stay locked', () async {
      final unknown = _AlwaysUnknownSource(source);
      final locked = WorkspaceController(unknown, clock: () => now);
      await locked.start();
      final r = locked.decision(_review)!.review;
      await locked.decide(
        _review,
        DecisionChoice.approve,
        seenVersion: r.version,
        seenDigest: r.actionDigest,
      );
      expect(locked.phaseOf(_review), ReviewPhase.acknowledgmentUnknown);
      expect(locked.decision(_review)!.canDecide, isFalse);
      expect(locked.needsYouCount, 1);

      await locked.decide(
        _review,
        DecisionChoice.approve,
        seenVersion: r.version,
        seenDigest: r.actionDigest,
      );
      expect(unknown.submits, 1, reason: 'no resend while unknown');

      await locked.checkDecision(_review);
      expect(unknown.resolves, 2);
      expect(unknown.submits, 1);
    });
  });

  group('stale and expired reviews', () {
    test('a stale refusal disables the decision until a new preview', () async {
      source.nextFault = DemoFault.stale;
      await decide(DecisionChoice.approve);
      expect(c.phaseOf(_review), ReviewPhase.stale);
      expect(c.decision(_review)!.canDecide, isFalse);
      expect(source.committed, isEmpty);

      await c.refreshReview(_review);
      final fresh = c.decision(_review)!;
      expect(fresh.phase, ReviewPhase.awaitingDecision);
      expect(fresh.review.version, 2);
      expect(source.committed, isEmpty, reason: 'nothing carried over');

      await decide(DecisionChoice.approve);
      expect(c.phaseOf(_review), ReviewPhase.approved);
    });

    test(
      'deciding on a version the person did not see is refused locally',
      () async {
        final seen = c.decision(_review)!.review;
        source.reviseReview();
        await c.poll();
        expect(c.decision(_review)!.review.version, 2);
        await c.decide(
          _review,
          DecisionChoice.approve,
          seenVersion: seen.version,
          seenDigest: seen.actionDigest,
        );
        expect(c.phaseOf(_review), ReviewPhase.stale);
        expect(source.committed, isEmpty);
      },
    );

    test('an expired review cannot be decided', () async {
      now = now.add(const Duration(hours: 8));
      expect(c.phaseOf(_review), ReviewPhase.expired);
      expect(c.decision(_review)!.canDecide, isFalse);
      await decide(DecisionChoice.approve);
      expect(source.committed, isEmpty);
      expect(c.needsYouCount, 0);
    });
  });

  group('offline', () {
    setUp(() async {
      source.offline = true;
      await c.poll();
    });

    test('labels the cached view and disables decisions', () async {
      expect(c.connection, ConnectionPhase.offline);
      expect(c.showsSavedView, isTrue);
      expect(c.snapshot.workers, isNotEmpty, reason: 'cached view stays');
      final view = c.decision(_review)!;
      expect(view.canDecide, isFalse);
      expect(view.blockedReason, contains('Offline'));

      await decide(DecisionChoice.approve);
      expect(c.phaseOf(_review), ReviewPhase.awaitingDecision);
      expect(source.committed, isEmpty);
    });

    test('keeps drafts visibly unsent', () async {
      final chat = DemoWorkspaceSource.conversationOf(_wren);
      c.setDraft(chat, 'Book the room for Friday');
      await c.sendDraft(chat);
      final message = c.outgoingFor(chat).single;
      expect(message.phase, OutgoingPhase.unsentDraft);
      expect(message.label, 'Unsent draft · not delivered');
      expect(source.committed, isEmpty);
    });

    test('reconnect does not auto-send drafts', () async {
      final chat = DemoWorkspaceSource.conversationOf(_wren);
      c.setDraft(chat, 'Book the room for Friday');
      await c.sendDraft(chat);

      source.offline = false;
      await c.reconnect();
      expect(c.connection, ConnectionPhase.online);
      await c.poll();
      expect(c.outgoingFor(chat).single.phase, OutgoingPhase.unsentDraft);
      expect(source.committed, isEmpty);

      // Only an explicit send delivers it.
      await c.sendUnsent(c.outgoingFor(chat).single.localId);
      expect(c.outgoingFor(chat), isEmpty);
      expect(source.committed, ['message to ${chat.value}']);
      expect(
        c.selectedConversation!.messages.map((m) => m.body),
        contains('Book the room for Friday'),
      );
    });

    test('a pause cannot be claimed offline', () async {
      await c.pauseRoutine(DemoWorkspaceSource.reconciliation);
      final view = c.routinesFor(DemoWorkspaceSource.ledger).single;
      expect(view.phase, RoutinePhase.active);
      expect(view.canPause, isFalse);
    });

    test('reconnecting is a distinct, labeled phase', () async {
      source.offline = false;
      final phases = <ConnectionPhase>[];
      c.addListener(() => phases.add(c.connection));
      await c.reconnect();
      expect(phases.first, ConnectionPhase.reconnecting);
      expect(phases.last, ConnectionPhase.online);
    });
  });

  group('connection loss during a submission', () {
    test('a decision that was not sent changes nothing', () async {
      source.nextFault = DemoFault.unavailable;
      await decide(DecisionChoice.approve);
      expect(c.connection, ConnectionPhase.offline);
      expect(c.phaseOf(_review), ReviewPhase.awaitingDecision);
      expect(c.decision(_review)!.note, contains('Not sent'));
      expect(source.committed, isEmpty);
    });

    test('a message that was not sent becomes an unsent draft', () async {
      final chat = DemoWorkspaceSource.conversationOf(_wren);
      source.nextFault = DemoFault.unavailable;
      c.setDraft(chat, 'hello');
      await c.sendDraft(chat);
      expect(c.outgoingFor(chat).single.phase, OutgoingPhase.unsentDraft);
    });
  });

  group('routines', () {
    test('paused shows only after acknowledgment', () async {
      final gate = Completer<void>();
      source.beforeSubmit = () => gate.future;
      final done = c.pauseRoutine(DemoWorkspaceSource.reconciliation);
      await Future<void>.delayed(Duration.zero);
      var view = c.routinesFor(DemoWorkspaceSource.ledger).single;
      expect(view.phase, RoutinePhase.submitting);
      expect(view.canPause, isFalse);

      gate.complete();
      await done;
      view = c.routinesFor(_wren).single;
      expect(view.phase, RoutinePhase.paused);
      expect(view.routine.paused, isTrue);
    });
  });

  group('prerequisites', () {
    test('notices are grouped by the surface they block', () async {
      final gaps = _WithPrerequisites(source);
      final g = WorkspaceController(gaps, clock: () => now);
      await g.start();
      expect(g.prerequisitesFor(DetailsTab.memory).single.title, 'No memory');
      expect(g.prerequisitesFor(DetailsTab.files), isEmpty);
      expect(g.prerequisitesFor(null).single.title, 'Add a credential');
    });

    test('a refusal for a missing prerequisite explains itself', () async {
      final refusing = _RefusingSource(source);
      final r = WorkspaceController(refusing, clock: () => now);
      await r.start();
      final review = r.decision(_review)!.review;
      await r.decide(
        _review,
        DecisionChoice.approve,
        seenVersion: review.version,
        seenDigest: review.actionDigest,
      );
      expect(r.phaseOf(_review), ReviewPhase.prerequisiteMissing);
      expect(r.decision(_review)!.note, contains('connection'));
      expect(r.needsYouCount, 1);
    });
  });
}

/// Delegates reads to the demo, and never learns whether a submission landed.
class _AlwaysUnknownSource extends _Delegating {
  _AlwaysUnknownSource(super.inner);
  int submits = 0;
  int resolves = 0;

  @override
  Future<void> submit(PendingSubmission submission) async {
    submits++;
    throw const AcknowledgmentUnknown('lost');
  }

  @override
  Future<Resolution> resolve(PendingSubmission submission) async {
    resolves++;
    throw const AcknowledgmentUnknown('still lost');
  }
}

class _RefusingSource extends _Delegating {
  _RefusingSource(super.inner);

  @override
  Future<void> submit(PendingSubmission submission) async =>
      throw const SourceRefusal(
        RefusalKind.prerequisiteMissing,
        'The repository connection is missing.',
      );
}

class _WithPrerequisites extends _Delegating {
  _WithPrerequisites(super.inner);

  @override
  Future<WorkspaceSnapshot> loadSnapshot() async {
    final s = await inner.loadSnapshot();
    return WorkspaceSnapshot(
      takenAt: s.takenAt,
      workers: s.workers,
      conversations: s.conversations,
      reviews: s.reviews,
      prerequisites: const [
        PrerequisiteNotice(title: 'Add a credential', message: 'None stored.'),
        PrerequisiteNotice(
          title: 'No memory',
          message: 'Not offered.',
          tab: DetailsTab.memory,
        ),
      ],
    );
  }
}

class _Delegating implements WorkspaceSource {
  _Delegating(this.inner);
  final WorkspaceSource inner;

  @override
  SourceKind get kind => inner.kind;
  @override
  String get label => inner.label;
  @override
  Future<WorkspaceSnapshot> loadSnapshot() => inner.loadSnapshot();
  @override
  Future<bool> hasChanges() => inner.hasChanges();
  @override
  Future<ReviewEntry> refreshReview(ReviewId id) => inner.refreshReview(id);
  @override
  PendingSubmission prepareDecision(ReviewEntry r, DecisionChoice c) =>
      inner.prepareDecision(r, c);
  @override
  PendingSubmission prepareMessage(ConversationId c, String body) =>
      inner.prepareMessage(c, body);
  @override
  PendingSubmission preparePause(RoutineEntry r) => inner.preparePause(r);
  @override
  Future<void> submit(PendingSubmission s) => inner.submit(s);
  @override
  Future<Resolution> resolve(PendingSubmission s) => inner.resolve(s);
}
