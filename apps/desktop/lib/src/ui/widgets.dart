// Small shared widgets.

import 'package:flutter/material.dart';

import 'theme.dart';

/// Worker initials: a compact navigation aid, never the only indication of
/// organization or status, and hidden from assistive technology.
class WorkerAvatar extends StatelessWidget {
  const WorkerAvatar({
    super.key,
    required this.name,
    required this.identity,
    this.root = false,
    this.size = 36,
  });

  final String name;
  final String identity;
  final bool root;
  final double size;

  @override
  Widget build(BuildContext context) {
    final (background, foreground) = ZatitiPalette.of(
      context,
    ).avatars[toneFor(identity, root: root)]!;
    return ExcludeSemantics(
      child: Container(
        width: size,
        height: size,
        alignment: Alignment.center,
        decoration: BoxDecoration(
          color: background,
          borderRadius: BorderRadius.circular(size / 3),
        ),
        child: Text(
          name.isEmpty ? '?' : name.characters.first.toUpperCase(),
          textScaler: TextScaler.noScaling,
          style: TextStyle(
            color: foreground,
            fontSize: size * 0.42,
            fontWeight: FontWeight.w500,
          ),
        ),
      ),
    );
  }
}

/// An amber notice: a consequence, a saved-view label, a blocked reason.
class Notice extends StatelessWidget {
  const Notice(this.text, {super.key, this.icon});

  final String text;
  final IconData? icon;

  @override
  Widget build(BuildContext context) {
    final p = ZatitiPalette.of(context);
    return Semantics(
      container: true,
      liveRegion: true,
      child: Container(
        width: double.infinity,
        padding: const EdgeInsets.symmetric(
          horizontal: Space.lg,
          vertical: Space.md,
        ),
        decoration: BoxDecoration(
          color: p.amberWash,
          border: Border.all(color: p.decisionLine),
          borderRadius: BorderRadius.circular(Measure.controlRadius + 2),
        ),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (icon != null) ...[
              Icon(icon, size: 16, color: p.amber),
              const SizedBox(width: Space.sm),
            ],
            Expanded(
              child: Text(
                text,
                style: Theme.of(
                  context,
                ).textTheme.bodyMedium!.copyWith(color: p.amber),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// A detail card: status line, title, description, actions.
class MiniCard extends StatelessWidget {
  const MiniCard({
    super.key,
    required this.title,
    this.status,
    this.statusIcon,
    this.statusIsDecision = false,
    this.body,
    this.footnotes = const [],
    this.actions = const [],
  });

  final String title;
  final String? status;
  final IconData? statusIcon;
  final bool statusIsDecision;
  final String? body;
  final List<String> footnotes;
  final List<Widget> actions;

  @override
  Widget build(BuildContext context) {
    final p = ZatitiPalette.of(context);
    final text = Theme.of(context).textTheme;
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.only(bottom: Space.md),
      padding: const EdgeInsets.all(Space.lg),
      decoration: BoxDecoration(
        color: p.card,
        border: Border.all(color: p.line),
        borderRadius: BorderRadius.circular(Measure.cardRadius),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (status != null)
            Padding(
              padding: const EdgeInsets.only(bottom: Space.sm),
              child: Row(
                children: [
                  if (statusIcon != null) ...[
                    Icon(
                      statusIcon,
                      size: 14,
                      color: statusIsDecision ? p.amber : p.accent,
                    ),
                    const SizedBox(width: 6),
                  ],
                  Expanded(
                    child: Text(
                      status!,
                      style: text.labelMedium!.copyWith(
                        color: statusIsDecision ? p.amber : p.accent,
                      ),
                    ),
                  ),
                ],
              ),
            ),
          Text(title, style: text.titleSmall),
          if (body != null && body!.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(top: 6),
              child: Text(body!, style: text.bodySmall!.copyWith(fontSize: 13)),
            ),
          for (final f in footnotes)
            Padding(
              padding: const EdgeInsets.only(top: 6),
              child: Text(f, style: text.bodySmall),
            ),
          if (actions.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(top: Space.md),
              child: Wrap(
                spacing: Space.sm,
                runSpacing: Space.sm,
                children: actions,
              ),
            ),
        ],
      ),
    );
  }
}

/// The designed empty state.
class EmptyState extends StatelessWidget {
  const EmptyState(this.text, {super.key});
  final String text;

  @override
  Widget build(BuildContext context) {
    final p = ZatitiPalette.of(context);
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(Space.xl),
      decoration: BoxDecoration(
        border: Border.all(color: p.line),
        borderRadius: BorderRadius.circular(Measure.cardRadius),
      ),
      child: Text(
        text,
        textAlign: TextAlign.center,
        style: Theme.of(context).textTheme.bodySmall,
      ),
    );
  }
}

class SectionLabel extends StatelessWidget {
  const SectionLabel(this.text, {super.key});
  final String text;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.only(top: Space.sm, bottom: Space.md),
    child: Semantics(
      header: true,
      child: Text(
        text.toUpperCase(),
        style: Theme.of(context).textTheme.labelSmall,
      ),
    ),
  );
}
