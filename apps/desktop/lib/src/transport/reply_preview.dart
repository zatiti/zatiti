import 'strict_json.dart';

class ReplyPreview {
  ReplyPreview.fromJson(Object? value) {
    final o = StrictObject(value, 'reply preview');
    id = o.string('id');
    source = o.string('source_message_id');
    turn = o.string('turn_id');
    worker = o.string('worker_id');
    voiceSession = o.optionalString('voice_session_id');
    text = o.string('text');
    state = o.string('state');
    phrases = o
        .list('phrases')
        .map((v) {
          if (v is! String || v.runes.length > 4000) {
            throw const FormatException('Invalid spoken phrase.');
          }
          return v;
        })
        .toList(growable: false);
    o.finish();
    if (id.isEmpty ||
        id.length > 256 ||
        text.runes.length > 8192 ||
        phrases.length > 1000 ||
        !const {
          'streaming',
          'generated',
          'committed',
          'interrupted',
        }.contains(state)) {
      throw const FormatException('Invalid reply preview.');
    }
  }
  late final String id, source, turn, worker, text, state;
  late final String? voiceSession;
  late final List<String> phrases;
}
