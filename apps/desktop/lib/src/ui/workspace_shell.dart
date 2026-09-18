// WorkspaceShell: the nested conversation sidebar, the conversation, and one
// optional details panel. Split layout on wide windows; dismissible overlays
// for navigation and details on narrow ones.

import 'package:flutter/material.dart';

import '../app/credential_store.dart';
import '../state/demo_source.dart';
import '../state/workspace_controller.dart';
import '../state/workspace_source.dart';
import 'action_review_dialog.dart';
import 'conversation_tree.dart';
import 'conversation_view.dart';
import 'theme.dart';
import 'widgets.dart';
import 'worker_details_panel.dart';
import 'workspace_settings.dart';

class WorkspaceShell extends StatefulWidget {
  const WorkspaceShell({
    super.key,
    required this.controller,
    required this.settings,
    this.credentials,
  });

  final WorkspaceController controller;
  final AppSettings settings;
  final CredentialStore? credentials;

  @override
  State<WorkspaceShell> createState() => _WorkspaceShellState();
}

class _WorkspaceShellState extends State<WorkspaceShell> {
  final GlobalKey<ScaffoldState> _scaffold = GlobalKey<ScaffoldState>();

  @override
  Widget build(BuildContext context) {
    return ListenableBuilder(
      listenable: widget.controller,
      builder: (context, _) => LayoutBuilder(builder: _layout),
    );
  }

  Widget _layout(BuildContext context, BoxConstraints box) {
    final controller = widget.controller;
    final p = ZatitiPalette.of(context);
    final width = box.maxWidth;
    final sidebarInline = width >= Measure.sidebarInline;
    final detailsInline = width >= Measure.detailsInline;
    final sidebarWidth = width >= Measure.largeWindow
        ? Measure.sidebarLarge
        : Measure.sidebar;
    final worker = controller.selectedWorker == null
        ? null
        : controller.snapshot.worker(controller.selectedWorker!);
    final showDetails = controller.detailsOpen && worker != null;

    Widget sidebar({VoidCallback? onOpened}) => _Sidebar(
      controller: controller,
      settings: widget.settings,
      credentials: widget.credentials,
      onOpened: onOpened,
    );

    final conversation = ConversationView(
      controller: controller,
      onOpenNavigation: sidebarInline
          ? null
          : () => _scaffold.currentState?.openDrawer(),
    );

    return Scaffold(
      key: _scaffold,
      backgroundColor: p.canvas,
      drawer: sidebarInline
          ? null
          : Drawer(
              width: Measure.sidebar,
              backgroundColor: p.sidebar,
              shape: const RoundedRectangleBorder(),
              child: sidebar(onOpened: () => Navigator.of(context).maybePop()),
            ),
      body: Column(
        children: [
          if (controller.source.kind == SourceKind.demo)
            _DemoBanner(controller: controller),
          Expanded(
            child: Stack(
              children: [
                Row(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    if (sidebarInline) ...[
                      SizedBox(width: sidebarWidth, child: sidebar()),
                      VerticalDivider(width: 1, color: p.line),
                    ],
                    Expanded(child: conversation),
                    if (showDetails && detailsInline) ...[
                      VerticalDivider(width: 1, color: p.line),
                      SizedBox(
                        width: Measure.details,
                        child: WorkerDetailsPanel(
                          controller: controller,
                          worker: worker,
                        ),
                      ),
                    ],
                  ],
                ),
                if (showDetails && !detailsInline) ...[
                  Positioned.fill(
                    child: Semantics(
                      label: 'Close details',
                      button: true,
                      child: GestureDetector(
                        onTap: controller.closeDetails,
                        child: const ColoredBox(color: Color(0x88000000)),
                      ),
                    ),
                  ),
                  Positioned(
                    top: 0,
                    right: 0,
                    bottom: 0,
                    width: width < Measure.details + 40
                        ? width
                        : Measure.details,
                    child: WorkerDetailsPanel(
                      controller: controller,
                      worker: worker,
                    ),
                  ),
                ],
              ],
            ),
          ),
        ],
      ),
    );
  }
}

/// Always visible with the demo source, so demo data cannot be mistaken for a
/// live controller.
class _DemoBanner extends StatelessWidget {
  const _DemoBanner({required this.controller});
  final WorkspaceController controller;

  @override
  Widget build(BuildContext context) {
    final p = ZatitiPalette.of(context);
    final text = Theme.of(context).textTheme;
    final source = controller.source;
    return Semantics(
      container: true,
      child: Container(
        key: const ValueKey('demo-banner'),
        width: double.infinity,
        color: p.amberWash,
        padding: const EdgeInsets.symmetric(horizontal: Space.xl, vertical: 6),
        child: Wrap(
          alignment: WrapAlignment.spaceBetween,
          crossAxisAlignment: WrapCrossAlignment.center,
          spacing: Space.lg,
          children: [
            Text(
              '${source.label}. Every record is fictional; nothing is sent '
              'anywhere.',
              style: text.bodySmall!.copyWith(
                color: p.amber,
                fontWeight: FontWeight.w600,
              ),
            ),
            if (source is DemoWorkspaceSource)
              TextButton(
                key: const ValueKey('demo-offline-toggle'),
                onPressed: () {
                  source.offline = !source.offline;
                  if (source.offline) {
                    controller.poll();
                  } else {
                    controller.reconnect();
                  }
                },
                child: Text(
                  source.offline
                      ? 'Demo: end simulated outage'
                      : 'Demo: simulate an outage',
                ),
              ),
          ],
        ),
      ),
    );
  }
}

class _Sidebar extends StatelessWidget {
  const _Sidebar({
    required this.controller,
    required this.settings,
    required this.credentials,
    this.onOpened,
  });

  final WorkspaceController controller;
  final AppSettings settings;
  final CredentialStore? credentials;
  final VoidCallback? onOpened;

  @override
  Widget build(BuildContext context) {
    final p = ZatitiPalette.of(context);
    final text = Theme.of(context).textTheme;
    final count = controller.needsYouCount;
    final filter = controller.filterRoot == null
        ? null
        : controller.snapshot.worker(controller.filterRoot!);
    return Material(
      color: p.sidebar,
      child: SafeArea(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(
                Space.xl,
                Space.xl,
                Space.lg,
                Space.lg,
              ),
              child: Semantics(
                header: true,
                child: Text(
                  'Zatiti',
                  style: text.titleLarge!.copyWith(fontSize: 19),
                ),
              ),
            ),
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: Space.md),
              child: Semantics(
                label: count == 0
                    ? 'Needs you, nothing waiting'
                    : 'Needs you, $count waiting',
                button: true,
                excludeSemantics: true,
                onTap: () => _showNeedsYou(context),
                child: InkWell(
                  key: const ValueKey('needs-you'),
                  borderRadius: BorderRadius.circular(9),
                  onTap: () => _showNeedsYou(context),
                  child: ConstrainedBox(
                    constraints: const BoxConstraints(minHeight: 48),
                    child: Padding(
                      padding: const EdgeInsets.symmetric(
                        horizontal: Space.md,
                        vertical: Space.sm,
                      ),
                      child: Row(
                        children: [
                          Icon(Icons.inbox_outlined, color: p.amber),
                          const SizedBox(width: Space.md),
                          Expanded(
                            child: Text('Needs you', style: text.titleSmall),
                          ),
                          Container(
                            padding: const EdgeInsets.symmetric(
                              horizontal: 7,
                              vertical: 3,
                            ),
                            decoration: BoxDecoration(
                              color: p.amberWash,
                              borderRadius: BorderRadius.circular(6),
                            ),
                            child: Text(
                              '$count',
                              key: const ValueKey('needs-you-count'),
                              style: text.labelMedium!.copyWith(color: p.amber),
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                ),
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(
                Space.lg,
                Space.sm,
                Space.lg,
                0,
              ),
              child: Align(
                alignment: Alignment.centerLeft,
                child: PopupMenuButton<String>(
                  key: const ValueKey('organization-filter'),
                  tooltip: 'Filter by organization',
                  onSelected: (id) {
                    for (final w in controller.filterOptions) {
                      if (w.id.value == id) return controller.setFilter(w.id);
                    }
                    controller.setFilter(null);
                  },
                  itemBuilder: (_) => [
                    const PopupMenuItem(value: '', child: Text('All chats')),
                    for (final w in controller.filterOptions)
                      PopupMenuItem(value: w.id.value, child: Text(w.ancestry)),
                  ],
                  child: Padding(
                    padding: const EdgeInsets.symmetric(
                      horizontal: Space.sm,
                      vertical: Space.md,
                    ),
                    child: Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Flexible(
                          child: Text(
                            filter?.ancestry ?? 'All chats',
                            overflow: TextOverflow.ellipsis,
                            style: text.bodySmall,
                          ),
                        ),
                        const Icon(Icons.expand_more, size: 15),
                      ],
                    ),
                  ),
                ),
              ),
            ),
            Expanded(
              child: OrganizationConversationTree(
                controller: controller,
                onOpened: onOpened,
              ),
            ),
            Divider(color: p.line),
            Semantics(
              label: 'Workspace settings',
              button: true,
              excludeSemantics: true,
              child: InkWell(
                key: const ValueKey('open-settings'),
                onTap: () => showWorkspaceSettings(
                  context,
                  controller: controller,
                  settings: settings,
                  credentials: credentials,
                ),
                child: Padding(
                  padding: const EdgeInsets.all(Space.lg),
                  child: Row(
                    children: [
                      const Icon(Icons.settings_outlined),
                      const SizedBox(width: Space.md),
                      Expanded(
                        child: Text(
                          controller.snapshot.workspaceName.isEmpty
                              ? 'Workspace'
                              : controller.snapshot.workspaceName,
                          maxLines: 2,
                          overflow: TextOverflow.ellipsis,
                          style: text.bodySmall,
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  void _showNeedsYou(BuildContext context) {
    showDialog<void>(
      context: context,
      barrierLabel: 'Close Needs you',
      builder: (_) => NeedsYouDialog(controller: controller),
    );
  }
}

/// Exact pending decisions across every organization.
class NeedsYouDialog extends StatelessWidget {
  const NeedsYouDialog({super.key, required this.controller});
  final WorkspaceController controller;

  @override
  Widget build(BuildContext context) {
    final text = Theme.of(context).textTheme;
    return ListenableBuilder(
      listenable: controller,
      builder: (context, _) {
        final items = controller.needsYou;
        return Dialog(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 520),
            child: Padding(
              padding: const EdgeInsets.all(28),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Expanded(
                        child: Semantics(
                          header: true,
                          namesRoute: true,
                          child: Text('Needs you', style: text.titleLarge),
                        ),
                      ),
                      IconButton(
                        tooltip: 'Close Needs you',
                        onPressed: () => Navigator.of(context).pop(),
                        icon: const Icon(Icons.close),
                      ),
                    ],
                  ),
                  const SizedBox(height: 6),
                  Text(
                    items.isEmpty
                        ? 'You’re all caught up.'
                        : items.length == 1
                        ? 'One decision across your workspace.'
                        : '${items.length} decisions across your workspace.',
                    style: text.bodySmall!.copyWith(fontSize: 13),
                  ),
                  const SizedBox(height: Space.xl),
                  if (items.isEmpty)
                    const EmptyState(
                      'No pending decisions. Your team can keep moving.',
                    ),
                  Flexible(
                    child: ListView(
                      shrinkWrap: true,
                      children: [
                        for (final d in items)
                          MiniCard(
                            title: d.review.title,
                            status: d.phase.label,
                            statusIcon: Icons.shield_outlined,
                            statusIsDecision: true,
                            body:
                                '${d.proposerName} · ${d.ancestry}\n'
                                '${d.review.destination} · up to '
                                '${d.review.costBound}',
                            actions: [
                              OutlinedButton(
                                key: ValueKey(
                                  'needs-open-${d.review.id.value}',
                                ),
                                onPressed: () {
                                  // The list closes first; the review opens
                                  // from the navigator that outlives it.
                                  final navigator = Navigator.of(context);
                                  navigator.pop();
                                  showActionReviewDialog(
                                    navigator.context,
                                    controller,
                                    d.review.id,
                                  );
                                },
                                child: const Text('Review & decide'),
                              ),
                            ],
                          ),
                      ],
                    ),
                  ),
                ],
              ),
            ),
          ),
        );
      },
    );
  }
}
