import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/transport/controller_client.dart';
import 'package:zatiti_desktop/src/transport/endpoint.dart';
import 'package:zatiti_desktop/src/transport/reply_preview.dart';

import '../support/fake_controller.dart';

const _credential = 'Bearer test-fixture-credential';
const _conversation = '00000000-0000-4000-8000-000000000031';

String _preview(
  String id,
  String text, {
  String state = 'streaming',
  List<String> phrases = const [],
}) => jsonEncode({
  'id': id,
  'source_message_id': 'm1',
  'turn_id': 't1',
  'worker_id': 'w1',
  'voice_session_id': 's1',
  'text': text,
  'state': state,
  'phrases': phrases,
});

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

  test('opens a read-only stream and decodes each snapshot', () async {
    fake.script = (_) => EventStreamReply([
      '[]',
      '[${_preview('a', 'Hello. Wor', phrases: ['Hello.'])}]',
    ]);
    final events = client.watchReplies(_conversation);
    final frames = await events.take(2).toList();

    expect(frames.first, isEmpty);
    expect(frames.last.single.text, 'Hello. Wor');
    expect(frames.last.single.phrases, ['Hello.']);
    final sent = fake.requests.first;
    expect(sent.path, '/v1/replies/stream');
    expect(sent.headers['accept'], 'text/event-stream');
    expect(sent.headers['authorization'], _credential);
    expect(sent.json['input'], {
      'scope': client.scope(),
      'conversation_id': _conversation,
    });
    // A read-only stream never carries a command identity.
    expect(sent.json.containsKey('submission_key'), isFalse);
  });

  test(
    'reconnects after the stream ends and resumes from a new snapshot',
    () async {
      var opened = 0;
      fake.script = (_) {
        opened++;
        return EventStreamReply(
          opened == 1
              ? ['[${_preview('a', 'One.')}]']
              : ['[${_preview('a', 'One. Two.', state: 'generated')}]'],
        );
      };
      final seen = <List<ReplyPreview>>[];
      var errors = 0;
      final done = Completer<void>();
      final sub = client.watchReplies(_conversation).listen((frame) {
        seen.add(frame);
        if (seen.length == 2) done.complete();
      }, onError: (Object _) => errors++);
      await done.future.timeout(const Duration(seconds: 10));
      await sub.cancel();

      expect(seen.map((f) => f.single.text), ['One.', 'One. Two.']);
      expect(seen.last.single.state, 'generated');
      expect(errors, 1, reason: 'the disconnect is reported once');
      // Reconnect repeats only the read; nothing else was sent.
      expect(fake.requests.map((r) => r.path).toSet(), {'/v1/replies/stream'});
    },
  );

  test('rejects malformed snapshots instead of displaying them', () async {
    fake.script = (_) => EventStreamReply([
      '{"not":"a list"}',
      '[${_preview('a', 'x', state: 'bogus')}]',
    ]);
    final error = Completer<Object>();
    final sub = client
        .watchReplies(_conversation)
        .listen(
          (_) => fail('a malformed snapshot was delivered'),
          onError: (Object e) {
            if (!error.isCompleted) error.complete(e);
          },
        );
    expect(await error.future, isA<FormatException>());
    await sub.cancel();
  });

  test('ReplyPreview enforces its bounds and states', () {
    Object? decode(Map<String, Object?> patch) => {
      ...jsonDecode(_preview('a', 'x')) as Map<String, Object?>,
      ...patch,
    };
    expect(ReplyPreview.fromJson(decode({})).state, 'streaming');
    for (final bad in [
      {'state': 'final'},
      {'id': ''},
      {'text': 'a' * 8193},
      {
        'phrases': ['a' * 4001],
      },
      {'unexpected': true},
    ]) {
      expect(
        () => ReplyPreview.fromJson(decode(bad)),
        throwsA(isA<Exception>()),
        reason: '$bad',
      );
    }
  });
}
