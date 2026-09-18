// MessageComposer: a per-conversation draft, sent only when the person sends
// it. There is no voice control and no file upload in this increment, so
// neither is shown.

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../state/snapshot.dart';
import '../state/workspace_controller.dart';
import 'theme.dart';

class MessageComposer extends StatefulWidget {
  MessageComposer({
    required this.controller,
    required this.conversationId,
    required this.recipientName,
    required this.contextLine,
  }) : super(key: ValueKey('composer-${conversationId.value}'));

  final WorkspaceController controller;
  final ConversationId conversationId;
  final String recipientName;
  final String contextLine;

  @override
  State<MessageComposer> createState() => _MessageComposerState();
}

class _MessageComposerState extends State<MessageComposer> {
  late final TextEditingController _text = TextEditingController(
    text: widget.controller.draftFor(widget.conversationId),
  );
  final FocusNode _focus = FocusNode(debugLabel: 'composer');

  @override
  void initState() {
    super.initState();
    _text.addListener(_keepDraft);
  }

  void _keepDraft() {
    widget.controller.setDraft(widget.conversationId, _text.text);
    setState(() {});
  }

  @override
  void dispose() {
    _text.removeListener(_keepDraft);
    _text.dispose();
    _focus.dispose();
    super.dispose();
  }

  void _send() {
    final body = _text.text;
    if (body.trim().isEmpty) return;
    // Clear the field without erasing the draft that is about to be sent.
    _text.removeListener(_keepDraft);
    _text.clear();
    _text.addListener(_keepDraft);
    widget.controller.setDraft(widget.conversationId, body);
    widget.controller.sendDraft(widget.conversationId);
    setState(() {});
    _focus.requestFocus();
  }

  @override
  Widget build(BuildContext context) {
    final p = ZatitiPalette.of(context);
    final text = Theme.of(context).textTheme;
    final offline = !widget.controller.isOnline;
    final canSend = _text.text.trim().isNotEmpty;
    return Container(
      decoration: BoxDecoration(
        color: p.card,
        border: Border.all(color: p.line),
        borderRadius: BorderRadius.circular(Measure.cardRadius + 1),
      ),
      padding: const EdgeInsets.fromLTRB(
        Space.lg,
        Space.sm,
        Space.sm,
        Space.sm,
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          CallbackShortcuts(
            bindings: {const SingleActivator(LogicalKeyboardKey.enter): _send},
            child: Semantics(
              label: 'Message ${widget.recipientName}',
              child: TextField(
                key: const ValueKey('composer-field'),
                controller: _text,
                focusNode: _focus,
                minLines: 1,
                maxLines: 6,
                textInputAction: TextInputAction.newline,
                style: text.bodyLarge,
                decoration: InputDecoration(
                  hintText:
                      'Ask ${widget.recipientName} to take something off your '
                      'plate…',
                  filled: false,
                  border: InputBorder.none,
                  enabledBorder: InputBorder.none,
                  focusedBorder: InputBorder.none,
                  contentPadding: const EdgeInsets.symmetric(
                    vertical: Space.md,
                  ),
                  isDense: true,
                ),
              ),
            ),
          ),
          Row(
            children: [
              Expanded(
                child: Text(
                  offline
                      ? 'Offline. Messages stay here, unsent, until you send '
                            'them yourself.'
                      : widget.contextLine,
                  style: text.bodySmall!.copyWith(
                    color: offline ? p.amber : p.subtle,
                  ),
                ),
              ),
              const SizedBox(width: Space.sm),
              Semantics(
                label: offline
                    ? 'Keep message as unsent draft'
                    : 'Send message to ${widget.recipientName}',
                button: true,
                enabled: canSend,
                excludeSemantics: true,
                child: IconButton.filled(
                  key: const ValueKey('composer-send'),
                  onPressed: canSend ? _send : null,
                  style: IconButton.styleFrom(
                    backgroundColor: p.accent,
                    foregroundColor: p.accentInk,
                    disabledBackgroundColor: p.hover,
                    shape: const CircleBorder(),
                    minimumSize: const Size(40, 40),
                  ),
                  icon: const Icon(Icons.arrow_upward, size: 18),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}
