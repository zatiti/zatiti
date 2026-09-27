// Decides which streamed reply phrases voice mode should speak, in order,
// exactly once. It holds no audio and makes no calls; the voice dialog
// synthesizes and plays what [take] returns.

import '../transport/reply_preview.dart';

/// One phrase of one reply stream, tagged with the listening generation it
/// was queued under so a barge-in can discard it.
typedef SpokenPhrase = ({String stream, int index, int generation});

class SpokenReplyQueue {
  SpokenReplyQueue({this.maxQueued = 128});

  final int maxQueued;
  final List<SpokenPhrase> _pending = [];
  // Every stream:index ever queued, so a reconnect's repeated snapshot
  // never queues a phrase twice.
  final Set<String> _queued = {};
  // Phrase texts already queued per source message, so a retried attempt
  // (a new stream for the same message) does not repeat what the
  // interrupted attempt already said.
  final Map<String, Map<int, String>> _saidBySource = {};
  String? _session;
  String? _source;
  Set<String> _workers = const {};
  int _generation = 0;

  int get generation => _generation;
  bool get isEmpty => _pending.isEmpty;

  /// Starts listening for replies to [source], the message just sent in
  /// voice [session]. Anything still queued belongs to an older turn.
  void expect({
    required String session,
    required String source,
    required Set<String> workers,
  }) {
    _generation++;
    _pending.clear();
    _session = session;
    _source = source;
    _workers = workers;
  }

  /// The human started speaking: drop everything not yet played and stop
  /// following the current reply.
  void interrupt() {
    _generation++;
    _pending.clear();
    _source = null;
  }

  /// Applies one snapshot. Returns the ids of followed streams that are
  /// withdrawn, so the caller can stop audio that belongs to one of them.
  Set<String> offer(List<ReplyPreview> previews) {
    final withdrawn = <String>{};
    final source = _source;
    if (source == null) return withdrawn;
    for (final p in previews) {
      if (p.voiceSession != _session ||
          p.source != source ||
          !_workers.contains(p.worker)) {
        continue;
      }
      if (p.state == 'interrupted') {
        // A provider interruption withdraws this stream only; a retried
        // attempt for the same message may still follow.
        _pending.removeWhere((q) => q.stream == p.id);
        withdrawn.add(p.id);
        continue;
      }
      final said = _saidBySource.putIfAbsent(source, () => {});
      for (var i = 0; i < p.phrases.length; i++) {
        if (!_queued.add('${p.id}:$i')) continue;
        if (said[i] == p.phrases[i]) continue;
        said[i] = p.phrases[i];
        if (_pending.length < maxQueued) {
          _pending.add((stream: p.id, index: i, generation: _generation));
        }
      }
    }
    return withdrawn;
  }

  /// The newest followed, uninterrupted preview in [previews], for display.
  ReplyPreview? followed(List<ReplyPreview> previews) {
    ReplyPreview? out;
    for (final p in previews) {
      if (_source != null &&
          p.source == _source &&
          p.voiceSession == _session &&
          _workers.contains(p.worker) &&
          p.state != 'interrupted') {
        out = p;
      }
    }
    return out;
  }

  /// Removes and returns the next current phrase, skipping stale ones.
  SpokenPhrase? take() {
    while (_pending.isNotEmpty) {
      final next = _pending.removeAt(0);
      if (next.generation == _generation) return next;
    }
    return null;
  }

  /// Whether [phrase] still belongs to the current listening generation.
  bool isCurrent(SpokenPhrase phrase) => phrase.generation == _generation;

  void clear() => _pending.clear();
}
