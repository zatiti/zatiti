import 'dart:async';
import 'package:flutter/material.dart';
import '../state/live_source.dart';
import '../state/snapshot.dart';
import '../state/workspace_controller.dart';
import '../transport/reply_preview.dart';

class ReplyStreamView extends StatefulWidget {
  const ReplyStreamView({
    super.key,
    required this.controller,
    required this.conversation,
  });
  final WorkspaceController controller;
  final ConversationId conversation;
  @override
  State<ReplyStreamView> createState() => _ReplyStreamViewState();
}

class _ReplyStreamViewState extends State<ReplyStreamView> {
  StreamSubscription<List<ReplyPreview>>? _subscription;
  List<ReplyPreview> _previews = [];
  final Set<String> _committed = {};
  bool _disconnected = false;
  @override
  void initState() {
    super.initState();
    final source = widget.controller.source;
    if (source is LiveWorkspaceSource) {
      _subscription = source.api.client
          .watchReplies(widget.conversation.value)
          .listen(
            (items) {
              if (!mounted) return;
              for (final item in items) {
                if (item.state == 'committed' && _committed.add(item.id)) {
                  unawaited(
                    widget.controller.refreshStreamedMessages(
                      widget.conversation,
                    ),
                  );
                }
              }
              setState(() {
                // A committed preview stays visible: execution recorded the
                // reply, but that is not durable conversation history.
                _previews = items.where((p) => p.text.isNotEmpty).toList();
                _disconnected = false;
              });
            },
            onError: (Object _) {
              if (mounted) setState(() => _disconnected = true);
            },
          );
    }
  }

  @override
  void dispose() {
    unawaited(_subscription?.cancel());
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      for (final p in _previews)
        Padding(
          padding: const EdgeInsets.symmetric(vertical: 12),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              SelectableText(p.text),
              Text(
                replyPreviewLabel(p.state, disconnected: _disconnected),
                style: Theme.of(context).textTheme.bodySmall,
              ),
            ],
          ),
        ),
    ],
  );
}

/// The provisional status shown under a live reply preview. None of these
/// states claims the reply is in durable conversation history.
String replyPreviewLabel(String state, {bool disconnected = false}) {
  if (state == 'interrupted') return 'Reply interrupted — incomplete';
  if (disconnected) return 'Live preview disconnected — reconnecting';
  return switch (state) {
    'streaming' => 'Reply in progress…',
    'generated' => 'Reply generated — not yet recorded',
    'committed' => 'Reply recorded — live preview only, not in history yet',
    _ => 'Reply preview',
  };
}
