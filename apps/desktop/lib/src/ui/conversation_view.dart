// ConversationView: requests, useful summaries, exact decisions and results.
// Conversation prose stays separate from disposition records: messages are
// text, and the decision card renders the controller's review record.

import 'package:flutter/material.dart';

import '../state/snapshot.dart';
import '../state/view_state.dart';
import '../state/workspace_controller.dart';
import 'action_review_dialog.dart';
import 'message_composer.dart';
import 'theme.dart';
import 'widgets.dart';

class ConversationView extends StatelessWidget {
  const ConversationView({
    super.key,
    required this.controller,
    this.onOpenNavigation,
  });

  final WorkspaceController controller;

  /// Set when the sidebar is an overlay and needs a button to open it.
  final VoidCallback? onOpenNavigation;

  @override
  Widget build(BuildContext context) {
    final worker = controller.selectedWorker == null
        ? null
        : controller.snapshot.worker(controller.selectedWorker!);
    final conversation = controller.selectedConversation;
    final title = worker?.name ?? conversation?.title ?? 'Zatiti';

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _Header(
          controller: controller,
          worker: worker,
          title: title,
          onOpenNavigation: onOpenNavigation,
        ),
        if (controller.showsSavedView) _OfflineBanner(controller: controller),
        Expanded(
          child: Semantics(
            container: true,
            label: 'Conversation with $title',
            explicitChildNodes: true,
            child: _Thread(
              controller: controller,
              worker: worker,
              conversation: conversation,
            ),
          ),
        ),
        Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: Measure.conversation),
            child: Padding(
              padding: const EdgeInsets.fromLTRB(
                Space.xl,
                0,
                Space.xl,
                Space.xl,
              ),
              child: conversation == null
                  ? Notice(
                      worker == null
                          ? 'Choose a conversation to begin.'
                          : 'There is no conversation with ${worker.name} '
                                'yet. Starting one from this app is not '
                                'available in this version.',
                    )
                  : MessageComposer(
                      controller: controller,
                      conversationId: conversation.id,
                      recipientName: title,
                      contextLine: worker == null
                          ? 'Talking to everyone in this group.'
                          : worker.parentId == null
                          ? '${worker.name} will coordinate the right people.'
                          : 'Talking directly to ${worker.name}.',
                    ),
            ),
          ),
        ),
      ],
    );
  }
}

class _Header extends StatelessWidget {
  const _Header({
    required this.controller,
    required this.worker,
    required this.title,
    this.onOpenNavigation,
  });

  final WorkspaceController controller;
  final WorkerEntry? worker;
  final String title;
  final VoidCallback? onOpenNavigation;

  @override
  Widget build(BuildContext context) {
    final p = ZatitiPalette.of(context);
    final text = Theme.of(context).textTheme;
    final w = worker;
    final compact = MediaQuery.sizeOf(context).width < 640;
    final status = switch (controller.connection) {
      ConnectionPhase.online => 'Available',
      ConnectionPhase.connecting => 'Connecting…',
      ConnectionPhase.reconnecting => 'Reconnecting…',
      ConnectionPhase.offline => 'Saved view',
      ConnectionPhase.unsupported => 'Unsupported controller',
    };
    return Container(
      constraints: const BoxConstraints(minHeight: 76),
      padding: const EdgeInsets.symmetric(
        horizontal: Space.xl,
        vertical: Space.md,
      ),
      decoration: BoxDecoration(
        border: Border(bottom: BorderSide(color: p.line)),
      ),
      child: Row(
        children: [
          if (onOpenNavigation != null)
            Padding(
              padding: const EdgeInsets.only(right: Space.sm),
              child: IconButton(
                tooltip: 'Open conversations',
                onPressed: onOpenNavigation,
                icon: const Icon(Icons.menu),
              ),
            ),
          if (w != null) ...[
            WorkerAvatar(
              name: w.name,
              identity: w.id.value,
              root: w.parentId == null,
            ),
            const SizedBox(width: Space.md),
          ],
          Expanded(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Semantics(
                  header: true,
                  child: Text(
                    title,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: text.titleMedium,
                  ),
                ),
                if (w != null)
                  Text(
                    '${w.ancestry} › ${w.role}',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: text.bodySmall,
                  ),
              ],
            ),
          ),
          const SizedBox(width: Space.md),
          Semantics(
            liveRegion: true,
            label: compact ? status : null,
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Container(
                  width: 6,
                  height: 6,
                  decoration: BoxDecoration(
                    shape: BoxShape.circle,
                    color: controller.isOnline ? p.accent : p.amber,
                  ),
                ),
                if (!compact) ...[
                  const SizedBox(width: 6),
                  Text(
                    status,
                    key: const ValueKey('connection-status'),
                    style: text.bodySmall,
                  ),
                ],
              ],
            ),
          ),
          if (w != null) ...[
            const SizedBox(width: Space.lg),
            if (compact)
              IconButton(
                key: const ValueKey('details-button'),
                tooltip: 'Details',
                onPressed: controller.detailsOpen
                    ? controller.closeDetails
                    : controller.openDetails,
                icon: const Icon(Icons.view_sidebar_outlined),
              )
            else
              OutlinedButton.icon(
                key: const ValueKey('details-button'),
                onPressed: controller.detailsOpen
                    ? controller.closeDetails
                    : controller.openDetails,
                icon: const Icon(Icons.view_sidebar_outlined, size: 16),
                label: const Text('Details'),
              ),
          ],
        ],
      ),
    );
  }
}

class _OfflineBanner extends StatelessWidget {
  const _OfflineBanner({required this.controller});
  final WorkspaceController controller;

  @override
  Widget build(BuildContext context) {
    final reconnecting = controller.connection == ConnectionPhase.reconnecting;
    final unsupported = controller.connection == ConnectionPhase.unsupported;
    return Padding(
      padding: const EdgeInsets.fromLTRB(Space.xl, Space.md, Space.xl, 0),
      child: Row(
        children: [
          Expanded(
            child: Notice(
              key: const ValueKey('offline-banner'),
              unsupported
                  ? controller.connectionMessage ??
                        'This controller does not serve what this app needs.'
                  : reconnecting
                  ? 'Reconnecting. This is a saved view. Unsent messages stay '
                        'unsent until you send them.'
                  : 'Offline. This is a saved view and may be out of date. '
                        'Decisions are disabled and messages stay unsent.',
              icon: Icons.cloud_off_outlined,
            ),
          ),
          const SizedBox(width: Space.md),
          OutlinedButton(
            key: const ValueKey('reconnect'),
            onPressed: reconnecting ? null : controller.reconnect,
            child: Text(reconnecting ? 'Reconnecting…' : 'Reconnect'),
          ),
        ],
      ),
    );
  }
}

class _Thread extends StatefulWidget {
  const _Thread({
    required this.controller,
    required this.worker,
    required this.conversation,
  });

  final WorkspaceController controller;
  final WorkerEntry? worker;
  final ConversationEntry? conversation;

  @override
  State<_Thread> createState() => _ThreadState();
}

class _ThreadState extends State<_Thread> {
  final ScrollController _scroll = ScrollController();
  String? _shownConversation;
  int _shownItems = 0;

  @override
  void dispose() {
    _scroll.dispose();
    super.dispose();
  }

  /// Follows new messages in the open conversation. Switching conversations
  /// restores that conversation's own scroll position instead.
  void _followNewItems(String? conversation, int items) {
    final grew = conversation == _shownConversation && items > _shownItems;
    _shownConversation = conversation;
    _shownItems = items;
    if (!grew) return;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_scroll.hasClients) {
        _scroll.jumpTo(_scroll.position.maxScrollExtent);
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final controller = widget.controller;
    final text = Theme.of(context).textTheme;
    final c = widget.conversation;
    final w = widget.worker;
    final decisions = w == null
        ? const <DecisionView>[]
        : controller.decisionsFor(w.id);
    final proposals = w == null
        ? const <ProposalEntry>[]
        : controller.proposalsFor(w.id);
    final outgoing = c == null
        ? const <OutgoingMessage>[]
        : controller.outgoingFor(c.id);
    final empty =
        (c == null || c.messages.isEmpty) &&
        decisions.isEmpty &&
        outgoing.isEmpty;

    _followNewItems(c?.id.value, (c?.messages.length ?? 0) + outgoing.length);

    return ListView(
      // Scroll position is kept per conversation.
      key: PageStorageKey('thread-${c?.id.value ?? w?.id.value ?? 'none'}'),
      controller: _scroll,
      padding: const EdgeInsets.symmetric(vertical: Space.xl),
      children: [
        for (final child in <Widget>[
          for (final n in controller.prerequisitesFor(null))
            MiniCard(
              title: n.title,
              status: 'Needs setup',
              statusIcon: Icons.info_outline,
              statusIsDecision: true,
              body: n.message,
              footnotes: [if (n.setupPath != null) 'Set up: ${n.setupPath}'],
            ),
          if (c?.historyNotice != null)
            Padding(
              padding: const EdgeInsets.only(bottom: Space.lg),
              child: Text(c!.historyNotice!, style: text.bodySmall),
            ),
          if (empty && controller.connection == ConnectionPhase.connecting)
            const EmptyState('Connecting to your controller…')
          else if (empty)
            EmptyState(
              w == null
                  ? 'Nothing here yet.'
                  : 'Describe what you’d like done, in ordinary language. '
                        'For example: “Review our website’s first-time '
                        'experience” or “Keep our expenses organized every '
                        'Friday.”',
            ),
          for (final m in c?.messages ?? const <ChatMessage>[])
            _MessageBubble(message: m),
          // A worker turn's other six states — acknowledgement, refusal,
          // review-waiting and blocked-setup — already render as their own
          // dedicated card below (an outgoing bubble, a decision card, a
          // prerequisite notice). This is the one state with no existing
          // card of its own: delivered, and nothing has come back yet.
          if (c != null &&
              outgoing.isEmpty &&
              controller.turnStatusFor(c.id, worker: w?.id) ==
                  TurnStatus.waitingForReply)
            Padding(
              padding: const EdgeInsets.only(bottom: Space.lg),
              child: Text(
                TurnStatus.waitingForReply.label,
                key: const ValueKey('turn-waiting-for-reply'),
                style: text.bodySmall,
              ),
            ),
          for (final pr in proposals)
            MiniCard(
              title: pr.title,
              status: 'Proposed · not active',
              statusIcon: Icons.edit_note,
              body: pr.summary,
            ),
          for (final d in decisions)
            DecisionCard(controller: controller, view: d),
          for (final o in outgoing)
            _OutgoingBubble(controller: controller, message: o),
        ])
          Center(
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: Measure.conversation),
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: Space.xl),
                child: child,
              ),
            ),
          ),
      ],
    );
  }
}

class _MessageBubble extends StatelessWidget {
  const _MessageBubble({required this.message});
  final ChatMessage message;

  @override
  Widget build(BuildContext context) {
    final p = ZatitiPalette.of(context);
    final text = Theme.of(context).textTheme;
    if (message.fromUser) {
      return Align(
        alignment: Alignment.centerRight,
        child: Semantics(
          label: 'You said',
          child: Container(
            margin: const EdgeInsets.only(bottom: Space.xl, left: 64),
            padding: const EdgeInsets.symmetric(
              horizontal: 18,
              vertical: Space.md,
            ),
            decoration: BoxDecoration(
              color: Theme.of(context).brightness == Brightness.dark
                  ? const Color(0xFF5A5A5A)
                  : p.card,
              borderRadius: BorderRadius.circular(22),
            ),
            child: SelectableText(message.body, style: text.bodyLarge),
          ),
        ),
      );
    }
    return Padding(
      padding: const EdgeInsets.only(bottom: Space.xl),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            '${message.senderName} · ${_clock(message.at)}',
            style: text.bodySmall,
          ),
          const SizedBox(height: Space.sm),
          Container(
            padding: const EdgeInsets.symmetric(
              horizontal: 18,
              vertical: Space.md,
            ),
            decoration: BoxDecoration(
              color: p.card,
              borderRadius: BorderRadius.circular(22),
            ),
            child: SelectableText(message.body, style: text.bodyLarge),
          ),
        ],
      ),
    );
  }
}

String _clock(DateTime utc) {
  final t = utc.toLocal();
  return '${t.hour.toString().padLeft(2, '0')}:'
      '${t.minute.toString().padLeft(2, '0')}';
}

class _OutgoingBubble extends StatelessWidget {
  const _OutgoingBubble({required this.controller, required this.message});
  final WorkspaceController controller;
  final OutgoingMessage message;

  @override
  Widget build(BuildContext context) {
    final p = ZatitiPalette.of(context);
    final text = Theme.of(context).textTheme;
    final unsent = message.phase == OutgoingPhase.unsentDraft;
    final removable = unsent || message.phase == OutgoingPhase.refused;
    return Align(
      alignment: Alignment.centerRight,
      child: Padding(
        padding: const EdgeInsets.only(bottom: Space.xl, left: 64),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.end,
          children: [
            Container(
              padding: const EdgeInsets.symmetric(
                horizontal: 18,
                vertical: Space.md,
              ),
              decoration: BoxDecoration(
                border: Border.all(color: p.decisionLine),
                borderRadius: BorderRadius.circular(22),
              ),
              child: Text(message.body, style: text.bodyMedium),
            ),
            const SizedBox(height: 6),
            Semantics(
              liveRegion: true,
              child: Text(
                [message.label, ?message.note].join(' · '),
                key: ValueKey('outgoing-label-${message.localId}'),
                style: text.bodySmall!.copyWith(color: p.amber),
              ),
            ),
            if (removable ||
                message.phase == OutgoingPhase.acknowledgmentUnknown)
              Padding(
                padding: const EdgeInsets.only(top: Space.sm),
                child: Wrap(
                  spacing: Space.sm,
                  children: [
                    if (message.phase == OutgoingPhase.acknowledgmentUnknown)
                      OutlinedButton(
                        onPressed: controller.isOnline
                            ? () => controller.checkMessage(message.localId)
                            : null,
                        child: const Text('Check again'),
                      ),
                    if (removable)
                      TextButton(
                        onPressed: () =>
                            controller.discardUnsent(message.localId),
                        child: const Text('Discard'),
                      ),
                    if (unsent)
                      OutlinedButton(
                        key: ValueKey('send-unsent-${message.localId}'),
                        onPressed: controller.isOnline
                            ? () => controller.sendUnsent(message.localId)
                            : null,
                        child: const Text('Send now'),
                      ),
                  ],
                ),
              ),
          ],
        ),
      ),
    );
  }
}

/// The decision card. It renders the same [DecisionView] as the Needs you
/// list, the Work tab and the review dialog.
class DecisionCard extends StatelessWidget {
  const DecisionCard({super.key, required this.controller, required this.view});

  final WorkspaceController controller;
  final DecisionView view;

  @override
  Widget build(BuildContext context) {
    final p = ZatitiPalette.of(context);
    final text = Theme.of(context).textTheme;
    final review = view.review;
    final open = view.phase.needsYou;
    return Semantics(
      container: true,
      label: open ? 'Decision needed' : 'Decision record',
      explicitChildNodes: true,
      child: Container(
        key: ValueKey('decision-card-${review.id.value}'),
        margin: const EdgeInsets.only(bottom: Space.xl),
        decoration: BoxDecoration(
          color: p.card,
          border: Border.all(color: open ? p.decisionLine : p.line),
          borderRadius: BorderRadius.circular(Measure.cardRadius + 1),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.all(Space.xl),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Icon(
                        open ? Icons.shield_outlined : Icons.check,
                        size: 15,
                        color: open ? p.amber : p.accent,
                      ),
                      const SizedBox(width: 6),
                      Expanded(
                        child: Text(
                          view.phase.label.toUpperCase(),
                          key: ValueKey('decision-phase-${review.id.value}'),
                          style: text.labelSmall!.copyWith(
                            color: open ? p.amber : p.accent,
                          ),
                        ),
                      ),
                    ],
                  ),
                  const SizedBox(height: Space.md),
                  Text(review.title, style: text.titleLarge),
                  const SizedBox(height: Space.sm),
                  Text(
                    review.consequence,
                    style: text.bodyMedium!.copyWith(color: p.muted),
                  ),
                  const SizedBox(height: Space.md),
                  Wrap(
                    spacing: Space.lg,
                    runSpacing: Space.sm,
                    children: [
                      _Meta(Icons.place_outlined, review.destination),
                      _Meta(Icons.shield_outlined, 'Up to ${review.costBound}'),
                    ],
                  ),
                ],
              ),
            ),
            Divider(color: p.line),
            Padding(
              padding: const EdgeInsets.symmetric(
                horizontal: Space.xl,
                vertical: Space.md,
              ),
              child: Wrap(
                alignment: WrapAlignment.spaceBetween,
                crossAxisAlignment: WrapCrossAlignment.center,
                spacing: Space.md,
                runSpacing: Space.sm,
                children: [
                  Text(
                    [
                      view.proposerName,
                      if (view.ancestry.isNotEmpty) view.ancestry,
                    ].join(' · '),
                    style: text.bodySmall,
                  ),
                  FilledButton.icon(
                    key: ValueKey('open-review-${review.id.value}'),
                    onPressed: () =>
                        showActionReviewDialog(context, controller, review.id),
                    iconAlignment: IconAlignment.end,
                    icon: const Icon(Icons.arrow_forward, size: 16),
                    label: Text(open ? 'Review & decide' : 'View decision'),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _Meta extends StatelessWidget {
  const _Meta(this.icon, this.label);
  final IconData icon;
  final String label;

  @override
  Widget build(BuildContext context) => Row(
    mainAxisSize: MainAxisSize.min,
    children: [
      Icon(icon, size: 15),
      const SizedBox(width: 6),
      Flexible(
        child: Text(label, style: Theme.of(context).textTheme.bodySmall),
      ),
    ],
  );
}
