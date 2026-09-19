// The end-to-end proof: this client's own transport against a real
// controller process over its real private Unix socket.
//
// Run it with tool/live-proof.sh. It is not part of `flutter test`, which
// proves local properties against a controlled fake; the specification puts
// real-controller proving outside the unit suite. Nothing here is mocked: if
// the controller cannot be started or reached, every test in the file fails
// with the controller's own log attached.

@Timeout(Duration(minutes: 10))
library;

import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/api/controller_api.dart';
import 'package:zatiti_desktop/src/api/models.dart' as wire;
import 'package:zatiti_desktop/src/state/live_source.dart';
import 'package:zatiti_desktop/src/state/snapshot.dart';
import 'package:zatiti_desktop/src/transport/controller_client.dart';
import 'package:zatiti_desktop/src/transport/endpoint.dart';
import 'package:zatiti_desktop/src/transport/envelope.dart';
import 'package:zatiti_desktop/src/transport/errors.dart';
import 'package:zatiti_desktop/src/transport/operations.dart';
import 'package:zatiti_desktop/src/transport/submission.dart';

import 'support/live_controller.dart';

/// Identity operations this proof drives directly. The application does not
/// call them, so they are not in [Operations.all]: adding them there would
/// make the app refuse a controller that serves everything it actually uses.
const principalList = OperationDescriptor.query('principal.list');
const principalCreate = OperationDescriptor.mutation('principal.create');

void main() {
  late LiveController controller;
  late ControllerClient client;
  late ControllerApi api;

  setUpAll(() async {
    controller = await LiveController.start();
    client = ControllerClient(
      endpoint: LocalSocketEndpoint(controller.socketPath),
      installationId: controller.installationId,
      credentials: () async => controller.authorization,
    );
    api = ControllerApi(client);
  });

  tearDownAll(() async => controller.stop());

  Map<String, Object?> agentDefinition(String name) => {
    'scope': client.scope(),
    'definition': {
      'kind': 'client_agent',
      'name': name,
      'scope': client.scope(),
      'revoked': false,
    },
  };

  test('the controller serves every operation this client calls', () async {
    final served = {for (final c in await api.capabilities()) c.id: c};
    final problems = <String>[];
    for (final op in Operations.all) {
      final c = served[op.id];
      if (c == null) {
        problems.add('${op.id} is not offered');
      } else if (c.version != op.version) {
        problems.add('${op.id} is version ${c.version}, not ${op.version}');
      } else if (c.submissionKey != op.isMutation) {
        problems.add(
          '${op.id} submission_key is ${c.submissionKey}, not ${op.isMutation}',
        );
      } else if (c.expectedVersion != op.expectedVersion) {
        problems.add(
          '${op.id} expected_version is ${c.expectedVersion}, not '
          '${op.expectedVersion}',
        );
      }
    }
    expect(problems, isEmpty, reason: problems.join('; '));
  });

  test('principal.list returns the owner and the controller service', () async {
    final principals = await api.listAll(
      principalList,
      wire.Principal.fromJson,
    );
    expect(
      principals.map((p) => '${p.kind}:${p.name}'),
      containsAll(<String>[
        'human:${controller.ownerName}',
        'service:controller',
      ]),
      reason: 'principals were ${principals.map((p) => p.name).toList()}',
    );
    for (final p in principals) {
      expect(p.revoked, isFalse);
      expect(p.version, greaterThanOrEqualTo(1));
      expect(p.scope.installationId, controller.installationId);
    }
  });

  test(
    'a keyed create completes and the identical resend replays it',
    () async {
      final input = agentDefinition('replay-proof');
      final first = client.prepare(principalCreate, input);
      final created = await client.submit(first);

      expect(created.status, ResultStatus.completed);
      expect(created.commandId, isNotEmpty);
      expect(created.error, isNull);
      final resource = wire.Principal.fromJson(
        created.requireData('principal.create')['resource'],
      );
      expect(resource.name, 'replay-proof');
      expect(resource.kind, 'client_agent');
      expect(resource.version, 1);

      // A client that lost the answer re-sends the original bytes under the
      // original key on a new connection. The controller must return the
      // original command, not a second principal.
      final resend = Submission(
        operation: principalCreate.id,
        operationVersion: principalCreate.version,
        key: first.key,
        input: input,
      );
      final replayed = await client.submit(resend);
      expect(replayed.commandId, created.commandId);
      expect(replayed.status, created.status);
      expect(
        wire.Principal.fromJson(
          replayed.requireData('principal.create')['resource'],
        ).id,
        resource.id,
      );

      // And exactly one principal exists under that name.
      final principals = await api.listAll(
        principalList,
        wire.Principal.fromJson,
      );
      expect(principals.where((p) => p.name == 'replay-proof'), hasLength(1));
    },
  );

  test('the same key with a changed input is refused', () async {
    final first = client.prepare(principalCreate, agentDefinition('bound-key'));
    await client.submit(first);

    final changed = Submission(
      operation: principalCreate.id,
      operationVersion: principalCreate.version,
      key: first.key,
      input: agentDefinition('bound-key-but-different'),
    );
    await expectLater(
      client.submit(changed),
      throwsA(
        isA<OperationFailedException>().having(
          (e) => e.fault.code,
          'fault code',
          FaultCode.submissionConflict,
        ),
      ),
    );
    final principals = await api.listAll(
      principalList,
      wire.Principal.fromJson,
    );
    expect(
      principals.where((p) => p.name == 'bound-key-but-different'),
      isEmpty,
      reason: 'a refused submission must not create anything',
    );
  });

  test('an unknown acknowledgment resolves through command.get', () async {
    final input = agentDefinition('lost-acknowledgment');
    final sent = client.prepare(principalCreate, input);
    final original = await client.submit(sent);

    // The same client after losing the answer: it holds the key and the
    // bytes, and knows only that the acknowledgment never arrived.
    final unknown = Submission(
      operation: principalCreate.id,
      operationVersion: principalCreate.version,
      key: sent.key,
      input: input,
    )..state = SubmissionState.acknowledgmentUnknown;

    final disposition = await client.resolve(unknown);
    expect(disposition, isA<DispositionFound>());
    final found = (disposition as DispositionFound).original;
    expect(found.commandId, original.commandId);
    expect(found.status, ResultStatus.completed);
    expect(
      wire.Principal.fromJson(
        found.requireData('principal.create')['resource'],
      ).id,
      wire.Principal.fromJson(
        original.requireData('principal.create')['resource'],
      ).id,
    );
    expect(unknown.state, SubmissionState.settled);
  });

  test('a call with no credential is refused and carries no data', () async {
    final anonymous = ControllerClient(
      endpoint: LocalSocketEndpoint(controller.socketPath),
      installationId: controller.installationId,
      credentials: () async => null,
    );
    try {
      await anonymous.query(Operations.installationStatus, {
        'scope': anonymous.scope(),
      });
      fail('the controller answered an unauthenticated call');
    } on OperationFailedException catch (e) {
      expect(e.envelope.status, ResultStatus.failed);
      expect(e.fault.code, FaultCode.verificationFailed);
      expect(e.envelope.data, isNull);
      expect(e.fault.retryable, isFalse);
    }
  });

  test('the live snapshot is the controller’s real workspace', () async {
    final source = LiveWorkspaceSource(client, endpointLabel: 'live proof');
    final snapshot = await source.loadSnapshot();

    expect(
      snapshot.workspaceName,
      'Installation ${controller.installationId.substring(0, 8)}',
    );
    // `installation.init` creates the personal organization, its chief and
    // the pinned personal-chief conversation. A first launch opens exactly
    // that conversation.
    final chief = snapshot.workers.singleWhere(
      (w) => w.isOrganizationChief,
      orElse: () => throw StateError(
        'no organization chief in ${snapshot.workers.map((w) => w.name)}',
      ),
    );
    expect(chief.organizationPath, ['Personal']);
    expect(chief.conversationId, isNotNull);
    expect(snapshot.conversations, hasLength(1));
    expect(snapshot.conversations.single.kind, ConversationKind.direct);

    // A fresh installation has no work and no decisions, and the client says
    // so rather than inventing any.
    expect(snapshot.tasks, isEmpty);
    expect(snapshot.reviews, isEmpty);
    expect(snapshot.routines, isEmpty);
    expect(snapshot.files, isEmpty);

    // Every prerequisite notice names a real gap, never an initialized
    // installation that is not initialized.
    expect(
      snapshot.prerequisites.map((p) => p.title),
      isNot(contains('This installation is not initialized')),
    );
  });

  test('a refusal arrives on the status the frozen mapping assigns', () async {
    // The client refuses an envelope whose HTTP status and fault code
    // disagree, so reaching the fault at all proves the mapping held.
    const principalGet = OperationDescriptor.query('principal.get');

    Future<Fault> refusalOf(Map<String, Object?> input) async {
      try {
        await client.query(principalGet, input);
        fail('the controller completed a call it should refuse');
      } on OperationFailedException catch (e) {
        expect(e.envelope.status, ResultStatus.failed);
        expect(e.envelope.data, isNull);
        expect(e.envelope.commandId, isNotEmpty);
        return e.fault;
      }
    }

    // 404: a well-formed identifier that names nothing.
    final missing = await refusalOf({
      'scope': client.scope(),
      'id': '00000000-0000-4000-8000-000000000009',
    });
    expect(missing.code, FaultCode.notFound);
    expect(missing.retryable, isFalse);

    // 400: input the operation's own schema refuses.
    final malformed = await refusalOf({
      'scope': client.scope(),
      'id': 'not-a-uuid',
    });
    expect(malformed.code, FaultCode.invalidInput);
    expect(
      malformed.message,
      isNot(contains('/tmp')),
      reason: 'a fault never discloses a private path',
    );
  });

  test('the snapshot names the identities the controller created', () async {
    final source = LiveWorkspaceSource(client, endpointLabel: 'live proof');
    final snapshot = await source.loadSnapshot();
    final byKind = {for (final p in snapshot.principals) p.kind: p};

    expect(byKind['human']?.name, controller.ownerName);
    expect(byKind['service']?.name, 'controller');
    for (final p in snapshot.principals) {
      expect(p.revoked, isFalse);
      expect(p.id, isNotEmpty);
      // Every kind the controller creates at bootstrap has a real label.
      expect(p.kindLabel, isNot(p.kind));
    }
  });

  test('a message sent from the composer is acknowledged', () async {
    final source = LiveWorkspaceSource(client, endpointLabel: 'live proof');
    final snapshot = await source.loadSnapshot();
    final conversation = snapshot.conversations.single;

    final pending = source.prepareMessage(
      conversation.id,
      'Create a marketing chief.',
    );
    await source.submit(pending);

    // The controller acknowledged it, so the client may show it. It shows
    // only what it sent: the catalog has no operation that reads a
    // conversation's history back.
    final after = await source.loadSnapshot();
    final shown = after.conversations.single;
    expect(shown.messages.map((m) => m.body), ['Create a marketing chief.']);
    expect(shown.messages.single.fromUser, isTrue);
    expect(shown.historyNotice, contains('no operation that reads'));
  });

  test('event.list gives the source a baseline and then no changes', () async {
    final source = LiveWorkspaceSource(client, endpointLabel: 'live proof');
    await source.loadSnapshot();
    expect(
      await source.hasChanges(),
      isFalse,
      reason: 'nothing happened between the snapshot and this call',
    );

    await client.submit(
      client.prepare(principalCreate, agentDefinition('event-proof')),
    );
    expect(
      await source.hasChanges(),
      isTrue,
      reason: 'creating a principal emits an event the client must see',
    );
  });
}
