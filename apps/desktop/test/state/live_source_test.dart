// The live source and the view-state controller, end to end over a real Unix
// socket against the fake controller. Fixtures follow the catalog's `$defs`.

import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/state/live_source.dart';
import 'package:zatiti_desktop/src/state/snapshot.dart';
import 'package:zatiti_desktop/src/state/view_state.dart';
import 'package:zatiti_desktop/src/state/workspace_controller.dart';
import 'package:zatiti_desktop/src/state/workspace_source.dart';
import 'package:zatiti_desktop/src/transport/controller_client.dart';
import 'package:zatiti_desktop/src/transport/endpoint.dart';

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
  String reviewState = 'pending';
  int reviewVersion = 3;
  String artifactDigest = _contentDigest;
  Reply? decideReply;
  bool decideCommitted = false;

  Reply handle(RecordedRequest r) {
    final op = r.path.substring('/v1/operations/'.length);
    Reply items(List<Object?> list) => completed(jsonEncode({'items': list}));
    switch (op) {
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
          },
        ]);
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
        if (!decideCommitted) {
          return fault(404, 'not_found', 'command not found');
        }
        return completed(
          jsonEncode({
            'resource': {
              'id': _id(800),
              'principal_id': _id(900),
              'operation': 'review.decide',
              'operation_version': 1,
              'submission_key': r.input['submission_key'],
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
    expect(
      c.selectedConversation!.historyNotice,
      contains('no operation that reads a conversation’s history'),
    );
    final called = fake.requests.map((r) => r.path.split('/').last).toSet();
    expect(called.any((op) => op.startsWith('memory.')), isFalse);
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
