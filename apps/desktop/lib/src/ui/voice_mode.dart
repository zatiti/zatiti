// Voice mode stays in the trusted Flutter client for microphone/speaker work;
// all model calls pass through the authenticated controller.
import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../api/models.dart' as wire;
import '../state/live_source.dart';
import '../state/snapshot.dart';
import '../state/workspace_controller.dart';
import '../transport/envelope.dart';
import '../transport/errors.dart';
import '../transport/operations.dart';
import '../transport/strict_json.dart';
import '../transport/reply_preview.dart';
import '../state/spoken_reply_queue.dart';

const _voiceChannel = MethodChannel('zatiti/voice');
const _voiceEvents = EventChannel('zatiti/voice/events');

Future<void> showVoiceMode(
  BuildContext context, {
  required WorkspaceController controller,
  required ConversationId conversation,
}) => showDialog<void>(
  context: context,
  barrierDismissible: false,
  builder: (_) =>
      _VoiceModeDialog(controller: controller, conversation: conversation),
);

class _VoiceModeDialog extends StatefulWidget {
  const _VoiceModeDialog({
    required this.controller,
    required this.conversation,
  });
  final WorkspaceController controller;
  final ConversationId conversation;
  @override
  State<_VoiceModeDialog> createState() => _VoiceModeDialogState();
}

class _VoiceModeDialogState extends State<_VoiceModeDialog> {
  late final LiveWorkspaceSource _source =
      widget.controller.source as LiveWorkspaceSource;
  StreamSubscription<dynamic>? _nativeEvents;
  List<wire.Connection> _connections = const [];
  wire.Connection? _input;
  wire.Connection? _output;
  String _transcriptionModel = 'openai/whisper-large-v3-turbo';
  String _speechModel = 'deepgram/flux-tts:free';
  String _voice = 'flux-alexis-en';
  String _style = 'conversational';
  double _budgetDollars = 1;
  String _status = 'Choose a dedicated OpenRouter voice key.';
  String? _transcript;
  String? _session;
  bool _busy = false;
  bool _closed = false;
  StreamSubscription<List<ReplyPreview>>? _replyEvents;
  final SpokenReplyQueue _speech = SpokenReplyQueue();
  final Set<String> _withdrawn = {};
  bool _speaking = false;
  String? _playing;
  Set<String> _workerIds = {};

  bool _isVoiceConnection(wire.Connection c) =>
      c.provider == 'openrouter' &&
      c.allowedScopes.length == 1 &&
      c.allowedScopes.contains('voice') &&
      c.destinations.length == 3 &&
      c.destinations.contains(
        'https://openrouter.ai/api/v1/audio/transcriptions',
      ) &&
      c.destinations.contains('https://openrouter.ai/api/v1/audio/speech') &&
      c.destinations.contains(
        'https://openrouter.ai/api/v1/chat/completions',
      ) &&
      c.validationState != wire.ConnectionValidationState.revoked &&
      c.validationState != wire.ConnectionValidationState.invalid &&
      c.validationState != wire.ConnectionValidationState.expired;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    if (!_closed) {
      _closed = true;
      _speech.interrupt();
      unawaited(_releaseOnDispose());
    }
    super.dispose();
  }

  Future<void> _releaseOnDispose() async {
    try {
      await _replyEvents?.cancel();
      await _nativeEvents?.cancel();
      await _voiceChannel.invokeMethod<void>('stop');
    } on Exception {
      // The runner may already be gone while the application closes.
    }
    final id = _session;
    if (id != null) {
      try {
        await _submit(Operations.voiceSessionEnd, {
          'scope': _scope(),
          'session_id': id,
        });
      } on Exception {
        // Session expiry remains the controller's bounded fallback.
      }
    }
  }

  Future<void> _load() async {
    try {
      final all = await _source.api.connections();
      if (!mounted) return;
      setState(() {
        _connections = all.where(_isVoiceConnection).toList();
        _input = _connections.firstOrNull;
        _output = _connections.firstOrNull;
        _workerIds = widget.controller.snapshot.workers
            .map((w) => w.id.value)
            .toSet();
        _status = _connections.isEmpty
            ? 'Create a Dedicated voice key in Settings, then capture it with Set API key.'
            : 'Voice transcripts are sent to this conversation. OpenRouter costs use an estimate.';
      });
    } on Exception {
      if (mounted) {
        setState(() => _status = 'Voice connections could not be loaded.');
      }
    }
  }

  Map<String, Object?> _scope() => _source.api.client.scope();

  Future<Map<String, Object?>> _submit(
    OperationDescriptor op,
    Map<String, Object?> input,
  ) async {
    final sub = _source.api.client.prepare(op, input);
    final result = await _source.api.client.submit(sub);
    if (result.status != ResultStatus.completed) {
      throw const InvalidRequestException(
        'The voice command was not completed.',
      );
    }
    return result.requireData(op.id);
  }

  void _validateSession(Object? value) {
    final outer = StrictObject(value, 'voice session result');
    final r = StrictObject(outer.object('resource'), 'voice session');
    _session = r.string('id');
    r.string('conversation_id');
    r.string('state');
    final settings = StrictObject(r.object('settings'), 'voice settings');
    final input = wire.Ref.fromJson(settings.object('input_connection'));
    final output = wire.Ref.fromJson(settings.object('output_connection'));
    settings.string('transcription_model');
    settings.string('speech_model');
    settings.string('voice');
    settings.string('language');
    settings.string('style');
    settings.integer('budget_micro_units');
    settings.integer('call_allowance_micro_units');
    settings.boolean('advisory_cost_acknowledged');
    settings.finish();
    if (!_connections.any(
          (c) => c.id == input.id && c.version == input.version,
        ) ||
        !_connections.any(
          (c) => c.id == output.id && c.version == output.version,
        )) {
      throw const FormatException('Voice connection version changed.');
    }
    r.integer('reserved_micro_units');
    r.integer('calls');
    r.dateTime('expires_at');
    r.finish();
    outer.finish();
  }

  Future<void> _start() async {
    final input = _input, output = _output;
    if (_busy ||
        input == null ||
        output == null ||
        !widget.controller.isOnline) {
      return;
    }
    setState(() {
      _busy = true;
      _status = 'Opening a private voice session…';
    });
    try {
      final data = await _submit(Operations.voiceSessionBegin, {
        'scope': _scope(),
        'conversation_id': widget.conversation.value,
        'settings': {
          'input_connection': {'id': input.id, 'version': input.version},
          'output_connection': {'id': output.id, 'version': output.version},
          'transcription_model': _transcriptionModel,
          'speech_model': _speechModel,
          'voice': _voice,
          'language': 'en',
          'style': _style,
          'budget_micro_units': (_budgetDollars * 1000000).round(),
          'call_allowance_micro_units': 5000,
          'advisory_cost_acknowledged': true,
        },
      });
      _validateSession(data);
      if (_session == null) {
        throw const FormatException('Voice session ID missing.');
      }
      if (_closed) {
        final id = _session!;
        _session = null;
        await _submit(Operations.voiceSessionEnd, {
          'scope': _scope(),
          'session_id': id,
        });
        return;
      }
      final session = _session;
      if (session == null) {
        throw const FormatException('Voice session ID missing.');
      }
      _replyEvents = _source.api.client
          .watchReplies(widget.conversation.value)
          .listen(
            _onReplies,
            onError: (Object _) {
              if (mounted) {
                setState(() => _status = 'Reconnecting to live replies…');
              }
            },
          );
      _nativeEvents = _voiceEvents.receiveBroadcastStream().listen(
        _onNative,
        onError: (Object error) {
          if (mounted) {
            setState(
              () => _status =
                  'Microphone unavailable. Check macOS privacy settings.',
            );
          }
        },
      );
      await _voiceChannel.invokeMethod<void>('start');
      if (!mounted) return;
      setState(() {
        _busy = false;
        _status =
            'Listening. Spoken turns are sent to ${widget.controller.selectedConversation?.title ?? 'this conversation'}.';
      });
    } on Exception catch (e) {
      await _end(sendEnd: _session != null);
      if (mounted) {
        setState(() {
          _busy = false;
          _status = 'Voice mode could not start: ${_safeError(e)}';
        });
      }
    }
  }

  String _safeError(Object e) => e is OperationFailedException
      ? e.fault.message
      : 'check the controller and dedicated key';

  void _interrupt() {
    _speech.interrupt();
    unawaited(_interruptAudio());
  }

  Future<void> _interruptAudio() async {
    try {
      await _voiceChannel.invokeMethod<void>('interrupt');
    } on Exception {
      // Native playback may already have stopped.
    }
  }

  void _onNative(dynamic event) {
    if (event is! Map || _closed) return;
    final type = event['type'];
    if (type == 'utterance' && event['audio'] is Uint8List) {
      unawaited(_transcribe((event['audio'] as Uint8List)));
    } else if (type == 'speech_start') {
      _interrupt();
      if (mounted) setState(() => _status = 'Listening to you…');
    } else if (type == 'playback_ended' && mounted) {
      setState(() => _status = 'Listening. Say something to continue.');
    } else if (type == 'stopped' && mounted && !_closed) {
      setState(() => _status = 'Audio stopped because Zatiti lost focus.');
      unawaited(_end());
    }
  }

  Future<void> _transcribe(Uint8List wav) async {
    if (_closed || _session == null || _busy) return;
    final session = _session!;
    if (mounted) {
      setState(() {
        _busy = true;
        _status = 'Transcribing…';
      });
    }
    try {
      final data = await _submit(Operations.voiceTranscribe, {
        'scope': _scope(),
        'session_id': session,
        'audio': base64Encode(wav),
      });
      final out = StrictObject(data, 'voice transcript');
      final returnedSession = out.string('session_id');
      out.string('call_id');
      final text = out.string('text');
      out.string('audio');
      out.string('media_type');
      final billing = out.string('billing');
      out.integer('reserved_micro_units');
      out.finish();
      if (_closed || returnedSession != _session) return;
      if (text.trim().isEmpty) {
        if (mounted) {
          setState(() {
            _busy = false;
            _status = 'No speech detected. Listening…';
          });
        }
        return;
      }
      if (mounted) {
        setState(() {
          _transcript = text;
          _status =
              'Sending your words to the selected conversation. Voice charge: $billing.';
        });
      }
      final message = _source.api.prepareMessageSend(
        conversationId: widget.conversation.value,
        body: text,
      );
      _speech.expect(
        session: session,
        source: message.messageId,
        workers: _workerIds,
      );
      final sent = await _source.api.client.submit(message.submission);
      if (sent.status != ResultStatus.completed) {
        throw const FormatException('Message was not confirmed.');
      }
      if (_closed) return;
      if (mounted) {
        setState(() {
          _busy = false;
          _status = 'Waiting for the worker…';
        });
      }
    } on Exception catch (e) {
      if (mounted) {
        setState(() {
          _busy = false;
          _status = 'Voice turn could not be confirmed: ${_safeError(e)}';
        });
      }
      // The original command identity is retained by the client; never replay
      // an ambiguous speech call in a fresh session automatically.
    }
  }

  void _onReplies(List<ReplyPreview> previews) {
    if (_closed || _session == null) return;
    final withdrawn = _speech.offer(previews);
    _withdrawn.addAll(withdrawn);
    if (_playing != null && withdrawn.contains(_playing)) {
      unawaited(_interruptAudio());
    }
    final shown = _speech.followed(previews);
    if (shown != null && mounted) setState(() => _transcript = shown.text);
    if (!_speaking) unawaited(_drainSpeech());
  }

  /// Synthesizes one phrase the controller resolves from the authorized
  /// preview; the client never supplies the text.
  Future<Uint8List> _synthesize(SpokenPhrase phrase) async {
    final data = await _submit(Operations.voiceSpeakPhrase, {
      'scope': _scope(),
      'session_id': _session,
      'stream_id': phrase.stream,
      'phrase_index': phrase.index,
    });
    final out = StrictObject(data, 'spoken phrase');
    final session = out.string('session_id');
    out.string('call_id');
    out.string('text');
    final audio = base64Decode(out.string('audio'));
    final media = out.string('media_type');
    out.string('billing');
    out.integer('reserved_micro_units');
    out.finish();
    if (session != _session ||
        media != 'audio/mpeg' ||
        audio.isEmpty ||
        audio.length > 4 * 1024 * 1024) {
      throw const FormatException('Invalid speech audio.');
    }
    return Uint8List.fromList(audio);
  }

  /// Plays queued phrases in order, synthesizing the next phrase while the
  /// current one plays so consecutive phrases do not wait on each other.
  Future<void> _drainSpeech() async {
    if (_speaking || _closed) return;
    _speaking = true;
    Future<Uint8List>? nextAudio;
    try {
      var current = _speech.take();
      var currentAudio = current == null ? null : _synthesize(current);
      while (current != null && currentAudio != null && !_closed) {
        final audio = await currentAudio;
        final next = _speech.take();
        nextAudio = next == null ? null : _synthesize(next);
        if (_speech.isCurrent(current) &&
            !_withdrawn.contains(current.stream)) {
          if (mounted) {
            setState(
              () => _status = 'Speaking. You can interrupt at any time.',
            );
          }
          _playing = current.stream;
          try {
            await _voiceChannel.invokeMethod<void>('play', {'audio': audio});
            if (!_withdrawn.contains(current.stream)) {
              _speech.markSpoken(current);
            }
          } finally {
            _playing = null;
          }
        }
        current = next ?? _speech.take();
        currentAudio = next != null
            ? nextAudio
            : (current == null ? null : _synthesize(current));
        nextAudio = null;
      }
    } on Exception catch (e) {
      nextAudio?.ignore();
      _speech.clear();
      if (mounted) {
        setState(() => _status = 'Reply audio unavailable: ${_safeError(e)}');
      }
    } finally {
      _speaking = false;
    }
  }

  Future<void> _end({bool sendEnd = true}) async {
    if (_closed) return;
    _closed = true;
    _speech.interrupt();
    await _replyEvents?.cancel();
    await _nativeEvents?.cancel();
    try {
      await _voiceChannel.invokeMethod<void>('stop');
    } on Exception {
      /* device already stopped */
    }
    final id = _session;
    _session = null;
    if (sendEnd && id != null) {
      try {
        await _submit(Operations.voiceSessionEnd, {
          'scope': _scope(),
          'session_id': id,
        });
      } on Exception {
        /* end command remains visible to the controller */
      }
    }
    if (mounted) Navigator.of(context).pop();
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: const Text('Voice conversation'),
    content: SizedBox(
      width: 470,
      child: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(_status),
            if (_session == null) ...[
              const SizedBox(height: 12),
              _connection(
                'Voice input key',
                _input,
                (v) => setState(() => _input = v),
              ),
              _connection(
                'Voice output key',
                _output,
                (v) => setState(() => _output = v),
              ),
              DropdownButtonFormField<String>(
                value: _transcriptionModel,
                decoration: const InputDecoration(
                  labelText: 'Speech recognition',
                ),
                items: const [
                  DropdownMenuItem(
                    value: 'openai/whisper-large-v3-turbo',
                    child: Text('Whisper Large V3 Turbo · low cost'),
                  ),
                  DropdownMenuItem(
                    value: 'qwen/qwen3-asr-0.6b',
                    child: Text('Qwen3 ASR 0.6B · low cost'),
                  ),
                ],
                onChanged: _busy
                    ? null
                    : (v) => setState(
                        () => _transcriptionModel = v ?? _transcriptionModel,
                      ),
              ),
              DropdownButtonFormField<String>(
                value: _speechModel,
                decoration: const InputDecoration(labelText: 'Speech voice'),
                items: const [
                  DropdownMenuItem(
                    value: 'deepgram/flux-tts:free',
                    child: Text('Deepgram Flux · free route'),
                  ),
                  DropdownMenuItem(
                    value: 'hexgrad/kokoro-82m',
                    child: Text('Kokoro · low-cost route'),
                  ),
                ],
                onChanged: _busy
                    ? null
                    : (v) => setState(() {
                        _speechModel = v ?? _speechModel;
                        _voice = _speechModel == 'deepgram/flux-tts:free'
                            ? 'flux-alexis-en'
                            : 'af_heart';
                      }),
              ),
              if (_speechModel == 'deepgram/flux-tts:free')
                DropdownButtonFormField<String>(
                  value: _voice,
                  decoration: const InputDecoration(labelText: 'Voice'),
                  items: const [
                    DropdownMenuItem(
                      value: 'flux-alexis-en',
                      child: Text('Alexis'),
                    ),
                    DropdownMenuItem(
                      value: 'flux-bree-en',
                      child: Text('Bree'),
                    ),
                    DropdownMenuItem(
                      value: 'flux-jack-en',
                      child: Text('Jack'),
                    ),
                  ],
                  onChanged: _busy
                      ? null
                      : (v) => setState(() => _voice = v ?? _voice),
                ),
              if (_speechModel == 'hexgrad/kokoro-82m')
                DropdownButtonFormField<String>(
                  value: _voice,
                  decoration: const InputDecoration(labelText: 'Voice'),
                  items: const [
                    DropdownMenuItem(value: 'af_heart', child: Text('Heart')),
                    DropdownMenuItem(value: 'af_bella', child: Text('Bella')),
                    DropdownMenuItem(
                      value: 'am_michael',
                      child: Text('Michael'),
                    ),
                  ],
                  onChanged: _busy
                      ? null
                      : (v) => setState(() => _voice = v ?? _voice),
                ),
              DropdownButtonFormField<String>(
                value: _style,
                decoration: const InputDecoration(
                  labelText: 'Narrate voice craft',
                ),
                items: const [
                  DropdownMenuItem(
                    value: 'conversational',
                    child: Text('Conversational'),
                  ),
                  DropdownMenuItem(value: 'coach', child: Text('Coach')),
                  DropdownMenuItem(
                    value: 'agent-update',
                    child: Text('Agent update'),
                  ),
                  DropdownMenuItem(value: 'verbatim', child: Text('Verbatim')),
                ],
                onChanged: _busy
                    ? null
                    : (v) => setState(() => _style = v ?? _style),
              ),
              Text(
                'Estimated session allowance: \$${_budgetDollars.toStringAsFixed(2)}. Provider billing can exceed the estimate; OpenRouter transcription does not enforce a hard per-call spend cap.',
              ),
              Slider(
                value: _budgetDollars,
                min: 0.01,
                max: 1,
                divisions: 99,
                label: '\$${_budgetDollars.toStringAsFixed(2)}',
                onChanged: _busy
                    ? null
                    : (v) => setState(() => _budgetDollars = v),
              ),
              const Text(
                'Voice input and speech leave this device for OpenRouter. Speech turns are sent automatically to the current conversation.',
              ),
            ],
            if (_transcript != null) ...[
              const SizedBox(height: 12),
              SelectableText(_transcript!),
            ],
          ],
        ),
      ),
    ),
    actions: [
      TextButton(
        onPressed: () => _end(),
        child: Text(_session == null ? 'Cancel' : 'End voice'),
      ),
      if (_session == null)
        FilledButton(
          onPressed: _busy || _input == null || _output == null ? null : _start,
          child: Text(_busy ? 'Starting…' : 'Start listening'),
        ),
      if (_session != null)
        TextButton(
          onPressed: _interrupt,
          child: const Text('Interrupt speech'),
        ),
    ],
  );

  Widget _connection(
    String label,
    wire.Connection? selected,
    ValueChanged<wire.Connection?> onChanged,
  ) => DropdownButtonFormField<wire.Connection>(
    value: selected,
    decoration: InputDecoration(labelText: label),
    items: [
      for (final c in _connections)
        DropdownMenuItem(
          value: c,
          child: Text('${c.accountIdentity} · ${c.validationState.name}'),
        ),
    ],
    onChanged: _busy ? null : onChanged,
  );
}
