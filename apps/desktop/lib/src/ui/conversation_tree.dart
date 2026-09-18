// OrganizationConversationTree: the nested sidebar. A row opens that worker's
// conversation. Its separate chevron expands or collapses descendants without
// changing conversations. Order is stable organization order.

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../state/snapshot.dart';
import '../state/view_state.dart';
import '../state/workspace_controller.dart';
import 'theme.dart';
import 'widgets.dart';

class OrganizationConversationTree extends StatelessWidget {
  const OrganizationConversationTree({
    super.key,
    required this.controller,
    this.onOpened,
  });

  final WorkspaceController controller;

  /// Called after a conversation opens, so an overlay sidebar can close.
  final VoidCallback? onOpened;

  @override
  Widget build(BuildContext context) {
    final rows = controller.tree;
    final groups = controller.groups;
    return Semantics(
      label: 'Conversations by organization',
      explicitChildNodes: true,
      child: FocusTraversalGroup(
        child: ListView(
          padding: const EdgeInsets.symmetric(horizontal: Space.md),
          children: [
            for (var i = 0; i < rows.length; i++)
              _TreeRow(
                key: ValueKey('tree-${rows[i].worker.id.value}'),
                node: rows[i],
                controller: controller,
                onOpened: onOpened,
              ),
            if (groups.isNotEmpty) ...[
              const Padding(
                padding: EdgeInsets.fromLTRB(Space.md, Space.lg, 0, 0),
                child: SectionLabel('Group chats'),
              ),
              for (var i = 0; i < groups.length; i++)
                _GroupRow(
                  group: groups[i],
                  selected: controller.selectedGroup == groups[i].id,
                  onTap: () {
                    controller.selectGroup(groups[i].id);
                    onOpened?.call();
                  },
                ),
            ],
          ],
        ),
      ),
    );
  }
}

class _TreeRow extends StatelessWidget {
  const _TreeRow({
    super.key,
    required this.node,
    required this.controller,
    this.onOpened,
  });

  final TreeNodeView node;
  final WorkspaceController controller;
  final VoidCallback? onOpened;

  @override
  Widget build(BuildContext context) {
    final p = ZatitiPalette.of(context);
    final text = Theme.of(context).textTheme;
    final w = node.worker;

    void open() {
      controller.selectWorker(w.id);
      onOpened?.call();
    }

    final scopeLine = node.pinned
        ? '${w.organizationPath.lastOrNull ?? ''} · ${w.role}'
        : w.isOrganizationChief
        ? '${w.organizationPath.lastOrNull ?? ''} · Chief'
        : w.organizationPath.skip(1).join(' › ');

    return Padding(
      padding: EdgeInsets.only(left: node.depth * 17.0, top: 3, bottom: 3),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          SizedBox(
            width: 28,
            child: node.hasChildren
                ? _Disclosure(
                    key: ValueKey('toggle-${w.id.value}'),
                    name: w.name,
                    expanded: node.expanded,
                    onToggle: () => controller.toggleExpanded(w.id),
                  )
                : null,
          ),
          Expanded(
            child: CallbackShortcuts(
              bindings: {
                const SingleActivator(LogicalKeyboardKey.arrowRight): () {
                  if (node.hasChildren) {
                    controller.setExpanded(w.id, expanded: true);
                  }
                },
                const SingleActivator(LogicalKeyboardKey.arrowLeft): () {
                  if (node.hasChildren) {
                    controller.setExpanded(w.id, expanded: false);
                  }
                },
                const SingleActivator(LogicalKeyboardKey.arrowDown): () =>
                    FocusScope.of(
                      context,
                    ).focusInDirection(TraversalDirection.down),
                const SingleActivator(LogicalKeyboardKey.arrowUp): () =>
                    FocusScope.of(
                      context,
                    ).focusInDirection(TraversalDirection.up),
              },
              child: Semantics(
                label: node.semanticLabel,
                button: true,
                selected: node.selected,
                excludeSemantics: true,
                onTap: open,
                child: Material(
                  color: node.selected ? p.hover : Colors.transparent,
                  borderRadius: BorderRadius.circular(10),
                  child: InkWell(
                    key: ValueKey('open-${w.id.value}'),
                    borderRadius: BorderRadius.circular(10),
                    hoverColor: p.card,
                    onTap: open,
                    child: ConstrainedBox(
                      constraints: const BoxConstraints(minHeight: 48),
                      child: Padding(
                        padding: const EdgeInsets.symmetric(
                          horizontal: Space.md,
                          vertical: Space.md,
                        ),
                        child: Row(
                          children: [
                            WorkerAvatar(
                              name: w.name,
                              identity: w.id.value,
                              root: node.pinned,
                            ),
                            const SizedBox(width: Space.md),
                            Expanded(
                              child: Column(
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  Row(
                                    children: [
                                      Expanded(
                                        child: Text(
                                          w.name,
                                          maxLines: 1,
                                          overflow: TextOverflow.ellipsis,
                                          style: text.titleSmall,
                                        ),
                                      ),
                                      if (node.showsDecisionIndicator)
                                        Container(
                                          key: ValueKey(
                                            'decision-dot-${w.id.value}',
                                          ),
                                          width: 7,
                                          height: 7,
                                          decoration: BoxDecoration(
                                            color: p.amber,
                                            shape: BoxShape.circle,
                                          ),
                                        )
                                      else if (node.pinned)
                                        Icon(
                                          Icons.push_pin_outlined,
                                          size: 13,
                                          color: p.muted,
                                        ),
                                    ],
                                  ),
                                  if (w.preview.isNotEmpty)
                                    Padding(
                                      padding: const EdgeInsets.only(top: 4),
                                      child: Text(
                                        w.preview,
                                        maxLines: 1,
                                        overflow: TextOverflow.ellipsis,
                                        style: text.bodySmall,
                                      ),
                                    ),
                                  if (scopeLine.trim().isNotEmpty)
                                    Padding(
                                      padding: const EdgeInsets.only(top: 3),
                                      child: Text(
                                        scopeLine,
                                        maxLines: 1,
                                        overflow: TextOverflow.ellipsis,
                                        style: text.bodySmall!.copyWith(
                                          fontSize: 11,
                                          color: p.subtle,
                                        ),
                                      ),
                                    ),
                                ],
                              ),
                            ),
                          ],
                        ),
                      ),
                    ),
                  ),
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

/// The disclosure control: its own button with its own label and state.
class _Disclosure extends StatelessWidget {
  const _Disclosure({
    super.key,
    required this.name,
    required this.expanded,
    required this.onToggle,
  });

  final String name;
  final bool expanded;
  final VoidCallback onToggle;

  @override
  Widget build(BuildContext context) {
    final label = '${expanded ? 'Collapse' : 'Expand'} $name';
    return Semantics(
      label: label,
      button: true,
      expanded: expanded,
      excludeSemantics: true,
      onTap: onToggle,
      child: Tooltip(
        message: label,
        excludeFromSemantics: true,
        child: InkResponse(
          onTap: onToggle,
          radius: 18,
          child: SizedBox(
            width: 28,
            height: 44,
            child: AnimatedRotation(
              turns: expanded ? 0 : -0.25,
              duration: const Duration(milliseconds: 120),
              child: const Icon(Icons.expand_more, size: 17),
            ),
          ),
        ),
      ),
    );
  }
}

class _GroupRow extends StatelessWidget {
  const _GroupRow({
    required this.group,
    required this.selected,
    required this.onTap,
  });

  final ConversationEntry group;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final p = ZatitiPalette.of(context);
    return Padding(
      padding: const EdgeInsets.only(left: 28, top: 3, bottom: 3),
      child: Semantics(
        label: '${group.title}, group conversation',
        button: true,
        selected: selected,
        excludeSemantics: true,
        onTap: onTap,
        child: Material(
          color: selected ? p.hover : Colors.transparent,
          borderRadius: BorderRadius.circular(10),
          child: InkWell(
            borderRadius: BorderRadius.circular(10),
            onTap: onTap,
            child: ConstrainedBox(
              constraints: const BoxConstraints(minHeight: 48),
              child: Padding(
                padding: const EdgeInsets.all(Space.md),
                child: Row(
                  children: [
                    Icon(Icons.forum_outlined, size: 18, color: p.muted),
                    const SizedBox(width: Space.md),
                    Expanded(
                      child: Text(
                        group.title,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: Theme.of(context).textTheme.titleSmall,
                      ),
                    ),
                  ],
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}
