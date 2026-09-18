// ActionReviewDialog: the exact review. The consequence, destination, actual
// content, cost boundary and expiry are in plain view. Operation identity,
// versions and digests sit in the collapsed evidence section.
//
// The dialog binds a decision to the version and digest it rendered. When the
// review changes underneath it, the decision is disabled until the person
// looks at the new version.

import 'package:flutter/material.dart';

import '../state/snapshot.dart';
import '../state/view_state.dart';
import '../state/workspace_controller.dart';
import '../state/workspace_source.dart';
import 'theme.dart';
import 'widgets.dart';

Future<void> showActionReviewDialog(
  BuildContext context,
  WorkspaceController controller,
  ReviewId id,
) => showDialog<void>(
  context: context,
  barrierLabel: 'Close review',
  builder: (_) => ActionReviewDialog(controller: controller, reviewId: id),
);

class ActionReviewDialog extends StatefulWidget {
  const ActionReviewDialog({
    super.key,
    required this.controller,
    required this.reviewId,
  });

  final WorkspaceController controller;
  final ReviewId reviewId;

  @override
  State<ActionReviewDialog> createState() => _ActionReviewDialogState();
}

class _ActionReviewDialogState extends State<ActionReviewDialog> {
  int? _seenVersion;
  String? _seenDigest;

  @override
  void initState() {
    super.initState();
    _see();
  }

  void _see() {
    final review = widget.controller.decision(widget.reviewId)?.review;
    _seenVersion = review?.version;
    _seenDigest = review?.actionDigest;
  }

  @override
  Widget build(BuildContext context) {
    return ListenableBuilder(
      listenable: widget.controller,
      builder: (context, _) {
        final view = widget.controller.decision(widget.reviewId);
        if (view == null) {
          return const AlertDialog(
            content: Text('This request is no longer available.'),
          );
        }
        return _build(context, view);
      },
    );
  }

  Widget _build(BuildContext context, DecisionView view) {
    final p = ZatitiPalette.of(context);
    final text = Theme.of(context).textTheme;
    final review = view.review;
    final changedUnderneath =
        review.version != _seenVersion || review.actionDigest != _seenDigest;
    final open = view.phase.needsYou;
    final canDecide = view.canDecide && !changedUnderneath;

    return Dialog(
      insetPadding: const EdgeInsets.all(Space.xl),
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 660),
        child: FocusTraversalGroup(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(28, Space.xl, Space.lg, 0),
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Semantics(
                            header: true,
                            namesRoute: true,
                            child: Text(
                              open
                                  ? 'Review: ${review.title}'
                                  : 'Your decision',
                              style: text.titleLarge,
                            ),
                          ),
                          const SizedBox(height: 6),
                          Text(
                            [
                              view.proposerName,
                              if (view.ancestry.isNotEmpty) view.ancestry,
                            ].join(' · '),
                            style: text.bodySmall!.copyWith(fontSize: 13),
                          ),
                        ],
                      ),
                    ),
                    IconButton(
                      tooltip: 'Close review',
                      onPressed: () => Navigator.of(context).pop(),
                      icon: const Icon(Icons.close),
                    ),
                  ],
                ),
              ),
              Flexible(
                child: SingleChildScrollView(
                  padding: const EdgeInsets.fromLTRB(
                    28,
                    Space.lg,
                    28,
                    Space.lg,
                  ),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      if (widget.controller.showsSavedView)
                        const Padding(
                          padding: EdgeInsets.only(bottom: Space.md),
                          child: Notice(
                            'Offline. Reconnect to check the current request '
                            'before deciding.',
                            icon: Icons.cloud_off_outlined,
                          ),
                        ),
                      Notice(
                        key: const ValueKey('review-consequence'),
                        open ? review.consequence : view.phase.label,
                        icon: open
                            ? Icons.shield_outlined
                            : Icons.check_circle_outline,
                      ),
                      if (open && view.phase != ReviewPhase.awaitingDecision)
                        Padding(
                          padding: const EdgeInsets.only(top: Space.md),
                          child: Notice(view.phase.label),
                        ),
                      if (view.note != null)
                        Padding(
                          padding: const EdgeInsets.only(top: Space.md),
                          child: Text(view.note!, style: text.bodySmall),
                        ),
                      const SizedBox(height: Space.xl),
                      _Facts([
                        ('Action', review.action),
                        ('Acting as', review.accountIdentity),
                        ('Destination', review.destination),
                        ('Maximum reserved cost', review.costBound),
                        ('Approval applies to', 'This exact content only'),
                        ('Expires', _when(review.expiresAt)),
                        for (final e in review.parameters.entries)
                          (e.key, e.value),
                      ]),
                      const SizedBox(height: Space.lg),
                      for (final part in review.content)
                        _ContentPreview(part: part),
                      if (review.content.isEmpty)
                        const EmptyState(
                          'This action carries no separate content. The '
                          'facts above are the whole request.',
                        ),
                      const SizedBox(height: Space.sm),
                      Divider(color: p.line),
                      Theme(
                        data: Theme.of(
                          context,
                        ).copyWith(dividerColor: Colors.transparent),
                        child: ExpansionTile(
                          key: const ValueKey('review-evidence'),
                          tilePadding: EdgeInsets.zero,
                          childrenPadding: const EdgeInsets.only(
                            bottom: Space.md,
                          ),
                          expandedCrossAxisAlignment: CrossAxisAlignment.start,
                          title: Text(
                            'Why this needs you · rules & evidence',
                            style: text.bodySmall!.copyWith(fontSize: 13),
                          ),
                          children: [
                            for (final line in review.evidence)
                              Padding(
                                padding: const EdgeInsets.only(bottom: 6),
                                child: Text('• $line', style: text.bodySmall),
                              ),
                          ],
                        ),
                      ),
                    ],
                  ),
                ),
              ),
              Divider(color: p.line),
              Padding(
                padding: const EdgeInsets.fromLTRB(28, Space.lg, 28, Space.xl),
                child: _footer(context, view, canDecide, changedUnderneath),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _footer(
    BuildContext context,
    DecisionView view,
    bool canDecide,
    bool changedUnderneath,
  ) {
    final text = Theme.of(context).textTheme;
    final controller = widget.controller;
    final id = widget.reviewId;

    if (!view.phase.needsYou) {
      return Align(
        alignment: Alignment.centerRight,
        child: FilledButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Done'),
        ),
      );
    }

    final String hint;
    Widget? recover;
    if (changedUnderneath || view.phase == ReviewPhase.stale) {
      hint = 'This request changed. Read the new version, then decide again.';
      recover = OutlinedButton(
        key: const ValueKey('review-refresh'),
        onPressed: controller.isOnline
            ? () async {
                await controller.refreshReview(id);
                if (mounted) setState(_see);
              }
            : null,
        child: const Text('Show the new version'),
      );
    } else if (view.phase == ReviewPhase.acknowledgmentUnknown) {
      hint = 'Checking whether your decision was received. Nothing is resent.';
      recover = OutlinedButton(
        key: const ValueKey('review-check'),
        onPressed: controller.isOnline
            ? () => controller.checkDecision(id)
            : null,
        child: const Text('Check again'),
      );
    } else {
      hint =
          view.blockedReason ??
          'Only this request. Your standing rules stay the same.';
    }

    void decide(DecisionChoice choice) => controller.decide(
      id,
      choice,
      seenVersion: _seenVersion ?? -1,
      seenDigest: _seenDigest ?? '',
    );

    return Wrap(
      alignment: WrapAlignment.end,
      crossAxisAlignment: WrapCrossAlignment.center,
      spacing: Space.md,
      runSpacing: Space.md,
      children: [
        ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 300),
          child: Text(hint, style: text.bodySmall),
        ),
        ?recover,
        OutlinedButton(
          key: const ValueKey('review-decline'),
          onPressed: canDecide ? () => decide(DecisionChoice.decline) : null,
          child: const Text('Decline'),
        ),
        FilledButton(
          key: const ValueKey('review-approve'),
          onPressed: canDecide ? () => decide(DecisionChoice.approve) : null,
          child: Text(
            view.phase == ReviewPhase.submitting
                ? 'Sending…'
                : 'Approve: ${view.review.title}',
          ),
        ),
      ],
    );
  }
}

String _when(DateTime utc) {
  final t = utc.toLocal();
  String two(int v) => v.toString().padLeft(2, '0');
  return '${t.year}-${two(t.month)}-${two(t.day)} '
      '${two(t.hour)}:${two(t.minute)}';
}

class _Facts extends StatelessWidget {
  const _Facts(this.facts);
  final List<(String, String)> facts;

  @override
  Widget build(BuildContext context) {
    final text = Theme.of(context).textTheme;
    return LayoutBuilder(
      builder: (context, box) {
        final columns = box.maxWidth > 480 ? 2 : 1;
        final width = (box.maxWidth - (columns - 1) * Space.xl) / columns;
        return Wrap(
          spacing: Space.xl,
          runSpacing: Space.lg,
          children: [
            for (final (label, value) in facts)
              SizedBox(
                width: width,
                child: MergeSemantics(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(label, style: text.bodySmall),
                      const SizedBox(height: 4),
                      Text(value, style: text.bodyMedium),
                    ],
                  ),
                ),
              ),
          ],
        );
      },
    );
  }
}

class _ContentPreview extends StatelessWidget {
  const _ContentPreview({required this.part});
  final ReviewContentPart part;

  @override
  Widget build(BuildContext context) {
    final p = ZatitiPalette.of(context);
    final text = Theme.of(context).textTheme;
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.only(bottom: Space.md),
      padding: const EdgeInsets.all(Space.lg),
      decoration: BoxDecoration(
        color: p.canvas,
        border: Border.all(color: p.line),
        borderRadius: BorderRadius.circular(Measure.cardRadius),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            '${part.label.toUpperCase()} · EXACT CONTENT',
            style: text.labelSmall,
          ),
          const SizedBox(height: Space.sm),
          if (part.text != null)
            SelectableText(part.text!, style: text.bodyMedium)
          else
            Notice(
              part.unavailableReason ?? 'The exact content cannot be shown.',
              icon: Icons.block_outlined,
            ),
        ],
      ),
    );
  }
}
