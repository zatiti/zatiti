import 'package:flutter_test/flutter_test.dart';
import 'package:zatiti_desktop/src/state/spoken_reply_queue.dart';
import 'package:zatiti_desktop/src/transport/reply_preview.dart';
import 'package:zatiti_desktop/src/ui/reply_stream_view.dart';

ReplyPreview _p(
  String id,
  List<String> phrases, {
  String state = 'streaming',
  String source = 'm1',
  String session = 's1',
  String worker = 'w1',
  int sequence = 1,
}) => ReplyPreview.fromJson({
  'id': id,
  'source_message_id': source,
  'turn_id': 't1',
  'worker_id': worker,
  'voice_session_id': session,
  'text': phrases.join(' '),
  'state': state,
  'phrases': phrases,
  'sequence': sequence,
});

List<String> _drain(SpokenReplyQueue q) {
  final out = <String>[];
  for (var p = q.take(); p != null; p = q.take()) {
    out.add('${p.stream}:${p.index}');
  }
  return out;
}

void main() {
  late SpokenReplyQueue q;
  setUp(() {
    q = SpokenReplyQueue()
      ..expect(session: 's1', source: 'm1', workers: {'w1'});
  });

  test('queues each stable phrase once, in order, across snapshots', () {
    q.offer([
      _p('a', ['One.']),
    ]);
    // A reconnect repeats the whole snapshot; only the new phrase queues.
    q.offer([
      _p('a', ['One.', 'Two.']),
    ]);
    q.offer([
      _p('a', ['One.', 'Two.']),
    ]);
    expect(_drain(q), ['a:0', 'a:1']);
    q.offer([
      _p('a', ['One.', 'Two.'], state: 'committed'),
    ]);
    expect(_drain(q), isEmpty);
  });

  test('ignores other sessions, messages and senders', () {
    q.offer([
      _p('x', ['Hi.'], session: 's2'),
      _p('y', ['Hi.'], source: 'm0'),
      _p('z', ['Hi.'], worker: 'w9'),
    ]);
    expect(_drain(q), isEmpty);
    expect(
      q.followed([
        _p('x', ['Hi.'], session: 's2'),
      ]),
      isNull,
    );
  });

  test('a barge-in drops queued phrases and stops following the reply', () {
    q.offer([
      _p('a', ['One.', 'Two.']),
    ]);
    final first = q.take()!;
    q.interrupt();
    expect(q.isCurrent(first), isFalse);
    expect(q.take(), isNull);
    q.offer([
      _p('a', ['One.', 'Two.', 'Three.']),
    ]);
    expect(_drain(q), isEmpty, reason: 'the reply was interrupted by speech');
  });

  test('a provider interruption withdraws only that stream', () {
    q.offer([
      _p('a', ['One.', 'Two.']),
    ]);
    final played = q.take()!;
    expect(played.stream, 'a');
    q.markSpoken(played);
    final withdrawn = q.offer([
      _p('a', ['One.'], state: 'interrupted'),
    ]);
    expect(withdrawn, {'a'});
    expect(_drain(q), isEmpty);
    // The retried attempt for the same message is still followed, but does
    // not repeat what was already played with the same text.
    q.offer([
      _p('a', ['One.'], state: 'interrupted'),
      _p('b', ['One.', 'Two, again.'], sequence: 2),
    ]);
    expect(_drain(q), ['b:1']);
  });

  test('a retry speaks phrases the interrupted attempt never played', () {
    q.offer([
      _p('a', ['One.', 'Two.']),
    ]);
    // a:0 was taken for synthesis but interrupted before it played; a:1
    // was still queued.
    expect(q.take()!.stream, 'a');
    q.offer([
      _p('a', ['One.'], state: 'interrupted'),
    ]);
    q.offer([
      _p('b', ['One.', 'Two.'], sequence: 2),
    ]);
    expect(_drain(q), ['b:0', 'b:1']);
  });

  test('a full queue offers the overflow again on the next snapshot', () {
    q = SpokenReplyQueue(maxQueued: 1)
      ..expect(session: 's1', source: 'm1', workers: {'w1'});
    q.offer([
      _p('a', ['One.', 'Two.']),
    ]);
    expect(_drain(q), ['a:0']);
    q.offer([
      _p('a', ['One.', 'Two.']),
    ]);
    expect(_drain(q), ['a:1']);
  });

  test('follows the most recently published preview', () {
    final shown = q.followed([
      _p('newer', ['Later.'], sequence: 5),
      _p('older', ['Earlier.'], sequence: 2),
    ]);
    expect(shown!.id, 'newer');
  });

  test('a new message starts a new generation', () {
    q.offer([
      _p('a', ['One.']),
    ]);
    q.expect(session: 's1', source: 'm2', workers: {'w1'});
    expect(q.take(), isNull);
    q.offer([
      _p('c', ['Fresh.'], source: 'm2'),
    ]);
    expect(_drain(q), ['c:0']);
  });

  test('preview labels never claim durable history', () {
    for (final state in [
      'streaming',
      'generated',
      'committed',
      'interrupted',
    ]) {
      expect(replyPreviewLabel(state), isNot(contains('saved')));
    }
    expect(replyPreviewLabel('committed'), contains('not in history'));
    expect(
      replyPreviewLabel('interrupted', disconnected: true),
      contains('interrupted'),
    );
  });
}
