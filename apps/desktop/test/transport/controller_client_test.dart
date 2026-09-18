import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/transport/controller_client.dart';
import 'package:zatiti_desktop/src/transport/endpoint.dart';
import 'package:zatiti_desktop/src/transport/envelope.dart';
import 'package:zatiti_desktop/src/transport/errors.dart';
import 'package:zatiti_desktop/src/transport/operations.dart';
import 'package:zatiti_desktop/src/transport/submission.dart';

import '../support/fake_controller.dart';

const _credential = 'Bearer test-fixture-credential';
const _reviewId = '00000000-0000-4000-8000-000000000010';

Map<String, Object?> _decideInput(ControllerClient c) => {
  'scope': c.scope(),
  'id': _reviewId,
  'expected_version': 3,
  'action_digest': 'a' * 64,
  'decision': 'approve',
  'reason': '',
};

String _commandResource(String key, String resultJson) =>
    '{"resource":{"id":"00000000-0000-4000-8000-000000000077",'
    '"principal_id":"00000000-0000-4000-8000-000000000078",'
    '"operation":"review.decide","operation_version":1,'
    '"submission_key":"$key","request_digest":"${'b' * 64}",'
    '"status":"completed","data":{},"result":$resultJson}}';

void main() {
  late FakeController fake;
  late ControllerClient client;

  setUp(() async {
    fake = await FakeController.start();
    client = ControllerClient(
      endpoint: LocalSocketEndpoint(fake.socketPath),
      installationId: testInstallationId,
      credentials: () async => _credential,
      timeout: const Duration(seconds: 5),
    );
  });

  tearDown(() => fake.stop());

  group('request shape', () {
    test('POSTs the envelope to the single operation route', () async {
      fake.script = (_) => completed('{"items":[]}');
      final result = await client.query(Operations.reviewList, {
        'scope': client.scope(),
        'filter': {'needs_you': true},
      });
      expect(result.status, ResultStatus.completed);

      final sent = fake.requests.single;
      expect(sent.method, 'POST');
      expect(sent.path, '/v1/operations/review.list');
      expect(sent.headers['content-type'], startsWith('application/json'));
      expect(sent.headers['authorization'], _credential);
      expect(sent.json['schema'], 'zatiti.request/v1');
      expect(sent.json.containsKey('submission_key'), isFalse);
      expect(sent.input, {
        'scope': {'installation_id': testInstallationId},
        'filter': {'needs_you': true},
      });
    });

    test('never puts the credential in the body', () async {
      await client.query(Operations.reviewList, {'scope': client.scope()});
      expect(fake.requests.single.body.contains('test-fixture'), isFalse);
    });

    test('omits Authorization when no credential is stored', () async {
      final anonymous = ControllerClient(
        endpoint: LocalSocketEndpoint(fake.socketPath),
        installationId: testInstallationId,
        credentials: () async => null,
      );
      await anonymous.query(Operations.installationStatus, {
        'scope': anonymous.scope(),
      });
      expect(
        fake.requests.single.headers.containsKey('authorization'),
        isFalse,
      );
    });

    test('refuses a credential that would split the header', () async {
      final bad = ControllerClient(
        endpoint: LocalSocketEndpoint(fake.socketPath),
        installationId: testInstallationId,
        credentials: () async => 'Bearer x\r\nX-Injected: 1',
      );
      await expectLater(
        bad.query(Operations.reviewList, {'scope': bad.scope()}),
        throwsA(isA<InvalidRequestException>()),
      );
      expect(fake.requests, isEmpty);
    });

    test('accepted results arrive on 202', () async {
      fake.script = (_) => accepted('{"resource":{}}');
      final s = client.prepare(Operations.conversationMessageSend, {
        'scope': client.scope(),
      });
      expect((await client.submit(s)).status, ResultStatus.accepted);
    });

    test('a query descriptor cannot be submitted and vice versa', () {
      expect(
        () => client.prepare(Operations.reviewList, {}),
        throwsA(isA<InvalidRequestException>()),
      );
      expect(
        client.query(Operations.reviewDecide, {}),
        throwsA(isA<InvalidRequestException>()),
      );
    });
  });

  group('faults', () {
    test('a failed envelope surfaces its fault and full envelope', () async {
      fake.script = (_) => fault(409, 'stale_version', 'review moved on');
      final s = client.prepare(Operations.reviewDecide, _decideInput(client));
      await expectLater(
        client.submit(s),
        throwsA(
          isA<OperationFailedException>()
              .having((e) => e.fault.code, 'code', FaultCode.staleVersion)
              .having((e) => e.envelope.commandId, 'command', testCommandId),
        ),
      );
      // A refusal is an authoritative disposition, not an unknown one.
      expect(s.state, SubmissionState.settled);
    });

    test('a fault envelope on the wrong HTTP status is not trusted', () async {
      fake.script = (_) =>
          RawReply(200, faultEnvelope('conflict', 'status says 200'));
      await expectLater(
        client.query(Operations.reviewList, {'scope': client.scope()}),
        throwsA(isA<OutcomeUnknownException>()),
      );
    });

    test('a redirect is refused, never followed', () async {
      fake.script = (_) => const RawReply(307, '');
      await expectLater(
        client.query(Operations.reviewList, {'scope': client.scope()}),
        throwsA(isA<OutcomeUnknownException>()),
      );
      expect(fake.requests, hasLength(1));
    });
  });

  group('submission keys', () {
    test('a mutation carries a generated key', () async {
      final s = client.prepare(Operations.reviewDecide, _decideInput(client));
      await client.submit(s);
      expect(isValidSubmissionKey(s.key), isTrue);
      expect(fake.requests.single.submissionKey, s.key);
    });

    test('two intended mutations never share a key', () {
      final a = client.prepare(Operations.reviewDecide, _decideInput(client));
      final b = client.prepare(Operations.reviewDecide, _decideInput(client));
      expect(a.key, isNot(b.key));
    });

    test('a settled submission cannot be sent again', () async {
      final s = client.prepare(Operations.reviewDecide, _decideInput(client));
      await client.submit(s);
      expect(() => client.submit(s), throwsStateError);
      expect(fake.requests, hasLength(1));
    });
  });

  group('controller_unavailable: nothing was sent', () {
    test('a missing socket is unavailable, and the key survives', () async {
      final offline = ControllerClient(
        endpoint: LocalSocketEndpoint('${fake.socketPath}.absent'),
        installationId: testInstallationId,
        credentials: () async => _credential,
      );
      final s = offline.prepare(Operations.reviewDecide, _decideInput(offline));
      await expectLater(
        offline.submit(s),
        throwsA(isA<ControllerUnavailableException>()),
      );
      expect(s.state, SubmissionState.notSent);
      expect(s.attempts, 0);
    });

    test('an explicit retry reuses the same key and the same bytes', () async {
      await fake.goDown();
      final s = client.prepare(Operations.reviewDecide, _decideInput(client));
      await expectLater(
        client.submit(s),
        throwsA(isA<ControllerUnavailableException>()),
      );
      expect(fake.requests, isEmpty);

      // The controller comes back at the same path; every call dials fresh.
      await fake.comeBack();
      await client.submit(s);
      final sent = fake.requests.single;
      expect(sent.body, String.fromCharCodes(s.body));
      expect(sent.submissionKey, s.key);
    });
  });

  group('outcome_unknown: bytes may have been sent', () {
    test('a dropped connection never triggers an automatic resend', () async {
      fake.script = (_) => const DropReply();
      final s = client.prepare(Operations.reviewDecide, _decideInput(client));
      await expectLater(
        client.submit(s),
        throwsA(
          isA<OutcomeUnknownException>().having(
            (e) => e.submission,
            'submission',
            same(s),
          ),
        ),
      );
      expect(s.state, SubmissionState.acknowledgmentUnknown);
      expect(fake.requestsFor('review.decide'), hasLength(1));
    });

    test('a timeout after sending is unknown, not unavailable', () async {
      final impatient = ControllerClient(
        endpoint: LocalSocketEndpoint(fake.socketPath),
        installationId: testInstallationId,
        credentials: () async => _credential,
        timeout: const Duration(milliseconds: 300),
      );
      fake.script = (_) => const HangReply();
      final s = impatient.prepare(
        Operations.reviewDecide,
        _decideInput(impatient),
      );
      await expectLater(
        impatient.submit(s),
        throwsA(isA<OutcomeUnknownException>()),
      );
      expect(s.state, SubmissionState.acknowledgmentUnknown);
    });

    test('a malformed envelope after a mutation is unknown', () async {
      fake.script = (_) => const RawReply(200, '{"schema":"zatiti.result/v1"');
      final s = client.prepare(Operations.reviewDecide, _decideInput(client));
      await expectLater(
        client.submit(s),
        throwsA(isA<OutcomeUnknownException>()),
      );
    });

    test('retry is locked until the disposition is looked up', () async {
      fake.script = (_) => const DropReply();
      final s = client.prepare(Operations.reviewDecide, _decideInput(client));
      await expectLater(
        client.submit(s),
        throwsA(isA<OutcomeUnknownException>()),
      );
      expect(() => client.submit(s), throwsStateError);
      expect(fake.requestsFor('review.decide'), hasLength(1));
    });

    test('command.get finds the original disposition', () async {
      final s = client.prepare(Operations.reviewDecide, _decideInput(client));
      fake.script = (r) => r.path.endsWith('command.get')
          ? completed(_commandResource(s.key, completedEnvelope('{"ok":1}')))
          : const DropReply();
      await expectLater(
        client.submit(s),
        throwsA(isA<OutcomeUnknownException>()),
      );

      final disposition = await client.resolve(s);
      expect(disposition, isA<DispositionFound>());
      expect((disposition as DispositionFound).original.data, {'ok': 1});
      expect(s.state, SubmissionState.settled);

      // The lookup is a query carrying the ORIGINAL key in its input, shaped
      // as the catalog's command.get input schema requires.
      final lookup = fake.requestsFor('command.get').single;
      expect(lookup.submissionKey, isNull);
      expect(lookup.input, {
        'scope': {'installation_id': testInstallationId},
        'submission_key': s.key,
        'operation': 'review.decide',
        'operation_version': 1,
      });
      // Settled: no resend is possible.
      expect(() => client.submit(s), throwsStateError);
      expect(fake.requestsFor('review.decide'), hasLength(1));
    });

    test('command.get returns a refused command as its fault', () async {
      final s = client.prepare(Operations.reviewDecide, _decideInput(client));
      fake.script = (r) => r.path.endsWith('command.get')
          ? completed(
              _commandResource(
                s.key,
                faultEnvelope('stale_version', 'review moved on'),
              ),
            )
          : const DropReply();
      await expectLater(
        client.submit(s),
        throwsA(isA<OutcomeUnknownException>()),
      );
      final found = await client.resolve(s) as DispositionFound;
      expect(found.original.error!.code, FaultCode.staleVersion);
    });

    test('not_found proves nothing committed and unlocks one explicit retry '
        'with the same key', () async {
      var decideCalls = 0;
      final s = client.prepare(Operations.reviewDecide, _decideInput(client));
      fake.script = (r) {
        if (r.path.endsWith('command.get')) {
          return fault(404, 'not_found', 'command not found');
        }
        decideCalls++;
        return decideCalls == 1 ? const DropReply() : completed('{}');
      };
      await expectLater(
        client.submit(s),
        throwsA(isA<OutcomeUnknownException>()),
      );
      expect(await client.resolve(s), isA<DispositionNotCommitted>());
      expect(s.state, SubmissionState.notCommitted);
      // Resolving did not resend anything by itself.
      expect(fake.requestsFor('review.decide'), hasLength(1));

      await client.submit(s);
      final sends = fake.requestsFor('review.decide');
      expect(sends, hasLength(2));
      expect(sends[0].body, sends[1].body);
      expect(sends[1].submissionKey, s.key);
    });

    test('a failed lookup leaves the submission locked', () async {
      fake.script = (_) => const DropReply();
      final s = client.prepare(Operations.reviewDecide, _decideInput(client));
      await expectLater(
        client.submit(s),
        throwsA(isA<OutcomeUnknownException>()),
      );
      await expectLater(
        client.resolve(s),
        throwsA(isA<OutcomeUnknownException>()),
      );
      expect(s.state, SubmissionState.acknowledgmentUnknown);
      expect(() => client.submit(s), throwsStateError);
    });

    test('a lookup answering for another submission is refused', () async {
      final s = client.prepare(Operations.reviewDecide, _decideInput(client));
      fake.script = (r) => r.path.endsWith('command.get')
          ? completed(_commandResource('someone-else', completedEnvelope('{}')))
          : const DropReply();
      await expectLater(
        client.submit(s),
        throwsA(isA<OutcomeUnknownException>()),
      );
      await expectLater(client.resolve(s), throwsA(isA<Exception>()));
      expect(s.state, SubmissionState.acknowledgmentUnknown);
    });

    test('only an unknown acknowledgment can be resolved', () {
      final s = client.prepare(Operations.reviewDecide, _decideInput(client));
      expect(() => client.resolve(s), throwsStateError);
    });

    test('a query with a lost answer may be reissued', () async {
      var calls = 0;
      fake.script = (_) =>
          ++calls == 1 ? const DropReply() : completed('{"items":[]}');
      await expectLater(
        client.query(Operations.reviewList, {'scope': client.scope()}),
        throwsA(
          isA<OutcomeUnknownException>().having(
            (e) => e.submission,
            'submission',
            isNull,
          ),
        ),
      );
      await client.query(Operations.reviewList, {'scope': client.scope()});
    });
  });
}
