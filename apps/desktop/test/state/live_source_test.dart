// The live source and the view-state controller, end to end over a real Unix
// socket against the fake controller. Fixtures follow the catalog's `$defs`.

import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/app/local_store.dart';
import 'package:zatiti_desktop/src/state/live_source.dart';
import 'package:zatiti_desktop/src/state/snapshot.dart';
import 'package:zatiti_desktop/src/state/view_state.dart';
import 'package:zatiti_desktop/src/state/workspace_controller.dart';
import 'package:zatiti_desktop/src/state/workspace_source.dart';
import 'package:zatiti_desktop/src/transport/controller_client.dart';
import 'package:zatiti_desktop/src/transport/endpoint.dart';
import 'package:zatiti_desktop/src/transport/operations.dart';

import '../support/fake_controller.dart';

String _id(int n) => '00000000-0000-4000-8000-${n.toString().padLeft(12, '0')}';

final _rootOrg = _id(100);
final _engOrg = _id(101);
final _chief = _id(200);
final _engChief = _id(201);
final _reviewer = _id(202);
final _conversation = _id(300);
final _reviewId = _id(400);
final _tool = _id(500);
final _connection = _id(501);
final _artifact = _id(600);
final _digest = 'c' * 64;
final _contentDigest = 'e' * 64;
const _prBody = 'Clarify the first-time setup steps';

Map<String, Object?> _scope([String? worker]) => {
  'installation_id': testInstallationId,
  'worker_id': ?worker,
};

Map<String, Object?> _worker(String id, String org, String key, String name) =>
    {
      'id': id,
      'version': 1,
      'organization_id': org,
      'key': key,
      'name': name,
      'purpose': 'Reviews the website',
      'instructions': '',
      'skill_versions': <Object?>[],
      'bindings': <Object?>[],
      'profile': null,
      'limits': null,
    };

Map<String, Object?> _principal(
  String id,
  String kind,
  String name, {
  bool revoked = false,
}) => {
  'id': id,
  'version': 1,
  'kind': kind,
  'name': name,
  'scope': _scope(),
  'revoked': revoked,
};

Map<String, Object?> _reviewJson({int version = 3, String state = 'pending'}) =>
    {
      'id': _reviewId,
      'version': version,
      'scope': _scope(_reviewer),
      'action_digest': _digest,
      'preview': {
        'scope': _scope(_reviewer),
        'tool': {'id': _tool, 'version': 2},
        'connection': {'id': _connection, 'version': 1},
        'account_identity': 'example-bot',
        'destination': 'github.com/example/website',
        'content': [
          {'id': _artifact, 'digest': _contentDigest},
        ],
        'not_before': '2026-09-18T10:00:00Z',
        'expires_at': '2026-09-18T18:00:00Z',
        'preconditions': <String, Object?>{},
        'configuration_revision': 7,
        'parameters': {'base': 'main', 'draft': false},
        'cost_bound': {'currency': 'USD', 'micro_units': 40000},
      },
      'requirement': {
        'action_digest': _digest,
        'human_required': true,
        'eligible_principals': <Object?>[],
        'expires_at': '2026-09-18T18:00:00Z',
        'separate_proposer': true,
      },
      'proposer_id': _id(900),
      'state': state,
    };

class _World {
  /// Operations the fake controller claims to serve; defaults to all.
  List<OperationDescriptor> served = Operations.all;
  String reviewState = 'pending';
  int reviewVersion = 3;
  String artifactDigest = _contentDigest;
  Reply? decideReply;
  bool decideCommitted = false;

  /// Overrides the next `conversation.message.send` reply. The message is
  /// still committed (added to [sentMessages]) even when this drops the
  /// connection, mirroring `decideReply`/`review.decide` below: the fixture
  /// can prove "the controller has it but the client does not know that".
  Reply? sendReply;

  /// What `conversation.message.list` answers. Null means the fixture's own
  /// default: exactly what `conversation.message.send` has accepted so far,
  /// oldest first — the single authoritative history, never a separate
  /// locally remembered echo.
  Reply? messageListReply;

  /// Messages the fake has accepted through `conversation.message.send`,
  /// by the `message_id` the client minted.
  final List<Map<String, Object?>> sentMessages = [];

  /// Every submission_key committed by any mutation, so a generic
  /// `command.get` lookup can find any of them, not only a review decision.
  final Set<String> committedSubmissionKeys = {};

  /// The controller's own unread projection for [_conversation], as
  /// `conversation.list` reports it. Zero/null (the default) omits the
  /// field entirely, exactly like a controller that has not computed one.
  int callerUnreadCount = 0;
  String? callerLastReadMarker;

  Reply handle(RecordedRequest r) {
    final op = r.path.substring('/v1/operations/'.length);
    Reply items(List<Object?> list) => completed(jsonEncode({'items': list}));
    switch (op) {
      case 'capabilities.list':
        return items([
          for (final o in served)
            {
              'id': o.id,
              'version': o.version,
              'owner': 'fixture',
              'input_schema': <String, Object?>{},
              'output_schema': <String, Object?>{},
              'effect': 'local',
              'scope_requirements': ['installation_id'],
              'cli': <Object?>[],
              'mcp': '',
              'submission_key': o.isMutation,
              'expected_version': o.expectedVersion,
            },
        ]);
      case 'installation.status':
        return completed(
          jsonEncode({
            'resource': {
              'installation_id': testInstallationId,
              'generation': 1,
              'paused': false,
              'maintenance': false,
              'initialized': true,
              'requirements': <Object?>[],
              'version': 1,
            },
          }),
        );
      case 'organization.list':
        return items([
          {
            'id': _engOrg,
            'version': 1,
            'key': 'engineering',
            'name': 'Engineering',
            'chief_id': _engChief,
            'parent_id': _rootOrg,
          },
          {
            'id': _rootOrg,
            'version': 1,
            'key': 'personal',
            'name': 'Personal',
            'chief_id': _chief,
          },
        ]);
      case 'worker.list':
        return items([
          _worker(_reviewer, _engOrg, 'reviewer', 'Website reviewer'),
          _worker(_engChief, _engOrg, 'eng-chief', 'Engineering chief'),
          _worker(_chief, _rootOrg, 'wren', 'Wren'),
        ]);
      case 'conversation.list':
        return items([
          {
            'id': _conversation,
            'version': 1,
            'scope': _scope(_chief),
            'kind': 'direct',
            'participant_ids': [_chief],
            'title': 'Wren',
            'pinned': true,
            if (callerUnreadCount != 0)
              'caller_unread_count': callerUnreadCount,
            if (callerLastReadMarker != null)
              'caller_last_read_marker': callerLastReadMarker,
          },
        ]);
      case 'principal.list':
        return items([
          _principal(_id(900), 'human', 'You'),
          _principal(_id(901), 'service', 'controller'),
          _principal(
            _id(902),
            'client_agent',
            'Retired importer',
            revoked: true,
          ),
        ]);
      case 'grant.list':
        return items([
          {
            'id': _id(950),
            'version': 1,
            'principal_id': _id(902),
            'scope': _scope(_reviewer),
            'capabilities': ['pull_request.create'],
            'destinations': ['github.com/example/website'],
            'denied': false,
          },
          {
            'id': _id(951),
            'version': 1,
            'principal_id': _id(999),
            'scope': _scope(_reviewer),
            'capabilities': ['repository.read'],
            'destinations': <Object?>[],
            'denied': false,
          },
        ]);
      case 'conversation.message.send':
        // The controller keeps the client's message_id and returns it as the
        // message's own id; the live proof observes exactly this.
        final scripted = sendReply;
        sendReply = null;
        if (scripted is RawReply) return scripted;
        final message = {
          'id': r.input['message_id'],
          'version': 1,
          'sender_id': _id(900),
          'recipient_ids': [_chief],
          'scope': _scope(),
          'task_ids': <Object?>[],
          'body': r.input['body'],
          'attachments': <Object?>[],
          'state': 'admitted',
          'created_at': '2026-09-18T12:00:00Z',
          'conversation_id': r.input['conversation_id'],
        };
        sentMessages.add(message);
        if (r.submissionKey != null) {
          committedSubmissionKeys.add(r.submissionKey!);
        }
        if (scripted is DropReply) return scripted;
        return completed(jsonEncode({'resource': message}));
      case 'conversation.message.list':
        return messageListReply ?? items(sentMessages);
      case 'review.list':
        return items([_reviewJson(version: reviewVersion, state: reviewState)]);
      case 'review.get':
        return completed(
          jsonEncode({
            'resource': _reviewJson(version: reviewVersion, state: reviewState),
          }),
        );
      case 'tool.get':
        return completed(
          jsonEncode({
            'resource': {
              'id': _tool,
              'version': 2,
              'name': 'Open a pull request',
            },
          }),
        );
      case 'artifact.read':
        return completed(
          jsonEncode({
            'bytes_base64': base64.encode(utf8.encode(_prBody)),
            'digest': artifactDigest,
            'offset': 0,
            'total_size': _prBody.length,
          }),
        );
      case 'usage.get':
        return completed(
          jsonEncode({
            'resource': {
              'currency': 'USD',
              'spent': 4820000,
              'reserved': 40000,
              'estimated': 0,
              'unknown': 0,
              'advisory': false,
            },
          }),
        );
      case 'review.decide':
        final scripted = decideReply;
        decideReply = null;
        if (scripted is RawReply) return scripted;
        decideCommitted = true;
        reviewState = 'approved';
        if (scripted is DropReply) return scripted;
        return completed(
          jsonEncode({
            'resource': {
              'id': _id(700),
              'review_id': _reviewId,
              'review_version': reviewVersion,
              'action_digest': _digest,
              'reviewer_id': _id(900),
              'decision': 'approve',
              'at': '2026-09-18T12:00:00Z',
              'reason': '',
            },
          }),
        );
      case 'command.get':
        final lookupKey = r.input['submission_key'] as String?;
        final committed =
            decideCommitted ||
            (lookupKey != null && committedSubmissionKeys.contains(lookupKey));
        if (!committed) {
          return fault(404, 'not_found', 'command not found');
        }
        return completed(
          jsonEncode({
            'resource': {
              'id': _id(800),
              'principal_id': _id(900),
              'operation': r.input['operation'],
              'operation_version': r.input['operation_version'],
              'submission_key': lookupKey,
              'request_digest': 'f' * 64,
              'status': 'completed',
              'data': <String, Object?>{},
              'result': jsonDecode(completedEnvelope('{}')),
            },
          }),
        );
      default:
        return items([]);
    }
  }
}

void main() {
  late FakeController fake;
  late _World world;
  late WorkspaceController c;
  final now = DateTime.utc(2026, 9, 18, 12);

  setUp(() async {
    fake = await FakeController.start();
    world = _World();
    fake.script = world.handle;
    final client = ControllerClient(
      endpoint: LocalSocketEndpoint(fake.socketPath),
      installationId: testInstallationId,
      credentials: () async => 'Bearer test-fixture-credential',
      timeout: const Duration(seconds: 5),
    );
    c = WorkspaceController(
      LiveWorkspaceSource(client, clock: () => now),
      clock: () => now,
    );
    await c.start();
  });

  tearDown(() => fake.stop());

  Future<void> approve() {
    final r = c.decision(ReviewId(_reviewId))!.review;
    return c.decide(
      r.id,
      DecisionChoice.approve,
      seenVersion: r.version,
      seenDigest: r.actionDigest,
    );
  }

  test('builds the organization tree from controller identities', () {
    expect(c.connection, ConnectionPhase.online);
    c.setExpanded(WorkerId(_engChief), expanded: true);
    expect(c.tree.map((n) => '${n.depth}:${n.worker.name}'), [
      '0:Wren',
      '1:Engineering chief',
      '2:Website reviewer',
    ]);
    final reviewer = c.snapshot.worker(WorkerId(_reviewer))!;
    expect(reviewer.ancestry, 'Personal › Engineering');
    expect(
      c.snapshot.worker(WorkerId(_chief))!.conversationId!.value,
      _conversation,
    );
  });

  test('shows the exact review content read from the controller', () {
    final view = c.needsYou.single;
    expect(view.review.title, 'Open a pull request');
    expect(view.review.content.single.text, _prBody);
    expect(view.review.costBound, '0.04 USD');
    expect(view.review.parameters, {'base': 'main', 'draft': 'false'});
    expect(view.proposerName, 'Website reviewer');
    expect(view.canDecide, isTrue);
    expect(view.review.evidence.join(' '), contains(_digest));
  });

  test('content that does not match its digest cannot be approved', () async {
    world.artifactDigest = '0' * 64;
    await c.reconnect();
    final view = c.needsYou.single;
    expect(view.review.contentShowable, isFalse);
    expect(view.canDecide, isFalse);
    await approve();
    expect(fake.requestsFor('review.decide'), isEmpty);
  });

  test('a decision binds the version and digest the person saw', () async {
    await approve();
    final sent = fake.requestsFor('review.decide').single;
    expect(sent.input['expected_version'], 3);
    expect(sent.input['action_digest'], _digest);
    expect(sent.input['decision'], 'approve');
    expect(sent.submissionKey, isNotNull);
    expect(c.phaseOf(ReviewId(_reviewId)), ReviewPhase.approved);
    expect(c.needsYouCount, 0);
  });

  test('an unknown acknowledgment is looked up, never resent', () async {
    world.decideReply = const DropReply();
    await approve();
    expect(fake.requestsFor('review.decide'), hasLength(1));
    expect(fake.requestsFor('command.get'), hasLength(1));
    expect(c.phaseOf(ReviewId(_reviewId)), ReviewPhase.approved);
  });

  test('stale_version disables the decision until a fresh preview', () async {
    world.decideReply = fault(409, 'stale_version', 'review moved on');
    world.reviewVersion = 4;
    await approve();
    final id = ReviewId(_reviewId);
    expect(c.phaseOf(id), ReviewPhase.stale);
    expect(c.decision(id)!.canDecide, isFalse);

    await c.refreshReview(id);
    expect(c.decision(id)!.review.version, 4);
    expect(c.phaseOf(id), ReviewPhase.awaitingDecision);
    expect(fake.requestsFor('review.decide'), hasLength(1));
  });

  test('a controller that goes away leaves a labeled saved view', () async {
    await fake.goDown();
    await approve();
    expect(c.connection, ConnectionPhase.offline);
    expect(c.showsSavedView, isTrue);
    expect(c.snapshot.workers, hasLength(3));
    expect(c.phaseOf(ReviewId(_reviewId)), ReviewPhase.awaitingDecision);

    await fake.comeBack();
    await c.reconnect();
    expect(c.connection, ConnectionPhase.online);
    expect(fake.requestsFor('review.decide'), isEmpty);
  });

  test('catalog gaps surface as designed notices, not invented calls', () {
    expect(
      c.prerequisitesFor(DetailsTab.memory).single.title,
      'Memory cannot be listed yet',
    );
    // conversation.message.list is a real operation this client now calls;
    // the selected conversation's history is read for real, not excused by
    // a "no operation exists" notice.
    expect(c.selectedConversation!.historyNotice, isNull);
    expect(fake.requestsFor('conversation.message.list'), isNotEmpty);
    final called = fake.requests.map((r) => r.path.split('/').last).toSet();
    expect(called.any((op) => op.startsWith('memory.')), isFalse);
  });

  test(
    'confirms the catalog at connect and names what is unsupported',
    () async {
      expect(fake.requestsFor('capabilities.list'), isNotEmpty);
      world.served = [
        for (final o in Operations.all)
          if (o.id != 'review.decide') o,
      ];
      await c.reconnect();
      expect(c.connection, ConnectionPhase.unsupported);
      expect(c.connectionMessage, contains('review.decide is not offered'));
      expect(c.showsSavedView, isTrue);
      expect(c.decision(ReviewId(_reviewId))!.canDecide, isFalse);
      await approve();
      expect(fake.requestsFor('review.decide'), isEmpty);
    },
  );

  test(
    'a controller too old for conversation.message.list shows a named '
    'unsupported state, never a silent failure or a fabricated history',
    () async {
      world.served = [
        for (final o in Operations.all)
          if (o.id != 'conversation.message.list') o,
      ];
      await c.reconnect();
      expect(c.connection, ConnectionPhase.unsupported);
      expect(
        c.connectionMessage,
        contains('conversation.message.list is not offered'),
      );
      expect(c.showsSavedView, isTrue);
      // The exact failure mode this guards against: an older controller's
      // absent operation must never be read as "no messages yet".
      expect(c.connectionMessage, isNot(contains('no messages')));
    },
  );

  test('the snapshot carries the controller’s identities', () {
    expect(fake.requestsFor('principal.list'), isNotEmpty);
    final principals = c.snapshot.principals;
    expect(
      principals.map((p) => p.name),
      ['Retired importer', 'You', 'controller'],
      reason: 'identities are listed in stable name order',
    );
    expect(principals.map((p) => p.kindLabel), [
      'Client application',
      'Person',
      'Controller service',
    ]);
    expect(principals.singleWhere((p) => p.revoked).name, 'Retired importer');
  });

  test('an access card names the identity that holds the grant', () {
    final access = c.accessFor(WorkerId(_reviewer));
    expect(
      access.firstWhere((a) => a.title == 'pull_request.create').detail,
      'Held by Retired importer. Destinations: github.com/example/website',
    );
    expect(
      access.firstWhere((a) => a.title == 'repository.read').detail,
      'Held by an identity that is not listed. No external destinations.',
      reason: 'an unlisted principal is said to be unlisted, never invented',
    );
  });

  group('message history', () {
    /// A plain source on the same fake controller, for tests that read
    /// message history directly rather than through the controller.
    LiveWorkspaceSource source() => LiveWorkspaceSource(
      ControllerClient(
        endpoint: LocalSocketEndpoint(fake.socketPath),
        installationId: testInstallationId,
        credentials: () async => 'Bearer test-fixture-credential',
        timeout: const Duration(seconds: 5),
      ),
      clock: () => now,
    );

    test(
      'a refused message list costs that conversation, not the workspace',
      () async {
        world.messageListReply = fault(
          403,
          'permission_denied',
          'this history is not disclosed to you',
        );
        // The controller-level read (through WorkspaceController, as the UI
        // actually calls it) records a per-conversation notice; everything
        // else on screen — the tree, the pending review — is unaffected.
        await c.reconnect();
        expect(c.snapshot.workers, isNotEmpty);
        expect(c.needsYou, isNotEmpty);
        expect(
          c.selectedConversation!.historyNotice,
          contains('not disclosed to you'),
        );
      },
    );

    test('send while online then reload: the real reply is not fabricated, '
        'and the sent message is never duplicated', () async {
      final src = source();
      await src.loadSnapshot();

      final pending = src.prepareMessage(
        ConversationId(_conversation),
        'Create a marketing chief.',
      );
      await src.submit(pending);

      // conversation.message.list is the single authoritative source: the
      // fixture already reflects the accepted send, no separate local
      // echo is merged in.
      final messages = await src.loadMessages(ConversationId(_conversation));
      expect(messages.map((m) => m.body), ['Create a marketing chief.']);
      expect(messages.single.fromUser, isTrue);
      expect(fake.requestsFor('conversation.message.send'), hasLength(1));

      // Reading it again — as a reopened app would — returns the exact
      // same one message, not a second copy.
      final again = await src.loadMessages(ConversationId(_conversation));
      expect(again, hasLength(1));
      expect(fake.requestsFor('conversation.message.send'), hasLength(1));
    });

    test('a dropped send acknowledgment is resolved by command.get, never '
        'resent, and creates no duplicate turn', () async {
      final src = source();
      await src.loadSnapshot();
      final pending = src.prepareMessage(
        ConversationId(_conversation),
        "Reconcile this week's expenses.",
      );

      // The fake controller commits the message but drops the connection
      // before answering: this client cannot tell success from loss.
      world.sendReply = const DropReply();
      await expectLater(
        src.submit(pending),
        throwsA(isA<AcknowledgmentUnknown>()),
      );
      expect(
        fake.requestsFor('conversation.message.send'),
        hasLength(1),
        reason: 'exactly one physical send, whatever the client learned',
      );

      // Resolving looks the original submission up by command.get's own
      // record of it — it never mints a second submission_key and never
      // calls conversation.message.send a second time.
      final resolution = await src.resolve(pending);
      expect(resolution, isA<ResolvedAcknowledged>());
      expect(fake.requestsFor('conversation.message.send'), hasLength(1));
      expect(fake.requestsFor('command.get'), isNotEmpty);

      // The single committed message is the whole history: no duplicate
      // turn was created by the drop, the retry-avoidance, or the resolve.
      final messages = await src.loadMessages(ConversationId(_conversation));
      expect(messages.map((m) => m.body), ["Reconcile this week's expenses."]);
    });

    test(
      'turnStatusFor reports waiting for a reply only from real delivery '
      'and arrival timestamps, and clears once the real reply lands',
      () async {
        // _chief is the root, so its subtree includes every worker; resolve
        // the fixture's one pending review first so it does not take
        // priority over the "waiting for a reply" status this test proves.
        await approve();
        final chat = ConversationId(_conversation);
        expect(
          c.turnStatusFor(chat, worker: WorkerId(_chief)),
          isNot(TurnStatus.waitingForReply),
          reason: 'nothing has been sent yet',
        );

        c.setDraft(chat, 'Keep our expenses organized every Friday.');
        await c.sendDraft(chat);
        expect(
          c.turnStatusFor(chat, worker: WorkerId(_chief)),
          TurnStatus.waitingForReply,
          reason: 'delivered, and the fixture has not added a reply yet',
        );

        // The real reply lands; the status line for a worker turn in
        // progress clears itself, never lingering past what happened.
        world.sentMessages.add({
          'id': _id(951),
          'version': 1,
          'sender_id': _chief,
          'recipient_ids': [_id(900)],
          'scope': _scope(),
          'task_ids': <Object?>[],
          'body': 'Done for this week.',
          'attachments': <Object?>[],
          'state': 'admitted',
          'created_at': '2026-09-18T13:00:00Z',
          'conversation_id': _conversation,
        });
        // reconnect always refreshes (poll only refreshes when event.list
        // reports a change, and this fixture's event.list is always empty).
        await c.reconnect();
        expect(
          c.turnStatusFor(chat, worker: WorkerId(_chief)),
          isNot(TurnStatus.waitingForReply),
        );
      },
    );
  });

  group('durability across restart', () {
    /// Builds a controller/source pair against the shared [fake]/[world],
    /// resuming from whatever [localStore] already holds — exactly what a
    /// fresh launch does after close/reopen, given the same installation.
    Future<WorkspaceController> open(LocalStore localStore) async {
      final initial = await localStore.read(testInstallationId);
      final client = ControllerClient(
        endpoint: LocalSocketEndpoint(fake.socketPath),
        installationId: testInstallationId,
        credentials: () async => 'Bearer test-fixture-credential',
        timeout: const Duration(seconds: 5),
      );
      final controller = WorkspaceController(
        LiveWorkspaceSource(
          client,
          clock: () => now,
          initialEventCursor: initial.eventCursor,
          initialLastSequence: initial.lastSequence,
          localStore: localStore,
        ),
        clock: () => now,
        localStore: localStore,
        installationId: testInstallationId,
        initialLocal: initial,
      );
      await controller.start();
      return controller;
    }

    test('send to chief, close/reopen app: both messages and the real reply '
        'remain, with correct read state', () async {
      final localStore = MemoryLocalStore();

      // First launch: send to the chief.
      final first = await open(localStore);
      expect(first.selectedConversation!.id.value, _conversation);
      final chat = ConversationId(_conversation);
      first.setDraft(chat, 'Create a marketing chief.');
      await first.sendDraft(chat);
      expect(
        first.outgoingFor(chat),
        isEmpty,
        reason: 'delivered, not stuck as unsent',
      );
      expect(
        first.selectedConversation!.messages.map((m) => m.body),
        contains('Create a marketing chief.'),
      );

      // The worker's real reply arrives, and the controller now reports
      // an unread count for it — both are facts this fixture asserts as
      // controller state, never rendered from local invention.
      world.sentMessages.add({
        'id': _id(950),
        'version': 1,
        'sender_id': _chief,
        'recipient_ids': [_id(900)],
        'scope': _scope(),
        'task_ids': <Object?>[],
        'body': 'Done. Marketing chief created.',
        'attachments': <Object?>[],
        'state': 'admitted',
        'created_at': '2026-09-18T12:05:00Z',
        'conversation_id': _conversation,
      });
      world.callerUnreadCount = 1;
      world.callerLastReadMarker = '2026-09-18T12:00:00Z';

      // Close: a real app disposes its controller; nothing further reads
      // or writes through `first` from here on.
      first.dispose();

      // Reopen: a fresh controller and source, same installation, same
      // on-disk local store (here: the same store instance standing in
      // for it) and the same controller behind the same fake socket.
      final second = await open(localStore);
      final conversation = second.selectedConversation!;
      expect(
        conversation.id.value,
        _conversation,
        reason: 'the chief’s conversation opens again, not a blank one',
      );
      expect(conversation.messages.map((m) => m.body), [
        'Create a marketing chief.',
        'Done. Marketing chief created.',
      ]);
      expect(
        conversation.messages.first.fromUser,
        isTrue,
        reason: 'the message this window sent is still attributed to you',
      );
      expect(
        conversation.messages.last.fromUser,
        isFalse,
        reason: 'the real reply is shown as the worker’s, never as yours',
      );
      expect(conversation.unreadCount, 1);
      expect(
        conversation.lastReadMarker,
        DateTime.parse('2026-09-18T12:00:00Z'),
      );
    });

    test('account switch clears the cache: no other account’s identities or '
        'selection carry over', () async {
      final localStore = MemoryLocalStore();
      final first = await open(localStore);
      first.selectWorker(WorkerId(_engChief));
      await Future<void>.delayed(Duration.zero); // let selection persist
      final stored = await localStore.read(testInstallationId);
      expect(stored.selectedWorkerId, _engChief);
      expect(stored.workers, isNotEmpty);

      // A credential change is the only signal this client has for a
      // possible account switch (no `identity.current` to confirm either
      // way), so it always clears rather than risk showing the wrong
      // account's cache.
      await first.resetForCredentialChange();
      expect(await localStore.read(testInstallationId), LocalState.empty);
      expect(first.snapshot, WorkspaceSnapshot.empty);
      expect(first.selectedWorker, isNull);
    });
  });

  test('a fixture with a field the client does not know is refused', () async {
    fake.script = (r) => r.path.endsWith('installation.status')
        ? completed(
            '{"resource":{"installation_id":"$testInstallationId",'
            '"generation":1,"paused":false,"maintenance":false,'
            '"initialized":true,"requirements":[],"version":1,'
            '"surprise":true}}',
          )
        : world.handle(r);
    await c.reconnect();
    expect(c.connection, ConnectionPhase.offline);
    expect(c.connectionMessage, contains('surprise'));
  });
}
